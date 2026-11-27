package shapes

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// A catalog built to reach the cases the running example does not: views
// with and without a Key, a keyless child, append policies, a foreign key a
// Draft cannot hold, and a child whose forward relation is missing.
func edgeCatalog() []*irv1.Catalog {
	text := &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}}
	cols := func(names ...string) []*irv1.Column {
		var out []*irv1.Column
		for i, n := range names {
			c := &irv1.Column{Name: &irv1.Identifier{Stored: strings.TrimSuffix(n, "?"), Words: strings.Split(strings.TrimSuffix(n, "?"), "_")},
				Ordinal: uint32(i + 1), Type: text}
			c.Nullable = strings.HasSuffix(n, "?")
			out = append(out, c)
		}
		return out
	}
	tab := func(name string, host bool, kind irv1.TableKind, c []*irv1.Column, rels ...*irv1.Relation) *irv1.Table {
		t := &irv1.Table{Name: &irv1.Identifier{Stored: name, Words: []string{name}}, Kind: kind, Columns: c, Relations: rels}
		if host {
			t.HostName = &irv1.Identifier{Words: []string{name}}
		}
		return t
	}
	table, view := irv1.TableKind_RELATION_KIND_TABLE, irv1.TableKind_RELATION_KIND_VIEW
	merge := &irv1.RelationPolicy{Collection: irv1.CollectionPolicy_COLLECTION_POLICY_MERGE, Removal: irv1.Removal_REMOVAL_DELETE}
	appendPolicy := &irv1.RelationPolicy{Collection: irv1.CollectionPolicy_COLLECTION_POLICY_APPEND, Removal: irv1.Removal_REMOVAL_DELETE}
	return []*irv1.Catalog{{
		Name: &irv1.Identifier{Stored: "c"},
		Namespaces: []*irv1.Namespace{{
			Name: &irv1.Identifier{Stored: "n"},
			Tables: []*irv1.Table{
				tab("a", true, table, cols("id", "b_id?", "g_id", "h_id", "v_id?", "nv_id?", "nn_id"),
					edgeRel("b", irv1.RelationKind_RELATION_KIND_MANY_TO_ONE, "b_id", "b", "id", true, "", nil),
					edgeRel("cs", irv1.RelationKind_RELATION_KIND_ONE_TO_MANY, "id", "cc", "a_id", false, "a", appendPolicy),
					edgeRel("g", irv1.RelationKind_RELATION_KIND_MANY_TO_ONE, "g_id", "b", "id", false, "", nil),
					edgeRel("h", irv1.RelationKind_RELATION_KIND_MANY_TO_ONE, "h_id", "b", "id", false, "", nil),
					edgeRel("mn", irv1.RelationKind_RELATION_KIND_MANY_TO_MANY, "id", "nk", "id", false, "", merge),
					edgeRel("nn", irv1.RelationKind_RELATION_KIND_MANY_TO_ONE, "nn_id", "nk", "id", false, "", nil),
					edgeRel("ks", irv1.RelationKind_RELATION_KIND_ONE_TO_MANY, "id", "k", "a_id", false, "missing_link", merge),
					edgeRel("ms", irv1.RelationKind_RELATION_KIND_MANY_TO_MANY, "id", "b", "id", false, "",
						&irv1.RelationPolicy{Collection: irv1.CollectionPolicy_COLLECTION_POLICY_APPEND}),
					edgeRel("nv", irv1.RelationKind_RELATION_KIND_MANY_TO_ONE, "nv_id", "nk", "id", true, "", nil),
					edgeRel("v", irv1.RelationKind_RELATION_KIND_MANY_TO_ONE, "v_id", "v", "id", true, "", nil),
					edgeRel("vs", irv1.RelationKind_RELATION_KIND_ONE_TO_MANY, "id", "vv", "a_id", false, "a", merge),
				),
				tab("b", true, table, cols("id")),
				tab("cc", true, table, cols("id", "a_id"),
					edgeRel("a", irv1.RelationKind_RELATION_KIND_MANY_TO_ONE, "a_id", "a", "id", false, "", nil)),
				tab("k", false, table, cols("a_id", "x")),
				tab("nk", true, view, cols("id")),
				tab("v", true, view, cols("id")),
				tab("vv", true, view, cols("id", "a_id")),
			},
		}},
	}}
}

