package dqlmysql

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/thec1oud/dql/runtime/go/civil"
	"github.com/thec1oud/dql/runtime/go/dec"
	"github.com/thec1oud/dql/runtime/go/dql"
)

// arguments returns a statement's arguments as the driver takes them. MySQL
// has no array parameter, so a list is one placeholder holding a JSON array
// (§25.3), which the dialect expands with JSON_TABLE: each item is encoded
// as a parameter would be, and written as JSON, a value the driver would
// send as text as a JSON string.
func arguments(args []any) ([]any, error) {
	out := make([]any, len(args))
	for i, a := range args {
		var err error
		if isList(a) {
			out[i], err = list(a)
		} else {
			out[i], err = encode(a)
		}
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// list writes a list argument as a JSON array.
func list(a any) (string, error) {
	r := reflect.ValueOf(a)
	items := make([]any, r.Len())
	for i := range items {
		v, err := encode(r.Index(i).Interface())
		if err != nil {
			return "", err
		}
		switch x := v.(type) {
		case []byte:
			v = string(x)
		case time.Time:
			v = x.UTC().Format("2006-01-02 15:04:05.999999")
		case string:
			if raw, ok := r.Index(i).Interface().(json.RawMessage); ok {
				v = raw
			}
		}
		items[i] = v
	}
	b, err := json.Marshal(items)
	return string(b), err
}

// isList reports whether a is a list argument: a slice other than bytes
// or JSON.
func isList(a any) bool {
	switch a.(type) {
	case []byte, json.RawMessage:
		return false
	}
	r := reflect.ValueOf(a)
	return r.Kind() == reflect.Slice && r.Type().Elem().Kind() != reflect.Uint8
}

// ErrMonths is the error for an interval with months, which a MySQL TIME
// cannot hold.
var ErrMonths = errors.New("dqlmysql: an interval with months has no TIME value")

// encode turns a parameter's value, in dql's host types, into one the
// driver encodes: a decimal, a civil type or an OffsetTime into its text,
// which MySQL reads as the parameter's type, an OffsetDateTime into its
// instant, an interval into a TIME's text, a bit string into its unsigned
// integer, and a pointer into its target or nil. Any other value passes
// to the driver, which encodes a named string or integer type, such as a
// generated enum, as its underlying type.
func encode(v any) (any, error) {
	switch x := v.(type) {
	case json.RawMessage:
		return string(x), nil
	case dec.Decimal:
		return x.String(), nil
	case civil.Date:
		return x.String(), nil
	case civil.DateTime:
		return x.String(), nil
	case civil.Time:
		return x.String(), nil
	case civil.OffsetTime:
		return x.String(), nil
	case civil.OffsetDateTime:
		return x.Instant(), nil
	case dql.Interval:
		return timeText(x)
	case dql.Bits:
		return bitsValue(x)
	}
	if r := reflect.ValueOf(v); r.Kind() == reflect.Pointer {
		if r.IsNil() {
			return nil, nil
		}
		return encode(r.Elem().Interface())
	}
	return v, nil
}

// timeText writes an interval as a MySQL TIME: [-]H:MM:SS.ffffff, its days
// counted as 24 hours.
func timeText(i dql.Interval) (string, error) {
	if i.Months != 0 {
		return "", ErrMonths
	}
	us := int64(i.Days)*86_400_000_000 + i.Microseconds
	sign := ""
	if us < 0 {
		sign, us = "-", -us
	}
	return fmt.Sprintf("%s%d:%02d:%02d.%06d", sign, us/3_600_000_000, us/60_000_000%60, us/1_000_000%60, us%1_000_000), nil
}

// bitsValue returns a bit string as the unsigned integer MySQL's BIT holds,
// its first bit most significant.
func bitsValue(b dql.Bits) (uint64, error) {
	if b.Len() > 64 {
		return 0, fmt.Errorf("dqlmysql: %d bits do not fit a BIT", b.Len())
	}
	var n uint64
	for i := range b.Len() {
		n <<= 1
		if b.Bit(i) {
			n |= 1
		}
	}
	return n, nil
}
