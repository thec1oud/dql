package shapes

import (
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/thec1oud/dql/core/bundle"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/testkit/determinism"
)

// layerOf builds the shape layer of the running example with the custom
// shapes of §14.8, failing on any diagnostic.
func layerOf(t *testing.T, catalogs []*irv1.Catalog) *irv1.ShapeLayer {
	t.Helper()
	layer, ds, err := Layer(catalogs, customRecord())
	if err != nil || len(ds) != 0 {
		t.Fatalf("Layer: %v %v", err, ds)
	}
	return layer
}

// spellings are the dotted spellings of layer's shapes, in order.
func spellings(catalogs []*irv1.Catalog, layer *irv1.ShapeLayer) []string {
	n := NewNester(catalogs, nil)
	var out []string
	for _, s := range layer.GetShapes() {
		out = append(out, n.spell(s.GetId()))
	}
	return out
}

func TestLayer(t *testing.T) {
	catalogs := shop(t)
	layer := layerOf(t, catalogs)
	got := spellings(catalogs, layer)
	if !slices.IsSorted(got) || len(slices.Compact(slices.Clone(got))) != len(got) {
		t.Errorf("shapes are not sorted once each: %v", got)
	}
	for _, want := range []string{
		"user.full.flat", "user.draft.nested", "user.key", "user.scope", "published_post.full.nests",
		"post.draft.flat.for.author", "post.partial.flat.for.editor",
		"post_with_tags", "quick_invite", "signup", "user_summary", "user_summary_named",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("no shape %s in %v", want, got)
		}
	}
	// A view gets only Full and Key (§6.4, §14.3).
	if slices.Contains(got, "published_post.draft.flat") {
		t.Error("published_post has a Draft")
	}
	var unions []string
	for _, u := range layer.GetUnions() {
		unions = append(unions, strings.Join(u.GetHostName().GetWords(), "_"))
	}
	if !slices.IsSorted(unions) || !slices.Contains(unions, "user_ref") {
		t.Errorf("unions %v", unions)
	}
	if len(layer.GetUpserts()) == 0 || layer.GetConversions() != nil || layer.GetMerges() != nil {
		t.Errorf("upserts %d, conversions %v, merges %v", len(layer.GetUpserts()), layer.GetConversions(), layer.GetMerges())
	}
}

// A custom write shape nesting a preset adds the Ref and Link unions the
// forms already have; the layer lists each once.
func TestLayerListsSharedUnionsOnce(t *testing.T) {
	catalogs := shop(t)
	plain, _, err := Layer(catalogs, nil)
	if err != nil {
		t.Fatal(err)
	}
	nested, ds, err := Layer(catalogs, []Custom{{Name: "post_by_author", From: "post.draft", Nest: map[string]string{"author": "user.draft"}}})
	if err != nil || len(ds) != 0 {
		t.Fatalf("Layer: %v %v", err, ds)
	}
	if len(nested.GetShapes()) != len(plain.GetShapes())+1 {
		t.Errorf("%d shapes, want %d", len(nested.GetShapes()), len(plain.GetShapes())+1)
	}
	if !proto.Equal(&irv1.ShapeLayer{Unions: nested.GetUnions()}, &irv1.ShapeLayer{Unions: plain.GetUnions()}) {
		t.Errorf("unions differ:\n%v\n%v", nested.GetUnions(), plain.GetUnions())
	}
}

func TestLayerReportsCustomShapes(t *testing.T) {
	layer, ds, err := Layer(shop(t), []Custom{{Name: "quick_invite", From: "user.draft", Pick: []string{"email"}}})
	if err != nil || len(ds) != 1 || ds[0].GetCode() != CodeWriteShapeIncomplete {
		t.Fatalf("%v %v", err, ds)
	}
	if slices.Contains(spellings(shop(t), layer), "quick_invite") {
		t.Error("the failed shape is in the layer")
	}
}

func TestLayerFailsOnUnassembledCatalogs(t *testing.T) {
	catalogs := customCatalogs()
	// posts' relations name users, which these catalogs lack.
	catalogs[0].Namespaces[0].Tables = catalogs[0].Namespaces[0].Tables[1:2]
	if _, _, err := Layer(catalogs, nil); err == nil || !strings.Contains(err.Error(), "without a flat Full") {
		t.Errorf("err %v", err)
	}
}

// TestLayerDeterministic builds the layer repeatedly, and from catalogs
// whose tables, relations and uniqueness facts come in shuffled order, and
// checks the bytes stay the same (guide §6.2).
func TestLayerDeterministic(t *testing.T) {
	gen := func(seed uint64) func() ([]byte, error) {
		return func() ([]byte, error) {
			catalogs := shop(t)
			if seed != 0 {
				for _, ns := range catalogs[0].GetNamespaces() {
					ns.Tables = determinism.Shuffled(seed, ns.Tables)
					for _, tb := range ns.Tables {
						tb.Relations = determinism.Shuffled(seed+1, tb.Relations)
						tb.Uniqueness = determinism.Shuffled(seed+2, tb.Uniqueness)
					}
				}
			}
			return bundle.MarshalJSON(layerOf(t, catalogs))
		}
	}
	if err := determinism.Repeat(3, gen(0)); err != nil {
		t.Error(err)
	}
	want, _ := gen(0)()
	for seed := uint64(1); seed <= 5; seed++ {
		if got, _ := gen(seed)(); string(got) != string(want) {
			t.Errorf("seed %d: the layer depends on input order", seed)
		}
	}
}

// TestRef resolves references as dql.yaml writes them; one that names no
// shape comes back as written, in a custom id that is in no layer.
func TestRef(t *testing.T) {
	catalogs := shop(t)
	layer := layerOf(t, catalogs)
	n := NewNester(catalogs, nil)
	for _, c := range []struct {
		ref, want string
		found     bool
	}{
		{"user.full", "user.full.flat", true},
		{"user.draft.nested", "user.draft.nested", true},
		{"user.key", "user.key", true},
		{"post.draft.for.author", "post.draft.flat.for.author", true},
		{"user_summary", "user_summary", true},
		{"user.fulll", "user.fulll", false},
		{"published_post.draft", "published_post.draft", false},
		{"nobody.full", "nobody.full", false},
	} {
		id := Ref(catalogs, layer, c.ref)
		if got := n.spell(id); got != c.want {
			t.Errorf("Ref(%s) = %s, want %s", c.ref, got, c.want)
		}
		found := slices.ContainsFunc(layer.GetShapes(), func(s *irv1.Shape) bool { return s.GetId() == id })
		if found != c.found {
			t.Errorf("Ref(%s) found %v", c.ref, found)
		}
	}
}
