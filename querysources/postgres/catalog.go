package postgres

import (
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// index finds the bundle's objects by name, as a query names them.
type index struct {
	catalogs []*irv1.Catalog
	domains  map[string]*irv1.Domain
}

func newIndex(b *irv1.Bundle) *index {
	ix := &index{catalogs: b.GetCatalogs(), domains: map[string]*irv1.Domain{}}
	for _, c := range ix.catalogs {
		for _, n := range c.GetNamespaces() {
			for _, d := range n.GetDomains() {
				ix.domains[qkey(c.GetName().GetStored(), n.GetName().GetStored(), d.GetName().GetStored())] = d
			}
		}
	}
	return ix
}

func qkey(parts ...string) string {
	k := ""
	for _, p := range parts {
		k += p + "\x00"
	}
	return k
}

// found is an object of a namespace, with where it is.
type found[T any] struct {
	catalog, namespace string
	obj                T
}

// columnName returns the qualified name of a column of table t.
func columnName(t found[*irv1.Table], column string) *irv1.QualifiedName {
	return &irv1.QualifiedName{Catalog: t.catalog, Namespace: t.namespace, Object: t.obj.GetName().GetStored(), Member: column}
}

// lookup finds the objects named name. A qualified name (namespace, and
// possibly catalog) names one place. An unqualified one is looked for in
// each catalog's default namespace first, as a search path would, then in
// every namespace. pick returns a namespace's objects of the wanted kind.
func lookup[T any](ix *index, catalog, namespace, name string, pick func(*irv1.Namespace) []T, nameOf func(T) string) []found[T] {
	var direct, others []found[T]
	for _, c := range ix.catalogs {
		cn := c.GetName().GetStored()
		if catalog != "" && catalog != cn {
			continue
		}
		for _, n := range c.GetNamespaces() {
			nn := n.GetName().GetStored()
			if namespace != "" && namespace != nn {
				continue
			}
			for _, o := range pick(n) {
				if nameOf(o) != name {
					continue
				}
				f := found[T]{catalog: cn, namespace: nn, obj: o}
				if namespace != "" || nn == c.GetDefaultNamespace() {
					direct = append(direct, f)
				} else {
					others = append(others, f)
				}
			}
		}
	}
	if len(direct) > 0 {
		return direct
	}
	return others
}

func (ix *index) tables(catalog, namespace, name string) []found[*irv1.Table] {
	return lookup(ix, catalog, namespace, name, (*irv1.Namespace).GetTables,
		func(t *irv1.Table) string { return t.GetName().GetStored() })
}

func (ix *index) routines(namespace, name string) []found[*irv1.Routine] {
	return lookup(ix, "", namespace, name, (*irv1.Namespace).GetRoutines,
		func(r *irv1.Routine) string { return r.GetName().GetStored() })
}

// namedType finds an enum, domain or composite type by name, for a cast.
func (ix *index) namedType(namespace, name string) []*irv1.Type {
	var out []*irv1.Type
	add := func(ref *irv1.NamedRef) { out = append(out, &irv1.Type{Kind: &irv1.Type_Named{Named: ref}}) }
	for _, c := range ix.catalogs {
		cn := c.GetName().GetStored()
		for _, n := range c.GetNamespaces() {
			nn := n.GetName().GetStored()
			if namespace != "" && namespace != nn || namespace == "" && nn != c.GetDefaultNamespace() {
				continue
			}
			q := &irv1.QualifiedName{Catalog: cn, Namespace: nn, Object: name}
			for _, e := range n.GetEnums() {
				if e.GetName().GetStored() == name {
					add(&irv1.NamedRef{Kind: &irv1.NamedRef_Enum{Enum: q}})
				}
			}
			for _, d := range n.GetDomains() {
				if d.GetName().GetStored() == name {
					add(&irv1.NamedRef{Kind: &irv1.NamedRef_Domain{Domain: q}})
				}
			}
			for _, d := range n.GetComposites() {
				if d.GetName().GetStored() == name {
					add(&irv1.NamedRef{Kind: &irv1.NamedRef_Composite{Composite: q}})
				}
			}
		}
	}
	return out
}

// base follows domains to the type they are over.
func (ix *index) base(t *irv1.Type) *irv1.Type {
	for {
		d := t.GetNamed().GetDomain()
		if d == nil {
			return t
		}
		// The core has checked that every domain a type names exists.
		t = ix.domains[qkey(d.GetCatalog(), d.GetNamespace(), d.GetObject())].GetBase()
	}
}
