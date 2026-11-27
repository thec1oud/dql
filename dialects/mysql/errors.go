package mysql

import (
	"cmp"
	"slices"
	"strings"

	"github.com/thec1oud/dql/core/diagnostics"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// sessionSetup runs on every new connection: the session time zone is UTC
// by contract (§8.8, §22.7), as an offset, which needs no time zone tables;
// and strict mode turns every invalid or missing value into an error, on
// every storage engine, rather than a warning and a stored default.
var sessionSetup = []string{
	"SET time_zone = '+00:00'",
	"SET SESSION sql_mode = CONCAT_WS(',', @@SESSION.sql_mode, 'STRICT_ALL_TABLES')",
}

// errorCodes are the server's error numbers, and the client's for a lost
// connection, that map into the taxonomy (§23.1). MySQL reports a
// serialization conflict as a deadlock. A canceled or timed-out statement
// is a Timeout: the runtime reports Canceled itself when its own context
// ended.
var errorCodes = map[string]irv1.ErrorKind{
	"1040": irv1.ErrorKind_ERROR_KIND_CONNECTION, // ER_CON_COUNT_ERROR, too many connections
	"1048": irv1.ErrorKind_ERROR_KIND_NOT_NULL_VIOLATION,
	"1053": irv1.ErrorKind_ERROR_KIND_CONNECTION, // ER_SERVER_SHUTDOWN
	"1062": irv1.ErrorKind_ERROR_KIND_UNIQUE_VIOLATION,
	"1152": irv1.ErrorKind_ERROR_KIND_CONNECTION, // ER_ABORTING_CONNECTION
	"1159": irv1.ErrorKind_ERROR_KIND_CONNECTION, // ER_NET_READ_INTERRUPTED
	"1161": irv1.ErrorKind_ERROR_KIND_CONNECTION, // ER_NET_WRITE_INTERRUPTED
	"1205": irv1.ErrorKind_ERROR_KIND_LOCK_TIMEOUT,
	"1213": irv1.ErrorKind_ERROR_KIND_DEADLOCK,
	"1216": irv1.ErrorKind_ERROR_KIND_FOREIGN_KEY_VIOLATION, // ER_NO_REFERENCED_ROW
	"1217": irv1.ErrorKind_ERROR_KIND_FOREIGN_KEY_VIOLATION, // ER_ROW_IS_REFERENCED
	"1263": irv1.ErrorKind_ERROR_KIND_NOT_NULL_VIOLATION,    // ER_WARN_NULL_TO_NOTNULL, in strict mode
	"1317": irv1.ErrorKind_ERROR_KIND_TIMEOUT,               // ER_QUERY_INTERRUPTED
	"1364": irv1.ErrorKind_ERROR_KIND_NOT_NULL_VIOLATION,    // ER_NO_DEFAULT_FOR_FIELD
	"1451": irv1.ErrorKind_ERROR_KIND_FOREIGN_KEY_VIOLATION, // ER_ROW_IS_REFERENCED_2
	"1452": irv1.ErrorKind_ERROR_KIND_FOREIGN_KEY_VIOLATION, // ER_NO_REFERENCED_ROW_2
	"1586": irv1.ErrorKind_ERROR_KIND_UNIQUE_VIOLATION,      // ER_DUP_ENTRY_WITH_KEY_NAME
	"1969": irv1.ErrorKind_ERROR_KIND_TIMEOUT,               // MariaDB's ER_STATEMENT_TIMEOUT
	"2002": irv1.ErrorKind_ERROR_KIND_CONNECTION,            // CR_CONNECTION_ERROR
	"2003": irv1.ErrorKind_ERROR_KIND_CONNECTION,            // CR_CONN_HOST_ERROR
	"2006": irv1.ErrorKind_ERROR_KIND_CONNECTION,            // CR_SERVER_GONE_ERROR
	"2013": irv1.ErrorKind_ERROR_KIND_CONNECTION,            // CR_SERVER_LOST
	"3024": irv1.ErrorKind_ERROR_KIND_TIMEOUT,               // ER_QUERY_TIMEOUT, max_execution_time
	"3572": irv1.ErrorKind_ERROR_KIND_LOCK_TIMEOUT,          // ER_LOCK_NOWAIT
	"3819": irv1.ErrorKind_ERROR_KIND_CHECK_VIOLATION,       // ER_CHECK_CONSTRAINT_VIOLATED
	"4025": irv1.ErrorKind_ERROR_KIND_CHECK_VIOLATION,       // MariaDB's ER_CONSTRAINT_FAILED
	"4031": irv1.ErrorKind_ERROR_KIND_CONNECTION,            // ER_CLIENT_INTERACTION_TIMEOUT
}

const keySensitive = "core.sensitive"

// Violations returns how MySQL and MariaDB report a violation of each rule
// of the catalogs' tables (§23.2). A duplicate names the unique key, which
// is PRIMARY for the primary key, a foreign-key or check violation its
// constraint, and a not-null violation its column; so what is reported is
// the rule's own name. The driver adapter strips the table MySQL 8 puts
// before a key's name.
//
// Only a duplicate's message holds values, the duplicate key's, so only it
// is redacted, when one of the key's columns is sensitive, or the key has
// an expression (§23.3).
func Violations(cats []*irv1.Catalog) []*irv1.ViolationReport {
	var out []*irv1.ViolationReport
	for _, cat := range cats {
		for _, ns := range cat.GetNamespaces() {
			for _, t := range ns.GetTables() {
				if t.GetKind() == irv1.TableKind_RELATION_KIND_TABLE {
					name := &irv1.QualifiedName{Catalog: cat.GetName().GetStored(), Namespace: ns.GetName().GetStored(), Object: t.GetName().GetStored()}
					out = append(out, tableViolations(name, t)...)
				}
			}
		}
	}
	slices.SortFunc(out, func(a, b *irv1.ViolationReport) int {
		return cmp.Or(diagnostics.CompareNames(a.GetObject(), b.GetObject()), cmp.Compare(a.GetKind(), b.GetKind()), strings.Compare(a.GetReported(), b.GetReported()))
	})
	return out
}

func tableViolations(name *irv1.QualifiedName, t *irv1.Table) []*irv1.ViolationReport {
	sensitive := map[string]bool{}
	for _, c := range t.GetColumns() {
		if slices.ContainsFunc(c.GetAnnotations(), func(a *irv1.Annotation) bool { return a.GetKey() == keySensitive }) {
			sensitive[c.GetName().GetStored()] = true
		}
	}
	var out []*irv1.ViolationReport
	for _, c := range t.GetColumns() {
		if !c.GetNullable() {
			out = append(out, report(irv1.ErrorKind_ERROR_KIND_NOT_NULL_VIOLATION, name, c.GetName().GetStored(), false))
		}
	}
	for _, c := range t.GetConstraints() {
		out = append(out, report(irv1.ErrorKind_ERROR_KIND_CHECK_VIOLATION, name, c.GetName(), false))
	}
	for _, u := range t.GetUniqueness() {
		redact := len(u.GetExpressions()) > 0 || slices.ContainsFunc(u.GetColumns(), func(c string) bool { return sensitive[c] })
		out = append(out, report(irv1.ErrorKind_ERROR_KIND_UNIQUE_VIOLATION, name, u.GetName(), redact))
	}
	for _, fk := range t.GetForeignKeys() {
		out = append(out, report(irv1.ErrorKind_ERROR_KIND_FOREIGN_KEY_VIOLATION, name, fk.GetName().GetStored(), false))
	}
	return out
}

func report(kind irv1.ErrorKind, object *irv1.QualifiedName, rule string, redact bool) *irv1.ViolationReport {
	return &irv1.ViolationReport{Kind: kind, Object: object, Reported: rule, Rule: rule, Redact: redact}
}
