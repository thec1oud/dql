package dqlsqlite

import (
	"encoding/json"
	"reflect"
	"time"

	"github.com/thec1oud/dql/runtime/go/civil"
	"github.com/thec1oud/dql/runtime/go/dec"
	"github.com/thec1oud/dql/runtime/go/storage"
)

// arguments returns a statement's arguments as the driver takes them.
// SQLite has no array parameter, so a list is one placeholder holding a
// JSON array (§25.3), which the dialect expands with json_each: each item
// is encoded as a parameter would be, and written as JSON.
func arguments(args []any) ([]any, error) {
	out := make([]any, len(args))
	for i, a := range args {
		if isList(a) {
			l, err := list(a)
			if err != nil {
				return nil, err
			}
			out[i] = l
		} else {
			out[i] = encode(a)
		}
	}
	return out, nil
}

// list writes a list argument as a JSON array. A JSON item is written as
// the JSON it holds.
func list(a any) (string, error) {
	r := reflect.ValueOf(a)
	items := make([]any, r.Len())
	for i := range items {
		v := encode(r.Index(i).Interface())
		if raw, ok := r.Index(i).Interface().(json.RawMessage); ok {
			v = raw
		}
		items[i] = v
	}
	b, err := json.Marshal(items)
	return string(b), err
}

// isList reports whether a is a list argument: a slice other than bytes,
// such as a blob, or JSON.
func isList(a any) bool {
	switch a.(type) {
	case []byte, json.RawMessage:
		return false
	}
	r := reflect.ValueOf(a)
	return r.Kind() == reflect.Slice && r.Type().Elem().Kind() != reflect.Uint8
}

// weakValue is a weak column's value (weak.Weak), which holds a typed or a
// raw value.
type weakValue interface{ Any() any }

// encode turns a parameter's value, in dql's host types, into one the
// driver encodes. SQLite has no time, decimal or JSON types, so these are
// written as text: a decimal, a civil type and JSON as their text, and an
// instant as RFC 3339 in UTC, which is what the generated decoders read
// back (§8.10). A bool is 1 or 0, a weak value is its typed or raw value,
// a raw value is its storage class's, and a pointer is its target or nil.
// Any other value passes to the driver, which encodes a named string or
// integer type, such as a generated enum, as its underlying type, and a
// driver.Valuer, such as a uuid, as its value.
func encode(v any) any {
	switch x := v.(type) {
	case bool:
		if x {
			return int64(1)
		}
		return int64(0)
	case json.RawMessage:
		return string(x)
	case dec.Decimal:
		return x.String()
	case civil.Date:
		return x.String()
	case civil.DateTime:
		return x.String()
	case civil.Time:
		return x.String()
	case civil.OffsetTime:
		return x.String()
	case civil.OffsetDateTime:
		return x.String()
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	case storage.Integer:
		return int64(x)
	case storage.Real:
		return float64(x)
	case storage.Text:
		return string(x)
	case storage.Blob:
		return []byte(x)
	case weakValue:
		return encode(x.Any())
	}
	if r := reflect.ValueOf(v); r.Kind() == reflect.Pointer {
		if r.IsNil() {
			return nil
		}
		return encode(r.Elem().Interface())
	}
	return v
}
