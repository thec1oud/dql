package facts

import (
	"slices"
	"strings"
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func postsWithAuthor(nullable bool, anns ...*irv1.Annotation) []*irv1.Table {
	name := "author_id"
	if nullable {
		name += "?"
	}
	p := table("posts", []string{"posts"}, col("id", i64(), "id"), col(name, i64(), "author", "id"))
	f := ref("posts_author_id_fkey", []string{"author_id"}, "users", "id")
	f.Annotations = anns
	p.ForeignKeys = []*irv1.ForeignKey{f}
	return []*irv1.Table{p, usersTable()}
}

// §14.6 policies from the collection-policy and removal annotations.
func TestPolicies(t *testing.T) {
	a := func(key, v string) *irv1.Annotation { return configured(key, "x", stringValue(v)) }
	for _, tc := range []struct {
		name     string
		nullable bool
		anns     []*irv1.Annotation
		want     string
		diags    []string
	}{
		{"default on a non-null FK", false, nil, `{"collection": "COLLECTION_POLICY_MERGE", "removal": "REMOVAL_DELETE"}`, nil},
		{"default on a nullable FK", true, nil, `{"collection": "COLLECTION_POLICY_MERGE", "removal": "REMOVAL_UNLINK"}`, nil},
		{"merge", false, []*irv1.Annotation{a(keyCollectionPolicy, "merge")}, `{"collection": "COLLECTION_POLICY_MERGE"}`, nil},
		{"append", false, []*irv1.Annotation{a(keyCollectionPolicy, "append")}, `{"collection": "COLLECTION_POLICY_APPEND"}`, nil},
		{"replace and delete", true, []*irv1.Annotation{a(keyCollectionPolicy, "replace"), a(keyRemoval, "delete")},
			`{"collection": "COLLECTION_POLICY_REPLACE", "removal": "REMOVAL_DELETE"}`, nil},
		{"unlink on a nullable FK", true, []*irv1.Annotation{a(keyRemoval, "unlink")}, `{"removal": "REMOVAL_UNLINK"}`, nil},
		{"unlink needs a nullable FK", false, []*irv1.Annotation{a(keyRemoval, "unlink")}, `{"removal": "REMOVAL_UNLINK"}`,
			[]string{"ERROR removal-unlink-not-nullable shop.app.posts.posts_author_id_fkey"}},
		{"invalid values", false, []*irv1.Annotation{a(keyCollectionPolicy, "keep"), a(keyRemoval, "drop")},
			`{"collection": "COLLECTION_POLICY_MERGE", "removal": "REMOVAL_DELETE"}`,
			[]string{"ERROR annotation-value-invalid shop.app.posts.posts_author_id_fkey", "ERROR annotation-value-invalid shop.app.posts.posts_author_id_fkey"}},
	} {
		c := catalogOf(postsWithAuthor(tc.nullable, tc.anns...)...)
		ds := diagLines(Derive([]*irv1.Catalog{c}, postgres))
		if !slices.Equal(ds, tc.diags) {
			t.Errorf("%s: diagnostics %v, want %v", tc.name, ds, tc.diags)
		}
		inv := relationOf(t, c, "users", "posts")
		assertContains(t, inv.GetPolicy(), tc.want)
		if len(inv.GetAnnotations()) != len(tc.anns) {
			t.Errorf("%s: inverse carries %d annotations", tc.name, len(inv.GetAnnotations()))
		}
	}
}

// §12.3: a composite FK under MATCH SIMPLE is present only when every column
// is non-null, so it is optional when any is nullable.
func TestCompositeForeignKeys(t *testing.T) {
	build := func(match irv1.ForeignKeyMatch, second string) *irv1.Catalog {
		p := table("members", []string{"members"}, col("tenant_id", i64(), "tenant", "id"), col(second, i64(), "user", "id"))
		p.Uniqueness = []*irv1.Uniqueness{uniq("members_user_key", "user_id"),
			// A partial index proves nothing (§11.2).
			{Name: "members_one_tenant", Kind: irv1.UniquenessKind_UNIQUENESS_INDEX, Columns: []string{"tenant_id"}, Predicate: &irv1.NativeText{Native: "true"}}}
		f := ref("members_user_fkey", []string{"tenant_id", "user_id"}, "users", "tenant_id", "id")
		f.Match = match
		p.ForeignKeys = []*irv1.ForeignKey{f}
		return catalogOf(p, usersTable(col("tenant_id", i64(), "tenant", "id")))
	}
	for _, tc := range []struct {
		name     string
		match    irv1.ForeignKeyMatch
		second   string
		optional bool
		kind     irv1.RelationKind
	}{
		{"simple, non-null", irv1.ForeignKeyMatch_FOREIGN_KEY_MATCH_SIMPLE, "user_id", false, irv1.RelationKind_RELATION_KIND_ONE_TO_ONE},
		{"simple, one nullable", irv1.ForeignKeyMatch_FOREIGN_KEY_MATCH_SIMPLE, "user_id?", true, irv1.RelationKind_RELATION_KIND_ONE_TO_ONE},
		// Under MATCH PARTIAL a row with user_id NULL still references by
		// tenant_id, so a nullable unique column proves nothing.
		{"partial, one nullable", irv1.ForeignKeyMatch_FOREIGN_KEY_MATCH_PARTIAL, "user_id?", true, irv1.RelationKind_RELATION_KIND_MANY_TO_ONE},
		{"partial, non-null", irv1.ForeignKeyMatch_FOREIGN_KEY_MATCH_PARTIAL, "user_id", false, irv1.RelationKind_RELATION_KIND_ONE_TO_ONE},
	} {
		c := build(tc.match, tc.second)
		if ds := Derive([]*irv1.Catalog{c}, postgres); len(ds) != 0 {
			t.Fatal(diagLines(ds))
		}
		r := relationOf(t, c, "members", "users")
		if r.GetOptional() != tc.optional || r.GetKind() != tc.kind {
			t.Errorf("%s: optional %v kind %v", tc.name, r.GetOptional(), r.GetKind())
		}
	}
}

func TestForeignKeyTargetMissing(t *testing.T) {
	ts := postsWithAuthor(false)
	c := catalogOf(ts[0])
	ds := Derive([]*irv1.Catalog{c}, postgres)
	if got := diagLines(ds); !slices.Equal(got, []string{"ERROR relation-target-missing shop.app.posts.posts_author_id_fkey"}) {
		t.Fatal(got)
	}
	if want := "foreign key `shop.app.posts.posts_author_id_fkey` references `shop.app.users`, which is not in the bundle; add its catalog or namespace to the sources"; ds[0].GetMessage() != want {
		t.Errorf("message %q", ds[0].GetMessage())
	}
	if names(c) != nil {
		t.Errorf("relations %v", names(c))
	}
}

// §5.3: billing.invoices.user_id points at shop.users without a foreign key.
func crossCatalog(namespace string, relation map[string]any) []*irv1.Catalog {
	users := usersTable()
	inv := table("invoices", []string{"invoices"}, col("id", i64(), "id"), col("user_id", i64(), "user", "id"), col("total", text(), "total"))
	inv.Uniqueness = []*irv1.Uniqueness{pk("invoices_pkey", "id")}
	inv.Columns[1].Annotations = []*irv1.Annotation{configured(keyRelation, "billing.invoices.user_id", structValue(relation))}
	cat := func(name string, t *irv1.Table) *irv1.Catalog {
		return &irv1.Catalog{Name: ident(name, name), DefaultNamespace: namespace,
			Namespaces: []*irv1.Namespace{{Name: ident(namespace), Tables: []*irv1.Table{t}}}}
	}
	return []*irv1.Catalog{cat("billing", inv), cat("shop", users)}
}

func TestAnnotatedCrossCatalogRelation(t *testing.T) {
	cs := crossCatalog("", map[string]any{"to": "shop.users.id", "name": "user", "inverse-name": []any{"invoices"}})
	if ds := Derive(cs, Options{Dialect: irv1.Dialect_DIALECT_MYSQL, CrossCatalogReferences: true}); len(ds) != 0 {
		t.Fatal(diagLines(ds))
	}
	// §5.3 shows from.table, but a ColumnSet omits the table where it is the
	// owner, as in §12.4, and annotated relations have the same form (§12.1).
	assertContains(t, relationOf(t, cs[0], "invoices", "user"), `{
	  "name": { "stored": "user", "words": ["user"] },
	  "origin": "RELATION_ORIGIN_ANNOTATED",
	  "from": { "columns": ["user_id"] },
	  "to": { "table": { "catalog": "shop", "namespace": "", "object": "users" }, "columns": ["id"] },
	  "kind": "RELATION_KIND_MANY_TO_ONE",
	  "crossCatalog": true
	}`)
	assertContains(t, relationOf(t, cs[1], "users", "invoices"), `{
	  "owner": { "catalog": "shop", "object": "users" },
	  "kind": "RELATION_KIND_ONE_TO_MANY",
	  "origin": "RELATION_ORIGIN_ANNOTATED",
	  "from": { "columns": ["id"] },
	  "to": { "table": { "catalog": "billing", "object": "invoices" }, "columns": ["user_id"] },
	  "inverseOf": "user",
	  "policy": { "collection": "COLLECTION_POLICY_MERGE", "removal": "REMOVAL_DELETE" },
	  "crossCatalog": true
	}`)
	if r := relationOf(t, cs[0], "invoices", "user"); r.GetVia() != nil {
		t.Errorf("annotated relation has via %v", r.GetVia())
	}
}

func TestCrossCatalogFailsWithoutCapability(t *testing.T) {
	cs := crossCatalog("public", map[string]any{"to": "shop.public.users.id", "name": "user", "inverse-name": "invoices"})
	ds := Derive(cs, postgres)
	if got := diagLines(ds); !slices.Equal(got, []string{"ERROR relation-cross-catalog billing.public.invoices.user"}) {
		t.Fatal(got)
	}
	want := "relation `billing.public.invoices.user` crosses catalogs, and dialect `postgres` cannot reference across catalogs; " +
		"remove the relation annotation on `billing.public.invoices.user_id` or keep both tables in one catalog"
	if ds[0].GetMessage() != want {
		t.Errorf("message %q", ds[0].GetMessage())
	}
}

func TestCrossCatalogJunction(t *testing.T) {
	ts := junctionTables()
	tags := &irv1.Catalog{Name: ident("tagging", "tagging"), DefaultNamespace: "app",
		Namespaces: []*irv1.Namespace{{Name: ident("app", "app"), Tables: []*irv1.Table{ts[1]}}}}
	ts[2].ForeignKeys[1].References.Table.Catalog = "tagging"
	ds := diagLines(Derive([]*irv1.Catalog{catalogOf(ts[0], ts[2]), tags}, postgres))
	want := []string{
		"ERROR relation-cross-catalog shop.app.post_tags.tag",
		"ERROR relation-cross-catalog shop.app.posts.tags",
		"ERROR relation-cross-catalog tagging.app.tags.posts",
	}
	if !slices.Equal(ds, want) {
		t.Errorf("got %v", ds)
	}
}

func TestAnnotatedRelationKinds(t *testing.T) {
	inv := table("invoices", []string{"invoices"}, col("id", i64(), "id"), col("user_id?", i64(), "user", "id"))
	inv.Uniqueness = []*irv1.Uniqueness{pk("invoices_pkey", "id"), uniq("invoices_user_key", "user_id")}
	inv.Columns[1].Annotations = []*irv1.Annotation{configured(keyRelation, "x", structValue(map[string]any{"to": "shop.app.users.id"}))}
	c := catalogOf(inv, usersTable())
	if ds := Derive([]*irv1.Catalog{c}, postgres); len(ds) != 0 {
		t.Fatal(diagLines(ds))
	}
	assertContains(t, relationOf(t, c, "invoices", "user"), `{"kind": "RELATION_KIND_ONE_TO_ONE", "optional": true}`)
	assertContains(t, relationOf(t, c, "users", "invoices"), `{"kind": "RELATION_KIND_INVERSE_TO_ONE", "optional": true,
	  "policy": {"collection": "COLLECTION_POLICY_MERGE", "removal": "REMOVAL_UNLINK"}}`)
}

func TestAnnotatedRelationErrors(t *testing.T) {
	at := "shop.app.invoices.user_id"
	for _, tc := range []struct {
		name  string
		value *irv1.AnnotationValue
		code  string
		msg   string
	}{
		{"not a struct", stringValue("shop.app.users.id"), CodeRelationAnnotationInvalid, "is not a struct"},
		{"unknown field", structValue(map[string]any{"to": "shop.app.users.id", "cardinality": "one"}), CodeRelationAnnotationInvalid, "has unknown field cardinality"},
		{"empty to", structValue(map[string]any{"to": ""}), CodeRelationAnnotationInvalid, "has an invalid to"},
		{"bad name", structValue(map[string]any{"to": "shop.app.users.id", "name": "Two Words"}), CodeRelationAnnotationInvalid, "has an invalid name"},
		{"bad inverse-name", structValue(map[string]any{"to": "shop.app.users.id", "inverse-name": []any{1.0}}), CodeRelationAnnotationInvalid, "has an invalid inverse-name"},
		{"bad name kind", structValue(map[string]any{"to": "shop.app.users.id", "name": 1.0}), CodeRelationAnnotationInvalid, "has an invalid name"},
		{"two-part to", structValue(map[string]any{"to": "users.id"}), CodeRelationAnnotationInvalid, "needs to: catalog.namespace.table.column"},
		{"missing table", structValue(map[string]any{"to": "shop.app.people.id"}), CodeRelationTargetMissing, "references `shop.app.people`, which is not in the bundle"},
		{"missing column", structValue(map[string]any{"to": "shop.app.users.uid"}), CodeRelationAnnotationInvalid, "references column `shop.app.users.uid`, which does not exist"},
		{"through", structValue(map[string]any{"to": "shop.app.users.id", "through": "shop.app.x"}), CodeRelationThroughUnsupported, "names through"},
	} {
		inv := table("invoices", []string{"invoices"}, col("id", i64(), "id"), col("user_id", i64(), "user", "id"))
		inv.Columns[1].Annotations = []*irv1.Annotation{configured(keyRelation, "x", tc.value)}
		c := catalogOf(inv, usersTable())
		ds := Derive([]*irv1.Catalog{c}, postgres)
		if len(ds) != 1 || ds[0].GetCode() != tc.code || dotted(ds[0].GetTarget()) != at || !strings.Contains(ds[0].GetMessage(), tc.msg) {
			t.Errorf("%s: %v", tc.name, ds)
		}
		if names(c) != nil {
			t.Errorf("%s: relations %v", tc.name, names(c))
		}
	}
}

func TestSortDiagnostics(t *testing.T) {
	q := func(o string) *irv1.QualifiedName { return &irv1.QualifiedName{Catalog: "c", Object: o} }
	ds := []*irv1.Diagnostic{
		{Severity: irv1.Severity_SEVERITY_WARNING, Code: "a", Target: q("a")},
		{Severity: irv1.Severity_SEVERITY_ERROR, Code: "b", Target: q("b"), Message: "2"},
		{Severity: irv1.Severity_SEVERITY_ERROR, Code: "b", Target: q("b"), Message: "1"},
		{Severity: irv1.Severity_SEVERITY_ERROR, Code: "a", Target: q("b")},
		{Severity: irv1.Severity_SEVERITY_ERROR, Code: "z", Target: q("a")},
	}
	SortDiagnostics(ds)
	var got []string
	for _, d := range ds {
		got = append(got, d.GetSeverity().String()[9:]+" "+d.GetTarget().GetObject()+" "+d.GetCode()+d.GetMessage())
	}
	want := []string{"ERROR a z", "ERROR b a", "ERROR b b1", "ERROR b b2", "WARNING a a"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v", got)
	}
}

func TestDialectName(t *testing.T) {
	for d, want := range map[irv1.Dialect]string{
		irv1.Dialect_DIALECT_POSTGRES:  "postgres",
		irv1.Dialect_DIALECT_SQLSERVER: "sqlserver",
		irv1.Dialect_DIALECT_MYSQL:     "mysql",
	} {
		if got := dialectName(d); got != want {
			t.Errorf("%v: %s", d, got)
		}
	}
}
