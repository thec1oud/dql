package irv1_test

import (
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Each example is copied from the design record. Unmarshalling rejects unknown
// fields, so a passing case shows the messages accept the record's encoding.
func TestDesignRecordExamples(t *testing.T) {
	cases := []struct {
		section string
		msg     proto.Message
		json    string
	}{
		{"§3.1 bundle", &irv1.Bundle{}, `{
		  "version": { "major": 1, "minor": 0, "patch": 0 },
		  "requiredFeatures": ["opaque-types"],
		  "dialect": "DIALECT_POSTGRES",
		  "catalogs": [{
		    "name": { "stored": "shop", "words": ["shop"] },
		    "provenance": { "source": "pg-live", "sourceVersion": "1.0.0",
		      "inputFingerprint": "sha256:9f2c", "fidelity": "SOURCE_FIDELITY_FULL" }
		  }]
		}`},
		{"§3.3 handshake", &irv1.PluginHandshake{}, `{
		  "plugin": { "name": "go", "kind": "PLUGIN_KIND_EMITTER", "version": "1.4.0" },
		  "irVersions": { "min": "1.0.0", "maxMajor": 1 },
		  "features": ["weak-types", "opaque-types", "nested-writes", "cross-catalog"]
		}`},
		{"§8.2 varchar", &irv1.Type{}, `{
		  "varchar": { "length": 80, "unit": "LENGTH_UNIT_CHARACTERS" },
		  "native": { "name": { "namespace": "pg_catalog", "object": "varchar" },
		    "modifiers": [{ "int": 80 }], "raw": "character varying(80)" }
		}`},
		{"§8.3 citext", &irv1.Type{}, `{
		  "opaque": {},
		  "native": { "name": { "namespace": "public", "object": "citext" }, "raw": "citext" }
		}`},
		{"§8.6 array", &irv1.Type{}, `{
		  "array": { "element": { "text": {} }, "elementNullable": true, "dimensions": 1 },
		  "native": { "name": { "namespace": "pg_catalog", "object": "_text" }, "raw": "text[]" }
		}`},
		{"§8.8 instant", &irv1.Type{}, `{
		  "instant": { "precision": 6 },
		  "native": { "name": { "namespace": "pg_catalog", "object": "timestamptz" },
		    "raw": "timestamp with time zone" }
		}`},
		{"§8.10 weak", &irv1.Type{}, `{
		  "weak": { "affinity": { "int64": {} },
		    "storageClasses": ["STORAGE_INTEGER", "STORAGE_REAL", "STORAGE_TEXT", "STORAGE_BLOB"] }
		}`},
		{"§14.10 domain ref", &irv1.Type{}, `{
		  "named": { "domain": { "catalog": "shop", "namespace": "app", "object": "email_address" } }
		}`},
		{"§7.5 annotation", &irv1.Annotation{}, `{
		  "key": "core.sensitive",
		  "origin": { "kind": "ORIGIN_DB_COMMENT", "plugin": "pg-live",
		    "location": "COMMENT ON COLUMN app.users.password_hash" }
		}`},
		{"§7.6 superseded", &irv1.Annotation{}, `{
		  "key": "core.inverse-name",
		  "value": { "words": ["profile"] },
		  "origin": { "kind": "ORIGIN_CONFIG", "plugin": "pg-live",
		    "location": "dql.yaml#annotations.shop.app.profiles.profiles_user_id_fkey" },
		  "superseded": [{ "key": "core.inverse-name", "value": { "words": ["user", "profile"] },
		    "origin": { "kind": "ORIGIN_DB_COMMENT", "plugin": "pg-live",
		      "location": "COMMENT ON CONSTRAINT profiles_user_id_fkey ON app.profiles" } }]
		}`},
		{"§7.7 plugin annotation", &irv1.Annotation{}, `{ "key": "x.ts.time-type", "value": { "string": "date" } }`},
		{"§14.8 diagnostic", &irv1.Diagnostic{}, `{
		  "severity": "SEVERITY_ERROR", "code": "write-shape-incomplete", "shape": "quick_invite",
		  "message": "shape derived from user.draft drops required field password_hash; keep it or bind it"
		}`},
		{"§17.3 diagnostic", &irv1.Diagnostic{}, `{
		  "severity": "SEVERITY_ERROR", "code": "query-unscoped", "query": "AllUsers",
		  "message": "query reads shop.app.users"
		}`},
		{"§21.3 diagnostic", &irv1.Diagnostic{}, `{
		  "severity": "SEVERITY_WARNING", "code": "soft-delete-unique",
		  "target": { "catalog": "shop", "namespace": "app", "object": "posts",
		    "member": "posts_one_published_slug" },
		  "message": "unique index on a soft-delete table does not exclude deleted rows"
		}`},
	}
	for _, c := range cases {
		t.Run(c.section, func(t *testing.T) {
			if err := protojson.Unmarshal([]byte(c.json), c.msg); err != nil {
				t.Fatalf("design record %s does not decode: %v", c.section, err)
			}
		})
	}
}
