package gen

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	// The running example's catalog carries Postgres extensions (§6.2).
	_ "github.com/thec1oud/dql/proto/dql/ext/postgres/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// shopShapes reads the running example's bundle and adds the custom shapes
// of §14.8 as the core derives them.
func shopShapes(t *testing.T) *irv1.Bundle {
	t.Helper()
	root := filepath.Join("..", "..", "..", "..")
	b := &irv1.Bundle{}
	layer := &irv1.ShapeLayer{}
	custom := &irv1.ShapeLayer{}
	for _, r := range []struct {
		path string
		into proto.Message
	}{
		{filepath.Join(root, "testdata", "shop", "catalog.json"), b},
		{filepath.Join(root, "testdata", "shop", "shapes.json"), layer},
		{filepath.Join(root, "core", "shapes", "testdata", "custom", "record.json"), custom},
	} {
		raw, err := os.ReadFile(r.path)
		if err != nil {
			t.Fatal(err)
		}
		if err := protojson.Unmarshal(raw, r.into); err != nil {
			t.Fatal(err)
		}
	}
	layer.Shapes = append(layer.Shapes, custom.GetShapes()...)
	b.Shapes = layer
	return b
}

// §14.1, §14.4 to §14.6, §14.8: the running example's shapes and unions,
// one file per entity, and the custom shapes in dql-shapes.dql.go.
func TestShopShapes(t *testing.T) {
	files, codes := generate(t, shopShapes(t), Options{})
	if len(codes) != 0 {
		t.Fatalf("codes %q", codes)
	}
	var names []string
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	want := []string{"dql-checks.dql.go", "dql-db.dql.go", "dql-dialect.dql.go", "dql-fragments.dql.go", "dql-shapes.dql.go", "dql-types.dql.go", "dql-version.dql.go", "post.dql.go", "post_tag.dql.go",
		"profile.dql.go", "published_post.dql.go", "tag.dql.go", "tenant.dql.go", "user.dql.go"}
	if !slices.Equal(names, want) {
		t.Fatalf("files %q", names)
	}
	for file, parts := range map[string][]string{
		"dql-shapes.dql.go": {
			"// QuickInvite is the shape quick_invite of shop.app.users.\n//\n// Bound, so its functions take them as parameters (§14.8): password_hash.\ntype QuickInvite struct {",
			"DisplayName string // narrowed; enforced when decoding",
			"Author   UserSummary\n",
			"Tags     []TagFull\n",
		},
		"user.dql.go": {
			"type UserFullNested struct {\n\tUserFull\n\tUserFullNests\n}",
			"Profile       *ProfileFull\n",
			"PostsToAuthor opt.Opt[[]PostChangeForAuthor]\n",
			"Version int32 // expected version, compared on writes\n",
			"PasswordHash string // sensitive\n",
			"func UserRefLink(k UserKey) UserRef {\n\treturn UserRef{change: dql.ChangeLink, key: k}\n}",
		},
		"post.dql.go": {
			// A Nested Draft that spells out its own fields spells out its
			// relations too (§14.5).
			"\tPublishedAt opt.Opt[*time.Time]\n\tAuthor      opt.Opt[UserRef]\n",
			"type PostPartialNested struct {\n\tPostPartial\n\tPostPartialNests\n}",
			"// PostAuthorChange holds one change to a related shop.app.users: link, create, update (§14.5, §14.6).",
			"func PostEditorUnlink() PostEditorChange {",
			"func PostDeleteForAuthor(k PostKey) PostChangeForAuthor {",
		},
		"published_post.dql.go": {"Title       string // asserted non-null; checked when decoding\n"},
	} {
		for _, part := range parts {
			if !strings.Contains(files[file], part) {
				t.Errorf("%s lacks %q:\n%s", file, part, files[file])
			}
		}
	}
}

