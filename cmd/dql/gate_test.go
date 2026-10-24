package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/thec1oud/dql/core/bundle"
	pgextv1 "github.com/thec1oud/dql/proto/dql/ext/postgres/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/fidelity"
	"github.com/thec1oud/dql/testkit/golden"
)

// This file is the M1 gate (implementation guide §4 M1): dql generate on
// testdata/shop, the running example (§1.3), produces a golden catalog IR
// that matches every catalog example of the design record about shop.
//
// Examples are compared by containment: the output holds every field an
// example shows, and fields it elides are not compared (guide §6.1). Where
// an example lists one element of a longer list, that element is compared
// on its own. Examples about another dialect (§5.3, §8.4's SQL Server
// enum, §8.7, §8.9, §8.10, §9) are not about shop and are checked by
// core/catalog and the dialect plugins' own tests; the shape and query
// examples (§7.5 steps 4 to 6, §14 onwards) belong to later milestones.
//
// What the examples say that the output cannot match, and why:
//
//   - §3.1 lists requiredFeatures ["opaque-types"], but §1.3 declares no
//     opaque column; the feature appears with §8.3's citext column, which
//     TestGateRecordExtras adds. Its provenance shows the placeholder
//     fingerprint "sha256:9f2c…" and "FIDELITY_FULL", which the IR spells
//     SOURCE_FIDELITY_FULL, the enum provenance has of its own.
//   - §7.6 locates the config annotation in rel.yaml; the project file is
//     dql.yaml.
//   - §10.4 writes posts_published_check's native text as
//     "CHECK (published_at IS NULL OR status <> 'draft'::app.post_status)";
//     Postgres's deparse, which pg-live keeps exactly, parenthesizes every
//     operand. It also lists posts_title_check first, while §3.2 rule 2
//     sorts constraints by name.
//   - §8.3 writes citext's raw native type as "citext", which assumes
//     public on the search_path; pg-live reads with an empty one, so that
//     every spelling qualifies what is outside pg_catalog, as §10.4's and
//     §11.3's 'draft'::app.post_status does, and gets "public.citext".
//   - §6.2's trigger, §8.3's column and §7.7's and §8.5's config are not in
//     §1.3; they are checked on the database and config testdata/extras.*
//     add.

// generateBundle runs dql generate with the config at path on the database
// at url and returns the bundle it wrote, in canonical JSON and decoded.
func generateBundle(t *testing.T, path, url string) ([]byte, *irv1.Bundle) {
	t.Helper()
	t.Setenv("SHOP_DATABASE_URL", url)
	out := filepath.Join(t.TempDir(), "bundle.json")
	if code, _, stderr := dql("generate", "-config", path, "-bundle", out); code != 0 {
		t.Fatalf("dql generate: %d\n%s", code, stderr)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	b := &irv1.Bundle{}
	if err := bundle.UnmarshalJSON(raw, b); err != nil {
		t.Fatal(err)
	}
	return raw, b
}

// assertContains fails unless got holds every field of want, a design
// record example in protobuf JSON.
func assertContains(t *testing.T, what string, got proto.Message, want string) {
	t.Helper()
	w := got.ProtoReflect().New().Interface()
	if err := protojson.Unmarshal([]byte(want), w); err != nil {
		t.Fatalf("%s: the example does not decode: %v", what, err)
	}
	var gm, wm any
	gb, _ := protojson.Marshal(got)
	wb, _ := protojson.Marshal(w)
	_ = json.Unmarshal(gb, &gm)
	_ = json.Unmarshal(wb, &wm)
	if path, ok := subset(wm, gm, "$"); !ok {
		t.Errorf("%s: the output differs from the example at %s\n got: %s\nwant: %s", what, path, gb, wb)
	}
}

func subset(want, got any, path string) (string, bool) {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return path, false
		}
		for k, v := range w {
			if p, ok := subset(v, g[k], path+"."+k); !ok {
				return p, false
			}
		}
		return "", true
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return path, false
		}
		for i := range w {
			if p, ok := subset(w[i], g[i], fmt.Sprintf("%s[%d]", path, i)); !ok {
				return p, false
			}
		}
		return "", true
	}
	return path, reflect.DeepEqual(want, got)
}

// named returns the element of list whose name is name, or fails.
func named[T any](t *testing.T, list []T, name func(T) string, want string) T {
	t.Helper()
	for _, e := range list {
		if name(e) == want {
			return e
		}
	}
	t.Fatalf("no %s", want)
	var zero T
	return zero
}

