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
