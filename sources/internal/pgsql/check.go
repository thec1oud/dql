package pgsql

import (
	"math"
	"regexp"
	"sort"
	"strconv"

	pg "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// RecognizeCheck reads a table's check constraint and returns it as a
// portable kind when it is one, and as opaque native text otherwise (§10.1,
// §10.2, §10.4). text is either the constraint as pg_get_constraintdef prints
// it ("CHECK (...)", with the casts Postgres adds, such as
// 'draft'::app.post_status) or the bare expression as dpg writes it; an opaque
// result keeps text exactly as given. columns gives the logical type of each
// column by name; recognition needs the type of the column it tests, so a
// column of a domain type is recognized only if its base type is passed.
//
// The result's Columns are the columns the expression reads, in order of
// appearance. Name, annotations and extensions are the caller's to fill. An
// error means text does not parse as a check constraint.
//
// Recognized forms, for a column x and constants a, b, n, where length is
// char_length, character_length or length (all count characters for text,
// §10.3):
//
//	length(x) <= n, < n, >= n, > n, = n      length            (text, varchar)
//	length(x) BETWEEN a AND b, or two bounds length
//	x <> ''                                  not_empty         (text, varchar)
//	x ~ 'regex'                              pattern           (text, varchar)
//	x >= a, x > a, x <= b, x < b, or one     range             (numbers, date,
//	lower and one upper bound, BETWEEN                          timestamp, time)
//	x = a, x IN (...), x = ANY (ARRAY[...])  membership        (integers,
//	                                                            numeric, text,
//	                                                            varchar, enums)
//
// Anything else, including any combination with OR or NOT, is opaque.
func RecognizeCheck(text string, columns map[string]*irv1.Type) (*irv1.Constraint, error) {
	return recognizer{types: columns}.recognize(text)
}

// RecognizeDomainCheck reads a domain's check constraint, whose subject is
// VALUE, against the domain's base type (§8.5). Its Columns are empty.
func RecognizeDomainCheck(text string, base *irv1.Type) (*irv1.Constraint, error) {
	return recognizer{domain: true, types: map[string]*irv1.Type{"value": base}}.recognize(text)
}

// SingleColumnMembership reports whether c tests one column's membership in
// a list of values: the only check an enum annotation accepts (§8.4).
func SingleColumnMembership(c *irv1.Constraint) bool {
	return c.GetPortable().GetMembership() != nil && len(c.GetColumns()) == 1
}

type recognizer struct {
	domain bool
	types  map[string]*irv1.Type
}

func (r recognizer) recognize(text string) (*irv1.Constraint, error) {
	expr, err := parseCheck(text)
	if err != nil {
		return nil, err
	}
	c := &irv1.Constraint{Kind: irv1.ConstraintKind_CONSTRAINT_KIND_CHECK}
	if !r.domain {
		c.Columns = columnRefs(expr)
	}
	if p := r.portable(expr); p != nil {
		c.Form = &irv1.Constraint_Portable{Portable: p}
		c.ClientValidated = true
	} else {
		c.Form = &irv1.Constraint_Opaque{Opaque: &irv1.NativeText{Native: text}}
	}
	return c, nil
}

// columnRefs lists the columns an expression reads, in order of appearance.
func columnRefs(expr *pg.Node) []string {
	var refs []*pg.ColumnRef
	walk(expr.ProtoReflect(), func(m protoreflect.Message) {
		if c, ok := m.Interface().(*pg.ColumnRef); ok {
			refs = append(refs, c)
		}
	})
	// protoreflect visits fields in no fixed order; locations fix it.
	sort.SliceStable(refs, func(i, j int) bool { return refs[i].Location < refs[j].Location })
	var out []string
	seen := map[string]bool{}
	for _, c := range refs {
		parts, ok := names(c.Fields)
		if !ok || seen[parts[len(parts)-1]] {
			continue
		}
		seen[parts[len(parts)-1]] = true
		out = append(out, parts[len(parts)-1])
	}
	return out
}

// walk calls fn on m and every message below it.
func walk(m protoreflect.Message, fn func(protoreflect.Message)) {
	fn(m)
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case fd.Message() == nil:
		case fd.IsList():
			for i := range v.List().Len() {
				walk(v.List().Get(i).Message(), fn)
			}
		default:
			walk(v.Message(), fn)
		}
		return true
	})
}

