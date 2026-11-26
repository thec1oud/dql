// Package mapping turns an introspect.Snapshot into the catalog layer of the
// IR: sqlite-live's output (design record §2.2, §6). A database file is a
// catalog with one implicit namespace, its main schema, stored as "" (§5.1).
// Every first-class object of §6.1 becomes its IR message, and everything
// else the Snapshot holds travels as a typed dql.ext.sqlite.v1 extension
// (§6.2), so nothing read is lost (§6.3).
//
// SQLite's pragmas give columns, keys and indexes, but not constraint
// names, checks, collations, generation expressions or comments; those come
// from the CREATE statements sqlite_schema stores, read with the SQLite
// grammar. A constraint without a name gets the one Postgres would give it,
// so the names a generator derives from it are stable across dialects.
//
// As pg-live's, the mapping emits the facts the database states and nothing
// the core derives: identifiers carry stored names only, annotations are the
// tokens of comments as read, and uniqueness facts do not say whether they
// identify rows.
//
// Output is deterministic (§3.2): columns and index parts keep the
// database's order, every other list is sorted by name.
package mapping

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	sqliteextv1 "github.com/thec1oud/dql/proto/dql/ext/sqlite/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/internal/comment"
	"github.com/thec1oud/dql/sources/internal/sqlitesql"
	"github.com/thec1oud/dql/sources/sqlitelive/internal/introspect"
)

// The source plugin's name and version, as provenance and annotation origins
// record them (§3.1, §7.4).
const (
	Source  = "sqlite-live"
	Version = "1.0.0"
)

// TypeURLPrefix prefixes the type URL of every extension (§6.2).
const TypeURLPrefix = "type.dql.dev/"

// Diagnostic codes. They are stable: tools and tests match on them.
const (
	// An annotation value whose parenthesis is never closed.
	CodeAnnotationUnclosed = "sqlitelive-annotation-unclosed"
	// Annotations in a comment sqlite-live does not read, such as a unique
	// constraint's.
	CodeAnnotationIgnored = "sqlitelive-annotation-ignored"
)

// Options carries what the mapping needs from config.
type Options struct {
	// Catalog names the catalog.
	Catalog string
}

// Map builds the catalog of s. Diagnostics report annotations that cannot be
// read; an error means the Snapshot holds SQL that does not parse as the
// statement sqlite_schema stores.
func Map(s *introspect.Snapshot, opts Options) (*irv1.Catalog, []*irv1.Diagnostic, error) {
	m := &mapper{opts: opts, tables: map[string]introspect.Table{}}
	for _, t := range s.Tables {
		m.tables[strings.ToLower(t.Name)] = t
	}
	c := m.catalog(s)
	if m.err == nil {
		c.Provenance, m.err = provenance(c)
	}
	if m.err != nil {
		return nil, nil, m.err
	}
	slices.SortFunc(m.diags, func(a, b *irv1.Diagnostic) int {
		return strings.Compare(a.Code+"\x00"+a.Message, b.Code+"\x00"+b.Message)
	})
	return c, m.diags, nil
}

// mapper holds the state of one Map call. Its first error sticks.
type mapper struct {
	opts   Options
	tables map[string]introspect.Table
	diags  []*irv1.Diagnostic
	err    error
}

func (m *mapper) fail(err error) {
	if m.err == nil {
		m.err = err
	}
}

// qn names an object of this catalog's one namespace; member may be empty.
func (m *mapper) qn(object, member string) *irv1.QualifiedName {
	return &irv1.QualifiedName{Catalog: m.opts.Catalog, Object: object, Member: member}
}

func ident(stored string) *irv1.Identifier { return &irv1.Identifier{Stored: stored} }

func (m *mapper) catalog(s *introspect.Snapshot) *irv1.Catalog {
	ns := &irv1.Namespace{Name: ident("")}
	for _, t := range s.Tables {
		if t.View {
			ns.Tables = append(ns.Tables, m.view(t))
		} else {
			ns.Tables = append(ns.Tables, m.table(t))
		}
	}
	e := &exts{m: m}
	e.add(&sqliteextv1.Database{Version: s.Database.Version, Encoding: s.Database.Encoding})
	return &irv1.Catalog{Name: ident(m.opts.Catalog), Namespaces: []*irv1.Namespace{ns}, Extensions: e.list()}
}

