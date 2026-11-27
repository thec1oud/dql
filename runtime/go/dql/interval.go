package dql

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Interval is a duration in months, days and microseconds, the host type of
// the logical type interval (design record §8.1, §8.8), such as a Postgres
// interval or a MySQL TIME. The three parts are kept apart, because a month
// has no fixed length and a day may not be 24 hours, so an Interval never
// becomes a time.Duration (§8.8, rule 3). Each part has its own sign, as in
// Postgres: 1 month -1 day is a valid interval.
type Interval struct {
	Months       int32
	Days         int32
	Microseconds int64
}

const (
	usPerSecond = 1_000_000
	usPerMinute = 60 * usPerSecond
	usPerHour   = 60 * usPerMinute
)

// String returns i as an ISO 8601 duration in Postgres's iso_8601 style,
// with a sign on each negative component: P1Y2M3DT4H5M6.5S, P1M-1D, PT0S.
func (i Interval) String() string {
	var b strings.Builder
	b.WriteByte('P')
	component(&b, int64(i.Months/12), 'Y')
	component(&b, int64(i.Months%12), 'M')
	component(&b, int64(i.Days), 'D')
	if us := i.Microseconds; us != 0 {
		b.WriteByte('T')
		component(&b, us/usPerHour, 'H')
		component(&b, us%usPerHour/usPerMinute, 'M')
		if s := us % usPerMinute; s != 0 {
			if s < 0 {
				b.WriteByte('-')
				s = -s
			}
			b.WriteString(strconv.FormatInt(s/usPerSecond, 10))
			if f := s % usPerSecond; f != 0 {
				b.WriteString(strings.TrimRight(fmt.Sprintf(".%06d", f), "0"))
			}
			b.WriteByte('S')
		}
	} else if i.Months == 0 && i.Days == 0 {
		b.WriteString("T0S")
	}
	return b.String()
}

func component(b *strings.Builder, n int64, unit byte) {
	if n != 0 {
		b.WriteString(strconv.FormatInt(n, 10))
		b.WriteByte(unit)
	}
}

// ParseInterval parses an ISO 8601 duration as String writes it: P, then
// optional years, months and days, then optionally T and hours, minutes and
// seconds with up to six fractional digits. Each component may carry a minus
// sign, and at least one must be present. Weeks are not accepted.
func ParseInterval(s string) (Interval, error) {
	i, ok := parseInterval(s)
	if !ok {
		return Interval{}, fmt.Errorf("dql: invalid interval %q", s)
	}
	return i, nil
}

func parseInterval(s string) (Interval, bool) {
	if !strings.HasPrefix(s, "P") {
		return Interval{}, false
	}
	s = s[1:]
	var months, days, us int64
	seen := false
	units, scales := "YMD", []int64{12, 1, 1}
	targets := []*int64{&months, &months, &days}
	for pass := 0; pass < 2; pass++ {
		if pass == 1 {
			if s == "" {
				break
			}
			if s[0] != 'T' || len(s) == 1 {
				return Interval{}, false
			}
			s = s[1:]
			units, scales = "HMS", []int64{usPerHour, usPerMinute, usPerSecond}
			targets = []*int64{&us, &us, &us}
		}
		for u := 0; u < len(units) && s != ""; u++ {
			end := strings.IndexByte(s, units[u])
			if end < 0 || strings.ContainsAny(s[:end], "YMDTHS") {
				continue
			}
			n, ok := number(s[:end], units[u] == 'S', scales[u])
			if !ok {
				return Interval{}, false
			}
			if *targets[u], ok = add(*targets[u], n); !ok {
				return Interval{}, false
			}
			s, seen = s[end+1:], true
		}
	}
	if s != "" || !seen || months < math.MinInt32 || months > math.MaxInt32 ||
		days < math.MinInt32 || days > math.MaxInt32 {
		return Interval{}, false
	}
	return Interval{int32(months), int32(days), us}, true
}

// number parses an optionally negative integer, or with frac a decimal of up
// to six fractional digits, and multiplies it by scale.
func number(s string, frac bool, scale int64) (int64, bool) {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, f, hasFrac := strings.Cut(s, ".")
	if hasFrac && (!frac || f == "" || len(f) > 6 || !digitsOnly(f)) {
		return 0, false
	}
	if whole == "" || !digitsOnly(whole) {
		return 0, false
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || w > math.MaxInt64/scale {
		return 0, false
	}
	n := w * scale
	if hasFrac {
		fv, _ := strconv.ParseInt(f+strings.Repeat("0", 6-len(f)), 10, 64)
		if n += fv; n < 0 {
			return 0, false
		}
	}
	if neg {
		n = -n
	}
	return n, true
}

func digitsOnly(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// add adds without overflowing int64.
func add(a, b int64) (int64, bool) {
	c := a + b
	return c, (c > a) == (b > 0)
}

// MarshalText encodes i as String does.
func (i Interval) MarshalText() ([]byte, error) {
	return []byte(i.String()), nil
}

// UnmarshalText decodes i as ParseInterval does.
func (i *Interval) UnmarshalText(b []byte) error {
	v, err := ParseInterval(string(b))
	if err != nil {
		return err
	}
	*i = v
	return nil
}
