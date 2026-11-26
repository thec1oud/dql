package sqlitesql

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Collation returns the comparison facts of a SQLite collation (§8.9):
// BINARY compares bytes, NOCASE folds ASCII letters only, and RTRIM
// ignores trailing spaces. An application's own collation is unknown.
func Collation(name string) *irv1.Collation {
	t, f := irv1.Tristate_TRISTATE_TRUE, irv1.Tristate_TRISTATE_FALSE
	switch strings.ToUpper(name) {
	case "", "BINARY":
		return &irv1.Collation{Name: "BINARY", CaseSensitive: t, AccentSensitive: t, PadSpace: f, Deterministic: t, ClientFold: irv1.ClientFold_CLIENT_FOLD_NONE}
	case "NOCASE":
		return &irv1.Collation{Name: "NOCASE", CaseSensitive: f, AccentSensitive: t, PadSpace: f, Deterministic: f, ClientFold: irv1.ClientFold_CLIENT_FOLD_ASCII_LOWER}
	case "RTRIM":
		return &irv1.Collation{Name: "RTRIM", CaseSensitive: t, AccentSensitive: t, PadSpace: t, Deterministic: f}
	}
	return &irv1.Collation{Name: name}
}

// exact reports whether a collation compares text as it is.
func exact(c *irv1.Collation) bool {
	return c.GetDeterministic() == irv1.Tristate_TRISTATE_TRUE && c.GetPadSpace() == irv1.Tristate_TRISTATE_FALSE
}

// CheckColumn is a column a check may test.
type CheckColumn struct {
	Type *irv1.Type
	// Collation is a text column's; nil for other types.
	Collation *irv1.Collation
}

// RecognizeCheck returns a check as a portable kind when it is one, and as
// opaque native text otherwise (§10.1, §10.2, §10.4). Its Columns are the
// columns it reads; its name is the caller's to fill.
//
// The forms are those of the Postgres recognizer, read under SQLite's
// rules: length counts a text value's characters and octet_length its
// bytes (§10.3); text compares by its column's collation, so equality and
// not_empty are recognized only on an exact one, and a range never on
// text. SQLite has no regular expressions of its own, so no pattern. A
// weak column may hold any storage class, so nothing on one is portable.
func RecognizeCheck(c Constraint, columns map[string]CheckColumn) *irv1.Constraint {
	out := &irv1.Constraint{Kind: irv1.ConstraintKind_CONSTRAINT_KIND_CHECK, Columns: References(c.Parsed)}
	if p := (recognizer{columns}).portable(c.Parsed); p != nil {
		out.Form = &irv1.Constraint_Portable{Portable: p}
		out.ClientValidated = true
	} else {
		out.Form = &irv1.Constraint_Opaque{Opaque: &irv1.NativeText{Native: c.Expr}}
	}
	return out
}

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
	op   string
	lit  Expr
}

func (r recognizer) portable(e Expr) *irv1.PortableConstraint {
	switch e := e.(type) {
	case Bin:
		if e.Op == "AND" {
			lo, ok1 := r.comparison(e.L)
			hi, ok2 := r.comparison(e.R)
			if !ok1 || !ok2 || lo.subj.name != hi.subj.name || lo.subj.length != hi.subj.length {
				return nil
			}
			return r.bounds([]bound{lo, hi})
		}
		return r.operator(e)
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

// operator recognizes a single comparison: not_empty, a one-value
// membership, or a one-sided length or range.
func (r recognizer) operator(e Bin) *irv1.PortableConstraint {
	b, ok := r.comparison(e)
	if !ok {
		return nil
	}
	if b.subj.length == irv1.LengthUnit_LENGTH_UNIT_UNSPECIFIED {
		switch b.op {
		case "<>":
			// x <> '' is not_empty: "length of at least 1, without
			// trimming" (§10.2), which RTRIM does not keep.
			if v, ok := b.lit.(Lit); !ok || v.Kind != LitString || v.Text != "" || family(b.subj.col.Type) != famText || !exact(b.subj.col.Collation) {
				return nil
			}
			return &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_NotEmpty{NotEmpty: &irv1.NotEmptyConstraint{}}}
		case "=":
			return r.members(b.subj, []Expr{b.lit})
		}
	}
	return r.bounds([]bound{b})
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

// lengthUnits are the length functions and what they count of text (§10.3).
var lengthUnits = map[string]irv1.LengthUnit{
	"length":       irv1.LengthUnit_LENGTH_UNIT_CHARACTERS,
	"octet_length": irv1.LengthUnit_LENGTH_UNIT_BYTES,
}

// subject reads a column or the length of one.
func (r recognizer) subject(e Expr) (subject, bool) {
	if f, ok := e.(Call); ok {
		unit, ok := lengthUnits[f.Name]
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

func (r recognizer) column(e Expr) (subject, bool) {
	c, ok := e.(Col)
	if !ok || c.Table != "" {
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
		v, ok := literalOf(b.lit, &irv1.Type{Kind: &irv1.Type_Int64{Int64: &irv1.Empty{}}})
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

// members recognizes subject = one of items. Text compares by its
// collation, so only an exact one keeps equality's meaning.
func (r recognizer) members(s subject, items []Expr) *irv1.PortableConstraint {
	if s.length != irv1.LengthUnit_LENGTH_UNIT_UNSPECIFIED || family(s.col.Type) == famText && !exact(s.col.Collation) {
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

// family classifies a subject's logical type; a weak one is famNone.
func family(t *irv1.Type) fam {
	switch t.GetKind().(type) {
	case *irv1.Type_Int64:
		return famInt
	case *irv1.Type_Decimal:
		return famDecimal
	case *irv1.Type_Float64:
		return famFloat
	case *irv1.Type_Text:
		return famText
	}
	return famNone
}

var (
	decimalText = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)
	numberText  = regexp.MustCompile(`^[-+]?[0-9]+(\.[0-9]+)?([eE][-+]?[0-9]+)?$`)
)

// literalOf reads a constant as a value of typ. Numbers are read exactly: a
// decimal stays text (§8.7).
func literalOf(e Expr, typ *irv1.Type) (*irv1.Value, bool) {
	neg := false
	if u, ok := e.(Neg); ok && u.Op == "-" {
		neg, e = true, u.X
	}
	l, ok := e.(Lit)
	if !ok || l.Kind != LitNumber && l.Kind != LitString || neg && l.Kind != LitNumber {
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
		return &irv1.Value{Kind: &irv1.Value_Decimal{Decimal: text}}, numeric && decimalText.MatchString(text)
	case famFloat:
		f, err := strconv.ParseFloat(text, 64)
		return &irv1.Value{Kind: &irv1.Value_Float{Float: f}}, err == nil && numeric && decimalText.MatchString(text)
	default: // famText; callers check the family first.
		return &irv1.Value{Kind: &irv1.Value_String_{String_: text}}, !numeric
	}
}
