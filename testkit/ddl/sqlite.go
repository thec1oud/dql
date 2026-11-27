package ddl

import (
	"google.golang.org/protobuf/types/known/anypb"

	sqliteextv1 "github.com/thec1oud/dql/proto/dql/ext/sqlite/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// sqlite writes DDL as sqlite-live reads it back: SQLite keeps the text of
// every table, index, view and trigger, and sqlite-live reads its columns,
// checks and comments out of that text, so the writer replays it.
type sqlite struct {
	script
	indexes, views, triggers []string
}

func (w *sqlite) write(c *irv1.Catalog) (string, error) {
	for _, m := range w.unpack("catalog "+c.GetName().GetStored(), c.GetExtensions()) {
		if _, ok := m.(*sqliteextv1.Database); !ok {
			w.unwritten("catalog", m)
		}
	}
	for _, ns := range c.GetNamespaces() {
		for _, t := range ns.GetTables() {
			w.table(t)
		}
	}
	w.stmts = append(w.stmts, w.indexes...)
	w.stmts = append(w.stmts, w.views...)
	w.stmts = append(w.stmts, w.triggers...)
	return w.join(";\n")
}

func (w *sqlite) table(t *irv1.Table) {
	what := "table " + t.GetName().GetStored()
	written := false
	for _, m := range w.unpack(what, t.GetExtensions()) {
		switch e := m.(type) {
		case *sqliteextv1.TableDefinition:
			w.add("%s", e.GetSql())
			written = true
		case *sqliteextv1.ViewDefinition:
			w.views = append(w.views, e.GetSql())
			written = true
		case *sqliteextv1.Trigger:
			w.triggers = append(w.triggers, e.GetSql())
		case *sqliteextv1.Index:
			w.index(e)
		case *sqliteextv1.Comment:
			// Part of the definition's text.
		default:
			w.unwritten(what, m)
		}
	}
	if !written {
		w.fail("%s: no definition", what)
	}
	// Columns' and constraints' comments and checks are part of the table's
	// text, and so are the indexes of its keys.
	for _, col := range t.GetColumns() {
		w.inText(what, col.GetExtensions())
	}
	for _, c := range t.GetConstraints() {
		w.inText(what, c.GetExtensions())
	}
	for _, u := range t.GetUniqueness() {
		for _, m := range w.unpack(what, u.GetExtensions()) {
			if e, ok := m.(*sqliteextv1.Index); ok {
				w.index(e)
			} else {
				w.unwritten(what, m)
			}
		}
	}
	for _, fk := range t.GetForeignKeys() {
		w.inText(what, fk.GetExtensions())
	}
}

// inText accepts the comments and checks SQLite keeps in a table's text.
func (w *sqlite) inText(what string, exts []*anypb.Any) {
	for _, m := range w.unpack(what, exts) {
		switch m.(type) {
		case *sqliteextv1.Comment, *sqliteextv1.Check:
		default:
			w.unwritten(what, m)
		}
	}
}

// index writes an index created by CREATE INDEX; SQLite makes the others
// for the keys its table's text declares.
func (w *sqlite) index(e *sqliteextv1.Index) {
	if e.GetOrigin() == "c" {
		w.indexes = append(w.indexes, e.GetSql())
	}
}
