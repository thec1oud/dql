package mysqlsql

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/pingcap/tidb/pkg/parser"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/opcode"
	"github.com/pingcap/tidb/pkg/parser/test_driver"
	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/internal/pgsql"
)

// CheckColumn is a column a check constraint may test.
type CheckColumn struct {
	Type *irv1.Type
	// Collation is a text column's comparison facts; nil for other types.
	Collation *irv1.Collation
}

// Unescape undoes the escaping MySQL applies to the quotes of the
// expressions it prints in information_schema: CHECK_CLAUSE and
// GENERATION_EXPRESSION print 'a' as \'a\'.
func Unescape(text string) string {
	return strings.ReplaceAll(text, `\'`, `'`)
}

// RecognizeCheck reads a check constraint's clause, unescaped, and returns
// it as a portable kind when it is one, and as opaque native text otherwise
// (§10.1, §10.2, §10.4). The result's Columns are the columns the clause
// reads, in order of appearance; its name is the caller's to fill. An error
// means the clause does not parse.
//
// The forms are those of the Postgres recognizer, read under MySQL's rules:
// char_length and character_length count characters, length counts bytes
// (§10.3); regexp_like(x, 're', 'c') is a pattern, case sensitive by its
// match type; and text compares by its collation, so equality and not_empty
// are recognized only on a column whose collation is exact (Exact), and a
// range never on text.
func RecognizeCheck(clause string, columns map[string]CheckColumn) (*irv1.Constraint, error) {
	expr, err := parseExpr(clause)
	if err != nil {
		return nil, err
	}
	c := &irv1.Constraint{Kind: irv1.ConstraintKind_CONSTRAINT_KIND_CHECK, Columns: columnRefs(expr)}
	r := recognizer{columns: columns}
	if p := r.portable(expr); p != nil {
		c.Form = &irv1.Constraint_Portable{Portable: p}
		c.ClientValidated = true
	} else {
		c.Form = &irv1.Constraint_Opaque{Opaque: &irv1.NativeText{Native: clause}}
	}
	return c, nil
}

// parseExpr parses one expression.
func parseExpr(text string) (ast.ExprNode, error) {
	stmt, err := parser.New().ParseOneStmt("SELECT "+text, "", "")
	if err != nil {
		return nil, fmt.Errorf("%s does not parse: %w", text, err)
	}
	sel, ok := stmt.(*ast.SelectStmt)
	if !ok || sel.Fields == nil || len(sel.Fields.Fields) != 1 || sel.From != nil {
		return nil, fmt.Errorf("%s is not one expression", text)
	}
	return sel.Fields.Fields[0].Expr, nil
}

// columnRefs lists the columns an expression reads, each once, in order of
// appearance.
func columnRefs(e ast.ExprNode) []string {
	v := &refs{seen: map[string]bool{}}
	e.Accept(v)
	return v.out
}

type refs struct {
	seen map[string]bool
	out  []string
}

func (v *refs) Enter(n ast.Node) (ast.Node, bool) {
	if c, ok := n.(*ast.ColumnNameExpr); ok && !v.seen[c.Name.Name.O] {
		v.seen[c.Name.Name.O] = true
		v.out = append(v.out, c.Name.Name.O)
	}
	return n, false
}

func (v *refs) Leave(n ast.Node) (ast.Node, bool) { return n, true }

type recognizer struct {
	columns map[string]CheckColumn
}

// subject is what a comparison tests: a column, or its length in unit.
type subject struct {
	name   string
	col    CheckColumn
	length irv1.LengthUnit
}

// bound is a comparison with its subject on the left: subj op lit.
type bound struct {
	subj subject
	op   opcode.Op
	lit  ast.ExprNode
}

func unparen(e ast.ExprNode) ast.ExprNode {
	for {
		p, ok := e.(*ast.ParenthesesExpr)
		if !ok {
			return e
		}
		e = p.Expr
	}
}

