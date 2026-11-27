// Package types checks and reasons over the logical types of a catalog
// (design record §8): named references, arrays, opaque types and their
// mapping, weak types, the optional IR features a bundle uses (§3.3), and the
// logical type words of overload names (§4.6).
//
// The core never parses SQL (§1.2, P5): every type arrives classified by its
// source plugin, and this package only checks what it was given.
package types

import (
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// key identifies an object by catalog, namespace and stored name, compared
// exactly as stored: the IR never folds names (§4.1).
type key struct{ catalog, namespace, object string }

func keyOf(q *irv1.QualifiedName) key {
	return key{q.GetCatalog(), q.GetNamespace(), q.GetObject()}
}

// Index finds the enums, domains and composite types of a bundle by
// qualified name.
type Index struct {
	enums      map[key]*irv1.Enum
	domains    map[key]*irv1.Domain
	composites map[key]*irv1.Composite
}

// NewIndex indexes the named types of every catalog in b.
func NewIndex(b *irv1.Bundle) *Index {
	ix := &Index{
		enums:      map[key]*irv1.Enum{},
		domains:    map[key]*irv1.Domain{},
		composites: map[key]*irv1.Composite{},
	}
	for _, c := range b.GetCatalogs() {
		cat := c.GetName().GetStored()
		for _, ns := range c.GetNamespaces() {
			nsName := ns.GetName().GetStored()
			for _, e := range ns.GetEnums() {
				ix.enums[key{cat, nsName, e.GetName().GetStored()}] = e
			}
			for _, d := range ns.GetDomains() {
				ix.domains[key{cat, nsName, d.GetName().GetStored()}] = d
			}
			for _, co := range ns.GetComposites() {
				ix.composites[key{cat, nsName, co.GetName().GetStored()}] = co
			}
		}
	}
	return ix
}

// Enum returns the enum q names, or nil.
func (ix *Index) Enum(q *irv1.QualifiedName) *irv1.Enum { return ix.enums[keyOf(q)] }

// Domain returns the domain q names, or nil.
func (ix *Index) Domain(q *irv1.QualifiedName) *irv1.Domain { return ix.domains[keyOf(q)] }

// Composite returns the composite type q names, or nil.
func (ix *Index) Composite(q *irv1.QualifiedName) *irv1.Composite { return ix.composites[keyOf(q)] }

// resolves reports whether a named reference names an existing object of its kind.
func (ix *Index) resolves(n *irv1.NamedRef) bool {
	switch k := n.GetKind().(type) {
	case *irv1.NamedRef_Enum:
		return ix.Enum(k.Enum) != nil
	case *irv1.NamedRef_Domain:
		return ix.Domain(k.Domain) != nil
	}
	return ix.Composite(n.GetComposite()) != nil
}

// Underlying follows domain references to the first type that is not a
// domain: the type a domain's values are compared in (§8.5). It returns nil
// when a domain does not resolve or the chain loops.
func (ix *Index) Underlying(t *irv1.Type) *irv1.Type {
	seen := map[key]bool{}
	for {
		q := t.GetNamed().GetDomain()
		if q == nil {
			return t
		}
		k := keyOf(q)
		d := ix.domains[k]
		if d == nil || seen[k] {
			return nil
		}
		seen[k] = true
		t = d.GetBase()
	}
}
