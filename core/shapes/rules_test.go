package shapes

import (
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func ann(keys ...string) []*irv1.Annotation {
	var out []*irv1.Annotation
	for _, k := range keys {
		out = append(out, &irv1.Annotation{Key: k})
	}
	return out
}

func column(name string, nullable bool, keys ...string) *irv1.Column {
	return &irv1.Column{Name: &irv1.Identifier{Stored: name, Words: []string{name}}, Nullable: nullable, Annotations: ann(keys...)}
}

func domainType(object string) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Named{Named: &irv1.NamedRef{
		Kind: &irv1.NamedRef_Domain{Domain: &irv1.QualifiedName{Catalog: "c", Namespace: "n", Object: object}},
	}}}
}

func check(name string, columns ...string) *irv1.Constraint {
	return &irv1.Constraint{Name: name, Columns: columns, ClientValidated: true}
}

func domain(name string, base *irv1.Type, constraints ...*irv1.Constraint) *irv1.Domain {
	return &irv1.Domain{Name: &irv1.Identifier{Stored: name}, Base: base, Constraints: constraints}
}

// catalog wraps tables and domains in catalog c, namespace n.
func catalog(tables []*irv1.Table, domains ...*irv1.Domain) []*irv1.Catalog {
	return []*irv1.Catalog{{
		Name: &irv1.Identifier{Stored: "c"},
		Namespaces: []*irv1.Namespace{{
			Name: &irv1.Identifier{Stored: "n"}, Tables: tables, Domains: domains,
		}},
	}}
}

func entity(t *irv1.Table) Entity {
	return Entity{Table: t, Name: &irv1.QualifiedName{Catalog: "c", Namespace: "n", Object: t.GetName().GetStored()}}
}

// TestAnnotationRules covers the rows of §14.2 and §21.3 that users lacks,
// and the class rule of a plain non-null column made optional in a Draft.
func TestAnnotationRules(t *testing.T) {
	updated := column("touched", false)
	updated.Default = &irv1.Default{Class: irv1.DefaultClass_DEFAULT_EXPRESSION, OnUpdate: true}
	tb := &irv1.Table{
		Name: &irv1.Identifier{Stored: "things", Words: []string{"things"}},
		Kind: irv1.TableKind_RELATION_KIND_TABLE,
		Columns: []*irv1.Column{
			column("tenant", false, keyBound),
			column("code", false),
			column("computed", false, keyReadOnly),
			column("deleted_at", true, keySoftDelete),
			column("filled", false, keyDBFilled),
			column("rev", false, keyVersion),
			updated,
		},
		Uniqueness: []*irv1.Uniqueness{
			{Name: "things_pkey", Kind: irv1.UniquenessKind_UNIQUENESS_PRIMARY_KEY, Columns: []string{"tenant", "code"}},
		},
	}
	cs := catalog([]*irv1.Table{tb})
	e := entity(tb)
	cases := map[irv1.Preset]string{
		irv1.Preset_PRESET_FULL: `tenant REQUIRED
code REQUIRED
computed REQUIRED
deleted_at REQUIRED nullable
filled REQUIRED
rev REQUIRED
touched REQUIRED
`,
		irv1.Preset_PRESET_DRAFT: `code REQUIRED VALUE_OR_EXPRESSION
filled OPTIONAL VALUE_OR_EXPRESSION
rev OPTIONAL VALUE_OR_EXPRESSION
touched OPTIONAL VALUE_OR_EXPRESSION
tenant FORBIDDEN_BOUND
computed FORBIDDEN_READ_ONLY
deleted_at FORBIDDEN_SOFT_DELETE
`,
		irv1.Preset_PRESET_PARTIAL: `filled OPTIONAL VALUE_OR_EXPRESSION
tenant FORBIDDEN_BOUND
code FORBIDDEN_KEY
computed FORBIDDEN_READ_ONLY
deleted_at FORBIDDEN_SOFT_DELETE
rev FORBIDDEN_VERSION
touched FORBIDDEN_DB_UPDATED
`,
	}
	for p, want := range cases {
		if got := summary(Flat(cs, e, p)); got != want {
			t.Errorf("%s:\n got %s\nwant %s", p, got, want)
		}
	}
	if got := summary(Key(e)); got != "tenant REQUIRED\ncode REQUIRED\nrev REQUIRED EXPECTED_VERSION\n" {
		t.Errorf("key:\n%s", got)
	}
	// Without a name annotation the entity's own words name its shapes.
	if got := Flat(cs, e, irv1.Preset_PRESET_DRAFT).GetHostName().GetWords(); len(got) != 2 || got[0] != "things" || got[1] != "draft" {
		t.Errorf("host name %v", got)
	}

	ups := Upserts(cs)
	if len(ups) != 1 {
		t.Fatalf("upserts %v", ups)
	}
	want := []string{"tenant EXCLUDE_BOUND", "code EXCLUDE_CONFLICT_KEY", "rev EXCLUDE_VERSION", "touched EXCLUDE_DB_UPDATED"}
	got := ups[0].GetDefaultUpdateExcludes()
	if len(got) != len(want) {
		t.Fatalf("excludes %v", got)
	}
	for i, x := range got {
		if s := x.GetColumn() + " " + x.GetReason().String(); s != want[i] {
			t.Errorf("exclude %d: %s, want %s", i, s, want[i])
		}
	}
}

