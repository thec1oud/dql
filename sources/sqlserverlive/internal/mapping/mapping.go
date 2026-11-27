// Package mapping turns an introspect.Snapshot into the catalog layer of the
// IR: sqlserver-live's output (design record §2.2, §6). A database is a
// catalog and each schema a namespace, dbo the default one (§4.3, §5.1).
// Every first-class object of §6.1 becomes its IR message, and everything
// else the Snapshot holds travels as a typed dql.ext.sqlserver.v1 extension
// (§6.2), so nothing read is lost (§6.3).
//
// Annotations are the tokens of MS_Description extended properties, SQL
// Server's comments (§7.1). As pg-live's, the mapping emits the facts the
// database states and nothing the core derives: identifiers carry stored
// names only, and uniqueness facts do not say whether they identify rows.
//
// Output is deterministic (§3.2): columns, parameters and index parts keep
// the database's order, every other list is sorted by name.
package mapping

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	sqlserverextv1 "github.com/thec1oud/dql/proto/dql/ext/sqlserver/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/internal/comment"
	"github.com/thec1oud/dql/sources/internal/mssqlsql"
	"github.com/thec1oud/dql/sources/sqlserverlive/internal/introspect"
)

// The source plugin's name and version, as provenance and annotation origins
// record them (§3.1, §7.4).
const (
	Source  = "sqlserver-live"
	Version = "1.0.0"
)

// TypeURLPrefix prefixes the type URL of every extension (§6.2).
const TypeURLPrefix = "type.dql.dev/"

// Diagnostic codes. They are stable: tools and tests match on them.
const (
	// An annotation value whose parenthesis is never closed.
	CodeAnnotationUnclosed = "sqlserverlive-annotation-unclosed"
	// Annotations in a description sqlserver-live does not read, such as an
	// index's.
	CodeAnnotationIgnored = "sqlserverlive-annotation-ignored"
)

// Options carries what the mapping needs from config.
type Options struct {
	// Catalog names the catalog.
	Catalog string
	// Database is the database's own name, which a view reading another
	// database tells this one apart by.
	Database string
	// DefaultNamespace overrides dbo as the default namespace (§4.3).
	DefaultNamespace string
}

// Map builds the catalog of s. Diagnostics report annotations that cannot be
// read or are ignored; an error means the Snapshot holds a check constraint
// or an index filter that does not parse as SQL Server prints one.
func Map(s *introspect.Snapshot, opts Options) (*irv1.Catalog, []*irv1.Diagnostic, error) {
	m := &mapper{opts: opts}
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
	opts  Options
	diags []*irv1.Diagnostic
	err   error
}

func (m *mapper) fail(err error) {
	if m.err == nil {
		m.err = err
	}
}

// qn names an object of this catalog; object and member may be empty.
func (m *mapper) qn(schema, object, member string) *irv1.QualifiedName {
	return &irv1.QualifiedName{Catalog: m.opts.Catalog, Namespace: schema, Object: object, Member: member}
}

func ident(stored string) *irv1.Identifier { return &irv1.Identifier{Stored: stored} }

func (m *mapper) catalog(s *introspect.Snapshot) *irv1.Catalog {
	c := &irv1.Catalog{Name: ident(m.opts.Catalog), DefaultNamespace: cmp.Or(m.opts.DefaultNamespace, "dbo")}
	for _, sc := range s.Schemas {
		ns := &irv1.Namespace{Name: ident(sc.Name)}
		ns.Annotations = m.annotations(m.qn(sc.Name, "", ""), "the MS_Description of schema "+sc.Name, sc.Comment)
		e := &exts{m: m}
		e.comment(sc.Comment)
		for _, t := range sc.Tables {
			ns.Tables = append(ns.Tables, m.table(sc.Name, t))
		}
		for _, r := range sc.Routines {
			if rt := m.routine(sc.Name, r); rt != nil {
				ns.Routines = append(ns.Routines, rt)
			} else {
				e.add(&sqlserverextv1.Routine{Name: r.Name, Type: r.Type, Definition: r.Definition, Comment: r.Comment})
			}
		}
		ns.Extensions = e.list()
		c.Namespaces = append(c.Namespaces, ns)
	}
	e := &exts{m: m}
	db := s.Database
	e.add(&sqlserverextv1.Database{Version: db.Version, Collation: db.Collation, CompatibilityLevel: db.CompatibilityLevel,
		ReadCommittedSnapshot: db.ReadCommittedSnapshot, SnapshotIsolation: db.SnapshotIsolation})
	c.Extensions = e.list()
	return c
}

