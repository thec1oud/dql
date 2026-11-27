package dqlsqlserver

import (
	"encoding/json"
	"errors"
	"reflect"
	"time"

	gcivil "github.com/golang-sql/civil"
	"github.com/google/uuid"
	mssql "github.com/microsoft/go-mssqldb"

	"github.com/thec1oud/dql/runtime/go/civil"
	"github.com/thec1oud/dql/runtime/go/dec"
	"github.com/thec1oud/dql/runtime/go/dql"
)

// arguments returns a statement's arguments as the driver takes them. SQL
// Server has no array parameter, so a list is one placeholder holding a
// JSON array (§25.3), which the dialect expands with OPENJSON as the
// compared column's type: each item is written as the text that type
// reads, a time to the 100 ns SQL Server keeps.
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
		case mssql.UniqueIdentifier:
			v = x.String()
		case gcivil.Date:
			v = x.String()
		case gcivil.DateTime:
			v = x.In(time.UTC).Format("2006-01-02T15:04:05.9999999")
		case gcivil.Time:
			v = time.Date(1, 1, 1, x.Hour, x.Minute, x.Second, x.Nanosecond, time.UTC).Format("15:04:05.9999999")
		case time.Time:
			v = x.Format("2006-01-02T15:04:05.9999999Z07:00")
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

// ErrNoType is the error for a value of a type SQL Server has no column
// for: an interval or a bit string.
var ErrNoType = errors.New("dqlsqlserver: SQL Server has no interval or bit-string type")

// encode turns a parameter's value, in dql's host types, into one the
// driver encodes: a decimal into its text, which SQL Server converts to
// the column's type, a civil date or time into the driver's own, which it
// sends as date, datetime2 or time, an OffsetDateTime into a time in its
// offset, which it sends as datetimeoffset, a UUID into a
// uniqueidentifier, and a pointer into its target or nil. Any other value
// passes to the driver, which encodes a named string or integer type, such
// as a generated enum, as its underlying type.
func encode(v any) (any, error) {
	switch x := v.(type) {
	case json.RawMessage:
		return string(x), nil
	case dec.Decimal:
		return x.String(), nil
	case civil.Date:
		return gcivil.Date{Year: x.Year, Month: x.Month, Day: x.Day}, nil
	case civil.DateTime:
		return gcivil.DateTime{
			Date: gcivil.Date{Year: x.Date.Year, Month: x.Date.Month, Day: x.Date.Day},
			Time: gcivil.Time{Hour: x.Time.Hour, Minute: x.Time.Minute, Second: x.Time.Second, Nanosecond: x.Time.Nanosecond},
		}, nil
	case civil.Time:
		return gcivil.Time{Hour: x.Hour, Minute: x.Minute, Second: x.Second, Nanosecond: x.Nanosecond}, nil
	case civil.OffsetDateTime:
		return x.Instant().In(time.FixedZone("", x.Offset)), nil
	case uuid.UUID:
		return mssql.UniqueIdentifier(x), nil
	case dql.Interval, dql.Bits, civil.OffsetTime:
		return nil, ErrNoType
	}
	if r := reflect.ValueOf(v); r.Kind() == reflect.Pointer {
		if r.IsNil() {
			return nil, nil
		}
		return encode(r.Elem().Interface())
	}
	return v, nil
}
