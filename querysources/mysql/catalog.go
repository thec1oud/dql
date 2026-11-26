package mysql

import (
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// index finds the bundle's objects by name, as a query names them. A MySQL
// catalog is one database, whose namespace has no name, so a query names
// its tables and routines unqualified.
type index struct {
	catalogs []*irv1.Catalog
}

func newIndex(b *irv1.Bundle) *index { return &index{catalogs: b.GetCatalogs()} }

func qkey(parts ...string) string { return strings.Join(parts, "\x00") }

// found is an object of a namespace, with where it is.
type found[T any] struct {
	catalog, namespace string
	obj                T
}

// columnName returns the qualified name of a column of table t.
func columnName(t found[*irv1.Table], column string) *irv1.QualifiedName {
	return &irv1.QualifiedName{Catalog: t.catalog, Namespace: t.namespace, Object: t.obj.GetName().GetStored(), Member: column}
}

// lookup finds the objects of every catalog named name; pick returns a
// namespace's objects of the wanted kind.
func lookup[T any](ix *index, name string, pick func(*irv1.Namespace) []T, nameOf func(T) string) []found[T] {
	var out []found[T]
	for _, c := range ix.catalogs {
		for _, n := range c.GetNamespaces() {
			for _, o := range pick(n) {
				if nameOf(o) == name {
					out = append(out, found[T]{catalog: c.GetName().GetStored(), namespace: n.GetName().GetStored(), obj: o})
				}
			}
		}
	}
	return out
}

func (ix *index) tables(name string) []found[*irv1.Table] {
	return lookup(ix, name, (*irv1.Namespace).GetTables, func(t *irv1.Table) string { return t.GetName().GetStored() })
}

// routines finds routines by their lowercase name: MySQL's routine names
// ignore case.
func (ix *index) routines(name string) []found[*irv1.Routine] {
	return lookup(ix, name, (*irv1.Namespace).GetRoutines, func(r *irv1.Routine) string { return strings.ToLower(r.GetName().GetStored()) })
}
