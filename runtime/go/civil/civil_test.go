package civil_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/thec1oud/dql/runtime/go/civil"
)

func TestOf(t *testing.T) {
	loc := time.FixedZone("", 5*3600+30*60)
	tm := time.Date(2026, time.October, 23, 13, 32, 20, 500, loc)
	if got := civil.DateOf(tm); got != (civil.Date{2026, time.October, 23}) {
		t.Errorf("DateOf = %v", got)
	}
	if got := civil.TimeOf(tm); got != (civil.Time{13, 32, 20, 500}) {
		t.Errorf("TimeOf = %v", got)
	}
	dt := civil.DateTimeOf(tm)
	if got := dt.In(loc); !got.Equal(tm) {
		t.Errorf("DateTimeOf(t).In = %v", got)
	}
	odt := civil.OffsetDateTimeOf(tm)
	if odt.Offset != 19800 || odt.DateTime != dt {
		t.Errorf("OffsetDateTimeOf = %v", odt)
	}
	if got := odt.Instant(); !got.Equal(tm) {
		t.Errorf("Instant = %v", got)
	}
	if got := dt.Date.In(time.UTC); !got.Equal(time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Date.In = %v", got)
	}
}

func TestIsValid(t *testing.T) {
	valid := []interface{ IsValid() bool }{
		civil.Date{2024, time.February, 29},
		civil.Date{2000, time.February, 29},
		civil.Date{0, time.February, 29},
		civil.Date{-4712, time.January, 1},
		civil.Date{2026, time.April, 30},
		civil.Time{23, 59, 59, 999999999},
		civil.DateTime{civil.Date{1, 1, 1}, civil.Time{}},
		civil.OffsetTime{civil.Time{}, -86399},
		civil.OffsetDateTime{civil.DateTime{civil.Date{1, 1, 1}, civil.Time{}}, 86399},
	}
	for _, v := range valid {
		if !v.IsValid() {
			t.Errorf("%#v is not valid", v)
		}
	}
	invalid := []interface{ IsValid() bool }{
		civil.Date{},
		civil.Date{2023, time.February, 29},
		civil.Date{1900, time.February, 29},
		civil.Date{2026, time.April, 31},
		civil.Date{2026, 13, 1},
		civil.Date{2026, 1, 0},
		civil.Time{24, 0, 0, 0},
		civil.Time{-1, 0, 0, 0},
		civil.Time{0, 60, 0, 0},
		civil.Time{0, -1, 0, 0},
		civil.Time{0, 0, 60, 0},
		civil.Time{0, 0, -1, 0},
		civil.Time{0, 0, 0, 1e9},
		civil.Time{0, 0, 0, -1},
		civil.DateTime{civil.Date{}, civil.Time{}},
		civil.DateTime{civil.Date{1, 1, 1}, civil.Time{Hour: 24}},
		civil.OffsetTime{civil.Time{}, 86400},
		civil.OffsetTime{civil.Time{Hour: 24}, 0},
		civil.OffsetDateTime{civil.DateTime{civil.Date{1, 1, 1}, civil.Time{}}, -86400},
		civil.OffsetDateTime{civil.DateTime{}, 0},
	}
	for _, v := range invalid {
		if v.IsValid() {
			t.Errorf("%#v is valid", v)
		}
	}
}

func TestDayArithmetic(t *testing.T) {
	// Checked against the time package over a span that crosses leap years,
	// century years and year 0.
	start := time.Date(-801, time.March, 1, 0, 0, 0, 0, time.UTC)
	base := civil.DateOf(start)
	for n := 0; n < 800*366; n += 7 {
		tm := start.AddDate(0, 0, n)
		want := civil.DateOf(tm)
		got := base.AddDays(n)
		if got != want {
			t.Fatalf("AddDays(%d) = %v, want %v", n, got, want)
		}
		if d := got.DaysSince(base); d != n {
			t.Fatalf("DaysSince = %d, want %d", d, n)
		}
		if w := got.Weekday(); w != tm.Weekday() {
			t.Fatalf("%v.Weekday() = %v, want %v", got, w, tm.Weekday())
		}
	}
	// Postgres's whole date range: 4713 BC to 5874897 AD.
	lo := civil.Date{-4712, time.January, 1}
	hi := civil.Date{5874897, time.December, 31}
	if got := lo.AddDays(hi.DaysSince(lo)); got != hi {
		t.Fatalf("round trip = %v", got)
	}
	if got := hi.AddDays(-1); got != (civil.Date{5874897, time.December, 30}) {
		t.Fatalf("AddDays(-1) = %v", got)
	}
	if got := (civil.Date{1970, 1, 1}).AddDays(-1); got != (civil.Date{1969, 12, 31}) {
		t.Fatalf("AddDays(-1) = %v", got)
	}
}

