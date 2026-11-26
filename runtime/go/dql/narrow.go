package dql

import (
	"time"

	"github.com/thec1oud/dql/runtime/go/civil"
)

// The converters below decode a column an annotation narrowed from the type
// the database stores (§7.2, §13): the stored form is converted and checked,
// and a value that does not fit is off type, which Asserted names.

// Parsed converts a value of type T, or text that parse reads, for a text
// column as(<logical type>) narrows, such as a CHAR(36) holding a uuid.
func Parsed[T any](parse func(string) (T, error)) func(any) (T, error) {
	return func(v any) (T, error) {
		switch x := v.(type) {
		case T:
			return x, nil
		case string:
			if t, err := parse(x); err == nil {
				return t, nil
			}
		}
		var zero T
		return zero, Fail(OffType)
	}
}

// ParseInstant parses RFC 3339 text, or YYYY-MM-DD HH:MM:SS with an
// optional fraction read as UTC, which is how a MySQL driver writes a
// time.Time into a text column.
func ParseInstant(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC(), nil
	}
	return time.Parse("2006-01-02 15:04:05.999999999", s)
}

// TimeOfDay converts a civil.Time, or an interval of no months or days
// from zero up to 24 hours, such as a MySQL TIME as(local_time) narrows.
func TimeOfDay(v any) (civil.Time, error) {
	switch x := v.(type) {
	case civil.Time:
		return x, nil
	case Interval:
		us := x.Microseconds
		if x.Months == 0 && x.Days == 0 && us >= 0 && us < 24*usPerHour {
			return civil.Time{Hour: int(us / usPerHour), Minute: int(us / usPerMinute % 60),
				Second: int(us / usPerSecond % 60), Nanosecond: int(us%usPerSecond) * 1000}, nil
		}
	}
	return civil.Time{}, Fail(OffType)
}
