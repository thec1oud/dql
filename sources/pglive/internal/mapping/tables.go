package mapping

import (
	"fmt"
	"slices"
	"strings"

	pgextv1 "github.com/thec1oud/dql/proto/dql/ext/postgres/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/internal/pgsql"
	"github.com/thec1oud/dql/sources/pglive/internal/introspect"
)

// placed is a table with the schema it belongs to.
type placed struct {
	schema string
	table  *irv1.Table
}

// tables maps every relation that is an entity: tables, partitioned tables,
// views and materialized views. A partition whose parent was read is not an
// entity; it travels on its parent as a Partition extension (§6.2).
func (m *mapper) tables(s *introspect.Snapshot) []placed {
	read := map[objectKey]bool{}
	for _, r := range s.Relations {
		read[objectKey{r.Schema, r.Name}] = true
	}
	children := map[objectKey][]introspect.Relation{}
	var top []introspect.Relation
	for _, r := range s.Relations {
		if p := r.PartitionOf; p != nil && read[objectKey{p.Schema, p.Name}] {
			children[objectKey{p.Schema, p.Name}] = append(children[objectKey{p.Schema, p.Name}], r)
			continue
		}
		top = append(top, r)
	}
	t := tableMapper{mapper: m, children: children, read: read}
	var out []placed
	for _, r := range top {
		out = append(out, placed{r.Schema, t.table(r)})
	}
	return out
}

// tableMapper maps relations with what they need from the rest of the
// Snapshot.
type tableMapper struct {
	*mapper
	children map[objectKey][]introspect.Relation
	read     map[objectKey]bool
}

var tableKinds = map[string]irv1.TableKind{
	"r": irv1.TableKind_RELATION_KIND_TABLE,
	"p": irv1.TableKind_RELATION_KIND_TABLE,
	"v": irv1.TableKind_RELATION_KIND_VIEW,
	"m": irv1.TableKind_RELATION_KIND_MATERIALIZED_VIEW,
}

// commentTargets spells each relation kind in COMMENT ON.
var commentTargets = map[string]string{"r": "TABLE", "p": "TABLE", "v": "VIEW", "m": "MATERIALIZED VIEW"}

func (m *tableMapper) table(r introspect.Relation) *irv1.Table {
	name := r.Schema + "." + r.Name
	t := &irv1.Table{Name: ident(r.Name), Kind: tableKinds[r.Kind]}
	t.Annotations = m.annotations(m.qn(r.Schema, r.Name, ""), "COMMENT ON "+commentTargets[r.Kind]+" "+name, r.Comment)
	e := &exts{m: m.mapper}
	e.comment(r.Comment)
	e.owner(r.Owner)
	e.member(r.Extension)

	checked := map[string]*irv1.Type{}
	for i, c := range r.Columns {
		col := m.column(r, c, i)
		t.Columns = append(t.Columns, col)
		checked[c.Name] = m.typeOf("column "+name+"."+c.Name, m.baseOf(c.Type))
	}

	indexes := map[string]introspect.Index{}
	for _, x := range r.Indexes {
		indexes[x.Name] = x
	}
	m.constraints(t, r, checked, indexes)
	m.indexes(t, r, indexes, e)
	for _, d := range r.DependsOn {
		t.DependsOn = append(t.DependsOn, m.qn(d.Schema, d.Name, ""))
	}
	m.relationExtensions(r, e)
	t.Extensions = e.list()
	return t
}

