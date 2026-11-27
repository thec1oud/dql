// Package opt holds optional values: the presence axis of a shape's fields
// (design record §14.1).
//
// Presence and nullability are separate axes and are never collapsed into one
// wrapper (§14.1): an optional non-null field is Opt[T], and an optional
// nullable field is Opt[*T], which has three states: absent, set to NULL
// (Some(nil)) and set to a value. Absence means DEFAULT in a Draft and
// unchanged in a Partial.
package opt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
)

// Opt is a value of type T that may be absent. The zero value is absent.
type Opt[T any] struct {
	value T
	ok    bool
}

// Some returns a present value.
func Some[T any](v T) Opt[T] {
	return Opt[T]{value: v, ok: true}
}

// None returns an absent value. It equals the zero Opt[T].
func None[T any]() Opt[T] {
	return Opt[T]{}
}

// Get returns the value and whether it is present. When it is absent, the
// value is the zero T.
func (o Opt[T]) Get() (T, bool) {
	return o.value, o.ok
}

// IsSome reports whether the value is present.
func (o Opt[T]) IsSome() bool {
	return o.ok
}

// IsZero reports whether the value is absent, so that a struct field tagged
// `json:",omitzero"` is omitted exactly when it is absent.
func (o Opt[T]) IsZero() bool {
	return !o.ok
}

// Or returns the value when it is present, and def otherwise.
func (o Opt[T]) Or(def T) T {
	if o.ok {
		return o.value
	}
	return def
}

// String formats the value as Some(v) or None.
func (o Opt[T]) String() string {
	if o.ok {
		return fmt.Sprintf("Some(%v)", o.value)
	}
	return "None"
}

// Map applies f to a present value, and keeps an absent one absent.
func Map[T, R any](o Opt[T], f func(T) R) Opt[R] {
	if o.ok {
		return Some(f(o.value))
	}
	return None[R]()
}

// MarshalJSON encodes a present value as the value itself. An absent value
// has no encoding of its own: tag the field `json:",omitzero"` so that it is
// left out; without the tag it encodes as null.
func (o Opt[T]) MarshalJSON() ([]byte, error) {
	if !o.ok {
		return []byte("null"), nil
	}
	return json.Marshal(o.value)
}

// UnmarshalJSON makes the value present. A field missing from the input
// leaves it absent, because encoding/json does not call UnmarshalJSON for it.
//
// null is accepted only when T can hold it, as a pointer, an interface or a
// type that decodes JSON itself; so Opt[*T] tells absent from NULL, and null
// for a non-null field is an error rather than a silent zero value (guide §7,
// rule 4).
func (o *Opt[T]) UnmarshalJSON(data []byte) error {
	var v T
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) && !nullable(reflect.TypeFor[T]()) {
		return fmt.Errorf("opt: null for a non-null %s", reflect.TypeFor[T]())
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*o = Some(v)
	return nil
}

var unmarshaler = reflect.TypeFor[json.Unmarshaler]()

func nullable(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Pointer, reflect.Interface:
		return true
	}
	return reflect.PointerTo(t).Implements(unmarshaler)
}
