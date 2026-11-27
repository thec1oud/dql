// Package introspect reads a SQLite database's main schema from
// sqlite_schema and its pragmas into a Snapshot, in one read transaction so
// that every query sees the same schema.
package introspect

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
)

// Querier is what Read needs from a connection: database/sql's *sql.Tx.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Read reads the database's main schema. Internal tables (sqlite_%),
// virtual tables and their shadow tables are left out.
func Read(ctx context.Context, q Querier) (*Snapshot, error) {
	r := &reader{ctx: ctx, q: q}
	s := &Snapshot{}
	r.query("the library version", "SELECT sqlite_version(), (SELECT encoding FROM pragma_encoding)",
		func() []any { return []any{&s.Database.Version, &s.Database.Encoding} }, func() {})
	r.tables(s)
	if r.err != nil {
		return nil, r.err
	}
	return s, nil
}

// reader holds one Read; its first error sticks and later queries are
// skipped.
type reader struct {
	ctx context.Context
	q   Querier
	err error
}

// query runs sql with args and scans each row into the pointers scan gives.
func (r *reader) query(what, query string, scan func() []any, row func(), args ...any) {
	if r.err != nil {
		return
	}
	rows, err := r.q.QueryContext(r.ctx, query, args...)
	if err != nil {
		r.err = fmt.Errorf("reading %s: %w", what, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		if err := rows.Scan(scan()...); err != nil {
			r.err = fmt.Errorf("reading %s: %w", what, err)
			return
		}
		row()
	}
	if err := rows.Err(); err != nil {
		r.err = fmt.Errorf("reading %s: %w", what, err)
	}
}

func (r *reader) tables(s *Snapshot) {
	var t Table
	var kind string
	r.query("the tables", `SELECT l.name, l.type, l.strict, l.wr, COALESCE(m.sql, '')
		FROM pragma_table_list l JOIN sqlite_schema m ON m.name = l.name AND m.type = l.type
		WHERE l.schema = 'main' AND l.type IN ('table', 'view') AND l.name NOT LIKE 'sqlite\_%' ESCAPE '\'
		  AND m.sql NOT LIKE 'CREATE VIRTUAL %'`,
		func() []any { t = Table{}; return []any{&t.Name, &kind, &t.Strict, &t.WithoutRowid, &t.SQL} },
		func() { t.View = kind == "view"; s.Tables = append(s.Tables, t) })
	// A virtual table's shadow tables are type shadow, which the filter
	// leaves out with the virtual table itself.
	slices.SortFunc(s.Tables, func(a, b Table) int { return strings.Compare(a.Name, b.Name) })
	for i := range s.Tables {
		r.table(&s.Tables[i])
	}
}

func (r *reader) table(t *Table) {
	var c Column
	var dflt sql.NullString
	r.query("the columns of "+t.Name, "SELECT name, type, \"notnull\", dflt_value, pk, hidden FROM pragma_table_xinfo(?) ORDER BY cid",
		func() []any {
			c = Column{}
			return []any{&c.Name, &c.Type, &c.NotNull, &dflt, &c.PrimaryKey, &c.Hidden}
		},
		func() {
			if dflt.Valid {
				d := dflt.String
				c.Default = &d
			}
			t.Columns = append(t.Columns, c)
		}, t.Name)
	if t.View {
		return
	}
	var x Index
	var partial int
	r.query("the indexes of "+t.Name, `SELECT l.name, l."unique", l.origin, l.partial, COALESCE(m.sql, '')
		FROM pragma_index_list(?) l LEFT JOIN sqlite_schema m ON m.type = 'index' AND m.name = l.name`,
		func() []any { x = Index{}; return []any{&x.Name, &x.Unique, &x.Origin, &partial, &x.SQL} },
		func() { x.Partial = partial == 1; t.Indexes = append(t.Indexes, x) }, t.Name)
	slices.SortFunc(t.Indexes, func(a, b Index) int { return strings.Compare(a.Name, b.Name) })
	for i := range t.Indexes {
		x := &t.Indexes[i]
		var p IndexPart
		var name sql.NullString
		r.query("the parts of index "+x.Name, "SELECT name, \"desc\", coll FROM pragma_index_xinfo(?) WHERE key = 1 ORDER BY seqno",
			func() []any { p = IndexPart{}; return []any{&name, &p.Descending, &p.Collation} },
			func() { p.Column = name.String; x.Parts = append(x.Parts, p) }, x.Name)
	}
	var id, seq int
	var from, to sql.NullString
	var fk ForeignKey
	var fks []ForeignKey
	last := -1
	r.query("the foreign keys of "+t.Name, `SELECT id, seq, "table", "from", "to", on_update, on_delete
		FROM pragma_foreign_key_list(?) ORDER BY id, seq`,
		func() []any { return []any{&id, &seq, &fk.RefTable, &from, &to, &fk.OnUpdate, &fk.OnDelete} },
		func() {
			if id != last {
				fks = append(fks, ForeignKey{RefTable: fk.RefTable, OnUpdate: fk.OnUpdate, OnDelete: fk.OnDelete})
				last = id
			}
			k := &fks[len(fks)-1]
			k.Columns = append(k.Columns, from.String)
			if to.Valid {
				k.RefColumns = append(k.RefColumns, to.String)
			}
		}, t.Name)
	// SQLite numbers foreign keys from the last declared; declaration order
	// is the reverse.
	slices.Reverse(fks)
	t.ForeignKeys = fks
	var tr Trigger
	r.query("the triggers of "+t.Name, "SELECT name, sql FROM sqlite_schema WHERE type = 'trigger' AND tbl_name = ?",
		func() []any { tr = Trigger{}; return []any{&tr.Name, &tr.SQL} },
		func() { t.Triggers = append(t.Triggers, tr) }, t.Name)
	slices.SortFunc(t.Triggers, func(a, b Trigger) int { return cmp.Compare(a.Name, b.Name) })
}
