package conversions

import (
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/testkit/determinism"
)

func userBundle(extra ...*irv1.Shape) *irv1.Bundle {
	shapes := append(append(userShapes(), postDraftShapes()...), extra...)
	return bundleOf([]*irv1.Table{users(), posts()}, []*irv1.Domain{emailDomain()}, shapes...)
}

func derive(t *testing.T, b *irv1.Bundle, declared ...Declared) []string {
	t.Helper()
	if ds := Derive(b, declared); len(ds) != 0 {
		t.Fatalf("unexpected diagnostics: %v", ds)
	}
	return rows(spelled(b), b.GetShapes().GetConversions())
}

func assertLines(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("got:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// The user table of §14.9, complete: every conversion between user's presets
// and forms and along the lineage of user_summary and user_summary_named,
// with post's Nested Draft to Flat Draft (§14.5).
func TestUserConversionTable(t *testing.T) {
	assertLines(t, derive(t, userBundle()), []string{
		"post.draft.nested -> post.draft.flat VIA_DATABASE PRESET",
		"user.draft.flat -> user.full.flat VIA_DATABASE PRESET",
		"user.draft.flat -> user.partial.flat TOTAL PRESET",
		"user.draft.nested -> user.draft.flat TOTAL PRESET",
		"user.draft.nested -> user.draft.nests TOTAL PRESET",
		"user.full.flat -> user.draft.flat NEEDS_INPUTS PRESET password_hash(WRITE_ONLY)",
		"user.full.flat -> user.key TOTAL PRESET",
		"user.full.flat -> user.partial.flat TOTAL PRESET",
		"user.full.flat -> user_summary TOTAL LINEAGE",
		"user.full.flat -> user_summary_named FALLIBLE LINEAGE",
		"user.full.nested -> user.full.flat TOTAL PRESET",
		"user.full.nested -> user.full.nests TOTAL PRESET",
		"user.partial.flat -> user.draft.flat WITH_BASE PRESET",
		"user.partial.flat -> user.full.flat WITH_BASE PRESET",
		"user.partial.nested -> user.partial.flat TOTAL PRESET",
		"user.partial.nested -> user.partial.nests TOTAL PRESET",
		"user_summary -> user.full.flat IMPOSSIBLE LINEAGE",
		"user_summary -> user_summary_named FALLIBLE LINEAGE",
		"user_summary_named -> user.full.flat IMPOSSIBLE LINEAGE",
		"user_summary_named -> user_summary TOTAL LINEAGE",
	})
}

// quick_invite binds password_hash (§14.8), so filling a Draft from it takes
// password_hash as an input.
func TestBoundFieldIsAnInput(t *testing.T) {
	invite := shape(custom("quick_invite"), "users", []*irv1.LineageStep{
		presetStep("users", draft, flat),
		{Op: irv1.ShapeOp_PICK, Columns: []string{"email"}},
		{Op: irv1.ShapeOp_BIND, Columns: []string{"password_hash"}},
	}, field("email", emailType, "rx"))
	invite.Bind = []string{"password_hash"}
	invite.Forbidden = []*irv1.ForbiddenField{forbid("display_name", irv1.ForbiddenReason_FORBIDDEN_OMITTED)}
	got := derive(t, userBundle(invite))
	for _, want := range []string{
		"quick_invite -> user.draft.flat NEEDS_INPUTS LINEAGE password_hash(OMITTED)",
		"user.draft.flat -> quick_invite TOTAL LINEAGE",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %q in:\n  %s", want, strings.Join(got, "\n  "))
		}
	}
}

// A declared conversion joins the table unless a preset or lineage
// conversion already holds the pair.
func TestDeclaredConversions(t *testing.T) {
	got := derive(t, userBundle(),
		Declared{From: custom("user_summary"), To: preset("users", draft, flat)},
		Declared{From: preset("users", full, flat), To: preset("users", keyP, noForm)},
	)
	for _, want := range []string{
		"user_summary -> user.draft.flat NEEDS_INPUTS DECLARED password_hash(WRITE_ONLY)",
		"user.full.flat -> user.key TOTAL PRESET",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %q in:\n  %s", want, strings.Join(got, "\n  "))
		}
	}
}

