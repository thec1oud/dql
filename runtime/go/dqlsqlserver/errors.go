package dqlsqlserver

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/thec1oud/dql/runtime/go/dql"
)

// The messages that name what a violation broke, by error number. A table
// is named schema.table, or database.schema.table in a not-null
// violation; the database, the catalog, is dropped.
var (
	// Violation of PRIMARY KEY constraint 'k'. Cannot insert duplicate key
	// in object 'app.users'. The duplicate key value is (1).
	keyConstraint = regexp.MustCompile(`^Violation of (?:PRIMARY|UNIQUE) KEY constraint '((?:[^']|'')*)'\. Cannot insert duplicate key in object '((?:[^']|'')*)'`)
	// Cannot insert duplicate key row in object 'app.users' with unique
	// index 'i'. The duplicate key value is (1).
	keyIndex = regexp.MustCompile(`^Cannot insert duplicate key row in object '((?:[^']|'')*)' with unique index '((?:[^']|'')*)'`)
	// The INSERT statement conflicted with the FOREIGN KEY constraint "c".
	// The conflict occurred in database "d", table "app.t", column 'c'.
	// The table is the referenced one, or on a delete the referencing one,
	// so only the constraint is kept.
	conflict = regexp.MustCompile(`conflicted with the (FOREIGN KEY|REFERENCE|CHECK) constraint "((?:[^"]|"")*)"\. The conflict occurred in database "(?:[^"]|"")*", table "((?:[^"]|"")*)"`)
	// Cannot insert the value NULL into column 'c', table 'd.app.t'; ...
	null = regexp.MustCompile(`^Cannot insert the value NULL into column '((?:[^']|'')*)', table '((?:[^']|'')*)'`)
)

// native returns an error's fields. SQL Server reports a foreign-key and a
// check violation under one number, 547, so a check's code is 547/CHECK,
// as the dialect's error map has it (§23.1). A duplicate's message quotes
// the duplicate key's values, so it is also the detail, which the runtime
// drops when the key is sensitive (§23.3).
func native(number int32, message string) dql.NativeError {
	n := dql.NativeError{Code: strconv.Itoa(int(number)), Message: message}
	switch number {
	case 2627:
		n.Detail = message
		if m := keyConstraint.FindStringSubmatch(message); m != nil {
			n.Constraint = unquote(m[1], "'")
			n.Schema, n.Table = split(unquote(m[2], "'"))
		}
	case 2601:
		n.Detail = message
		if m := keyIndex.FindStringSubmatch(message); m != nil {
			n.Schema, n.Table = split(unquote(m[1], "'"))
			n.Constraint = unquote(m[2], "'")
		}
	case 547:
		if m := conflict.FindStringSubmatch(message); m != nil {
			n.Constraint = unquote(m[2], `"`)
			if m[1] == "CHECK" {
				n.Code = "547/CHECK"
				n.Schema, n.Table = split(unquote(m[3], `"`))
			}
		}
	case 515:
		if m := null.FindStringSubmatch(message); m != nil {
			n.Column = unquote(m[1], "'")
			n.Schema, n.Table = split(unquote(m[2], "'"))
		}
	}
	return n
}

// split splits a [database.]schema.table name into its schema and table.
// Names holding dots are not told apart, as the message does not quote
// them.
func split(name string) (schema, table string) {
	parts := strings.Split(name, ".")
	if len(parts) < 2 {
		return "", name
	}
	return parts[len(parts)-2], parts[len(parts)-1]
}

func unquote(s, q string) string { return strings.ReplaceAll(s, q+q, q) }
