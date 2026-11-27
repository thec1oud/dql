package sqlite

import (
	"cmp"
	"slices"
	"strings"

	"github.com/thec1oud/dql/core/diagnostics"
	sqliteextv1 "github.com/thec1oud/dql/proto/dql/ext/sqlite/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// sessionSetup runs on every new connection: SQLite checks foreign keys
// only when a connection asks it to, and LIKE folds ASCII case unless told
// not to (§22.7). SQLite keeps no session time zone: 'now' is UTC.
var sessionSetup = []string{
	"PRAGMA foreign_keys = ON",
	"PRAGMA case_sensitive_like = ON",
}

// errorCodes are SQLite's extended result codes that map into the taxonomy
// (§23.1). A busy or locked database is a lock timeout: the busy timeout
// ran out. A stale read snapshot that cannot be upgraded to write is a
// serialization failure, which a retry resolves. An interrupted statement
// is a Timeout: the runtime reports Canceled itself when its own context
// ended.
var errorCodes = map[string]irv1.ErrorKind{
	"5":    irv1.ErrorKind_ERROR_KIND_LOCK_TIMEOUT,          // SQLITE_BUSY
	"6":    irv1.ErrorKind_ERROR_KIND_LOCK_TIMEOUT,          // SQLITE_LOCKED
	"9":    irv1.ErrorKind_ERROR_KIND_TIMEOUT,               // SQLITE_INTERRUPT
	"14":   irv1.ErrorKind_ERROR_KIND_CONNECTION,            // SQLITE_CANTOPEN
	"261":  irv1.ErrorKind_ERROR_KIND_LOCK_TIMEOUT,          // SQLITE_BUSY_RECOVERY
	"262":  irv1.ErrorKind_ERROR_KIND_LOCK_TIMEOUT,          // SQLITE_LOCKED_SHAREDCACHE
	"275":  irv1.ErrorKind_ERROR_KIND_CHECK_VIOLATION,       // SQLITE_CONSTRAINT_CHECK
	"517":  irv1.ErrorKind_ERROR_KIND_SERIALIZATION_FAILURE, // SQLITE_BUSY_SNAPSHOT
	"787":  irv1.ErrorKind_ERROR_KIND_FOREIGN_KEY_VIOLATION, // SQLITE_CONSTRAINT_FOREIGNKEY
	"1299": irv1.ErrorKind_ERROR_KIND_NOT_NULL_VIOLATION,    // SQLITE_CONSTRAINT_NOTNULL
	"1555": irv1.ErrorKind_ERROR_KIND_UNIQUE_VIOLATION,      // SQLITE_CONSTRAINT_PRIMARYKEY
	"2067": irv1.ErrorKind_ERROR_KIND_UNIQUE_VIOLATION,      // SQLITE_CONSTRAINT_UNIQUE
}

// Violations returns how SQLite reports a violation of each rule of the
// catalogs' tables (§23.2). SQLite names columns rather than keys:
//
//   - a duplicate names the key's columns, as "t.a, t.b", which the driver
//     adapter reads as the table and "a, b"; an expression index it names
//     as "index 'name'", without its table, which the adapter reads as the
//     index's name;
//   - a not-null violation names the column, as "t.c";
//   - a check violation names its constraint without the table, or, when
//     CONSTRAINT gives it no name, its expression as written;
//   - a foreign-key violation names nothing, so no report can name its
//     rule.
//
// When two keys have the same columns, the first by name is reported.
// Only a duplicate's message could hold values, and SQLite's holds none,
// so nothing is redacted.
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
	var out []*irv1.ViolationReport
	for _, c := range t.GetColumns() {
		if !c.GetNullable() {
			out = append(out, report(irv1.ErrorKind_ERROR_KIND_NOT_NULL_VIOLATION, name, c.GetName().GetStored(), c.GetName().GetStored()))
		}
	}
	for _, c := range t.GetConstraints() {
		reported := c.GetName()
		for _, e := range c.GetExtensions() {
			var check sqliteextv1.Check
			if e.UnmarshalTo(&check) == nil && !check.GetNamed() {
				reported = check.GetExpression()
			}
		}
		out = append(out, report(irv1.ErrorKind_ERROR_KIND_CHECK_VIOLATION, name, reported, c.GetName()))
	}
	seen := map[string]bool{}
	for _, u := range t.GetUniqueness() {
		reported := strings.Join(u.GetColumns(), ", ")
		if len(u.GetExpressions()) > 0 {
			reported = u.GetName()
		}
		if !seen[reported] {
			seen[reported] = true
			out = append(out, report(irv1.ErrorKind_ERROR_KIND_UNIQUE_VIOLATION, name, reported, u.GetName()))
		}
	}
	return out
}

func report(kind irv1.ErrorKind, object *irv1.QualifiedName, reported, rule string) *irv1.ViolationReport {
	return &irv1.ViolationReport{Kind: kind, Object: object, Reported: reported, Rule: rule}
}
