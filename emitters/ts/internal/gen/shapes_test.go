package gen

import (
	"fmt"
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

// generate runs Generate and returns its files by name and its diagnostics
// as "severity code target".
func generate(t *testing.T, b *irv1.Bundle, o Options) (map[string]string, []string) {
	t.Helper()
	fs, ds := Generate(b, o)
	files := map[string]string{}
	for _, f := range fs {
		files[f.Name] = string(f.Content)
	}
	var codes []string
	for _, d := range ds {
		tg := d.GetTarget()
		target := strings.Join(slices.DeleteFunc([]string{tg.GetCatalog(), tg.GetNamespace(), tg.GetObject(), tg.GetMember()}, func(s string) bool { return s == "" }), ".")
		codes = append(codes, fmt.Sprintf("%s %s %s", d.GetSeverity(), d.GetCode(), target))
	}
	return files, codes
}

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
// one file per entity, and the custom shapes in dql-shapes.dql.ts.
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
	want := []string{"dql-checks.dql.ts", "dql-db.dql.ts", "dql-decoders.dql.ts", "dql-dialect.dql.ts", "dql-fragments.dql.ts", "dql-shapes.dql.ts", "dql-stores.dql.ts",
		"dql-types.dql.ts", "dql-version.dql.ts", "index.dql.ts", "post.dql.ts", "post_tag.dql.ts",
		"profile.dql.ts", "published_post.dql.ts", "tag.dql.ts", "tenant.dql.ts", "user.dql.ts"}
	if !slices.Equal(names, want) {
		t.Fatalf("files %q", names)
	}
	for file, parts := range map[string][]string{
		"dql-shapes.dql.ts": {
			"/**\n * QuickInvite is the shape quick_invite of shop.app.users.\n *\n * Bound, so its functions take them as parameters (§14.8): password_hash.\n */\nexport interface QuickInvite {",
			"displayName: string; // narrowed; enforced when decoding",
			"  author: UserSummary;\n",
			"  tags: TagFull[];\n",
		},
		"user.dql.ts": {
			"export type UserFullNested = UserFull & UserFullNests;",
			"  profile: ProfileFull | null;\n",
			"  postsToAuthor?: PostChangeForAuthor[];\n",
			"version: number; // expected version, compared on writes\n",
			"passwordHash: string; // sensitive\n",
		},
		"post.dql.ts": {
			// A Nested Draft that spells out its own fields spells out its
			// relations too (§14.5).
			"  publishedAt?: Temporal.Instant | null;\n  author?: UserRef;\n",
			"export type PostPartialNested = PostPartial & PostPartialNests;",
			"/** PostAuthorChange holds one change to a related shop.app.users: link, create, update (§14.5, §14.6). */",
			"  | { kind: \"unlink\" };\n",
			"  | { kind: \"create\"; draft: UserDraft }\n",
			"  | { kind: \"update\"; partial: UserPartial };\n",
			"  | { kind: \"update\"; key: PostKey; partial: PostPartialForEditor }\n",
		},
		"published_post.dql.ts": {"title: string; // asserted non-null; checked when decoding\n"},
		"index.dql.ts":          {"export * from \"./dql-shapes.dql.ts\";\n"},
	} {
		for _, part := range parts {
			if !strings.Contains(files[file], part) {
				t.Errorf("%s lacks %q:\n%s", file, part, files[file])
			}
		}
	}
}

func qn(object string) *irv1.QualifiedName {
	return &irv1.QualifiedName{Catalog: "shop", Namespace: "app", Object: object}
}

func int64Type() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Int64{Int64: &irv1.Empty{}}} }

func col(stored string, typ *irv1.Type, words ...string) *irv1.Column {
	if len(words) == 0 {
		words = []string{stored}
	}
	return &irv1.Column{Name: &irv1.Identifier{Stored: stored}, HostName: &irv1.Identifier{Stored: stored, Words: words}, Type: typ}
}

// shapeBundle holds one table posts(id, i_d) with relations author and i_d
// to users(id), and the given shapes and unions.
func shapeBundle(shapes []*irv1.Shape, unions ...*irv1.RelationUnion) *irv1.Bundle {
	return shapeBundleWith(nil, shapes, unions...)
}