func TestCompare(t *testing.T) {
	d1, d2 := civil.Date{2026, 1, 31}, civil.Date{2026, 2, 1}
	if d1.Compare(d2) != -1 || d2.Compare(d1) != 1 || d1.Compare(d1) != 0 {
		t.Error("Date.Compare")
	}
	if !d1.Before(d2) || d1.After(d2) || !d2.After(d1) {
		t.Error("Date.Before/After")
	}
	t1, t2 := civil.Time{1, 2, 3, 4}, civil.Time{1, 2, 3, 5}
	if t1.Compare(t2) != -1 || t2.Compare(t1) != 1 || t1.Compare(t1) != 0 {
		t.Error("Time.Compare")
	}
	if !t1.Before(t2) || t1.After(t2) || !t2.After(t1) {
		t.Error("Time.Before/After")
	}
	a := civil.DateTime{d1, t2}
	b := civil.DateTime{d2, t1}
	c := civil.DateTime{d1, t1}
	if a.Compare(b) != -1 || b.Compare(a) != 1 || a.Compare(c) != 1 || a.Compare(a) != 0 {
		t.Error("DateTime.Compare")
	}
	if !a.Before(b) || a.After(b) || !b.After(a) {
		t.Error("DateTime.Before/After")
	}
}

func TestText(t *testing.T) {
	tests := []struct {
		v    interface{ String() string }
		text string
	}{
		{civil.Date{2026, 10, 23}, "2026-10-23"},
		{civil.Date{0, 1, 1}, "0000-01-01"},
		{civil.Date{9999, 12, 31}, "9999-12-31"},
		{civil.Date{10000, 1, 1}, "+010000-01-01"},
		{civil.Date{5874897, 12, 31}, "+5874897-12-31"},
		{civil.Date{-1, 1, 1}, "-000001-01-01"},
		{civil.Date{-4712, 1, 1}, "-004712-01-01"},
		{civil.Time{13, 32, 20, 0}, "13:32:20"},
		{civil.Time{13, 32, 20, 500000000}, "13:32:20.5"},
		{civil.Time{0, 0, 0, 1}, "00:00:00.000000001"},
		{civil.Time{0, 0, 0, 123456000}, "00:00:00.123456"},
		{civil.DateTime{civil.Date{2026, 10, 23}, civil.Time{13, 32, 20, 0}}, "2026-10-23T13:32:20"},
		{civil.OffsetTime{civil.Time{13, 32, 0, 0}, 0}, "13:32:00+00:00"},
		{civil.OffsetTime{civil.Time{13, 32, 0, 0}, 19800}, "13:32:00+05:30"},
		{civil.OffsetTime{civil.Time{13, 32, 0, 0}, -(15*3600 + 59*60 + 59)}, "13:32:00-15:59:59"},
		{civil.OffsetDateTime{civil.DateTime{civil.Date{2026, 10, 23}, civil.Time{13, 0, 0, 0}}, -3600}, "2026-10-23T13:00:00-01:00"},
		{civil.OffsetDateTime{civil.DateTime{civil.Date{-1, 10, 23}, civil.Time{13, 0, 0, 0}}, 3600}, "-000001-10-23T13:00:00+01:00"},
	}
	for _, tt := range tests {
		if got := tt.v.String(); got != tt.text {
			t.Errorf("String() = %q, want %q", got, tt.text)
		}
		b, err := json.Marshal(tt.v)
		if err != nil || string(b) != `"`+tt.text+`"` {
			t.Errorf("Marshal(%v) = %s, %v", tt.v, b, err)
		}
		var err2 error
		var got any
		switch tt.v.(type) {
		case civil.Date:
			got, err2 = civil.ParseDate(tt.text)
		case civil.Time:
			got, err2 = civil.ParseTime(tt.text)
		case civil.DateTime:
			got, err2 = civil.ParseDateTime(tt.text)
		case civil.OffsetTime:
			got, err2 = civil.ParseOffsetTime(tt.text)
		case civil.OffsetDateTime:
			got, err2 = civil.ParseOffsetDateTime(tt.text)
		}
		if err2 != nil || got != tt.v {
			t.Errorf("Parse(%q) = %v, %v", tt.text, got, err2)
		}
	}
}

