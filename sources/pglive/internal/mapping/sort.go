package mapping

import (
	"cmp"
	"slices"
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/pglive/internal/introspect"
)

// canonical returns a copy of s with every list that has no database order
// sorted bytewise (§3.2 rule 2), so the mapping's output does not depend on
// the order in which the Snapshot lists objects. Columns, attributes,
// arguments, labels, constraint, index and statistics column lists, and
// inheritance parents keep their order (§3.2 rule 1).
func canonical(s *introspect.Snapshot) *introspect.Snapshot {
	c := *s
	c.Extensions = sorted(s.Extensions, func(e introspect.Extension) string { return e.Name })
	c.Schemas = sorted(s.Schemas, func(sc introspect.Schema) string { return sc.Name })
	for i := range c.Schemas {
		c.Schemas[i].Grants = sortGrants(c.Schemas[i].Grants)
	}
	c.Collations = sorted(s.Collations, func(co introspect.Collation) string { return key(co.Schema, co.Name) })
	c.Relations = sorted(s.Relations, func(r introspect.Relation) string { return key(r.Schema, r.Name) })
	for i := range c.Relations {
		r := &c.Relations[i]
		r.Columns = slices.Clone(r.Columns)
		for j := range r.Columns {
			r.Columns[j].Grants = sortGrants(r.Columns[j].Grants)
		}
		r.DependsOn = sorted(r.DependsOn, func(o introspect.ObjectName) string { return key(o.Schema, o.Name) })
		r.Constraints = sorted(r.Constraints, func(c introspect.Constraint) string { return c.Name })
		r.Indexes = sorted(r.Indexes, func(x introspect.Index) string { return x.Name })
		r.Triggers = sorted(r.Triggers, func(t introspect.Trigger) string { return t.Name })
		r.Policies = sorted(r.Policies, func(p introspect.Policy) string { return p.Name })
		r.Grants = sortGrants(r.Grants)
	}
	c.Enums = sorted(s.Enums, func(e introspect.Enum) string { return key(e.Schema, e.Name) })
	for i := range c.Enums {
		c.Enums[i].Grants = sortGrants(c.Enums[i].Grants)
	}
	c.Domains = sorted(s.Domains, func(d introspect.Domain) string { return key(d.Schema, d.Name) })
	for i := range c.Domains {
		c.Domains[i].Checks = sorted(c.Domains[i].Checks, func(ch introspect.DomainCheck) string { return ch.Name })
		c.Domains[i].Grants = sortGrants(c.Domains[i].Grants)
	}
	c.Composites = sorted(s.Composites, func(ct introspect.Composite) string { return key(ct.Schema, ct.Name) })
	for i := range c.Composites {
		c.Composites[i].Grants = sortGrants(c.Composites[i].Grants)
	}
	c.Ranges = sorted(s.Ranges, func(r introspect.Range) string { return key(r.Schema, r.Name) })
	for i := range c.Ranges {
		c.Ranges[i].Grants = sortGrants(c.Ranges[i].Grants)
	}
	c.BaseTypes = sorted(s.BaseTypes, func(b introspect.BaseType) string { return key(b.Schema, b.Name) })
	for i := range c.BaseTypes {
		c.BaseTypes[i].Grants = sortGrants(c.BaseTypes[i].Grants)
	}
	c.Sequences = sorted(s.Sequences, func(sq introspect.Sequence) string { return key(sq.Schema, sq.Name) })
	for i := range c.Sequences {
		c.Sequences[i].Grants = sortGrants(c.Sequences[i].Grants)
	}
	c.Routines = sorted(s.Routines, func(r introspect.Routine) string {
		return key(r.Schema, r.Name, r.IdentityArguments)
	})
	for i := range c.Routines {
		c.Routines[i].Grants = sortGrants(c.Routines[i].Grants)
	}
	c.Statistics = sorted(s.Statistics, func(st introspect.Statistics) string { return key(st.Schema, st.Name) })
	return &c
}

// key joins name parts with a byte that sorts before any character a name
// can hold, so "a"."b" sorts before "a b"."c".
func key(parts ...string) string { return strings.Join(parts, "\x00") }

// sorted returns a copy of s sorted by k.
func sorted[T any](s []T, k func(T) string) []T {
	out := slices.Clone(s)
	slices.SortStableFunc(out, func(a, b T) int { return strings.Compare(k(a), k(b)) })
	return out
}

func sortGrants(gs []introspect.Grant) []introspect.Grant {
	return sorted(gs, func(g introspect.Grant) string { return key(g.Grantee, g.Grantor, g.Privilege) })
}

// sortDiagnostics orders diagnostics by severity, then qualified name, then
// code (§3.2 rule 5), and then message, so equal diagnostics of different
// objects keep a fixed order.
func sortDiagnostics(ds []*irv1.Diagnostic) {
	slices.SortStableFunc(ds, func(a, b *irv1.Diagnostic) int {
		ta, tb := a.GetTarget(), b.GetTarget()
		return cmp.Or(
			cmp.Compare(a.GetSeverity(), b.GetSeverity()),
			strings.Compare(key(ta.GetCatalog(), ta.GetNamespace(), ta.GetObject(), ta.GetMember()),
				key(tb.GetCatalog(), tb.GetNamespace(), tb.GetObject(), tb.GetMember())),
			strings.Compare(a.GetCode(), b.GetCode()),
			strings.Compare(a.GetMessage(), b.GetMessage()),
		)
	})
}
