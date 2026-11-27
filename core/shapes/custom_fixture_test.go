package shapes

import (
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// This file builds the part of the running example (§1.3) that the custom
// shapes of §14.8 derive from: users, posts and tags with their relations,
// and their preset shapes as §14.3 to §14.5 give them.

func customQName(object string) *irv1.QualifiedName {
	return &irv1.QualifiedName{Catalog: "shop", Namespace: "app", Object: object}
}

func customTable(stored string, words []string, cols []string, rels ...*irv1.Relation) *irv1.Table {
	t := &irv1.Table{Name: &irv1.Identifier{Stored: stored}, HostName: &irv1.Identifier{Words: words}, Relations: rels}
	for i, c := range cols {
		t.Columns = append(t.Columns, &irv1.Column{Name: &irv1.Identifier{Stored: c}, Ordinal: uint32(i + 1)})
	}
	return t
}

func customRel(name string, kind irv1.RelationKind, to string, optional bool) *irv1.Relation {
	return &irv1.Relation{Name: &irv1.Identifier{Stored: name}, Kind: kind, Optional: optional, To: &irv1.ColumnSet{Table: customQName(to)}}
}

func customCatalogs() []*irv1.Catalog {
	return []*irv1.Catalog{{
		Name: &irv1.Identifier{Stored: "shop"},
		Namespaces: []*irv1.Namespace{{
			Name: &irv1.Identifier{Stored: "app"},
			Tables: []*irv1.Table{
				customTable("users", []string{"user"},
					[]string{"id", "tenant_id", "email", "display_name", "password_hash", "created_at", "updated_at", "version"},
					customRel("posts_to_author", irv1.RelationKind_RELATION_KIND_ONE_TO_MANY, "posts", false),
					customRel("profile", irv1.RelationKind_RELATION_KIND_INVERSE_TO_ONE, "profiles", true)),
				customTable("posts", []string{"post"},
					[]string{"id", "author_id", "editor_id", "title"},
					customRel("author", irv1.RelationKind_RELATION_KIND_MANY_TO_ONE, "users", false),
					customRel("editor", irv1.RelationKind_RELATION_KIND_MANY_TO_ONE, "users", true),
					customRel("tags", irv1.RelationKind_RELATION_KIND_MANY_TO_MANY, "tags", false)),
				customTable("tags", []string{"tag"}, []string{"id", "name"}),
				customTable("profiles", []string{"profile"}, []string{"user_id", "bio"}),
				customTable("notes", []string{"note"}, []string{"id"}),
			},
		}},
	}}
}

// customShape decodes a shape written as the IR's JSON.
func customShape(t testing.TB, js string) *irv1.Shape {
	t.Helper()
	s := &irv1.Shape{}
	if err := protojson.Unmarshal([]byte(js), s); err != nil {
		t.Fatalf("fixture does not decode: %v\n%s", err, js)
	}
	return s
}

const (
	customI64  = `{ "int64": {} }`
	customI32  = `{ "int32": {} }`
	customText = `{ "text": {} }`
	customUUID = `{ "uuid": {} }`
	customTime = `{ "instant": {} }`
	customMail = `{ "named": { "domain": { "catalog": "shop", "namespace": "app", "object": "email_address" } } }`
)

// customPresets are the preset shapes the tests derive from.
func customPresets(t testing.TB) []*irv1.Shape {
	t.Helper()
	var out []*irv1.Shape
	for _, js := range []string{
		`{ "id": { "entity": { "catalog": "shop", "namespace": "app", "object": "users" }, "preset": "PRESET_FULL", "form": "FORM_FLAT" },
		  "hostName": { "words": ["user", "full"] },
		  "entity": { "catalog": "shop", "namespace": "app", "object": "users" },
		  "lineage": [{ "op": "PRESET", "entity": { "catalog": "shop", "namespace": "app", "object": "users" }, "preset": "PRESET_FULL", "form": "FORM_FLAT" }],
		  "fields": [
		    { "column": "id", "presence": "PRESENCE_REQUIRED", "nullabilitySource": "NULLABILITY_SOURCE_DATABASE", "type": ` + customI64 + ` },
		    { "column": "tenant_id", "presence": "PRESENCE_REQUIRED", "nullabilitySource": "NULLABILITY_SOURCE_DATABASE", "type": ` + customUUID + ` },
		    { "column": "email", "presence": "PRESENCE_REQUIRED", "nullabilitySource": "NULLABILITY_SOURCE_DATABASE", "type": ` + customMail + ` },
		    { "column": "display_name", "presence": "PRESENCE_REQUIRED", "nullable": true, "nullabilitySource": "NULLABILITY_SOURCE_UNPROVEN", "type": ` + customText + ` },
		    { "column": "created_at", "presence": "PRESENCE_REQUIRED", "nullabilitySource": "NULLABILITY_SOURCE_DATABASE", "type": ` + customTime + ` },
		    { "column": "updated_at", "presence": "PRESENCE_REQUIRED", "nullabilitySource": "NULLABILITY_SOURCE_DATABASE", "type": ` + customTime + ` },
		    { "column": "version", "presence": "PRESENCE_REQUIRED", "nullabilitySource": "NULLABILITY_SOURCE_DATABASE", "type": ` + customI32 + ` }
		  ],
		  "forbidden": [{ "column": "password_hash", "reason": "FORBIDDEN_WRITE_ONLY" }],
		  "scope": ["tenant_id"] }`,
		`{ "id": { "entity": { "catalog": "shop", "namespace": "app", "object": "users" }, "preset": "PRESET_FULL", "form": "FORM_NESTED" },
		  "hostName": { "words": ["user", "full", "nested"] },
		  "entity": { "catalog": "shop", "namespace": "app", "object": "users" },
		  "lineage": [{ "op": "PRESET", "entity": { "catalog": "shop", "namespace": "app", "object": "users" }, "preset": "PRESET_FULL", "form": "FORM_NESTED" }],
		  "fields": [
		    { "column": "id", "presence": "PRESENCE_REQUIRED", "type": ` + customI64 + ` },
		    { "column": "email", "presence": "PRESENCE_REQUIRED", "type": ` + customMail + ` }
		  ],
		  "forbidden": [{ "column": "password_hash", "reason": "FORBIDDEN_WRITE_ONLY" }],
		  "nests": [
		    { "relation": "posts_to_author", "presence": "PRESENCE_REQUIRED",
		      "shape": { "entity": { "catalog": "shop", "namespace": "app", "object": "posts" }, "preset": "PRESET_FULL", "form": "FORM_FLAT" } },
		    { "relation": "profile", "presence": "PRESENCE_REQUIRED", "nullable": true,
		      "shape": { "entity": { "catalog": "shop", "namespace": "app", "object": "profiles" }, "preset": "PRESET_FULL", "form": "FORM_FLAT" } }
		  ] }`,
		`{ "id": { "entity": { "catalog": "shop", "namespace": "app", "object": "users" }, "preset": "PRESET_DRAFT", "form": "FORM_FLAT" },
		  "hostName": { "words": ["user", "draft"] },
		  "entity": { "catalog": "shop", "namespace": "app", "object": "users" },
		  "lineage": [{ "op": "PRESET", "entity": { "catalog": "shop", "namespace": "app", "object": "users" }, "preset": "PRESET_DRAFT", "form": "FORM_FLAT" }],
		  "fields": [
		    { "column": "email", "presence": "PRESENCE_REQUIRED", "nullabilitySource": "NULLABILITY_SOURCE_DATABASE", "valueKind": "VALUE_KIND_VALUE_OR_EXPRESSION", "type": ` + customMail + `, "validation": ["email_address_check"] },
		    { "column": "display_name", "presence": "PRESENCE_OPTIONAL", "nullable": true, "nullabilitySource": "NULLABILITY_SOURCE_UNPROVEN", "valueKind": "VALUE_KIND_VALUE_OR_EXPRESSION", "type": ` + customText + ` },
		    { "column": "password_hash", "presence": "PRESENCE_REQUIRED", "nullabilitySource": "NULLABILITY_SOURCE_DATABASE", "valueKind": "VALUE_KIND_VALUE_OR_EXPRESSION", "type": ` + customText + `, "redact": true },
		    { "column": "created_at", "presence": "PRESENCE_OPTIONAL", "nullabilitySource": "NULLABILITY_SOURCE_DATABASE", "valueKind": "VALUE_KIND_VALUE_OR_EXPRESSION", "type": ` + customTime + ` },
		    { "column": "updated_at", "presence": "PRESENCE_OPTIONAL", "nullabilitySource": "NULLABILITY_SOURCE_DATABASE", "valueKind": "VALUE_KIND_VALUE_OR_EXPRESSION", "type": ` + customTime + ` },
		    { "column": "version", "presence": "PRESENCE_OPTIONAL", "nullabilitySource": "NULLABILITY_SOURCE_DATABASE", "valueKind": "VALUE_KIND_VALUE_OR_EXPRESSION", "type": ` + customI32 + ` }
		  ],
		  "forbidden": [
		    { "column": "id", "reason": "FORBIDDEN_IDENTITY_ALWAYS" },
		    { "column": "tenant_id", "reason": "FORBIDDEN_BOUND" }
		  ],
		  "scope": ["tenant_id"] }`,
		`{ "id": { "entity": { "catalog": "shop", "namespace": "app", "object": "users" }, "preset": "PRESET_PARTIAL", "form": "FORM_FLAT" },
		  "hostName": { "words": ["user", "partial"] },
		  "entity": { "catalog": "shop", "namespace": "app", "object": "users" },
		  "lineage": [{ "op": "PRESET", "entity": { "catalog": "shop", "namespace": "app", "object": "users" }, "preset": "PRESET_PARTIAL", "form": "FORM_FLAT" }],
		  "fields": [
		    { "column": "email", "presence": "PRESENCE_OPTIONAL", "valueKind": "VALUE_KIND_VALUE_OR_EXPRESSION", "type": ` + customMail + ` },
		    { "column": "display_name", "presence": "PRESENCE_OPTIONAL", "nullable": true, "valueKind": "VALUE_KIND_VALUE_OR_EXPRESSION", "type": ` + customText + ` },
		    { "column": "password_hash", "presence": "PRESENCE_OPTIONAL", "valueKind": "VALUE_KIND_VALUE_OR_EXPRESSION", "type": ` + customText + `, "redact": true }
		  ],
		  "forbidden": [
		    { "column": "id", "reason": "FORBIDDEN_KEY" },
		    { "column": "tenant_id", "reason": "FORBIDDEN_BOUND" },
		    { "column": "created_at", "reason": "FORBIDDEN_IMMUTABLE" },
		    { "column": "updated_at", "reason": "FORBIDDEN_DB_UPDATED" },
		    { "column": "version", "reason": "FORBIDDEN_VERSION" }
		  ],
		  "scope": ["tenant_id"] }`,
		`{ "id": { "entity": { "catalog": "shop", "namespace": "app", "object": "users" }, "preset": "PRESET_KEY" },
		  "hostName": { "words": ["user", "key"] },
		  "entity": { "catalog": "shop", "namespace": "app", "object": "users" },
		  "lineage": [{ "op": "PRESET", "entity": { "catalog": "shop", "namespace": "app", "object": "users" }, "preset": "PRESET_KEY" }],
		  "fields": [
		    { "column": "id", "presence": "PRESENCE_REQUIRED", "type": ` + customI64 + ` },
		    { "column": "version", "presence": "PRESENCE_REQUIRED", "type": ` + customI32 + `, "role": "FIELD_ROLE_EXPECTED_VERSION" }
		  ] }`,
		`{ "id": { "entity": { "catalog": "shop", "namespace": "app", "object": "posts" }, "preset": "PRESET_FULL", "form": "FORM_FLAT" },
		  "hostName": { "words": ["post", "full"] },
		  "entity": { "catalog": "shop", "namespace": "app", "object": "posts" },
		  "lineage": [{ "op": "PRESET", "entity": { "catalog": "shop", "namespace": "app", "object": "posts" }, "preset": "PRESET_FULL", "form": "FORM_FLAT" }],
		  "fields": [
		    { "column": "id", "presence": "PRESENCE_REQUIRED", "type": ` + customI64 + ` },
		    { "column": "author_id", "presence": "PRESENCE_REQUIRED", "type": ` + customI64 + ` },
		    { "column": "editor_id", "presence": "PRESENCE_REQUIRED", "nullable": true, "type": ` + customI64 + ` },
		    { "column": "title", "presence": "PRESENCE_REQUIRED", "type": ` + customText + ` }
		  ] }`,
		`{ "id": { "entity": { "catalog": "shop", "namespace": "app", "object": "posts" }, "preset": "PRESET_DRAFT", "form": "FORM_FLAT" },
		  "hostName": { "words": ["post", "draft"] },
		  "entity": { "catalog": "shop", "namespace": "app", "object": "posts" },
		  "lineage": [{ "op": "PRESET", "entity": { "catalog": "shop", "namespace": "app", "object": "posts" }, "preset": "PRESET_DRAFT", "form": "FORM_FLAT" }],
		  "fields": [
		    { "column": "id", "presence": "PRESENCE_OPTIONAL", "type": ` + customI64 + ` },
		    { "column": "author_id", "presence": "PRESENCE_REQUIRED", "type": ` + customI64 + ` },
		    { "column": "editor_id", "presence": "PRESENCE_OPTIONAL", "nullable": true, "type": ` + customI64 + ` },
		    { "column": "title", "presence": "PRESENCE_REQUIRED", "type": ` + customText + ` }
		  ] }`,
		`{ "id": { "entity": { "catalog": "shop", "namespace": "app", "object": "posts" }, "preset": "PRESET_DRAFT", "form": "FORM_NESTED" },
		  "hostName": { "words": ["post", "draft", "nested"] },
		  "entity": { "catalog": "shop", "namespace": "app", "object": "posts" },
		  "lineage": [{ "op": "PRESET", "entity": { "catalog": "shop", "namespace": "app", "object": "posts" }, "preset": "PRESET_DRAFT", "form": "FORM_NESTED" }],
		  "fields": [
		    { "column": "id", "presence": "PRESENCE_OPTIONAL", "type": ` + customI64 + ` },
		    { "column": "author_id", "presence": "PRESENCE_OPTIONAL", "type": ` + customI64 + ` },
		    { "column": "editor_id", "presence": "PRESENCE_OPTIONAL", "nullable": true, "type": ` + customI64 + ` },
		    { "column": "title", "presence": "PRESENCE_REQUIRED", "type": ` + customText + ` }
		  ],
		  "nests": [
		    { "relation": "author", "presence": "PRESENCE_OPTIONAL",
		      "union": { "kind": "RELATION_UNION_REF", "entity": { "catalog": "shop", "namespace": "app", "object": "users" } },
		      "supplies": ["author_id"], "needsOne": true },
		    { "relation": "editor", "presence": "PRESENCE_OPTIONAL", "nullable": true,
		      "union": { "kind": "RELATION_UNION_REF", "entity": { "catalog": "shop", "namespace": "app", "object": "users" } },
		      "supplies": ["editor_id"] }
		  ] }`,
		`{ "id": { "entity": { "catalog": "shop", "namespace": "app", "object": "posts" }, "preset": "PRESET_DRAFT", "form": "FORM_FLAT", "forRelation": "author" },
		  "hostName": { "words": ["post", "draft", "for", "author"] },
		  "entity": { "catalog": "shop", "namespace": "app", "object": "posts" },
		  "lineage": [
		    { "op": "PRESET", "entity": { "catalog": "shop", "namespace": "app", "object": "posts" }, "preset": "PRESET_DRAFT", "form": "FORM_FLAT" },
		    { "op": "FOR_RELATION", "relation": "author" }
		  ],
		  "fields": [
		    { "column": "id", "presence": "PRESENCE_OPTIONAL", "type": ` + customI64 + ` },
		    { "column": "title", "presence": "PRESENCE_REQUIRED", "type": ` + customText + ` }
		  ],
		  "forbidden": [{ "column": "author_id", "reason": "FORBIDDEN_PARENT" }] }`,
		`{ "id": { "entity": { "catalog": "shop", "namespace": "app", "object": "posts" }, "preset": "PRESET_DRAFT", "form": "FORM_NESTED", "forRelation": "author" },
		  "hostName": { "words": ["post", "draft", "nested", "for", "author"] },
		  "entity": { "catalog": "shop", "namespace": "app", "object": "posts" },
		  "lineage": [
		    { "op": "PRESET", "entity": { "catalog": "shop", "namespace": "app", "object": "posts" }, "preset": "PRESET_DRAFT", "form": "FORM_NESTED" },
		    { "op": "FOR_RELATION", "relation": "author" }
		  ],
		  "fields": [
		    { "column": "editor_id", "presence": "PRESENCE_OPTIONAL", "nullable": true, "type": ` + customI64 + ` },
		    { "column": "title", "presence": "PRESENCE_REQUIRED", "type": ` + customText + ` }
		  ],
		  "nests": [
		    { "relation": "editor", "presence": "PRESENCE_OPTIONAL", "nullable": true,
		      "union": { "kind": "RELATION_UNION_REF", "entity": { "catalog": "shop", "namespace": "app", "object": "users" } },
		      "supplies": ["editor_id"] }
		  ] }`,
		`{ "id": { "entity": { "catalog": "shop", "namespace": "app", "object": "tags" }, "preset": "PRESET_FULL", "form": "FORM_FLAT" },
		  "hostName": { "words": ["tag", "full"] },
		  "entity": { "catalog": "shop", "namespace": "app", "object": "tags" },
		  "lineage": [{ "op": "PRESET", "entity": { "catalog": "shop", "namespace": "app", "object": "tags" }, "preset": "PRESET_FULL", "form": "FORM_FLAT" }],
		  "fields": [
		    { "column": "id", "presence": "PRESENCE_REQUIRED", "type": ` + customI64 + ` },
		    { "column": "name", "presence": "PRESENCE_REQUIRED", "type": ` + customText + ` }
		  ] }`,
		`{ "id": { "entity": { "catalog": "shop", "namespace": "app", "object": "tags" }, "preset": "PRESET_DRAFT", "form": "FORM_FLAT" },
		  "hostName": { "words": ["tag", "draft"] },
		  "entity": { "catalog": "shop", "namespace": "app", "object": "tags" },
		  "lineage": [{ "op": "PRESET", "entity": { "catalog": "shop", "namespace": "app", "object": "tags" }, "preset": "PRESET_DRAFT", "form": "FORM_FLAT" }],
		  "fields": [{ "column": "name", "presence": "PRESENCE_REQUIRED", "type": ` + customText + ` }],
		  "forbidden": [{ "column": "id", "reason": "FORBIDDEN_IDENTITY_ALWAYS" }] }`,
		`{ "id": { "entity": { "catalog": "other", "namespace": "app", "object": "ghosts" }, "preset": "PRESET_FULL", "form": "FORM_FLAT" },
		  "hostName": { "words": ["ghost", "full"] },
		  "entity": { "catalog": "other", "namespace": "app", "object": "ghosts" },
		  "lineage": [{ "op": "PRESET", "preset": "PRESET_FULL", "form": "FORM_FLAT" }] }`,
	} {
		out = append(out, customShape(t, js))
	}
	return out
}

// customFakeNester stands in for the nested-write rules of §14.5 and §14.6:
// it nests target as a REF union and records each call.
type customFakeNester struct {
	calls []string
	fail  *irv1.Diagnostic
}

func (f *customFakeNester) NestWrite(shape *irv1.Shape, rel *irv1.Relation, target *irv1.Shape) ([]*irv1.Shape, []*irv1.RelationUnion, *irv1.Diagnostic) {
	f.calls = append(f.calls, shape.GetId().GetCustom()+"."+rel.GetName().GetStored())
	if f.fail != nil {
		return nil, nil, f.fail
	}
	id := &irv1.RelationUnionId{Kind: irv1.RelationUnionKind_RELATION_UNION_REF, Entity: target.GetEntity(), Custom: shape.GetId().GetCustom()}
	customSetNest(shape, &irv1.Nest{Relation: rel.GetName().GetStored(), Presence: irv1.Presence_PRESENCE_OPTIONAL, Target: &irv1.Nest_Union{Union: id}})
	child := proto.Clone(target).(*irv1.Shape)
	child.Id.ForRelation = rel.GetName().GetStored()
	return []*irv1.Shape{child}, []*irv1.RelationUnion{{Id: id, HostName: &irv1.Identifier{Words: []string{"x", "ref"}}}}, nil
}
