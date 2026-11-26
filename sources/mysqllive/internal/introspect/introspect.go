// Package introspect reads a MySQL or MariaDB database's catalog from
// information_schema into a Snapshot, in one read-only transaction so that
// every query sees the same schema.
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

// Read reads the database named db. It fails when the database does not
// exist.
func Read(ctx context.Context, q Querier, db string) (*Snapshot, error) {
	r := &reader{ctx: ctx, q: q, db: db}
	s := &Snapshot{}
	r.database(s)
	r.tables(s)
	r.routines(s)
	if r.err != nil {
		return nil, r.err
	}
	return s, nil
}

// reader holds one Read; its first error sticks and later queries are skipped.
type reader struct {
	ctx context.Context
	q   Querier
	db  string
	err error
}

// query runs sql with args and scans each row into dest's pointers, which
// scan gives for each row.
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

func (r *reader) database(s *Snapshot) {
	var version string
	found := false
	var cs, co string
	r.query("the server version", "SELECT VERSION()", func() []any { return []any{&version} }, func() {})
	r.query("the database", `SELECT DEFAULT_CHARACTER_SET_NAME, DEFAULT_COLLATION_NAME
		FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = ?`,
		func() []any { return []any{&cs, &co} }, func() { found = true }, r.db)
	if r.err == nil && !found {
		r.err = fmt.Errorf("database %s does not exist", r.db)
		return
	}
	s.Database = Database{Flavor: "mysql", CharacterSet: cs, Collation: co}
	if strings.Contains(strings.ToLower(version), "mariadb") {
		s.Database.Flavor = "mariadb"
	}
	// The collations the database and its columns use. MariaDB has no
	// PAD_ATTRIBUTE; its NO PAD collations say so in their names.
	pad := "PAD_ATTRIBUTE = 'PAD SPACE'"
	if s.Database.Flavor == "mariadb" {
		pad = "COLLATION_NAME NOT LIKE '%nopad%'"
	}
	var c Collation
	r.query("collations", `SELECT COLLATION_NAME, `+pad+` FROM information_schema.COLLATIONS
		WHERE COLLATION_NAME = ? OR COLLATION_NAME IN
		(SELECT COLLATION_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ?)
		ORDER BY COLLATION_NAME`,
		func() []any { c = Collation{}; return []any{&c.Name, &c.PadSpace} },
		func() { s.Database.Collations = append(s.Database.Collations, c) }, co, r.db)
}

func (r *reader) tables(s *Snapshot) {
	byName := map[string]*Table{}
	var names []string
	var t Table
	var kind string
	var comment, engine, collation, options sql.NullString
	r.query("tables", `SELECT TABLE_NAME, TABLE_TYPE, TABLE_COMMENT, ENGINE, TABLE_COLLATION, CREATE_OPTIONS
		FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_TYPE IN ('BASE TABLE', 'VIEW')
		ORDER BY TABLE_NAME`,
		func() []any { t = Table{}; return []any{&t.Name, &kind, &comment, &engine, &collation, &options} },
		func() {
			t.View = kind == "VIEW"
			if !t.View {
				t.Comment, t.Engine, t.Collation, t.CreateOptions = comment.String, engine.String, collation.String, options.String
			}
			names = append(names, t.Name)
			byName[t.Name] = &Table{}
			*byName[t.Name] = t
		}, r.db)
	r.columns(byName)
	r.checks(byName)
	r.indexes(byName)
	r.foreignKeys(byName)
	r.triggers(byName)
	r.views(byName)
	for _, n := range names {
		s.Tables = append(s.Tables, *byName[n])
	}
}

func (r *reader) columns(byName map[string]*Table) {
	var table string
	var c Column
	var nullable string
	var def, cs, co, gen sql.NullString
	r.query("columns", `SELECT TABLE_NAME, COLUMN_NAME, DATA_TYPE, COLUMN_TYPE, IS_NULLABLE, COLUMN_DEFAULT,
		EXTRA, GENERATION_EXPRESSION, CHARACTER_SET_NAME, COLLATION_NAME, COLUMN_COMMENT
		FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? ORDER BY TABLE_NAME, ORDINAL_POSITION`,
		func() []any {
			c = Column{}
			return []any{&table, &c.Name, &c.DataType, &c.ColumnType, &nullable, &def, &c.Extra, &gen, &cs, &co, &c.Comment}
		},
		func() {
			t := byName[table]
			c.Nullable = nullable == "YES"
			if def.Valid && !t.View {
				d := def.String
				c.Default = &d
			}
			c.Generation, c.CharacterSet, c.Collation = gen.String, cs.String, co.String
			t.Columns = append(t.Columns, c)
		}, r.db)
}