// shapeBundle holds one table posts(id, title) with relation author to
// users(id), and the given shapes and unions.
func shapeBundle(shapes []*irv1.Shape, unions ...*irv1.RelationUnion) *irv1.Bundle {
	int64Col := func(stored string, words ...string) *irv1.Column {
		return attr(stored, &irv1.Type{Kind: &irv1.Type_Int64{Int64: e()}}, false, words...)
	}
	ns := &irv1.Namespace{Tables: []*irv1.Table{
		{Name: &irv1.Identifier{Stored: "posts"}, HostName: &irv1.Identifier{Words: []string{"post"}},
			Columns: []*irv1.Column{int64Col("id"), int64Col("i_d", "i", "d")},
			Relations: []*irv1.Relation{
				{Name: &irv1.Identifier{Stored: "author", Words: []string{"author"}}, Kind: irv1.RelationKind_RELATION_KIND_MANY_TO_ONE},
				{Name: &irv1.Identifier{Stored: "i_d", Words: []string{"i", "d"}}, Kind: irv1.RelationKind_RELATION_KIND_MANY_TO_ONE},
			}},
		{Name: &irv1.Identifier{Stored: "users"}, HostName: &irv1.Identifier{Words: []string{"user"}}, Columns: []*irv1.Column{int64Col("id")}},
	}}
	b := bundle(ns)
	b.Shapes = &irv1.ShapeLayer{Shapes: shapes, Unions: unions}
	return b
}

func field(column string) *irv1.Field {
	return &irv1.Field{Column: column, Presence: irv1.Presence_PRESENCE_REQUIRED, Type: &irv1.Type{Kind: &irv1.Type_Int64{Int64: e()}}}
}

func custom(name string, entity string, fields []*irv1.Field, nests ...*irv1.Nest) *irv1.Shape {
	return &irv1.Shape{Id: &irv1.ShapeId{Base: &irv1.ShapeId_Custom{Custom: name}}, HostName: &irv1.Identifier{Words: strings.Split(name, "_")},
		Entity: qn(entity), Fields: fields, Nests: nests}
}

// A shape or union of a missing entity, a field or nest the entity does not
// have, a reference to a missing shape or union, and fields that meet after
// Go casing.
func TestShapeFailures(t *testing.T) {
	missingShape := &irv1.ShapeId{Base: &irv1.ShapeId_Custom{Custom: "gone"}}
	missingUnion := &irv1.RelationUnionId{Kind: irv1.RelationUnionKind_RELATION_UNION_REF, Entity: qn("gone")}
	b := shapeBundle([]*irv1.Shape{
		custom("orphan", "gone", nil),
		custom("bad_field", "posts", []*irv1.Field{field("nope")}),
		custom("bad_nest", "posts", nil, &irv1.Nest{Relation: "nope"}),
		custom("bad_shape", "posts", nil, &irv1.Nest{Relation: "author", Target: &irv1.Nest_Shape{Shape: missingShape}}),
		custom("bad_union", "posts", nil, &irv1.Nest{Relation: "author", Target: &irv1.Nest_Union{Union: missingUnion}}),
		custom("twice", "posts", []*irv1.Field{field("id"), field("i_d")}, &irv1.Nest{Relation: "i_d", Target: &irv1.Nest_Shape{Shape: missingShape}}),
	}, &irv1.RelationUnion{Id: &irv1.RelationUnionId{Entity: qn("gone")}, HostName: &irv1.Identifier{Words: []string{"lost", "ref"}}})
	files, codes := generate(t, b, Options{})
	if len(files) != 0 {
		t.Errorf("files despite errors")
	}
	want := []string{
		"SEVERITY_ERROR go-reference-unknown shop.app.gone",
		"SEVERITY_ERROR go-reference-unknown shop.app.gone",
		"SEVERITY_ERROR go-reference-unknown shop.app.posts.author",
		"SEVERITY_ERROR go-reference-unknown shop.app.posts.author",
		"SEVERITY_ERROR go-name-collision shop.app.posts.i_d",
		"SEVERITY_ERROR go-name-collision shop.app.posts.i_d",
		"SEVERITY_ERROR go-reference-unknown shop.app.posts.i_d",
		"SEVERITY_ERROR go-reference-unknown shop.app.posts.nope",
		"SEVERITY_ERROR go-reference-unknown shop.app.posts.nope",
	}
	if !slices.Equal(codes, want) {
		t.Fatalf("codes %q", codes)
	}
	_, ds := Generate(b, Options{Package: "p"})
	var messages []string
	for _, d := range ds {
		messages = append(messages, d.GetMessage())
	}
	all := strings.Join(messages, "\n")
	for _, s := range []string{
		"shape orphan belongs to an entity the bundle does not hold",
		"relation union lost ref belongs to an entity the bundle does not hold",
		"shape bad_field has field nope, which is not a column of shop.app.posts",
		"shape bad_nest nests nope, which is not a relation of shop.app.posts",
		"relation shop.app.posts.author references a shape the bundle does not hold",
		"relation shop.app.posts.author references a relation union the bundle does not hold",
		"id and i_d of shape twice both get the Go name ID",
		"i_d and i_d of shape twice both get the Go name ID",
	} {
		if !strings.Contains(all, s) {
			t.Errorf("messages lack %q:\n%s", s, all)
		}
	}
}

