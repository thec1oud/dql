// Package mapping turns a decoded dpg snapshot into the catalog layer of the
// IR: dpg-snapshot's output (design record §2.2, §6). It maps each object the
// way pg-live maps the live catalog of the same schema, through the same
// functions: types by pgsql.ParseTypeText and pgsql.Types, defaults by
// pgsql.ClassifyDefault, checks by pgsql.RecognizeCheck, collations by
// pgsql.CollationFacts and annotations by comment.Annotations. The two
// catalogs then differ only by what a snapshot does not record, the limits
// fidelity.DPGSnapshot names, which the provenance lists.
//
// Every first-class object of §6.1 becomes its IR message, and what the IR
// has no place for travels as the dql.ext.postgres.v1 extension pg-live
// writes for it. What only dpg records, such as renamed_from, name_maps,
// grant statements or the objects dpg keeps by body hash, travels as a
// dql.ext.dpg.v1 Record, so nothing the snapshot holds is lost (§6.3).
//
// The mapping emits the facts the snapshot states and nothing the core
// derives. Identifiers carry their stored names only: words and host names
// are the core's (§4.2). Annotations are the tokens of comments as read, one
// per token, with values kept as written (§7.1). Uniqueness facts do not say
// whether they identify rows, tables carry no relations or logical keys, and
// overloads no signatures; the core computes those (§11.1, §12, §4.6).
//
// Output is deterministic (§3.2): columns, attributes, arguments and
// constraint column lists keep the snapshot's order, every other list is
// sorted by stored name, compared bytewise, and extensions by type URL.
package mapping

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"

	dpgextv1 "github.com/thec1oud/dql/proto/dql/ext/dpg/v1"
	pgextv1 "github.com/thec1oud/dql/proto/dql/ext/postgres/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/dpgsnapshot/internal/format"
	"github.com/thec1oud/dql/sources/fidelity"
)

// The source plugin's name and version, as provenance and annotation origins
// record them (§3.1, §7.4).
const (
	Source  = "dpg-snapshot"
	Version = "1.0.0"
)

// TypeURLPrefix prefixes the type URL of every extension (§6.2).
const TypeURLPrefix = "type.dql.dev/"

// Options carries what the mapping needs from config (§1.3).
type Options struct {
	// Catalog names the catalog. Empty means the snapshot's database (§5.1).
	Catalog string
	// DefaultNamespace is config's default-namespace. Empty means public,
	// Postgres's own (§4.3).
	DefaultNamespace string
	// Namespaces are the schemas read. None means every schema the snapshot
	// holds.
	Namespaces []string
}

// Map builds the catalog s describes. Diagnostics report annotations that
// cannot be read and views whose columns cannot be read from their query,
// sorted (§3.2 rule 5). An error means the snapshot describes no database, a
// cluster-level snapshot, or holds text that does not parse as what dpg
// writes, such as a type or a constraint.
func Map(s *format.Snapshot, opts Options) (*irv1.Catalog, []*irv1.Diagnostic, error) {
	opts.Catalog = cmp.Or(opts.Catalog, s.Database)
	if opts.Catalog == "" {
		return nil, nil, fmt.Errorf("mapping: the dpg snapshot of cluster %q describes no database, so it holds no catalog", s.Cluster)
	}
	m := newMapper(s, opts)
	c := m.catalog(s)
	if m.err != nil {
		return nil, nil, m.err
	}
	c.Provenance = &irv1.Provenance{
		Source:           Source,
		SourceVersion:    Version,
		InputFingerprint: s.Fingerprint,
		Fidelity:         irv1.SourceFidelity_SOURCE_FIDELITY_PARTIAL,
		FidelityLimits:   slices.Clone(fidelity.DPGSnapshot),
	}
	sortDiagnostics(m.diags)
	return c, m.diags, nil
}

// mapper holds the state of one Map call. Its first error sticks: later steps
// still run on zero values, and Map returns the error.
type mapper struct {
	opts Options

	// The snapshot's tables, views, types and collations by schema and name.
	tables     map[objectKey]*format.Table
	views      map[objectKey]*format.View
	typeDefs   map[objectKey]*format.Type
	collations map[objectKey]*format.Opaque
	sequences  map[objectKey]bool

	b     *namespaces
	names map[string]*namer
	// mapped holds the mapped tables and views, which view columns are read
	// from; viewing marks the views being read, to stop a cycle.
	mapped  map[objectKey]*irv1.Table
	viewing map[objectKey]bool

	diags []*irv1.Diagnostic
	err   error
}

