package mssqlsql

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// CheckColumn is a column a check may test.
type CheckColumn struct {
	// System is the column's system type's name, lowercased, such as
	// nvarchar, whatever alias type it was declared with.
	System string
	Type   *irv1.Type
	// Collation is a text column's; nil for other types.
	Collation *irv1.Collation
}

// RecognizeCheck returns a check constraint as a portable kind when it is
// one, and as opaque native text otherwise (§10.1, §10.2, §10.4). Its
// Columns are the columns it reads; its name is the caller's to fill. An
// error means the definition does not parse.
//
// The forms are those of the Postgres recognizer, read under SQL Server's
// rules. LEN ignores trailing spaces, so no length is read from it;
// DATALENGTH counts bytes, which is two per UTF-16 code unit of an
// nvarchar or ntext value and a UTF-8 byte of a varchar or text value
// under a UTF-8 collation (§10.3). Every collation ignores trailing
// spaces, so not_empty is never portable and a range never on text. Text
// membership is: the client accepts exactly the listed values, which the
// server accepts too.
//
// SQL Server stores x IN (a, b, c) as (x = c OR x = b OR x = a): an OR
// chain of equalities on one column is read as a membership with its
// values reversed, back in declared order.
func RecognizeCheck(definition string, columns map[string]CheckColumn) (*irv1.Constraint, error) {
	e, err := ParseCondition(definition)
	if err != nil {
		return nil, err
	}
	out := &irv1.Constraint{Kind: irv1.ConstraintKind_CONSTRAINT_KIND_CHECK, Columns: References(e)}
	if p := (recognizer{columns}).portable(e); p != nil {
		out.Form = &irv1.Constraint_Portable{Portable: p}
		out.ClientValidated = true
	} else {
		out.Form = &irv1.Constraint_Opaque{Opaque: &irv1.NativeText{Native: definition}}
	}
	return out, nil
}

type recognizer struct {
	columns map[string]CheckColumn
}

// subject is what a comparison tests: a column, or its length in unit,
// counting bytes per unit.
type subject struct {
	name   string
	col    CheckColumn
	length irv1.LengthUnit
	per    int64
}

// bound is a comparison with its subject on the left: subj op lit.
type bound struct {
	subj subject
	op   string
	lit  Expr
}

func (r recognizer) portable(e Expr) *irv1.PortableConstraint {
	switch e := e.(type) {
	case Bin:
		switch e.Op {
		case "AND":
			lo, ok1 := r.comparison(e.L)
			hi, ok2 := r.comparison(e.R)
			if !ok1 || !ok2 || lo.subj.name != hi.subj.name || lo.subj.length != hi.subj.length {
				return nil
			}
			return r.bounds([]bound{lo, hi})
		case "OR":
			return r.chain(e)
		case "=":
			b, ok := r.comparison(e)
			if !ok || b.subj.length != irv1.LengthUnit_LENGTH_UNIT_UNSPECIFIED {
				break
			}
			return r.members(b.subj, []Expr{b.lit})
		}
		b, ok := r.comparison(e)
		if !ok {
			return nil
		}
		return r.bounds([]bound{b})
	case Between:
		s, ok := r.subject(e.X)
		if !ok || e.Not {
			return nil
		}
		return r.bounds([]bound{{s, ">=", e.Lo}, {s, "<=", e.Hi}})
	case In:
		s, ok := r.column(e.X)
		if !ok || e.Not {
			return nil
		}
		return r.members(s, e.Items)
	}
	return nil
}

// chain reads an OR chain of equalities on one column, as SQL Server
// stores an IN list, reversed.
func (r recognizer) chain(e Bin) *irv1.PortableConstraint {
	var items []Expr
	var subj subject
	var walk func(x Expr) bool
	walk = func(x Expr) bool {
		if b, ok := x.(Bin); ok && b.Op == "OR" {
			return walk(b.L) && walk(b.R)
		}
		b, ok := r.comparison(x)
		if !ok || b.op != "=" || b.subj.length != irv1.LengthUnit_LENGTH_UNIT_UNSPECIFIED || items != nil && b.subj.name != subj.name {
			return false
		}
		subj = b.subj
		items = append([]Expr{b.lit}, items...)
		return true
	}
	if !walk(e) {
		return nil
	}
	return r.members(subj, items)
}

var flipped = map[string]string{"<": ">", "<=": ">=", ">": "<", ">=": "<=", "=": "=", "<>": "<>"}

