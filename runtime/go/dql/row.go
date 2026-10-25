package dql

import (
	"strconv"

	"github.com/thec1oud/dql/runtime/go/storage"
	"github.com/thec1oud/dql/runtime/go/weak"
)

// Row is one row of a shape in the driver-neutral form generated decoders
// read: one value for each of the shape's fields in their order, then one for
// each of its nests. The dialect layer turns what its driver returns into
// this form, so decoders hold no dialect knowledge and use no reflection.
//
// A value is nil for NULL, and otherwise:
//
//   - an integer as int64 or uint64, a float as float64, a bool as bool or
//     as an integer 0 or 1;
//   - text, an enum label and a string-fallback opaque value as string, and
//     bytes and a bytes-fallback opaque value as []byte;
//   - an array as []any, one level per dimension;
//   - a composite value as a Row of its attributes in their order;
//   - a weak column's value as a storage.Value;
//   - a to-one nest as a Row, and a to-many nest as []Row;
//   - any other value as its Go type itself, such as time.Time,
//     dec.Decimal, civil.Date or Range[int64].
type Row []any

// Decoder decodes one Row into a shape. It keeps the first error, and every
// decode after it does nothing (§13).
type Decoder struct {
	shape string
	row   Row
	err   *DecodeError
}

// NewDecoder starts decoding v, which must be a Row of the given number of
// values, into the named shape. A composite value is decoded with an empty
// shape, which the field that holds it fills in.
func NewDecoder(shape string, v any, values int) *Decoder {
	d := &Decoder{shape: shape}
	r, ok := v.(Row)
	if !ok || len(r) != values {
		d.err = &DecodeError{Shape: shape, Reason: OffType}
		return d
	}
	d.row = r
	return d
}

// Err returns the first error, a *DecodeError, or nil.
func (d *Decoder) Err() error {
	if d.err == nil {
		return nil
	}
	return d.err
}

// fail records err, an error of a converter or a nested decoder, under the
// given field path.
func (d *Decoder) fail(field string, err error) {
	e := *err.(*DecodeError)
	e.Field = join(field, e.Field)
	if e.Shape == "" {
		e.Shape = d.shape
	}
	d.err = &e
}

// Field decodes value i of a field declared or asserted non-null. NULL is a
// NullIntoNonNull error that names the assertion, if any.
func Field[T any](d *Decoder, i int, field, assertion string, conv func(any) (T, error)) T {
	var zero T
	if d.err != nil {
		return zero
	}
	v := d.row[i]
	if v == nil {
		d.err = &DecodeError{Shape: d.shape, Field: field, Reason: NullIntoNonNull, Assertion: assertion}
		return zero
	}
	x, err := conv(v)
	if err != nil {
		d.fail(field, err)
		return zero
	}
	return x
}

// Nullable decodes value i of a nullable field: nil for NULL.
func Nullable[T any](d *Decoder, i int, field string, conv func(any) (T, error)) *T {
	if d.err != nil || d.row[i] == nil {
		return nil
	}
	x := Field(d, i, field, "", conv)
	if d.err != nil {
		return nil
	}
	return &x
}

// Many decodes value i of a to-many nest, a []Row, with decode. NULL is no
// rows.
func Many[T any](d *Decoder, i int, field string, decode func(Row) (T, error)) []T {
	if d.err != nil || d.row[i] == nil {
		return nil
	}
	rows, ok := d.row[i].([]Row)
	if !ok {
		d.err = &DecodeError{Shape: d.shape, Field: field, Reason: OffType}
		return nil
	}
	out := make([]T, len(rows))
	for j, r := range rows {
		x, err := decode(r)
		if err != nil {
			d.fail(field+"["+strconv.Itoa(j)+"]", err)
			return nil
		}
		out[j] = x
	}
	return out
}

// Nest adapts a shape's decoder to decode a to-one nest with Field or
// Nullable.
func Nest[T any](decode func(Row) (T, error)) func(any) (T, error) {
	return func(v any) (T, error) {
		r, ok := v.(Row)
		if !ok {
			var zero T
			return zero, Fail(OffType)
		}
		return decode(r)
	}
}

// Fail returns a *DecodeError of the reason, for a converter to return.
func Fail(reason DecodeReason) error {
	return &DecodeError{Reason: reason}
}

// The converters below turn one non-null value into a field's Go type, or
// fail with OffType.

