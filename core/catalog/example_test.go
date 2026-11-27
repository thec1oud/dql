package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/thec1oud/dql/core/bundle"
	pgextv1 "github.com/thec1oud/dql/proto/dql/ext/postgres/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/testkit/determinism"
	"github.com/thec1oud/dql/testkit/golden"
)

func assembledShop(t *testing.T) *irv1.Bundle {
	t.Helper()
	b, ds := Assemble([]SourceOutput{shopSource()}, shopOptions)
	if len(ds) != 0 || b == nil {
		t.Fatalf("Assemble(shop) = %v", lines(ds))
	}
	return b
}

func shopNS(t *testing.T, b *irv1.Bundle) *irv1.Namespace { return namespace(t, b, "shop", "app") }

// §3.1: the start of the running example's bundle.
func TestShopBundleStart(t *testing.T) {
	b := assembledShop(t)
	assertContains(t, b, `{
	  "version": { "major": 1, "minor": 0, "patch": 0 },
	  "requiredFeatures": ["opaque-types"],
	  "dialect": "DIALECT_POSTGRES",
	  "catalogs": [
	    {
	      "name": { "stored": "shop", "words": ["shop"] },
	      "provenance": {
	        "source": "pg-live",
	        "sourceVersion": "1.0.0",
	        "inputFingerprint": "sha256:9f2c…",
	        "fidelity": "SOURCE_FIDELITY_FULL"
	      }
	    }
	  ]
	}`)
	if len(b.GetDiagnostics()) != 0 {
		t.Errorf("diagnostics = %v", lines(b.GetDiagnostics()))
	}
}

// §6.4: the view with nonnull assertions and a logical key from comments.
func TestShopView(t *testing.T) {
	v := tableOf(t, shopNS(t, assembledShop(t)), "published_posts")
	assertContains(t, v, `{
	  "name": { "stored": "published_posts", "words": ["published", "posts"] },
	  "kind": "RELATION_KIND_VIEW",
	  "columns": [
	    { "name": { "stored": "id", "words": ["id"] }, "ordinal": 1,
	      "type": { "int64": {} }, "nullable": false,
	      "nullabilitySource": "NULLABILITY_SOURCE_ANNOTATION" },
	    { "name": { "stored": "title", "words": ["title"] }, "ordinal": 2,
	      "type": { "text": {} }, "nullable": false,
	      "nullabilitySource": "NULLABILITY_SOURCE_ANNOTATION" },
	    { "name": { "stored": "author_id", "words": ["author", "id"] }, "ordinal": 3,
	      "type": { "int64": {} }, "nullable": true,
	      "nullabilitySource": "NULLABILITY_SOURCE_UNPROVEN" },
	    { "name": { "stored": "published_at", "words": ["published", "at"] }, "ordinal": 4,
	      "type": { "instant": { "precision": 6 } }, "nullable": true,
	      "nullabilitySource": "NULLABILITY_SOURCE_UNPROVEN" }
	  ],
	  "logicalKey": { "columns": ["id"], "origin": "ORIGIN_DB_COMMENT" },
	  "dependsOn": [{ "catalog": "shop", "namespace": "app", "object": "posts" }]
	}`)
	// §4.4: the name annotation gives PublishedPostFull.
	assertContains(t, v, `{"hostName": { "words": ["published", "post"] }}`)
}

