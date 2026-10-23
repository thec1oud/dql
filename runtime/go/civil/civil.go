// Package civil holds dates and wall-clock times that have no location
// (design record §8.8).
//
// Only an instant maps to time.Time: a local date or time in time.Time would
// silently gain a location (§8.8, rule 1). The types here hold the reading
// itself. Dates use the proleptic Gregorian calendar with astronomical year
// numbering (year 0 is 1 BC); times have nanosecond precision.
//
// The text form of every type is that of ISO 8601 and RFC 9557, as Temporal
// writes it: years outside 0000 to 9999 have a sign and at least six digits,
// fractional seconds drop trailing zeros, and offsets are ±HH:MM, with :SS
// when they have seconds.
//
// The types hold finite values only. Postgres date and timestamp ±infinity
// and time 24:00:00 have no civil value: decoding one is a DecodeError with
// reason off_type (§13), as in every emitter.
package civil

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Date is a calendar date.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// Time is a time of day, from 00:00:00 to 23:59:59.999999999.
type Time struct {
	Hour       int
	Minute     int
	Second     int
	Nanosecond int
}

// DateTime is a wall-clock reading: a date and a time of day, with no zone.
type DateTime struct {
	Date Date
	Time Time
}

// OffsetTime is a time of day and a UTC offset in seconds east of UTC, such as
// a Postgres timetz.
type OffsetTime struct {
	Time   Time
	Offset int
}

// OffsetDateTime is a point in time and the offset it was recorded with, in
// seconds east of UTC, such as a SQL Server datetimeoffset.
type OffsetDateTime struct {
	DateTime DateTime
	Offset   int
}

// maxOffset bounds an offset's magnitude, as RFC 9557 and Temporal do.
const maxOffset = 24*60*60 - 1

// DateOf returns the date of t in t's location.
func DateOf(t time.Time) Date {
	y, m, d := t.Date()
	return Date{y, m, d}
}

// TimeOf returns the time of day of t in t's location.
func TimeOf(t time.Time) Time {
	h, m, s := t.Clock()
	return Time{h, m, s, t.Nanosecond()}
}

// DateTimeOf returns the wall-clock reading of t in t's location.
func DateTimeOf(t time.Time) DateTime {
	return DateTime{DateOf(t), TimeOf(t)}
}

// OffsetDateTimeOf returns t with the offset of its location at t.
func OffsetDateTimeOf(t time.Time) OffsetDateTime {
	_, off := t.Zone()
	return OffsetDateTime{DateTimeOf(t), off}
}

// IsValid reports whether d is a date of the calendar.
func (d Date) IsValid() bool {
	return d.Month >= time.January && d.Month <= time.December &&
		d.Day >= 1 && d.Day <= daysIn(d.Year, d.Month)
}

// IsValid reports whether t is a time of day.
func (t Time) IsValid() bool {
	return t.Hour >= 0 && t.Hour < 24 && t.Minute >= 0 && t.Minute < 60 &&
		t.Second >= 0 && t.Second < 60 && t.Nanosecond >= 0 && t.Nanosecond < 1e9
}

// IsValid reports whether both the date and the time are valid.
func (dt DateTime) IsValid() bool {
	return dt.Date.IsValid() && dt.Time.IsValid()
}

// IsValid reports whether the time and the offset are valid.
func (t OffsetTime) IsValid() bool {
	return t.Time.IsValid() && validOffset(t.Offset)
}

// IsValid reports whether the reading and the offset are valid.
func (dt OffsetDateTime) IsValid() bool {
	return dt.DateTime.IsValid() && validOffset(dt.Offset)
}

func validOffset(off int) bool {
	return off >= -maxOffset && off <= maxOffset
}

// In returns midnight at the start of d in loc.
func (d Date) In(loc *time.Location) time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, loc)
}

