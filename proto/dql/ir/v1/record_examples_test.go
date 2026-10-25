package irv1_test

import (
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	_ "github.com/thec1oud/dql/proto/dql/ext/postgres/v1"
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
		{"§4.1 foreign key", &irv1.ForeignKey{}, `{
		  "name": { "stored": "posts_author_id_fkey", "words": ["posts", "author", "id", "fkey"] },
		  "columns": ["author_id"],
		  "references": {
		    "table": { "catalog": "shop", "namespace": "app", "object": "users" },
		    "columns": ["id"]
		  }
		}`},
		// §5.3 writes "cardinality": "CARDINALITY_MANY_TO_ONE" where §12.4 writes
		// "kind": "RELATION_KIND_MANY_TO_ONE" for the same fact; the IR follows §12.4.
		{"§5.3 cross-catalog relation", &irv1.Relation{}, `{
		  "name": { "stored": "user", "words": ["user"] },
		  "origin": "RELATION_ORIGIN_ANNOTATED",
		  "from": { "table": { "catalog": "billing", "namespace": "", "object": "invoices" },
		    "columns": ["user_id"] },
		  "to": { "table": { "catalog": "shop", "namespace": "", "object": "users" },
		    "columns": ["id"] },
		  "kind": "RELATION_KIND_MANY_TO_ONE",
		  "crossCatalog": true
		}`},
		{"§6.2 extension", &irv1.Table{}, `{
		  "extensions": [{
		    "@type": "type.dql.dev/dql.ext.postgres.v1.Trigger",
		    "name": "users_touch_updated_at",
		    "timing": "BEFORE",
		    "events": ["UPDATE"],
		    "function": { "catalog": "shop", "namespace": "app", "object": "touch_updated_at" }
		  }]
		}`},
		{"§6.4 view", &irv1.Table{}, `{
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
		}`},
		{"§6.5 function", &irv1.Routine{}, `{
		  "name": { "stored": "post_score", "words": ["post", "score"] },
		  "overloads": [{
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
		  }]
		}`},
		{"§7.5 column", &irv1.Column{}, `{
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
		}`},
		{"§7.6 relation", &irv1.Relation{}, `{
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
		}`},
		{"§8.4 enum", &irv1.Enum{}, `{
		  "name": { "stored": "post_status", "words": ["post", "status"] },
		  "labels": ["draft", "published", "archived"],
		  "origin": "ENUM_ORIGIN_NATIVE"
		}`},
		{"§8.5 domain", &irv1.Domain{}, `{
		  "name": { "stored": "email_address", "words": ["email", "address"] },
		  "base": { "text": {} },
		  "nullable": true,
		  "constraints": [
		    { "name": "email_address_check",
		      "portable": { "length": { "max": 320, "unit": "LENGTH_UNIT_CHARACTERS" } } }
		  ]
		}`},
		{"§8.9 collation", &irv1.Collation{}, `{
		  "name": "utf8mb4_0900_ai_ci",
		  "caseSensitive": "TRISTATE_FALSE",
		  "accentSensitive": "TRISTATE_FALSE",
		  "padSpace": "TRISTATE_FALSE",
		  "deterministic": "TRISTATE_TRUE",
		  "clientFold": "CLIENT_FOLD_NONE"
		}`},
		{"§8.10 weak column", &irv1.Column{}, `{
		  "name": { "stored": "attempts", "words": ["attempts"] },
		  "type": { "weak": { "affinity": { "int64": {} },
		    "storageClasses": ["STORAGE_INTEGER", "STORAGE_REAL", "STORAGE_TEXT", "STORAGE_BLOB"] } },
		  "nullable": true
		}`},
		{"§10.4 title check", &irv1.Constraint{}, `{
		  "name": "posts_title_check", "columns": ["title"],
		  "portable": { "length": { "min": 1, "max": 200, "unit": "LENGTH_UNIT_CHARACTERS" } },
		  "clientValidated": true
		}`},
		{"§10.4 published check", &irv1.Constraint{}, `{
		  "name": "posts_published_check", "columns": ["published_at", "status"],
		  "opaque": { "native": "CHECK (published_at IS NULL OR status <> 'draft'::app.post_status)" },
		  "clientValidated": false
		}`},
		{"§10.4 slug pattern", &irv1.Constraint{}, `{
		  "name": "slug_text_check",
		  "portable": { "pattern": { "regex": "^[a-z0-9]+(-[a-z0-9]+)*$" } },
		  "clientValidated": true
		}`},
		{"§11.2 unique constraint", &irv1.Uniqueness{}, `{
		  "name": "users_tenant_email_key",
		  "kind": "UNIQUENESS_CONSTRAINT",
		  "columns": ["tenant_id", "email"],
		  "nullsDistinct": true,
		  "identifies": true
		}`},
		{"§11.2 partial index", &irv1.Uniqueness{}, `{
		  "name": "posts_one_published_slug",
		  "kind": "UNIQUENESS_INDEX",
		  "columns": ["slug"],
		  "predicate": { "native": "(status = 'published'::app.post_status)" },
		  "nullsDistinct": true,
		  "identifies": false
		}`},
		{"§11.3 identity", &irv1.Column{}, `{
		  "name": { "stored": "id", "words": ["id"] },
		  "default": { "class": "DEFAULT_IDENTITY_ALWAYS",
		    "sequence": { "catalog": "shop", "namespace": "app", "object": "users_id_seq" } }
		}`},
		{"§11.3 constant", &irv1.Column{}, `{
		  "name": { "stored": "status", "words": ["status"] },
		  "default": { "class": "DEFAULT_CONSTANT", "native": "'draft'::app.post_status",
		    "value": { "string": "draft" } }
		}`},
		{"§11.3 expression", &irv1.Column{}, `{
		  "name": { "stored": "created_at", "words": ["created", "at"] },
		  "default": { "class": "DEFAULT_EXPRESSION", "native": "now()" }
		}`},
		{"§11.3 generated", &irv1.Column{}, `{
		  "name": { "stored": "slug", "words": ["slug"] },
		  "generation": { "kind": "GENERATION_STORED",
		    "native": "lower(replace(title, ' '::text, '-'::text))" }
		}`},
		{"§12.4 many-to-one", &irv1.Relation{}, `{
		  "name": { "stored": "author", "words": ["author"] },
		  "owner": { "catalog": "shop", "namespace": "app", "object": "posts" },
		  "kind": "RELATION_KIND_MANY_TO_ONE",
		  "origin": "RELATION_ORIGIN_FOREIGN_KEY",
		  "via": { "catalog": "shop", "namespace": "app", "object": "posts", "member": "posts_author_id_fkey" },
		  "from": { "columns": ["author_id"] },
		  "to": { "table": { "catalog": "shop", "namespace": "app", "object": "users" }, "columns": ["id"] },
		  "optional": false
		}`},
		{"§12.4 one-to-many", &irv1.Relation{}, `{
		  "name": { "stored": "posts_to_author", "words": ["posts", "to", "author"] },
		  "owner": { "catalog": "shop", "namespace": "app", "object": "users" },
		  "kind": "RELATION_KIND_ONE_TO_MANY",
		  "origin": "RELATION_ORIGIN_FOREIGN_KEY",
		  "via": { "catalog": "shop", "namespace": "app", "object": "posts", "member": "posts_author_id_fkey" },
		  "inverseOf": "author",
		  "policy": { "collection": "COLLECTION_POLICY_REPLACE", "removal": "REMOVAL_DELETE" }
		}`},
		{"§12.4 many-to-many", &irv1.Relation{}, `{
		  "name": { "stored": "tags", "words": ["tags"] },
		  "owner": { "catalog": "shop", "namespace": "app", "object": "posts" },
		  "kind": "RELATION_KIND_MANY_TO_MANY",
		  "origin": "RELATION_ORIGIN_INFERRED_JUNCTION",
		  "through": { "catalog": "shop", "namespace": "app", "object": "post_tags" },
		  "to": { "table": { "catalog": "shop", "namespace": "app", "object": "tags" }, "columns": ["id"] }
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
		{"§14.10 shape", &irv1.Shape{}, `{
		  "id": {
		    "entity": { "catalog": "shop", "namespace": "app", "object": "users" },
		    "preset": "PRESET_PARTIAL",
		    "form": "FORM_FLAT"
		  },
		  "hostName": { "words": ["user", "partial"] },
		  "lineage": [{ "op": "PRESET", "preset": "PRESET_PARTIAL" }],
		  "fields": [
		    { "column": "email", "presence": "PRESENCE_OPTIONAL", "nullable": false,
		      "valueKind": "VALUE_KIND_VALUE_OR_EXPRESSION",
		      "type": { "named": { "domain": { "catalog": "shop", "namespace": "app", "object": "email_address" } } },
		      "validation": ["email_address_check"] },
		    { "column": "display_name", "presence": "PRESENCE_OPTIONAL", "nullable": true,
		      "valueKind": "VALUE_KIND_VALUE_OR_EXPRESSION",
		      "type": { "varchar": { "length": 80, "unit": "LENGTH_UNIT_CHARACTERS" } } },
		    { "column": "password_hash", "presence": "PRESENCE_OPTIONAL", "nullable": false,
		      "valueKind": "VALUE_KIND_VALUE_OR_EXPRESSION",
		      "type": { "text": {} }, "redact": true }
		  ],
		  "forbidden": [
		    { "column": "id", "reason": "FORBIDDEN_KEY" },
		    { "column": "tenant_id", "reason": "FORBIDDEN_BOUND" },
		    { "column": "created_at", "reason": "FORBIDDEN_IMMUTABLE" },
		    { "column": "updated_at", "reason": "FORBIDDEN_DB_UPDATED" },
		    { "column": "version", "reason": "FORBIDDEN_VERSION" }
		  ],
		  "scope": ["tenant_id"]
		}`},
		// §14.7 and §14.10 write shape ids in their dotted spelling ("user.draft.flat");
		// the IR carries the structured ShapeId, and the dotted spelling is display only.
		{"§14.7 upsert", &irv1.Upsert{}, `{
		  "entity": { "catalog": "shop", "namespace": "app", "object": "users" },
		  "conflictTarget": "users_tenant_email_key",
		  "insertShape": { "entity": { "catalog": "shop", "namespace": "app", "object": "users" },
		    "preset": "PRESET_DRAFT", "form": "FORM_FLAT" },
		  "defaultUpdateExcludes": [
		    { "column": "tenant_id", "reason": "EXCLUDE_BOUND" },
		    { "column": "email", "reason": "EXCLUDE_CONFLICT_KEY" },
		    { "column": "created_at", "reason": "EXCLUDE_IMMUTABLE" },
		    { "column": "updated_at", "reason": "EXCLUDE_DB_UPDATED" },
		    { "column": "version", "reason": "EXCLUDE_VERSION" }
		  ]
		}`},
		{"§14.10 conversions", &irv1.ShapeLayer{}, `{ "conversions": [
		  { "from": { "entity": { "catalog": "shop", "namespace": "app", "object": "users" },
		      "preset": "PRESET_DRAFT", "form": "FORM_FLAT" },
		    "to": { "entity": { "catalog": "shop", "namespace": "app", "object": "users" },
		      "preset": "PRESET_FULL", "form": "FORM_FLAT" },
		    "kind": "CONVERSION_VIA_DATABASE" },
		  { "from": { "entity": { "catalog": "shop", "namespace": "app", "object": "users" },
		      "preset": "PRESET_FULL", "form": "FORM_FLAT" },
		    "to": { "entity": { "catalog": "shop", "namespace": "app", "object": "users" },
		      "preset": "PRESET_DRAFT", "form": "FORM_FLAT" },
		    "kind": "CONVERSION_NEEDS_INPUTS",
		    "inputs": [{ "column": "password_hash", "reason": "INPUT_WRITE_ONLY" }] },
		  { "from": { "entity": { "catalog": "shop", "namespace": "app", "object": "users" },
		      "preset": "PRESET_FULL", "form": "FORM_FLAT" },
		    "to": { "entity": { "catalog": "shop", "namespace": "app", "object": "users" },
		      "preset": "PRESET_KEY" },
		    "kind": "CONVERSION_TOTAL" },
		  { "from": { "entity": { "catalog": "shop", "namespace": "app", "object": "users" },
		      "preset": "PRESET_PARTIAL", "form": "FORM_FLAT" },
		    "to": { "entity": { "catalog": "shop", "namespace": "app", "object": "users" },
		      "preset": "PRESET_FULL", "form": "FORM_FLAT" },
		    "kind": "CONVERSION_WITH_BASE" },
		  { "from": { "custom": "user_summary" },
		    "to": { "entity": { "catalog": "shop", "namespace": "app", "object": "users" },
		      "preset": "PRESET_FULL", "form": "FORM_FLAT" },
		    "kind": "CONVERSION_IMPOSSIBLE" }
		] }`},
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
		// §9.1 writes a type variable as a bare string ("T") in a list of types; the
		// IR spells it {"var": "T"} and a type {"type": {...}}, so both are typed.
		{"§9.1 operators", &irv1.Bundle{}, `{ "operators": [
		  { "name": "eq",
		    "signature": { "args": [{ "var": "T" }, { "var": "T" }], "returns": { "type": { "bool": {} } } },
		    "requires": ["comparable"], "nullBehavior": "NULL_BEHAVIOR_STRICT" },
		  { "name": "ilike",
		    "signature": { "args": [{ "type": { "text": {} } }, { "type": { "text": {} } }], "returns": { "type": { "bool": {} } } },
		    "nullBehavior": "NULL_BEHAVIOR_STRICT" },
		  { "name": "is_null",
		    "signature": { "args": [{ "var": "T" }], "returns": { "type": { "bool": {} } } },
		    "requires": ["nullable"], "nullBehavior": "NULL_BEHAVIOR_NEVER_NULL" }
		] }`},
		{"§9.2 dialect support", &irv1.CapabilityProfile{}, `{
		  "dialect": "DIALECT_MYSQL",
		  "operators": [
		    { "name": "like", "fidelity": "FIDELITY_EXACT" },
		    { "name": "ilike", "fidelity": "FIDELITY_EXACT",
		      "when": { "collation": { "caseSensitive": "TRISTATE_FALSE" } },
		      "otherwise": "FIDELITY_APPROXIMATE" },
		    { "name": "regex_match", "fidelity": "FIDELITY_APPROXIMATE" }
		  ]
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
