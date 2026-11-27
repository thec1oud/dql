// Package mapping turns an introspect.Snapshot into the catalog layer of the
// IR: mysql-live's output (design record §2.2, §6). A database is a catalog
// with one implicit namespace, stored as "" (§5.1). Every first-class object
// of §6.1 becomes its IR message, and everything else the Snapshot holds
// travels as a typed dql.ext.mysql.v1 extension (§6.2), so nothing read is
// lost (§6.3).
//
// As pg-live's, the mapping emits the facts the database states and nothing
// the core derives: identifiers carry stored names only, annotations are the
// tokens of comments as read, and uniqueness facts do not say whether they
// identify rows.
//
// Output is deterministic (§3.2): columns and index parts keep the
// database's order, every other list is sorted by stored name.
package mapping

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	mysqlextv1 "github.com/thec1oud/dql/proto/dql/ext/mysql/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/internal/comment"
	"github.com/thec1oud/dql/sources/internal/mysqlsql"
	"github.com/thec1oud/dql/sources/mysqllive/internal/introspect"
)

// The source plugin's name and version, as provenance and annotation origins
// record them (§3.1, §7.4).
const (
	Source  = "mysql-live"
	Version = "1.0.0"
)

// TypeURLPrefix prefixes the type URL of every extension (§6.2).
const TypeURLPrefix = "type.dql.dev/"

// Diagnostic codes. They are stable: tools and tests match on them.
const (
	// An annotation value whose parenthesis is never closed.
	CodeAnnotationUnclosed = "mysqllive-annotation-unclosed"
	// Annotations in a comment mysql-live does not read, such as an index's
	// or a routine's that is not public.
	CodeAnnotationIgnored = "mysqllive-annotation-ignored"
)

// Options carries what the mapping needs from config and the connection.
type Options struct {
	// Catalog names the catalog.
	Catalog string
	// Database is the database's own name, which foreign keys to other
	// databases are told apart by. Those name the other database as their
	// catalog (§5.3).
	Database string
}

// Map builds the catalog of s. Diagnostics report annotations that cannot be
// read; an error means the Snapshot holds text that does not parse as what
// the server prints, such as a column type or a check constraint.
func Map(s *introspect.Snapshot, opts Options) (*irv1.Catalog, []*irv1.Diagnostic, error) {
	m := &mapper{opts: opts, flavor: s.Database.Flavor, pad: map[string]bool{}}
	for _, c := range s.Database.Collations {
		m.pad[c.Name] = c.PadSpace
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
	flavor string
	pad    map[string]bool
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
		table, enums := m.table(t)
		ns.Tables = append(ns.Tables, table)
		ns.Enums = append(ns.Enums, enums...)
	}
	slices.SortFunc(ns.Enums, func(a, b *irv1.Enum) int { return strings.Compare(a.Name.Stored, b.Name.Stored) })
	var hidden exts
	hidden.m = m
	for _, r := range s.Routines {
		if rt := m.routine(r, &hidden); rt != nil {
			ns.Routines = append(ns.Routines, rt)
		}
	}
	ns.Extensions = hidden.list()
	e := &exts{m: m}
	e.add(&mysqlextv1.Database{Flavor: s.Database.Flavor, DefaultCharacterSet: s.Database.CharacterSet,
		DefaultCollation: s.Database.Collation})
	return &irv1.Catalog{Name: ident(m.opts.Catalog), Namespaces: []*irv1.Namespace{ns}, Extensions: e.list()}
}

