package catalog

import (
	"slices"
	"strings"

	"github.com/thec1oud/dql/core/diagnostics"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// sortCatalogs sorts what §3.2 rule 2 sorts and core/facts does not:
// catalogs, namespaces, enums, domains, composites and sequences by stored
// name, check and exclusion constraints by name, view dependencies by
// qualified name, and fidelity limits. Columns, attributes and arguments keep
// the database's order (rule 1). core/facts sorts tables, routines,
// uniqueness facts, foreign keys, relations and overloads, and
// core/annotate.Merge sorts annotations. Extensions keep the order the source
// gave them, untouched (§6.3).
func sortCatalogs(cats []*irv1.Catalog) {
	slices.SortStableFunc(cats, func(a, b *irv1.Catalog) int { return byStored(a.GetName(), b.GetName()) })
	for _, c := range cats {
		slices.Sort(c.GetProvenance().GetFidelityLimits())
		slices.SortFunc(c.Namespaces, func(a, b *irv1.Namespace) int { return byStored(a.GetName(), b.GetName()) })
		for _, ns := range c.GetNamespaces() {
			slices.SortFunc(ns.Enums, func(a, b *irv1.Enum) int { return byStored(a.GetName(), b.GetName()) })
			slices.SortFunc(ns.Domains, func(a, b *irv1.Domain) int { return byStored(a.GetName(), b.GetName()) })
			slices.SortFunc(ns.Composites, func(a, b *irv1.Composite) int { return byStored(a.GetName(), b.GetName()) })
			slices.SortFunc(ns.Sequences, func(a, b *irv1.Sequence) int { return byStored(a.GetName(), b.GetName()) })
			for _, t := range ns.GetTables() {
				sortConstraints(t.GetConstraints())
				slices.SortFunc(t.DependsOn, diagnostics.CompareNames)
			}
			for _, d := range ns.GetDomains() {
				sortConstraints(d.GetConstraints())
			}
		}
	}
}

func sortConstraints(cs []*irv1.Constraint) {
	slices.SortFunc(cs, func(a, b *irv1.Constraint) int { return strings.Compare(a.GetName(), b.GetName()) })
}

func byStored(a, b *irv1.Identifier) int { return strings.Compare(a.GetStored(), b.GetStored()) }

// dialectName is the dialect as diagnostics name it: DIALECT_POSTGRES is
// postgres, as in §5.3.
func dialectName(d irv1.Dialect) string {
	return strings.ToLower(strings.TrimPrefix(d.String(), "DIALECT_"))
}

func sortedDialects(m map[irv1.Dialect][]string) []irv1.Dialect {
	var out []irv1.Dialect
	for d := range m {
		out = append(out, d)
	}
	slices.Sort(out)
	return out
}

// joinList writes items for a message: a, b and c.
func joinList(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

func joinNames(names []string) string { return strings.Join(names, ", ") }
