package dql

import (
	"regexp"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/thec1oud/dql/runtime/go/dec"
)

// The checks of the portable constraint kinds (§10.2). Generated validators
// declare one per client-validated constraint and run it on a field's value,
// appending an issue to v when the value breaks it. Every host language
// checks the same semantics, so a value passes here exactly when it passes
// the constraint in the database (§10.2, §10.3).

// LengthCheck is a length constraint, counted in the database's unit (§10.3).
// A nil Min or Max is unbounded.
type LengthCheck struct {
	Constraint string
	Min, Max   *int
	Unit       Unit
}

// Length counts s in unit: code points, bytes or UTF-16 code units.
func Length(s string, unit Unit) int {
	switch unit {
	case UnitBytes:
		return len(s)
	case UnitUTF16CodeUnits:
		n := 0
		for _, r := range s {
			n += utf16.RuneLen(r)
		}
		return n
	}
	return utf8.RuneCountInString(s)
}

// Check appends an issue to v when s's length is out of c's bounds.
func (c LengthCheck) Check(v *ValidationErrors, shape, field, s string) {
	n := Length(s, c.Unit)
	if (c.Min == nil || n >= *c.Min) && (c.Max == nil || n <= *c.Max) {
		return
	}
	issue := ValidationIssue{Shape: shape, Field: field, Kind: KindLength, Constraint: c.Constraint,
		MinInclusive: true, MaxInclusive: true, Unit: c.Unit, Got: n}
	if c.Min != nil {
		issue.Min = *c.Min
	}
	if c.Max != nil {
		issue.Max = *c.Max
	}
	*v = append(*v, issue)
}

// NotEmptyCheck is a not_empty constraint: a length of at least 1, without
// trimming.
type NotEmptyCheck struct {
	Constraint string
}

// Check appends an issue to v when s is empty.
func (c NotEmptyCheck) Check(v *ValidationErrors, shape, field, s string) {
	if s == "" {
		*v = append(*v, ValidationIssue{Shape: shape, Field: field, Kind: KindNotEmpty, Constraint: c.Constraint})
	}
}

// PatternCheck is a pattern constraint: an unanchored search with a regex in
// the portable subset, matched over code points (§10.2). Go's syntax agrees
// with the subset: ^ and $ match the whole string, and \d, \w and \s are
// ASCII only.
type PatternCheck struct {
	Constraint string
	Regex      *regexp.Regexp
}

// Check appends an issue to v when s does not match.
func (c PatternCheck) Check(v *ValidationErrors, shape, field, s string) {
	if !c.Regex.MatchString(s) {
		*v = append(*v, ValidationIssue{Shape: shape, Field: field, Kind: KindPattern, Constraint: c.Constraint, Pattern: c.Regex.String()})
	}
}

// RangeCheck is a range constraint, compared in the field's logical type with
// Compare. A nil Min or Max is unbounded.
type RangeCheck[T any] struct {
	Constraint                 string
	Min, Max                   *T
	MinInclusive, MaxInclusive bool
	Compare                    func(a, b T) int
}

// Check appends an issue to v when x is out of c's bounds.
func (c RangeCheck[T]) Check(v *ValidationErrors, shape, field string, x T) {
	above := c.Min == nil || c.Compare(x, *c.Min) > 0 || (c.MinInclusive && c.Compare(x, *c.Min) == 0)
	below := c.Max == nil || c.Compare(x, *c.Max) < 0 || (c.MaxInclusive && c.Compare(x, *c.Max) == 0)
	if above && below {
		return
	}
	issue := ValidationIssue{Shape: shape, Field: field, Kind: KindRange, Constraint: c.Constraint,
		MinInclusive: c.MinInclusive, MaxInclusive: c.MaxInclusive}
	if c.Min != nil {
		issue.Min = *c.Min
	}
	if c.Max != nil {
		issue.Max = *c.Max
	}
	*v = append(*v, issue)
}

// DigitsCheck is a digits constraint: the value fits decimal(Precision,
// Scale) after rounding to Scale, as the database stores it.
type DigitsCheck struct {
	Constraint       string
	Precision, Scale int
}

// Check appends an issue to v when x does not fit.
func (c DigitsCheck) Check(v *ValidationErrors, shape, field string, x dec.Decimal) {
	if !x.Fits(int32(c.Precision), int32(c.Scale)) {
		*v = append(*v, ValidationIssue{Shape: shape, Field: field, Kind: KindDigits, Constraint: c.Constraint,
			Precision: c.Precision, Scale: c.Scale})
	}
}

// MembershipCheck is a membership constraint: equal to one listed value.
type MembershipCheck[T comparable] struct {
	Constraint string
	Values     []T
}

// Check appends an issue to v when x is none of the values.
func (c MembershipCheck[T]) Check(v *ValidationErrors, shape, field string, x T) {
	for _, value := range c.Values {
		if x == value {
			return
		}
	}
	values := make([]any, len(c.Values))
	for i, value := range c.Values {
		values[i] = value
	}
	*v = append(*v, ValidationIssue{Shape: shape, Field: field, Kind: KindMembership, Constraint: c.Constraint, Values: values})
}

// Must returns v, and panics when err is not nil. Generated code uses it for
// constraint bounds the emitter has already parsed.
func Must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