// view maps a view. Its columns are always unproven (§6.4). SQLite
// declares a view column with its source column's declared type when it
// reads one directly, and with none otherwise; so when every relation the
// view reads is a STRICT table, a column with a declared type holds that
// type's class, and any other column is weak.
func (m *mapper) view(t introspect.Table) *irv1.Table {
	out := &irv1.Table{Name: ident(t.Name), Kind: irv1.TableKind_RELATION_KIND_VIEW}
	reads, err := sqlitesql.ViewReads(t.SQL)
	if err != nil {
		m.fail(fmt.Errorf("mapping: view %s: %w", t.Name, err))
	}
	strict := true
	for _, r := range reads {
		read, ok := m.tables[strings.ToLower(r)]
		strict = strict && ok && read.Strict
		out.DependsOn = append(out.DependsOn, m.qn(m.stored(r), ""))
	}
	for i, c := range t.Columns {
		out.Columns = append(out.Columns, &irv1.Column{
			Name:              ident(c.Name),
			Ordinal:           uint32(i + 1),
			Type:              sqlitesql.ParseType(c.Type, strict && c.Type != "", false),
			Nullable:          true,
			NullabilitySource: irv1.NullabilitySource_NULLABILITY_SOURCE_UNPROVEN,
		})
	}
	e := &exts{m: m}
	e.add(&sqliteextv1.ViewDefinition{Sql: t.SQL})
	out.Extensions = e.list()
	return out
}

// stored returns the name a table is stored under, which a statement may
// spell in another case.
func (m *mapper) stored(name string) string {
	if t, ok := m.tables[strings.ToLower(name)]; ok {
		return t.Name
	}
	return name
}

// rowidAlias returns the column that aliases t's rowid, or "". SQLite
// makes an index for every primary key but that of a rowid alias, the one
// column declared INTEGER PRIMARY KEY (and not DESC) of a table with a
// rowid, so a rowid table whose key has no index has that column as key.
func rowidAlias(t introspect.Table) string {
	if t.WithoutRowid || slices.ContainsFunc(t.Indexes, func(x introspect.Index) bool { return x.Origin == "pk" }) {
		return ""
	}
	for _, c := range t.Columns {
		if c.PrimaryKey > 0 {
			return c.Name
		}
	}
	return ""
}

// table maps a table.
func (m *mapper) table(t introspect.Table) *irv1.Table {
	out := &irv1.Table{Name: ident(t.Name), Kind: irv1.TableKind_RELATION_KIND_TABLE}
	def, err := sqlitesql.ParseTable(t.SQL)
	if err != nil {
		m.fail(fmt.Errorf("mapping: table %s: %w", t.Name, err))
		def = &sqlitesql.Table{}
	}
	parsed := map[string]sqlitesql.Column{}
	for _, c := range def.Columns {
		parsed[strings.ToLower(c.Name)] = c
	}
	out.Annotations = m.annotations(m.qn(t.Name, ""), "the comment of table "+t.Name, def.Comment)
	e := &exts{m: m}
	e.comment(def.Comment)
	e.add(&sqliteextv1.TableDefinition{Sql: t.SQL, Strict: t.Strict, WithoutRowid: t.WithoutRowid})

	alias := rowidAlias(t)
	checked := map[string]sqlitesql.CheckColumn{}
	for i, c := range t.Columns {
		col := m.column(t, c, i, parsed[strings.ToLower(c.Name)], c.Name == alias)
		out.Columns = append(out.Columns, col)
		checked[c.Name] = sqlitesql.CheckColumn{Type: col.Type, Collation: col.Collation}
	}

	names := &namer{table: t.Name, used: map[string]bool{}}
	for _, c := range def.Constraints {
		if c.Name != "" {
			names.used[c.Name] = true
		}
	}
	for _, c := range def.Constraints {
		if c.Kind != sqlitesql.Check {
			continue
		}
		con := sqlitesql.RecognizeCheck(c, checked)
		con.Name = c.Name
		if con.Name == "" {
			con.Name = names.name(con.Columns, "check")
		}
		con.Annotations = m.annotations(m.qn(t.Name, con.Name), "the comment of constraint "+con.Name, c.Comment)
		ce := &exts{m: m}
		ce.comment(c.Comment)
		ce.add(&sqliteextv1.Check{Expression: c.Expr, Named: c.Name != ""})
		con.Extensions = ce.list()
		out.Constraints = append(out.Constraints, con)
	}
	slices.SortFunc(out.Constraints, func(a, b *irv1.Constraint) int { return strings.Compare(a.Name, b.Name) })

	m.uniqueness(out, t, def, names, e)
	slices.SortFunc(out.Uniqueness, func(a, b *irv1.Uniqueness) int { return strings.Compare(a.Name, b.Name) })

	unused := constraintsOf(def, sqlitesql.ForeignKey)
	for _, fk := range t.ForeignKeys {
		out.ForeignKeys = append(out.ForeignKeys, m.foreignKey(t, fk, unused, names))
	}
	slices.SortFunc(out.ForeignKeys, func(a, b *irv1.ForeignKey) int { return strings.Compare(a.Name.Stored, b.Name.Stored) })

	for _, tr := range t.Triggers {
		e.add(&sqliteextv1.Trigger{Name: tr.Name, Sql: tr.SQL})
	}
	out.Extensions = e.list()
	return out
}

