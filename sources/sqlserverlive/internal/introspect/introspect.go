// Package introspect reads a SQL Server database's catalog from its sys
// views into a Snapshot, in one transaction so that every query sees the
// same schema. It reads the database the connection is in.
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

// Read reads the connection's current database.
func Read(ctx context.Context, q Querier) (*Snapshot, error) {
	r := &reader{ctx: ctx, q: q, comments: map[key]string{}, objects: map[int64]*object{}}
	s := &Snapshot{}
	r.database(s)
	r.readComments()
	r.schemas()
	r.objectsOf()
	r.columns()
	r.checks()
	r.indexes()
	r.foreignKeys()
	r.triggers()
	r.views()
	r.parameters()
	if r.err != nil {
		return nil, r.err
	}
	s.Schemas = r.snapshot()
	return s, nil
}

// Comment classes of sys.extended_properties.
const (
	classObject = 1 // an object, or a column of one by minor_id
	classSchema = 3
	classIndex  = 7
)

// key names what an extended property is on.
type key struct {
	class        int
	major, minor int64
}

// object is a table, view or routine while Read assembles it.
type object struct {
	schema int64
	table  *Table
	// routine is set for a function or procedure.
	routine *Routine
	// columns by column_id, for foreign keys and index parts.
	columns map[int64]string
}

// reader holds one Read; its first error sticks and later queries are skipped.
type reader struct {
	ctx      context.Context
	q        Querier
	err      error
	db       string
	comments map[key]string
	// schemaNames by schema_id, of the schemas read.
	schemaNames map[int64]string
	objects     map[int64]*object
}

// query runs query and calls row after scanning each row into the pointers
// scan gives.
func (r *reader) query(what, query string, scan func() []any, row func()) {
	if r.err != nil {
		return
	}
	rows, err := r.q.QueryContext(r.ctx, query)
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
	d := &s.Database
	var rcsi bool
	var snapshot uint8
	r.query("the database", `SELECT CAST(SERVERPROPERTY('ProductVersion') AS nvarchar(128)), name, collation_name,
			compatibility_level, is_read_committed_snapshot_on, snapshot_isolation_state
		FROM sys.databases WHERE database_id = DB_ID()`,
		func() []any {
			return []any{&d.Version, &r.db, &d.Collation, &d.CompatibilityLevel, &rcsi, &snapshot}
		}, func() {})
	d.ReadCommittedSnapshot = rcsi
	// 1 is ON; 2 and 3 are on the way off and on.
	d.SnapshotIsolation = snapshot == 1
}

func (r *reader) readComments() {
	var k key
	var text string
	r.query("comments", `SELECT class, major_id, minor_id, CAST(value AS nvarchar(max))
		FROM sys.extended_properties WHERE name = 'MS_Description' AND class IN (1, 3, 7)`,
		func() []any { return []any{&k.class, &k.major, &k.minor, &text} },
		func() { r.comments[k] = text })
}

func (r *reader) comment(class int, major, minor int64) string {
	return r.comments[key{class, major, minor}]
}

// schemas reads dbo and the schemas users made: not sys, guest,
// INFORMATION_SCHEMA or a fixed database role's.
func (r *reader) schemas() {
	r.schemaNames = map[int64]string{}
	var id int64
	var name string
	r.query("schemas", `SELECT schema_id, name FROM sys.schemas WHERE schema_id = 1 OR schema_id BETWEEN 5 AND 16383`,
		func() []any { return []any{&id, &name} }, func() { r.schemaNames[id] = name })
}

// userObjects is the condition on sys.objects o that picks the objects read.
const userObjects = `o.is_ms_shipped = 0 AND (o.schema_id = 1 OR o.schema_id BETWEEN 5 AND 16383)`

func (r *reader) objectsOf() {
	var id, schema int64
	var name, typ, typeDesc string
	r.query("tables, views and routines", `SELECT o.object_id, o.schema_id, o.name, RTRIM(o.type), o.type_desc
		FROM sys.objects o WHERE o.type IN ('U', 'V', 'FN', 'IF', 'TF', 'P') AND `+userObjects,
		func() []any { return []any{&id, &schema, &name, &typ, &typeDesc} },
		func() {
			o := &object{schema: schema, columns: map[int64]string{}}
			switch typ {
			case "U", "V":
				o.table = &Table{Name: name, View: typ == "V", Comment: r.comment(classObject, id, 0)}
			default:
				o.routine = &Routine{Name: name, Type: typeDesc, Comment: r.comment(classObject, id, 0)}
			}
			r.objects[id] = o
		})
}

