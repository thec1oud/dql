package dqlmysql

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/thec1oud/dql/runtime/go/dql"
)

// The messages that name what a violation broke, by error number. A MySQL
// catalog is one database, so the database a message names is dropped and
// the table kept.
var (
	// Duplicate entry 'x' for key 'users.email'; MariaDB and MySQL before
	// 8.0.19 write the key alone.
	duplicate = regexp.MustCompile(`for key '((?:[^']|'')*)'$`)
	// ... a foreign key constraint fails (`db`.`t`, CONSTRAINT `c` FOREIGN KEY ...
	foreignKey = regexp.MustCompile("\\(`(?:[^`]|``)*`\\.`((?:[^`]|``)*)`, CONSTRAINT `((?:[^`]|``)*)`")
	// Check constraint 'c' is violated.
	check = regexp.MustCompile(`^Check constraint '((?:[^']|'')*)' is violated`)
	// MariaDB: CONSTRAINT `c` failed for `db`.`t`
	mariaCheck = regexp.MustCompile("^CONSTRAINT `((?:[^`]|``)*)` failed for `(?:[^`]|``)*`\\.`((?:[^`]|``)*)`")
	// Column 'c' cannot be null; Field 'c' doesn't have a default value;
	// ... NULL supplied to NOT NULL column 'c' at row 1
	column = regexp.MustCompile(`(?:^Column|^Field|NOT NULL column) '((?:[^']|'')*)'`)
)

// native returns an error's fields. A duplicate's message quotes the
// duplicate key's values, so it is also the detail, which the runtime
// drops when the key is sensitive (§23.3). MySQL names the table only in
// a foreign-key violation, a duplicate and MariaDB's check violation; the
// runtime finds the others' rules by name.
func native(number uint16, message string) dql.NativeError {
	n := dql.NativeError{Code: strconv.Itoa(int(number)), Message: message}
	switch number {
	case 1062, 1586:
		n.Detail = message
		if m := duplicate.FindStringSubmatch(message); m != nil {
			key := unquote(m[1], "'")
			if i := strings.IndexByte(key, '.'); i >= 0 {
				n.Table, key = key[:i], key[i+1:]
			}
			n.Constraint = key
		}
	case 1451, 1452:
		if m := foreignKey.FindStringSubmatch(message); m != nil {
			n.Table, n.Constraint = unquote(m[1], "`"), unquote(m[2], "`")
		}
	case 3819:
		if m := check.FindStringSubmatch(message); m != nil {
			n.Constraint = unquote(m[1], "'")
		}
	case 4025:
		if m := mariaCheck.FindStringSubmatch(message); m != nil {
			n.Constraint, n.Table = unquote(m[1], "`"), unquote(m[2], "`")
		}
	case 1048, 1263, 1364:
		if m := column.FindStringSubmatch(message); m != nil {
			n.Column = unquote(m[1], "'")
		}
	}
	return n
}

func unquote(s, q string) string { return strings.ReplaceAll(s, q+q, q) }
