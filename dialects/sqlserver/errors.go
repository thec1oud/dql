package sqlserver

import (
	"cmp"
	"slices"
	"strings"

	"github.com/thec1oud/dql/core/diagnostics"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// sessionSetup runs on every new connection. A filtered index, such as a
// soft-delete table's unique key, or an index on a computed column takes
// writes only under ANSI_NULLS, QUOTED_IDENTIFIER and ARITHABORT, which
// some clients leave off. SQL Server keeps no session time zone: dql writes
// datetimeoffset values with their offset (§8.8).
var sessionSetup = []string{
	"SET ANSI_NULLS ON",
	"SET QUOTED_IDENTIFIER ON",
	"SET ARITHABORT ON",
}

// errorCodes are the server's error numbers that map into the taxonomy
// (§23.1). SQL Server reports a foreign-key and a check violation under one
// number, 547; the driver adapter tells a check violation apart by its
// message and reports it as 547/CHECK. A snapshot transaction that meets
// another's write fails with a serialization failure, which a retry
// resolves.
var errorCodes = map[string]irv1.ErrorKind{
	"233":       irv1.ErrorKind_ERROR_KIND_CONNECTION, // no process on the other end of the pipe
	"515":       irv1.ErrorKind_ERROR_KIND_NOT_NULL_VIOLATION,
	"547":       irv1.ErrorKind_ERROR_KIND_FOREIGN_KEY_VIOLATION,
	"547/CHECK": irv1.ErrorKind_ERROR_KIND_CHECK_VIOLATION,
	"1205":      irv1.ErrorKind_ERROR_KIND_DEADLOCK,              // chosen as the deadlock victim
	"1222":      irv1.ErrorKind_ERROR_KIND_LOCK_TIMEOUT,          // LOCK_TIMEOUT ran out, or NOWAIT
	"2601":      irv1.ErrorKind_ERROR_KIND_UNIQUE_VIOLATION,      // a unique index
	"2627":      irv1.ErrorKind_ERROR_KIND_UNIQUE_VIOLATION,      // a primary key or unique constraint
	"3960":      irv1.ErrorKind_ERROR_KIND_SERIALIZATION_FAILURE, // snapshot update conflict
	"3961":      irv1.ErrorKind_ERROR_KIND_SERIALIZATION_FAILURE, // snapshot conflict with DDL
	"10053":     irv1.ErrorKind_ERROR_KIND_CONNECTION,            // connection aborted
	"10054":     irv1.ErrorKind_ERROR_KIND_CONNECTION,            // connection reset
}

const keySensitive = "core.sensitive"

// Violations returns how SQL Server reports a violation of each rule of
// the catalogs' tables (§23.2). A duplicate names its primary key, unique
// constraint or unique index, and a foreign-key or check violation its
// constraint, each with the table as schema.name; a not-null violation
// names the column. So what is reported is the rule's own name.
//
// Only a duplicate's message holds values, the duplicate key's, so only it
// is redacted, when one of the key's columns is sensitive (§23.3).
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
		redact := slices.ContainsFunc(u.GetColumns(), func(c string) bool { return sensitive[c] })
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