func edgeRel(name string, kind irv1.RelationKind, from, to, toCol string, optional bool, inverseOf string, policy *irv1.RelationPolicy) *irv1.Relation {
	return &irv1.Relation{
		Name: &irv1.Identifier{Stored: name, Words: []string{name}}, Kind: kind,
		From:     &irv1.ColumnSet{Columns: []string{from}},
		To:       &irv1.ColumnSet{Table: edgeQN(to), Columns: []string{toCol}},
		Optional: optional, InverseOf: inverseOf, Policy: policy,
	}
}

func edgeQN(object string) *irv1.QualifiedName {
	return &irv1.QualifiedName{Catalog: "c", Namespace: "n", Object: object}
}

var edgeFlats = map[string][]string{
	"a":  {"id R O KEY K", "b_id R O O", "g_id R GENERATED GENERATED", "h_id R GENERATED GENERATED", "v_id R O O", "nv_id R O O", "nn_id R R O"},
	"b":  {"id R R KEY K"},
	"cc": {"id R R KEY K", "a_id R R O"},
	"k":  {"a_id R R O", "x R O O"},
	"nk": {"id R - -"},
	"v":  {"id R - - K"},
	"vv": {"id R - - K", "a_id R - -"},
}

func edgeForms(t *testing.T) ([]*irv1.Shape, []*irv1.RelationUnion) {
	t.Helper()
	cats := edgeCatalog()
	shapes, unions, err := Forms(cats, flatShapes(t, cats, edgeFlats))
	if err != nil {
		t.Fatal(err)
	}
	return shapes, unions
}

func findShape(t *testing.T, shapes []*irv1.Shape, words string) *irv1.Shape {
	t.Helper()
	for _, s := range shapes {
		if strings.Join(s.GetHostName().GetWords(), " ") == words {
			return s
		}
	}
	t.Fatalf("no shape %q", words)
	return nil
}

func findUnion(t *testing.T, unions []*irv1.RelationUnion, words string) *irv1.RelationUnion {
	t.Helper()
	for _, u := range unions {
		if strings.Join(u.GetHostName().GetWords(), " ") == words {
			return u
		}
	}
	t.Fatalf("no union %q", words)
	return nil
}

// variantWords lists a union's variants by host words.
func variantWords(u *irv1.RelationUnion) string {
	var out []string
	for _, v := range u.GetVariants() {
		out = append(out, strings.Join(v.GetHostName().GetWords(), " "))
	}
	return strings.Join(out, ", ")
}

func nestNames(s *irv1.Shape) string {
	var out []string
	for _, n := range s.GetNests() {
		out = append(out, n.GetRelation())
	}
	return strings.Join(out, " ")
}

func forbiddenNames(s *irv1.Shape) string {
	var out []string
	for _, f := range s.GetForbidden() {
		name := f.GetColumn()
		if name == "" {
			name = "rel:" + f.GetRelation()
		}
		out = append(out, name+"="+strings.TrimPrefix(f.GetReason().String(), "FORBIDDEN_"))
	}
	return strings.Join(out, " ")
}

func TestDraftNestsOfEdgeCases(t *testing.T) {
	shapes, unions := edgeForms(t)
	d := findShape(t, shapes, "a draft nested")
	// nv targets a view without a Key: no change can be written. vs has a
	// view child: no Draft for it. g's foreign key is generated, so the
	// Draft cannot hold it.
	if got, want := nestNames(d), "b cs ks ms v"; got != want {
		t.Errorf("nests = %q, want %q", got, want)
	}
	// nn and mn reach a view without a Key either. Relations no change can
	// write are listed as not writable.
	if got, want := forbiddenNames(d), "g_id=GENERATED h_id=GENERATED rel:g=GENERATED rel:h=GENERATED "+
		"rel:mn=NOT_WRITABLE rel:nn=NOT_WRITABLE rel:nv=NOT_WRITABLE rel:vs=NOT_WRITABLE"; got != want {
		t.Errorf("forbidden = %q, want %q", got, want)
	}
	// A Ref to a view links only: views have no Draft.
	if got, want := variantWords(findUnion(t, unions, "v ref")), "v ref link"; got != want {
		t.Errorf("v ref = %q, want %q", got, want)
	}
	// k has no host name, so its words come from its name; its forward
	// relation is missing, so the relation's words come from its stored name.
	if got := findShape(t, shapes, "k draft for missing link"); forbiddenNames(got) != "a_id=PARENT" {
		t.Errorf("k for = %q", forbiddenNames(got))
	}
}

