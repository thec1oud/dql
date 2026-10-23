package mapping

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	pgextv1 "github.com/thec1oud/dql/proto/dql/ext/postgres/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/pglive/internal/introspect"
)

// The cases here are Snapshots no test database produces, written by hand.

func builtin(name, formatted string) introspect.TypeRef {
	return introspect.TypeRef{Schema: "pg_catalog", Name: name, Kind: "b", Typmod: -1, Formatted: formatted}
}

var (
	int4 = builtin("int4", "integer")
	text = builtin("text", "text")
	void = introspect.TypeRef{Schema: "pg_catalog", Name: "void", Kind: "p", Typmod: -1, Formatted: "void"}
)

func TestDiagnostics(t *testing.T) {
	s := &introspect.Snapshot{
		Schemas: []introspect.Schema{{Name: "s", Comment: "@name(x @x.go.type(int)"}},
		Relations: []introspect.Relation{{
			Schema: "s", Name: "t", Kind: "r",
			Columns: []introspect.Column{{Name: "a", Type: int4, NotNull: true, Comment: "@x.go.type(int64) @immutable"}},
			Constraints: []introspect.Constraint{
				{Name: "t_pkey", Kind: "p", Columns: []string{"a"}, Index: "t_pkey", Comment: "@name(k)"},
				{Name: "t_trigger", Kind: "t"},
			},
			Indexes: []introspect.Index{
				{Name: "t_pkey", Unique: true, Primary: true, Constraint: "t_pkey", Keys: []introspect.IndexKey{{Column: "a"}}},
				{Name: "t_lower", Unique: true, Comment: "@x", Keys: []introspect.IndexKey{
					{Expression: "lower(b)"}, {Column: "a"}, {Column: "c", Included: true}}},
				{Name: "t_plain", Comment: "@y @z"},
			},
			Triggers: []introspect.Trigger{{Name: "tr", Comment: "@db-filled"}},
			Policies: []introspect.Policy{{Name: "po", Command: "w", Comment: "@p"}},
		}},
		Ranges:     []introspect.Range{{Schema: "s", Name: "r", Subtype: int4, Comment: "@r"}},
		BaseTypes:  []introspect.BaseType{{Schema: "s", Name: "b", Comment: "@b"}},
		Collations: []introspect.Collation{{CollationRef: introspect.CollationRef{Schema: "s", Name: "co"}, Comment: "@c"}},
		Statistics: []introspect.Statistics{{Schema: "s", Name: "st", Comment: "@st"}},
		Routines: []introspect.Routine{
			{Schema: "s", Name: "agg", Kind: "a", Comment: "@public", Arguments: []introspect.Argument{{Mode: "i", Type: int4}}},
			{Schema: "s", Name: "f", Kind: "f", Comment: "@public(", Returns: &void},
		},
	}
	c, ds, err := Map(s, Options{Catalog: "c"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range ds {
		q := d.GetTarget()
		got = append(got, d.GetCode()+" "+q.GetNamespace()+"."+q.GetObject()+"."+q.GetMember()+": "+d.GetMessage())
	}
	want := []string{
		"pglive-annotation-unclosed s..: COMMENT ON SCHEMA s: the value of @name has no closing parenthesis, so it is not read; close it",
		"pglive-annotation-ignored s.agg.: COMMENT ON AGGREGATE s.agg(integer): the routine is not exposed, so pg-live reads no annotations on it, so @public is ignored; remove the annotation tokens from the comment",
		"pglive-annotation-ignored s.b.: COMMENT ON TYPE s.b: pg-live reads no annotations on base types, so @b is ignored; remove the annotation tokens from the comment",
		"pglive-annotation-ignored s.co.: COMMENT ON COLLATION s.co: pg-live reads no annotations on collations, so @c is ignored; remove the annotation tokens from the comment",
		"pglive-annotation-ignored s.f.: COMMENT ON FUNCTION s.f(): the routine is not exposed, so pg-live reads no annotations on it, so @public is ignored; remove the annotation tokens from the comment",
		"pglive-annotation-ignored s.r.: COMMENT ON TYPE s.r: pg-live reads no annotations on range types, so @r is ignored; remove the annotation tokens from the comment",
		"pglive-annotation-ignored s.st.: COMMENT ON STATISTICS s.st: pg-live reads no annotations on statistics objects, so @st is ignored; remove the annotation tokens from the comment",
		"pglive-annotation-ignored s.t.po: COMMENT ON POLICY po ON s.t: pg-live reads no annotations on policies, so @p is ignored; remove the annotation tokens from the comment",
		"pglive-annotation-ignored s.t.t_lower: COMMENT ON INDEX s.t_lower: pg-live reads no annotations on indexes, so @x is ignored; remove the annotation tokens from the comment",
		"pglive-annotation-ignored s.t.t_pkey: COMMENT ON CONSTRAINT t_pkey ON s.t: pg-live reads no annotations on primary keys and unique constraints, so @name is ignored; remove the annotation tokens from the comment",
		"pglive-annotation-ignored s.t.t_plain: COMMENT ON INDEX s.t_plain: pg-live reads no annotations on indexes, so @y @z is ignored; remove the annotation tokens from the comment",
		"pglive-annotation-ignored s.t.tr: COMMENT ON TRIGGER tr ON s.t: pg-live reads no annotations on triggers, so @db-filled is ignored; remove the annotation tokens from the comment",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("diagnostics:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// The schema's unclosed annotation is dropped; the one after it is part
	// of its value. Plugin keys keep their spelling, and values stay as written.
	ns := c.Namespaces[0]
	if len(ns.Annotations) != 0 {
		t.Errorf("schema annotations: %v", ns.Annotations)
	}
	col := ns.Tables[0].Columns[0]
	if len(col.Annotations) != 2 || col.Annotations[0].Key != "core.immutable" ||
		col.Annotations[1].Key != "x.go.type" || col.Annotations[1].Value.GetString_() != "int64" {
		t.Errorf("column annotations: %v", col.Annotations)
	}
	u := ns.Tables[0].Uniqueness[0]
	if u.Name != "t_lower" || !proto.Equal(&irv1.Uniqueness{Name: u.Name, Kind: u.Kind, Columns: u.Columns,
		Expressions: u.Expressions, NullsDistinct: u.NullsDistinct}, &irv1.Uniqueness{
		Name: "t_lower", Kind: irv1.UniquenessKind_UNIQUENESS_INDEX, Columns: []string{"a"},
		Expressions: []*irv1.IndexExpression{{Position: 0, Native: "lower(b)"}}, NullsDistinct: true}) {
		t.Errorf("expression index: %v", u)
	}
	if len(ns.Routines) != 0 {
		t.Errorf("an aggregate and an unclosed @public are not public: %v", ns.Routines)
	}
}

func TestRoutineResults(t *testing.T) {
	setof := int4
	public := func(name string, r introspect.Routine) introspect.Routine {
		r.Schema, r.Name, r.Kind, r.Comment, r.Volatility = "s", name, "f", "@public", "v"
		return r
	}
	s := &introspect.Snapshot{Routines: []introspect.Routine{
		public("nothing", introspect.Routine{Returns: &void}),
		public("rows", introspect.Routine{Returns: &setof, ReturnsSet: true, Arguments: []introspect.Argument{{Mode: "i", Type: int4}}}),
		public("table", introspect.Routine{ReturnsSet: true, Returns: &introspect.TypeRef{Schema: "pg_catalog", Name: "record", Typmod: -1},
			Arguments: []introspect.Argument{{Mode: "t", Name: "x", Type: text}}}),
	}}
	c, _ := mapped(t, s, Options{Catalog: "c"})
	rs := c.Namespaces[0].Routines
	if rs[0].Overloads[0].Returns.GetNothing() == nil {
		t.Errorf("void: %v", rs[0].Overloads[0].Returns)
	}
	if o := rs[1].Overloads[0]; o.Returns.GetSetOf().GetType().GetInt32() == nil || !o.Returns.GetSetOf().Nullable ||
		o.Arguments[0].Name != nil {
		t.Errorf("setof: %v", o)
	}
	if cols := rs[2].Overloads[0].Returns.GetTable().GetColumns(); len(cols) != 1 || cols[0].Name.Stored != "x" ||
		!cols[0].Nullable || len(rs[2].Overloads[0].Arguments) != 0 {
		t.Errorf("table: %v", rs[2].Overloads[0])
	}
}

func TestObjectsOutsideTheSchemas(t *testing.T) {
	domainRef := introspect.TypeRef{Schema: "elsewhere", Name: "d", Kind: "d", Typmod: -1, Formatted: "elsewhere.d"}
	vector := introspect.TypeRef{Schema: "ext", Name: "vector", Kind: "b", Typmod: 3, Formatted: "ext.vector(3)"}
	s := &introspect.Snapshot{
		Relations: []introspect.Relation{{
			Schema: "s", Name: "part", Kind: "r", Extension: "x", Tablespace: "fast",
			PartitionOf:    &introspect.ObjectName{Schema: "elsewhere", Name: "parent"},
			PartitionBound: "DEFAULT",
			Columns: []introspect.Column{
				{Name: "d", Type: domainRef},
				{Name: "v", Type: vector},
				{Name: "ints", Type: introspect.TypeRef{Schema: "pg_catalog", Name: "_int4", Kind: "b", Category: "A",
					Typmod: -1, Formatted: "integer[]", Element: &introspect.ElementRef{Schema: "pg_catalog", Name: "int4", Kind: "b"}}},
				{Name: "chars", Type: introspect.TypeRef{Schema: "pg_catalog", Name: "_bpchar", Kind: "b", Category: "A",
					Typmod: 6, Formatted: "character(2)[]", Element: &introspect.ElementRef{Schema: "pg_catalog", Name: "bpchar", Kind: "b"}},
					Collation: &introspect.CollationRef{Schema: "pg_catalog", Name: "C", Provider: "c", Deterministic: true}},
			},
		}},
		Ranges: []introspect.Range{{Schema: "s", Name: "r", Subtype: text, Multirange: introspect.ObjectName{Schema: "s", Name: "mr"},
			Collation: &introspect.CollationRef{Schema: "pg_catalog", Name: "C"}}},
		Enums: []introspect.Enum{{Schema: "s", Name: "e", Extension: "x"}},
	}
	c, _ := mapped(t, s, Options{Catalog: "c"})
	ns := c.Namespaces[0]
	if ns.Name.Stored != "s" {
		t.Fatalf("namespace %v", ns.Name)
	}
	tb := ns.Tables[0]
	if tb.Columns[0].Type.GetOpaque() == nil || tb.Columns[1].Type.GetNative().GetModifiers()[0].GetInt() != 3 {
		t.Errorf("types outside the schemas read are opaque: %v %v", tb.Columns[0].Type, tb.Columns[1].Type)
	}
	if a := tb.Columns[2].Type.GetArray(); a.GetElement().GetInt32() == nil || a.Dimensions != 1 {
		t.Errorf("array: %v", tb.Columns[2].Type)
	}
	if co := tb.Columns[3].Collation; co.Name != "C" || co.PadSpace != irv1.Tristate_TRISTATE_TRUE ||
		co.ClientFold != irv1.ClientFold_CLIENT_FOLD_UNSPECIFIED || tb.Columns[3].Type.GetArray().GetElement().GetChar().GetLength() != 2 {
		t.Errorf("char(2)[]: %v %v", co, tb.Columns[3].Type)
	}
	want := map[string]proto.Message{
		"PartitionOf": &pgextv1.PartitionOf{
			Parent: &irv1.QualifiedName{Catalog: "c", Namespace: "elsewhere", Object: "parent"}, Bound: "DEFAULT"},
		"ExtensionMember": &pgextv1.ExtensionMember{Extension: "x"},
		"Tablespace":      &pgextv1.Tablespace{Name: "fast"},
	}
	for _, a := range tb.Extensions {
		m, _ := a.UnmarshalNew()
		name := string(m.ProtoReflect().Descriptor().Name())
		if w, ok := want[name]; ok && !proto.Equal(m, w) {
			t.Errorf("%s: %v", name, m)
		}
		delete(want, name)
	}
	if len(want) > 0 {
		t.Errorf("missing extensions: %v", want)
	}
	if len(ns.Enums[0].Extensions) != 1 {
		t.Errorf("enum extensions: %v", ns.Enums[0].Extensions)
	}
	r := extension(t, ns.Extensions, "r").(*pgextv1.RangeType)
	if r.Collation != "pg_catalog.C" || r.Multirange != "mr" {
		t.Errorf("range: %v", r)
	}
}

func TestCollationFacts(t *testing.T) {
	icu := func(locale string) *introspect.CollationRef {
		return &introspect.CollationRef{Schema: "s", Name: "x", Provider: "i", ICULocale: locale}
	}
	u, f, tr := irv1.Tristate_TRISTATE_UNSPECIFIED, irv1.Tristate_TRISTATE_FALSE, irv1.Tristate_TRISTATE_TRUE
	for _, c := range []struct {
		coll        *introspect.CollationRef
		cs, as, det irv1.Tristate
	}{
		{icu("und-u-ks-level1"), f, f, f},
		{icu("und-u-ks-level2"), f, tr, f},
		{icu("und-u-ks-level2-kc"), u, tr, f},
		{icu("und-u-kc-false-ks-level2"), f, tr, f},
		{icu("und-u-ks-level3"), u, u, f},
		{icu("und-u-ks"), u, u, f},
		{&introspect.CollationRef{Schema: "s", Name: "x", Provider: "c"}, u, u, f},
	} {
		m := newMapper(&introspect.Snapshot{}, Options{})
		got := m.collation(c.coll, text)
		if got.CaseSensitive != c.cs || got.AccentSensitive != c.as || got.Deterministic != c.det || got.Name != "s.x" {
			t.Errorf("%+v: %v", c.coll, got)
		}
	}
	if newMapper(&introspect.Snapshot{}, Options{}).collation(nil, text) != nil {
		t.Error("no collation, no facts")
	}
}

func TestErrors(t *testing.T) {
	table := func(cols []introspect.Column, cons ...introspect.Constraint) *introspect.Snapshot {
		return &introspect.Snapshot{Relations: []introspect.Relation{{Schema: "s", Name: "t", Kind: "r",
			Columns: cols, Constraints: cons}}}
	}
	col := []introspect.Column{{Name: "a", Type: int4}}
	for _, c := range []struct {
		name string
		s    *introspect.Snapshot
		want string
	}{
		{"check", table(col, introspect.Constraint{Name: "k", Kind: "c", Definition: "CHECK ("}),
			"mapping: check constraint of s.t"},
		{"default", table([]introspect.Column{{Name: "a", Type: int4, Default: "now("}}),
			"mapping: default of column s.t.a"},
		{"type modifiers", table([]introspect.Column{{Name: "a", Type: introspect.TypeRef{
			Schema: "ext", Name: "odd", Typmod: 1, Formatted: "ext.odd("}}}),
			`mapping: type "ext.odd(" of column s.t.a`},
		{"built-in modifiers", table([]introspect.Column{{Name: "a", Type: introspect.TypeRef{
			Schema: "pg_catalog", Name: "int4", Typmod: 4, Formatted: "integer(4)"}}}),
			`mapping: type "integer(4)" of column s.t.a`},
		{"domain check", &introspect.Snapshot{Domains: []introspect.Domain{{Schema: "s", Name: "d", Base: text,
			Checks: []introspect.DomainCheck{{Name: "k", Definition: "CHECK ("}}}}},
			"mapping: check constraint k of domain s.d"},
		{"domain default", &introspect.Snapshot{Domains: []introspect.Domain{{Schema: "s", Name: "d", Base: text, Default: "("}}},
			"mapping: default of domain s.d"},
		{"privilege", &introspect.Snapshot{Schemas: []introspect.Schema{{Name: "s",
			Grants: []introspect.Grant{{Grantee: "r", Privilege: "FLY"}}}}},
			`mapping: unknown privilege "FLY" granted to r`},
		{"invalid UTF-8 in an extension", &introspect.Snapshot{Schemas: []introspect.Schema{{Name: "s", Comment: "\xff"}}},
			"mapping: "},
		{"invalid UTF-8 in a name", &introspect.Snapshot{Schemas: []introspect.Schema{{Name: "\xff"}}},
			"mapping: fingerprinting catalog c"},
	} {
		_, _, err := Map(c.s, Options{Catalog: "c"})
		if err == nil || !strings.HasPrefix(err.Error(), c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

// TestCanonical sorts every list that has no database order, whatever order
// the Snapshot lists it in.
func TestCanonical(t *testing.T) {
	two := func(a, b string) (string, string) { return b, a }
	z, a := two("a", "z")
	s := &introspect.Snapshot{
		Collations: []introspect.Collation{{CollationRef: introspect.CollationRef{Name: z}}, {CollationRef: introspect.CollationRef{Name: a}}},
		Relations:  []introspect.Relation{{Policies: []introspect.Policy{{Name: z}, {Name: a}}}},
		Enums:      []introspect.Enum{{Name: z}, {Name: a}},
		Composites: []introspect.Composite{{Name: z}, {Name: a}},
		Ranges:     []introspect.Range{{Name: z}, {Name: a}},
		BaseTypes:  []introspect.BaseType{{Name: z}, {Name: a}},
	}
	c := canonical(s)
	for what, first := range map[string]string{
		"collations": c.Collations[0].Name,
		"policies":   c.Relations[0].Policies[0].Name,
		"enums":      c.Enums[0].Name,
		"composites": c.Composites[0].Name,
		"ranges":     c.Ranges[0].Name,
		"base types": c.BaseTypes[0].Name,
	} {
		if first != "a" {
			t.Errorf("%s are not sorted", what)
		}
	}
	if s.Composites[0].Name != "z" {
		t.Error("canonical changed its input")
	}
}