func TestDeclaredConversionErrors(t *testing.T) {
	b := userBundle()
	ds := Derive(b, []Declared{
		{From: preset("users", keyP, noForm), To: preset("users", full, flat)},
		{From: custom("nope"), To: preset("ghosts", full, flat)},
		{From: preset("users", full, flat), To: preset("users", full, nestedF)},
	})
	var got []string
	for _, d := range ds {
		got = append(got, strings.Join([]string{d.GetSeverity().String(), d.GetCode(), d.GetTarget().GetObject(), d.GetShape(), d.GetMessage()}, " | "))
	}
	assertLines(t, got, []string{
		"SEVERITY_ERROR | conversion-shape-unknown |  | nope | conversion from nope to ghosts.full.flat names shape nope, which does not exist; name a preset such as user.full or a shape declared under shapes:",
		"SEVERITY_ERROR | conversion-shape-unknown | ghosts | ghosts.full.flat | conversion from nope to ghosts.full.flat names shape ghosts.full.flat, which does not exist; name a preset such as user.full or a shape declared under shapes:",
		"SEVERITY_ERROR | conversion-impossible | users | user.full.flat | conversion from user.full.flat to user.full.nested is impossible: user.full.flat cannot supply posts_to_author; remove it from conversions:",
		"SEVERITY_ERROR | conversion-impossible | users | user.key | conversion from user.key to user.full.flat is impossible: user.key cannot supply tenant_id, email, display_name, created_at, updated_at; remove it from conversions:",
	})
	for _, c := range rows(spelled(b), b.GetShapes().GetConversions()) {
		if strings.Contains(c, "DECLARED") {
			t.Errorf("a failed declaration joined the table: %s", c)
		}
	}
}