// constraints maps r's check and exclusion constraints, its primary key and
// unique constraints, with the indexes behind them, and its foreign keys.
// checked holds each column's type with domains resolved, which check
// constraints are recognized against.
func (m *tableMapper) constraints(t *irv1.Table, r introspect.Relation, checked map[string]*irv1.Type, indexes map[string]introspect.Index) {
	name := r.Schema + "." + r.Name
	for _, c := range r.Constraints {
		target := m.qn(r.Schema, r.Name, c.Name)
		loc := "COMMENT ON CONSTRAINT " + c.Name + " ON " + name
		ce := &exts{m: m.mapper}
		ce.comment(c.Comment)
		switch c.Kind {
		case "c":
			con := m.check(name, c.Definition, checked)
			con.Name, con.Columns = c.Name, c.Columns
			con.Annotations = m.annotations(target, loc, c.Comment)
			ce.options(&pgextv1.ConstraintOptions{NotValid: c.NotValid, NoInherit: c.NoInherit})
			con.Extensions = ce.list()
			t.Constraints = append(t.Constraints, con)
		case "x":
			con := &irv1.Constraint{
				Name: c.Name, Kind: irv1.ConstraintKind_CONSTRAINT_KIND_EXCLUSION, Columns: c.Columns,
				Form: &irv1.Constraint_Opaque{Opaque: &irv1.NativeText{Native: c.Definition}},
			}
			con.Annotations = m.annotations(target, loc, c.Comment)
			ce.index(indexes, c.Index)
			ce.options(&pgextv1.ConstraintOptions{Deferrable: c.Deferrable, InitiallyDeferred: c.InitiallyDeferred})
			con.Extensions = ce.list()
			t.Constraints = append(t.Constraints, con)
		case "p", "u":
			u := &irv1.Uniqueness{Name: c.Name, Kind: irv1.UniquenessKind_UNIQUENESS_CONSTRAINT, Columns: c.Columns,
				NullsDistinct: !c.NullsNotDistinct}
			if c.Kind == "p" {
				u.Kind = irv1.UniquenessKind_UNIQUENESS_PRIMARY_KEY
			}
			m.ignored(target, loc, c.Comment, "pg-live reads no annotations on primary keys and unique constraints")
			ce.index(indexes, c.Index)
			ce.options(&pgextv1.ConstraintOptions{Deferrable: c.Deferrable, InitiallyDeferred: c.InitiallyDeferred})
			u.Extensions = ce.list()
			t.Uniqueness = append(t.Uniqueness, u)
		case "f":
			fk := m.foreignKey(c)
			fk.Annotations = m.annotations(target, loc, c.Comment)
			ce.options(&pgextv1.ConstraintOptions{InitiallyDeferred: c.InitiallyDeferred, NotValid: c.NotValid,
				OnDeleteColumns: c.References.OnDeleteColumns})
			fk.Extensions = ce.list()
			t.ForeignKeys = append(t.ForeignKeys, fk)
		}
		// Not-null constraints (n) are column nullability, and constraint
		// triggers (t) are triggers, which carry their definition.
	}
}

// indexes maps the indexes that back no constraint: a unique one is a
// uniqueness fact (§11.2), any other an extension (§6.2).
func (m *tableMapper) indexes(t *irv1.Table, r introspect.Relation, indexes map[string]introspect.Index, e *exts) {
	for _, x := range r.Indexes {
		if x.Constraint != "" {
			continue // carried on its constraint
		}
		m.ignored(m.qn(r.Schema, r.Name, x.Name), "COMMENT ON INDEX "+r.Schema+"."+x.Name, x.Comment,
			"pg-live reads no annotations on indexes")
		if !x.Unique {
			e.index(indexes, x.Name)
			continue
		}
		u := uniqueIndex(x)
		ue := &exts{m: m.mapper}
		ue.index(indexes, x.Name)
		u.Extensions = ue.list()
		t.Uniqueness = append(t.Uniqueness, u)
	}
	slices.SortFunc(t.Uniqueness, func(a, b *irv1.Uniqueness) int { return strings.Compare(a.Name, b.Name) })
}