// table maps a table or view, and returns the inline enums of its enum
// columns (§8.4).
func (m *mapper) table(t introspect.Table) (*irv1.Table, []*irv1.Enum) {
	out := &irv1.Table{Name: ident(t.Name), Kind: irv1.TableKind_RELATION_KIND_TABLE}
	e := &exts{m: m}
	if t.View {
		out.Kind = irv1.TableKind_RELATION_KIND_VIEW
		v := t.ViewDef
		if v != nil {
			e.add(&mysqlextv1.ViewDefinition{Definition: v.Definition, Updatable: v.Updatable,
				SecurityType: v.SecurityType, CheckOption: v.CheckOption})
			for _, r := range v.Reads {
				db, name, _ := strings.Cut(r, ".")
				out.DependsOn = append(out.DependsOn, m.ref(db, name))
			}
		}
	} else {
		out.Annotations = m.annotations(m.qn(t.Name, ""), "the comment of table "+t.Name, t.Comment)
		e.comment(t.Comment)
		e.add(&mysqlextv1.TableOptions{Engine: t.Engine, Collation: t.Collation, CreateOptions: t.CreateOptions})
	}
	var enums []*irv1.Enum
	checked := map[string]mysqlsql.CheckColumn{}
	for i, c := range t.Columns {
		col, en := m.column(t, c, i)
		out.Columns = append(out.Columns, col)
		if en != nil {
			enums = append(enums, en)
		}
		checked[c.Name] = mysqlsql.CheckColumn{Type: col.Type, Collation: col.Collation}
	}
	for _, c := range t.Checks {
		con, err := mysqlsql.RecognizeCheck(mysqlsql.Unescape(c.Clause), checked)
		if err != nil {
			m.fail(fmt.Errorf("mapping: check constraint %s of %s: %w", c.Name, t.Name, err))
			continue
		}
		con.Name = c.Name
		if !c.Enforced {
			// The server does not enforce it, so nothing may rely on it.
			con.Form = &irv1.Constraint_Opaque{Opaque: &irv1.NativeText{Native: mysqlsql.Unescape(c.Clause)}}
			con.ClientValidated = false
		}
		out.Constraints = append(out.Constraints, con)
	}
	for _, x := range t.Indexes {
		m.index(out, x, e)
	}
	slices.SortFunc(out.Uniqueness, func(a, b *irv1.Uniqueness) int { return strings.Compare(a.Name, b.Name) })
	for _, fk := range t.ForeignKeys {
		out.ForeignKeys = append(out.ForeignKeys, m.foreignKey(fk))
	}
	for _, tr := range t.Triggers {
		e.add(&mysqlextv1.Trigger{Name: tr.Name, Timing: tr.Timing, Event: tr.Event, Order: tr.Order, Statement: tr.Statement})
	}
	out.Extensions = e.list()
	return out, enums
}

// ref names a table of this catalog, or of another database's catalog.
func (m *mapper) ref(db, table string) *irv1.QualifiedName {
	if db == m.opts.Database {
		return m.qn(table, "")
	}
	return &irv1.QualifiedName{Catalog: db, Object: table}
}

// column maps the i-th column of t. A table column is non-null when the
// server declares it NOT NULL. A view column is always unproven (§6.4): the
// server's own inference for views is not a guarantee dql relies on.
func (m *mapper) column(t introspect.Table, c introspect.Column, i int) (*irv1.Column, *irv1.Enum) {
	name := t.Name + "." + c.Name
	var enumRef *irv1.QualifiedName
	if strings.EqualFold(c.DataType, "enum") {
		enumRef = m.qn(t.Name+"_"+c.Name, "")
	}
	typ, labels, err := mysqlsql.ParseType(mysqlsql.Column{DataType: c.DataType, ColumnType: c.ColumnType, Enum: enumRef})
	if err != nil {
		m.fail(fmt.Errorf("mapping: column %s: %w", name, err))
		typ = &irv1.Type{}
	}
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
		col.Collation = mysqlsql.Collation(c.Collation, m.pad[c.Collation])
	}
	extra := strings.ToLower(c.Extra)
	switch {
	case strings.Contains(extra, "stored generated"):
		col.Generation = &irv1.Generation{Kind: irv1.GenerationKind_GENERATION_STORED, Native: mysqlsql.Unescape(c.Generation)}
	case strings.Contains(extra, "virtual generated"):
		col.Generation = &irv1.Generation{Kind: irv1.GenerationKind_GENERATION_VIRTUAL, Native: mysqlsql.Unescape(c.Generation)}
	default:
		col.Default = mysqlsql.ClassifyDefault(mysqlsql.Default{Text: c.Default, Extra: c.Extra, MariaDB: m.flavor == "mariadb", Type: typ})
	}
	e := &exts{m: m}
	if !t.View {
		col.Annotations = m.annotations(m.qn(t.Name, c.Name), "the comment of column "+name, c.Comment)
		e.comment(c.Comment)
	}
	if c.CharacterSet != "" {
		e.add(&mysqlextv1.ColumnCharset{CharacterSet: c.CharacterSet})
	}
	col.Extensions = e.list()
	if enumRef == nil {
		return col, nil
	}
	return col, &irv1.Enum{Name: ident(enumRef.Object), Labels: labels, Origin: irv1.EnumOrigin_ENUM_ORIGIN_INLINE,
		DerivedFrom: m.qn(t.Name, c.Name)}
}

