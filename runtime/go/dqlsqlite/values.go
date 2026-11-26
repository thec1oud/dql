package dqlsqlite

import "time"

// decode turns one value the driver scanned into its dql.Row form. SQLite
// stores integers, reals, text and blobs, which the driver gives as int64,
// float64, string and []byte, the form a weak column's value takes too
// (§8.10).
//
// The driver reads text in a column declared DATE, DATETIME or TIMESTAMP
// as a time.Time where it parses, which decode writes back as text, as
// YYYY-MM-DD HH:MM:SS with a fraction where it has one and its offset
// where it is not UTC. Text written in another form, such as a date alone,
// comes back in this one.
func decode(v any) any {
	if t, ok := v.(time.Time); ok {
		if _, off := t.Zone(); off == 0 {
			return t.Format("2006-01-02 15:04:05.999999999")
		}
		return t.Format("2006-01-02 15:04:05.999999999-07:00")
	}
	return v
}