// shapeBundleWith adds columns to posts.
func shapeBundleWith(extra []*irv1.Column, shapes []*irv1.Shape, unions ...*irv1.RelationUnion) *irv1.Bundle {
	ns := &irv1.Namespace{Name: &irv1.Identifier{Stored: "app"}, Tables: []*irv1.Table{
		{Name: &irv1.Identifier{Stored: "posts"}, HostName: &irv1.Identifier{Words: []string{"post"}},
			Columns: append([]*irv1.Column{col("id", int64Type()), col("i_d", int64Type(), "i", "d")}, extra...),
			Relations: []*irv1.Relation{
				{Name: &irv1.Identifier{Stored: "author", Words: []string{"author"}}, Kind: irv1.RelationKind_RELATION_KIND_MANY_TO_ONE},
				{Name: &irv1.Identifier{Stored: "i_d", Words: []string{"i", "d"}}, Kind: irv1.RelationKind_RELATION_KIND_MANY_TO_ONE},
			}},
		{Name: &irv1.Identifier{Stored: "users"}, HostName: &irv1.Identifier{Words: []string{"user"}}, Columns: []*irv1.Column{col("id", int64Type())}},
	}}
	return &irv1.Bundle{
		Version: &irv1.IrVersion{},
		Catalogs: []*irv1.Catalog{{
			Name:       &irv1.Identifier{Stored: "shop"},
			Provenance: &irv1.Provenance{Source: "pg-live", SourceVersion: "1.0.0", InputFingerprint: "sha256:x"},
			Namespaces: []*irv1.Namespace{ns},
		}},
		Shapes: &irv1.ShapeLayer{Shapes: shapes, Unions: unions},
	}
}

func field(column string) *irv1.Field {
	return &irv1.Field{Column: column, Presence: irv1.Presence_PRESENCE_REQUIRED, Type: int64Type()}
}

func custom(name string, entity string, fields []*irv1.Field, nests ...*irv1.Nest) *irv1.Shape {
	return &irv1.Shape{Id: &irv1.ShapeId{Base: &irv1.ShapeId_Custom{Custom: name}}, HostName: &irv1.Identifier{Words: strings.Split(name, "_")},
		Entity: qn(entity), Fields: fields, Nests: nests}
}

// A shape or union of a missing entity, a field or nest the entity does not
// have, a reference to a missing shape or union, and fields that meet after
// TypeScript casing.
func TestShapeFailures(t *testing.T) {
	missingShape := &irv1.ShapeId{Base: &irv1.ShapeId_Custom{Custom: "gone"}}
	missingUnion := &irv1.RelationUnionId{Kind: irv1.RelationUnionKind_RELATION_UNION_REF, Entity: qn("gone")}
	b := shapeBundleWith([]*irv1.Column{col("iD", int64Type(), "i", "d")}, []*irv1.Shape{
		custom("orphan", "gone", nil),
		custom("bad_field", "posts", []*irv1.Field{field("nope")}),
		custom("bad_nest", "posts", nil, &irv1.Nest{Relation: "nope"}),
		custom("bad_shape", "posts", nil, &irv1.Nest{Relation: "author", Target: &irv1.Nest_Shape{Shape: missingShape}}),
		custom("bad_union", "posts", nil, &irv1.Nest{Relation: "author", Target: &irv1.Nest_Union{Union: missingUnion}}),
		custom("twice", "posts", []*irv1.Field{field("i_d"), field("iD")}, &irv1.Nest{Relation: "i_d", Target: &irv1.Nest_Shape{Shape: missingShape}}),
	}, &irv1.RelationUnion{Id: &irv1.RelationUnionId{Entity: qn("gone")}, HostName: &irv1.Identifier{Words: []string{"lost", "ref"}}})
	files, codes := generate(t, b, Options{})
	if len(files) != 0 {
		t.Errorf("files despite errors")
	}
	want := []string{
		"SEVERITY_ERROR ts-reference-unknown shop.app.gone",
		"SEVERITY_ERROR ts-reference-unknown shop.app.gone",
		"SEVERITY_ERROR ts-reference-unknown shop.app.posts.author",
		"SEVERITY_ERROR ts-reference-unknown shop.app.posts.author",
		"SEVERITY_ERROR ts-name-collision shop.app.posts.iD",
		"SEVERITY_ERROR ts-name-collision shop.app.posts.i_d",
		"SEVERITY_ERROR ts-reference-unknown shop.app.posts.i_d",
		"SEVERITY_ERROR ts-reference-unknown shop.app.posts.nope",
		"SEVERITY_ERROR ts-reference-unknown shop.app.posts.nope",
	}
	if !slices.Equal(codes, want) {
		t.Fatalf("codes %q", codes)
	}
	_, ds := Generate(b, Options{})
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
		"i_d and iD of shape twice both get the TypeScript name iD",
		"iD and i_d of shape twice both get the TypeScript name iD",
	} {
		if !strings.Contains(all, s) {
			t.Errorf("messages lack %q:\n%s", s, all)
		}
	}
}

func postID(preset irv1.Preset, form irv1.Form) *irv1.ShapeId {
	return &irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: qn("posts")}, Preset: preset, Form: form}
}

