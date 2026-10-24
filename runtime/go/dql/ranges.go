package dql

import (
	"fmt"
	"strings"
)

// BoundKind says whether a range bound includes its value, excludes it, or is
// absent, so that the range is unbounded on that side.
type BoundKind uint8

// The bound kinds. The zero value is Unbounded.
const (
	Unbounded BoundKind = iota
	Inclusive
	Exclusive
)

// Bound is one end of a range. Value is meaningful only when Kind is not
// Unbounded.
type Bound[T any] struct {
	Kind  BoundKind
	Value T
}

// InclusiveBound returns a bound that includes v.
func InclusiveBound[T any](v T) Bound[T] { return Bound[T]{Inclusive, v} }

// ExclusiveBound returns a bound that excludes v.
func ExclusiveBound[T any](v T) Bound[T] { return Bound[T]{Exclusive, v} }

// UnboundedBound returns an absent bound.
func UnboundedBound[T any]() Bound[T] { return Bound[T]{} }

// Range is a value of the logical type range<T> (design record §8.1), such as
// a Postgres int4range or tstzrange: the values between two bounds, or the
// empty range. A multirange<T> is a []Range[T]. The zero value is the range of
// every value, (-∞, ∞).
//
// A Range holds its bounds as given; the database normalizes ranges of
// discrete types, such as [1,10] into [1,11).
type Range[T any] struct {
	Lower, Upper Bound[T]
	// Empty marks the empty range; its bounds are then ignored.
	Empty bool
}

// NewRange returns the range between lower and upper.
func NewRange[T any](lower, upper Bound[T]) Range[T] {
	return Range[T]{Lower: lower, Upper: upper}
}

// EmptyRange returns the empty range.
func EmptyRange[T any]() Range[T] {
	return Range[T]{Empty: true}
}

// Contains reports whether v lies in r, comparing values with cmp, which
// returns a negative number, zero or a positive number as its first argument
// is less than, equal to or greater than its second, such as cmp.Compare or
// a method expression like dec.Decimal.Cmp.
func (r Range[T]) Contains(v T, cmp func(a, b T) int) bool {
	if r.Empty {
		return false
	}
	switch r.Lower.Kind {
	case Inclusive:
		if cmp(v, r.Lower.Value) < 0 {
			return false
		}
	case Exclusive:
		if cmp(v, r.Lower.Value) <= 0 {
			return false
		}
	}
	switch r.Upper.Kind {
	case Inclusive:
		return cmp(v, r.Upper.Value) <= 0
	case Exclusive:
		return cmp(v, r.Upper.Value) < 0
	}
	return true
}

// String formats r as Postgres writes a range, such as [1,10) or (,5], with
// each value formatted by fmt, or as empty.
func (r Range[T]) String() string {
	if r.Empty {
		return "empty"
	}
	var b strings.Builder
	b.WriteByte("([("[r.Lower.Kind])
	if r.Lower.Kind != Unbounded {
		fmt.Fprint(&b, r.Lower.Value)
	}
	b.WriteByte(',')
	if r.Upper.Kind != Unbounded {
		fmt.Fprint(&b, r.Upper.Value)
	}
	b.WriteByte(")])"[r.Upper.Kind])
	return b.String()
}
