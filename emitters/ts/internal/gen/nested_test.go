package gen

import (
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

const (
	pFull  = irv1.Preset_PRESET_FULL
	pDraft = irv1.Preset_PRESET_DRAFT
	pPart  = irv1.Preset_PRESET_PARTIAL
	pKey   = irv1.Preset_PRESET_KEY
	fFlat  = irv1.Form_FORM_FLAT
	fNests = irv1.Form_FORM_NESTS
	fNest  = irv1.Form_FORM_NESTED
)

// presetShape returns the shop bundle's preset shape of table object.
func presetShape(t *testing.T, b *irv1.Bundle, object string, p irv1.Preset, form irv1.Form, forRelation string) *irv1.Shape {
	t.Helper()
	for _, s := range b.GetShapes().GetShapes() {
		id := s.GetId()
		if id.GetEntity().GetObject() == object && id.GetPreset() == p && id.GetForm() == form && id.GetForRelation() == forRelation {
			return s
		}
	}
	t.Fatalf("no shape of %s", object)
	return nil
}

// planCase is a change to the running example and what it leaves of a
// nested write.
type planCase struct {
	name   string
	mutate func(t *testing.T, b *irv1.Bundle, edit func(object string, p irv1.Preset, form irv1.Form, forRelation string) *irv1.Shape)
	// class is the store class whose method is checked.
	class   string
	present bool
	want    []string
}

func dropQuery(b *irv1.Bundle, name string) {
	b.GetQueries().Queries = slices.DeleteFunc(b.GetQueries().GetQueries(), func(q *irv1.Query) bool { return q.GetName().GetStored() == name })
}

func dropNest(s *irv1.Shape, rel string) {
	s.Nests = slices.DeleteFunc(s.GetNests(), func(n *irv1.Nest) bool { return n.GetRelation() == rel })
}

func dropFragment(b *irv1.Bundle, id string) {
	fs := b.GetQueries().GetFragments()
	fs.Fragments = slices.DeleteFunc(fs.GetFragments(), func(f *irv1.Fragment) bool { return f.GetId() == id })
}

// classBody returns the body of class name in the stores module.
func classBody(files map[string]string, name string) string {
	s := files["dql-stores.dql.ts"]
	i := strings.Index(s, "export class "+name+" ")
	if i < 0 {
		return ""
	}
	s = s[i:]
	return s[:strings.Index(s, "\n}\n")]
}

// runPlans generates each case and checks that method is on its class or
// not, holding want.
func runPlans(t *testing.T, method string, cases []planCase) {
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := shopShapes(t)
			var touched []*irv1.ShapeId
			edit := func(object string, p irv1.Preset, form irv1.Form, forRelation string) *irv1.Shape {
				s := presetShape(t, b, object, p, form, forRelation)
				touched = append(touched, s.GetId())
				return s
			}
			c.mutate(t, b, edit)
			// Conversions no longer fit the shapes changed by hand.
			b.GetShapes().Conversions = slices.DeleteFunc(b.GetShapes().GetConversions(), func(cv *irv1.Conversion) bool {
				return slices.ContainsFunc(touched, func(id *irv1.ShapeId) bool {
					return proto.Equal(id, cv.GetFrom()) || proto.Equal(id, cv.GetTo()) && cv.GetKind() != irv1.ConversionKind_CONVERSION_WITH_BASE
				})
			})
			files, codes := generate(t, b, Options{})
			if len(codes) != 0 {
				t.Fatal(codes)
			}
			body := classBody(files, c.class)
			if strings.Contains(body, "async "+method+"(") != c.present {
				t.Errorf("%s: %s present %v", c.class, method, !c.present)
			}
			for _, w := range c.want {
				if !strings.Contains(body, w) {
					t.Errorf("lacks %q in\n%s", w, body)
				}
			}
			// A link to a Key the Partial leaves out has no validator to call.
			if !strings.HasPrefix(c.name, "link to a key") {
				typecheck(t, files)
			}
		})
	}
}