// objectKey identifies an object in a schema.
type objectKey struct{ schema, name string }

func (k objectKey) String() string { return k.schema + "." + k.name }

func newMapper(s *format.Snapshot, opts Options) *mapper {
	m := &mapper{
		opts:   opts,
		tables: map[objectKey]*format.Table{}, views: map[objectKey]*format.View{},
		typeDefs: map[objectKey]*format.Type{}, collations: map[objectKey]*format.Opaque{},
		sequences: map[objectKey]bool{},
		names:     map[string]*namer{},
		mapped:    map[objectKey]*irv1.Table{}, viewing: map[objectKey]bool{},
	}
	m.b = &namespaces{m: m, byName: map[string]*namespace{}}
	for _, e := range s.Objects {
		switch o := e.Object; {
		case o.Table != nil:
			m.tables[objectKey{o.Table.Schema, o.Table.Name}] = o.Table
		case o.View != nil:
			m.views[objectKey{o.View.Schema, o.View.Name}] = o.View
		case o.Type != nil:
			m.typeDefs[objectKey{o.Type.Schema, o.Type.Name}] = o.Type
		case o.Sequence != nil:
			m.sequences[objectKey{o.Sequence.Schema, o.Sequence.Name}] = true
		case o.Opaque != nil && o.Kind == "collation":
			m.collations[objectKey{o.Opaque.Schema, o.Opaque.Name}] = o.Opaque
		}
	}
	return m
}

func (m *mapper) fail(err error) {
	if m.err == nil {
		m.err = err
	}
}

// qn names an object of this catalog; member may be empty.
func (m *mapper) qn(schema, object, member string) *irv1.QualifiedName {
	return &irv1.QualifiedName{Catalog: m.opts.Catalog, Namespace: schema, Object: object, Member: member}
}

func ident(stored string) *irv1.Identifier { return &irv1.Identifier{Stored: stored} }

// wanted reports whether objects of schema belong in the catalog.
func (m *mapper) wanted(schema string) bool {
	return len(m.opts.Namespaces) == 0 || slices.Contains(m.opts.Namespaces, schema)
}

// catalog maps every object, in the order Postgres would create them, so
// derived names come out as the database's: schemas, then types, whose
// domains name their checks, then tables, which name their constraints and
// sequences, then views, whose columns are read from the tables, and the
// rest. Within a step objects come in key order (format.Snapshot).
func (m *mapper) catalog(s *format.Snapshot) *irv1.Catalog {
	c := &irv1.Catalog{Name: ident(m.opts.Catalog), DefaultNamespace: cmp.Or(m.opts.DefaultNamespace, "public")}
	catalogExts := &exts{m: m}
	for _, step := range []func(format.Entry, *exts){m.schemaStep, m.typeStep, m.tableStep, m.viewStep, m.otherStep} {
		for _, e := range s.Objects {
			step(e, catalogExts)
		}
	}
	for _, n := range m.b.all {
		c.Namespaces = append(c.Namespaces, n.finish())
	}
	slices.SortFunc(c.Namespaces, func(a, b *irv1.Namespace) int { return strings.Compare(a.Name.Stored, b.Name.Stored) })
	c.Extensions = catalogExts.list()
	return c
}

func (m *mapper) schemaStep(e format.Entry, _ *exts) {
	sc := e.Object.Schema
	if sc == nil || !m.wanted(sc.Name) {
		return
	}
	n := m.b.get(sc.Name)
	n.ns.Annotations = m.annotations(m.qn(sc.Name, "", ""), "COMMENT ON SCHEMA "+sc.Name, deref(sc.Comment))
	n.exts.comment(deref(sc.Comment))
	n.exts.owner(sc.Owner)
	n.exts.record("schema", "", sc, "name", "owner", "comment")
}

func (m *mapper) typeStep(e format.Entry, _ *exts) {
	t := e.Object.Type
	if t == nil || !m.wanted(t.Schema) {
		return
	}
	n := m.b.get(t.Schema)
	switch t.Variant {
	case "ENUM":
		n.ns.Enums = append(n.ns.Enums, m.enum(t))
	case "DOMAIN":
		n.ns.Domains = append(n.ns.Domains, m.domain(t))
	case "COMPOSITE":
		n.ns.Composites = append(n.ns.Composites, m.composite(t))
	default:
		// A range records no subtype and a base type only a hash of its
		// definition (fidelity.RangeSubtypes, fidelity.BodyHashedObjects).
		m.ignored(m.qn(t.Schema, t.Name, ""), "COMMENT ON TYPE "+t.Schema+"."+t.Name, deref(t.Comment),
			"dpg-snapshot reads no annotations on "+strings.ToLower(t.Variant)+" types")
		n.exts.record("type", e.Key, t)
	}
}

