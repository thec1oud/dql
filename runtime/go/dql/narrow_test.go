package dql_test

import (
	"errors"
	"testing"
	"time"

	"github.com/thec1oud/dql/runtime/go/civil"
	"github.com/thec1oud/dql/runtime/go/dql"
)

func offType(err error) bool {
	var de *dql.DecodeError
	return errors.As(err, &de) && de.Reason == dql.OffType
}

// §7.2, §13: a narrowed column decodes from its stored form, and a stored
// value that does not fit is off type.
func TestNarrowed(t *testing.T) {
	date := dql.Parsed(civil.ParseDate)
	for _, v := range []any{"2026-11-26", civil.Date{Year: 2026, Month: 11, Day: 26}} {
		if d, err := date(v); err != nil || d != (civil.Date{Year: 2026, Month: 11, Day: 26}) {
			t.Errorf("Parsed(%v) = %v, %v", v, d, err)
		}
	}
	for _, v := range []any{"26/11/2026", int64(1)} {
		if _, err := date(v); !offType(err) {
			t.Errorf("Parsed(%v) = %v", v, err)
		}
	}

	want := time.Date(2026, 11, 26, 9, 30, 0, 500_000_000, time.UTC)
	for _, s := range []string{"2026-11-26T10:30:00.5+01:00", "2026-11-26 09:30:00.5"} {
		if got, err := dql.ParseInstant(s); err != nil || !got.Equal(want) || got.Location() != time.UTC {
			t.Errorf("ParseInstant(%q) = %v, %v", s, got, err)
		}
	}
	if _, err := dql.ParseInstant("tomorrow"); err == nil {
		t.Error("ParseInstant(tomorrow) succeeded")
	}

	noon := civil.Time{Hour: 12, Minute: 30, Second: 15, Nanosecond: 250_000_000}
	for _, v := range []any{noon, dql.Interval{Microseconds: 45_015_250_000}} {
		if got, err := dql.TimeOfDay(v); err != nil || got != noon {
			t.Errorf("TimeOfDay(%v) = %v, %v", v, got, err)
		}
	}
	for _, v := range []any{dql.Interval{Microseconds: -1}, dql.Interval{Microseconds: 86_400_000_000},
		dql.Interval{Days: 1}, dql.Interval{Months: 1}, "12:00"} {
		if _, err := dql.TimeOfDay(v); !offType(err) {
			t.Errorf("TimeOfDay(%v) = %v", v, err)
		}
	}
}