// relationExtensions carries what the IR does not model about r (§6.2).
func (m *tableMapper) relationExtensions(r introspect.Relation, e *exts) {
	name := r.Schema + "." + r.Name
	if r.Definition != "" {
		e.add(&pgextv1.ViewDefinition{Definition: r.Definition})
	}
	if r.Tablespace != "" {
		e.add(&pgextv1.Tablespace{Name: r.Tablespace})
	}
	if len(r.Options) > 0 {
		e.add(&pgextv1.StorageParameters{Parameters: r.Options})
	}
	if r.RowSecurity || r.ForceRowSecurity {
		e.add(&pgextv1.RowSecurity{Enabled: r.RowSecurity, Forced: r.ForceRowSecurity})
	}
	if r.Persistence == "u" {
		e.add(&pgextv1.Unlogged{})
	}
	if r.PartitionKey != "" {
		e.add(&pgextv1.PartitionKey{Definition: r.PartitionKey})
	}
	if p := r.PartitionOf; p != nil && !m.read[objectKey{p.Schema, p.Name}] {
		// A partition whose parent was not read stays a table of its own.
		e.add(&pgextv1.PartitionOf{Parent: m.qn(p.Schema, p.Name, ""), Bound: r.PartitionBound})
	}
	for _, p := range r.Inherits {
		e.add(&pgextv1.PartitionOf{Parent: m.qn(p.Schema, p.Name, "")})
	}
	for _, child := range m.children[objectKey{r.Schema, r.Name}] {
		e.add(&pgextv1.Partition{Namespace: child.Schema, Bound: child.PartitionBound, Table: m.table(child)})
	}
	e.grants(r.Grants)
	for _, p := range r.Policies {
		m.ignored(m.qn(r.Schema, r.Name, p.Name), "COMMENT ON POLICY "+p.Name+" ON "+name, p.Comment, "pg-live reads no annotations on policies")
		e.add(&pgextv1.Policy{Name: p.Name, Command: policyCommands[p.Command], Permissive: p.Permissive,
			Roles: p.Roles, Using: p.Using, WithCheck: p.WithCheck, Comment: p.Comment})
	}
	for _, tr := range r.Triggers {
		m.ignored(m.qn(r.Schema, r.Name, tr.Name), "COMMENT ON TRIGGER "+tr.Name+" ON "+name, tr.Comment, "pg-live reads no annotations on triggers")
		e.add(&pgextv1.Trigger{Name: tr.Name, Timing: tr.Timing, Events: triggerEvents(tr.Events), Level: tr.Level,
			Function: m.qn(tr.Function.Schema, tr.Function.Name, ""), Enabled: triggerEnabled[tr.Enabled],
			Definition: tr.Definition, Comment: tr.Comment})
	}
}

// column maps the i-th column of r. A table column is non-null when the
// database declares it NOT NULL; nothing else proves it, not even a domain's
// NOT NULL, which Postgres does not enforce on every path into a column. A
// view column is always unproven (§6.4).
func (m *tableMapper) column(r introspect.Relation, c introspect.Column, i int) *irv1.Column {
	name := r.Schema + "." + r.Name + "." + c.Name
	col := &irv1.Column{
		Name:              ident(c.Name),
		Ordinal:           uint32(i + 1),
		Type:              m.typeOf("column "+name, c.Type),
		Nullable:          !c.NotNull,
		NullabilitySource: irv1.NullabilitySource_NULLABILITY_SOURCE_UNPROVEN,
		Collation:         m.collation(c.Collation, c.Type),
	}
	if c.NotNull {
		col.NullabilitySource = irv1.NullabilitySource_NULLABILITY_SOURCE_DATABASE
	}
	switch {
	case c.Identity != "":
		col.Default = &irv1.Default{Class: identities[c.Identity], Sequence: m.qn(r.Schema, c.IdentitySequence, "")}
	case c.Default != "":
		col.Default = m.defaultOf("column "+name, c.Default)
	}
	if c.Generated != "" {
		col.Generation = &irv1.Generation{Kind: generations[c.Generated], Native: c.Generation}
	}
	col.Annotations = m.annotations(m.qn(r.Schema, r.Name, c.Name), "COMMENT ON COLUMN "+name, c.Comment)
	e := &exts{m: m.mapper}
	e.comment(c.Comment)
	e.grants(c.Grants)
	col.Extensions = e.list()
	return col
}

var identities = map[string]irv1.DefaultClass{
	"a": irv1.DefaultClass_DEFAULT_IDENTITY_ALWAYS,
	"d": irv1.DefaultClass_DEFAULT_IDENTITY_BY_DEFAULT,
}

var generations = map[string]irv1.GenerationKind{
	"s": irv1.GenerationKind_GENERATION_STORED,
	"v": irv1.GenerationKind_GENERATION_VIRTUAL,
}

// defaultOf classifies a default expression (§11.3). The sequence of a
// nextval default is in this catalog.
func (m *mapper) defaultOf(what, text string) *irv1.Default {
	d, err := pgsql.ClassifyDefault(text)
	if err != nil {
		m.fail(fmt.Errorf("mapping: default of %s: %w", what, err))
		return nil
	}
	if d.Sequence != nil {
		d.Sequence.Catalog = m.opts.Catalog
	}
	return d
}