// A Nested shape whose preset has no Nests form, and a preset shape whose
// Flat form is missing, spell out their own fields and relations; a Key
// never extends.
func TestShapeWithoutForms(t *testing.T) {
	user := &irv1.Shape{Id: &irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: qn("users")}, Preset: irv1.Preset_PRESET_FULL, Form: irv1.Form_FORM_FLAT},
		HostName: &irv1.Identifier{Words: []string{"user", "full"}}, Entity: qn("users"), Fields: []*irv1.Field{field("id")}}
	nested := &irv1.Shape{Id: postID(irv1.Preset_PRESET_FULL, irv1.Form_FORM_NESTED), HostName: &irv1.Identifier{Words: []string{"post", "full", "nested"}},
		Entity: qn("posts"), Fields: []*irv1.Field{field("id")},
		Nests: []*irv1.Nest{{Relation: "author", Nullable: true, Target: &irv1.Nest_Shape{Shape: user.GetId()}}}}
	key := &irv1.Shape{Id: postID(irv1.Preset_PRESET_KEY, irv1.Form_FORM_UNSPECIFIED), HostName: &irv1.Identifier{Words: []string{"post", "key"}},
		Entity: qn("posts"), Fields: []*irv1.Field{field("id")}}
	files, codes := generate(t, shapeBundle([]*irv1.Shape{nested, user, key}), Options{})
	if len(codes) != 0 {
		t.Fatalf("codes %q", codes)
	}
	for _, want := range []string{
		"/** PostFullNested is the shape post.full.nested of shop.app.posts. */\nexport interface PostFullNested {\n  id: bigint;\n  author: UserFull | null;\n}",
		"/** PostKey is the shape post.key of shop.app.posts. */\nexport interface PostKey {\n  id: bigint;\n}",
		"import { type UserFull, decodeUserFull } from \"./user.dql.ts\";",
	} {
		if !strings.Contains(files["post.dql.ts"], want) {
			t.Errorf("post.dql.ts lacks %q:\n%s", want, files["post.dql.ts"])
		}
	}
}

// A Nested form whose Nests form holds other relations extends its Flat
// form and spells out its relations; one with no fields extends nothing.
func TestShapeNestsDiffer(t *testing.T) {
	ref := &irv1.Nest_Union{Union: &irv1.RelationUnionId{Kind: irv1.RelationUnionKind_RELATION_UNION_REF, Entity: qn("posts"), Relation: "author"}}
	flat := &irv1.Shape{Id: postID(irv1.Preset_PRESET_DRAFT, irv1.Form_FORM_FLAT), HostName: &irv1.Identifier{Words: []string{"post", "draft"}},
		Entity: qn("posts"), Fields: []*irv1.Field{field("id")}}
	nests := &irv1.Shape{Id: postID(irv1.Preset_PRESET_DRAFT, irv1.Form_FORM_NESTS), HostName: &irv1.Identifier{Words: []string{"post", "draft", "nests"}},
		Entity: qn("posts"), Nests: []*irv1.Nest{{Relation: "author", Target: ref}}}
	nested := &irv1.Shape{Id: postID(irv1.Preset_PRESET_DRAFT, irv1.Form_FORM_NESTED), HostName: &irv1.Identifier{Words: []string{"post", "draft", "nested"}},
		Entity: qn("posts"), Fields: []*irv1.Field{field("id")},
		Nests: []*irv1.Nest{{Relation: "author", Presence: irv1.Presence_PRESENCE_OPTIONAL, Target: ref}}}
	union := &irv1.RelationUnion{Id: ref.Union, HostName: &irv1.Identifier{Words: []string{"post", "author", "ref"}}, Variants: []*irv1.RelationVariant{
		{Change: irv1.RelationChange_RELATION_CHANGE_LINK, Key: &irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: qn("users")}, Preset: irv1.Preset_PRESET_KEY}},
	}}
	userKey := &irv1.Shape{Id: union.GetVariants()[0].GetKey(), HostName: &irv1.Identifier{Words: []string{"user", "key"}}, Entity: qn("users"), Fields: []*irv1.Field{field("id")}}
	files, codes := generate(t, shapeBundle([]*irv1.Shape{flat, nests, nested, userKey}, union), Options{})
	if len(codes) != 0 {
		t.Fatalf("codes %q", codes)
	}
	for _, want := range []string{
		"export interface PostDraftNests {\n  author: PostAuthorRef;\n}",
		"export interface PostDraftNested extends PostDraft {\n  author?: PostAuthorRef;\n}",
		"/** PostAuthorRef holds one change to a related shop.app.users: link (§14.5, §14.6). */\nexport type PostAuthorRef =\n  | { kind: \"link\"; key: UserKey };\n",
	} {
		if !strings.Contains(files["post.dql.ts"], want) {
			t.Errorf("post.dql.ts lacks %q:\n%s", want, files["post.dql.ts"])
		}
	}
}