func (r recognizer) portable(e ast.ExprNode) *irv1.PortableConstraint {
	switch e := unparen(e).(type) {
	case *ast.BinaryOperationExpr:
		if e.Op == opcode.LogicAnd {
			lo, ok1 := r.comparison(e.L)
			hi, ok2 := r.comparison(e.R)
			if !ok1 || !ok2 || lo.subj.name != hi.subj.name || lo.subj.length != hi.subj.length {
				return nil
			}
			return r.bounds([]bound{lo, hi})
		}
		return r.operator(e)
	case *ast.BetweenExpr:
		s, ok := r.subject(e.Expr)
		if !ok || e.Not {
			return nil
		}
		return r.bounds([]bound{{s, opcode.GE, e.Left}, {s, opcode.LE, e.Right}})
	case *ast.PatternInExpr:
		if e.Not || e.Sel != nil {
			return nil
		}
		return r.membership(e.Expr, e.List)
	case *ast.FuncCallExpr:
		return r.pattern(e)
	case *ast.PatternRegexpExpr:
		if e.Not {
			return nil
		}
		return r.regex(e.Expr, e.Pattern, false)
	}
	return nil
}

// caseSensitive is the inline flag that makes a pattern case sensitive in
// both MySQL's ICU and MariaDB's PCRE, whatever the column's collation.
const caseSensitive = "(?-i)"

// pattern recognizes regexp_like(x, 're', 'c') and regexp_like(x,
// '(?-i)re'), which MySQL prints for x REGEXP '(?-i)re'.
func (r recognizer) pattern(f *ast.FuncCallExpr) *irv1.PortableConstraint {
	if f.FnName.L != "regexp_like" || len(f.Args) < 2 || len(f.Args) > 3 {
		return nil
	}
	matchCase := false
	if len(f.Args) == 3 {
		mt, ok := stringOf(f.Args[2])
		if !ok || mt != "c" {
			return nil
		}
		matchCase = true
	}
	return r.regex(f.Args[0], f.Args[1], matchCase)
}

// regex recognizes a text column matched against a pattern, as a portable
// pattern when the match is case sensitive: by a match type of c, or by
// the pattern's leading (?-i), as MariaDB, which has no regexp_like, writes
// x REGEXP '(?-i)re'.
func (r recognizer) regex(x, pattern ast.ExprNode, matchCase bool) *irv1.PortableConstraint {
	s, ok := r.column(x)
	if !ok || family(s.col.Type) != famText {
		return nil
	}
	re, ok := stringOf(pattern)
	if !ok {
		return nil
	}
	if !matchCase {
		var found bool
		if re, found = strings.CutPrefix(re, caseSensitive); !found {
			return nil
		}
	}
	if !pgsql.PortableRegex(re) {
		return nil
	}
	return &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Pattern{Pattern: &irv1.PatternConstraint{Regex: re}}}
}

// operator recognizes a single comparison: not_empty, a one-value
// membership, or a one-sided length or range.
func (r recognizer) operator(e *ast.BinaryOperationExpr) *irv1.PortableConstraint {
	b, ok := r.comparison(e)
	if !ok {
		return nil
	}
	if b.subj.length == irv1.LengthUnit_LENGTH_UNIT_UNSPECIFIED {
		switch b.op {
		case opcode.NE:
			// x <> '' is not_empty: "length of at least 1, without trimming"
			// (§10.2), which a padding collation does not keep.
			if family(b.subj.col.Type) != famText || !Exact(b.subj.col.Collation) {
				return nil
			}
			if v, ok := stringOf(b.lit); !ok || v != "" {
				return nil
			}
			return &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_NotEmpty{NotEmpty: &irv1.NotEmptyConstraint{}}}
		case opcode.EQ:
			return r.members(b.subj, []ast.ExprNode{b.lit})
		}
	}
	return r.bounds([]bound{b})
}

var flipped = map[opcode.Op]opcode.Op{
	opcode.LT: opcode.GT, opcode.LE: opcode.GE, opcode.GT: opcode.LT, opcode.GE: opcode.LE,
	opcode.EQ: opcode.EQ, opcode.NE: opcode.NE,
}