// In returns the instant at which loc reads dt. A reading that loc skips or
// repeats is resolved as time.Date resolves it.
func (dt DateTime) In(loc *time.Location) time.Time {
	d, t := dt.Date, dt.Time
	return time.Date(d.Year, d.Month, d.Day, t.Hour, t.Minute, t.Second, t.Nanosecond, loc)
}

// Instant returns the point in time dt names, in a fixed zone of its offset.
func (dt OffsetDateTime) Instant() time.Time {
	return dt.DateTime.In(time.FixedZone("", dt.Offset))
}

// AddDays returns the date n days after d.
func (d Date) AddDays(n int) Date {
	return dateOfDays(d.days() + int64(n))
}

// DaysSince returns the number of days from s to d.
func (d Date) DaysSince(s Date) int {
	return int(d.days() - s.days())
}

// Weekday returns the day of the week of d.
func (d Date) Weekday() time.Weekday {
	// Day 0 of the epoch, 1970-01-01, was a Thursday.
	w := (d.days() + int64(time.Thursday)) % 7
	if w < 0 {
		w += 7
	}
	return time.Weekday(w)
}

// Compare returns -1, 0 or +1 as d is before, equal to or after o.
func (d Date) Compare(o Date) int {
	return compare(d.Year, o.Year, int(d.Month), int(o.Month), d.Day, o.Day)
}

// Before reports whether d is before o.
func (d Date) Before(o Date) bool { return d.Compare(o) < 0 }

// After reports whether d is after o.
func (d Date) After(o Date) bool { return d.Compare(o) > 0 }

// Compare returns -1, 0 or +1 as t is before, equal to or after o.
func (t Time) Compare(o Time) int {
	return compare(t.Hour, o.Hour, t.Minute, o.Minute, t.Second, o.Second, t.Nanosecond, o.Nanosecond)
}

// Before reports whether t is before o.
func (t Time) Before(o Time) bool { return t.Compare(o) < 0 }

// After reports whether t is after o.
func (t Time) After(o Time) bool { return t.Compare(o) > 0 }

// Compare returns -1, 0 or +1 as dt is before, equal to or after o.
func (dt DateTime) Compare(o DateTime) int {
	if c := dt.Date.Compare(o.Date); c != 0 {
		return c
	}
	return dt.Time.Compare(o.Time)
}

// Before reports whether dt is before o.
func (dt DateTime) Before(o DateTime) bool { return dt.Compare(o) < 0 }

// After reports whether dt is after o.
func (dt DateTime) After(o DateTime) bool { return dt.Compare(o) > 0 }

// compare compares pairs of numbers in order of significance.
func compare(pairs ...int) int {
	for i := 0; i < len(pairs); i += 2 {
		switch {
		case pairs[i] < pairs[i+1]:
			return -1
		case pairs[i] > pairs[i+1]:
			return +1
		}
	}
	return 0
}

// String returns d as YYYY-MM-DD.
func (d Date) String() string {
	var b strings.Builder
	writeYear(&b, d.Year)
	fmt.Fprintf(&b, "-%02d-%02d", int(d.Month), d.Day)
	return b.String()
}

// String returns t as HH:MM:SS, with a fraction when it has one.
func (t Time) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%02d:%02d:%02d", t.Hour, t.Minute, t.Second)
	if t.Nanosecond != 0 {
		b.WriteString(strings.TrimRight(fmt.Sprintf(".%09d", t.Nanosecond), "0"))
	}
	return b.String()
}

// String returns dt as YYYY-MM-DDTHH:MM:SS.
func (dt DateTime) String() string {
	return dt.Date.String() + "T" + dt.Time.String()
}

// String returns t as HH:MM:SS±HH:MM.
func (t OffsetTime) String() string {
	return t.Time.String() + formatOffset(t.Offset)
}

// String returns dt as YYYY-MM-DDTHH:MM:SS±HH:MM.
func (dt OffsetDateTime) String() string {
	return dt.DateTime.String() + formatOffset(dt.Offset)
}