// column maps the i-th column of t, with its definition p. A column is
// non-null when it is declared NOT NULL, aliases the rowid, or is a key
// column of a WITHOUT ROWID table, which SQLite never lets hold NULL.
func (m *mapper) column(t introspect.Table, c introspect.Column, i int, p sqlitesql.Column, alias bool) *irv1.Column {
	name := t.Name + "." + c.Name
	typ := sqlitesql.ParseType(c.Type, t.Strict, alias)
	col := &irv1.Column{
		Name:              ident(c.Name),
		Ordinal:           uint32(i + 1),
		Type:              typ,
		Nullable:          !c.NotNull && !alias && !(t.WithoutRowid && c.PrimaryKey > 0),
		NullabilitySource: irv1.NullabilitySource_NULLABILITY_SOURCE_UNPROVEN,
	}
	if !col.Nullable {
		col.NullabilitySource = irv1.NullabilitySource_NULLABILITY_SOURCE_DATABASE
	}
	if p.Collation != "" || isText(typ) {
		col.Collation = sqlitesql.Collation(p.Collation)
	}
	switch c.Hidden {
	case 2:
		col.Generation = &irv1.Generation{Kind: irv1.GenerationKind_GENERATION_VIRTUAL, Native: p.Generation}
	case 3:
		col.Generation = &irv1.Generation{Kind: irv1.GenerationKind_GENERATION_STORED, Native: p.Generation}
	default:
		col.Default = sqlitesql.ClassifyDefault(c.Default, typ, alias)
	}
	col.Annotations = m.annotations(m.qn(t.Name, c.Name), "the comment of column "+name, p.Comment)
	e := &exts{m: m}
	e.comment(p.Comment)
	col.Extensions = e.list()
	return col
}

// isText reports whether a column holds text, weakly or not, so that it
// compares by a collation.
func isText(t *irv1.Type) bool {
	if w := t.GetWeak(); w != nil {
		t = w.GetAffinity()
	}
	return t.GetText() != nil
}

// uniqueness maps the primary key and every unique index (§11.2), and adds
// each other index to the table's extensions (§6.2). The primary key of a
// rowid alias has no index; any other key, and each UNIQUE constraint, has
// one SQLite made for it, which travels on its fact. SQLite lets a unique
// key hold several NULLs.
func (m *mapper) uniqueness(out *irv1.Table, t introspect.Table, def *sqlitesql.Table, names *namer, e *exts) {
	var pk []string
	for pos := 1; ; pos++ {
		i := slices.IndexFunc(t.Columns, func(c introspect.Column) bool { return c.PrimaryKey == pos })
		if i < 0 {
			break
		}
		pk = append(pk, t.Columns[i].Name)
	}
	unique := constraintsOf(def, sqlitesql.Unique)
	if pk != nil {
		u := &irv1.Uniqueness{Kind: irv1.UniquenessKind_UNIQUENESS_PRIMARY_KEY, Columns: pk, NullsDistinct: true}
		c := constraintsOf(def, sqlitesql.PrimaryKey).take(pk)
		if c != nil {
			u.Name = c.Name
		}
		if u.Name == "" {
			u.Name = names.unique(t.Name + "_pkey")
		}
		m.ignored(t.Name, u.Name, c)
		if i := slices.IndexFunc(t.Indexes, func(x introspect.Index) bool { return x.Origin == "pk" }); i >= 0 {
			ue := &exts{m: m}
			ue.add(m.index(t, t.Indexes[i], nil))
			u.Extensions = ue.list()
		}
		out.Uniqueness = append(out.Uniqueness, u)
	}
	for _, x := range t.Indexes {
		if x.Origin == "pk" {
			continue
		}
		var parsed *sqlitesql.Index
		if x.SQL != "" {
			var err error
			if parsed, err = sqlitesql.ParseIndex(x.SQL); err != nil {
				m.fail(fmt.Errorf("mapping: index %s of %s: %w", x.Name, t.Name, err))
				continue
			}
		}
		ix := m.index(t, x, parsed)
		if !x.Unique {
			e.add(ix)
			continue
		}
		u := &irv1.Uniqueness{Name: x.Name, Kind: irv1.UniquenessKind_UNIQUENESS_INDEX, NullsDistinct: true}
		for i, p := range ix.Parts {
			if p.Expression != "" {
				u.Expressions = append(u.Expressions, &irv1.IndexExpression{Position: uint32(i), Native: p.Expression})
			} else {
				u.Columns = append(u.Columns, p.Column)
			}
		}
		if parsed != nil && parsed.Where != "" {
			u.Predicate = &irv1.NativeText{Native: parsed.Where}
			u.PredicateNullColumns = parsed.NullColumns
			u.PredicateNotTrueColumns = parsed.NotTrueColumns
		}
		if x.Origin == "u" {
			u.Kind = irv1.UniquenessKind_UNIQUENESS_CONSTRAINT
			u.Name = ""
			c := unique.take(u.Columns)
			if c != nil {
				u.Name = c.Name
			}
			if u.Name == "" {
				u.Name = names.name(u.Columns, "key")
			}
			m.ignored(t.Name, u.Name, c)
		}
		ue := &exts{m: m}
		ue.add(ix)
		u.Extensions = ue.list()
		out.Uniqueness = append(out.Uniqueness, u)
	}
}