func tableOf(t *testing.T, ns *irv1.Namespace, name string) *irv1.Table {
	t.Helper()
	return named(t, ns.GetTables(), func(e *irv1.Table) string { return e.GetName().GetStored() }, name)
}

func columnOf(t *testing.T, tb *irv1.Table, name string) *irv1.Column {
	t.Helper()
	return named(t, tb.GetColumns(), func(e *irv1.Column) string { return e.GetName().GetStored() }, name)
}

func relationOf(t *testing.T, tb *irv1.Table, name string) *irv1.Relation {
	t.Helper()
	return named(t, tb.GetRelations(), func(e *irv1.Relation) string { return e.GetName().GetStored() }, name)
}

// TestGateGolden pins the running example's bundle in two golden files: the
// catalog layer in catalog.json, as the bundle without its shape layer, and
// the shape layer in shapes.json (guide §6.1).
func TestGateGolden(t *testing.T) {
	_, b := generateBundle(t, filepath.Join(shopDir, "dql.yaml"), shopURL)
	layer := b.GetShapes()
	b.Shapes = nil
	for path, m := range map[string]proto.Message{"catalog.json": b, "shapes.json": layer} {
		js, err := bundle.MarshalJSON(m)
		if err != nil {
			t.Fatal(err)
		}
		golden.Assert(t, filepath.Join(shopDir, path), js)
	}
	if len(b.GetDiagnostics()) != 0 || len(b.GetRequiredFeatures()) != 0 {
		t.Errorf("diagnostics %v, required features %v", b.GetDiagnostics(), b.GetRequiredFeatures())
	}
}