func writeYear(b *strings.Builder, y int) {
	switch {
	case y >= 0 && y <= 9999:
		fmt.Fprintf(b, "%04d", y)
	case y < 0:
		fmt.Fprintf(b, "-%06d", -y)
	default:
		fmt.Fprintf(b, "+%06d", y)
	}
}

func formatOffset(off int) string {
	sign := '+'
	if off < 0 {
		sign, off = '-', -off
	}
	s := fmt.Sprintf("%c%02d:%02d", sign, off/3600, off/60%60)
	if off%60 != 0 {
		s += fmt.Sprintf(":%02d", off%60)
	}
	return s
}

// ParseDate parses YYYY-MM-DD, or ±YYYYYY-MM-DD for years outside 0000 to
// 9999.
func ParseDate(s string) (Date, error) {
	d, ok := parseDate(s)
	if !ok {
		return Date{}, syntaxError("date", s)
	}
	return d, nil
}

// ParseTime parses HH:MM, HH:MM:SS or HH:MM:SS with a fraction of one to nine
// digits.
func ParseTime(s string) (Time, error) {
	t, ok := parseTime(s)
	if !ok {
		return Time{}, syntaxError("time", s)
	}
	return t, nil
}

// ParseDateTime parses a date and a time joined by T.
func ParseDateTime(s string) (DateTime, error) {
	dt, ok := parseDateTime(s)
	if !ok {
		return DateTime{}, syntaxError("date-time", s)
	}
	return dt, nil
}

// ParseOffsetTime parses a time followed by an offset: Z, ±HH:MM or
// ±HH:MM:SS.
func ParseOffsetTime(s string) (OffsetTime, error) {
	i := strings.IndexAny(s, "+-Zz")
	if i >= 0 {
		t, ok1 := parseTime(s[:i])
		off, ok2 := parseOffset(s[i:])
		if ok1 && ok2 {
			return OffsetTime{t, off}, nil
		}
	}
	return OffsetTime{}, syntaxError("offset time", s)
}

// ParseOffsetDateTime parses a date-time followed by an offset: Z, ±HH:MM or
// ±HH:MM:SS.
func ParseOffsetDateTime(s string) (OffsetDateTime, error) {
	if t := strings.IndexByte(s, 'T'); t >= 0 {
		if i := strings.IndexAny(s[t:], "+-Zz"); i >= 0 {
			dt, ok1 := parseDateTime(s[:t+i])
			off, ok2 := parseOffset(s[t+i:])
			if ok1 && ok2 {
				return OffsetDateTime{dt, off}, nil
			}
		}
	}
	return OffsetDateTime{}, syntaxError("offset date-time", s)
}

func syntaxError(what, s string) error {
	return fmt.Errorf("civil: invalid %s %q", what, s)
}

func parseDate(s string) (Date, bool) {
	var year int
	var ok bool
	switch {
	case len(s) > 0 && (s[0] == '+' || s[0] == '-'):
		// An expanded year: a sign and at least six digits, never -000000.
		end := strings.IndexByte(s[1:], '-') + 1
		if end < 7 {
			return Date{}, false
		}
		if year, ok = digits(s[1:end]); !ok || year == 0 && s[0] == '-' {
			return Date{}, false
		}
		if s[0] == '-' {
			year = -year
		}
		s = s[end:]
	case len(s) >= 4:
		if year, ok = digits(s[:4]); !ok {
			return Date{}, false
		}
		s = s[4:]
	default:
		return Date{}, false
	}
	if len(s) != 6 || s[0] != '-' || s[3] != '-' {
		return Date{}, false
	}
	m, ok1 := digits(s[1:3])
	d, ok2 := digits(s[4:6])
	date := Date{year, time.Month(m), d}
	return date, ok1 && ok2 && date.IsValid()
}