// index returns an index's extension. A key element that is no column
// takes its expression from the CREATE INDEX statement, parsed.
func (m *mapper) index(t introspect.Table, x introspect.Index, parsed *sqlitesql.Index) *sqliteextv1.Index {
	ix := &sqliteextv1.Index{Name: x.Name, Unique: x.Unique, Origin: x.Origin, Sql: x.SQL}
	if parsed != nil {
		ix.Where = parsed.Where
	}
	for i, p := range x.Parts {
		part := &sqliteextv1.IndexPart{Column: p.Column, Descending: p.Descending, Collation: p.Collation}
		if p.Column == "" && parsed != nil && i < len(parsed.Expressions) {
			part.Expression = parsed.Expressions[i]
		}
		ix.Parts = append(ix.Parts, part)
	}
	return ix
}

var actions = map[string]irv1.ForeignKeyAction{
	"NO ACTION":   irv1.ForeignKeyAction_FOREIGN_KEY_ACTION_NO_ACTION,
	"RESTRICT":    irv1.ForeignKeyAction_FOREIGN_KEY_ACTION_RESTRICT,
	"CASCADE":     irv1.ForeignKeyAction_FOREIGN_KEY_ACTION_CASCADE,
	"SET NULL":    irv1.ForeignKeyAction_FOREIGN_KEY_ACTION_SET_NULL,
	"SET DEFAULT": irv1.ForeignKeyAction_FOREIGN_KEY_ACTION_SET_DEFAULT,
}

// foreignKey maps a foreign key, named by its definition. SQLite checks a
// key as MATCH SIMPLE, whatever MATCH it declares: a key with a NULL column
// is not checked (§12.3). A key that names no columns references its
// target's primary key.
func (m *mapper) foreignKey(t introspect.Table, fk introspect.ForeignKey, unused *constraints, names *namer) *irv1.ForeignKey {
	refCols := fk.RefColumns
	if len(refCols) == 0 {
		target := m.tables[strings.ToLower(fk.RefTable)]
		for pos := 1; ; pos++ {
			i := slices.IndexFunc(target.Columns, func(c introspect.Column) bool { return c.PrimaryKey == pos })
			if i < 0 {
				break
			}
			refCols = append(refCols, target.Columns[i].Name)
		}
	}
	out := &irv1.ForeignKey{
		Columns:    fk.Columns,
		References: &irv1.ColumnSet{Table: m.qn(m.stored(fk.RefTable), ""), Columns: refCols},
		Match:      irv1.ForeignKeyMatch_FOREIGN_KEY_MATCH_SIMPLE,
		OnUpdate:   actions[fk.OnUpdate],
		OnDelete:   actions[fk.OnDelete],
	}
	name, text := "", ""
	if c := unused.take(fk.Columns); c != nil {
		name, text = c.Name, c.Comment
	}
	if name == "" {
		name = names.name(fk.Columns, "fkey")
	}
	out.Name = ident(name)
	out.Annotations = m.annotations(m.qn(t.Name, name), "the comment of foreign key "+name, text)
	e := &exts{m: m}
	e.comment(text)
	out.Extensions = e.list()
	return out
}

