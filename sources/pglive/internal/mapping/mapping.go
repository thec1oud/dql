// Package mapping turns an introspect.Snapshot into the catalog layer of the
// IR: pg-live's output (design record §2.2, §6). Every first-class object of
// §6.1 becomes its IR message, and everything else the Snapshot holds travels
// as a typed dql.ext.postgres.v1 extension (§6.2), so nothing read is lost
// (§6.3).
//
// The mapping emits the facts the database states and nothing the core
// derives. Identifiers carry their stored names only: words and host names
// are the core's (§4.2). Annotations are the tokens of comments as read, one
// per token, with values kept as written; the core merges them with config
// and checks them (§7.1, §7.3). Uniqueness facts do not say whether they
// identify rows, tables carry no relations or logical keys, and overloads no
// signatures; the core computes those (§11.1, §12, §4.6).
//
// Output is deterministic (§3.2): columns, attributes, arguments and
// constraint column lists keep the database's order, every other list is
// sorted by stored name, compared bytewise, and extensions by type URL. The
// result does not depend on the order of the Snapshot's lists.
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

	pgextv1 "github.com/thec1oud/dql/proto/dql/ext/postgres/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/internal/pgsql"
	"github.com/thec1oud/dql/sources/pglive/internal/introspect"
)

// The source plugin's name and version, as provenance and annotation origins
// record them (§3.1, §7.4).
const (
	Source  = "pg-live"
	Version = "1.0.0"
)

// TypeURLPrefix prefixes the type URL of every extension (§6.2).
const TypeURLPrefix = "type.dql.dev/"

// Options carries what the mapping needs from config (§1.3).
type Options struct {
	// Catalog names the catalog; the Snapshot does not hold the database's name.
	Catalog string
	// DefaultNamespace is config's default-namespace. Empty means public,
	// Postgres's own (§4.3).
	DefaultNamespace string
}

// Map builds the catalog of s. Diagnostics report annotations that cannot be
// read, sorted (§3.2 rule 5); an error means the Snapshot holds text that
// does not parse as what Postgres prints, such as a check constraint or a
// type, or text that is not valid UTF-8.
func Map(s *introspect.Snapshot, opts Options) (*irv1.Catalog, []*irv1.Diagnostic, error) {
	s = canonical(s)
	m := newMapper(s, opts)
	c := m.catalog(s)
	if m.err == nil {
		c.Provenance, m.err = provenance(c)
	}
	if m.err != nil {
		return nil, nil, m.err
	}
	sortDiagnostics(m.diags)
	return c, m.diags, nil
}

// mapper holds the state of one Map call. Its first error sticks: later steps
// still run on zero values, and Map returns the error.
type mapper struct {
	opts    Options
	types   pgsql.Types
	domains map[objectKey]introspect.Domain
	diags   []*irv1.Diagnostic
	err     error
}

// objectKey identifies an object in a schema.
type objectKey struct{ schema, name string }

