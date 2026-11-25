package dqlpgx

import (
	"encoding/json"
	"net"
	"net/netip"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/thec1oud/dql/runtime/go/civil"
	"github.com/thec1oud/dql/runtime/go/dec"
	"github.com/thec1oud/dql/runtime/go/dql"
)

func encodeArgs(args []any) []any {
	out := make([]any, len(args))
	for i, a := range args {
		out[i] = encode(a)
	}
	return out
}

// encode turns a parameter's value, in dql's host types, into one pgx
// encodes: a dql or civil type into its pgtype counterpart, a pointer into
// its target or nil, a slice into []any, a dql.Range into pgtype.Range and
// a dql.Row into a composite value. Any other value passes through, so a
// named string or integer type, such as a generated enum, encodes as its
// underlying type.
func encode(v any) any {
	switch x := v.(type) {
	case nil, string, []byte, json.RawMessage, net.HardwareAddr, bool,
		int64, int32, int16, int8, uint32, uint16, uint8, float64, float32, time.Time, netip.Prefix:
		return v
	case dec.Decimal:
		return pgtype.Numeric{Int: x.Coefficient(), Exp: x.Exponent(), Valid: true}
	case civil.Date:
		return pgtype.Date{Time: x.In(time.UTC), Valid: true}
	case civil.DateTime:
		return pgtype.Timestamp{Time: x.In(time.UTC), Valid: true}
	case civil.Time:
		us := int64(x.Hour)*3_600_000_000 + int64(x.Minute)*60_000_000 + int64(x.Second)*1_000_000 + int64(x.Nanosecond/1000)
		return pgtype.Time{Microseconds: us, Valid: true}
	case civil.OffsetTime:
		return x.String()
	case civil.OffsetDateTime:
		return x.Instant()
	case dql.Interval:
		return pgtype.Interval{Months: x.Months, Days: x.Days, Microseconds: x.Microseconds, Valid: true}
	case dql.Bits:
		return pgtype.Bits{Bytes: x.Bytes(), Len: int32(x.Len()), Valid: true}
	case uuid.UUID:
		return pgtype.UUID{Bytes: x, Valid: true}
	case netip.Addr:
		return netip.PrefixFrom(x, x.BitLen())
	case dql.Row:
		return pgtype.CompositeFields(encodeArgs(x))
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Pointer:
		if r.IsNil() {
			return nil
		}
		return encode(r.Elem().Interface())
	case reflect.Slice:
		if r.IsNil() {
			return nil
		}
		if isRange(r.Type().Elem()) {
			out := make(pgtype.Multirange[rangeArg], r.Len())
			for i := range out {
				out[i] = encodeRange(r.Index(i))
			}
			return out
		}
		a := pgtype.Array[any]{Elements: []any{}}
		flatten(r, 0, &a)
		if len(a.Dims) == 1 {
			return a.Elements
		}
		a.Valid = true
		return a
	case reflect.Struct:
		if isRange(r.Type()) {
			return encodeRange(r)
		}
	}
	return v
}

// isRange reports whether t is a dql.Range of some element type.
func isRange(t reflect.Type) bool {
	return t.PkgPath() == rangeType.PkgPath() && t.NumField() == rangeType.NumField() &&
		len(t.Name()) > len("Range[") && t.Name()[:len("Range[")] == "Range["
}

var rangeType = reflect.TypeFor[dql.Range[any]]()

// rangeArg is a range pgx encodes: pgtype.Range[any] hands pgx pointers to
// its bounds, which pgx has no plan for.
type rangeArg struct{ pgtype.Range[any] }

func (r rangeArg) Bounds() (lower, upper any) { return r.Lower, r.Upper }

func encodeRange(r reflect.Value) rangeArg {
	if r.FieldByName("Empty").Bool() {
		return rangeArg{pgtype.Range[any]{LowerType: pgtype.Empty, UpperType: pgtype.Empty, Valid: true}}
	}
	lowerType, lower := encodeBound(r.FieldByName("Lower"))
	upperType, upper := encodeBound(r.FieldByName("Upper"))
	return rangeArg{pgtype.Range[any]{Lower: lower, Upper: upper, LowerType: lowerType, UpperType: upperType, Valid: true}}
}

