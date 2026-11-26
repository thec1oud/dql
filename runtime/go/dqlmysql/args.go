package dqlmysql

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/thec1oud/dql/runtime/go/civil"
	"github.com/thec1oud/dql/runtime/go/dec"
	"github.com/thec1oud/dql/runtime/go/dql"
)

// statement returns a statement's text and arguments as the driver takes
// them. MySQL has no array parameter, so the dialect writes a list as one
// placeholder (§9.1), which statement expands into one placeholder for
// each item, or into NULL for an empty list, so that x IN (NULL) holds for
// no row.
func statement(q string, args []any) (string, []any, error) {
	lists := false
	for _, a := range args {
		lists = lists || isList(a)
	}
	if !lists {
		out := make([]any, len(args))
		for i, a := range args {
			v, err := encode(a)
			if err != nil {
				return "", nil, err
			}
			out[i] = v
		}
		return q, out, nil
	}
	var b strings.Builder
	var out []any
	n := 0
	var failed error
	scan(q, func(text string, placeholder bool) {
		if !placeholder {
			b.WriteString(text)
			return
		}
		if n >= len(args) {
			b.WriteString(text)
			return
		}
		a := args[n]
		n++
		items := []any{a}
		if isList(a) {
			r := reflect.ValueOf(a)
			items = make([]any, r.Len())
			for i := range items {
				items[i] = r.Index(i).Interface()
			}
			if len(items) == 0 {
				b.WriteString("NULL")
				return
			}
		}
		for i, it := range items {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteByte('?')
			v, err := encode(it)
			if err != nil && failed == nil {
				failed = err
			}
			out = append(out, v)
		}
	})
	if failed != nil {
		return "", nil, failed
	}
	return b.String(), out, nil
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

// scan splits q into text and placeholders, calling f with each in order.
// A ? in a string, a quoted identifier or a comment is text. Strings may
// escape with a backslash or a doubled quote, so either is read as an
// escape, which is right under every sql_mode for the statements the
// dialect writes.
func scan(q string, f func(text string, placeholder bool)) {
	start := 0
	for i := 0; i < len(q); i++ {
		switch c := q[i]; {
		case c == '?':
			f(q[start:i], false)
			f("?", true)
			start = i + 1
		case c == '\'' || c == '"' || c == '`':
			i = quoted(q, i, c)
		case c == '#' || c == '-' && strings.HasPrefix(q[i:], "-- "):
			if j := strings.IndexByte(q[i:], '\n'); j >= 0 {
				i += j
			} else {
				i = len(q)
			}
		case c == '/' && strings.HasPrefix(q[i:], "/*"):
			if j := strings.Index(q[i+2:], "*/"); j >= 0 {
				i += j + 3
			} else {
				i = len(q)
			}
		}
	}
	f(q[start:], false)
}

// quoted returns the index of the quote that closes the quoted text
// starting at q[i], or the end of q.
func quoted(q string, i int, quote byte) int {
	for i++; i < len(q); i++ {
		switch {
		case q[i] == '\\' && quote != '`':
			i++
		case q[i] == quote:
			if i+1 < len(q) && q[i+1] == quote {
				i++
				continue
			}
			return i
		}
	}
	return len(q)
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