// portable returns the portable kind expr is, or nil.
func (r recognizer) portable(expr *pg.Node) *irv1.PortableConstraint {
	if b := expr.GetBoolExpr(); b != nil {
		if b.Boolop != pg.BoolExprType_AND_EXPR || len(b.Args) != 2 {
			return nil
		}
		lo, ok1 := r.comparison(b.Args[0].GetAExpr())
		hi, ok2 := r.comparison(b.Args[1].GetAExpr())
		if !ok1 || !ok2 || lo.subj.name != hi.subj.name || lo.subj.length != hi.subj.length {
			return nil
		}
		return r.bounds([]bound{lo, hi})
	}
	e := expr.GetAExpr()
	switch e.GetKind() {
	case pg.A_Expr_Kind_AEXPR_OP:
		return r.operator(e)
	case pg.A_Expr_Kind_AEXPR_BETWEEN:
		s, ok := r.subject(e.Lexpr)
		items := e.Rexpr.GetList().GetItems()
		if !ok {
			return nil
		}
		return r.bounds([]bound{{s, ">=", items[0], e.Lexpr}, {s, "<=", items[1], e.Lexpr}})
	case pg.A_Expr_Kind_AEXPR_IN:
		if opName(e) != "=" {
			return nil
		}
		return r.membership(e.Lexpr, e.Rexpr.GetList().GetItems())
	case pg.A_Expr_Kind_AEXPR_OP_ANY:
		if opName(e) != "=" {
			return nil
		}
		s, ok := r.subject(e.Lexpr)
		if !ok {
			return nil
		}
		arr := e.Rexpr
		for arr.GetTypeCast() != nil && castAllowed(arr.GetTypeCast().TypeName, s.typ, true) {
			arr = arr.GetTypeCast().Arg
		}
		if arr.GetAArrayExpr() == nil {
			return nil
		}
		return r.membership(e.Lexpr, arr.GetAArrayExpr().Elements)
	}
	return nil
}

// operator recognizes a single binary operator: a pattern, not_empty, a
// one-value membership or a one-sided length or range.
func (r recognizer) operator(e *pg.A_Expr) *irv1.PortableConstraint {
	switch opName(e) {
	case "~":
		s, ok := r.subject(e.Lexpr)
		if !ok || s.length || family(s.typ) != famText {
			return nil
		}
		v, ok := literal(e.Rexpr, s.typ)
		if !ok || !portableRegex(v.GetString_()) {
			return nil
		}
		return &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Pattern{Pattern: &irv1.PatternConstraint{Regex: v.GetString_()}}}
	case "<>":
		// x <> '' is not_empty: "length of at least 1, without trimming"
		// (§10.2). A length bound such as char_length(x) > 0 stays a length.
		b, ok := r.comparison(e)
		if !ok || b.subj.length || family(b.subj.typ) != famText {
			return nil
		}
		if v, ok := literal(b.lit, b.subj.typ); !ok || v.GetString_() != "" {
			return nil
		}
		return &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_NotEmpty{NotEmpty: &irv1.NotEmptyConstraint{}}}
	case "=":
		b, ok := r.comparison(e)
		if ok && !b.subj.length {
			return r.membership(b.subjNode, []*pg.Node{b.lit})
		}
	}
	b, ok := r.comparison(e)
	if !ok {
		return nil
	}
	return r.bounds([]bound{b})
}

func opName(e *pg.A_Expr) string {
	if parts, ok := names(e.GetName()); ok && len(parts) == 1 {
		return parts[0]
	}
	return ""
}

// subject is what a comparison tests: a column (or VALUE), or its length.
type subject struct {
	name   string
	typ    *irv1.Type
	length bool
}

// bound is a comparison with its subject on the left: subj op lit.
type bound struct {
	subj     subject
	op       string
	lit      *pg.Node
	subjNode *pg.Node
}

var flipped = map[string]string{"<": ">", "<=": ">=", ">": "<", ">=": "<=", "=": "=", "<>": "<>"}

// comparison reads x op a or a op x for a comparison operator.
func (r recognizer) comparison(e *pg.A_Expr) (bound, bool) {
	op := opName(e)
	if e.GetKind() != pg.A_Expr_Kind_AEXPR_OP || flipped[op] == "" {
		return bound{}, false
	}
	if s, ok := r.subject(e.Lexpr); ok {
		return bound{s, op, e.Rexpr, e.Lexpr}, true
	}
	if s, ok := r.subject(e.Rexpr); ok {
		return bound{s, flipped[op], e.Lexpr, e.Rexpr}, true
	}
	return bound{}, false
}

// lengthFuncs count characters for text and varchar in Postgres (§10.3).
var lengthFuncs = map[string]bool{"char_length": true, "character_length": true, "length": true}

