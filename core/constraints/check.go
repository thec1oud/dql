package constraints

import (
	"fmt"

	"github.com/thec1oud/dql/core/diagnostics"
	"github.com/thec1oud/dql/core/types"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Diagnostic codes of Check. Every one is an error: a malformed constraint
// is a bug in the source plugin that produced it.
const (
	CodeColumnUnknown              = "constraint-column-unknown"
	CodeFormMissing                = "constraint-form-missing"
	CodeClientValidated            = "constraint-client-validated"
	CodePortableKindMissing        = "constraint-portable-kind-missing"
	CodePortableColumns            = "constraint-portable-columns"
	CodeLengthUnitMissing          = "constraint-length-unit-missing"
	CodeLengthUnbounded            = "constraint-length-unbounded"
	CodeLengthMinAboveMax          = "constraint-length-min-above-max"
	CodeRangeUnbounded             = "constraint-range-unbounded"
	CodeRangeInclusiveWithoutBound = "constraint-range-inclusive-without-bound"
	CodeRangeType                  = "constraint-range-type"
	CodeRangeBoundType             = "constraint-range-bound-type"
	CodeDigitsPrecisionZero        = "constraint-digits-precision-zero"
	CodeDigitsScaleAbovePrecision  = "constraint-digits-scale-above-precision"
	CodeMembershipEmpty            = "constraint-membership-empty"
	CodePatternEmpty               = "constraint-pattern-empty"
	CodePatternUnportable          = "constraint-pattern-unportable"
)

// Check checks the check and exclusion constraints of every table and domain
// in b and returns its diagnostics, sorted (§3.2 rule 5):
//
//   - A constraint is portable or opaque (§10.1, §6.3), and client_validated
//     is true exactly for portable ones (§10.4).
//   - The columns a table constraint names exist. A portable constraint
//     constrains one value: one column of a table, or a domain's value.
//   - A portable constraint is well formed (§10.2): a length has a unit and a
//     bound, and min ≤ max; a range has a bound, and its bounds are values of
//     the logical type it compares in; digits have 1 ≤ precision and
//     scale ≤ precision; a membership lists values; a pattern is in the
//     portable subset.
func Check(b *irv1.Bundle) []*irv1.Diagnostic {
	ix := types.NewIndex(b)
	var ds []*irv1.Diagnostic
	for _, c := range b.GetCatalogs() {
		ck := &checker{ix: ix, source: c.GetProvenance().GetSource()}
		if ck.source == "" {
			ck.source = "that produced the catalog"
		}
		for _, ns := range c.GetNamespaces() {
			obj := func(name string) *irv1.QualifiedName {
				return &irv1.QualifiedName{Catalog: c.GetName().GetStored(), Namespace: ns.GetName().GetStored(), Object: name}
			}
			for _, t := range ns.GetTables() {
				cols := map[string]*irv1.Column{}
				for _, col := range t.GetColumns() {
					cols[col.GetName().GetStored()] = col
				}
				for _, con := range t.GetConstraints() {
					ds = append(ds, ck.table(obj(t.GetName().GetStored()), cols, con)...)
				}
			}
			for _, d := range ns.GetDomains() {
				for _, con := range d.GetConstraints() {
					ds = append(ds, ck.domain(obj(d.GetName().GetStored()), d, con)...)
				}
			}
		}
	}
	diagnostics.Sort(ds)
	return ds
}

type checker struct {
	ix     *types.Index
	source string
	target *irv1.QualifiedName
	ds     []*irv1.Diagnostic
}

// bug records a malformed constraint, telling who must fix it.
func (ck *checker) bug(code, format string, args ...any) {
	ck.ds = append(ck.ds, diagnostics.Errorf(code, ck.target,
		"constraint %s %s; the source plugin %s must emit a well-formed portable constraint or leave it opaque (§10.1)",
		diagnostics.Name(ck.target), fmt.Sprintf(format, args...), ck.source))
}

func (ck *checker) begin(owner *irv1.QualifiedName, con *irv1.Constraint) {
	ck.target = &irv1.QualifiedName{Catalog: owner.GetCatalog(), Namespace: owner.GetNamespace(), Object: owner.GetObject(), Member: con.GetName()}
	ck.ds = nil
}

func (ck *checker) table(owner *irv1.QualifiedName, cols map[string]*irv1.Column, con *irv1.Constraint) []*irv1.Diagnostic {
	ck.begin(owner, con)
	for _, name := range con.GetColumns() {
		if cols[name] == nil {
			ck.bug(CodeColumnUnknown, "reads column %s, which table %s does not have", name, diagnostics.Name(owner))
		}
	}
	var valueType *irv1.Type
	if con.GetPortable() != nil {
		if n := len(con.GetColumns()); n != 1 {
			ck.bug(CodePortableColumns, "is portable but reads %d columns; a portable kind constrains exactly one value (§10.2)", n)
		} else {
			valueType = cols[con.GetColumns()[0]].GetType()
		}
	}
	ck.constraint(con, valueType)
	return ck.ds
}

func (ck *checker) domain(owner *irv1.QualifiedName, d *irv1.Domain, con *irv1.Constraint) []*irv1.Diagnostic {
	ck.begin(owner, con)
	if con.GetPortable() != nil && len(con.GetColumns()) != 0 {
		ck.bug(CodePortableColumns, "is a domain constraint that names columns; it constrains the domain's value (§8.5)")
	}
	ck.constraint(con, d.GetBase())
	return ck.ds
}

// constraint checks a constraint's form. valueType is the type of the value
// a portable constraint constrains, or nil when it is not known.
func (ck *checker) constraint(con *irv1.Constraint, valueType *irv1.Type) {
	switch con.GetForm().(type) {
	case nil:
		ck.bug(CodeFormMissing, "is neither portable nor opaque; a constraint the source does not recognize keeps its native text as opaque (§6.3, §10.4)")
		return
	case *irv1.Constraint_Portable:
		if !con.GetClientValidated() {
			ck.bug(CodeClientValidated, "is portable, so clientValidated must be true (§10.4)")
		}
		ck.portable(con.GetPortable(), valueType)
	case *irv1.Constraint_Opaque:
		if con.GetClientValidated() {
			ck.bug(CodeClientValidated, "is opaque, so clientValidated must be false: only the database enforces it (§10.4)")
		}
	}
}

func (ck *checker) portable(p *irv1.PortableConstraint, valueType *irv1.Type) {
	switch k := p.GetKind().(type) {
	case nil:
		ck.bug(CodePortableKindMissing, "is portable but has no kind (§10.2)")
	case *irv1.PortableConstraint_Length:
		l := k.Length
		if l.GetUnit() == irv1.LengthUnit_LENGTH_UNIT_UNSPECIFIED {
			ck.bug(CodeLengthUnitMissing, "is a length without a unit; emitters count in the database's unit (§10.3)")
		}
		switch {
		case l.Min == nil && l.Max == nil:
			ck.bug(CodeLengthUnbounded, "is a length with neither a minimum nor a maximum (§10.2)")
		case l.Min != nil && l.Max != nil && l.GetMin() > l.GetMax():
			ck.bug(CodeLengthMinAboveMax, "is a length with minimum %d above its maximum %d (§10.2)", l.GetMin(), l.GetMax())
		}
	case *irv1.PortableConstraint_Range:
		ck.rangeConstraint(k.Range, valueType)
	case *irv1.PortableConstraint_Digits:
		d := k.Digits
		if d.GetPrecision() == 0 {
			ck.bug(CodeDigitsPrecisionZero, "is digits with precision 0; decimal(p,s) has 1 ≤ p (§10.2)")
		} else if d.GetScale() > d.GetPrecision() {
			ck.bug(CodeDigitsScaleAbovePrecision, "is digits with scale %d above precision %d; decimal(p,s) has s ≤ p (§10.2)", d.GetScale(), d.GetPrecision())
		}
	case *irv1.PortableConstraint_Membership:
		if len(k.Membership.GetValues()) == 0 {
			ck.bug(CodeMembershipEmpty, "is a membership that lists no values (§10.2)")
		}
	case *irv1.PortableConstraint_Pattern:
		if k.Pattern.GetRegex() == "" {
			ck.bug(CodePatternEmpty, "is a pattern without a regex (§10.2)")
		} else if err := ValidatePattern(k.Pattern.GetRegex()); err != nil {
			ck.bug(CodePatternUnportable, "has a regex outside the portable subset: %v", err)
		}
	}
}

// Value kinds, as a bit set.
const (
	valueString = 1 << iota
	valueInt
	valueUint
	valueDecimal
	valueFloat
	valueBool
)

// rangeValues returns the value kinds a range bound may have when it
// compares in logical type t: numbers, dates and times (§10.2). Dates and
// times travel in their text form (values.proto). A decimal bound is never a
// binary float (§8.7). An interval has no range: a month has no fixed length
// (§8.8 rule 3), so the order of intervals is not a portable comparison.
func rangeValues(t *irv1.Type) int {
	switch t.GetKind().(type) {
	case *irv1.Type_Int8, *irv1.Type_Int16, *irv1.Type_Int32, *irv1.Type_Int64,
		*irv1.Type_Uint8, *irv1.Type_Uint16, *irv1.Type_Uint32, *irv1.Type_Uint64:
		return valueInt | valueUint
	case *irv1.Type_Float32, *irv1.Type_Float64:
		return valueInt | valueUint | valueDecimal | valueFloat
	case *irv1.Type_Decimal, *irv1.Type_Money:
		return valueInt | valueUint | valueDecimal
	case *irv1.Type_Date, *irv1.Type_Instant, *irv1.Type_InstantWithOffset,
		*irv1.Type_LocalDatetime, *irv1.Type_LocalTime, *irv1.Type_TimeWithOffset:
		return valueString
	}
	return 0
}

func valueKind(v *irv1.Value) (int, string) {
	switch v.GetKind().(type) {
	case *irv1.Value_String_:
		return valueString, "a string"
	case *irv1.Value_Int:
		return valueInt, "an int"
	case *irv1.Value_Uint:
		return valueUint, "a uint"
	case *irv1.Value_Decimal:
		return valueDecimal, "a decimal"
	case *irv1.Value_Float:
		return valueFloat, "a float"
	case *irv1.Value_Bool:
		return valueBool, "a bool"
	}
	return 0, "an empty"
}

func (ck *checker) rangeConstraint(r *irv1.RangeConstraint, valueType *irv1.Type) {
	if r.GetMin() == nil && r.GetMax() == nil {
		ck.bug(CodeRangeUnbounded, "is a range with neither a minimum nor a maximum (§10.2)")
	}
	if (r.GetMinInclusive() && r.GetMin() == nil) || (r.GetMaxInclusive() && r.GetMax() == nil) {
		ck.bug(CodeRangeInclusiveWithoutBound, "is a range with an inclusive flag on a missing bound (§10.2)")
	}
	t := ck.logical(valueType)
	if t == nil {
		return // the type is missing or unresolved, which types.Check reports
	}
	allowed := rangeValues(t)
	if allowed == 0 {
		ck.bug(CodeRangeType, "is a range over %s; a range compares numbers, dates and times in their logical type (§10.2)", types.KindName(t))
		return
	}
	for _, b := range []struct {
		name string
		v    *irv1.Value
	}{{"minimum", r.GetMin()}, {"maximum", r.GetMax()}} {
		if b.v == nil {
			continue
		}
		if k, desc := valueKind(b.v); k&allowed == 0 {
			ck.bug(CodeRangeBoundType, "is a range over %s whose %s is %s value (§10.2, §8.7)", types.KindName(t), b.name, desc)
		}
	}
}

// logical returns the logical type a value of t compares in: through
// domains to their base, and through a weak type to its affinity (§8.10).
func (ck *checker) logical(t *irv1.Type) *irv1.Type {
	t = ck.ix.Underlying(t)
	if w := t.GetWeak(); w != nil {
		t = ck.ix.Underlying(w.GetAffinity())
	}
	if t.GetKind() == nil {
		return nil
	}
	return t
}