// The kinds the running example does not reach, one rule at a time.
func TestClassify(t *testing.T) {
	docs := table("docs", []string{"doc"}, []string{"id"},
		withDefault(col("id", i64Type), irv1.DefaultClass_DEFAULT_IDENTITY_ALWAYS),
		col("slug", textType),
		col("search", textType, "db-filled"),
		col("title", textType),
		col("body", textType),
	)
	docs.Columns[1].Generation = &irv1.Generation{Kind: irv1.GenerationKind_GENERATION_STORED}
	b := bundleOf([]*irv1.Table{users(), posts(), docs}, []*irv1.Domain{emailDomain()})
	d := spelled(b)
	docShape := func(p irv1.Preset, fields ...*irv1.Field) *irv1.Shape {
		return shape(custom("s"), "docs", []*irv1.LineageStep{presetStep("docs", p, flat)}, fields...)
	}
	draftDoc := docShape(draft, field("title", textType, "rx"), field("body", textType, "o?x"))
	cases := []struct {
		name    string
		a, b    *irv1.Shape
		kind    irv1.ConversionKind
		missing []string
	}{{
		name: "the database fills generated, db-filled and identity columns",
		a:    draftDoc,
		b:    docShape(full, field("id", i64Type, "r"), field("slug", textType, "r"), field("search", textType, "r"), field("title", textType, "r"), field("body", textType, "r?")),
		kind: irv1.ConversionKind_CONVERSION_VIA_DATABASE,
	}, {
		name: "no conversion fills one field through the database and another through the caller",
		a: withNests(withForbidden(shape(custom("s"), "posts", []*irv1.LineageStep{presetStep("posts", draft, nestedF)},
			field("author_id", i64Type, "ox")), forbid("title", irv1.ForbiddenReason_FORBIDDEN_OMITTED)),
			&irv1.Nest{Relation: "author", Supplies: []string{"author_id"}}),
		b:       shape(preset("posts", draft, flat), "posts", nil, field("author_id", i64Type, "rx"), field("title", textType, "rx")),
		kind:    irv1.ConversionKind_CONVERSION_IMPOSSIBLE,
		missing: []string{"author_id", "title"},
	}, {
		name: "the database stores a column the catalog does not list",
		a:    docShape(draft, field("ghost", textType, "rx")),
		b:    docShape(full, field("ghost", textType, "r")),
		kind: irv1.ConversionKind_CONVERSION_VIA_DATABASE,
	}, {
		name:    "a nullable Draft field cannot fill a non-null column of the row",
		a:       draftDoc,
		b:       docShape(full, field("title", textType, "r"), field("body", textType, "r")),
		kind:    irv1.ConversionKind_CONVERSION_IMPOSSIBLE,
		missing: []string{"body"},
	}, {
		name: "the database returns a Draft's key and expected version",
		a:    shape(preset("users", draft, flat), "users", nil, userDraftFields()...),
		b:    shape(preset("users", keyP, noForm), "users", nil, field("id", i64Type, "r"), field("version", i32Type, "r")),
		kind: irv1.ConversionKind_CONVERSION_VIA_DATABASE,
	}, {
		name:    "a field held in a modality that cannot fill a write shape is not an input",
		a:       shape(custom("s"), "docs", []*irv1.LineageStep{presetStep("docs", draft, flat)}, field("title", textType, "ox")),
		b:       withForbidden(docShape(draft, field("title", textType, "rx"), field("body", textType, "rx")), forbid("body", irv1.ForbiddenReason_FORBIDDEN_OMITTED)),
		kind:    irv1.ConversionKind_CONVERSION_IMPOSSIBLE,
		missing: []string{"title", "body"},
	}, {
		name:    "a column left out for a reason the caller cannot make up for is not an input",
		a:       withForbidden(docShape(full, field("title", textType, "r")), forbid("id", irv1.ForbiddenReason_FORBIDDEN_OMITTED), forbid("slug", irv1.ForbiddenReason_FORBIDDEN_GENERATED)),
		b:       docShape(draft, field("title", textType, "rx"), field("slug", textType, "rx")),
		kind:    irv1.ConversionKind_CONVERSION_IMPOSSIBLE,
		missing: []string{"slug"},
	}, {
		name:    "a narrowed field beside another gap is missing too",
		a:       docShape(full, field("title", textType, "r?")),
		b:       docShape(full, field("title", textType, "r"), field("body", textType, "r")),
		kind:    irv1.ConversionKind_CONVERSION_IMPOSSIBLE,
		missing: []string{"title", "body"},
	}, {
		name: "a Partial onto another entity's Full is no base",
		a:    shape(preset("users", partial, flat), "users", nil, userPartialFields()...),
		b:    docShape(full, field("title", textType, "r")),
		kind: irv1.ConversionKind_CONVERSION_IMPOSSIBLE, missing: []string{"title"},
	}, {
		name: "a shape with no lineage has no root preset",
		a:    shape(custom("bare"), "docs", nil, field("title", textType, "r")),
		b:    shape(custom("bare2"), "docs", nil, field("title", textType, "r")),
		kind: irv1.ConversionKind_CONVERSION_TOTAL,
	}, {
		name:    "an optional relation does not fill a required one",
		a:       withNests(docShape(full), &irv1.Nest{Relation: "author", Presence: irv1.Presence_PRESENCE_OPTIONAL, Target: &irv1.Nest_Shape{Shape: custom("x")}}),
		b:       withNests(docShape(full), &irv1.Nest{Relation: "author", Presence: irv1.Presence_PRESENCE_REQUIRED, Target: &irv1.Nest_Shape{Shape: custom("x")}}),
		kind:    irv1.ConversionKind_CONVERSION_IMPOSSIBLE,
		missing: []string{"author"},
	}, {
		name: "a non-null relation fills a nullable one",
		a:    withNests(docShape(full), &irv1.Nest{Relation: "author", Presence: irv1.Presence_PRESENCE_REQUIRED, Target: &irv1.Nest_Shape{Shape: custom("x")}}),
		b:    withNests(docShape(full), &irv1.Nest{Relation: "author", Presence: irv1.Presence_PRESENCE_REQUIRED, Nullable: true, Target: &irv1.Nest_Shape{Shape: custom("x")}}),
		kind: irv1.ConversionKind_CONVERSION_TOTAL,
	}, {
		name:    "a nullable relation does not fill a non-null one",
		a:       withNests(docShape(full), &irv1.Nest{Relation: "author", Presence: irv1.Presence_PRESENCE_REQUIRED, Nullable: true, Target: &irv1.Nest_Shape{Shape: custom("x")}}),
		b:       withNests(docShape(full), &irv1.Nest{Relation: "author", Presence: irv1.Presence_PRESENCE_REQUIRED, Target: &irv1.Nest_Shape{Shape: custom("x")}}),
		kind:    irv1.ConversionKind_CONVERSION_IMPOSSIBLE,
		missing: []string{"author"},
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := classify(d, c.a, c.b)
			if r.kind != c.kind || !slices.Equal(r.missing, c.missing) {
				t.Errorf("got %s %v, want %s %v", r.kind, r.missing, c.kind, c.missing)
			}
		})
	}
}

