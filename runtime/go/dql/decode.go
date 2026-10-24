package dql

import "strings"

// DecodeReason is why a value could not be decoded (§13).
type DecodeReason string

// The reasons of §13.
const (
	// NullIntoNonNull: NULL in a field declared or asserted non-null.
	NullIntoNonNull DecodeReason = "null_into_non_null"
	// NullElementIntoNonNull: a NULL element in an array asserted
	// array-elements-nonnull.
	NullElementIntoNonNull DecodeReason = "null_element_into_non_null"
	// UnknownEnumLabel: an enum label added after generation.
	UnknownEnumLabel DecodeReason = "unknown_enum_label"
	// DimensionMismatch: an array whose dimensions differ from the
	// declaration.
	DimensionMismatch DecodeReason = "dimension_mismatch"
	// OffType: an off-type value in a strict field, such as 2 in a
	// TINYINT(1) annotated bool.
	OffType DecodeReason = "off_type"
)

// DecodeError reports a row value that breaks what its shape declares (§13).
// Decoding never produces a silent zero value: the worst case is this error.
// It never holds the value itself.
type DecodeError struct {
	Origin
	// Shape is the shape decoded, such as published_post.full.flat.
	Shape string
	// Field is the field's path, as in ValidationIssue.
	Field  string
	Reason DecodeReason
	// Assertion is where an asserted fact came from, such as
	// "core.nonnull from COMMENT ON COLUMN app.published_posts.title"; it
	// is empty for facts the database proves.
	Assertion string
}

// Class returns ClassDecodeError.
func (e *DecodeError) Class() Class { return ClassDecodeError }

func (e *DecodeError) Error() string {
	var b strings.Builder
	b.WriteString(e.Shape)
	if e.Field != "" {
		b.WriteString(" field ")
		b.WriteString(e.Field)
	}
	b.WriteString(": ")
	b.WriteString(string(e.Reason))
	if e.Assertion != "" {
		b.WriteString(", asserted by ")
		b.WriteString(e.Assertion)
	}
	return message(e.Class(), b.String(), e.Origin)
}