// TestKeyless checks that an entity without a key gets no Key and no Scope,
// and that every column of its Partial stays optional (§11.1).
func TestKeyless(t *testing.T) {
	tb := &irv1.Table{
		Name:    &irv1.Identifier{Stored: "log", Words: []string{"log"}},
		Kind:    irv1.TableKind_RELATION_KIND_TABLE,
		Columns: []*irv1.Column{column("line", false)},
	}
	shapes := Presets(catalog([]*irv1.Table{tb}))
	if len(shapes) != 3 {
		t.Fatalf("presets %v", shapes)
	}
	if got := summary(shapes[2]); got != "line OPTIONAL VALUE_OR_EXPRESSION\n" {
		t.Errorf("partial:\n%s", got)
	}
}

// TestDomainValidation checks that a field validates against every domain
// along its type, outermost first, and stops at a missing or repeated domain.
func TestDomainValidation(t *testing.T) {
	outer := column("outer", false)
	outer.Type = domainType("outer_d")
	looped := column("looped", false)
	looped.Type = domainType("loop_d")
	missing := column("missing", false)
	missing.Type = domainType("nowhere")
	tb := &irv1.Table{
		Name:        &irv1.Identifier{Stored: "t", Words: []string{"t"}},
		Kind:        irv1.TableKind_RELATION_KIND_TABLE,
		Columns:     []*irv1.Column{outer, looped, missing},
		Constraints: []*irv1.Constraint{check("t_pair", "outer", "looped"), check("t_outer", "outer"), {Name: "t_opaque", Columns: []string{"outer"}}},
	}
	cs := catalog([]*irv1.Table{tb},
		domain("outer_d", domainType("inner_d"), check("outer_check")),
		domain("inner_d", nil, check("inner_check"), &irv1.Constraint{Name: "inner_opaque"}),
		domain("loop_d", domainType("loop_d"), check("loop_check")),
	)
	got := summary(Flat(cs, entity(tb), irv1.Preset_PRESET_DRAFT))
	want := `outer REQUIRED VALUE_OR_EXPRESSION validate(outer_check,inner_check,t_outer)
looped REQUIRED VALUE_OR_EXPRESSION validate(loop_check)
missing REQUIRED VALUE_OR_EXPRESSION
`
	if got != want {
		t.Errorf("draft:\n got %s\nwant %s", got, want)
	}
}

func TestSpelling(t *testing.T) {
	user := &irv1.QualifiedName{Catalog: "shop", Namespace: "app", Object: "users"}
	cases := []struct {
		id   *irv1.ShapeId
		want string
	}{
		{&irv1.ShapeId{Base: &irv1.ShapeId_Custom{Custom: "user_summary"}}, "user_summary"},
		{&irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: user}, Preset: irv1.Preset_PRESET_FULL, Form: irv1.Form_FORM_NESTED}, "user.full.nested"},
		{&irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: user}, Preset: irv1.Preset_PRESET_DRAFT, Form: irv1.Form_FORM_NESTS}, "user.draft.nests"},
		{&irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: user}, Preset: irv1.Preset_PRESET_SCOPE}, "user.scope"},
		{&irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: user}, Preset: irv1.Preset_PRESET_DRAFT, Form: irv1.Form_FORM_FLAT, ForRelation: "author"}, "post_tag.draft.flat.for.author"},
	}
	for i, c := range cases {
		words := []string{"user"}
		if i == len(cases)-1 {
			words = []string{"post", "tag"}
		}
		if got := Spelling(c.id, words); got != c.want {
			t.Errorf("%d: %s, want %s", i, got, c.want)
		}
	}
}