// index maps an index: the primary key and each unique index are
// uniqueness facts (§11.2), with the index carried on them; any other index
// is an extension of its table (§6.2). MySQL has no partial indexes, and
// lets a unique key hold several NULLs. A prefix or expression part is an
// expression element, so the key never identifies rows.
func (m *mapper) index(t *irv1.Table, x introspect.Index, e *exts) {
	ix := &mysqlextv1.Index{Name: x.Name, Type: x.Type, Visible: x.Visible, Comment: x.Comment}
	for _, p := range x.Parts {
		ix.Parts = append(ix.Parts, &mysqlextv1.IndexPart{Column: p.Column, Prefix: p.Prefix,
			Expression: mysqlsql.Unescape(p.Expression), Descending: p.Descending})
	}
	m.ignored(m.qn(t.Name.Stored, x.Name), "the comment of index "+x.Name, x.Comment, "mysql-live reads no annotations on indexes")
	if !x.Unique {
		e.add(ix)
		return
	}
	u := &irv1.Uniqueness{Name: x.Name, Kind: irv1.UniquenessKind_UNIQUENESS_CONSTRAINT, NullsDistinct: true}
	if x.Name == "PRIMARY" {
		u.Kind = irv1.UniquenessKind_UNIQUENESS_PRIMARY_KEY
	}
	for i, p := range x.Parts {
		switch {
		case p.Expression != "":
			u.Expressions = append(u.Expressions, &irv1.IndexExpression{Position: uint32(i), Native: mysqlsql.Unescape(p.Expression)})
		case p.Prefix > 0:
			u.Expressions = append(u.Expressions, &irv1.IndexExpression{Position: uint32(i), Native: fmt.Sprintf("`%s`(%d)", p.Column, p.Prefix)})
		default:
			u.Columns = append(u.Columns, p.Column)
		}
	}
	ue := &exts{m: m}
	ue.add(ix)
	u.Extensions = ue.list()
	t.Uniqueness = append(t.Uniqueness, u)
}

var actions = map[string]irv1.ForeignKeyAction{
	"NO ACTION":   irv1.ForeignKeyAction_FOREIGN_KEY_ACTION_NO_ACTION,
	"RESTRICT":    irv1.ForeignKeyAction_FOREIGN_KEY_ACTION_RESTRICT,
	"CASCADE":     irv1.ForeignKeyAction_FOREIGN_KEY_ACTION_CASCADE,
	"SET NULL":    irv1.ForeignKeyAction_FOREIGN_KEY_ACTION_SET_NULL,
	"SET DEFAULT": irv1.ForeignKeyAction_FOREIGN_KEY_ACTION_SET_DEFAULT,
}

// foreignKey maps a foreign key. InnoDB enforces every foreign key as MATCH
// SIMPLE: a key with a NULL column is not checked (§12.3).
func (m *mapper) foreignKey(fk introspect.ForeignKey) *irv1.ForeignKey {
	return &irv1.ForeignKey{
		Name:       ident(fk.Name),
		Columns:    fk.Columns,
		References: &irv1.ColumnSet{Table: m.ref(fk.RefDB, fk.RefTable), Columns: fk.RefCols},
		Match:      irv1.ForeignKeyMatch_FOREIGN_KEY_MATCH_SIMPLE,
		OnUpdate:   actions[fk.OnUpdate],
		OnDelete:   actions[fk.OnDelete],
	}
}