// §7.5: password_hash carries both comment annotations, sorted by key, and
// keeps the raw comment as an extension.
func TestShopPasswordHash(t *testing.T) {
	c := columnOf(t, tableOf(t, shopNS(t, assembledShop(t)), "users"), "password_hash")
	assertContains(t, c, `{
	  "name": { "stored": "password_hash", "words": ["password", "hash"] },
	  "ordinal": 5,
	  "type": { "text": {} },
	  "nullable": false,
	  "annotations": [
	    { "key": "core.sensitive",
	      "origin": { "kind": "ORIGIN_DB_COMMENT", "plugin": "pg-live",
	                  "location": "COMMENT ON COLUMN app.users.password_hash" } },
	    { "key": "core.write-only",
	      "origin": { "kind": "ORIGIN_DB_COMMENT", "plugin": "pg-live",
	                  "location": "COMMENT ON COLUMN app.users.password_hash" } }
	  ]
	}`)
	var raw pgextv1.Comment
	if len(c.GetExtensions()) != 1 || c.GetExtensions()[0].UnmarshalTo(&raw) != nil || raw.GetText() != "@write-only @sensitive" {
		t.Errorf("extensions = %v", c.GetExtensions())
	}
}

// §7.6: config overrides the comment, which is kept as superseded; the
// comment's user_profile is split into words by the core.
func TestShopInverseNameTrace(t *testing.T) {
	r := relationOf(t, tableOf(t, shopNS(t, assembledShop(t)), "users"), "profile")
	assertContains(t, r, `{
	  "name": { "stored": "profile", "words": ["profile"] },
	  "kind": "RELATION_KIND_INVERSE_TO_ONE",
	  "via": { "catalog": "shop", "namespace": "app", "object": "profiles", "member": "profiles_user_id_fkey" },
	  "annotations": [
	    {
	      "key": "core.inverse-name",
	      "value": { "words": ["profile"] },
	      "origin": { "kind": "ORIGIN_CONFIG", "plugin": "pg-live",
	                  "location": "dql.yaml#annotations.shop.app.profiles.profiles_user_id_fkey" },
	      "superseded": [
	        { "key": "core.inverse-name", "value": { "words": ["user", "profile"] },
	          "origin": { "kind": "ORIGIN_DB_COMMENT", "plugin": "pg-live",
	                      "location": "COMMENT ON CONSTRAINT profiles_user_id_fkey ON app.profiles" } }
	      ]
	    }
	  ]
	}`)
}

// §12.4, with the replace and delete policy from config (§1.3).
func TestShopRelations(t *testing.T) {
	ns := shopNS(t, assembledShop(t))
	assertContains(t, relationOf(t, tableOf(t, ns, "posts"), "author"), `{
	  "name": { "stored": "author", "words": ["author"] },
	  "owner": { "catalog": "shop", "namespace": "app", "object": "posts" },
	  "kind": "RELATION_KIND_MANY_TO_ONE",
	  "origin": "RELATION_ORIGIN_FOREIGN_KEY",
	  "via": { "catalog": "shop", "namespace": "app", "object": "posts", "member": "posts_author_id_fkey" },
	  "from": { "columns": ["author_id"] },
	  "to": { "table": { "catalog": "shop", "namespace": "app", "object": "users" }, "columns": ["id"] },
	  "optional": false
	}`)
	assertContains(t, relationOf(t, tableOf(t, ns, "users"), "posts_to_author"), `{
	  "name": { "stored": "posts_to_author", "words": ["posts", "to", "author"] },
	  "owner": { "catalog": "shop", "namespace": "app", "object": "users" },
	  "kind": "RELATION_KIND_ONE_TO_MANY",
	  "origin": "RELATION_ORIGIN_FOREIGN_KEY",
	  "via": { "catalog": "shop", "namespace": "app", "object": "posts", "member": "posts_author_id_fkey" },
	  "inverseOf": "author",
	  "policy": { "collection": "COLLECTION_POLICY_REPLACE", "removal": "REMOVAL_DELETE" }
	}`)
	assertContains(t, relationOf(t, tableOf(t, ns, "posts"), "tags"), `{
	  "name": { "stored": "tags", "words": ["tags"] },
	  "owner": { "catalog": "shop", "namespace": "app", "object": "posts" },
	  "kind": "RELATION_KIND_MANY_TO_MANY",
	  "origin": "RELATION_ORIGIN_INFERRED_JUNCTION",
	  "through": { "catalog": "shop", "namespace": "app", "object": "post_tags" },
	  "to": { "table": { "catalog": "shop", "namespace": "app", "object": "tags" }, "columns": ["id"] }
	}`)
	// §4.5's relation table, by owner and name.
	var got []string
	for _, tb := range ns.GetTables() {
		for _, r := range tb.GetRelations() {
			got = append(got, tb.GetName().GetStored()+"."+r.GetName().GetStored())
		}
	}
	want := []string{
		"post_tags.post", "post_tags.tag",
		"posts.author", "posts.editor", "posts.post_tags", "posts.tags",
		"profiles.user",
		"tags.post_tags", "tags.posts",
		"tenants.users",
		"users.posts_to_author", "users.posts_to_editor", "users.profile", "users.tenant",
	}
	if !slices.Equal(got, want) {
		t.Errorf("relations = %v", got)
	}
}

