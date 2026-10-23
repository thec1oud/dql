package facts

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thec1oud/dql/core/bundle"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/testkit/determinism"
	"github.com/thec1oud/dql/testkit/golden"
)

func derivedShop(t *testing.T) *irv1.Catalog {
	t.Helper()
	c := shop()
	if ds := Derive([]*irv1.Catalog{c}, postgres); len(ds) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diagLines(ds))
	}
	return c
}

// The relation table of §4.5, complete: every relation of every entity.
func TestRunningExampleRelationTable(t *testing.T) {
	c := derivedShop(t)
	var got []string
	for _, ns := range c.GetNamespaces() {
		for _, tb := range ns.GetTables() {
			for _, r := range tb.GetRelations() {
				got = append(got, fmt.Sprintf("%s.%s %s via=%s through=%s optional=%v inverse_of=%s",
					tb.GetName().GetStored(), r.GetName().GetStored(),
					strings.TrimPrefix(r.GetKind().String(), "RELATION_KIND_"),
					r.GetVia().GetMember(), r.GetThrough().GetObject(), r.GetOptional(), r.GetInverseOf()))
			}
		}
	}
	want := []string{
		"post_tags.post MANY_TO_ONE via=post_tags_post_id_fkey through= optional=false inverse_of=",
		"post_tags.tag MANY_TO_ONE via=post_tags_tag_id_fkey through= optional=false inverse_of=",
		"posts.author MANY_TO_ONE via=posts_author_id_fkey through= optional=false inverse_of=",
		"posts.editor MANY_TO_ONE via=posts_editor_id_fkey through= optional=true inverse_of=",
		"posts.post_tags ONE_TO_MANY via=post_tags_post_id_fkey through= optional=false inverse_of=post",
		"posts.tags MANY_TO_MANY via= through=post_tags optional=false inverse_of=",
		"profiles.user ONE_TO_ONE via=profiles_user_id_fkey through= optional=false inverse_of=",
		"tags.post_tags ONE_TO_MANY via=post_tags_tag_id_fkey through= optional=false inverse_of=tag",
		"tags.posts MANY_TO_MANY via= through=post_tags optional=false inverse_of=",
		"tenants.users ONE_TO_MANY via=users_tenant_id_fkey through= optional=false inverse_of=tenant",
		"users.posts_to_author ONE_TO_MANY via=posts_author_id_fkey through= optional=false inverse_of=author",
		"users.posts_to_editor ONE_TO_MANY via=posts_editor_id_fkey through= optional=false inverse_of=editor",
		"users.profile INVERSE_TO_ONE via=profiles_user_id_fkey through= optional=true inverse_of=user",
		"users.tenant MANY_TO_ONE via=users_tenant_id_fkey through= optional=false inverse_of=",
	}
	if !slices.Equal(got, want) {
		t.Errorf("relations:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// §12.4.
func TestRunningExampleRelationIR(t *testing.T) {
	c := derivedShop(t)
	assertContains(t, relationOf(t, c, "posts", "author"), `{
	  "name": { "stored": "author", "words": ["author"] },
	  "owner": { "catalog": "shop", "namespace": "app", "object": "posts" },
	  "kind": "RELATION_KIND_MANY_TO_ONE",
	  "origin": "RELATION_ORIGIN_FOREIGN_KEY",
	  "via": { "catalog": "shop", "namespace": "app", "object": "posts", "member": "posts_author_id_fkey" },
	  "from": { "columns": ["author_id"] },
	  "to": { "table": { "catalog": "shop", "namespace": "app", "object": "users" }, "columns": ["id"] },
	  "optional": false
	}`)
	assertContains(t, relationOf(t, c, "users", "posts_to_author"), `{
	  "name": { "stored": "posts_to_author", "words": ["posts", "to", "author"] },
	  "owner": { "catalog": "shop", "namespace": "app", "object": "users" },
	  "kind": "RELATION_KIND_ONE_TO_MANY",
	  "origin": "RELATION_ORIGIN_FOREIGN_KEY",
	  "via": { "catalog": "shop", "namespace": "app", "object": "posts", "member": "posts_author_id_fkey" },
	  "inverseOf": "author",
	  "policy": { "collection": "COLLECTION_POLICY_REPLACE", "removal": "REMOVAL_DELETE" }
	}`)
	assertContains(t, relationOf(t, c, "posts", "tags"), `{
	  "name": { "stored": "tags", "words": ["tags"] },
	  "owner": { "catalog": "shop", "namespace": "app", "object": "posts" },
	  "kind": "RELATION_KIND_MANY_TO_MANY",
	  "origin": "RELATION_ORIGIN_INFERRED_JUNCTION",
	  "through": { "catalog": "shop", "namespace": "app", "object": "post_tags" },
	  "to": { "table": { "catalog": "shop", "namespace": "app", "object": "tags" }, "columns": ["id"] }
	}`)
	// Without annotations the policy is merge, and removal unlinks a nullable
	// foreign key and deletes otherwise (§12.4, §14.6).
	for _, tc := range []struct {
		object, name string
		want         string
	}{
		{"users", "posts_to_editor", `{"collection": "COLLECTION_POLICY_MERGE", "removal": "REMOVAL_UNLINK"}`},
		{"users", "profile", `{"collection": "COLLECTION_POLICY_MERGE", "removal": "REMOVAL_DELETE"}`},
		{"tenants", "users", `{"collection": "COLLECTION_POLICY_MERGE", "removal": "REMOVAL_DELETE"}`},
		{"tags", "posts", `{"collection": "COLLECTION_POLICY_MERGE"}`},
	} {
		got := relationOf(t, c, tc.object, tc.name).GetPolicy()
		assertContains(t, got, tc.want)
		if tc.name == "posts" && got.GetRemoval() != irv1.Removal_REMOVAL_UNSPECIFIED {
			t.Errorf("many-to-many removal = %v", got.GetRemoval())
		}
	}
	if p := relationOf(t, c, "posts", "author").GetPolicy(); p != nil {
		t.Errorf("forward relation has a policy: %v", p)
	}
}

// §7.6: the effective inverse-name names the relation, and the relation
// carries the annotation with what it superseded.
func TestRunningExampleInverseNameTrace(t *testing.T) {
	c := derivedShop(t)
	assertContains(t, relationOf(t, c, "users", "profile"), `{
	  "name": { "stored": "profile", "words": ["profile"] },
	  "kind": "RELATION_KIND_INVERSE_TO_ONE",
	  "via": { "catalog": "shop", "namespace": "app", "object": "profiles", "member": "profiles_user_id_fkey" },
	  "annotations": [{
	    "key": "core.inverse-name",
	    "value": { "words": ["profile"] },
	    "origin": { "kind": "ORIGIN_CONFIG", "plugin": "pg-live",
	      "location": "dql.yaml#annotations.shop.app.profiles.profiles_user_id_fkey" },
	    "superseded": [{ "key": "core.inverse-name", "value": { "words": ["user", "profile"] },
	      "origin": { "kind": "ORIGIN_DB_COMMENT", "plugin": "pg-live",
	        "location": "COMMENT ON CONSTRAINT profiles_user_id_fkey ON app.profiles" } }]
	  }]
	}`)
}

// §6.4 and §11.1, §11.2.
func TestRunningExampleKeys(t *testing.T) {
	c := derivedShop(t)
	view := tableOf(t, c, "published_posts")
	assertContains(t, view, `{
	  "name": { "stored": "published_posts", "words": ["published", "posts"] },
	  "kind": "RELATION_KIND_VIEW",
	  "logicalKey": { "columns": ["id"], "origin": "ORIGIN_DB_COMMENT" },
	  "dependsOn": [{ "catalog": "shop", "namespace": "app", "object": "posts" }]
	}`)
	if k := Key(view); !slices.Equal(k, []string{"id"}) {
		t.Errorf("view key = %v", k)
	}
	users := tableOf(t, c, "users")
	if k := Key(users); !slices.Equal(k, []string{"id"}) {
		t.Errorf("users key = %v", k)
	}
	assertContains(t, users, `{"uniqueness": [
	  { "name": "users_pkey", "kind": "UNIQUENESS_PRIMARY_KEY", "columns": ["id"], "nullsDistinct": true, "identifies": true },
	  { "name": "users_tenant_email_key", "kind": "UNIQUENESS_CONSTRAINT",
	    "columns": ["tenant_id", "email"], "nullsDistinct": true, "identifies": true }
	]}`)
}

// The whole derived catalog, pinned for review as a diff.
func TestRunningExampleGolden(t *testing.T) {
	js, err := bundle.MarshalJSON(derivedShop(t))
	if err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, filepath.Join("testdata", "shop.json"), js)
}

// §3.2: shuffled introspection results give byte-identical output.
func TestDeterministicUnderShuffledInput(t *testing.T) {
	want, _ := bundle.MarshalJSON(derivedShop(t))
	for seed := uint64(1); seed <= 20; seed++ {
		c := shop()
		ns := c.GetNamespaces()[0]
		ns.Tables = determinism.Shuffled(seed, ns.Tables)
		for _, tb := range ns.Tables {
			tb.ForeignKeys = determinism.Shuffled(seed, tb.ForeignKeys)
			tb.Uniqueness = determinism.Shuffled(seed, tb.Uniqueness)
		}
		if ds := Derive([]*irv1.Catalog{c}, postgres); len(ds) != 0 {
			t.Fatalf("seed %d: %v", seed, diagLines(ds))
		}
		got, _ := bundle.MarshalJSON(c)
		if string(got) != string(want) {
			t.Fatalf("seed %d: output differs", seed)
		}
	}
	err := determinism.Repeat(5, func() ([]byte, error) {
		c := shop()
		Derive([]*irv1.Catalog{c}, postgres)
		return bundle.MarshalJSON(c)
	})
	if err != nil {
		t.Fatal(err)
	}
}