// flatten appends the elements of the slice r, at depth dim of a
// multidimensional array, to a's elements, and records each dimension's
// length from its first slice. A slice of a byte type is an element.
func flatten(r reflect.Value, dim int, a *pgtype.Array[any]) {
	if dim == len(a.Dims) {
		a.Dims = append(a.Dims, pgtype.ArrayDimension{Length: int32(r.Len()), LowerBound: 1})
	}
	nested := r.Type().Elem().Kind() == reflect.Slice && r.Type().Elem().Elem().Kind() != reflect.Uint8
	for i := range r.Len() {
		if nested {
			flatten(r.Index(i), dim+1, a)
		} else {
			a.Elements = append(a.Elements, encode(r.Index(i).Interface()))
		}
	}
}

func encodeBound(b reflect.Value) (pgtype.BoundType, any) {
	switch dql.BoundKind(b.FieldByName("Kind").Uint()) {
	case dql.Unbounded:
		return pgtype.Unbounded, nil
	case dql.Inclusive:
		return pgtype.Inclusive, encode(b.FieldByName("Value").Interface())
	}
	return pgtype.Exclusive, encode(b.FieldByName("Value").Interface())
}

// simpleArgs encodes arguments for the simple protocol, which writes each
// value into the statement as a literal, so the server never says what
// type a parameter has (§26.4). A Go scalar pgx writes as it is; any other
// value whose declared type m knows is encoded as that type's text, which
// the server reads as the parameter's type; the rest are given Go types
// pgx can infer a database type from (inferable).
func simpleArgs(m *pgtype.Map, args []any, types []string) ([]any, error) {
	out := make([]any, len(args))
	for i, a := range args {
		v := encode(a)
		var t *pgtype.Type
		if i < len(types) && !scalar(v) {
			t = typeFor(m, types[i])
		}
		if t == nil {
			out[i] = inferable(v)
			continue
		}
		b, err := m.Encode(t.OID, pgtype.TextFormatCode, v, nil)
		if err != nil {
			return nil, err
		}
		if b != nil {
			out[i] = string(b)
		}
	}
	return out, nil
}

// scalar reports whether pgx writes v into a simple statement as it is.
func scalar(v any) bool {
	switch v.(type) {
	case nil, string, []byte, bool, int64, int32, int16, int8, uint32, uint16, uint8, float64, float32:
		return true
	}
	return false
}

// typeFor returns the type m registers under name, written
// namespace.name, or under its name alone, as pgx registers the built-in
// types and a type the driver was given unqualified; nil when m has
// neither.
func typeFor(m *pgtype.Map, name string) *pgtype.Type {
	if name == "" {
		return nil
	}
	if t, ok := m.TypeForName(name); ok {
		return t
	}
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		if t, ok := m.TypeForName(name[i+1:]); ok {
			return t
		}
	}
	return nil
}

// inferable returns v in a form pgx infers a database type from: a
// one-dimensional list as a slice of its elements' type, of pointers when
// an element is NULL, and a named scalar type as its underlying type.
// A list with no element to go by is a text list, whose literal the
// server reads as the parameter's type.
func inferable(v any) any {
	elems, ok := v.([]any)
	if !ok {
		return v
	}
	var t reflect.Type
	null := false
	for _, e := range elems {
		if e == nil {
			null = true
		} else if t == nil {
			t = underlying(reflect.TypeOf(e))
		}
	}
	if t == nil {
		t = reflect.TypeFor[string]()
	}
	et := t
	if null {
		et = reflect.PointerTo(t)
	}
	out := reflect.MakeSlice(reflect.SliceOf(et), len(elems), len(elems))
	for i, e := range elems {
		if e == nil {
			continue
		}
		x := reflect.ValueOf(e)
		if !x.CanConvert(t) {
			return v
		}
		x = x.Convert(t)
		if null {
			p := reflect.New(t)
			p.Elem().Set(x)
			x = p
		}
		out.Index(i).Set(x)
	}
	return out.Interface()
}

// underlying returns string for a named string type, such as a
// generated enum, which pgx has no encoder for, and t otherwise.
func underlying(t reflect.Type) reflect.Type {
	if t.Kind() == reflect.String {
		return reflect.TypeFor[string]()
	}
	return t
}