// comparison reads x op a or a op x for a comparison operator.
func (r recognizer) comparison(e ast.ExprNode) (bound, bool) {
	b, ok := unparen(e).(*ast.BinaryOperationExpr)
	if !ok {
		return bound{}, false
	}
	if _, ok := flipped[b.Op]; !ok {
		return bound{}, false
	}
	if s, ok := r.subject(b.L); ok {
		return bound{s, b.Op, b.R}, true
	}
	if s, ok := r.subject(b.R); ok {
		return bound{s, flipped[b.Op], b.L}, true
	}
	return bound{}, false
}

// lengthUnits are the length functions and what they count (§10.3).
var lengthUnits = map[string]irv1.LengthUnit{
	"char_length":      irv1.LengthUnit_LENGTH_UNIT_CHARACTERS,
	"character_length": irv1.LengthUnit_LENGTH_UNIT_CHARACTERS,
	"length":           irv1.LengthUnit_LENGTH_UNIT_BYTES,
	"octet_length":     irv1.LengthUnit_LENGTH_UNIT_BYTES,
}

// subject reads a column or the length of one.
func (r recognizer) subject(e ast.ExprNode) (subject, bool) {
	if f, ok := unparen(e).(*ast.FuncCallExpr); ok {
		unit, ok := lengthUnits[f.FnName.L]
		if !ok || len(f.Args) != 1 {
			return subject{}, false
		}
		s, ok := r.column(f.Args[0])
		if !ok || family(s.col.Type) != famText {
			return subject{}, false
		}
		s.length = unit
		return s, true
	}
	return r.column(e)
}

func (r recognizer) column(e ast.ExprNode) (subject, bool) {
	c, ok := unparen(e).(*ast.ColumnNameExpr)
	if !ok || c.Name.Table.O != "" {
		return subject{}, false
	}
	col, ok := r.columns[c.Name.Name.O]
	if !ok {
		return subject{}, false
	}
	return subject{name: c.Name.Name.O, col: col}, true
}

// bounds turns one or two bounds on one subject into a length or a range.
func (r recognizer) bounds(bs []bound) *irv1.PortableConstraint {
	if bs[0].subj.length != irv1.LengthUnit_LENGTH_UNIT_UNSPECIFIED {
		return lengthOf(bs)
	}
	t := bs[0].subj.col.Type
	if !rangeFamilies[family(t)] {
		return nil
	}
	rc := &irv1.RangeConstraint{}
	for _, b := range bs {
		v, ok := literal(b.lit, t)
		if !ok {
			return nil
		}
		lower := b.op == opcode.GE || b.op == opcode.GT
		upper := b.op == opcode.LE || b.op == opcode.LT
		switch {
		case lower && rc.Min == nil:
			rc.Min, rc.MinInclusive = v, b.op == opcode.GE
		case upper && rc.Max == nil:
			rc.Max, rc.MaxInclusive = v, b.op == opcode.LE
		default:
			return nil
		}
	}
	return &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Range{Range: rc}}
}