// §6.5 and §4.6: overloads with signatures and host names.
func TestShopOverloads(t *testing.T) {
	ns := shopNS(t, assembledShop(t))
	assertContains(t, ns.GetRoutines()[0], `{
	  "name": { "stored": "post_score", "words": ["post", "score"] },
	  "overloads": [
	    {
	      "signature": ["int64"],
	      "hostName": { "words": ["post", "score", "int64"] },
	      "kind": "ROUTINE_KIND_FUNCTION",
	      "arguments": [
	        { "name": { "stored": "p_id", "words": ["p", "id"] },
	          "type": { "int64": {} }, "mode": "ARG_MODE_IN", "hasDefault": false }
	      ],
	      "returns": { "scalar": { "type": { "decimal": {} }, "nullable": true } },
	      "nullBehavior": "NULL_BEHAVIOR_STRICT",
	      "volatility": "VOLATILITY_STABLE",
	      "annotations": [{ "key": "core.public", "origin": { "kind": "ORIGIN_DB_COMMENT" } }]
	    },
	    { "signature": ["int64", "int32"], "hostName": { "words": ["post", "score", "int64", "int32"] } }
	  ]
	}`)
}

// The other catalog examples of the record that the shop holds.
func TestShopTypesAndFacts(t *testing.T) {
	ns := shopNS(t, assembledShop(t))
	users, posts := tableOf(t, ns, "users"), tableOf(t, ns, "posts")
	// §8.2.
	assertContains(t, columnOf(t, users, "display_name"), `{"type": {
	  "varchar": { "length": 80, "unit": "LENGTH_UNIT_CHARACTERS" },
	  "native": { "name": { "namespace": "pg_catalog", "object": "varchar" },
	              "modifiers": [{ "int": 80 }], "raw": "character varying(80)" }
	}}`)
	// §8.3.
	assertContains(t, columnOf(t, users, "nickname"), `{"type": {
	  "opaque": {},
	  "native": { "name": { "namespace": "public", "object": "citext" }, "raw": "citext" }
	}}`)
	// §8.4.
	assertContains(t, ns.GetEnums()[0], `{
	  "name": { "stored": "post_status", "words": ["post", "status"] },
	  "labels": ["draft", "published", "archived"],
	  "origin": "ENUM_ORIGIN_NATIVE",
	  "hostName": { "words": ["post", "status"] }
	}`)
	// §8.5; domains are sorted by stored name.
	assertContains(t, ns.GetDomains()[0], `{
	  "name": { "stored": "email_address", "words": ["email", "address"] },
	  "base": { "text": {} },
	  "nullable": true,
	  "constraints": [
	    { "name": "email_address_check",
	      "portable": { "length": { "max": 320, "unit": "LENGTH_UNIT_CHARACTERS" } } }
	  ]
	}`)
	// §8.5: shop.app.money_amount.amount: { nonnull } narrows the attribute.
	assertContains(t, ns.GetComposites()[0], `{"attributes": [
	  { "name": { "stored": "amount", "words": ["amount"] }, "nullable": false,
	    "nullabilitySource": "NULLABILITY_SOURCE_ANNOTATION", "hostName": { "words": ["amount"] } },
	  { "name": { "stored": "currency", "words": ["currency"] }, "nullable": true,
	    "nullabilitySource": "NULLABILITY_SOURCE_UNPROVEN" }
	]}`)
	// §8.6.
	assertContains(t, columnOf(t, posts, "labels"), `{
	  "type": {
	    "array": { "element": { "text": {} }, "elementNullable": true, "dimensions": 1 },
	    "native": { "name": { "namespace": "pg_catalog", "object": "_text" }, "raw": "text[]" }
	  },
	  "nullable": true
	}`)
	// §7.7: a plugin annotation travels untouched.
	assertContains(t, columnOf(t, posts, "published_at"), `{"annotations": [
	  { "key": "x.ts.time-type", "value": { "string": "date" } }
	]}`)
	// §4.1.
	assertContains(t, posts.GetForeignKeys()[0], `{
	  "name": { "stored": "posts_author_id_fkey", "words": ["posts", "author", "id", "fkey"] },
	  "columns": ["author_id"],
	  "references": {
	    "table": { "catalog": "shop", "namespace": "app", "object": "users" },
	    "columns": ["id"]
	  }
	}`)
	// §11.2.
	assertContains(t, users, `{"uniqueness": [
	  { "name": "users_pkey", "kind": "UNIQUENESS_PRIMARY_KEY", "columns": ["id"], "nullsDistinct": true, "identifies": true },
	  { "name": "users_tenant_email_key", "kind": "UNIQUENESS_CONSTRAINT",
	    "columns": ["tenant_id", "email"], "nullsDistinct": true, "identifies": true }
	]}`)
	// §11.3.
	assertContains(t, columnOf(t, users, "id"), `{ "name": { "stored": "id", "words": ["id"] },
	  "default": { "class": "DEFAULT_IDENTITY_ALWAYS",
	               "sequence": { "catalog": "shop", "namespace": "app", "object": "users_id_seq" } } }`)
	assertContains(t, columnOf(t, posts, "status"), `{ "name": { "stored": "status", "words": ["status"] },
	  "default": { "class": "DEFAULT_CONSTANT", "native": "'draft'::app.post_status",
	               "value": { "string": "draft" } } }`)
	assertContains(t, columnOf(t, users, "created_at"), `{ "name": { "stored": "created_at", "words": ["created", "at"] },
	  "default": { "class": "DEFAULT_EXPRESSION", "native": "now()" } }`)
	assertContains(t, columnOf(t, posts, "slug"), `{ "name": { "stored": "slug", "words": ["slug"] },
	  "generation": { "kind": "GENERATION_STORED",
	                  "native": "lower(replace(title, ' '::text, '-'::text))" } }`)
	// §6.2: the trigger extension passes through unchanged.
	var trig pgextv1.Trigger
	if len(users.GetExtensions()) != 1 || users.GetExtensions()[0].UnmarshalTo(&trig) != nil || trig.GetName() != "users_touch_updated_at" {
		t.Errorf("extensions = %v", users.GetExtensions())
	}
}

