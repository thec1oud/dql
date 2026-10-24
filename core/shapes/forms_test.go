package shapes

import (
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/thec1oud/dql/core/bundle"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/testkit/golden"
)

// The forms of the running example, reviewed as a diff.
func TestShopForms(t *testing.T) {
	cats := shopCatalogs(t)
	shapes, unions, err := Forms(cats, flatShapes(t, cats, shopFlats))
	if err != nil {
		t.Fatal(err)
	}
	b, err := bundle.MarshalJSON(&irv1.ShapeLayer{Shapes: shapes, Unions: unions})
	if err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, filepath.Join("testdata", "forms", "shop.json"), b)
}

// The M2 gate: nested Drafts and Partials for user and post as §14.5 and
// §14.6 write them.
func TestShopNestedWritesMatchRecord(t *testing.T) {
	cats := shopCatalogs(t)
	shapes, unions, err := Forms(cats, flatShapes(t, cats, shopFlats))
	if err != nil {
		t.Fatal(err)
	}
	fields := func(s *irv1.Shape) string {
		var out []string
		for _, f := range s.GetFields() {
			p := "opt"
			if f.GetPresence() == irv1.Presence_PRESENCE_REQUIRED {
				p = "req"
			}
			if f.GetNullable() {
				p += "?"
			}
			out = append(out, f.GetColumn()+":"+p)
		}
		return strings.Join(out, " ")
	}
	nests := func(s *irv1.Shape) string {
		var out []string
		for _, n := range s.GetNests() {
			var target string
			if n.GetShape() != nil {
				target = shapeWords(t, shapes, n.GetShape())
			} else {
				target = strings.Join(findUnionByID(t, unions, n.GetUnion()), " ")
			}
			x := n.GetRelation() + "=" + target
			if len(n.GetSupplies()) > 0 {
				x += " supplied-by:" + strings.Join(n.GetSupplies(), ",")
			}
			if n.GetNeedsOne() {
				x += " needs-one"
			}
			if p := n.GetPolicy(); p != nil {
				x += " " + strings.ToLower(strings.TrimPrefix(p.GetCollection().String(), "COLLECTION_POLICY_"))
				if p.GetRemoval() != irv1.Removal_REMOVAL_UNSPECIFIED {
					x += "+" + strings.ToLower(strings.TrimPrefix(p.GetRemoval().String(), "REMOVAL_"))
				}
			}
			out = append(out, x)
		}
		return strings.Join(out, "; ")
	}
	for _, c := range []struct{ shape, fields, nests, forbidden string }{{
		shape:  "post draft nested",
		fields: "id:opt author_id:opt editor_id:opt? title:req status:opt price:opt? labels:opt? published_at:opt?",
		nests: "author=user ref supplied-by:author_id needs-one; editor=user ref supplied-by:editor_id; " +
			"post_tags=post tag draft for post; tags=tag link",
		forbidden: "slug=GENERATED",
	}, {
		shape:     "user draft nests",
		nests:     "posts_to_author=post draft for author; posts_to_editor=post draft for editor; profile=profile draft for user",
		forbidden: "rel:tenant=BOUND",
	}, {
		shape: "user partial nests",
		nests: "posts_to_author=post change for author replace+delete; posts_to_editor=post change for editor merge+unlink; " +
			"profile=profile change for user merge+delete",
		forbidden: "rel:tenant=BOUND",
	}, {
		shape:  "post partial nested",
		fields: "author_id:opt editor_id:opt? title:opt status:opt price:opt? labels:opt? published_at:opt?",
		nests: "author=post author change supplied-by:author_id; editor=post editor change supplied-by:editor_id; " +
			"post_tags=post tag change for post merge+delete; tags=post tags change merge",
		forbidden: "id=KEY slug=GENERATED",
	}, {
		shape:     "post draft for author",
		fields:    "id:opt editor_id:opt? title:req status:opt price:opt? labels:opt? published_at:opt?",
		forbidden: "author_id=PARENT slug=GENERATED",
	}, {
		shape:     "post partial for author",
		fields:    "editor_id:opt? title:opt status:opt price:opt? labels:opt? published_at:opt?",
		forbidden: "id=KEY author_id=PARENT slug=GENERATED",
	}} {
		s := findShape(t, shapes, c.shape)
		if got := fields(s); got != c.fields {
			t.Errorf("%s fields\n got %s\nwant %s", c.shape, got, c.fields)
		}
		if got := nests(s); got != c.nests {
			t.Errorf("%s nests\n got %s\nwant %s", c.shape, got, c.nests)
		}
		if got := forbiddenNames(s); got != c.forbidden {
			t.Errorf("%s forbidden\n got %s\nwant %s", c.shape, got, c.forbidden)
		}
	}
	for words, want := range map[string]string{
		"user ref":               "user ref link, user ref create",
		"tag link":               "tag link key, tag link create",
		"post change for author": "post create for author, post update for author, post delete for author",
		"post author change":     "post author link, post author create, post author update",
		"post editor change":     "post editor link, post editor create, post editor update, post editor unlink",
		"post tags change":       "post tags link, post tags create, post tags unlink",
	} {
		if got := variantWords(findUnion(t, unions, words)); got != want {
			t.Errorf("%s = %q, want %q", words, got, want)
		}
	}
}

func findUnionByID(t *testing.T, unions []*irv1.RelationUnion, id *irv1.RelationUnionId) []string {
	t.Helper()
	for _, u := range unions {
		if proto.Equal(u.GetId(), id) {
			return u.GetHostName().GetWords()
		}
	}
	t.Fatalf("no union %v", id)
	return nil
}

func shapeWords(t *testing.T, shapes []*irv1.Shape, id *irv1.ShapeId) string {
	t.Helper()
	for _, s := range shapes {
		if proto.Equal(s.GetId(), id) {
			return strings.Join(s.GetHostName().GetWords(), " ")
		}
	}
	t.Fatalf("no shape %v", id)
	return ""
}