// constraints are a table's definitions of one kind not yet matched to what
// the pragmas report.
type constraints struct{ list []sqlitesql.Constraint }

func constraintsOf(def *sqlitesql.Table, kind string) *constraints {
	c := &constraints{}
	for _, d := range def.Constraints {
		if d.Kind == kind {
			c.list = append(c.list, d)
		}
	}
	return c
}

// take returns, and removes, the first definition on exactly columns, or
// nil.
func (c *constraints) take(columns []string) *sqlitesql.Constraint {
	for i, d := range c.list {
		if len(d.Expressions) == 0 && slices.EqualFunc(d.Columns, columns, strings.EqualFold) {
			c.list = slices.Delete(c.list, i, i+1)
			return &d
		}
	}
	return nil
}

// namer gives constraints without a name the one Postgres would: the
// table, the columns and a suffix, numbered when that name is taken.
type namer struct {
	table string
	used  map[string]bool
}

func (n *namer) name(columns []string, suffix string) string {
	parts := append([]string{n.table}, columns...)
	if len(columns) > 0 && suffix == "check" {
		parts = []string{n.table, columns[0]}
	}
	return n.unique(strings.Join(append(parts, suffix), "_"))
}

func (n *namer) unique(base string) string {
	name := base
	for i := 1; n.used[name]; i++ {
		name = base + strconv.Itoa(i)
	}
	n.used[name] = true
	return name
}

// annotations reads the annotations of an object's comment (§7.1, §7.5),
// reporting each token whose value is never closed.
func (m *mapper) annotations(target *irv1.QualifiedName, location, text string) []*irv1.Annotation {
	out, unclosed := comment.Annotations(text, Source, location)
	for _, tok := range unclosed {
		m.diag(irv1.Severity_SEVERITY_ERROR, CodeAnnotationUnclosed, target, fmt.Sprintf(
			"%s: the value of @%s has no closing parenthesis, so it is not read; close it", location, tok.Key))
	}
	return out
}

// ignored reports the annotation tokens in the comment of a key's
// definition c, if any, as a key carries no annotations (§7.5).
func (m *mapper) ignored(table, name string, c *sqlitesql.Constraint) {
	if c == nil {
		return
	}
	var keys []string
	for _, tok := range comment.ParseComment(c.Comment) {
		keys = append(keys, "@"+tok.Key)
	}
	if len(keys) > 0 {
		location := "the comment of key " + name + " of " + table
		m.diag(irv1.Severity_SEVERITY_WARNING, CodeAnnotationIgnored, m.qn(table, name), fmt.Sprintf(
			"%s: sqlite-live reads no annotations on keys, so %s is ignored; remove the annotation tokens from the comment",
			location, strings.Join(keys, " ")))
	}
}

func (m *mapper) diag(sev irv1.Severity, code string, target *irv1.QualifiedName, msg string) {
	m.diags = append(m.diags, &irv1.Diagnostic{Severity: sev, Code: code, Target: target, Message: msg})
}

// provenance names the source and fingerprints the catalog it read (§3.1,
// §3.2 rule 3).
func provenance(c *irv1.Catalog) (*irv1.Provenance, error) {
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("mapping: fingerprinting catalog %s: %w", c.Name.Stored, err)
	}
	sum := sha256.Sum256(b)
	return &irv1.Provenance{
		Source:           Source,
		SourceVersion:    Version,
		InputFingerprint: "sha256:" + hex.EncodeToString(sum[:]),
		Fidelity:         irv1.SourceFidelity_SOURCE_FIDELITY_FULL,
	}, nil
}

// exts collects one object's extensions.
type exts struct {
	m   *mapper
	all []*anypb.Any
}

func (e *exts) add(msgs ...proto.Message) {
	for _, msg := range msgs {
		b, err := proto.MarshalOptions{Deterministic: true}.Marshal(msg)
		if err != nil {
			e.m.fail(fmt.Errorf("mapping: %w", err))
			continue
		}
		e.all = append(e.all, &anypb.Any{TypeUrl: TypeURLPrefix + string(msg.ProtoReflect().Descriptor().FullName()), Value: b})
	}
}

func (e *exts) comment(text string) {
	if text != "" {
		e.add(&sqliteextv1.Comment{Text: text})
	}
}

// list returns the extensions sorted by type URL, keeping the order in which
// those of one type were added.
func (e *exts) list() []*anypb.Any {
	slices.SortStableFunc(e.all, func(a, b *anypb.Any) int { return strings.Compare(a.TypeUrl, b.TypeUrl) })
	return e.all
}