// §4.3 and §4.4: host names of the running example, after the name
// annotations; app is the default namespace, so nothing is prefixed.
func TestShopHostNames(t *testing.T) {
	ns := shopNS(t, assembledShop(t))
	var got []string
	for _, tb := range ns.GetTables() {
		got = append(got, fmt.Sprintf("%s=%v", tb.GetName().GetStored(), tb.GetHostName().GetWords()))
	}
	for _, s := range ns.GetSequences() {
		got = append(got, fmt.Sprintf("%s=%v", s.GetName().GetStored(), s.GetHostName().GetWords()))
	}
	want := []string{
		"post_tags=[post tag]", "posts=[post]", "profiles=[profile]", "published_posts=[published post]",
		"tags=[tag]", "tenants=[tenant]", "users=[user]",
		"posts_id_seq=[posts id seq]", "tags_id_seq=[tags id seq]", "users_id_seq=[users id seq]",
	}
	if !slices.Equal(got, want) {
		t.Errorf("host names = %v", got)
	}
	if w := columnOf(t, tableOf(t, ns, "users"), "display_name").GetHostName(); !proto.Equal(w, &irv1.Identifier{Words: []string{"display", "name"}}) {
		t.Errorf("column host name = %v", w)
	}
}

// The whole assembled bundle, pinned for review as a diff (guide §6.1).
func TestShopGolden(t *testing.T) {
	js, err := bundle.MarshalJSON(assembledShop(t))
	if err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, filepath.Join("testdata", "shop.json"), js)
}

