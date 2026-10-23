package annotate

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Diagnostic codes. They are stable: tools and tests match on them
// (implementation guide §7, rule 13).
const (
	// CodeKeyInvalid: a key is neither core.<key> nor x.<plugin>.<key> (§7.2).
	CodeKeyInvalid = "annotation-key-invalid"
	// CodeUnknown: a core. key the vocabulary does not define (§7.3).
	CodeUnknown = "annotation-unknown"
	// CodeWrongTarget: a key on an object §7.2 does not allow it on (§7.3).
	CodeWrongTarget = "annotation-wrong-target"
	// CodeValueInvalid: a value of the wrong form, or outside its allowed set.
	CodeValueInvalid = "annotation-value-invalid"
	// CodeEnumNotMembership: enum on a check that is not a single-column
	// membership test (§7.3, §8.4).
	CodeEnumNotMembership = "enum-not-membership-test"
	// CodeLogicalKeyNullable: logical-key over a nullable column (§7.3, §11.1).
	CodeLogicalKeyNullable = "logical-key-nullable"
	// CodeBoundWithVersion: bound and version on one column (§7.3).
	CodeBoundWithVersion = "bound-with-version"
	// CodeRedundant: an annotation that changes nothing (§7.3). A warning.
	CodeRedundant = "annotation-redundant"
	// CodeDuplicate: one source gave a key twice with the same value. A warning.
	CodeDuplicate = "annotation-duplicate"
	// CodeConflict: one source gave a key twice with different values.
	CodeConflict = "annotation-conflict"
	// CodeOriginMissing: an annotation that does not say where it was read
	// (§7.1: every annotation records where it came from).
	CodeOriginMissing = "annotation-origin-missing"
)

func diag(sev irv1.Severity, code string, target *irv1.QualifiedName, format string, args ...any) *irv1.Diagnostic {
	return &irv1.Diagnostic{Severity: sev, Code: code, Target: target, Message: fmt.Sprintf(format, args...)}
}

func errorf(code string, target *irv1.QualifiedName, format string, args ...any) *irv1.Diagnostic {
	return diag(irv1.Severity_SEVERITY_ERROR, code, target, format, args...)
}

func warnf(code string, target *irv1.QualifiedName, format string, args ...any) *irv1.Diagnostic {
	return diag(irv1.Severity_SEVERITY_WARNING, code, target, format, args...)
}

// SortDiagnostics sorts by severity, then qualified name, then code (§3.2,
// rule 5). The message breaks remaining ties so the order is total. Strings
// compare bytewise, never by locale (§3.2, rule 4).
func SortDiagnostics(ds []*irv1.Diagnostic) {
	slices.SortStableFunc(ds, func(a, b *irv1.Diagnostic) int {
		return cmp.Or(
			cmp.Compare(a.GetSeverity(), b.GetSeverity()),
			strings.Compare(a.GetTarget().GetCatalog(), b.GetTarget().GetCatalog()),
			strings.Compare(a.GetTarget().GetNamespace(), b.GetTarget().GetNamespace()),
			strings.Compare(a.GetTarget().GetObject(), b.GetTarget().GetObject()),
			strings.Compare(a.GetTarget().GetMember(), b.GetTarget().GetMember()),
			strings.Compare(a.GetCode(), b.GetCode()),
			strings.Compare(a.GetMessage(), b.GetMessage()),
		)
	})
}

// display writes a qualified name as messages show it, as in §5.3:
// its non-empty parts joined by dots.
func display(q *irv1.QualifiedName) string {
	var parts []string
	for _, p := range []string{q.GetCatalog(), q.GetNamespace(), q.GetObject(), q.GetMember()} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, ".")
}