// A Nested shape whose preset has no Nests form, and a preset shape whose
// Flat form is missing, spell out their own fields and relations.
func TestShapeWithoutForms(t *testing.T) {
	post := func(preset irv1.Preset, form irv1.Form) *irv1.ShapeId {
		return &irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: qn("posts")}, Preset: preset, Form: form}
	}
	user := &irv1.Shape{Id: &irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: qn("users")}, Preset: irv1.Preset_PRESET_FULL, Form: irv1.Form_FORM_FLAT},
		HostName: &irv1.Identifier{Words: []string{"user", "full"}}, Entity: qn("users"), Fields: []*irv1.Field{field("id")}}
	nested := &irv1.Shape{Id: post(irv1.Preset_PRESET_FULL, irv1.Form_FORM_NESTED), HostName: &irv1.Identifier{Words: []string{"post", "full", "nested"}},
		Entity: qn("posts"), Fields: []*irv1.Field{field("id")},
		Nests: []*irv1.Nest{{Relation: "author", Nullable: true, Target: &irv1.Nest_Shape{Shape: user.GetId()}}}}
	files, codes := generate(t, shapeBundle([]*irv1.Shape{nested, user}), Options{})
	if len(codes) != 0 {
		t.Fatalf("codes %q", codes)
	}
	if want := "type PostFullNested struct {\n\tID     int64\n\tAuthor *UserFull\n}"; !strings.Contains(files["post.dql.go"], want) {
		t.Errorf("post.dql.go lacks %q:\n%s", want, files["post.dql.go"])
	}
}

// Nests that differ only in what a write supplies get the same Go field;
// any difference in type does not (§14.5, §14.6).
func TestSameNestType(t *testing.T) {
	ref := &irv1.Nest_Union{Union: &irv1.RelationUnionId{Kind: irv1.RelationUnionKind_RELATION_UNION_REF, Entity: qn("users")}}
	base := &irv1.Nest{Relation: "author", Target: ref}
	if !sameNestType(base, &irv1.Nest{Relation: "author", Target: ref, Supplies: []string{"author_id"}, NeedsOne: true}) {
		t.Errorf("supplies changed the type")
	}
	for _, n := range []*irv1.Nest{
		{Relation: "editor", Target: ref},
		{Relation: "author", Target: ref, Presence: irv1.Presence_PRESENCE_OPTIONAL},
		{Relation: "author", Target: ref, Nullable: true},
		{Relation: "author"},
		{Relation: "author", Target: &irv1.Nest_Shape{Shape: &irv1.ShapeId{Base: &irv1.ShapeId_Custom{Custom: "x"}}}},
	} {
		if sameNestType(base, n) {
			t.Errorf("%v has the type of %v", n, base)
		}
	}
}