// §20.5: insertNested is generated only where every relation of the Full
// Nested is one it writes and reads back.
func TestInsertNestedPlans(t *testing.T) {
	type editFn = func(string, irv1.Preset, irv1.Form, string) *irv1.Shape
	runPlans(t, "insertNested", []planCase{
		{"child without insert", func(t *testing.T, b *irv1.Bundle, _ editFn) { dropQuery(b, "profile.insert") }, "UsersWrites", false, nil},
		{"key beyond the foreign key", func(t *testing.T, b *irv1.Bundle, edit editFn) {
			k := edit("tenants", pKey, 0, "")
			k.Fields = append(k.Fields, shapeField(presetShape(t, b, "tenants", pFull, fFlat, ""), "name"))
		}, "UsersWrites", false, nil},
		{"null reference", func(t *testing.T, b *irv1.Bundle, edit editFn) {
			shapeNest(edit("post_tags", pDraft, fNest, ""), "tag").Nullable = true
		}, "PostTagsWrites", false, nil},
		{"create without insert", func(t *testing.T, b *irv1.Bundle, _ editFn) { dropQuery(b, "tag.insert") }, "PostTagsWrites", false, nil},
		{"null child", func(t *testing.T, b *irv1.Bundle, edit editFn) {
			shapeNest(edit("users", pDraft, fNest, ""), "profile").Nullable = true
		}, "UsersWrites", false, nil},
		{"child missing a required field", func(t *testing.T, b *irv1.Bundle, edit editFn) {
			s := edit("posts", pDraft, fFlat, "author")
			s.Fields = slices.DeleteFunc(s.GetFields(), func(f *irv1.Field) bool { return f.GetColumn() == "title" })
		}, "UsersWrites", false, nil},
		{"child optional where required", func(t *testing.T, b *irv1.Bundle, edit editFn) {
			shapeField(edit("posts", pDraft, fFlat, "author"), "title").Presence = irv1.Presence_PRESENCE_OPTIONAL
		}, "UsersWrites", false, nil},
		{"junction without insert", func(t *testing.T, b *irv1.Bundle, edit editFn) {
			dropQuery(b, "post_tag.insert")
			for _, s := range []*irv1.Shape{edit("tags", pFull, fNest, ""), edit("tags", pFull, fNests, "")} {
				dropNest(s, "post_tags")
			}
		}, "TagsWrites", false, nil},
		{"link without its junction relation", func(t *testing.T, b *irv1.Bundle, edit editFn) {
			for _, s := range []*irv1.Shape{edit("tags", pFull, fNest, ""), edit("tags", pFull, fNests, "")} {
				dropNest(s, "post_tags")
			}
		}, "TagsWrites", false, nil},
		{"nullable foreign key read into a required relation", func(t *testing.T, b *irv1.Bundle, edit editFn) {
			shapeNest(edit("posts", pFull, fNest, ""), "editor").Nullable = false
			shapeNest(edit("posts", pFull, fNests, ""), "editor").Nullable = false
		}, "PostsUserScopedWrites", false, nil},
		{"child required where optional", func(t *testing.T, b *irv1.Bundle, edit editFn) {
			shapeField(edit("posts", pDraft, fFlat, "author"), "status").Presence = irv1.Presence_PRESENCE_REQUIRED
		}, "UsersWrites", true, []string{"const child = await postsStore.insert({ ...c, authorId: row.id });"}},
		{"relations the draft leaves out", func(t *testing.T, b *irv1.Bundle, edit editFn) {
			for _, s := range []*irv1.Shape{edit("tags", pDraft, fNest, ""), edit("tags", pDraft, fNests, "")} {
				dropNest(s, "posts")
			}
			dropNest(edit("users", pDraft, fNest, ""), "profile")
		}, "TagsWrites", true, []string{"postsRows.push(await postsStore.get({ id: child.postId }));"}},
		{"required foreign key", func(t *testing.T, b *irv1.Bundle, edit editFn) {
			shapeField(edit("post_tags", pDraft, fNest, ""), "post_id").Presence = irv1.Presence_PRESENCE_REQUIRED
		}, "PostTagsWrites", true, []string{"const draft: PostTagDraft = { ...d, tagId: d.tagId! };"}},
	})
}