// comparison reads x op a or a op x for a comparison operator.
func (r recognizer) comparison(e Expr) (bound, bool) {
	b, ok := e.(Bin)
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

// subject reads a column or its DATALENGTH, in the unit the column's
// bytes count (§10.3).
func (r recognizer) subject(e Expr) (subject, bool) {
	if f, ok := e.(Call); ok {
		if f.Name != "datalength" || len(f.Args) != 1 {
			return subject{}, false
		}
		s, ok := r.column(f.Args[0])
		if !ok {
			return subject{}, false
		}
		switch s.col.System {
		case "nvarchar", "ntext":
			s.length, s.per = irv1.LengthUnit_LENGTH_UNIT_UTF16_CODE_UNITS, 2
		case "varchar", "text":
			if !strings.Contains(strings.ToUpper(s.col.Collation.GetName()), "_UTF8") {
				return subject{}, false
			}
			s.length, s.per = irv1.LengthUnit_LENGTH_UNIT_BYTES, 1
		default:
			return subject{}, false
		}
		return s, true
	}
	return r.column(e)
}

func (r recognizer) column(e Expr) (subject, bool) {
	c, ok := e.(Col)
	if !ok {
		return subject{}, false
	}
	col, ok := r.columns[c.Name]
	if !ok || family(col.Type) == famNone {
		return subject{}, false
	}
	return subject{name: c.Name, col: col}, true
}

// bounds turns one or two bounds on one subject into a length or a range.
func (r recognizer) bounds(bs []bound) *irv1.PortableConstraint {
	if bs[0].subj.length != irv1.LengthUnit_LENGTH_UNIT_UNSPECIFIED {
		return lengthOf(bs)
	}
	t := bs[0].subj.col.Type
	if family(t) == famText {
		return nil
	}
	rc := &irv1.RangeConstraint{}
	for _, b := range bs {
		v, ok := literalOf(b.lit, t)
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

// lengthOf turns bounds on a byte count into bounds on units of per bytes:
// at least n bytes is at least ceil(n/per) units, at most n bytes at most
// floor(n/per) units, and exactly n bytes is exactly n/per units, which
// must be whole.
func lengthOf(bs []bound) *irv1.PortableConstraint {
	per := bs[0].subj.per
	lc := &irv1.LengthConstraint{Unit: bs[0].subj.length}
	set := func(dst **uint32, v int64) bool {
		if *dst != nil || v < 0 || v > math.MaxUint32 {
			return false
		}
		*dst = proto.Uint32(uint32(v))
		return true
	}
	ceil := func(n int64) int64 { return (n + per - 1) / per }
	for _, b := range bs {
		v, ok := literalOf(b.lit, &irv1.Type{Kind: &irv1.Type_Int64{Int64: &irv1.Empty{}}})
		if !ok {
			return nil
		}
		n := v.GetInt()
		switch b.op {
		case ">=":
			ok = set(&lc.Min, ceil(n))
		case ">":
			ok = set(&lc.Min, ceil(n+1))
		case "<=":
			ok = n >= 0 && set(&lc.Max, n/per)
		case "<":
			ok = n >= 1 && set(&lc.Max, (n-1)/per)
		case "=":
			ok = n%per == 0 && set(&lc.Min, n/per) && set(&lc.Max, n/per)
		default:
			ok = false
		}
		if !ok {
			return nil
		}
	}
	return &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Length{Length: lc}}
}

// members recognizes subject = one of items. A fixed-length char or nchar
// column holds its values padded, so it is never one.
func (r recognizer) members(s subject, items []Expr) *irv1.PortableConstraint {
	if s.col.Type.GetChar() != nil {
		return nil
	}
	mc := &irv1.MembershipConstraint{}
	for _, it := range items {
		v, ok := literalOf(it, s.col.Type)
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
	famDecimal
	famFloat
	famText
)

// family classifies a subject's logical type.
func family(t *irv1.Type) fam {
	switch t.GetKind().(type) {
	case *irv1.Type_Uint8, *irv1.Type_Int16, *irv1.Type_Int32, *irv1.Type_Int64:
		return famInt
	case *irv1.Type_Decimal:
		return famDecimal
	case *irv1.Type_Float32, *irv1.Type_Float64:
		return famFloat
	case *irv1.Type_Text, *irv1.Type_Varchar, *irv1.Type_Char:
		return famText
	}
	return famNone
}

var plainDecimal = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

// literalOf reads a constant as a value of typ. Numbers are read exactly: a
// decimal stays text (§8.7).
func literalOf(e Expr, typ *irv1.Type) (*irv1.Value, bool) {
	neg := false
	if u, ok := e.(Neg); ok && u.Op == "-" {
		neg, e = true, u.X
	}
	l, ok := e.(Lit)
	if !ok || l.Kind == LitNull || neg && l.Kind != LitNumber {
		return nil, false
	}
	text := l.Text
	if neg {
		text = "-" + text
	}
	numeric := l.Kind == LitNumber
	switch family(typ) {
	case famInt:
		n, err := strconv.ParseInt(text, 10, 64)
		return &irv1.Value{Kind: &irv1.Value_Int{Int: n}}, err == nil && numeric
	case famDecimal:
		return &irv1.Value{Kind: &irv1.Value_Decimal{Decimal: text}}, numeric && plainDecimal.MatchString(text)
	case famFloat:
		f, err := strconv.ParseFloat(text, 64)
		return &irv1.Value{Kind: &irv1.Value_Float{Float: f}}, err == nil && numeric && plainDecimal.MatchString(text)
	default: // famText; column admits no other family.
		return &irv1.Value{Kind: &irv1.Value_String_{String_: text}}, !numeric
	}
}
