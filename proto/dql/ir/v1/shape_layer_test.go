package irv1_test

import (
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// The nested writes of §14.5 and §14.6 for post, written as shape-layer IR.
// Unmarshalling rejects unknown fields, so a passing case shows the messages
// hold every relation-derived shape, reference and change the record names.
func TestNestedWritesFitTheShapeLayer(t *testing.T) {
	const layer = `{
	  "shapes": [
	    { "id": { "entity": { "catalog": "shop", "namespace": "app", "object": "posts" },
	        "preset": "PRESET_DRAFT", "form": "FORM_FLAT", "forRelation": "author" },
	      "hostName": { "words": ["post", "draft", "for", "author"] },
	      "entity": { "catalog": "shop", "namespace": "app", "object": "posts" },
	      "lineage": [
	        { "op": "PRESET", "entity": { "catalog": "shop", "namespace": "app", "object": "posts" },
	          "preset": "PRESET_DRAFT", "form": "FORM_FLAT" },
	        { "op": "FOR_RELATION", "relation": "author" }
	      ],
	      "forbidden": [{ "column": "author_id", "reason": "FORBIDDEN_PARENT" }] },
	    { "id": { "entity": { "catalog": "shop", "namespace": "app", "object": "posts" },
	        "preset": "PRESET_DRAFT", "form": "FORM_NESTED" },
	      "hostName": { "words": ["post", "draft", "nested"] },
	      "fields": [
	        { "column": "author_id", "presence": "PRESENCE_OPTIONAL",
	          "valueKind": "VALUE_KIND_VALUE_OR_EXPRESSION", "type": { "int64": {} } }
	      ],
	      "nests": [
	        { "relation": "author", "presence": "PRESENCE_OPTIONAL",
	          "union": { "kind": "RELATION_UNION_REF",
	            "entity": { "catalog": "shop", "namespace": "app", "object": "users" } },
	          "supplies": ["author_id"], "needsOne": true },
	        { "relation": "post_tags", "presence": "PRESENCE_OPTIONAL",
	          "shape": { "entity": { "catalog": "shop", "namespace": "app", "object": "post_tags" },
	            "preset": "PRESET_DRAFT", "form": "FORM_FLAT", "forRelation": "post" } },
	        { "relation": "tags", "presence": "PRESENCE_OPTIONAL",
	          "union": { "kind": "RELATION_UNION_LINK",
	            "entity": { "catalog": "shop", "namespace": "app", "object": "tags" } } }
	      ] },
	    { "id": { "custom": "user_summary_named" },
	      "hostName": { "words": ["user", "summary", "named"] },
	      "entity": { "catalog": "shop", "namespace": "app", "object": "users" },
	      "lineage": [
	        { "op": "PRESET", "entity": { "catalog": "shop", "namespace": "app", "object": "users" },
	          "preset": "PRESET_FULL", "form": "FORM_FLAT" },
	        { "op": "PICK", "columns": ["id", "email", "display_name"] },
	        { "op": "FROM", "shape": { "custom": "user_summary" } },
	        { "op": "NONNULL", "columns": ["display_name"] }
	      ],
	      "fields": [
	        { "column": "display_name", "presence": "PRESENCE_REQUIRED", "nullable": false,
	          "nullabilitySource": "NULLABILITY_SOURCE_SHAPE_OPERATOR",
	          "nullabilityShape": { "custom": "user_summary_named" },
	          "type": { "varchar": { "length": 80, "unit": "LENGTH_UNIT_CHARACTERS" } } }
	      ],
	      "forbidden": [{ "column": "tenant_id", "reason": "FORBIDDEN_OMITTED" }] },
	    { "id": { "custom": "quick_invite" },
	      "hostName": { "words": ["quick", "invite"] },
	      "bind": ["password_hash"] },
	    { "id": { "entity": { "catalog": "shop", "namespace": "app", "object": "users" },
	        "preset": "PRESET_KEY" },
	      "fields": [
	        { "column": "id", "presence": "PRESENCE_REQUIRED", "type": { "int64": {} } },
	        { "column": "version", "presence": "PRESENCE_REQUIRED", "type": { "int32": {} },
	          "role": "FIELD_ROLE_EXPECTED_VERSION" }
	      ] },
	    { "id": { "entity": { "catalog": "shop", "namespace": "app", "object": "users" },
	        "preset": "PRESET_PARTIAL", "form": "FORM_NESTS" },
	      "nests": [
	        { "relation": "posts_to_author", "presence": "PRESENCE_OPTIONAL",
	          "union": { "kind": "RELATION_UNION_CHILD_CHANGE",
	            "entity": { "catalog": "shop", "namespace": "app", "object": "posts" },
	            "relation": "author" },
	          "policy": { "collection": "COLLECTION_POLICY_REPLACE", "removal": "REMOVAL_DELETE" } }
	      ],
	      "forbidden": [{ "relation": "tenant", "reason": "FORBIDDEN_BOUND" }] }
	  ],
	  "unions": [
	    { "id": { "kind": "RELATION_UNION_CHILD_CHANGE",
	        "entity": { "catalog": "shop", "namespace": "app", "object": "posts" },
	        "relation": "author" },
	      "hostName": { "words": ["post", "change", "for", "author"] },
	      "variants": [
	        { "change": "RELATION_CHANGE_CREATE",
	          "hostName": { "words": ["post", "create", "for", "author"] },
	          "payload": { "entity": { "catalog": "shop", "namespace": "app", "object": "posts" },
	            "preset": "PRESET_DRAFT", "form": "FORM_FLAT", "forRelation": "author" } },
	        { "change": "RELATION_CHANGE_UPDATE",
	          "hostName": { "words": ["post", "update", "for", "author"] },
	          "key": { "entity": { "catalog": "shop", "namespace": "app", "object": "posts" },
	            "preset": "PRESET_KEY" },
	          "payload": { "entity": { "catalog": "shop", "namespace": "app", "object": "posts" },
	            "preset": "PRESET_PARTIAL", "form": "FORM_FLAT", "forRelation": "author" } },
	        { "change": "RELATION_CHANGE_DELETE",
	          "hostName": { "words": ["post", "delete", "for", "author"] },
	          "key": { "entity": { "catalog": "shop", "namespace": "app", "object": "posts" },
	            "preset": "PRESET_KEY" } }
	      ] },
	    { "id": { "kind": "RELATION_UNION_REF",
	        "entity": { "catalog": "shop", "namespace": "app", "object": "users" } },
	      "hostName": { "words": ["user", "ref"] },
	      "variants": [
	        { "change": "RELATION_CHANGE_LINK", "hostName": { "words": ["user", "link"] },
	          "key": { "entity": { "catalog": "shop", "namespace": "app", "object": "users" },
	            "preset": "PRESET_KEY" } },
	        { "change": "RELATION_CHANGE_CREATE", "hostName": { "words": ["user", "create"] },
	          "payload": { "entity": { "catalog": "shop", "namespace": "app", "object": "users" },
	            "preset": "PRESET_DRAFT", "form": "FORM_FLAT" } }
	      ] }
	  ],
	  "conversions": [
	    { "from": { "entity": { "catalog": "shop", "namespace": "app", "object": "users" },
	        "preset": "PRESET_FULL", "form": "FORM_FLAT" },
	      "to": { "custom": "user_summary" },
	      "kind": "CONVERSION_TOTAL", "origin": "CONVERSION_ORIGIN_LINEAGE" }
	  ],
	  "merges": [
	    { "entity": { "catalog": "shop", "namespace": "app", "object": "users" },
	      "kind": "CONVERSION_TOTAL",
	      "key": [{ "column": "id", "comparison": "COMPARISON_EXACT" }] },
	    { "entity": { "catalog": "shop", "namespace": "app", "object": "countries" },
	      "kind": "CONVERSION_FALLIBLE",
	      "key": [{ "column": "code", "comparison": "COMPARISON_INEXACT" }] },
	    { "entity": { "catalog": "shop", "namespace": "app", "object": "handles" },
	      "kind": "CONVERSION_TOTAL",
	      "key": [{ "column": "handle", "comparison": "COMPARISON_FOLDED",
	        "fold": "CLIENT_FOLD_ASCII_LOWER" }] }
	  ]
	}`
	if err := protojson.Unmarshal([]byte(layer), &irv1.ShapeLayer{}); err != nil {
		t.Fatalf("shape layer does not decode: %v", err)
	}
}