func (r *reader) checks(byName map[string]*Table) {
	var table string
	var c Check
	var enforced string
	r.query("check constraints", `SELECT tc.TABLE_NAME, cc.CONSTRAINT_NAME, cc.CHECK_CLAUSE, tc.ENFORCED
		FROM information_schema.CHECK_CONSTRAINTS cc
		JOIN information_schema.TABLE_CONSTRAINTS tc
		  ON tc.CONSTRAINT_SCHEMA = cc.CONSTRAINT_SCHEMA AND tc.CONSTRAINT_NAME = cc.CONSTRAINT_NAME
		 AND tc.CONSTRAINT_TYPE = 'CHECK'
		WHERE cc.CONSTRAINT_SCHEMA = ? ORDER BY tc.TABLE_NAME, cc.CONSTRAINT_NAME`,
		func() []any { c = Check{}; return []any{&table, &c.Name, &c.Clause, &enforced} },
		func() {
			if t := byName[table]; t != nil {
				c.Enforced = enforced != "NO"
				t.Checks = append(t.Checks, c)
			}
		}, r.db)
}

func (r *reader) indexes(byName map[string]*Table) {
	var table, name, typ, visible, comment string
	var nonUnique int
	var col, coll, expr sql.NullString
	var sub sql.NullInt64
	byIndex := map[[2]string]*Index{}
	var order [][2]string
	r.query("indexes", `SELECT TABLE_NAME, INDEX_NAME, NON_UNIQUE, COLUMN_NAME, SUB_PART, COLLATION, INDEX_TYPE,
		IS_VISIBLE, INDEX_COMMENT, EXPRESSION
		FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = ? ORDER BY TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX`,
		func() []any {
			return []any{&table, &name, &nonUnique, &col, &sub, &coll, &typ, &visible, &comment, &expr}
		},
		func() {
			k := [2]string{table, name}
			x := byIndex[k]
			if x == nil {
				x = &Index{Name: name, Unique: nonUnique == 0, Type: typ, Visible: visible != "NO", Comment: comment}
				byIndex[k] = x
				order = append(order, k)
			}
			x.Parts = append(x.Parts, IndexPart{Column: col.String, Prefix: uint32(sub.Int64), Expression: expr.String, Descending: coll.String == "D"})
		}, r.db)
	for _, k := range order {
		if t := byName[k[0]]; t != nil {
			t.Indexes = append(t.Indexes, *byIndex[k])
		}
	}
}

func (r *reader) foreignKeys(byName map[string]*Table) {
	var table, name, col, refDB, refTable, refCol, onUpdate, onDelete string
	byKey := map[[2]string]*ForeignKey{}
	var order [][2]string
	r.query("foreign keys", `SELECT k.TABLE_NAME, k.CONSTRAINT_NAME, k.COLUMN_NAME, k.REFERENCED_TABLE_SCHEMA,
		k.REFERENCED_TABLE_NAME, k.REFERENCED_COLUMN_NAME, rc.UPDATE_RULE, rc.DELETE_RULE
		FROM information_schema.KEY_COLUMN_USAGE k
		JOIN information_schema.REFERENTIAL_CONSTRAINTS rc
		  ON rc.CONSTRAINT_SCHEMA = k.CONSTRAINT_SCHEMA AND rc.CONSTRAINT_NAME = k.CONSTRAINT_NAME
		 AND rc.TABLE_NAME = k.TABLE_NAME
		WHERE k.TABLE_SCHEMA = ? AND k.REFERENCED_TABLE_NAME IS NOT NULL
		ORDER BY k.TABLE_NAME, k.CONSTRAINT_NAME, k.ORDINAL_POSITION`,
		func() []any { return []any{&table, &name, &col, &refDB, &refTable, &refCol, &onUpdate, &onDelete} },
		func() {
			k := [2]string{table, name}
			fk := byKey[k]
			if fk == nil {
				fk = &ForeignKey{Name: name, RefDB: refDB, RefTable: refTable, OnUpdate: onUpdate, OnDelete: onDelete}
				byKey[k] = fk
				order = append(order, k)
			}
			fk.Columns = append(fk.Columns, col)
			fk.RefCols = append(fk.RefCols, refCol)
		}, r.db)
	for _, k := range order {
		if t := byName[k[0]]; t != nil {
			t.ForeignKeys = append(t.ForeignKeys, *byKey[k])
		}
	}
}

