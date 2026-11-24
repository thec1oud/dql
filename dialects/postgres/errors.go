package postgres

import (
	"cmp"
	"slices"
	"strings"

	"github.com/thec1oud/dql/core/diagnostics"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// sessionSetup runs on every new connection: the session time zone is UTC
// by contract (§8.8, §22.7).
var sessionSetup = []string{"SET TIME ZONE 'UTC'"}

// errorCodes are the SQLSTATEs that map into the taxonomy (§23.1). A
// canceled statement is a Timeout: the runtime reports Canceled itself when
// its own context ended.
var errorCodes = map[string]irv1.ErrorKind{
	"08000": irv1.ErrorKind_ERROR_KIND_CONNECTION, // connection_exception
	"08001": irv1.ErrorKind_ERROR_KIND_CONNECTION, // sqlclient_unable_to_establish_sqlconnection
	"08003": irv1.ErrorKind_ERROR_KIND_CONNECTION, // connection_does_not_exist
	"08004": irv1.ErrorKind_ERROR_KIND_CONNECTION, // sqlserver_rejected_establishment_of_sqlconnection
	"08006": irv1.ErrorKind_ERROR_KIND_CONNECTION, // connection_failure
	"08007": irv1.ErrorKind_ERROR_KIND_CONNECTION, // transaction_resolution_unknown
	"08P01": irv1.ErrorKind_ERROR_KIND_CONNECTION, // protocol_violation
	"23502": irv1.ErrorKind_ERROR_KIND_NOT_NULL_VIOLATION,
	"23503": irv1.ErrorKind_ERROR_KIND_FOREIGN_KEY_VIOLATION,
	"23505": irv1.ErrorKind_ERROR_KIND_UNIQUE_VIOLATION,
	"23514": irv1.ErrorKind_ERROR_KIND_CHECK_VIOLATION,
	"23P01": irv1.ErrorKind_ERROR_KIND_EXCLUSION_VIOLATION,
	"25P03": irv1.ErrorKind_ERROR_KIND_TIMEOUT, // idle_in_transaction_session_timeout
	"40001": irv1.ErrorKind_ERROR_KIND_SERIALIZATION_FAILURE,
	"40P01": irv1.ErrorKind_ERROR_KIND_DEADLOCK,
	"53300": irv1.ErrorKind_ERROR_KIND_CONNECTION,   // too_many_connections
	"55P03": irv1.ErrorKind_ERROR_KIND_LOCK_TIMEOUT, // lock_not_available
	"57014": irv1.ErrorKind_ERROR_KIND_TIMEOUT,      // query_canceled
	"57P01": irv1.ErrorKind_ERROR_KIND_CONNECTION,   // admin_shutdown
	"57P02": irv1.ErrorKind_ERROR_KIND_CONNECTION,   // crash_shutdown
	"57P03": irv1.ErrorKind_ERROR_KIND_CONNECTION,   // cannot_connect_now
	"57P05": irv1.ErrorKind_ERROR_KIND_CONNECTION,   // idle_session_timeout
}

const keySensitive = "core.sensitive"

// Violations returns how Postgres reports a violation of each rule of the
// catalogs' tables and domains (§23.2). Postgres names the constraint or
// unique index, and the column of a not-null violation, so what it reports
// is the rule's own name.
//
// The detail Postgres adds holds values: a unique, foreign-key or exclusion
// violation shows its key, and a check or not-null violation the whole
// failing row. A report is redacted when one of those may be a sensitive
// column's (§23.3).
func Violations(cats []*irv1.Catalog) []*irv1.ViolationReport {
	// Every sensitive column, by qualified name.
	sensitive := map[string]bool{}
	for _, cat := range cats {
		for _, ns := range cat.GetNamespaces() {
			for _, t := range ns.GetTables() {
				for _, c := range t.GetColumns() {
					if slices.ContainsFunc(c.GetAnnotations(), func(a *irv1.Annotation) bool { return a.GetKey() == keySensitive }) {
						sensitive[diagnostics.Name(&irv1.QualifiedName{Catalog: cat.GetName().GetStored(), Namespace: ns.GetName().GetStored(),
							Object: t.GetName().GetStored(), Member: c.GetName().GetStored()})] = true
					}
				}
			}
		}
	}
	var out []*irv1.ViolationReport
	for _, cat := range cats {
		for _, ns := range cat.GetNamespaces() {
			name := func(o string) *irv1.QualifiedName {
				return &irv1.QualifiedName{Catalog: cat.GetName().GetStored(), Namespace: ns.GetName().GetStored(), Object: o}
			}
			for _, t := range ns.GetTables() {
				if t.GetKind() == irv1.TableKind_RELATION_KIND_TABLE {
					out = append(out, tableViolations(name(t.GetName().GetStored()), t, sensitive)...)
				}
			}
			for _, d := range ns.GetDomains() {
				for _, c := range d.GetConstraints() {
					out = append(out, report(irv1.ErrorKind_ERROR_KIND_CHECK_VIOLATION, name(d.GetName().GetStored()), c.GetName(), false))
				}
			}
		}
	}
	slices.SortFunc(out, func(a, b *irv1.ViolationReport) int {
		return cmp.Or(diagnostics.CompareNames(a.GetObject(), b.GetObject()), cmp.Compare(a.GetKind(), b.GetKind()), strings.Compare(a.GetReported(), b.GetReported()))
	})
	return out
}

func tableViolations(name *irv1.QualifiedName, t *irv1.Table, sensitive map[string]bool) []*irv1.ViolationReport {
	in := func(table *irv1.QualifiedName, cols []string) bool {
		return slices.ContainsFunc(cols, func(c string) bool {
			return sensitive[diagnostics.Name(&irv1.QualifiedName{Catalog: table.GetCatalog(), Namespace: table.GetNamespace(), Object: table.GetObject(), Member: c})]
		})
	}
	some := func(cols []string) bool { return in(name, cols) }
	var all []string
	for _, c := range t.GetColumns() {
		all = append(all, c.GetName().GetStored())
	}
	row := some(all)
	var out []*irv1.ViolationReport
	for _, c := range t.GetColumns() {
		if !c.GetNullable() {
			out = append(out, report(irv1.ErrorKind_ERROR_KIND_NOT_NULL_VIOLATION, name, c.GetName().GetStored(), row))
		}
	}
	for _, c := range t.GetConstraints() {
		if c.GetKind() == irv1.ConstraintKind_CONSTRAINT_KIND_EXCLUSION {
			out = append(out, report(irv1.ErrorKind_ERROR_KIND_EXCLUSION_VIOLATION, name, c.GetName(), some(c.GetColumns())))
		} else {
			out = append(out, report(irv1.ErrorKind_ERROR_KIND_CHECK_VIOLATION, name, c.GetName(), row))
		}
	}
	for _, u := range t.GetUniqueness() {
		// An expression's value hides which columns it reads.
		redact := some(u.GetColumns()) || len(u.GetExpressions()) > 0 && row
		out = append(out, report(irv1.ErrorKind_ERROR_KIND_UNIQUE_VIOLATION, name, u.GetName(), redact))
	}
	for _, fk := range t.GetForeignKeys() {
		// The key's values are the referenced columns' values too.
		ref := fk.GetReferences()
		out = append(out, report(irv1.ErrorKind_ERROR_KIND_FOREIGN_KEY_VIOLATION, name, fk.GetName().GetStored(), some(fk.GetColumns()) || in(ref.GetTable(), ref.GetColumns())))
	}
	return out
}

func report(kind irv1.ErrorKind, object *irv1.QualifiedName, rule string, redact bool) *irv1.ViolationReport {
	return &irv1.ViolationReport{Kind: kind, Object: object, Reported: rule, Rule: rule, Redact: redact}
}
