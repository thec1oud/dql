package dqlpgx

import (
	"encoding/json"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/thec1oud/dql/runtime/go/civil"
	"github.com/thec1oud/dql/runtime/go/dec"
	"github.com/thec1oud/dql/runtime/go/dql"
)

// The OIDs of built-in types that pgx has no type or Go value for, or whose
// value the OID alone tells apart.
const (
	oidJSON   = 114
	oidXML    = 142
	oidMoney  = 790
	oidTimeTZ = 1266
)

const usPerDay = 24 * 60 * 60 * 1_000_000

// decode turns one column's wire value into its dql.Row form (dql.Row): pgx
// decodes it, and convert maps pgx's value onto dql's host type. A type pgx
// does not know, such as an enum it has not registered, keeps its text.
func decode(m *pgtype.Map, oid uint32, format int16, raw []byte) (any, error) {
	if raw == nil {
		return nil, nil
	}
	t, ok := m.TypeForOID(oid)
	if !ok {
		if format == pgtype.TextFormatCode {
			return convert(m, oid, string(raw)), nil
		}
		return append([]byte(nil), raw...), nil
	}
	switch c := t.Codec.(type) {
	case *pgtype.JSONCodec, *pgtype.JSONBCodec:
		if _, jsonb := c.(*pgtype.JSONBCodec); jsonb && format == pgtype.BinaryFormatCode {
			// Binary jsonb starts with its format's version.
			raw = raw[1:]
		}
		return json.RawMessage(append([]byte(nil), raw...)), nil
	case *pgtype.ArrayCodec:
		// An array scans with its dimensions, which its decoded value loses.
		var a pgtype.Array[any]
		if err := m.Scan(oid, format, raw, &a); err != nil {
			return nil, err
		}
		return reshape(convertAll(m, c.ElementType.OID, a.Elements), a.Dims), nil
	}
	v, err := t.Codec.DecodeValue(m, oid, format, raw)
	if err != nil {
		return nil, err
	}
	return convert(m, oid, v), nil
}

// reshape nests a flat array's elements, one []any for each dimension.
func reshape(flat []any, dims []pgtype.ArrayDimension) []any {
	if len(dims) <= 1 {
		return flat
	}
	n := len(flat) / int(dims[0].Length)
	out := make([]any, dims[0].Length)
	for i := range out {
		out[i] = reshape(flat[i*n:(i+1)*n], dims[1:])
	}
	return out
}

func convertAll(m *pgtype.Map, oid uint32, vs []any) []any {
	out := make([]any, len(vs))
	for i, v := range vs {
		out[i] = convert(m, oid, v)
	}
	return out
}

// convert maps a value pgx decoded for a column of type oid onto dql's
// host type. A value dql has no host type for, such as an infinite date or
// a numeric NaN, stays as pgx decoded it, and a generated decoder rejects
// it as off type.
func convert(m *pgtype.Map, oid uint32, v any) any {
	if t, ok := m.TypeForOID(oid); ok {
		switch c := t.Codec.(type) {
		case *pgtype.CompositeCodec:
			if fields, ok := v.(map[string]any); ok {
				row := make(dql.Row, len(c.Fields))
				for i, f := range c.Fields {
					row[i] = convert(m, f.Type.OID, fields[f.Name])
				}
				return row
			}
		case *pgtype.ArrayCodec:
			// An array in a composite value, decoded flat.
			if vs, ok := v.([]any); ok {
				return convertAll(m, c.ElementType.OID, vs)
			}
		case *pgtype.JSONCodec, *pgtype.JSONBCodec:
			// JSON in an array or a composite value, decoded into Go values.
			if b, err := json.Marshal(v); err == nil {
				return json.RawMessage(b)
			}
		case *pgtype.RangeCodec:
			if r, ok := v.(pgtype.Range[any]); ok {
				if out, ok := rangeOf(m, c.ElementType.OID, r); ok {
					return out
				}
			}
		case *pgtype.MultirangeCodec:
			if rs, ok := v.(pgtype.Multirange[pgtype.Range[any]]); ok {
				if out, ok := multirangeOf(m, c.ElementType, rs); ok {
					return out
				}
			}
		}
	}
	switch x := v.(type) {
	case int16:
		return int64(x)
	case int32:
		return int64(x)
	case uint32:
		return int64(x)
	case float32:
		return float64(x)
	case [16]byte:
		return uuid.UUID(x)
	case time.Time:
		switch oid {
		case pgtype.DateOID:
			return civil.DateOf(x)
		case pgtype.TimestampOID:
			return civil.DateTimeOf(x)
		}
		return x.UTC()
	case pgtype.Time:
		if x.Microseconds < usPerDay {
			return civilTime(x.Microseconds)
		}
	case pgtype.Interval:
		return dql.Interval{Months: x.Months, Days: x.Days, Microseconds: x.Microseconds}
	case pgtype.Numeric:
		if x.Int != nil {
			return dec.NewFromBigInt(x.Int, x.Exp)
		}
	case netip.Prefix:
		if oid == pgtype.InetOID && x.Bits() == x.Addr().BitLen() {
			return x.Addr()
		}
	case pgtype.Bits:
		if b, err := dql.NewBits(x.Bytes, int(x.Len)); err == nil {
			return b
		}
	case []byte:
		if oid == oidXML {
			return string(x)
		}
	case string:
		switch oid {
		case oidTimeTZ:
			if t, ok := offsetTime(x); ok {
				return t
			}
		case oidMoney:
			if d, ok := money(x); ok {
				return d
			}
		}
	}
	return v
}

