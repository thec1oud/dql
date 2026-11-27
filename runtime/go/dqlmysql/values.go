package dqlmysql

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/thec1oud/dql/runtime/go/civil"
	"github.com/thec1oud/dql/runtime/go/dec"
	"github.com/thec1oud/dql/runtime/go/dql"
)

// decode turns one value the driver scanned, from a column of the named
// database type, into its dql.Row form. The driver gives integers and
// floats as Go numbers, except an unsigned BIGINT past the largest int64,
// and dates and times, read in UTC, as time.Time; everything else comes as
// bytes, which the type tells apart.
//
// A BIT comes as its bytes without its length, so it decodes as dql.Bits
// of every bit of those bytes: a BIT(3) holding b'101' is 00000101, which
// the generated decoder cuts to the column's length (dql.FixedBits).
func decode(typ string, v any) (any, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case float32:
		return float64(x), nil
	case int64:
		if typ == "YEAR" {
			// YEAR is opaque, which falls back to text (§8.3).
			return strconv.FormatInt(x, 10), nil
		}
		return x, nil
	case time.Time:
		switch typ {
		case "DATE":
			return civil.DateOf(x), nil
		case "DATETIME":
			return civil.DateTimeOf(x), nil
		}
		return x.UTC(), nil
	case []byte:
		return decodeBytes(typ, x)
	}
	return v, nil
}

func decodeBytes(typ string, b []byte) (any, error) {
	var v any
	var err error
	switch {
	case typ == "UNSIGNED BIGINT":
		v, err = strconv.ParseUint(string(b), 10, 64)
	case typ == "DECIMAL":
		v, err = dec.Parse(string(b))
	case typ == "TIME":
		v, err = parseTime(string(b))
	case typ == "JSON":
		return json.RawMessage(b), nil
	case typ == "BIT":
		return dql.NewBits(b, 8*len(b))
	case strings.Contains(typ, "BLOB") || strings.Contains(typ, "BINARY") || typ == "GEOMETRY" || typ == "VECTOR":
		return b, nil
	default:
		return string(b), nil
	}
	if err != nil {
		return nil, fmt.Errorf("dqlmysql: %s value %q: %w", typ, b, err)
	}
	return v, nil
}

// parseTime reads a TIME, [-]H:MM:SS[.ffffff], as an interval of
// microseconds.
func parseTime(s string) (dql.Interval, error) {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	frac := ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		s, frac = s[:i], s[i+1:]
	}
	parts := strings.Split(s, ":")
	if len(parts) != 3 || len(frac) > 6 {
		return dql.Interval{}, fmt.Errorf("not a time")
	}
	var us int64
	for i, p := range append(parts, (frac + "000000")[:6]) {
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil || n < 0 {
			return dql.Interval{}, fmt.Errorf("not a time")
		}
		us = us*[]int64{1, 60, 60, 1_000_000}[i] + n
	}
	if neg {
		us = -us
	}
	return dql.Interval{Microseconds: us}, nil
}