// typeColumns are the columns of a Type, for a query on sys.columns or
// sys.parameters c joined to sys.types ut on its user_type_id.
const typeColumns = `TYPE_NAME(c.system_type_id), c.max_length, c.precision, c.scale,
	CASE WHEN ut.is_user_defined = 1 THEN SCHEMA_NAME(ut.schema_id) + '.' + ut.name ELSE '' END`

func (r *reader) columns() {
	var id, colID int64
	var c Column
	var collation, seed, increment, computed, defName, defText sql.NullString
	var identity, isComputed, persisted bool
	r.query("columns", `SELECT c.object_id, c.column_id, c.name, `+typeColumns+`, c.is_nullable, c.collation_name,
			c.is_identity, CAST(ic.seed_value AS nvarchar(64)), CAST(ic.increment_value AS nvarchar(64)),
			c.is_computed, cc.definition, COALESCE(cc.is_persisted, 0), c.is_rowguidcol, dc.name, dc.definition
		FROM sys.columns c
		JOIN sys.objects o ON o.object_id = c.object_id
		JOIN sys.types ut ON ut.user_type_id = c.user_type_id
		LEFT JOIN sys.identity_columns ic ON ic.object_id = c.object_id AND ic.column_id = c.column_id
		LEFT JOIN sys.computed_columns cc ON cc.object_id = c.object_id AND cc.column_id = c.column_id
		LEFT JOIN sys.default_constraints dc ON dc.parent_object_id = c.object_id AND dc.parent_column_id = c.column_id
		WHERE o.type IN ('U', 'V', 'IF', 'TF') AND `+userObjects+`
		ORDER BY c.object_id, c.column_id`,
		func() []any {
			c = Column{}
			return []any{&id, &colID, &c.Name, &c.Type.Name, &c.MaxLength, &c.Precision, &c.Scale, &c.Alias, &c.Nullable,
				&collation, &identity, &seed, &increment, &isComputed, &computed, &persisted, &c.RowGUID, &defName, &defText}
		},
		func() {
			c.Collation = collation.String
			// A view's column of an identity column is one too, with no numbering.
			if identity && seed.Valid {
				c.Identity = &Identity{Seed: seed.String, Increment: increment.String}
			}
			if isComputed {
				c.Computed = &Computed{Definition: computed.String, Persisted: persisted}
			}
			if defName.Valid {
				c.Default = &Default{Name: defName.String, Definition: defText.String}
			}
			c.Comment = r.comment(classObject, id, colID)
			o := r.objects[id]
			o.columns[colID] = c.Name
			if o.table != nil {
				o.table.Columns = append(o.table.Columns, c)
			} else {
				o.routine.Columns = append(o.routine.Columns, c)
			}
		})
}

func (r *reader) checks() {
	var id, parent int64
	var c Check
	r.query("check constraints", `SELECT k.object_id, k.parent_object_id, k.name, k.definition, k.is_disabled, k.is_not_trusted
		FROM sys.check_constraints k JOIN sys.objects o ON o.object_id = k.parent_object_id WHERE `+userObjects,
		func() []any { c = Check{}; return []any{&id, &parent, &c.Name, &c.Definition, &c.Disabled, &c.NotTrusted} },
		func() {
			c.Comment = r.comment(classObject, id, 0)
			t := r.objects[parent].table
			t.Checks = append(t.Checks, c)
		})
}

func (r *reader) indexes() {
	type ix struct {
		table int64
		index *Index
	}
	byID := map[[2]int64]*Index{}
	var order []ix
	var id, indexID int64
	var constraint sql.NullInt64
	var filter sql.NullString
	var x Index
	r.query("indexes", `SELECT i.object_id, i.index_id, i.name, i.type_desc, i.is_unique, i.is_primary_key,
			i.is_unique_constraint, i.filter_definition, i.is_disabled, kc.object_id
		FROM sys.indexes i
		JOIN sys.objects o ON o.object_id = i.object_id
		LEFT JOIN sys.key_constraints kc ON kc.parent_object_id = i.object_id AND kc.unique_index_id = i.index_id
		WHERE i.type > 0 AND i.is_hypothetical = 0 AND o.type IN ('U', 'V') AND `+userObjects,
		func() []any {
			x = Index{}
			return []any{&id, &indexID, &x.Name, &x.Type, &x.Unique, &x.PrimaryKey, &x.UniqueConstraint, &filter, &x.Disabled, &constraint}
		},
		func() {
			x.Filter = filter.String
			if constraint.Valid {
				x.Comment = r.comment(classObject, constraint.Int64, 0)
			} else {
				x.Comment = r.comment(classIndex, id, indexID)
			}
			p := &Index{}
			*p = x
			byID[[2]int64{id, indexID}] = p
			order = append(order, ix{id, p})
		})
	var column string
	var descending, included bool
	r.query("index columns", `SELECT ic.object_id, ic.index_id, COL_NAME(ic.object_id, ic.column_id), ic.is_descending_key,
			ic.is_included_column
		FROM sys.index_columns ic
		JOIN sys.indexes i ON i.object_id = ic.object_id AND i.index_id = ic.index_id
		JOIN sys.objects o ON o.object_id = ic.object_id
		WHERE i.is_hypothetical = 0 AND o.type IN ('U', 'V') AND `+userObjects+`
		ORDER BY ic.object_id, ic.index_id, ic.key_ordinal, ic.index_column_id`,
		func() []any { return []any{&id, &indexID, &column, &descending, &included} },
		func() {
			x := byID[[2]int64{id, indexID}]
			if included {
				x.Included = append(x.Included, column)
			} else {
				x.Parts = append(x.Parts, IndexPart{Column: column, Descending: descending})
			}
		})
	for _, i := range order {
		t := r.objects[i.table].table
		t.Indexes = append(t.Indexes, *i.index)
	}
}