// A union variant naming a missing key or payload shape.
func TestUnionUnknownShape(t *testing.T) {
	id := &irv1.RelationUnionId{Kind: irv1.RelationUnionKind_RELATION_UNION_RELATION_CHANGE, Entity: qn("posts"), Relation: "author"}
	missing := &irv1.ShapeId{Base: &irv1.ShapeId_Custom{Custom: "gone"}}
	union := &irv1.RelationUnion{Id: id, HostName: &irv1.Identifier{Words: []string{"post", "author", "change"}}, Variants: []*irv1.RelationVariant{
		{Change: irv1.RelationChange_RELATION_CHANGE_UPDATE, Key: missing, Payload: missing},
	}}
	_, codes := generate(t, shapeBundle(nil, union), Options{})
	// The key and the payload are the same shape, reported once.
	if want := []string{"SEVERITY_ERROR ts-reference-unknown shop.app.posts"}; !slices.Equal(codes, want) {
		t.Errorf("codes %q", codes)
	}
}

// x.ts.time-type: date maps an instant to Date and says what it costs;
// temporal is the default; on another type, or with another value, it fails
// (§7.7, §8.8).
func TestTimeType(t *testing.T) {
	annotate := func(c *irv1.Column, v string) *irv1.Column {
		c.Annotations = []*irv1.Annotation{{Key: "x.other"}, {Key: TimeTypeKey, Value: &irv1.AnnotationValue{Kind: &irv1.AnnotationValue_String_{String_: v}}}}
		return c
	}
	instant := &irv1.Type{Kind: &irv1.Type_Instant{Instant: &irv1.TimeType{}}}
	cols := []*irv1.Column{
		annotate(col("at", instant), "date"),
		annotate(col("since", instant), "temporal"),
	}
	fld := func(c *irv1.Column, nullable bool) *irv1.Field {
		return &irv1.Field{Column: c.GetName().GetStored(), Presence: irv1.Presence_PRESENCE_REQUIRED, Type: c.GetType(), Nullable: nullable}
	}
	s := custom("times", "posts", []*irv1.Field{fld(cols[0], true), fld(cols[1], false)})
	files, codes := generate(t, shapeBundleWith(cols, []*irv1.Shape{s}), Options{})
	if len(codes) != 0 {
		t.Fatalf("codes %q", codes)
	}
	want := "  at: Date | null; // Date, millisecond precision (x.ts.time-type: date)\n  since: Temporal.Instant;\n"
	if !strings.Contains(files["dql-shapes.dql.ts"], want) {
		t.Errorf("dql-shapes.dql.ts lacks %q:\n%s", want, files["dql-shapes.dql.ts"])
	}

	bad := []*irv1.Column{annotate(col("n", int64Type()), "date"), annotate(col("m", instant), "Date")}
	s = custom("times", "posts", []*irv1.Field{fld(bad[0], false), fld(bad[1], false)})
	_, ds := Generate(shapeBundleWith(bad, []*irv1.Shape{s}), Options{})
	var got []string
	for _, d := range ds {
		if d.GetCode() != CodeTimeTypeInvalid {
			t.Errorf("code %s", d.GetCode())
		}
		got = append(got, d.GetMessage())
	}
	for i, w := range []string{
		`column shop.app.posts.m has x.ts.time-type: "Date"; use date or temporal (§7.7)`,
		"column shop.app.posts.n has x.ts.time-type: date, which applies only to instant columns (§7.7, §8.8); remove it",
	} {
		if i >= len(got) || got[i] != w {
			t.Errorf("diagnostics %q, want %q at %d", got, w, i)
		}
	}
}

// Nests that differ only in what a write supplies get the same member; any
// difference in type does not (§14.5, §14.6).
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

// A column every shape holds reports its unmapped type once (§8.3).
func TestShapeDiagnosticOnce(t *testing.T) {
	opaque := &irv1.Type{Kind: &irv1.Type_Opaque{Opaque: &irv1.Opaque{}}, Native: &irv1.NativeType{Name: &irv1.QualifiedName{Namespace: "public", Object: "citext"}}}
	c := col("nick", opaque)
	f := &irv1.Field{Column: "nick", Presence: irv1.Presence_PRESENCE_REQUIRED, Type: opaque}
	b := shapeBundleWith([]*irv1.Column{c}, []*irv1.Shape{custom("one", "posts", []*irv1.Field{f}), custom("two", "posts", []*irv1.Field{f})})
	_, codes := generate(t, b, Options{})
	if want := []string{"SEVERITY_ERROR ts-opaque-unmapped shop.app.posts.nick"}; !slices.Equal(codes, want) {
		t.Errorf("codes %q", codes)
	}
}