func TestGateRecordExamples(t *testing.T) {
	_, b := generateBundle(t, filepath.Join(shopDir, "dql.yaml"), shopURL)
	ns := b.GetCatalogs()[0].GetNamespaces()[0]
	users, posts := tableOf(t, ns, "users"), tableOf(t, ns, "posts")

	// §3.1, without the feature and with the IR's spellings (see above).
	assertContains(t, "§3.1", b, `{
	  "version": { "major": 1, "minor": 0, "patch": 0 },
	  "dialect": "DIALECT_POSTGRES",
	  "catalogs": [
	    {
	      "name": { "stored": "shop", "words": ["shop"] },
	      "provenance": {
	        "source": "pg-live",
	        "sourceVersion": "1.0.0",
	        "fidelity": "SOURCE_FIDELITY_FULL"
	      }
	    }
	  ]
	}`)
	if fp := b.GetCatalogs()[0].GetProvenance().GetInputFingerprint(); !strings.HasPrefix(fp, "sha256:") {
		t.Errorf("§3.1: fingerprint %q", fp)
	}

	// §4.1.
	assertContains(t, "§4.1", named(t, posts.GetForeignKeys(), func(f *irv1.ForeignKey) string { return f.GetName().GetStored() }, "posts_author_id_fkey"), `{
	  "name": { "stored": "posts_author_id_fkey", "words": ["posts", "author", "id", "fkey"] },
	  "columns": ["author_id"],
	  "references": {
	    "table": { "catalog": "shop", "namespace": "app", "object": "users" },
	    "columns": ["id"]
	  }
	}`)

	// §4.3, §4.4: host names after the name annotations; app is the default
	// namespace and shop the only catalog, so nothing is prefixed.
	var hosts []string
	for _, tb := range ns.GetTables() {
		hosts = append(hosts, tb.GetName().GetStored()+"="+strings.Join(tb.GetHostName().GetWords(), " "))
	}
	if want := []string{"post_tags=post tag", "posts=post", "profiles=profile", "published_posts=published post",
		"tags=tag", "tenants=tenant", "users=user"}; !slices.Equal(hosts, want) {
		t.Errorf("§4.3: host names %v", hosts)
	}

	// §4.5: every relation of the table, by owner and name.
	var rels []string
	for _, tb := range ns.GetTables() {
		for _, r := range tb.GetRelations() {
			rels = append(rels, tb.GetName().GetStored()+"."+r.GetName().GetStored())
		}
	}
	if want := []string{
		"post_tags.post", "post_tags.tag",
		"posts.author", "posts.editor", "posts.post_tags", "posts.tags",
		"profiles.user",
		"tags.post_tags", "tags.posts",
		"tenants.users",
		"users.posts_to_author", "users.posts_to_editor", "users.profile", "users.tenant",
	}; !slices.Equal(rels, want) {
		t.Errorf("§4.5: relations %v", rels)
	}

	// §4.6, §6.5: the example shows the first overload; the second is
	// compared by its signature and host name.
	assertContains(t, "§6.5", ns.GetRoutines()[0], `{
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

	// §6.4.
	assertContains(t, "§6.4", tableOf(t, ns, "published_posts"), `{
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

	// §7.5 step 3, and step 2's raw comment kept as an extension.
	passwordHash := columnOf(t, users, "password_hash")
	assertContains(t, "§7.5", passwordHash, `{
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
	if !slices.ContainsFunc(passwordHash.GetExtensions(), func(a *anypb.Any) bool {
		var c pgextv1.Comment
		return a.UnmarshalTo(&c) == nil && c.GetText() == "@write-only @sensitive"
	}) {
		t.Errorf("§7.5: extensions %v", passwordHash.GetExtensions())
	}

	// §7.6, located in dql.yaml (see above).
	assertContains(t, "§7.6", relationOf(t, users, "profile"), `{
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

	// §8.2.
	assertContains(t, "§8.2", columnOf(t, users, "display_name"), `{"type": {
	  "varchar": { "length": 80, "unit": "LENGTH_UNIT_CHARACTERS" },
	  "native": {
	    "name": { "namespace": "pg_catalog", "object": "varchar" },
	    "modifiers": [{ "int": 80 }],
	    "raw": "character varying(80)"
	  }
	}}`)

	// §8.4.
	assertContains(t, "§8.4", ns.GetEnums()[0], `{
	  "name": { "stored": "post_status", "words": ["post", "status"] },
	  "labels": ["draft", "published", "archived"],
	  "origin": "ENUM_ORIGIN_NATIVE"
	}`)

	// §8.5; without §8.5's config the composite's attributes stay nullable.
	assertContains(t, "§8.5", named(t, ns.GetDomains(), func(d *irv1.Domain) string { return d.GetName().GetStored() }, "email_address"), `{
	  "name": { "stored": "email_address", "words": ["email", "address"] },
	  "base": { "text": {} },
	  "nullable": true,
	  "constraints": [
	    { "name": "email_address_check",
	      "portable": { "length": { "max": 320, "unit": "LENGTH_UNIT_CHARACTERS" } } }
	  ]
	}`)
	assertContains(t, "§8.5", ns.GetComposites()[0], `{"attributes": [
	  { "name": { "stored": "amount" }, "nullable": true },
	  { "name": { "stored": "currency" }, "nullable": true }
	]}`)

	// §8.6.
	assertContains(t, "§8.6", columnOf(t, posts, "labels"), `{
	  "type": {
	    "array": { "element": { "text": {} }, "elementNullable": true, "dimensions": 1 },
	    "native": { "name": { "namespace": "pg_catalog", "object": "_text" }, "raw": "text[]" }
	  },
	  "nullable": true
	}`)

	// §8.8.
	assertContains(t, "§8.8", columnOf(t, users, "created_at"), `{"type": {
	  "instant": { "precision": 6 },
	  "native": { "name": { "namespace": "pg_catalog", "object": "timestamptz" }, "raw": "timestamp with time zone" }
	}}`)

	// §10.4, each constraint by name; posts_published_check's native text is
	// Postgres's deparse (see above).
	constraint := func(cs []*irv1.Constraint, name string) *irv1.Constraint {
		return named(t, cs, func(c *irv1.Constraint) string { return c.GetName() }, name)
	}
	assertContains(t, "§10.4", constraint(posts.GetConstraints(), "posts_title_check"), `{
	  "name": "posts_title_check", "columns": ["title"],
	  "portable": { "length": { "min": 1, "max": 200, "unit": "LENGTH_UNIT_CHARACTERS" } },
	  "clientValidated": true }`)
	assertContains(t, "§10.4", constraint(posts.GetConstraints(), "posts_published_check"), `{
	  "name": "posts_published_check", "columns": ["published_at", "status"],
	  "opaque": { "native": "CHECK (((published_at IS NULL) OR (status <> 'draft'::app.post_status)))" },
	  "clientValidated": false }`)
	slugText := named(t, ns.GetDomains(), func(d *irv1.Domain) string { return d.GetName().GetStored() }, "slug_text")
	assertContains(t, "§10.4", constraint(slugText.GetConstraints(), "slug_text_check"), `{
	  "name": "slug_text_check",
	  "portable": { "pattern": { "regex": "^[a-z0-9]+(-[a-z0-9]+)*$" } },
	  "clientValidated": true }`)

	// §11.2.
	uniqueness := func(tb *irv1.Table, name string) *irv1.Uniqueness {
		return named(t, tb.GetUniqueness(), func(u *irv1.Uniqueness) string { return u.GetName() }, name)
	}
	assertContains(t, "§11.2", uniqueness(users, "users_tenant_email_key"), `{
	  "name": "users_tenant_email_key",
	  "kind": "UNIQUENESS_CONSTRAINT",
	  "columns": ["tenant_id", "email"],
	  "nullsDistinct": true,
	  "identifies": true }`)
	assertContains(t, "§11.2", uniqueness(posts, "posts_one_published_slug"), `{
	  "name": "posts_one_published_slug",
	  "kind": "UNIQUENESS_INDEX",
	  "columns": ["slug"],
	  "predicate": { "native": "(status = 'published'::app.post_status)" },
	  "nullsDistinct": true,
	  "identifies": false }`)

	// §11.3.
	assertContains(t, "§11.3", columnOf(t, users, "id"), `{ "name": { "stored": "id", "words": ["id"] },
	  "default": { "class": "DEFAULT_IDENTITY_ALWAYS",
	               "sequence": { "catalog": "shop", "namespace": "app", "object": "users_id_seq" } } }`)
	assertContains(t, "§11.3", columnOf(t, posts, "status"), `{ "name": { "stored": "status", "words": ["status"] },
	  "default": { "class": "DEFAULT_CONSTANT", "native": "'draft'::app.post_status",
	               "value": { "string": "draft" } } }`)
	assertContains(t, "§11.3", columnOf(t, users, "created_at"), `{ "name": { "stored": "created_at", "words": ["created", "at"] },
	  "default": { "class": "DEFAULT_EXPRESSION", "native": "now()" } }`)
	assertContains(t, "§11.3", columnOf(t, posts, "slug"), `{ "name": { "stored": "slug", "words": ["slug"] },
	  "generation": { "kind": "GENERATION_STORED",
	                  "native": "lower(replace(title, ' '::text, '-'::text))" } }`)

	// §12.4, with the replace and delete policy from config (§1.3).
	assertContains(t, "§12.4", relationOf(t, posts, "author"), `{
	  "name": { "stored": "author", "words": ["author"] },
	  "owner": { "catalog": "shop", "namespace": "app", "object": "posts" },
	  "kind": "RELATION_KIND_MANY_TO_ONE",
	  "origin": "RELATION_ORIGIN_FOREIGN_KEY",
	  "via": { "catalog": "shop", "namespace": "app", "object": "posts", "member": "posts_author_id_fkey" },
	  "from": { "columns": ["author_id"] },
	  "to": { "table": { "catalog": "shop", "namespace": "app", "object": "users" }, "columns": ["id"] },
	  "optional": false
	}`)
	assertContains(t, "§12.4", relationOf(t, users, "posts_to_author"), `{
	  "name": { "stored": "posts_to_author", "words": ["posts", "to", "author"] },
	  "owner": { "catalog": "shop", "namespace": "app", "object": "users" },
	  "kind": "RELATION_KIND_ONE_TO_MANY",
	  "origin": "RELATION_ORIGIN_FOREIGN_KEY",
	  "via": { "catalog": "shop", "namespace": "app", "object": "posts", "member": "posts_author_id_fkey" },
	  "inverseOf": "author",
	  "policy": { "collection": "COLLECTION_POLICY_REPLACE", "removal": "REMOVAL_DELETE" }
	}`)
	assertContains(t, "§12.4", relationOf(t, posts, "tags"), `{
	  "name": { "stored": "tags", "words": ["tags"] },
	  "owner": { "catalog": "shop", "namespace": "app", "object": "posts" },
	  "kind": "RELATION_KIND_MANY_TO_MANY",
	  "origin": "RELATION_ORIGIN_INFERRED_JUNCTION",
	  "through": { "catalog": "shop", "namespace": "app", "object": "post_tags" },
	  "to": { "table": { "catalog": "shop", "namespace": "app", "object": "tags" }, "columns": ["id"] }
	}`)
	// §12.3: an inverse to-one relation is always optional, a forward one
	// optional when an FK column is nullable.
	for _, r := range []*irv1.Relation{relationOf(t, users, "profile"), relationOf(t, posts, "editor")} {
		if !r.GetOptional() {
			t.Errorf("§12.3: %s is not optional", r.GetName().GetStored())
		}
	}
}

// The examples about shop that need what §1.3 does not declare: §8.3's
// citext column, §6.2's trigger, and §7.7's and §8.5's config.
func TestGateRecordExtras(t *testing.T) {
	_, b := generateBundle(t, filepath.Join("testdata", "extras.yaml"), extrasURL)
	ns := b.GetCatalogs()[0].GetNamespaces()[0]
	users, posts := tableOf(t, ns, "users"), tableOf(t, ns, "posts")

	// §3.1: the opaque column makes the bundle require opaque-types.
	assertContains(t, "§3.1", b, `{"requiredFeatures": ["opaque-types"]}`)
	// §8.3, mapped by the go emitter's types config; the raw spelling is
	// qualified (see above).
	assertContains(t, "§8.3", columnOf(t, users, "nickname"), `{"type": {
	  "opaque": {},
	  "native": { "name": { "namespace": "public", "object": "citext" }, "raw": "public.citext" }
	}}`)
	// §6.2.
	if !slices.ContainsFunc(users.GetExtensions(), func(a *anypb.Any) bool {
		var trig pgextv1.Trigger
		if a.UnmarshalTo(&trig) != nil {
			return false
		}
		got, _ := protojson.Marshal(&trig)
		w := &pgextv1.Trigger{}
		protojson.Unmarshal([]byte(`{"name": "users_touch_updated_at", "timing": "BEFORE", "events": ["UPDATE"],
		  "function": { "catalog": "shop", "namespace": "app", "object": "touch_updated_at" }}`), w)
		want, _ := protojson.Marshal(w)
		var gm, wm any
		json.Unmarshal(got, &gm)
		json.Unmarshal(want, &wm)
		_, ok := subset(wm, gm, "$")
		return ok
	}) {
		t.Errorf("§6.2: extensions %v", users.GetExtensions())
	}
	// §7.7.
	assertContains(t, "§7.7", columnOf(t, posts, "published_at"), `{"annotations": [
	  { "key": "x.ts.time-type", "value": { "string": "date" } }
	]}`)
	// §8.5: shop.app.money_amount.amount: { nonnull } narrows the attribute.
	assertContains(t, "§8.5", ns.GetComposites()[0], `{"attributes": [
	  { "name": { "stored": "amount" }, "nullable": false, "nullabilitySource": "NULLABILITY_SOURCE_ANNOTATION" },
	  { "name": { "stored": "currency" }, "nullable": true }
	]}`)
}

// TestGateSnapshotParity is the gate's second item (implementation guide
// §4 M1): dql generate on the dpg snapshot of the running example assembles
// the same bundle as on the database, apart from provenance and the limits
// the snapshot's provenance names, which fidelity.Normalize removes from
// each catalog. The database leaves out the two objects the snapshot's dpg
// source does not declare (testdata/dpg/drift.sql), and the snapshot's
// dql.yaml asserts in config the nonnull that the view column comments
// assert on the database; that config is removed before comparing, since
// it stands in for comments the snapshot cannot hold.
func TestGateSnapshotParity(t *testing.T) {
	_, live := generateBundle(t, filepath.Join(shopDir, "dql.yaml"), driftlessURL)
	_, snap := generateBundle(t, filepath.Join("testdata", "dpg", "dql.yaml"), "")
	limits := snap.GetCatalogs()[0].GetProvenance().GetFidelityLimits()
	if !slices.Equal(limits, fidelity.DPGSnapshot) {
		t.Fatalf("limits: %v", limits)
	}
	for _, col := range tableOf(t, snap.GetCatalogs()[0].GetNamespaces()[0], "published_posts").GetColumns() {
		col.Annotations = slices.DeleteFunc(col.Annotations, func(a *irv1.Annotation) bool {
			return a.GetOrigin().GetKind() == irv1.OriginKind_ORIGIN_CONFIG
		})
	}
	normalized := func(b *irv1.Bundle) string {
		for i, c := range b.GetCatalogs() {
			b.Catalogs[i] = fidelity.Normalize(c, limits)
		}
		js, err := bundle.MarshalJSON(b)
		if err != nil {
			t.Fatal(err)
		}
		return string(js)
	}
	if want, got := normalized(live), normalized(snap); got != want {
		t.Errorf("the normalized bundles differ:\n--- pg-live\n%s\n--- dpg-snapshot\n%s", want, got)
	}
}