func lengthOf(bs []bound) *irv1.PortableConstraint {
	lc := &irv1.LengthConstraint{Unit: bs[0].subj.length}
	set := func(dst **uint32, v int64) bool {
		if *dst != nil || v < 0 || v > math.MaxUint32 {
			return false
		}
		*dst = proto.Uint32(uint32(v))
		return true
	}
	for _, b := range bs {
		v, ok := literal(b.lit, &irv1.Type{Kind: &irv1.Type_Int64{Int64: &irv1.Empty{}}})
		if !ok {
			return nil
		}
		n := v.GetInt()
		switch b.op {
		case opcode.GE:
			ok = set(&lc.Min, n)
		case opcode.GT:
			ok = set(&lc.Min, n+1)
		case opcode.LE:
			ok = set(&lc.Max, n)
		case opcode.LT:
			ok = set(&lc.Max, n-1)
		case opcode.EQ:
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

// membership recognizes subject = one of items. Text compares by its
// collation, so only an exact one keeps equality's meaning.
func (r recognizer) membership(subj ast.ExprNode, items []ast.ExprNode) *irv1.PortableConstraint {
	s, ok := r.column(subj)
	if !ok {
		return nil
	}
	return r.members(s, items)
}

func (r recognizer) members(s subject, items []ast.ExprNode) *irv1.PortableConstraint {
	f := family(s.col.Type)
	if !membershipFamilies[f] || f == famText && !Exact(s.col.Collation) {
		return nil
	}
	mc := &irv1.MembershipConstraint{}
	for _, it := range items {
		v, ok := literal(it, s.col.Type)
		if !ok {
			return nil
		}
		mc.Values = append(mc.Values, v)
	}
	return &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Membership{Membership: mc}}
}

type fam int

const (
	famNone fam = iota
	famInt
	famUint
	famDecimal
	famFloat
	famText
	famDate
	famLocalDatetime
)

// family classifies a subject's logical type. char is left out, as on
// Postgres: MySQL strips its trailing spaces. Enums are left out because
// MySQL compares their labels by the collation, and a check on an enum
// column restates its labels. instant is left out: a constant without an
// offset depends on the session's time zone.
func family(t *irv1.Type) fam {
	switch t.GetKind().(type) {
	case *irv1.Type_Int8, *irv1.Type_Int16, *irv1.Type_Int32, *irv1.Type_Int64:
		return famInt
	case *irv1.Type_Uint8, *irv1.Type_Uint16, *irv1.Type_Uint32, *irv1.Type_Uint64:
		return famUint
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
	}
	return famNone
}

var rangeFamilies = map[fam]bool{famInt: true, famUint: true, famDecimal: true, famFloat: true, famDate: true, famLocalDatetime: true}

var membershipFamilies = map[fam]bool{famInt: true, famUint: true, famDecimal: true, famText: true}

// stringOf reads a string constant, with or without a character set
// introducer such as _latin1'a'.
func stringOf(e ast.ExprNode) (string, bool) {
	v, ok := unparen(e).(*test_driver.ValueExpr)
	if !ok || v.Kind() != test_driver.KindString {
		return "", false
	}
	return v.GetString(), true
}

// literal reads a constant as a value of typ. Numbers are read exactly: a
// decimal stays text (§8.7).
func literal(e ast.ExprNode, typ *irv1.Type) (*irv1.Value, bool) {
	neg := false
	if u, ok := unparen(e).(*ast.UnaryOperationExpr); ok && u.Op == opcode.Minus {
		neg, e = true, u.V
	}
	v, ok := unparen(e).(*test_driver.ValueExpr)
	if !ok {
		return nil, false
	}
	var text string
	numeric := true
	switch v.Kind() {
	case test_driver.KindInt64:
		text = strconv.FormatInt(v.GetInt64(), 10)
	case test_driver.KindUint64:
		text = strconv.FormatUint(v.GetUint64(), 10)
	case test_driver.KindMysqlDecimal:
		text = v.GetMysqlDecimal().String()
	case test_driver.KindFloat64:
		text = strconv.FormatFloat(v.GetFloat64(), 'g', -1, 64)
	case test_driver.KindString:
		text, numeric = v.GetString(), false
	default:
		return nil, false
	}
	if neg {
		if !numeric || strings.HasPrefix(text, "-") {
			return nil, false
		}
		text = "-" + text
	}
	switch family(typ) {
	case famInt:
		n, err := strconv.ParseInt(text, 10, 64)
		return &irv1.Value{Kind: &irv1.Value_Int{Int: n}}, err == nil && numeric
	case famUint:
		n, err := strconv.ParseUint(text, 10, 64)
		return &irv1.Value{Kind: &irv1.Value_Uint{Uint: n}}, err == nil && numeric
	case famDecimal:
		return &irv1.Value{Kind: &irv1.Value_Decimal{Decimal: text}}, numeric && decimalText.MatchString(text)
	case famFloat:
		f, err := strconv.ParseFloat(text, 64)
		return &irv1.Value{Kind: &irv1.Value_Float{Float: f}}, err == nil && numeric && decimalText.MatchString(text)
	case famText:
		return &irv1.Value{Kind: &irv1.Value_String_{String_: text}}, !numeric
	case famDate:
		return &irv1.Value{Kind: &irv1.Value_String_{String_: text}}, !numeric && dateText.MatchString(text)
	default: // famLocalDatetime; callers check the family first.
		return &irv1.Value{Kind: &irv1.Value_String_{String_: text}}, !numeric && localDatetimeText.MatchString(text)
	}
}