func civilTime(us int64) civil.Time {
	return civil.Time{
		Hour:       int(us / 3_600_000_000),
		Minute:     int(us / 60_000_000 % 60),
		Second:     int(us / 1_000_000 % 60),
		Nanosecond: int(us % 1_000_000 * 1000),
	}
}

// offsetTime parses a timetz in Postgres's output, whose offset may be
// whole hours, such as 12:34:56+02.
func offsetTime(s string) (civil.OffsetTime, bool) {
	if i := strings.LastIndexAny(s, "+-"); i >= 0 && len(s)-i == 3 {
		s += ":00"
	}
	t, err := civil.ParseOffsetTime(s)
	return t, err == nil
}

// money parses money in Postgres's output for a locale that writes a
// decimal point, such as C or en_US: -$1,234.50.
func money(s string) (dec.Decimal, bool) {
	s = strings.NewReplacer("$", "", ",", "").Replace(s)
	d, err := dec.Parse(s)
	return d, err == nil
}

// rangeOf converts a range of a built-in range type: dql.Range of the
// element's host type.
func rangeOf(m *pgtype.Map, elem uint32, r pgtype.Range[any]) (any, bool) {
	switch elem {
	case pgtype.Int4OID:
		return typedRange(r, func(v any) (int32, bool) { x, ok := v.(int32); return x, ok })
	case pgtype.Int8OID:
		return typedRange(r, func(v any) (int64, bool) { x, ok := v.(int64); return x, ok })
	case pgtype.NumericOID:
		return typedRange(r, hostOf[dec.Decimal](m, elem))
	case pgtype.DateOID:
		return typedRange(r, hostOf[civil.Date](m, elem))
	case pgtype.TimestampOID:
		return typedRange(r, hostOf[civil.DateTime](m, elem))
	case pgtype.TimestamptzOID:
		return typedRange(r, hostOf[time.Time](m, elem))
	}
	return nil, false
}

// hostOf returns a function that converts a value of type oid to its host
// type T.
func hostOf[T any](m *pgtype.Map, oid uint32) func(any) (T, bool) {
	return func(v any) (T, bool) {
		x, ok := convert(m, oid, v).(T)
		return x, ok
	}
}

func typedRange[T any](r pgtype.Range[any], conv func(any) (T, bool)) (dql.Range[T], bool) {
	if r.LowerType == pgtype.Empty {
		return dql.EmptyRange[T](), true
	}
	lower, ok1 := bound(r.LowerType, r.Lower, conv)
	upper, ok2 := bound(r.UpperType, r.Upper, conv)
	return dql.NewRange(lower, upper), ok1 && ok2
}

func bound[T any](k pgtype.BoundType, v any, conv func(any) (T, bool)) (dql.Bound[T], bool) {
	if k == pgtype.Unbounded {
		return dql.UnboundedBound[T](), true
	}
	x, ok := conv(v)
	if k == pgtype.Inclusive {
		return dql.InclusiveBound(x), ok
	}
	return dql.ExclusiveBound(x), ok
}

// multirangeOf converts a multirange of a built-in range type: a slice of
// dql.Range of the element's host type.
func multirangeOf(m *pgtype.Map, r *pgtype.Type, rs pgtype.Multirange[pgtype.Range[any]]) (any, bool) {
	elem := r.Codec.(*pgtype.RangeCodec).ElementType.OID
	switch elem {
	case pgtype.Int4OID:
		return typedRanges[int32](m, elem, rs)
	case pgtype.Int8OID:
		return typedRanges[int64](m, elem, rs)
	case pgtype.NumericOID:
		return typedRanges[dec.Decimal](m, elem, rs)
	case pgtype.DateOID:
		return typedRanges[civil.Date](m, elem, rs)
	case pgtype.TimestampOID:
		return typedRanges[civil.DateTime](m, elem, rs)
	case pgtype.TimestamptzOID:
		return typedRanges[time.Time](m, elem, rs)
	}
	return nil, false
}

func typedRanges[T any](m *pgtype.Map, elem uint32, rs pgtype.Multirange[pgtype.Range[any]]) ([]dql.Range[T], bool) {
	out := make([]dql.Range[T], len(rs))
	for i, r := range rs {
		x, ok := rangeOf(m, elem, r)
		if !ok {
			return nil, false
		}
		out[i] = x.(dql.Range[T])
	}
	return out, true
}