func (m *mapper) tableStep(e format.Entry, _ *exts) {
	t := e.Object.Table
	if t == nil || !m.wanted(t.Schema) {
		return
	}
	n := m.b.get(t.Schema)
	if t.Foreign {
		// pg-live reads no foreign tables; the record keeps it.
		n.exts.record("table", e.Key, t)
		return
	}
	tbl := m.table(t)
	n.ns.Tables = append(n.ns.Tables, tbl)
	m.mapped[objectKey{t.Schema, t.Name}] = tbl
}

func (m *mapper) viewStep(e format.Entry, _ *exts) {
	if v := e.Object.View; v != nil && m.wanted(v.Schema) {
		m.view(v)
	}
}

// otherStep maps sequences, routines and extensions, and keeps every other
// object as a dpg record: on its namespace when it has a schema, else on the
// catalog.
func (m *mapper) otherStep(e format.Entry, catalogExts *exts) {
	o := e.Object
	switch {
	case o.Schema != nil, o.Type != nil, o.Table != nil, o.View != nil:
	case o.Sequence != nil:
		if m.wanted(o.Sequence.Schema) {
			m.sequence(o.Sequence)
		}
	case o.Function != nil:
		if m.wanted(o.Function.Schema) {
			m.function(e.Key, o.Function)
		}
	case o.Opaque != nil && o.Kind == "procedure":
		if m.wanted(o.Opaque.Schema) {
			m.procedure(e.Key, o.Opaque)
		}
	case o.Extension != nil:
		x := o.Extension
		catalogExts.add(&pgextv1.InstalledExtension{Name: x.Name, Version: deref(x.Version), Namespace: deref(x.Schema)})
		catalogExts.record("extension", e.Key, x, "name", "version", "schema")
	case o.Opaque != nil && o.Opaque.Schema != "":
		if m.wanted(o.Opaque.Schema) {
			m.b.get(o.Opaque.Schema).exts.record(o.Kind, e.Key, o.Opaque)
		}
	case o.VirtualType != nil:
		if m.wanted(o.VirtualType.Schema) {
			m.b.get(o.VirtualType.Schema).exts.record(o.Kind, e.Key, o.VirtualType)
		}
	case o.DefaultPrivileges != nil && deref(o.DefaultPrivileges.InSchema) != "":
		if s := *o.DefaultPrivileges.InSchema; m.wanted(s) {
			m.b.get(s).exts.record(o.Kind, e.Key, o.DefaultPrivileges)
		}
	default:
		// Roles, default and parameter privileges for the whole database,
		// and objects with no schema, such as event triggers and servers.
		catalogExts.record(o.Kind, e.Key, o)
	}
}

// namespaces builds the namespaces of a catalog.
type namespaces struct {
	m      *mapper
	byName map[string]*namespace
	all    []*namespace
}

type namespace struct {
	ns       *irv1.Namespace
	exts     *exts
	routines map[string]*irv1.Routine
}

// get returns the namespace named schema, created on first use, such as for
// objects in public, which a snapshot need not record.
func (b *namespaces) get(schema string) *namespace {
	n, ok := b.byName[schema]
	if !ok {
		n = &namespace{ns: &irv1.Namespace{Name: ident(schema)}, exts: &exts{m: b.m}, routines: map[string]*irv1.Routine{}}
		b.byName[schema] = n
		b.all = append(b.all, n)
	}
	return n
}

// finish sorts the namespace's lists by stored name: tables and views come
// from separate records, sequences from sequence records and from columns,
// and routines group overloads by name.
func (n *namespace) finish() *irv1.Namespace {
	ns := n.ns
	slices.SortStableFunc(ns.Tables, func(a, b *irv1.Table) int { return strings.Compare(a.Name.Stored, b.Name.Stored) })
	slices.SortStableFunc(ns.Enums, func(a, b *irv1.Enum) int { return strings.Compare(a.Name.Stored, b.Name.Stored) })
	slices.SortStableFunc(ns.Domains, func(a, b *irv1.Domain) int { return strings.Compare(a.Name.Stored, b.Name.Stored) })
	slices.SortStableFunc(ns.Composites, func(a, b *irv1.Composite) int { return strings.Compare(a.Name.Stored, b.Name.Stored) })
	slices.SortStableFunc(ns.Sequences, func(a, b *irv1.Sequence) int { return strings.Compare(a.Name.Stored, b.Name.Stored) })
	for _, name := range sortedKeys(n.routines) {
		ns.Routines = append(ns.Routines, n.routines[name])
	}
	ns.Extensions = n.exts.list()
	return ns
}