func (r *reader) foreignKeys() {
	byID := map[int64]*ForeignKey{}
	var parents []int64
	var ids []int64
	var id, parent int64
	var fk ForeignKey
	r.query("foreign keys", `SELECT fk.object_id, fk.parent_object_id, fk.name, SCHEMA_NAME(ro.schema_id), ro.name,
			fk.update_referential_action_desc, fk.delete_referential_action_desc, fk.is_disabled, fk.is_not_trusted
		FROM sys.foreign_keys fk
		JOIN sys.objects o ON o.object_id = fk.parent_object_id
		JOIN sys.objects ro ON ro.object_id = fk.referenced_object_id
		WHERE `+userObjects,
		func() []any {
			fk = ForeignKey{}
			return []any{&id, &parent, &fk.Name, &fk.RefSchema, &fk.RefTable, &fk.OnUpdate, &fk.OnDelete, &fk.Disabled, &fk.NotTrusted}
		},
		func() {
			fk.Comment = r.comment(classObject, id, 0)
			p := &ForeignKey{}
			*p = fk
			byID[id] = p
			ids = append(ids, id)
			parents = append(parents, parent)
		})
	var column, ref string
	r.query("foreign key columns", `SELECT fkc.constraint_object_id, COL_NAME(fkc.parent_object_id, fkc.parent_column_id),
			COL_NAME(fkc.referenced_object_id, fkc.referenced_column_id)
		FROM sys.foreign_key_columns fkc JOIN sys.objects o ON o.object_id = fkc.parent_object_id
		WHERE `+userObjects+`
		ORDER BY fkc.constraint_object_id, fkc.constraint_column_id`,
		func() []any { return []any{&id, &column, &ref} },
		func() {
			fk := byID[id]
			fk.Columns = append(fk.Columns, column)
			fk.RefCols = append(fk.RefCols, ref)
		})
	for i, id := range ids {
		t := r.objects[parents[i]].table
		t.ForeignKeys = append(t.ForeignKeys, *byID[id])
	}
}

func (r *reader) triggers() {
	var parent int64
	var tr Trigger
	var insert, update, del bool
	r.query("triggers", `SELECT t.parent_id, t.name, t.is_instead_of_trigger, t.is_disabled, COALESCE(m.definition, ''),
			COALESCE(OBJECTPROPERTY(t.object_id, 'ExecIsInsertTrigger'), 0),
			COALESCE(OBJECTPROPERTY(t.object_id, 'ExecIsUpdateTrigger'), 0),
			COALESCE(OBJECTPROPERTY(t.object_id, 'ExecIsDeleteTrigger'), 0)
		FROM sys.triggers t
		JOIN sys.objects o ON o.object_id = t.parent_id
		LEFT JOIN sys.sql_modules m ON m.object_id = t.object_id
		WHERE t.parent_class = 1 AND t.is_ms_shipped = 0 AND `+userObjects,
		func() []any {
			tr = Trigger{}
			return []any{&parent, &tr.Name, &tr.InsteadOf, &tr.Disabled, &tr.Definition, &insert, &update, &del}
		},
		func() {
			for _, e := range []struct {
				on   bool
				name string
			}{{insert, "INSERT"}, {update, "UPDATE"}, {del, "DELETE"}} {
				if e.on {
					tr.Events = append(tr.Events, e.name)
				}
			}
			t := r.objects[parent].table
			t.Triggers = append(t.Triggers, tr)
		})
}