func TestParseVariants(t *testing.T) {
	if got, err := civil.ParseTime("13:32"); err != nil || got != (civil.Time{13, 32, 0, 0}) {
		t.Errorf("ParseTime(13:32) = %v, %v", got, err)
	}
	if got, err := civil.ParseDate("+001999-01-02"); err != nil || got != (civil.Date{1999, 1, 2}) {
		t.Errorf("ParseDate = %v, %v", got, err)
	}
	for _, s := range []string{"13:32Z", "13:32z"} {
		if got, err := civil.ParseOffsetTime(s); err != nil || got != (civil.OffsetTime{civil.Time{13, 32, 0, 0}, 0}) {
			t.Errorf("ParseOffsetTime(%q) = %v, %v", s, got, err)
		}
	}
	if got, err := civil.ParseOffsetTime("13:32-00:00:30"); err != nil || got.Offset != -30 {
		t.Errorf("ParseOffsetTime = %v, %v", got, err)
	}
	if got, err := civil.ParseOffsetDateTime("2026-10-23T13:32Z"); err != nil || got.Offset != 0 {
		t.Errorf("ParseOffsetDateTime = %v, %v", got, err)
	}
}

func TestParseErrors(t *testing.T) {
	dates := []string{
		"", "202", "2026", "2026-1-01", "2026/10/23", "2026-10-2x", "2026-1x-23",
		"20x6-10-23", "2026-02-30", "2026-10-23T", "+02026-10-23", "-000000-01-01",
		"+0x0000-01-01", "+010000", "+99999999999999999999999-01-01", " 2026-10-23",
	}
	for _, s := range dates {
		if _, err := civil.ParseDate(s); err == nil {
			t.Errorf("ParseDate(%q) succeeded", s)
		}
	}
	times := []string{
		"", "1:00", "13-32", "1x:32", "13:3x", "13:32:", "13:32:0", "13:32:0x",
		"13:32:00.", "13:32:00,5", "13:32:00.1234567890", "13:32:00.12x",
		"24:00", "23:60", "23:59:60", "13:32:00Z",
	}
	for _, s := range times {
		if _, err := civil.ParseTime(s); err == nil {
			t.Errorf("ParseTime(%q) succeeded", s)
		}
	}
	dateTimes := []string{"", "2026-10-23", "2026-10-23 13:32", "2026-10-23T25:00", "2026-13-23T13:00"}
	for _, s := range dateTimes {
		if _, err := civil.ParseDateTime(s); err == nil {
			t.Errorf("ParseDateTime(%q) succeeded", s)
		}
	}
	offsetTimes := []string{
		"13:32", "13:32+5", "13:32+05:3", "13:32+05-30", "13:32+05:30-10", "13:32+0x:30",
		"13:32+05:x0", "13:32+05:30:x0", "13:32+05:60", "13:32+05:30:60", "13:32+24:00",
		"13:32Z05:30", "13:32+05:30:0", "1x:32+05:30",
	}
	for _, s := range offsetTimes {
		if _, err := civil.ParseOffsetTime(s); err == nil {
			t.Errorf("ParseOffsetTime(%q) succeeded", s)
		}
	}
	offsetDateTimes := []string{
		"2026-10-23", "2026-10-23T13:32", "2026-10-23T13:32+24:00", "2026-10-32T13:32Z",
	}
	for _, s := range offsetDateTimes {
		if _, err := civil.ParseOffsetDateTime(s); err == nil {
			t.Errorf("ParseOffsetDateTime(%q) succeeded", s)
		}
	}
}

func TestMarshalInvalid(t *testing.T) {
	for _, v := range []any{
		civil.Date{}, civil.Time{Hour: 24}, civil.DateTime{},
		civil.OffsetTime{Offset: 86400}, civil.OffsetDateTime{},
	} {
		if _, err := json.Marshal(v); err == nil {
			t.Errorf("Marshal(%#v) succeeded", v)
		}
	}
}

func TestUnmarshal(t *testing.T) {
	var v struct {
		D   civil.Date
		T   civil.Time
		DT  civil.DateTime
		OT  civil.OffsetTime
		ODT civil.OffsetDateTime
	}
	in := `{"D":"2026-10-23","T":"13:32:20","DT":"2026-10-23T13:32:20","OT":"13:32:20+03:00","ODT":"2026-10-23T13:32:20+03:00"}`
	if err := json.Unmarshal([]byte(in), &v); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(v)
	if err != nil || string(out) != in {
		t.Fatalf("round trip = %s, %v", out, err)
	}
	for _, field := range []string{"D", "T", "DT", "OT", "ODT"} {
		if err := json.Unmarshal([]byte(`{"`+field+`":"x"}`), &v); err == nil {
			t.Errorf("%s: x decoded", field)
		}
	}
}
