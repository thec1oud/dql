package civil

import "time"

// The day arithmetic counts days from 1970-01-01 in int64, because a
// time.Duration spans only about 292 years and Postgres dates span millions.
// The algorithms are Howard Hinnant's days_from_civil and civil_from_days.

func daysIn(y int, m time.Month) int {
	switch m {
	case time.February:
		if y%4 == 0 && (y%100 != 0 || y%400 == 0) {
			return 29
		}
		return 28
	case time.April, time.June, time.September, time.November:
		return 30
	}
	return 31
}

// floorDiv divides rounding toward negative infinity.
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}

func (d Date) days() int64 {
	y, m := int64(d.Year), int64(d.Month)
	if m <= 2 {
		y--
	}
	era := floorDiv(y, 400)
	yoe := y - era*400
	mp := (m + 9) % 12
	doy := (153*mp+2)/5 + int64(d.Day) - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}

func dateOfDays(z int64) Date {
	z += 719468
	era := floorDiv(z, 146097)
	doe := z - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	y := yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	d := doy - (153*mp+2)/5 + 1
	m := (mp+2)%12 + 1
	if m <= 2 {
		y++
	}
	return Date{int(y), time.Month(m), int(d)}
}
