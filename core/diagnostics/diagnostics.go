// Package diagnostics builds and orders the core's generation diagnostics.
// Every diagnostic has a stable kebab-case code, names a qualified target and
// says how to fix the problem (implementation guide §7.13). Lists are sorted
// by severity, then qualified name, then code (design record §3.2 rule 5).
package diagnostics

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Errorf returns an error diagnostic: one that fails generation.
func Errorf(code string, target *irv1.QualifiedName, format string, args ...any) *irv1.Diagnostic {
	return newDiagnostic(irv1.Severity_SEVERITY_ERROR, code, target, format, args)
}

// Warningf returns a warning diagnostic: one carried through to emitters.
func Warningf(code string, target *irv1.QualifiedName, format string, args ...any) *irv1.Diagnostic {
	return newDiagnostic(irv1.Severity_SEVERITY_WARNING, code, target, format, args)
}

func newDiagnostic(s irv1.Severity, code string, target *irv1.QualifiedName, format string, args []any) *irv1.Diagnostic {
	return &irv1.Diagnostic{Severity: s, Code: code, Target: target, Message: fmt.Sprintf(format, args...)}
}

// Sort orders ds by severity (errors first), then target, then code (§3.2
// rule 5). Shape, query and message break the remaining ties, so the order
// never depends on the order the checks ran in.
func Sort(ds []*irv1.Diagnostic) {
	slices.SortStableFunc(ds, func(a, b *irv1.Diagnostic) int {
		return cmp.Or(
			cmp.Compare(a.GetSeverity(), b.GetSeverity()),
			CompareNames(a.GetTarget(), b.GetTarget()),
			strings.Compare(a.GetCode(), b.GetCode()),
			strings.Compare(a.GetShape(), b.GetShape()),
			strings.Compare(a.GetQuery(), b.GetQuery()),
			strings.Compare(a.GetMessage(), b.GetMessage()),
		)
	})
}

// CompareNames orders qualified names part by part, comparing bytes, so the
// order is locale-independent (§3.2 rule 4).
func CompareNames(a, b *irv1.QualifiedName) int {
	return cmp.Or(
		strings.Compare(a.GetCatalog(), b.GetCatalog()),
		strings.Compare(a.GetNamespace(), b.GetNamespace()),
		strings.Compare(a.GetObject(), b.GetObject()),
		strings.Compare(a.GetMember(), b.GetMember()),
	)
}

// Name renders q for messages as its non-empty parts joined by dots, so a
// MySQL table in the implicit namespace "" reads billing.invoices (§5.1).
func Name(q *irv1.QualifiedName) string {
	var parts []string
	for _, p := range []string{q.GetCatalog(), q.GetNamespace(), q.GetObject(), q.GetMember()} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, ".")
}