// table maps a table or a view.
func (m *mapper) table(schema string, t introspect.Table) *irv1.Table {
	name := schema + "." + t.Name
	out := &irv1.Table{Name: ident(t.Name), Kind: irv1.TableKind_RELATION_KIND_TABLE}
	what := "table "
	if t.View {
		out.Kind, what = irv1.TableKind_RELATION_KIND_VIEW, "view "
	}
	out.Annotations = m.annotations(m.qn(schema, t.Name, ""), "the MS_Description of "+what+name, t.Comment)
	e := &exts{m: m}
	e.comment(t.Comment)

	checked := map[string]mssqlsql.CheckColumn{}
	bits := map[string]bool{}
	for i, c := range t.Columns {
		col := m.column(schema, t, c, i)
		out.Columns = append(out.Columns, col)
		checked[c.Name] = mssqlsql.CheckColumn{System: strings.ToLower(c.Type.Name), Type: col.Type, Collation: col.Collation}
		bits[c.Name] = col.Type.GetBool() != nil
	}
	for _, c := range t.Checks {
		out.Constraints = append(out.Constraints, m.check(schema, t.Name, c, checked))
	}
	for _, x := range t.Indexes {
		m.index(schema, t.Name, out, x, bits, e)
	}
	slices.SortFunc(out.Uniqueness, func(a, b *irv1.Uniqueness) int { return strings.Compare(a.Name, b.Name) })
	for _, fk := range t.ForeignKeys {
		out.ForeignKeys = append(out.ForeignKeys, m.foreignKey(schema, t.Name, fk))
	}
	for _, tr := range t.Triggers {
		timing := "AFTER"
		if tr.InsteadOf {
			timing = "INSTEAD OF"
		}
		e.add(&sqlserverextv1.Trigger{Name: tr.Name, Timing: timing, Events: tr.Events, Disabled: tr.Disabled, Definition: tr.Definition})
	}
	if v := t.ViewDef; v != nil {
		e.add(&sqlserverextv1.ViewDefinition{Definition: v.Definition, CheckOption: v.CheckOption, SchemaBound: v.SchemaBound})
		for _, r := range v.Reads {
			out.DependsOn = append(out.DependsOn, m.ref(r))
		}
	}
	out.Extensions = e.list()
	return out
}

// ref names what a view reads: schema.name in this database, or
// database.schema.name in another, whose catalog is named as the database.
func (m *mapper) ref(read string) *irv1.QualifiedName {
	parts := strings.SplitN(read, ".", 3)
	if len(parts) == 3 {
		return &irv1.QualifiedName{Catalog: parts[0], Namespace: parts[1], Object: parts[2]}
	}
	return m.qn(parts[0], parts[1], "")
}