func withForbidden(s *irv1.Shape, f ...*irv1.ForbiddenField) *irv1.Shape {
	s.Forbidden = append(s.Forbidden, f...)
	return s
}

func withNests(s *irv1.Shape, n ...*irv1.Nest) *irv1.Shape {
	s.Nests = append(s.Nests, n...)
	return s
}

// A lineage step names the preset at its root, possibly qualified by a
// relation, and each custom shape in between; anything else is no lineage.
func TestAncestors(t *testing.T) {
	child := shape(custom("c"), "posts", []*irv1.LineageStep{
		presetStep("posts", draft, flat),
		{Op: irv1.ShapeOp_FOR_RELATION, Relation: "author"},
		{Op: irv1.ShapeOp_FROM, Shape: custom("b")},
	})
	want := []*irv1.ShapeId{preset("posts", draft, flat), custom("b")}
	want[0].ForRelation = "author"
	if got := ancestors(child); len(got) != 2 || !proto.Equal(got[0], want[0]) || !proto.Equal(got[1], want[1]) {
		t.Errorf("got %v, want %v", got, want)
	}
	if got := ancestors(shape(custom("c"), "posts", []*irv1.LineageStep{{Op: irv1.ShapeOp_PICK}})); got != nil {
		t.Errorf("got %v from a lineage without a preset", got)
	}
	// An ancestor the layer does not hold adds nothing.
	b := userBundle(child)
	derive(t, b)
	for _, c := range b.GetShapes().GetConversions() {
		if c.GetFrom().GetCustom() == "c" || c.GetTo().GetCustom() == "c" {
			t.Errorf("unexpected conversion %v", c)
		}
	}
}

func TestDeriveCreatesTheShapeLayer(t *testing.T) {
	b := &irv1.Bundle{}
	if ds := Derive(b, nil); len(ds) != 0 || b.GetShapes() == nil || len(b.GetShapes().GetConversions()) != 0 {
		t.Errorf("got %v and %v", ds, b.GetShapes())
	}
}

// The table and the merges do not depend on the order shapes and tables
// arrive in (§3.2).
func TestDeterminism(t *testing.T) {
	seed := uint64(0)
	err := determinism.Repeat(8, func() ([]byte, error) {
		seed++
		b := userBundle()
		b.Shapes.Shapes = determinism.Shuffled(seed, b.Shapes.Shapes)
		ns := b.Catalogs[0].Namespaces[0]
		ns.Tables = determinism.Shuffled(seed, ns.Tables)
		Derive(b, nil)
		return proto.MarshalOptions{Deterministic: true}.Marshal(&irv1.ShapeLayer{Conversions: b.Shapes.Conversions, Merges: b.Shapes.Merges})
	})
	if err != nil {
		t.Fatal(err)
	}
}
