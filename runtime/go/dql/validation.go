package dql

import (
	"fmt"
	"strings"
)

// Kind is the rule a validation issue breaks: one of the portable constraint
// kinds of §10.2, or a rule of the shape algebra (§14.5, §20.2). Its values
// are the same in every host language.
type Kind string

// The kinds of validation issues.
const (
	KindLength     Kind = "length"
	KindRange      Kind = "range"
	KindDigits     Kind = "digits"
	KindMembership Kind = "membership"
	KindNotEmpty   Kind = "not_empty"
	KindPattern    Kind = "pattern"

	// KindRelationConflict: an FK column and its owning relation are both
	// given and disagree (§14.5). Field and Other name the two.
	KindRelationConflict Kind = "relation_conflict"
	// KindRelationMissing: a NOT NULL FK is given neither through its column
	// nor through its owning relation (§14.5). Field and Other name the two.
	KindRelationMissing Kind = "relation_missing"
	// KindFieldSetTwice: a field is assigned both by the payload and by an
	// expression (§20.2).
	KindFieldSetTwice Kind = "field_set_twice"
)

// Unit is the unit a length is counted in (§10.3): never the host's own, but
// the database's.
type Unit string

// The length units.
const (
	UnitCharacters     Unit = "characters" // code points
	UnitBytes          Unit = "bytes"
	UnitUTF16CodeUnits Unit = "utf16_code_units"
)

// ValidationIssue is one broken rule (§10.5). Its fields are the same in
// every host language: the field path, the kind, the constraint's qualified
// name and its parameters. It never holds the value that broke the rule.
type ValidationIssue struct {
	// Shape is the shape validated, such as post.draft.flat.
	Shape string
	// Field is the path of the field: stored names joined by dots, with [i]
	// after a list, such as posts_to_author[1].title.
	Field string
	Kind  Kind
	// Constraint is the constraint's qualified name, such as
	// shop.app.posts.posts_title_check. It is empty for the rules of the
	// shape algebra.
	Constraint string

	// Min and Max bound a length (as an int) or a range (in the field's
	// logical type); nil means unbounded. MinInclusive and MaxInclusive say
	// whether a range includes its bounds; a length always does.
	Min, Max                   any
	MinInclusive, MaxInclusive bool
	// Unit is the unit of a length, and Got the length counted in it. Got
	// is also the length for not_empty.
	Unit Unit
	Got  int
	// Precision and Scale are the parameters of digits.
	Precision, Scale int
	// Values are the listed values of membership.
	Values []any
	// Pattern is the regex of pattern, in the portable subset of §10.2.
	Pattern string
	// Other is the second field of a relation rule: the owning relation when
	// Field is the FK column.
	Other string
}

// String describes the issue without the value that broke it.
func (i ValidationIssue) String() string {
	var b strings.Builder
	b.WriteString(i.Field)
	b.WriteString(": ")
	switch i.Kind {
	case KindLength:
		fmt.Fprintf(&b, "length %d %s, want %s", i.Got, i.Unit, bounds(i.Min, i.Max, true, true))
	case KindRange:
		fmt.Fprintf(&b, "out of range %s", bounds(i.Min, i.Max, i.MinInclusive, i.MaxInclusive))
	case KindDigits:
		fmt.Fprintf(&b, "does not fit decimal(%d,%d)", i.Precision, i.Scale)
	case KindMembership:
		fmt.Fprintf(&b, "not one of %d listed values", len(i.Values))
	case KindNotEmpty:
		b.WriteString("empty")
	case KindPattern:
		fmt.Fprintf(&b, "does not match %q", i.Pattern)
	case KindRelationConflict:
		fmt.Fprintf(&b, "disagrees with %s", i.Other)
	case KindRelationMissing:
		fmt.Fprintf(&b, "required, through it or %s", i.Other)
	case KindFieldSetTwice:
		b.WriteString("set by both the payload and an expression")
	default:
		b.WriteString(string(i.Kind))
	}
	if i.Constraint != "" {
		fmt.Fprintf(&b, " (%s)", i.Constraint)
	}
	return b.String()
}

// bounds formats an interval such as [1, 200] or (0, ∞).
func bounds(min, max any, minIn, maxIn bool) string {
	lo, hi := "(", ")"
	if minIn && min != nil {
		lo = "["
	}
	if maxIn && max != nil {
		hi = "]"
	}
	if min == nil {
		min = "-∞"
	}
	if max == nil {
		max = "∞"
	}
	return fmt.Sprintf("%s%v, %v%s", lo, min, max, hi)
}

// ValidationErrors is the list of issues a validator found (§10.5). A
// validator returns its Err, so that no issues is a nil error.
type ValidationErrors []ValidationIssue

// Class returns ClassValidationErrors.
func (v ValidationErrors) Class() Class { return ClassValidationErrors }

// Error lists the issues.
func (v ValidationErrors) Error() string {
	parts := make([]string, len(v))
	for i, issue := range v {
		parts[i] = issue.String()
	}
	return "dql: " + string(ClassValidationErrors) + ": " + strings.Join(parts, "; ")
}

// Err returns v as an error, or nil when it holds no issues.
func (v ValidationErrors) Err() error {
	if len(v) == 0 {
		return nil
	}
	return v
}

// At returns a copy of v with every field path, and every Other path, placed
// under prefix, for issues found in a nested shape: At("posts_to_author[1]")
// turns title into posts_to_author[1].title.
func (v ValidationErrors) At(prefix string) ValidationErrors {
	out := make(ValidationErrors, len(v))
	for i, issue := range v {
		issue.Field = join(prefix, issue.Field)
		if issue.Other != "" {
			issue.Other = join(prefix, issue.Other)
		}
		out[i] = issue
	}
	return out
}

func join(prefix, path string) string {
	switch {
	case prefix == "":
		return path
	case path == "" || path[0] == '[':
		return prefix + path
	}
	return prefix + "." + path
}