// column maps the i-th column of t. A table column is non-null when SQL
// Server declares it NOT NULL. A view column is always unproven (§6.4):
// the server's inference for views is not a guarantee dql relies on. An
// IDENTITY column is identity always; a computed column is generated,
// stored when PERSISTED; and a rowversion column is generated too, as SQL
// Server writes it on every insert and update.
func (m *mapper) column(schema string, t introspect.Table, c introspect.Column, i int) *irv1.Column {
	typ := mssqlsql.ParseType(mssqlsql.Type(c.Type))
	col := &irv1.Column{
		Name:              ident(c.Name),
		Ordinal:           uint32(i + 1),
		Type:              typ,
		Nullable:          c.Nullable || t.View,
		NullabilitySource: irv1.NullabilitySource_NULLABILITY_SOURCE_UNPROVEN,
	}
	if !col.Nullable {
		col.NullabilitySource = irv1.NullabilitySource_NULLABILITY_SOURCE_DATABASE
	}
	if c.Collation != "" {
		col.Collation = mssqlsql.Collation(c.Collation)
	}
	e := &exts{m: m}
	e.comment(c.Comment)
	if c.Alias != "" {
		as, an, _ := strings.Cut(c.Alias, ".")
		e.add(&sqlserverextv1.AliasType{Schema: as, Name: an})
	}
	switch {
	case c.Computed != nil:
		col.Generation = &irv1.Generation{Kind: irv1.GenerationKind_GENERATION_VIRTUAL, Native: c.Computed.Definition}
		if c.Computed.Persisted {
			col.Generation.Kind = irv1.GenerationKind_GENERATION_STORED
		}
	case strings.EqualFold(c.Type.Name, "timestamp") && !t.View:
		col.Generation = &irv1.Generation{Kind: irv1.GenerationKind_GENERATION_STORED, Native: "rowversion"}
	case !t.View:
		var def *string
		if c.Default != nil {
			def = &c.Default.Definition
			e.add(&sqlserverextv1.DefaultConstraint{Name: c.Default.Name})
		}
		col.Default = mssqlsql.ClassifyDefault(def, typ, c.Identity != nil)
	}
	if c.Identity != nil {
		e.add(&sqlserverextv1.Identity{Seed: c.Identity.Seed, Increment: c.Identity.Increment})
	}
	if c.RowGUID {
		e.add(&sqlserverextv1.RowGuidColumn{})
	}
	what := "table "
	if t.View {
		what = "view "
	}
	col.Annotations = m.annotations(m.qn(schema, t.Name, c.Name),
		"the MS_Description of column "+c.Name+" of "+what+schema+"."+t.Name, c.Comment)
	col.Extensions = e.list()
	return col
}

// check maps a check constraint. One SQL Server does not enforce, because
// it is disabled or not trusted to hold for rows written while it was, is
// opaque: no client may rely on it.
func (m *mapper) check(schema, table string, c introspect.Check, checked map[string]mssqlsql.CheckColumn) *irv1.Constraint {
	con, err := mssqlsql.RecognizeCheck(c.Definition, checked)
	if err != nil {
		m.fail(fmt.Errorf("mapping: check constraint %s of %s.%s: %w", c.Name, schema, table, err))
		return &irv1.Constraint{Name: c.Name}
	}
	con.Name = c.Name
	e := &exts{m: m}
	e.comment(c.Comment)
	if c.Disabled || c.NotTrusted {
		con.Form = &irv1.Constraint_Opaque{Opaque: &irv1.NativeText{Native: c.Definition}}
		con.ClientValidated = false
		e.add(&sqlserverextv1.UntrustedCheck{Name: c.Name, Disabled: c.Disabled, NotTrusted: c.NotTrusted})
	}
	con.Annotations = m.annotations(m.qn(schema, table, c.Name),
		"the MS_Description of constraint "+c.Name+" of "+schema+"."+table, c.Comment)
	con.Extensions = e.list()
	return con
}