func parseTime(s string) (Time, bool) {
	if len(s) < 5 || s[2] != ':' {
		return Time{}, false
	}
	h, ok1 := digits(s[:2])
	m, ok2 := digits(s[3:5])
	t := Time{Hour: h, Minute: m}
	ok := ok1 && ok2
	if s = s[5:]; s != "" {
		if len(s) < 3 || s[0] != ':' {
			return Time{}, false
		}
		var ok3 bool
		t.Second, ok3 = digits(s[1:3])
		ok = ok && ok3
		if s = s[3:]; s != "" {
			frac := s[1:]
			if s[0] != '.' || len(frac) > 9 {
				return Time{}, false
			}
			var ok4 bool
			t.Nanosecond, ok4 = digits(frac + strings.Repeat("0", 9-len(frac)))
			ok = ok && ok4 && frac != ""
		}
	}
	return t, ok && t.IsValid()
}

func parseDateTime(s string) (DateTime, bool) {
	i := strings.IndexByte(s, 'T')
	if i < 0 {
		return DateTime{}, false
	}
	d, ok1 := parseDate(s[:i])
	t, ok2 := parseTime(s[i+1:])
	return DateTime{d, t}, ok1 && ok2
}

func parseOffset(s string) (int, bool) {
	if s == "Z" || s == "z" {
		return 0, true
	}
	if (len(s) != 6 && len(s) != 9) || (s[0] != '+' && s[0] != '-') ||
		s[3] != ':' || (len(s) == 9 && s[6] != ':') {
		return 0, false
	}
	h, ok1 := digits(s[1:3])
	m, ok2 := digits(s[4:6])
	sec, ok3 := 0, true
	if len(s) == 9 {
		sec, ok3 = digits(s[7:9])
	}
	off := h*3600 + m*60 + sec
	if s[0] == '-' {
		off = -off
	}
	return off, ok1 && ok2 && ok3 && m < 60 && sec < 60 && validOffset(off)
}

// digits parses a non-empty run of ASCII digits.
func digits(s string) (int, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

var errInvalid = errors.New("civil: invalid value")

// MarshalText encodes d as String does. An invalid date is an error.
func (d Date) MarshalText() ([]byte, error) {
	return marshal(d.IsValid(), d.String())
}

// UnmarshalText decodes d as ParseDate does.
func (d *Date) UnmarshalText(b []byte) error {
	return unmarshal(d, b, ParseDate)
}

// MarshalText encodes t as String does. An invalid time is an error.
func (t Time) MarshalText() ([]byte, error) {
	return marshal(t.IsValid(), t.String())
}

// UnmarshalText decodes t as ParseTime does.
func (t *Time) UnmarshalText(b []byte) error {
	return unmarshal(t, b, ParseTime)
}

// MarshalText encodes dt as String does. An invalid value is an error.
func (dt DateTime) MarshalText() ([]byte, error) {
	return marshal(dt.IsValid(), dt.String())
}

// UnmarshalText decodes dt as ParseDateTime does.
func (dt *DateTime) UnmarshalText(b []byte) error {
	return unmarshal(dt, b, ParseDateTime)
}

// MarshalText encodes t as String does. An invalid value is an error.
func (t OffsetTime) MarshalText() ([]byte, error) {
	return marshal(t.IsValid(), t.String())
}

// UnmarshalText decodes t as ParseOffsetTime does.
func (t *OffsetTime) UnmarshalText(b []byte) error {
	return unmarshal(t, b, ParseOffsetTime)
}

// MarshalText encodes dt as String does. An invalid value is an error.
func (dt OffsetDateTime) MarshalText() ([]byte, error) {
	return marshal(dt.IsValid(), dt.String())
}

// UnmarshalText decodes dt as ParseOffsetDateTime does.
func (dt *OffsetDateTime) UnmarshalText(b []byte) error {
	return unmarshal(dt, b, ParseOffsetDateTime)
}

func marshal(valid bool, s string) ([]byte, error) {
	if !valid {
		return nil, errInvalid
	}
	return []byte(s), nil
}

func unmarshal[T any](dst *T, b []byte, parse func(string) (T, error)) error {
	v, err := parse(string(b))
	if err != nil {
		return err
	}
	*dst = v
	return nil
}