// exts collects one object's extensions.
type exts struct {
	m   *mapper
	all []*anypb.Any
}

// add packs each message with the dql type URL prefix.
func (e *exts) add(msgs ...proto.Message) {
	for _, msg := range msgs {
		b, err := proto.MarshalOptions{Deterministic: true}.Marshal(msg)
		if err != nil {
			e.m.fail(fmt.Errorf("mapping: %w", err))
			continue
		}
		url := TypeURLPrefix + string(msg.ProtoReflect().Descriptor().FullName())
		e.all = append(e.all, &anypb.Any{TypeUrl: url, Value: b})
	}
}

// comment keeps a comment's raw text (§7.5 step 2). An empty comment is no
// comment, as in Postgres.
func (e *exts) comment(text string) {
	if text != "" {
		e.add(&pgextv1.Comment{Text: text})
	}
}

// owner records an owning role, when the snapshot declares one
// (fidelity.Owners).
func (e *exts) owner(role *string) {
	if role != nil {
		e.add(&pgextv1.Owner{Role: *role})
	}
}

// tablespace records a tablespace, when one is declared.
func (e *exts) tablespace(name string) {
	if name != "" {
		e.add(&pgextv1.Tablespace{Name: name})
	}
}

// record carries the fields of v, a snapshot record, other than those named
// in used, as a dpg Record, unless none remain. Fields are read through v's
// JSON form, so they keep dpg's names and nothing dpg writes is missed. name
// is the record's key or member name, empty for the record of the object
// carrying it.
func (e *exts) record(kind, name string, v any, used ...string) {
	b, _ := json.Marshal(v)
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	var fields map[string]any
	_ = dec.Decode(&fields)
	for _, k := range used {
		delete(fields, k)
	}
	if len(fields) == 0 {
		return
	}
	// Decoded JSON holds only values a Struct can.
	s, _ := structpb.NewStruct(numbers(fields).(map[string]any))
	e.add(&dpgextv1.Record{Kind: kind, Name: name, Fields: s})
}

// numbers turns JSON numbers into doubles, or into strings for integers a
// double cannot hold exactly.
func numbers(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			x[k] = numbers(e)
		}
	case []any:
		for i, e := range x {
			x[i] = numbers(e)
		}
	case json.Number:
		if i, err := strconv.ParseInt(string(x), 10, 64); err == nil && (i > 1<<53 || i < -(1<<53)) {
			return string(x)
		}
		f, _ := x.Float64()
		return f
	}
	return v
}

// list returns the extensions sorted by type URL, keeping the order in which
// those of one type were added.
func (e *exts) list() []*anypb.Any {
	slices.SortStableFunc(e.all, func(a, b *anypb.Any) int { return strings.Compare(a.TypeUrl, b.TypeUrl) })
	return e.all
}

// sortedKeys returns m's keys in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// dbComment returns the comment dpg writes to the database for a table,
// column, view or function, and the record field it comes from. dpg writes a
// deprecation as the comment "[DEPRECATED] msg", in place of any comment, so
// the comment then stays in the dpg record.
func dbComment(c, deprecated *string) (string, string) {
	if deprecated != nil {
		return "[DEPRECATED] " + *deprecated, "deprecated"
	}
	return deref(c), "comment"
}

// sortDiagnostics orders diagnostics by severity, then qualified name, then
// code (§3.2 rule 5), and then message, so equal diagnostics of different
// objects keep a fixed order.
func sortDiagnostics(ds []*irv1.Diagnostic) {
	key := func(q *irv1.QualifiedName) string {
		return strings.Join([]string{q.GetCatalog(), q.GetNamespace(), q.GetObject(), q.GetMember()}, "\x00")
	}
	slices.SortStableFunc(ds, func(a, b *irv1.Diagnostic) int {
		return cmp.Or(
			cmp.Compare(a.GetSeverity(), b.GetSeverity()),
			strings.Compare(key(a.GetTarget()), key(b.GetTarget())),
			strings.Compare(a.GetCode(), b.GetCode()),
			strings.Compare(a.GetMessage(), b.GetMessage()),
		)
	})
}