func newMapper(s *introspect.Snapshot, opts Options) *mapper {
	m := &mapper{opts: opts, domains: map[objectKey]introspect.Domain{}}
	for _, d := range s.Domains {
		m.domains[objectKey{d.Schema, d.Name}] = d
	}
	m.types = pgsql.Types{Catalog: opts.Catalog, Resolve: resolver(s)}
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

func (m *mapper) catalog(s *introspect.Snapshot) *irv1.Catalog {
	c := &irv1.Catalog{Name: ident(m.opts.Catalog), DefaultNamespace: cmp.Or(m.opts.DefaultNamespace, "public")}
	b := &namespaces{m: m, byName: map[string]*namespace{}}
	for _, sc := range s.Schemas {
		n := b.get(sc.Name)
		n.ns.Annotations = m.annotations(m.qn(sc.Name, "", ""), "COMMENT ON SCHEMA "+sc.Name, sc.Comment)
		n.exts.comment(sc.Comment)
		n.exts.owner(sc.Owner)
		n.exts.grants(sc.Grants)
	}
	for _, t := range m.tables(s) {
		n := b.get(t.schema)
		n.ns.Tables = append(n.ns.Tables, t.table)
	}
	for _, e := range s.Enums {
		n := b.get(e.Schema)
		n.ns.Enums = append(n.ns.Enums, m.enum(e))
	}
	for _, d := range s.Domains {
		n := b.get(d.Schema)
		n.ns.Domains = append(n.ns.Domains, m.domain(d))
	}
	for _, ct := range s.Composites {
		n := b.get(ct.Schema)
		n.ns.Composites = append(n.ns.Composites, m.composite(ct))
	}
	for _, sq := range s.Sequences {
		n := b.get(sq.Schema)
		n.ns.Sequences = append(n.ns.Sequences, m.sequence(sq))
	}
	public, hidden := m.routines(s.Routines)
	for _, r := range public {
		n := b.get(r.schema)
		n.ns.Routines = append(n.ns.Routines, r.routine)
	}
	for _, r := range hidden {
		b.get(r.Schema).exts.add(m.hiddenRoutine(r))
	}
	for _, r := range s.Ranges {
		b.get(r.Schema).exts.add(m.rangeType(r))
	}
	for _, bt := range s.BaseTypes {
		b.get(bt.Schema).exts.add(m.baseType(bt))
	}
	for _, co := range s.Collations {
		b.get(co.Schema).exts.add(m.collationDef(co))
	}
	for _, st := range s.Statistics {
		b.get(st.Schema).exts.add(m.statistics(st))
	}
	for _, n := range b.all {
		n.ns.Extensions = n.exts.list()
		c.Namespaces = append(c.Namespaces, n.ns)
	}
	slices.SortFunc(c.Namespaces, func(a, b *irv1.Namespace) int { return strings.Compare(a.Name.Stored, b.Name.Stored) })

	e := &exts{m: m}
	db := s.Database
	e.add(&pgextv1.Database{Encoding: db.Encoding, LocaleProvider: localeProviders[db.LocaleProvider],
		Collate: db.Collate, Ctype: db.Ctype, IcuLocale: db.ICULocale, IcuRules: db.ICURules})
	for _, x := range s.Extensions {
		e.add(&pgextv1.InstalledExtension{Name: x.Name, Version: x.Version, Namespace: x.Schema})
	}
	c.Extensions = e.list()
	return c
}

// namespaces builds the namespaces of a catalog. Objects arrive sorted
// (canonical), so each namespace's lists come out sorted by stored name.
type namespaces struct {
	m      *mapper
	byName map[string]*namespace
	all    []*namespace
}

type namespace struct {
	ns   *irv1.Namespace
	exts *exts
}

// get returns the namespace named schema, created on first use.
func (b *namespaces) get(schema string) *namespace {
	n, ok := b.byName[schema]
	if !ok {
		n = &namespace{ns: &irv1.Namespace{Name: ident(schema)}, exts: &exts{m: b.m}}
		b.byName[schema] = n
		b.all = append(b.all, n)
	}
	return n
}

// localeProviders spells datlocprovider and collprovider. Postgres 17 added
// the builtin provider.
var localeProviders = map[string]string{"b": "builtin", "c": "libc", "i": "icu", "d": "default"}

// provenance names the source and fingerprints the catalog it read: a hash
// of the catalog's deterministic encoding, which covers everything read and
// nothing about when (§3.1, §3.2 rule 3).
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

// comment keeps a comment's raw text (§7.5 step 2).
func (e *exts) comment(text string) {
	if text != "" {
		e.add(&pgextv1.Comment{Text: text})
	}
}

// owner records an owning role.
func (e *exts) owner(role string) {
	if role != "" {
		e.add(&pgextv1.Owner{Role: role})
	}
}

// member records the installed extension an object belongs to.
func (e *exts) member(extension string) {
	if extension != "" {
		e.add(&pgextv1.ExtensionMember{Extension: extension})
	}
}

func (e *exts) grants(gs []introspect.Grant) {
	for _, g := range e.m.grants(gs) {
		e.add(g)
	}
}

// list returns the extensions sorted by type URL, keeping the order in which
// those of one type were added.
func (e *exts) list() []*anypb.Any {
	slices.SortStableFunc(e.all, func(a, b *anypb.Any) int { return strings.Compare(a.TypeUrl, b.TypeUrl) })
	return e.all
}
