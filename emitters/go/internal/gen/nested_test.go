package gen

import (
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// touched holds the shapes a test changed by hand, whose conversions no
// longer fit them.
var touched []*irv1.ShapeId

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

// edit is presetShape for a shape the test changes.
func edit(t *testing.T, b *irv1.Bundle, object string, p irv1.Preset, form irv1.Form, forRelation string) *irv1.Shape {
	t.Helper()
	s := presetShape(t, b, object, p, form, forRelation)
	touched = append(touched, s.GetId())
	return s
}

func dropQuery(b *irv1.Bundle, name string) {
	b.GetQueries().Queries = slices.DeleteFunc(b.GetQueries().GetQueries(), func(q *irv1.Query) bool { return q.GetName().GetStored() == name })
}

func dropNest(s *irv1.Shape, rel string) {
	s.Nests = slices.DeleteFunc(s.GetNests(), func(n *irv1.Nest) bool { return n.GetRelation() == rel })
}

// §20.5: InsertNested is generated only where every relation of the Full
// Nested is one it writes and reads back.
func TestInsertNestedPlans(t *testing.T) {
	const (
		full  = irv1.Preset_PRESET_FULL
		draft = irv1.Preset_PRESET_DRAFT
		key   = irv1.Preset_PRESET_KEY
		flat  = irv1.Form_FORM_FLAT
		nests = irv1.Form_FORM_NESTS
		nest  = irv1.Form_FORM_NESTED
	)
	for _, c := range []struct {
		name   string
		mutate func(t *testing.T, b *irv1.Bundle)
		file   string
		want   []string
		absent bool
	}{
		{"child without insert", func(t *testing.T, b *irv1.Bundle) { dropQuery(b, "profile.insert") }, "user.dql.go", nil, true},
		{"key beyond the foreign key", func(t *testing.T, b *irv1.Bundle) {
			k := edit(t, b, "tenants", key, 0, "")
			k.Fields = append(k.Fields, shapeField(presetShape(t, b, "tenants", full, flat, ""), "name"))
		}, "user.dql.go", nil, true},
		{"null reference", func(t *testing.T, b *irv1.Bundle) {
			shapeNest(edit(t, b, "post_tags", draft, nest, ""), "tag").Nullable = true
		}, "post_tag.dql.go", nil, true},
		{"create without insert", func(t *testing.T, b *irv1.Bundle) { dropQuery(b, "tag.insert") }, "post_tag.dql.go", nil, true},
		{"null child", func(t *testing.T, b *irv1.Bundle) {
			shapeNest(edit(t, b, "users", draft, nest, ""), "profile").Nullable = true
		}, "user.dql.go", nil, true},
		{"child missing a required field", func(t *testing.T, b *irv1.Bundle) {
			s := edit(t, b, "posts", draft, flat, "author")
			s.Fields = slices.DeleteFunc(s.GetFields(), func(f *irv1.Field) bool { return f.GetColumn() == "title" })
		}, "user.dql.go", nil, true},
		{"child optional where required", func(t *testing.T, b *irv1.Bundle) {
			shapeField(edit(t, b, "posts", draft, flat, "author"), "title").Presence = irv1.Presence_PRESENCE_OPTIONAL
		}, "user.dql.go", nil, true},
		{"junction without insert", func(t *testing.T, b *irv1.Bundle) {
			dropQuery(b, "post_tag.insert")
			for _, s := range []*irv1.Shape{edit(t, b, "tags", full, nest, ""), edit(t, b, "tags", full, nests, "")} {
				dropNest(s, "post_tags")
			}
		}, "tag.dql.go", nil, true},
		{"link without its junction relation", func(t *testing.T, b *irv1.Bundle) {
			for _, s := range []*irv1.Shape{edit(t, b, "tags", full, nest, ""), edit(t, b, "tags", full, nests, "")} {
				dropNest(s, "post_tags")
			}
		}, "tag.dql.go", nil, true},
		{"child required where optional", func(t *testing.T, b *irv1.Bundle) {
			shapeField(edit(t, b, "posts", draft, flat, "author"), "status").Presence = irv1.Presence_PRESENCE_REQUIRED
		}, "user.dql.go", []string{"Status: opt.Some(c.Status)"}, false},
		{"relations the draft leaves out", func(t *testing.T, b *irv1.Bundle) {
			for _, s := range []*irv1.Shape{edit(t, b, "tags", draft, nest, ""), edit(t, b, "tags", draft, nests, "")} {
				dropNest(s, "posts")
			}
			dropNest(edit(t, b, "users", draft, nest, ""), "profile")
		}, "tag.dql.go", []string{"far, err := postsStore.Get(ctx, PostKey{ID: child.PostID})"}, false},
		{"required foreign key", func(t *testing.T, b *irv1.Bundle) {
			shapeField(edit(t, b, "post_tags", draft, nest, ""), "post_id").Presence = irv1.Presence_PRESENCE_REQUIRED
		}, "post_tag.dql.go", []string{"draft.PostID = d.PostID"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := shopShapes(t)
			touched = nil
			c.mutate(t, b)
			b.GetShapes().Conversions = slices.DeleteFunc(b.GetShapes().GetConversions(), func(cv *irv1.Conversion) bool {
				return slices.ContainsFunc(touched, func(id *irv1.ShapeId) bool {
					return proto.Equal(id, cv.GetFrom()) || proto.Equal(id, cv.GetTo()) && cv.GetKind() != irv1.ConversionKind_CONVERSION_WITH_BASE
				})
			})
			files, codes := generate(t, b, Options{})
			if len(codes) != 0 {
				_, ds := Generate(b, Options{Package: "shopdb"})
				for _, d := range ds {
					t.Log(d.GetMessage())
				}
				t.Fatal(codes)
			}
			if c.file != "" && strings.Contains(files[c.file], ") InsertNested(") == c.absent {
				t.Errorf("%s: InsertNested present %v", c.file, !c.absent)
			}
			all := strings.Join([]string{files["post_tag.dql.go"], files["tag.dql.go"], files["user.dql.go"]}, "")
			for _, w := range c.want {
				if !strings.Contains(squash(all), squash(w)) {
					t.Errorf("lacks %q", w)
				}
			}
		})
	}
}

// table returns the shop bundle's table name.
func table(t *testing.T, b *irv1.Bundle, name string) *irv1.Table {
	t.Helper()
	for _, ns := range b.GetCatalogs()[0].GetNamespaces() {
		for _, tb := range ns.GetTables() {
			if tb.GetName().GetStored() == name {
				return tb
			}
		}
	}
	t.Fatalf("no table %s", name)
	return nil
}

func dropFragment(b *irv1.Bundle, id string) {
	fs := b.GetQueries().GetFragments()
	fs.Fragments = slices.DeleteFunc(fs.GetFragments(), func(f *irv1.Fragment) bool { return f.GetId() == id })
}

// §20.5: PatchNested is generated only where every relation of the Full
// Nested is one it reads back, and every change the Nested Partial allows
// is one it writes.
func TestPatchNestedPlans(t *testing.T) {
	const (
		part  = irv1.Preset_PRESET_PARTIAL
		full  = irv1.Preset_PRESET_FULL
		flat  = irv1.Form_FORM_FLAT
		nests = irv1.Form_FORM_NESTS
		nest  = irv1.Form_FORM_NESTED
	)
	replace := func(t *testing.T, b *irv1.Bundle, object, rel string) {
		shapeNest(edit(t, b, object, part, nest, ""), rel).GetPolicy().Collection = irv1.CollectionPolicy_COLLECTION_POLICY_REPLACE
	}
	dropNests := func(t *testing.T, b *irv1.Bundle, object, rel string) {
		for _, s := range []*irv1.Shape{edit(t, b, object, part, nest, ""), edit(t, b, object, part, nests, "")} {
			dropNest(s, rel)
		}
	}
	for _, c := range []struct {
		name    string
		mutate  func(t *testing.T, b *irv1.Bundle)
		file    string
		present bool
		want    []string
	}{
		{"owning key beyond the foreign key", func(t *testing.T, b *irv1.Bundle) {
			k := edit(t, b, "tenants", irv1.Preset_PRESET_KEY, 0, "")
			k.Fields = append(k.Fields, shapeField(presetShape(t, b, "tenants", full, flat, ""), "name"))
		}, "user.dql.go", false, nil},
		{"link without its junction relation", func(t *testing.T, b *irv1.Bundle) {
			for _, s := range []*irv1.Shape{edit(t, b, "tags", full, nest, ""), edit(t, b, "tags", full, nests, "")} {
				dropNest(s, "post_tags")
			}
		}, "tag.dql.go", false, nil},
		{"no order", func(t *testing.T, b *irv1.Bundle) { dropFragment(b, "order.asc") }, "user.dql.go", false, nil},
		{"foreign key without eq", func(t *testing.T, b *irv1.Bundle) {
			col := column(table(t, b, "posts"), "author_id")
			col.Operators = slices.DeleteFunc(col.GetOperators(), func(o *irv1.ColumnOperator) bool { return o.GetName() == "eq" })
		}, "user.dql.go", false, nil},
		{"owning update without patch", func(t *testing.T, b *irv1.Bundle) { dropQuery(b, "user.patch") }, "post.dql.go", false, nil},
		{"owning create without insert", func(t *testing.T, b *irv1.Bundle) { dropQuery(b, "user.insert") }, "post.dql.go", false, nil},
		{"link to a key the partial leaves out", func(t *testing.T, b *irv1.Bundle) {
			n := shapeNest(presetShape(t, b, "profiles", part, nest, ""), "user")
			for _, u := range b.GetShapes().GetUnions() {
				if proto.Equal(u.GetId(), n.GetUnion()) {
					u.Variants = append(u.Variants, &irv1.RelationVariant{Change: irv1.RelationChange_RELATION_CHANGE_LINK, Payload: presetShape(t, b, "users", irv1.Preset_PRESET_KEY, 0, "").GetId()})
				}
			}
		}, "profile.dql.go", false, nil},
		{"required to-one child", func(t *testing.T, b *irv1.Bundle) {
			shapeNest(edit(t, b, "users", full, nest, ""), "profile").Nullable = false
		}, "user.dql.go", false, nil},
		{"replaced to-one child", func(t *testing.T, b *irv1.Bundle) { replace(t, b, "users", "profile") }, "user.dql.go", false, nil},
		{"unlink without patch", func(t *testing.T, b *irv1.Bundle) {
			dropNests(t, b, "users", "posts_to_author")
			dropQuery(b, "post.patch")
		}, "user.dql.go", false, nil},
		{"unlink of a key the patch leaves out", func(t *testing.T, b *irv1.Bundle) {
			shapeNest(edit(t, b, "users", part, nest, ""), "profile").GetPolicy().Removal = irv1.Removal_REMOVAL_UNLINK
		}, "user.dql.go", false, nil},
		{"replace without delete_where", func(t *testing.T, b *irv1.Bundle) { dropQuery(b, "post.delete_where") }, "user.dql.go", false, nil},
		{"update of a field the patch leaves out", func(t *testing.T, b *irv1.Bundle) {
			s := edit(t, b, "posts", part, flat, "author")
			s.Fields = append(s.Fields, shapeField(presetShape(t, b, "posts", full, flat, ""), "slug"))
		}, "user.dql.go", false, nil},
		{"delete without delete", func(t *testing.T, b *irv1.Bundle) { dropQuery(b, "post.delete") }, "user.dql.go", false, nil},
		{"replace and unlink", func(t *testing.T, b *irv1.Bundle) { replace(t, b, "users", "posts_to_editor") }, "user.dql.go", true, []string{
			"var kept []PostKey",
			"where := dql.And(Posts.EditorID.Eq(row.ID))",
			"where = dql.And(where, dql.Not(dql.And(Posts.ID.Eq(k.ID))))",
			"postsStore.UpdateWhere(ctx, where, PostPartial{EditorID: opt.Some[*int64](nil)})",
		}},
		{"links the partial leaves out", func(t *testing.T, b *irv1.Bundle) { dropNests(t, b, "tags", "posts") }, "tag.dql.go", true, nil},
		{"link without insert_or_ignore", func(t *testing.T, b *irv1.Bundle) { dropQuery(b, "post_tag.insert_or_ignore.by_primary") }, "tag.dql.go", false, nil},
		{"unlink without delete", func(t *testing.T, b *irv1.Bundle) {
			dropNests(t, b, "tags", "post_tags")
			dropQuery(b, "post_tag.delete")
		}, "tag.dql.go", false, nil},
		{"replaced links without delete_where", func(t *testing.T, b *irv1.Bundle) {
			replace(t, b, "tags", "posts")
			dropQuery(b, "post_tag.delete_where")
		}, "tag.dql.go", false, nil},
		{"replaced links", func(t *testing.T, b *irv1.Bundle) { replace(t, b, "tags", "posts") }, "tag.dql.go", true, []string{
			"kept = append(kept, ch.key)",
			"kept = append(kept, PostKey{ID: related.ID})",
			"where := dql.And(PostTags.TagID.Eq(row.ID))",
			"where = dql.And(where, dql.Not(dql.And(PostTags.PostID.Eq(k.ID))))",
			"postTagsStore.DeleteWhere(ctx, where)",
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := shopShapes(t)
			touched = nil
			c.mutate(t, b)
			b.GetShapes().Conversions = slices.DeleteFunc(b.GetShapes().GetConversions(), func(cv *irv1.Conversion) bool {
				return slices.ContainsFunc(touched, func(id *irv1.ShapeId) bool {
					return proto.Equal(id, cv.GetFrom()) || proto.Equal(id, cv.GetTo()) && cv.GetKind() != irv1.ConversionKind_CONVERSION_WITH_BASE
				})
			})
			files, codes := generate(t, b, Options{})
			if len(codes) != 0 {
				_, ds := Generate(b, Options{Package: "shopdb"})
				for _, d := range ds {
					t.Log(d.GetMessage())
				}
				t.Fatal(codes)
			}
			if strings.Contains(files[c.file], "Writes) PatchNested(") != c.present {
				t.Errorf("%s: PatchNested present %v", c.file, !c.present)
			}
			for _, w := range c.want {
				if !strings.Contains(squash(files[c.file]), squash(w)) {
					t.Errorf("lacks %q", w)
				}
			}
		})
	}
}
