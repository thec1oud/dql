package dqlsqlite

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/thec1oud/dql/runtime/go/dql"
)

// The messages that name what a violation broke. modernc.org/sqlite writes
// a message as "<result>: <message> (<code>)".
var (
	// UNIQUE constraint failed: t.a, t.b; or, for an expression index,
	// UNIQUE constraint failed: index 'name'.
	unique = regexp.MustCompile(`UNIQUE constraint failed: (.+) \(\d+\)$`)
	index  = regexp.MustCompile(`^index '((?:[^']|'')*)'$`)
	// NOT NULL constraint failed: t.c
	notNull = regexp.MustCompile(`NOT NULL constraint failed: ([^.]+)\.(.+) \(\d+\)$`)
	// CHECK constraint failed: name, or the expression of an unnamed check.
	check = regexp.MustCompile(`CHECK constraint failed: (.+) \(\d+\)$`)
)

// native returns an error's fields, as the dialect's violation reports read
// them (§23.2): a duplicate names the table and the key's columns, as
// "a, b", or an expression index by its name; a not-null violation names
// the table and column; a check violation names the constraint, or the
// expression of one CONSTRAINT gives no name, without its table, which the
// runtime finds the rule by. A foreign-key violation names nothing. No
// message quotes values, so there is no detail.
func native(code int, message string) dql.NativeError {
	n := dql.NativeError{Code: strconv.Itoa(code), Message: message}
	switch code {
	case 1555, 2067: // SQLITE_CONSTRAINT_PRIMARYKEY, SQLITE_CONSTRAINT_UNIQUE
		if m := unique.FindStringSubmatch(message); m != nil {
			if ix := index.FindStringSubmatch(m[1]); ix != nil {
				n.Constraint = strings.ReplaceAll(ix[1], "''", "'")
				break
			}
			var cols []string
			for _, part := range strings.Split(m[1], ", ") {
				t, col, _ := strings.Cut(part, ".")
				n.Table, cols = t, append(cols, col)
			}
			n.Constraint = strings.Join(cols, ", ")
		}
	case 1299: // SQLITE_CONSTRAINT_NOTNULL
		if m := notNull.FindStringSubmatch(message); m != nil {
			n.Table, n.Column = m[1], m[2]
		}
	case 275: // SQLITE_CONSTRAINT_CHECK
		if m := check.FindStringSubmatch(message); m != nil {
			n.Constraint = m[1]
		}
	}
	return n
}
