package shapes

import (
	"slices"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// index holds what shapes look up across catalogs: domains by qualified
// name.
type index struct {
	domains map[qn]*irv1.Domain
}

// qn is a qualified name as a map key.
type qn struct{ catalog, namespace, object string }

func keyOf(n *irv1.QualifiedName) qn {
	return qn{n.GetCatalog(), n.GetNamespace(), n.GetObject()}
}

func newIndex(catalogs []*irv1.Catalog) *index {
	ix := &index{domains: map[qn]*irv1.Domain{}}
	for _, c := range catalogs {
		for _, ns := range c.GetNamespaces() {
			for _, d := range ns.GetDomains() {
				ix.domains[qn{c.GetName().GetStored(), ns.GetName().GetStored(), d.GetName().GetStored()}] = d
			}
		}
	}
	return ix
}

// validation returns the names of the client-validated constraints a write
// shape checks c's value against (§10.5): those of its domain, outermost
// domain first, then the table's constraints on c alone, each in catalog
// order.
func (ix *index) validation(t *irv1.Table, c *irv1.Column) []string {
	var out []string
	seen := map[qn]bool{}
	for typ := c.GetType(); typ.GetNamed().GetDomain() != nil; {
		k := keyOf(typ.GetNamed().GetDomain())
		d := ix.domains[k]
		if d == nil || seen[k] {
			break
		}
		seen[k] = true
		out = append(out, validated(d.GetConstraints(), nil)...)
		typ = d.GetBase()
	}
	return append(out, validated(t.GetConstraints(), []string{c.GetName().GetStored()})...)
}

// validated returns the names of the client-validated constraints in cs over
// exactly columns; a domain's constraints have none.
func validated(cs []*irv1.Constraint, columns []string) []string {
	var out []string
	for _, k := range cs {
		if k.GetClientValidated() && slices.Equal(k.GetColumns(), columns) {
			out = append(out, k.GetName())
		}
	}
	return out
}