// check recognizes a check constraint as a portable kind, or keeps it opaque
// (§10.1, §10.4). types holds each column's type with domains resolved.
func (m *mapper) check(table, text string, types map[string]*irv1.Type) *irv1.Constraint {
	c, err := pgsql.RecognizeCheck(text, types)
	if err != nil {
		m.fail(fmt.Errorf("mapping: check constraint of %s: %w", table, err))
		return &irv1.Constraint{}
	}
	return c
}

func (m *mapper) foreignKey(c introspect.Constraint) *irv1.ForeignKey {
	ref := c.References
	return &irv1.ForeignKey{
		Name:       ident(c.Name),
		Columns:    c.Columns,
		References: &irv1.ColumnSet{Table: m.qn(ref.Schema, ref.Table, ""), Columns: ref.Columns},
		Match:      matches[ref.Match],
		OnUpdate:   actions[ref.OnUpdate],
		OnDelete:   actions[ref.OnDelete],
		Deferrable: c.Deferrable,
	}
}

var matches = map[string]irv1.ForeignKeyMatch{
	"s": irv1.ForeignKeyMatch_FOREIGN_KEY_MATCH_SIMPLE,
	"f": irv1.ForeignKeyMatch_FOREIGN_KEY_MATCH_FULL,
	"p": irv1.ForeignKeyMatch_FOREIGN_KEY_MATCH_PARTIAL,
}

var actions = map[string]irv1.ForeignKeyAction{
	"a": irv1.ForeignKeyAction_FOREIGN_KEY_ACTION_NO_ACTION,
	"r": irv1.ForeignKeyAction_FOREIGN_KEY_ACTION_RESTRICT,
	"c": irv1.ForeignKeyAction_FOREIGN_KEY_ACTION_CASCADE,
	"n": irv1.ForeignKeyAction_FOREIGN_KEY_ACTION_SET_NULL,
	"d": irv1.ForeignKeyAction_FOREIGN_KEY_ACTION_SET_DEFAULT,
}

// uniqueIndex is the uniqueness fact of a unique index that backs no
// constraint, partial and expression indexes included (§11.2). INCLUDE
// columns are not part of the key.
func uniqueIndex(x introspect.Index) *irv1.Uniqueness {
	u := &irv1.Uniqueness{Name: x.Name, Kind: irv1.UniquenessKind_UNIQUENESS_INDEX, NullsDistinct: !x.NullsNotDistinct}
	for i, k := range x.Keys {
		switch {
		case k.Included:
		case k.Expression != "":
			u.Expressions = append(u.Expressions, &irv1.IndexExpression{Position: uint32(i), Native: k.Expression})
		default:
			u.Columns = append(u.Columns, k.Column)
		}
	}
	if x.Predicate != "" {
		u.Predicate = &irv1.NativeText{Native: x.Predicate}
	}
	return u
}

// index carries the index named name, with its definition and storage.
func (e *exts) index(indexes map[string]introspect.Index, name string) {
	if x, ok := indexes[name]; ok {
		e.add(&pgextv1.Index{Name: x.Name, Method: x.Method, Definition: x.Definition,
			StorageParameters: x.Options, Tablespace: x.Tablespace, Comment: x.Comment})
	}
}

// options carries o unless every option has its default.
func (e *exts) options(o *pgextv1.ConstraintOptions) {
	if o.Deferrable || o.InitiallyDeferred || o.NotValid || o.NoInherit || len(o.OnDeleteColumns) > 0 {
		e.add(o)
	}
}

var policyCommands = map[string]string{"r": "SELECT", "a": "INSERT", "w": "UPDATE", "d": "DELETE", "*": "ALL"}

var triggerEnabled = map[string]string{"O": "ORIGIN", "A": "ALWAYS", "R": "REPLICA", "D": "DISABLED"}

// triggerEvents orders events as the extension documents them.
func triggerEvents(events []string) []string {
	order := []string{"INSERT", "UPDATE", "DELETE", "TRUNCATE"}
	out := slices.Clone(events)
	slices.SortFunc(out, func(a, b string) int { return slices.Index(order, a) - slices.Index(order, b) })
	return out
}