func TestPartialNestsOfEdgeCases(t *testing.T) {
	shapes, unions := edgeForms(t)
	p := findShape(t, shapes, "a partial nested")
	// g's foreign key cannot be set, so only Update is left. b, nv and v
	// are nullable and unlink; nv's view has no Key, so it only unlinks. vs
	// has a view child, which no change can write.
	if got, want := nestNames(p), "b cs g h ks ms nv v"; got != want {
		t.Errorf("nests = %q, want %q", got, want)
	}
	if got, want := forbiddenNames(p), "id=KEY g_id=GENERATED h_id=GENERATED rel:mn=NOT_WRITABLE rel:nn=NOT_WRITABLE rel:vs=NOT_WRITABLE"; got != want {
		t.Errorf("forbidden = %q, want %q", got, want)
	}
	// The Nests form lists them too.
	if got, want := forbiddenNames(findShape(t, shapes, "a partial nests")), "rel:mn=NOT_WRITABLE rel:nn=NOT_WRITABLE rel:vs=NOT_WRITABLE"; got != want {
		t.Errorf("nests forbidden = %q, want %q", got, want)
	}
	for words, want := range map[string]string{
		"a b change":  "a b link, a b create, a b update, a b unlink",
		"a g change":  "a g update",
		"a v change":  "a v link, a v unlink",
		"a nv change": "a nv unlink",
		"a ms change": "a ms link, a ms create",
		// append: Create only.
		"cc change for a": "cc create for a",
		// k has no Key, so its children cannot be found to update or delete.
		"k change for missing link": "k create for missing link",
	} {
		if got := variantWords(findUnion(t, unions, words)); got != want {
			t.Errorf("%s = %q, want %q", words, got, want)
		}
	}
	for _, n := range p.GetNests() {
		if n.GetRelation() == "g" && len(n.GetSupplies()) != 0 {
			t.Errorf("g supplies %v, but its foreign key cannot be set", n.GetSupplies())
		}
	}
}

func TestFormsRejectsForeignInput(t *testing.T) {
	cats := edgeCatalog()
	flat := flatShapes(t, cats, edgeFlats)
	stray := proto.Clone(flat[0]).(*irv1.Shape)
	stray.Id.Base = &irv1.ShapeId_Entity{Entity: edgeQN("gone")}
	if _, _, err := Forms(cats, append(flat, stray)); err == nil || !strings.Contains(err.Error(), "not in the catalogs") {
		t.Errorf("err = %v", err)
	}
	var noB []*irv1.Shape
	for _, s := range flat {
		if s.GetEntity().GetObject() != "b" {
			noB = append(noB, s)
		}
	}
	if _, _, err := Forms(cats, noB); err == nil || !strings.Contains(err.Error(), "without a flat Full") {
		t.Errorf("err = %v", err)
	}
}

// Forms derives forms only from flat presets: custom and relation-qualified
// shapes in its input are left alone.
func TestFormsSkipsOtherShapes(t *testing.T) {
	cats := edgeCatalog()
	flat := flatShapes(t, cats, edgeFlats)
	custom := &irv1.Shape{Id: &irv1.ShapeId{Base: &irv1.ShapeId_Custom{Custom: "x"}}}
	child := proto.Clone(flat[0]).(*irv1.Shape)
	child.Id.ForRelation = "b"
	a, _, err := Forms(cats, flat)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := Forms(cats, append(flat, custom, child))
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != len(b) {
		t.Errorf("%d shapes, want %d", len(b), len(a))
	}
}

// Every shape and union appears once, in the order of §3.2, whatever order
// the flat presets arrive in.
func TestFormsAreDeterministic(t *testing.T) {
	cats := shopCatalogs(t)
	flat := flatShapes(t, cats, shopFlats)
	want, wantU, _ := Forms(cats, flat)
	rev := make([]*irv1.Shape, len(flat))
	for i, s := range flat {
		rev[len(flat)-1-i] = s
	}
	got, gotU, _ := Forms(cats, rev)
	if !proto.Equal(&irv1.ShapeLayer{Shapes: got, Unions: gotU}, &irv1.ShapeLayer{Shapes: want, Unions: wantU}) {
		t.Error("forms depend on the order of their input")
	}
}
