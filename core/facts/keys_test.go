package facts

import (
	"slices"
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func catalogOf(tables ...*irv1.Table) *irv1.Catalog {
	return &irv1.Catalog{
		Name: ident("shop", "shop"), DefaultNamespace: "app",
		Namespaces: []*irv1.Namespace{{Name: ident("app", "app"), Tables: tables}},
	}
}

func derive(tables ...*irv1.Table) []*irv1.Diagnostic {
	return Derive([]*irv1.Catalog{catalogOf(tables...)}, postgres)
}

func TestIdentifies(t *testing.T) {
	tb := table("t", []string{"t"}, col("a", i64(), "a"), col("b?", i64(), "b"))
	for _, tc := range []struct {
		name string
		u    *irv1.Uniqueness
		want bool
	}{
		{"non-null", uniq("k", "a"), true},
		{"nullable column", uniq("k", "a", "b"), false},
		{"unknown column", uniq("k", "zz"), false},
		{"no columns", uniq("k"), false},
		{"partial", &irv1.Uniqueness{Columns: []string{"a"}, Predicate: &irv1.NativeText{Native: "a > 0"}}, false},
		{"expression", &irv1.Uniqueness{Columns: []string{"a"}, Expressions: []*irv1.IndexExpression{{Position: 1, Native: "lower(b)"}}}, false},
	} {
		if got := Identifies(tb, tc.u); got != tc.want {
			t.Errorf("%s: identifies = %v", tc.name, got)
		}
	}
}

// §11.2: identifies is filled on every fact, partial indexes never identify.
func TestDeriveFillsIdentifies(t *testing.T) {
	posts := table("posts", []string{"posts"}, col("id", i64(), "id"), col("slug?", text(), "slug"))
	posts.Uniqueness = []*irv1.Uniqueness{
		{Name: "posts_one_published_slug", Kind: irv1.UniquenessKind_UNIQUENESS_INDEX, Columns: []string{"slug"},
			Predicate: &irv1.NativeText{Native: "(status = 'published'::app.post_status)"}, NullsDistinct: true, Identifies: true},
		pk("posts_pkey", "id"),
	}
	if ds := derive(posts); len(ds) != 0 {
		t.Fatal(diagLines(ds))
	}
	assertContains(t, posts, `{"uniqueness": [
	  { "name": "posts_one_published_slug", "kind": "UNIQUENESS_INDEX", "columns": ["slug"],
	    "predicate": { "native": "(status = 'published'::app.post_status)" },
	    "nullsDistinct": true, "identifies": false },
	  { "name": "posts_pkey", "identifies": true }
	]}`)
}

func TestKey(t *testing.T) {
	withPK := table("t", []string{"t"}, col("id", i64(), "id"))
	withPK.Uniqueness = []*irv1.Uniqueness{uniq("u", "id"), pk("p", "id")}
	if k := Key(withPK); !slices.Equal(k, []string{"id"}) {
		t.Errorf("primary key: %v", k)
	}
	none := table("t", []string{"t"}, col("id", i64(), "id"))
	if k := Key(none); k != nil {
		t.Errorf("no key: %v", k)
	}
	logical := table("t", []string{"t"}, col("id", i64(), "id"), col("n?", i64(), "n"))
	logical.LogicalKey = &irv1.LogicalKey{Columns: []string{"id"}}
	if k := Key(logical); !slices.Equal(k, []string{"id"}) {
		t.Errorf("logical key: %v", k)
	}
	for _, bad := range []string{"n", "zz"} {
		logical.LogicalKey = &irv1.LogicalKey{Columns: []string{bad}}
		if k := Key(logical); k != nil {
			t.Errorf("invalid logical key %s: %v", bad, k)
		}
	}
}

func TestLogicalKeys(t *testing.T) {
	view := func() *irv1.Table {
		v := table("v", []string{"v"}, col("id", i64(), "id"), col("n?", i64(), "n"))
		v.Kind = irv1.TableKind_RELATION_KIND_VIEW
		return v
	}
	ann := func(tb *irv1.Table, v *irv1.AnnotationValue) *irv1.Table {
		tb.Annotations = []*irv1.Annotation{commented(keyLogicalKey, "COMMENT ON VIEW v", v)}
		return tb
	}
	tableNoPK := func() *irv1.Table {
		return table("t", []string{"t"}, col("id", i64(), "id"), col("n?", i64(), "n"))
	}
	covered := tableNoPK()
	covered.Uniqueness = []*irv1.Uniqueness{uniq("t_id_key", "id")}
	withPK := tableNoPK()
	withPK.Uniqueness = []*irv1.Uniqueness{pk("t_pkey", "id")}
	preset := view()
	preset.LogicalKey = &irv1.LogicalKey{Columns: []string{"id"}, Origin: irv1.OriginKind_ORIGIN_CONFIG}
	ann(preset, listValue("n"))

	for _, tc := range []struct {
		name string
		tb   *irv1.Table
		want []string
	}{
		{"view assertion, no uniqueness needed", ann(view(), listValue("id")), nil},
		{"string value", ann(view(), stringValue("id")), nil},
		{"words value", ann(view(), wordsValue("id")), nil},
		{"source-set key is kept", preset, nil},
		{"table key covered by a unique constraint", ann(covered, listValue("id")), nil},
		{"table key covered by nothing", ann(tableNoPK(), listValue("id")), []string{"WARNING logical-key-not-unique shop.app.t"}},
		{"nullable column", ann(view(), listValue("id", "n")), []string{"ERROR logical-key-nullable shop.app.v.n"}},
		{"unknown column", ann(view(), listValue("zz")), []string{"ERROR logical-key-unknown-column shop.app.v"}},
		{"table with a primary key", ann(withPK, listValue("id")), []string{"ERROR logical-key-with-primary-key shop.app.t"}},
		{"no value", ann(view(), nil), []string{"ERROR annotation-value-invalid shop.app.v"}},
		{"empty string", ann(view(), stringValue("")), []string{"ERROR annotation-value-invalid shop.app.v"}},
		{"non-string list item", ann(view(), &irv1.AnnotationValue{Kind: &irv1.AnnotationValue_List{List: mustList(1.0)}}), []string{"ERROR annotation-value-invalid shop.app.v"}},
	} {
		got := diagLines(derive(tc.tb))
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
	if !slices.Equal(preset.GetLogicalKey().GetColumns(), []string{"id"}) {
		t.Errorf("source-set key replaced: %v", preset.GetLogicalKey())
	}
}