func (r *reader) views() {
	var id int64
	var v View
	var definition sql.NullString
	r.query("views", `SELECT v.object_id, m.definition, v.with_check_option, m.is_schema_bound
		FROM sys.views v JOIN sys.objects o ON o.object_id = v.object_id
		JOIN sys.sql_modules m ON m.object_id = v.object_id WHERE `+userObjects,
		func() []any { v = View{}; return []any{&id, &definition, &v.CheckOption, &v.SchemaBound} },
		func() {
			// An encrypted view's definition is NULL.
			v.Definition = definition.String
			p := &View{}
			*p = v
			r.objects[id].table.ViewDef = p
		})
	// A view reads a table or view of its own database, which the
	// dependency names by id, or of another, which it names by name.
	var db sql.NullString
	var schema, name string
	r.query("view dependencies", `SELECT DISTINCT d.referencing_id, d.referenced_database_name,
			COALESCE(SCHEMA_NAME(ro.schema_id), d.referenced_schema_name, ''), d.referenced_entity_name
		FROM sys.sql_expression_dependencies d
		JOIN sys.objects o ON o.object_id = d.referencing_id
		LEFT JOIN sys.objects ro ON ro.object_id = d.referenced_id
		WHERE o.type = 'V' AND d.referencing_class = 1 AND d.referenced_class = 1 AND `+userObjects+`
			AND (ro.type IN ('U', 'V') OR d.referenced_database_name IS NOT NULL)`,
		func() []any { return []any{&id, &db, &schema, &name} },
		func() {
			read := schema + "." + name
			if db.Valid && db.String != r.db {
				read = db.String + "." + read
			}
			v := r.objects[id].table.ViewDef
			v.Reads = append(v.Reads, read)
		})
}

func (r *reader) parameters() {
	var id int64
	var definition sql.NullString
	var bound bool
	r.query("routine definitions", `SELECT m.object_id, m.definition, m.is_schema_bound
		FROM sys.sql_modules m JOIN sys.objects o ON o.object_id = m.object_id
		WHERE o.type IN ('FN', 'IF', 'TF', 'P') AND `+userObjects,
		func() []any { return []any{&id, &definition, &bound} },
		func() {
			rt := r.objects[id].routine
			rt.Definition, rt.SchemaBound = definition.String, bound
		})
	var paramID int64
	var p Parameter
	r.query("parameters", `SELECT c.object_id, c.parameter_id, c.name, `+typeColumns+`, c.is_output
		FROM sys.parameters c
		JOIN sys.objects o ON o.object_id = c.object_id
		JOIN sys.types ut ON ut.user_type_id = c.user_type_id
		WHERE o.type IN ('FN', 'IF', 'TF', 'P') AND `+userObjects+`
		ORDER BY c.object_id, c.parameter_id`,
		func() []any {
			p = Parameter{}
			return []any{&id, &paramID, &p.Name, &p.Type.Name, &p.MaxLength, &p.Precision, &p.Scale, &p.Alias, &p.Output}
		},
		func() {
			rt := r.objects[id].routine
			if paramID == 0 {
				// A scalar function's result.
				t := p.Type
				rt.Returns = &t
				return
			}
			p.Name = strings.TrimPrefix(p.Name, "@")
			rt.Parameters = append(rt.Parameters, p)
		})
}

// snapshot sorts what was read into its schemas.
func (r *reader) snapshot() []Schema {
	byID := map[int64]*Schema{}
	for id, name := range r.schemaNames {
		byID[id] = &Schema{Name: name, Comment: r.comment(classSchema, id, 0)}
	}
	for _, o := range r.objects {
		s := byID[o.schema]
		if o.table != nil {
			t := o.table
			slices.SortFunc(t.Checks, func(a, b Check) int { return strings.Compare(a.Name, b.Name) })
			slices.SortFunc(t.Indexes, func(a, b Index) int { return strings.Compare(a.Name, b.Name) })
			slices.SortFunc(t.ForeignKeys, func(a, b ForeignKey) int { return strings.Compare(a.Name, b.Name) })
			slices.SortFunc(t.Triggers, func(a, b Trigger) int { return strings.Compare(a.Name, b.Name) })
			if t.ViewDef != nil {
				slices.Sort(t.ViewDef.Reads)
			}
			s.Tables = append(s.Tables, *t)
		} else {
			s.Routines = append(s.Routines, *o.routine)
		}
	}
	var out []Schema
	for _, s := range byID {
		slices.SortFunc(s.Tables, func(a, b Table) int { return strings.Compare(a.Name, b.Name) })
		slices.SortFunc(s.Routines, func(a, b Routine) int { return strings.Compare(a.Name, b.Name) })
		out = append(out, *s)
	}
	slices.SortFunc(out, func(a, b Schema) int { return cmp.Compare(a.Name, b.Name) })
	return out
}