// §20.5: patchNested is generated only where every relation of the Full
// Nested is one it reads back, and every change the Nested Partial allows
// is one it writes.
func TestPatchNestedPlans(t *testing.T) {
	type editFn = func(string, irv1.Preset, irv1.Form, string) *irv1.Shape
	replace := func(edit editFn, object, rel string) {
		shapeNest(edit(object, pPart, fNest, ""), rel).GetPolicy().Collection = irv1.CollectionPolicy_COLLECTION_POLICY_REPLACE
	}
	dropNests := func(edit editFn, object, rel string) {
		for _, s := range []*irv1.Shape{edit(object, pPart, fNest, ""), edit(object, pPart, fNests, "")} {
			dropNest(s, rel)
		}
	}
	runPlans(t, "patchNested", []planCase{
		{"owning key beyond the foreign key", func(t *testing.T, b *irv1.Bundle, edit editFn) {
			k := edit("tenants", pKey, 0, "")
			k.Fields = append(k.Fields, shapeField(presetShape(t, b, "tenants", pFull, fFlat, ""), "name"))
		}, "UsersWrites", false, nil},
		{"link without its junction relation", func(t *testing.T, b *irv1.Bundle, edit editFn) {
			for _, s := range []*irv1.Shape{edit("tags", pFull, fNest, ""), edit("tags", pFull, fNests, "")} {
				dropNest(s, "post_tags")
			}
		}, "TagsWrites", false, nil},
		{"no order", func(t *testing.T, b *irv1.Bundle, _ editFn) { dropFragment(b, "order.asc") }, "UsersWrites", false, nil},
		{"foreign key without eq", func(t *testing.T, b *irv1.Bundle, _ editFn) {
			col := column(table(t, b, "posts"), "author_id")
			col.Operators = slices.DeleteFunc(col.GetOperators(), func(o *irv1.ColumnOperator) bool { return o.GetName() == "eq" })
		}, "UsersWrites", false, nil},
		{"owning update without patch", func(t *testing.T, b *irv1.Bundle, _ editFn) { dropQuery(b, "user.patch") }, "PostsUserScopedWrites", false, nil},
		{"owning create without insert", func(t *testing.T, b *irv1.Bundle, _ editFn) { dropQuery(b, "user.insert") }, "PostsUserScopedWrites", false, nil},
		{"link to a key the partial leaves out", func(t *testing.T, b *irv1.Bundle, _ editFn) {
			n := shapeNest(presetShape(t, b, "profiles", pPart, fNest, ""), "user")
			for _, u := range b.GetShapes().GetUnions() {
				if proto.Equal(u.GetId(), n.GetUnion()) {
					u.Variants = append(u.Variants, &irv1.RelationVariant{Change: irv1.RelationChange_RELATION_CHANGE_LINK, Payload: presetShape(t, b, "users", pKey, 0, "").GetId()})
				}
			}
		}, "ProfilesUserScopedWrites", false, nil},
		{"required to-one child", func(t *testing.T, b *irv1.Bundle, edit editFn) {
			shapeNest(edit("users", pFull, fNest, ""), "profile").Nullable = false
		}, "UsersWrites", false, nil},
		{"replaced to-one child", func(t *testing.T, b *irv1.Bundle, edit editFn) { replace(edit, "users", "profile") }, "UsersWrites", false, nil},
		{"unlink without patch", func(t *testing.T, b *irv1.Bundle, edit editFn) {
			dropNests(edit, "users", "posts_to_author")
			dropQuery(b, "post.patch")
		}, "UsersWrites", false, nil},
		{"unlink of a key the patch leaves out", func(t *testing.T, b *irv1.Bundle, edit editFn) {
			shapeNest(edit("users", pPart, fNest, ""), "profile").GetPolicy().Removal = irv1.Removal_REMOVAL_UNLINK
		}, "UsersWrites", false, nil},
		{"replace without delete_where", func(t *testing.T, b *irv1.Bundle, _ editFn) { dropQuery(b, "post.delete_where") }, "UsersWrites", false, nil},
		{"update of a field the patch leaves out", func(t *testing.T, b *irv1.Bundle, edit editFn) {
			s := edit("posts", pPart, fFlat, "author")
			s.Fields = append(s.Fields, shapeField(presetShape(t, b, "posts", pFull, fFlat, ""), "slug"))
		}, "UsersWrites", false, nil},
		{"delete without delete", func(t *testing.T, b *irv1.Bundle, _ editFn) { dropQuery(b, "post.delete") }, "UsersWrites", false, nil},
		{"update without get", func(t *testing.T, b *irv1.Bundle, _ editFn) { dropQuery(b, "profile.get.by_primary") }, "UsersWrites", false, nil},
		{"replace and unlink", func(t *testing.T, b *irv1.Bundle, edit editFn) { replace(edit, "users", "posts_to_editor") }, "UsersWrites", true, []string{
			"const kept: PostKey[] = [];",
			"let where = posts.editorId.eq(row.id);",
			"where = and(where, not(posts.id.eq(key.id)));",
			"await postsStore.updateWhere(where, { editorId: null });",
		}},
		{"links the partial leaves out", func(t *testing.T, b *irv1.Bundle, edit editFn) { dropNests(edit, "tags", "posts") }, "TagsWrites", true, nil},
		{"link without insert_or_ignore", func(t *testing.T, b *irv1.Bundle, _ editFn) { dropQuery(b, "post_tag.insert_or_ignore.by_primary") }, "TagsWrites", false, nil},
		{"unlink without delete", func(t *testing.T, b *irv1.Bundle, edit editFn) {
			dropNests(edit, "tags", "post_tags")
			dropQuery(b, "post_tag.delete")
		}, "TagsWrites", false, nil},
		{"replaced links without delete_where", func(t *testing.T, b *irv1.Bundle, edit editFn) {
			replace(edit, "tags", "posts")
			dropQuery(b, "post_tag.delete_where")
		}, "TagsWrites", false, nil},
		{"replaced junction rows", func(t *testing.T, b *irv1.Bundle, edit editFn) { replace(edit, "tags", "post_tags") }, "TagsWrites", true, []string{
			"where = and(where, not(and(postTags.postId.eq(key.postId), postTags.tagId.eq(key.tagId))));",
		}},
		{"replaced links", func(t *testing.T, b *irv1.Bundle, edit editFn) { replace(edit, "tags", "posts") }, "TagsWrites", true, []string{
			"kept.push(ch.key);",
			"kept.push({ id: related.id });",
			"let where = postTags.tagId.eq(row.id);",
			"where = and(where, not(postTags.postId.eq(key.id)));",
			"await postTagsStore.deleteWhere(where);",
		}},
	})
}

// §14.1: a read fills the expected version it ignores with a zero of its
// type.
func TestReadKey(t *testing.T) {
	b := shopShapes(t)
	users := &entity{t: table(t, b, "users"), key: presetShape(t, b, "users", pKey, 0, "")}
	if got := readKey(users, []string{"id: 1n"}); got != "{ id: 1n, version: 0 }" {
		t.Error(got)
	}
	for _, f := range users.key.GetFields() {
		if f.GetRole() == irv1.FieldRole_FIELD_ROLE_EXPECTED_VERSION {
			f.Type = int64Type()
		}
	}
	if got := readKey(users, []string{"id: 1n"}); got != "{ id: 1n, version: 0n }" {
		t.Error(got)
	}
}