// subject reads a column, VALUE, or the length of one.
func (r recognizer) subject(n *pg.Node) (subject, bool) {
	if f := n.GetFuncCall(); f != nil {
		fn, _ := names(f.Funcname)
		if len(fn) == 2 && fn[0] == "pg_catalog" {
			fn = fn[1:]
		}
		plain := len(f.Args) == 1 && len(f.AggOrder) == 0 && f.AggFilter == nil && f.Over == nil &&
			!f.AggStar && !f.AggDistinct && !f.FuncVariadic && !f.AggWithinGroup
		if len(fn) != 1 || !lengthFuncs[fn[0]] || !plain {
			return subject{}, false
		}
		s, ok := r.column(f.Args[0])
		if !ok || family(s.typ) != famText {
			return subject{}, false
		}
		s.length = true
		return s, true
	}
	return r.column(n)
}

// column reads a column reference, or VALUE in a domain check. Postgres
// prints a varchar column compared with text as (x)::text; such casts between
// the text types are dropped, because they do not change the value.
func (r recognizer) column(n *pg.Node) (subject, bool) {
	cast := false
	for n.GetTypeCast() != nil && castAllowed(n.GetTypeCast().TypeName, &irv1.Type{Kind: &irv1.Type_Text{}}, false) {
		n, cast = n.GetTypeCast().Arg, true
	}
	parts, ok := names(n.GetColumnRef().GetFields())
	if !ok || len(parts) == 0 || r.domain && (len(parts) != 1 || parts[0] != "value") {
		return subject{}, false
	}
	name := parts[len(parts)-1]
	typ, ok := r.types[name]
	if !ok || cast && family(typ) != famText {
		return subject{}, false
	}
	return subject{name: name, typ: typ}, true
}

// bounds turns one or two bounds on one subject into a length or a range.
// Each of min and max may be set once.
func (r recognizer) bounds(bs []bound) *irv1.PortableConstraint {
	if bs[0].subj.length {
		return lengthOf(bs)
	}
	fam := family(bs[0].subj.typ)
	if !rangeFamilies[fam] {
		return nil
	}
	rc := &irv1.RangeConstraint{}
	for _, b := range bs {
		v, ok := literal(b.lit, b.subj.typ)
		if !ok {
			return nil
		}
		lower := b.op == ">=" || b.op == ">"
		upper := b.op == "<=" || b.op == "<"
		switch {
		case lower && rc.Min == nil:
			rc.Min, rc.MinInclusive = v, b.op == ">="
		case upper && rc.Max == nil:
			rc.Max, rc.MaxInclusive = v, b.op == "<="
		default:
			return nil
		}
	}
	return &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Range{Range: rc}}
}

func lengthOf(bs []bound) *irv1.PortableConstraint {
	lc := &irv1.LengthConstraint{Unit: irv1.LengthUnit_LENGTH_UNIT_CHARACTERS}
	set := func(dst **uint32, v int64) bool {
		if *dst != nil || v < 0 || v > math.MaxUint32 {
			return false
		}
		*dst = proto.Uint32(uint32(v))
		return true
	}
	for _, b := range bs {
		v, ok := literal(b.lit, int64Type())
		if !ok {
			return nil
		}
		n := v.GetInt()
		switch b.op {
		case ">=":
			ok = set(&lc.Min, n)
		case ">":
			ok = set(&lc.Min, n+1)
		case "<=":
			ok = set(&lc.Max, n)
		case "<":
			ok = set(&lc.Max, n-1)
		case "=":
			ok = set(&lc.Min, n) && set(&lc.Max, n)
		default:
			ok = false
		}
		if !ok {
			return nil
		}
	}
	return &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Length{Length: lc}}
}

// membership recognizes subject = one of items.
func (r recognizer) membership(subj *pg.Node, items []*pg.Node) *irv1.PortableConstraint {
	s, ok := r.column(subj)
	if !ok || !membershipFamilies[family(s.typ)] {
		return nil
	}
	mc := &irv1.MembershipConstraint{}
	for _, it := range items {
		v, ok := literal(it, s.typ)
		if !ok {
			return nil
		}
		mc.Values = append(mc.Values, v)
	}
	return &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Membership{Membership: mc}}
}

// fam groups logical types by how their constants are read.
type fam int

const (
	famNone fam = iota
	famInt
	famDecimal
	famFloat
	famText
	famEnum
	famDate
	famLocalDatetime
	famLocalTime
)