// index maps an index: an enabled primary key, unique constraint or unique
// index is a uniqueness fact (§11.2), carrying its index, and any other
// index an extension of its table (§6.2). SQL Server lets a unique key hold
// one NULL, not several. A filtered index is partial.
func (m *mapper) index(schema, table string, out *irv1.Table, x introspect.Index, bits map[string]bool, e *exts) {
	ix := &sqlserverextv1.Index{Name: x.Name, Type: x.Type, Unique: x.Unique, Filter: x.Filter, Included: x.Included, Disabled: x.Disabled}
	for _, p := range x.Parts {
		ix.Parts = append(ix.Parts, &sqlserverextv1.IndexPart{Column: p.Column, Descending: p.Descending})
	}
	what := "index "
	if x.PrimaryKey || x.UniqueConstraint {
		what = "constraint "
	}
	location := "the MS_Description of " + what + x.Name + " of " + schema + "." + table
	m.ignored(m.qn(schema, table, x.Name), location, x.Comment, "sqlserver-live reads no annotations on keys and indexes")
	if !x.Unique || x.Disabled {
		ix.Comment = x.Comment
		e.add(ix)
		return
	}
	u := &irv1.Uniqueness{Name: x.Name, Kind: irv1.UniquenessKind_UNIQUENESS_INDEX}
	switch {
	case x.PrimaryKey:
		u.Kind = irv1.UniquenessKind_UNIQUENESS_PRIMARY_KEY
	case x.UniqueConstraint:
		u.Kind = irv1.UniquenessKind_UNIQUENESS_CONSTRAINT
	}
	for _, p := range x.Parts {
		u.Columns = append(u.Columns, p.Column)
	}
	if x.Filter != "" {
		u.Predicate = &irv1.NativeText{Native: x.Filter}
		p, err := mssqlsql.RecognizePredicate(x.Filter, bits)
		if err != nil {
			m.fail(fmt.Errorf("mapping: the filter of index %s of %s.%s: %w", x.Name, schema, table, err))
		}
		u.PredicateNullColumns, u.PredicateNotTrueColumns = p.NullColumns, p.NotTrueColumns
	}
	ue := &exts{m: m}
	ue.comment(x.Comment)
	ue.add(ix)
	u.Extensions = ue.list()
	out.Uniqueness = append(out.Uniqueness, u)
}

var actions = map[string]irv1.ForeignKeyAction{
	"NO_ACTION":   irv1.ForeignKeyAction_FOREIGN_KEY_ACTION_NO_ACTION,
	"CASCADE":     irv1.ForeignKeyAction_FOREIGN_KEY_ACTION_CASCADE,
	"SET_NULL":    irv1.ForeignKeyAction_FOREIGN_KEY_ACTION_SET_NULL,
	"SET_DEFAULT": irv1.ForeignKeyAction_FOREIGN_KEY_ACTION_SET_DEFAULT,
}

// foreignKey maps a foreign key. SQL Server checks a key as MATCH SIMPLE:
// a key with a NULL column is not checked (§12.3). One it does not enforce
// keeps its place, as the relation it states still holds for the rows the
// application writes; an extension says so.
func (m *mapper) foreignKey(schema, table string, fk introspect.ForeignKey) *irv1.ForeignKey {
	out := &irv1.ForeignKey{
		Name:       ident(fk.Name),
		Columns:    fk.Columns,
		References: &irv1.ColumnSet{Table: m.qn(fk.RefSchema, fk.RefTable, ""), Columns: fk.RefCols},
		Match:      irv1.ForeignKeyMatch_FOREIGN_KEY_MATCH_SIMPLE,
		OnUpdate:   actions[fk.OnUpdate],
		OnDelete:   actions[fk.OnDelete],
	}
	out.Annotations = m.annotations(m.qn(schema, table, fk.Name),
		"the MS_Description of constraint "+fk.Name+" of "+schema+"."+table, fk.Comment)
	e := &exts{m: m}
	e.comment(fk.Comment)
	if fk.Disabled || fk.NotTrusted {
		e.add(&sqlserverextv1.UntrustedForeignKey{Name: fk.Name, Disabled: fk.Disabled, NotTrusted: fk.NotTrusted})
	}
	out.Extensions = e.list()
	return out
}

// routineWhat spells each routine type as a description names its kind.
var routineWhat = map[string]string{
	"SQL_SCALAR_FUNCTION":              "function ",
	"SQL_INLINE_TABLE_VALUED_FUNCTION": "function ",
	"SQL_TABLE_VALUED_FUNCTION":        "function ",
	"SQL_STORED_PROCEDURE":             "procedure ",
}