// shuffledShop is the shop with every object list and annotation list in an
// order fixed by seed (§3.2, guide §6.2). Columns and arguments keep the
// database's order.
func shuffledShop(seed uint64) SourceOutput {
	src := shopSource()
	c := src.Catalogs[0]
	ns := c.GetNamespaces()[0]
	ns.Tables = determinism.Shuffled(seed, ns.Tables)
	ns.Domains = determinism.Shuffled(seed, ns.Domains)
	ns.Sequences = determinism.Shuffled(seed, ns.Sequences)
	ns.Routines[0].Overloads = determinism.Shuffled(seed, ns.Routines[0].Overloads)
	for _, tb := range ns.Tables {
		tb.ForeignKeys = determinism.Shuffled(seed, tb.ForeignKeys)
		tb.Uniqueness = determinism.Shuffled(seed, tb.Uniqueness)
		tb.Constraints = determinism.Shuffled(seed, tb.Constraints)
		for _, col := range tb.Columns {
			col.Annotations = determinism.Shuffled(seed, col.Annotations)
		}
	}
	for k, anns := range src.Config {
		src.Config[k] = determinism.Shuffled(seed, anns)
	}
	return src
}

func TestShopDeterministicUnderShuffledInput(t *testing.T) {
	want, _ := bundle.MarshalJSON(assembledShop(t))
	for seed := uint64(1); seed <= 20; seed++ {
		b, ds := Assemble([]SourceOutput{shuffledShop(seed)}, shopOptions)
		if len(ds) != 0 {
			t.Fatalf("seed %d: %v", seed, lines(ds))
		}
		got, _ := bundle.MarshalJSON(b)
		if string(got) != string(want) {
			t.Fatalf("seed %d: output differs", seed)
		}
	}
	err := determinism.Repeat(5, func() ([]byte, error) {
		b, _ := Assemble([]SourceOutput{shopSource()}, shopOptions)
		return bundle.MarshalJSON(b)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// §3.2 rule 4: the test binary prints the assembled bundle under each
// environment of determinism.Environments, Turkish among them, and the
// outputs must be identical.
func TestShopIgnoresLocale(t *testing.T) {
	const helper = "DQL_CATALOG_PRINT_SHOP=1"
	if os.Getenv("DQL_CATALOG_PRINT_SHOP") == "1" {
		js, _ := bundle.MarshalJSON(assembledShop(t))
		fmt.Print(string(js))
		return
	}
	var envs [][]string
	for _, env := range determinism.Environments {
		envs = append(envs, append(slices.Clone(env), helper))
	}
	if err := determinism.Command(envs, os.Args[0], "-test.run=^TestShopIgnoresLocale$"); err != nil {
		t.Fatal(err)
	}
}

// Assemble does not modify its inputs.
func TestAssembleLeavesInputs(t *testing.T) {
	src := shopSource()
	before := proto.CloneOf(src.Catalogs[0])
	cfg := proto.CloneOf(src.Config["shop.app.users"][0])
	Assemble([]SourceOutput{src}, shopOptions)
	if !proto.Equal(src.Catalogs[0], before) || !proto.Equal(src.Config["shop.app.users"][0], cfg) {
		t.Error("Assemble modified its input")
	}
}