// family classifies a subject's logical type. char is left out: Postgres
// compares and measures it without trailing spaces, which portable kinds do
// not (§10.2). instant and time_with_offset are left out because a constant
// without an offset depends on the session's time zone.
func family(t *irv1.Type) fam {
	switch t.GetKind().(type) {
	case *irv1.Type_Int16, *irv1.Type_Int32, *irv1.Type_Int64:
		return famInt
	case *irv1.Type_Decimal:
		return famDecimal
	case *irv1.Type_Float32, *irv1.Type_Float64:
		return famFloat
	case *irv1.Type_Text, *irv1.Type_Varchar:
		return famText
	case *irv1.Type_Date:
		return famDate
	case *irv1.Type_LocalDatetime:
		return famLocalDatetime
	case *irv1.Type_LocalTime:
		return famLocalTime
	}
	if t.GetNamed().GetEnum() != nil {
		return famEnum
	}
	return famNone
}

// rangeFamilies are compared "in the logical type: numbers, dates, times"
// (§10.2). Text is not: its order depends on the collation.
var rangeFamilies = map[fam]bool{famInt: true, famDecimal: true, famFloat: true, famDate: true, famLocalDatetime: true, famLocalTime: true}

// membershipFamilies compare by exact equality in every host.
var membershipFamilies = map[fam]bool{famInt: true, famDecimal: true, famText: true, famEnum: true}

// castTargets are the pg_catalog types a constant of each family may be cast
// to without changing its value.
var castTargets = map[fam]map[string]bool{
	famInt:           {"int2": true, "int4": true, "int8": true},
	famDecimal:       {"numeric": true},
	famFloat:         {"float4": true, "float8": true},
	famText:          {"text": true, "varchar": true},
	famDate:          {"date": true},
	famLocalDatetime: {"timestamp": true},
	famLocalTime:     {"time": true},
}

// castAllowed reports whether a cast to tn keeps a value of typ unchanged: a
// cast to the type itself or a sibling without modifiers (which would
// truncate or round), and for array the same as an array type.
func castAllowed(tn *pg.TypeName, typ *irv1.Type, array bool) bool {
	if len(tn.Typmods) > 0 || (len(tn.ArrayBounds) > 0) != array {
		return false
	}
	if ref := typ.GetNamed().GetEnum(); ref != nil {
		parts, _ := names(tn.Names)
		return len(parts) == 2 && parts[0] == ref.Namespace && parts[1] == ref.Object ||
			len(parts) == 1 && parts[0] == ref.Object
	}
	name, ok := builtinName(tn)
	return ok && castTargets[family(typ)][name]
}

var (
	decimalText       = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)
	dateText          = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	timeText          = `[0-9]{2}:[0-9]{2}(:[0-9]{2}(\.[0-9]{1,6})?)?`
	localDatetimeText = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}[ T]` + timeText + `$`)
	localTimeText     = regexp.MustCompile(`^` + timeText + `$`)
)

// literal reads a constant, possibly cast, as a value of typ. Numbers are read
// exactly: a decimal stays text (§8.7). Date and time constants must be plain
// ISO values; special inputs such as 'infinity' or 'now' are not recognized.
func literal(n *pg.Node, typ *irv1.Type) (*irv1.Value, bool) {
	for n.GetTypeCast() != nil {
		if !castAllowed(n.GetTypeCast().TypeName, typ, false) {
			return nil, false
		}
		n = n.GetTypeCast().Arg
	}
	c := n.GetAConst()
	if c == nil || c.Isnull {
		return nil, false
	}
	var text string
	switch {
	case c.GetIval() != nil:
		text = strconv.FormatInt(int64(c.GetIval().Ival), 10)
	case c.GetFval() != nil:
		text = c.GetFval().Fval
	case c.GetSval() != nil:
		text = c.GetSval().Sval
	default:
		return nil, false
	}
	numeric := c.GetSval() == nil
	switch family(typ) {
	case famInt:
		v, err := strconv.ParseInt(text, 10, 64)
		return &irv1.Value{Kind: &irv1.Value_Int{Int: v}}, err == nil
	case famDecimal:
		return &irv1.Value{Kind: &irv1.Value_Decimal{Decimal: text}}, decimalText.MatchString(text)
	case famFloat:
		v, err := strconv.ParseFloat(text, 64)
		return &irv1.Value{Kind: &irv1.Value_Float{Float: v}}, err == nil && decimalText.MatchString(text)
	case famText, famEnum:
		return &irv1.Value{Kind: &irv1.Value_String_{String_: text}}, !numeric
	case famDate:
		return &irv1.Value{Kind: &irv1.Value_String_{String_: text}}, !numeric && dateText.MatchString(text)
	case famLocalDatetime:
		return &irv1.Value{Kind: &irv1.Value_String_{String_: text}}, !numeric && localDatetimeText.MatchString(text)
	default: // famLocalTime; callers check the family first.
		return &irv1.Value{Kind: &irv1.Value_String_{String_: text}}, !numeric && localTimeText.MatchString(text)
	}
}