// routine maps a public function or procedure, marked @public in its
// comment (§6.5); any other travels whole on hidden. MySQL has no
// overloading, so each routine has one overload.
func (m *mapper) routine(r introspect.Routine, hidden *exts) *irv1.Routine {
	what := strings.ToLower(r.Type) + " " + r.Name
	target := m.qn(r.Name, "")
	if !comment.Marked(r.Comment, "public") {
		m.ignored(target, "the comment of "+what, r.Comment, "the routine is not exposed, so mysql-live reads no annotations on it")
		hidden.add(&mysqlextv1.Routine{Name: r.Name, Type: r.Type, Definition: r.Definition, Comment: r.Comment})
		return nil
	}
	o := &irv1.Overload{
		Kind:         irv1.RoutineKind_ROUTINE_KIND_FUNCTION,
		NullBehavior: irv1.NullBehavior_NULL_BEHAVIOR_CALLED_ON_NULL_INPUT,
		Volatility:   irv1.Volatility_VOLATILITY_VOLATILE,
	}
	if r.Deterministic && r.DataAccess != "MODIFIES SQL DATA" {
		// MySQL's DETERMINISTIC is the definer's claim, as Postgres's
		// IMMUTABLE is. A function that reads data can change its result
		// between statements, so it is at most stable.
		o.Volatility = irv1.Volatility_VOLATILITY_IMMUTABLE
		if r.DataAccess == "READS SQL DATA" {
			o.Volatility = irv1.Volatility_VOLATILITY_STABLE
		}
	}
	typeOf := func(where, dtd string) *irv1.Type {
		base, _, _ := strings.Cut(dtd, "(")
		base, _, _ = strings.Cut(base, " ")
		t, _, err := mysqlsql.ParseType(mysqlsql.Column{DataType: base, ColumnType: dtd})
		if err != nil {
			m.fail(fmt.Errorf("mapping: %s of %s: %w", where, what, err))
			return &irv1.Type{}
		}
		return t
	}
	for _, a := range r.Parameters {
		arg := &irv1.Argument{Name: ident(a.Name), Type: typeOf("argument "+a.Name, a.Type), Mode: argModes[a.Mode]}
		o.Arguments = append(o.Arguments, arg)
	}
	if r.Type == "PROCEDURE" {
		o.Kind = irv1.RoutineKind_ROUTINE_KIND_PROCEDURE
		o.Returns = &irv1.Returns{Kind: &irv1.Returns_Nothing{Nothing: &irv1.Empty{}}}
	} else {
		o.Returns = &irv1.Returns{Kind: &irv1.Returns_Scalar{Scalar: &irv1.ReturnValue{Type: typeOf("result", r.Returns), Nullable: true}}}
	}
	o.Annotations = m.annotations(target, "the comment of "+what, r.Comment)
	e := &exts{m: m}
	e.comment(r.Comment)
	e.add(&mysqlextv1.RoutineProperties{Deterministic: r.Deterministic, DataAccess: r.DataAccess,
		SecurityType: r.SecurityType, Definition: r.Definition})
	o.Extensions = e.list()
	return &irv1.Routine{Name: ident(r.Name), Overloads: []*irv1.Overload{o}}
}

var argModes = map[string]irv1.ArgMode{
	"":      irv1.ArgMode_ARG_MODE_IN,
	"IN":    irv1.ArgMode_ARG_MODE_IN,
	"OUT":   irv1.ArgMode_ARG_MODE_OUT,
	"INOUT": irv1.ArgMode_ARG_MODE_INOUT,
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

// ignored reports the annotation tokens of a comment mysql-live does not
// read, saying why. The comment still travels whole.
func (m *mapper) ignored(target *irv1.QualifiedName, location, text, why string) {
	var keys []string
	for _, tok := range comment.ParseComment(text) {
		keys = append(keys, "@"+tok.Key)
	}
	if len(keys) > 0 {
		m.diag(irv1.Severity_SEVERITY_WARNING, CodeAnnotationIgnored, target, fmt.Sprintf(
			"%s: %s, so %s is ignored; remove the annotation tokens from the comment", location, why, strings.Join(keys, " ")))
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
		e.add(&mysqlextv1.Comment{Text: text})
	}
}

// list returns the extensions sorted by type URL, keeping the order in which
// those of one type were added.
func (e *exts) list() []*anypb.Any {
	slices.SortStableFunc(e.all, func(a, b *anypb.Any) int { return strings.Compare(a.TypeUrl, b.TypeUrl) })
	return e.all
}