// routine maps a function or procedure marked @public (§6.5), or returns
// nil for one that is not, which travels as an extension. SQL Server has
// no overloads, so a routine has one. A function's result is nullable, and
// so are a table-valued function's columns; a procedure returns nothing,
// and its OUTPUT parameters are both its inputs and its outputs. SQL Server
// declares no volatility, so every routine is volatile.
func (m *mapper) routine(schema string, r introspect.Routine) *irv1.Routine {
	location := "the MS_Description of " + routineWhat[r.Type] + schema + "." + r.Name
	target := m.qn(schema, r.Name, "")
	if !comment.Marked(r.Comment, "public") {
		m.ignored(target, location, r.Comment, "the routine is not exposed, so sqlserver-live reads no annotations on it")
		return nil
	}
	o := &irv1.Overload{
		Kind:         irv1.RoutineKind_ROUTINE_KIND_FUNCTION,
		NullBehavior: irv1.NullBehavior_NULL_BEHAVIOR_CALLED_ON_NULL_INPUT,
		Volatility:   irv1.Volatility_VOLATILITY_VOLATILE,
	}
	for _, p := range r.Parameters {
		a := &irv1.Argument{Name: ident(p.Name), Type: mssqlsql.ParseType(mssqlsql.Type(p.Type)), Mode: irv1.ArgMode_ARG_MODE_IN}
		if p.Output {
			a.Mode = irv1.ArgMode_ARG_MODE_INOUT
		}
		o.Arguments = append(o.Arguments, a)
	}
	switch {
	case r.Type == "SQL_STORED_PROCEDURE":
		o.Kind = irv1.RoutineKind_ROUTINE_KIND_PROCEDURE
		o.Returns = &irv1.Returns{Kind: &irv1.Returns_Nothing{Nothing: &irv1.Empty{}}}
	case r.Returns != nil:
		o.Returns = &irv1.Returns{Kind: &irv1.Returns_Scalar{Scalar: &irv1.ReturnValue{
			Type: mssqlsql.ParseType(mssqlsql.Type(*r.Returns)), Nullable: true}}}
	default:
		rt := &irv1.ReturnTable{}
		for i, c := range r.Columns {
			col := &irv1.Column{Name: ident(c.Name), Ordinal: uint32(i + 1), Type: mssqlsql.ParseType(mssqlsql.Type(c.Type)),
				Nullable: true, NullabilitySource: irv1.NullabilitySource_NULLABILITY_SOURCE_UNPROVEN}
			if c.Collation != "" {
				col.Collation = mssqlsql.Collation(c.Collation)
			}
			rt.Columns = append(rt.Columns, col)
		}
		o.Returns = &irv1.Returns{Kind: &irv1.Returns_Table{Table: rt}}
	}
	o.Annotations = m.annotations(target, location, r.Comment)
	e := &exts{m: m}
	e.comment(r.Comment)
	e.add(&sqlserverextv1.RoutineProperties{Type: r.Type, Definition: r.Definition, SchemaBound: r.SchemaBound})
	o.Extensions = e.list()
	return &irv1.Routine{Name: ident(r.Name), Overloads: []*irv1.Overload{o}}
}

// annotations reads the annotations of an object's description (§7.1,
// §7.5), reporting each token whose value is never closed.
func (m *mapper) annotations(target *irv1.QualifiedName, location, text string) []*irv1.Annotation {
	out, unclosed := comment.Annotations(text, Source, location)
	for _, tok := range unclosed {
		m.diag(irv1.Severity_SEVERITY_ERROR, CodeAnnotationUnclosed, target, fmt.Sprintf(
			"%s: the value of @%s has no closing parenthesis, so it is not read; close it", location, tok.Key))
	}
	return out
}

// ignored reports the annotation tokens in a description sqlserver-live
// does not read, if any.
func (m *mapper) ignored(target *irv1.QualifiedName, location, text, why string) {
	var keys []string
	for _, tok := range comment.ParseComment(text) {
		keys = append(keys, "@"+tok.Key)
	}
	if len(keys) > 0 {
		m.diag(irv1.Severity_SEVERITY_WARNING, CodeAnnotationIgnored, target, fmt.Sprintf(
			"%s: %s, so %s is ignored; remove the annotation tokens from the description",
			location, why, strings.Join(keys, " ")))
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
		e.add(&sqlserverextv1.Comment{Text: text})
	}
}

// list returns the extensions sorted by type URL, keeping the order in which
// those of one type were added.
func (e *exts) list() []*anypb.Any {
	slices.SortStableFunc(e.all, func(a, b *anypb.Any) int { return strings.Compare(a.TypeUrl, b.TypeUrl) })
	return e.all
}
