package sqlite

import (
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// index finds the bundle's tables and views by name, as a query names
// them. A SQLite catalog is one database file, whose main schema is the
// namespace, with no name; SQLite's names ignore case.
type index struct {
	catalogs []*irv1.Catalog
}

func newIndex(b *irv1.Bundle) *index { return &index{catalogs: b.GetCatalogs()} }

func qkey(parts ...string) string { return strings.Join(parts, "\x00") }

// found is a table of a namespace, with where it is.
type found struct {
	catalog, namespace string
	table              *irv1.Table
}

// columnName returns the qualified name of a column of table t.
func columnName(t found, column string) *irv1.QualifiedName {
	return &irv1.QualifiedName{Catalog: t.catalog, Namespace: t.namespace, Object: t.table.GetName().GetStored(), Member: column}
}

// tables finds the tables and views of every catalog named name.
func (ix *index) tables(name string) []found {
	var out []found
	for _, c := range ix.catalogs {
		for _, n := range c.GetNamespaces() {
			for _, t := range n.GetTables() {
				if strings.EqualFold(t.GetName().GetStored(), name) {
					out = append(out, found{catalog: c.GetName().GetStored(), namespace: n.GetName().GetStored(), table: t})
				}
			}
		}
	}
	return out
}