func (r *reader) triggers(byName map[string]*Table) {
	var table string
	var tr Trigger
	r.query("triggers", `SELECT EVENT_OBJECT_TABLE, TRIGGER_NAME, ACTION_TIMING, EVENT_MANIPULATION, ACTION_ORDER,
		ACTION_STATEMENT FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = ? ORDER BY EVENT_OBJECT_TABLE, TRIGGER_NAME`,
		func() []any {
			tr = Trigger{}
			return []any{&table, &tr.Name, &tr.Timing, &tr.Event, &tr.Order, &tr.Statement}
		},
		func() {
			if t := byName[table]; t != nil {
				t.Triggers = append(t.Triggers, tr)
			}
		}, r.db)
}

func (r *reader) views(byName map[string]*Table) {
	var name, updatable string
	var v View
	r.query("views", `SELECT TABLE_NAME, VIEW_DEFINITION, IS_UPDATABLE, SECURITY_TYPE, CHECK_OPTION
		FROM information_schema.VIEWS WHERE TABLE_SCHEMA = ? ORDER BY TABLE_NAME`,
		func() []any {
			v = View{}
			return []any{&name, &v.Definition, &updatable, &v.SecurityType, &v.CheckOption}
		},
		func() {
			if t := byName[name]; t != nil {
				v.Updatable = updatable == "YES"
				t.ViewDef = &View{}
				*t.ViewDef = v
			}
		}, r.db)
	if r.err != nil {
		return
	}
	// MySQL 8.0.13 and later list what each view reads; MariaDB does not.
	var view, db, table string
	var has bool
	r.query("view usage support", `SELECT COUNT(*) > 0 FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = 'information_schema' AND TABLE_NAME = 'VIEW_TABLE_USAGE'`,
		func() []any { return []any{&has} }, func() {})
	if !has {
		return
	}
	r.query("view usage", `SELECT VIEW_NAME, TABLE_SCHEMA, TABLE_NAME FROM information_schema.VIEW_TABLE_USAGE
		WHERE VIEW_SCHEMA = ? ORDER BY VIEW_NAME, TABLE_SCHEMA, TABLE_NAME`,
		func() []any { return []any{&view, &db, &table} },
		func() {
			if t := byName[view]; t != nil && t.ViewDef != nil {
				t.ViewDef.Reads = append(t.ViewDef.Reads, db+"."+table)
			}
		}, r.db)
}

func (r *reader) routines(s *Snapshot) {
	byName := map[string]*Routine{}
	var rt Routine
	var returns, def sql.NullString
	var deterministic string
	r.query("routines", `SELECT ROUTINE_NAME, ROUTINE_TYPE, ROUTINE_COMMENT, DTD_IDENTIFIER, IS_DETERMINISTIC,
		SQL_DATA_ACCESS, SECURITY_TYPE, ROUTINE_DEFINITION
		FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = ? ORDER BY ROUTINE_NAME, ROUTINE_TYPE`,
		func() []any {
			rt = Routine{}
			return []any{&rt.Name, &rt.Type, &rt.Comment, &returns, &deterministic, &rt.DataAccess, &rt.SecurityType, &def}
		},
		func() {
			rt.Returns, rt.Definition, rt.Deterministic = returns.String, def.String, deterministic == "YES"
			s.Routines = append(s.Routines, rt)
		}, r.db)
	for i := range s.Routines {
		byName[s.Routines[i].Type+"."+s.Routines[i].Name] = &s.Routines[i]
	}
	var name, typ string
	var a Argument
	var mode, pname sql.NullString
	r.query("routine parameters", `SELECT SPECIFIC_NAME, ROUTINE_TYPE, PARAMETER_MODE, PARAMETER_NAME, DTD_IDENTIFIER
		FROM information_schema.PARAMETERS WHERE SPECIFIC_SCHEMA = ? AND ORDINAL_POSITION > 0
		ORDER BY SPECIFIC_NAME, ROUTINE_TYPE, ORDINAL_POSITION`,
		func() []any { a = Argument{}; return []any{&name, &typ, &mode, &pname, &a.Type} },
		func() {
			if rt := byName[typ+"."+name]; rt != nil {
				a.Name, a.Mode = pname.String, cmp.Or(mode.String, "")
				if rt.Type == "FUNCTION" {
					a.Mode = ""
				}
				rt.Parameters = append(rt.Parameters, a)
			}
		}, r.db)
	slices.SortFunc(s.Routines, func(a, b Routine) int {
		return cmp.Or(strings.Compare(a.Name, b.Name), strings.Compare(a.Type, b.Type))
	})
}