// Int converts an integer that fits T.
func Int[T int8 | int16 | int32 | int64](v any) (T, error) {
	var x T
	switch n := v.(type) {
	case int64:
		x = T(n)
		if int64(x) == n {
			return x, nil
		}
	case uint64:
		x = T(n)
		if x >= 0 && uint64(x) == n {
			return x, nil
		}
	}
	return 0, Fail(OffType)
}

// Uint converts an integer that fits T.
func Uint[T uint8 | uint16 | uint32 | uint64](v any) (T, error) {
	var x T
	switch n := v.(type) {
	case int64:
		x = T(n)
		if n >= 0 && uint64(x) == uint64(n) {
			return x, nil
		}
	case uint64:
		x = T(n)
		if uint64(x) == n {
			return x, nil
		}
	}
	return 0, Fail(OffType)
}

// Float converts a float that T holds exactly.
func Float[T float32 | float64](v any) (T, error) {
	n, ok := v.(float64)
	if x := T(n); ok && (float64(x) == n || n != n) {
		return x, nil
	}
	return 0, Fail(OffType)
}

// Bool converts a bool, or an integer 0 or 1 such as a TINYINT(1) annotated
// bool holds; any other integer is off type (§13).
func Bool(v any) (bool, error) {
	switch b := v.(type) {
	case bool:
		return b, nil
	case int64:
		if b == 0 || b == 1 {
			return b == 1, nil
		}
	case uint64:
		if b == 0 || b == 1 {
			return b == 1, nil
		}
	}
	return false, Fail(OffType)
}

// String converts a string.
func String(v any) (string, error) {
	return As[string](v)
}

// Bytes converts a []byte.
func Bytes(v any) ([]byte, error) {
	return As[[]byte](v)
}

// As converts a value that is already of type T.
func As[T any](v any) (T, error) {
	x, ok := v.(T)
	if !ok {
		return x, Fail(OffType)
	}
	return x, nil
}

// Enum converts a label that valid accepts; a label added after generation
// is an UnknownEnumLabel error (§13).
func Enum[E ~string](valid func(E) bool) func(any) (E, error) {
	return func(v any) (E, error) {
		s, err := String(v)
		if err != nil {
			return "", err
		}
		if !valid(E(s)) {
			return "", Fail(UnknownEnumLabel)
		}
		return E(s), nil
	}
}

// Array converts one dimension of an array, a []any, with elem converting
// each element. A value that is not a []any where the declaration has a
// dimension is a DimensionMismatch error (§13).
func Array[E any](elem func(any) (E, error)) func(any) ([]E, error) {
	return func(v any) ([]E, error) {
		values, ok := v.([]any)
		if !ok {
			return nil, Fail(DimensionMismatch)
		}
		out := make([]E, len(values))
		for i, x := range values {
			e, err := elem(x)
			if err != nil {
				err.(*DecodeError).Field = join("["+strconv.Itoa(i)+"]", err.(*DecodeError).Field)
				return nil, err
			}
			out[i] = e
		}
		return out, nil
	}
}

// Elem converts an element of an array whose elements are non-null: a NULL
// element is a NullElementIntoNonNull error, and a nested []any, a dimension
// the declaration lacks, is a DimensionMismatch error (§13).
func Elem[T any](conv func(any) (T, error)) func(any) (T, error) {
	return func(v any) (T, error) {
		var zero T
		switch v.(type) {
		case nil:
			return zero, Fail(NullElementIntoNonNull)
		case []any:
			return zero, Fail(DimensionMismatch)
		}
		return conv(v)
	}
}

// NullableElem converts an element of an array whose elements may be NULL.
func NullableElem[T any](conv func(any) (T, error)) func(any) (*T, error) {
	return func(v any) (*T, error) {
		switch v.(type) {
		case nil:
			return nil, nil
		case []any:
			return nil, Fail(DimensionMismatch)
		}
		x, err := conv(v)
		if err != nil {
			return nil, err
		}
		return &x, nil
	}
}

// Weak converts a weak column's storage.Value: a value conv accepts as the
// affinity type, and otherwise the raw value, which is not an error (§8.10,
// §13).
func Weak[T any](conv func(any) (T, error)) func(any) (weak.Weak[T], error) {
	return func(v any) (weak.Weak[T], error) {
		var plain any
		switch s := v.(type) {
		case storage.Integer:
			plain = int64(s)
		case storage.Real:
			plain = float64(s)
		case storage.Text:
			plain = string(s)
		case storage.Blob:
			plain = []byte(s)
		default:
			return weak.Weak[T]{}, Fail(OffType)
		}
		if x, err := conv(plain); err == nil {
			return weak.Of(x), nil
		}
		return weak.Raw[T](v.(storage.Value)), nil
	}
}
