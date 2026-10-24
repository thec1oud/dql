// Package weak holds values of weak columns: columns that can hold any
// storage class, as in a non-STRICT SQLite table (design record §8.10).
//
// A Weak[T] holds either a value of the column's declared affinity T, or a
// raw value of another storage class that some writer stored. Reading a raw
// value is not an error (§13); writing one takes explicit construction with
// Raw, so it is always visible in code. Raw values skip client-side
// validation (§10.5).
//
// Nullability stays a separate axis: a nullable weak column is *Weak[T].
package weak

import (
	"fmt"

	"github.com/thec1oud/dql/runtime/go/storage"
)

// Weak is a value of affinity T, or a raw value of another storage class. The
// zero value holds the zero T.
type Weak[T any] struct {
	value T
	raw   storage.Value
}

// Of returns a value of the affinity type: the normal, typed value.
func Of[T any](v T) Weak[T] {
	return Weak[T]{value: v}
}

// Raw returns an explicit raw value, written as it is. It panics when v is
// nil: a raw value is never NULL.
func Raw[T any](v storage.Value) Weak[T] {
	if v == nil {
		panic("weak: Raw of a nil storage value")
	}
	return Weak[T]{raw: v}
}

// Get returns the typed value and true, or the zero T and false when the
// value is raw.
func (w Weak[T]) Get() (T, bool) {
	return w.value, w.raw == nil
}

// Raw returns the raw value, or nil when the value is typed.
func (w Weak[T]) Raw() storage.Value {
	return w.raw
}

// String formats a typed value as itself and a raw value as raw <class>(v).
func (w Weak[T]) String() string {
	if w.raw != nil {
		return fmt.Sprintf("raw %s(%v)", w.raw.Class(), w.raw)
	}
	return fmt.Sprint(w.value)
}
