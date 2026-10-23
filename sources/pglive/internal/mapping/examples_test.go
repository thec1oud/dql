package mapping

import (
	"slices"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// The catalog examples of the design record that pg-live produces, written as
// pg-live emits them. The record shows the catalog after the core has run,
// so these leave out what the core adds: words and host names (§4.2),
// whether a uniqueness fact identifies rows (§11.2), logical keys and
// nullability narrowed by annotations (§6.4), overload signatures (§4.6) and
// annotation values in their key's form (§7.6). Each example must be
// contained in the object it describes, apart from the fields it elides
// (implementation guide §6.1).
func TestRecordExamples(t *testing.T) {
	shopCat, _ := mapped(t, shop, shopOptions)
	exCat, _ := mapped(t, examples, shopOptions)
	users, posts := table(t, shopCat, "users"), table(t, shopCat, "posts")
	cases := []struct {
		section string
		got     proto.Message
		want    string
	}{
		{"§4.1 foreign key", foreignKey(t, posts, "posts_author_id_fkey"), `{
		  "name": { "stored": "posts_author_id_fkey" },
		  "columns": ["author_id"],
		  "references": {
		    "table": { "catalog": "shop", "namespace": "app", "object": "users" },
		    "columns": ["id"]
		  }
		}`},
		{"§6.2 trigger", extension(t, table(t, exCat, "users").Extensions, "users_touch_updated_at"), `{
		  "name": "users_touch_updated_at",
		  "timing": "BEFORE",
		  "events": ["UPDATE"],
		  "function": { "catalog": "shop", "namespace": "app", "object": "touch_updated_at" }
		}`},
		// Postgres reports every view column nullable; the nonnull
		// annotations make id and title non-null in the core (§6.4).
		{"§6.4 view", table(t, shopCat, "published_posts"), `{
		  "name": { "stored": "published_posts" },
		  "kind": "RELATION_KIND_VIEW",
		  "columns": [
		    { "name": { "stored": "id" }, "ordinal": 1, "type": { "int64": {} }, "nullable": true,
		      "nullabilitySource": "NULLABILITY_SOURCE_UNPROVEN",
		      "annotations": [{ "key": "core.nonnull", "origin": { "kind": "ORIGIN_DB_COMMENT" } }] },
		    { "name": { "stored": "title" }, "ordinal": 2, "type": { "text": {} }, "nullable": true,
		      "nullabilitySource": "NULLABILITY_SOURCE_UNPROVEN",
		      "annotations": [{ "key": "core.nonnull", "origin": { "kind": "ORIGIN_DB_COMMENT" } }] },
		    { "name": { "stored": "author_id" }, "ordinal": 3, "type": { "int64": {} }, "nullable": true,
		      "nullabilitySource": "NULLABILITY_SOURCE_UNPROVEN" },
		    { "name": { "stored": "published_at" }, "ordinal": 4, "type": { "instant": { "precision": 6 } },
		      "nullable": true, "nullabilitySource": "NULLABILITY_SOURCE_UNPROVEN" }
		  ],
		  "dependsOn": [{ "catalog": "shop", "namespace": "app", "object": "posts" }],
		  "annotations": [{ "key": "core.logical-key", "value": { "string": "id" },
		    "origin": { "kind": "ORIGIN_DB_COMMENT", "plugin": "pg-live", "location": "COMMENT ON VIEW app.published_posts" } }]
		}`},
		{"§6.5 routine", routine(t, shopCat, "post_score"), `{
		  "name": { "stored": "post_score" },
		  "overloads": [
		    {
		      "kind": "ROUTINE_KIND_FUNCTION",
		      "arguments": [
		        { "name": { "stored": "p_id" }, "type": { "int64": {} }, "mode": "ARG_MODE_IN" }
		      ],
		      "returns": { "scalar": { "type": { "decimal": {} }, "nullable": true } },
		      "nullBehavior": "NULL_BEHAVIOR_STRICT",
		      "volatility": "VOLATILITY_STABLE",
		      "annotations": [{ "key": "core.public", "origin": { "kind": "ORIGIN_DB_COMMENT" } }]
		    },
		    {
		      "kind": "ROUTINE_KIND_FUNCTION",
		      "arguments": [
		        { "name": { "stored": "p_id" }, "type": { "int64": {} }, "mode": "ARG_MODE_IN" },
		        { "name": { "stored": "window_days" }, "type": { "int32": {} }, "mode": "ARG_MODE_IN" }
		      ],
		      "returns": { "scalar": { "type": { "decimal": {} }, "nullable": true } },
		      "nullBehavior": "NULL_BEHAVIOR_STRICT",
		      "volatility": "VOLATILITY_STABLE",
		      "annotations": [{ "key": "core.public", "origin": { "kind": "ORIGIN_DB_COMMENT" } }]
		    }
		  ]
		}`},
		{"§7.5 password_hash", column(t, users, "password_hash"), `{
		  "name": { "stored": "password_hash" },
		  "ordinal": 5,
		  "type": { "text": {} },
		  "annotations": [
		    { "key": "core.sensitive",
		      "origin": { "kind": "ORIGIN_DB_COMMENT", "plugin": "pg-live",
		                  "location": "COMMENT ON COLUMN app.users.password_hash" } },
		    { "key": "core.write-only",
		      "origin": { "kind": "ORIGIN_DB_COMMENT", "plugin": "pg-live",
		                  "location": "COMMENT ON COLUMN app.users.password_hash" } }
		  ],
		  "extensions": [{ "@type": "type.dql.dev/dql.ext.postgres.v1.Comment", "text": "@write-only @sensitive" }]
		}`},
		// The comment's value as written; the core splits it into the words
		// [user, profile] of §7.6 and puts config's annotation above it.
		{"§7.6 comment annotation", foreignKey(t, table(t, shopCat, "profiles"), "profiles_user_id_fkey"), `{
		  "annotations": [
		    { "key": "core.inverse-name", "value": { "string": "user_profile" },
		      "origin": { "kind": "ORIGIN_DB_COMMENT", "plugin": "pg-live",
		                  "location": "COMMENT ON CONSTRAINT profiles_user_id_fkey ON app.profiles" } }
		  ]
		}`},
		{"§8.2 native type", column(t, users, "display_name").Type, `{
		  "varchar": { "length": 80, "unit": "LENGTH_UNIT_CHARACTERS" },
		  "native": {
		    "name": { "namespace": "pg_catalog", "object": "varchar" },
		    "modifiers": [{ "int": 80 }],
		    "raw": "character varying(80)"
		  }
		}`},
		// format_type qualifies the type, since pg-live reads with an empty
		// search_path; the record's raw "citext" assumes public on it.
		{"§8.3 opaque type", column(t, table(t, exCat, "users"), "nickname").Type, `{
		  "opaque": {},
		  "native": { "name": { "namespace": "public", "object": "citext" }, "raw": "public.citext" }
		}`},
		{"§8.4 enum", enum(t, shopCat, "post_status"), `{
		  "name": { "stored": "post_status" },
		  "labels": ["draft", "published", "archived"],
		  "origin": "ENUM_ORIGIN_NATIVE"
		}`},
		{"§8.5 domain", domain(t, shopCat, "email_address"), `{
		  "name": { "stored": "email_address" },
		  "base": { "text": {} },
		  "nullable": true,
		  "constraints": [
		    { "name": "email_address_check",
		      "portable": { "length": { "max": 320, "unit": "LENGTH_UNIT_CHARACTERS" } } }
		  ]
		}`},
		{"§8.5 composite", composite(t, shopCat, "money_amount"), `{
		  "name": { "stored": "money_amount" },
		  "attributes": [
		    { "name": { "stored": "amount" }, "type": { "decimal": { "precision": 12, "scale": 2 } }, "nullable": true },
		    { "name": { "stored": "currency" }, "type": { "char": { "length": 3, "unit": "LENGTH_UNIT_CHARACTERS" } }, "nullable": true }
		  ]
		}`},
		{"§8.6 array", column(t, posts, "labels"), `{
		  "type": {
		    "array": { "element": { "text": {} }, "elementNullable": true, "dimensions": 1 },
		    "native": { "name": { "namespace": "pg_catalog", "object": "_text" }, "raw": "text[]" }
		  },
		  "nullable": true
		}`},
		{"§8.8 time", column(t, users, "created_at").Type, `{
		  "instant": { "precision": 6 },
		  "native": { "name": { "namespace": "pg_catalog", "object": "timestamptz" }, "raw": "timestamp with time zone" }
		}`},
		// §8.9 shows a MySQL collation. The shop database's default collation
		// is C, which compares bytes: every fact is exact.
		{"§8.9 collation", column(t, users, "password_hash").Collation, `{
		  "name": "default",
		  "caseSensitive": "TRISTATE_TRUE",
		  "accentSensitive": "TRISTATE_TRUE",
		  "padSpace": "TRISTATE_FALSE",
		  "deterministic": "TRISTATE_TRUE",
		  "clientFold": "CLIENT_FOLD_NONE"
		}`},
		// Postgres prints the opaque check with the parentheses of its
		// deparser, which the record's text leaves out.
		{"§10.4 table constraints", &irv1.Table{Constraints: posts.Constraints}, `{
		  "constraints": [
		    { "name": "posts_published_check", "columns": ["published_at", "status"],
		      "opaque": { "native": "CHECK (((published_at IS NULL) OR (status <> 'draft'::app.post_status)))" } },
		    { "name": "posts_title_check", "columns": ["title"],
		      "portable": { "length": { "min": 1, "max": 200, "unit": "LENGTH_UNIT_CHARACTERS" } },
		      "clientValidated": true }
		  ]
		}`},
		{"§10.4 domain constraint", domain(t, shopCat, "slug_text").Constraints[0], `{
		  "name": "slug_text_check",
		  "portable": { "pattern": { "regex": "^[a-z0-9]+(-[a-z0-9]+)*$" } },
		  "clientValidated": true
		}`},
		{"§11.2 unique constraint", uniqueness(t, users, "users_tenant_email_key"), `{
		  "name": "users_tenant_email_key",
		  "kind": "UNIQUENESS_CONSTRAINT",
		  "columns": ["tenant_id", "email"],
		  "nullsDistinct": true
		}`},
		{"§11.2 partial unique index", uniqueness(t, posts, "posts_one_published_slug"), `{
		  "name": "posts_one_published_slug",
		  "kind": "UNIQUENESS_INDEX",
		  "columns": ["slug"],
		  "predicate": { "native": "(status = 'published'::app.post_status)" },
		  "nullsDistinct": true
		}`},
		{"§11.3 identity always", column(t, users, "id"), `{
		  "name": { "stored": "id" },
		  "default": { "class": "DEFAULT_IDENTITY_ALWAYS",
		               "sequence": { "catalog": "shop", "namespace": "app", "object": "users_id_seq" } }
		}`},
		{"§11.3 constant", column(t, posts, "status"), `{
		  "name": { "stored": "status" },
		  "default": { "class": "DEFAULT_CONSTANT", "native": "'draft'::app.post_status",
		               "value": { "string": "draft" } }
		}`},
		{"§11.3 expression", column(t, users, "created_at"), `{
		  "name": { "stored": "created_at" },
		  "default": { "class": "DEFAULT_EXPRESSION", "native": "now()" }
		}`},
		{"§11.3 generated", column(t, posts, "slug"), `{
		  "name": { "stored": "slug" },
		  "generation": { "kind": "GENERATION_STORED",
		                  "native": "lower(replace(title, ' '::text, '-'::text))" }
		}`},
	}
	for _, c := range cases {
		want := c.got.ProtoReflect().New().Interface()
		if err := protojson.Unmarshal([]byte(c.want), want); err != nil {
			t.Fatalf("%s: %v", c.section, err)
		}
		if got := project(c.got.ProtoReflect(), want.ProtoReflect()).Interface(); !proto.Equal(got, want) {
			t.Errorf("%s:\n got  %v\n want %v", c.section, got, want)
		}
	}

	// Zero values are absent from protobuf JSON, so containment cannot check
	// them; these are the ones the examples state.
	if c := column(t, users, "password_hash"); c.Nullable ||
		c.NullabilitySource != irv1.NullabilitySource_NULLABILITY_SOURCE_DATABASE {
		t.Errorf("§7.5: password_hash nullable %v from %v", c.Nullable, c.NullabilitySource)
	}
	for _, o := range routine(t, shopCat, "post_score").Overloads {
		if o.Arguments[0].HasDefault {
			t.Error("§6.5: p_id has no default")
		}
	}
	if u := uniqueness(t, users, "users_tenant_email_key"); u.Identifies {
		t.Error("§11.2: whether a uniqueness fact identifies rows is the core's to decide")
	}
}

// project returns a copy of got holding only the fields want sets, so
// proto.Equal compares exactly what an example states. Repeated fields keep
// every element of got, so a list of another length differs.
func project(got, want protoreflect.Message) protoreflect.Message {
	out := got.New()
	want.Range(func(fd protoreflect.FieldDescriptor, wv protoreflect.Value) bool {
		if !got.Has(fd) {
			return true
		}
		gv := got.Get(fd)
		switch {
		case fd.IsList() && fd.Message() != nil:
			ol := out.Mutable(fd).List()
			for i := range gv.List().Len() {
				g := gv.List().Get(i).Message()
				if i < wv.List().Len() {
					g = project(g, wv.List().Get(i).Message())
				}
				ol.Append(protoreflect.ValueOfMessage(g))
			}
		case fd.Message() != nil && !fd.IsList():
			out.Set(fd, protoreflect.ValueOfMessage(project(gv.Message(), wv.Message())))
		default:
			out.Set(fd, gv)
		}
		return true
	})
	return out
}

func table(t *testing.T, c *irv1.Catalog, name string) *irv1.Table {
	t.Helper()
	return find(t, c.Namespaces[0].Tables, func(x *irv1.Table) string { return x.Name.Stored }, name)
}

func column(t *testing.T, tb *irv1.Table, name string) *irv1.Column {
	t.Helper()
	return find(t, tb.Columns, func(x *irv1.Column) string { return x.Name.Stored }, name)
}

func foreignKey(t *testing.T, tb *irv1.Table, name string) *irv1.ForeignKey {
	t.Helper()
	return find(t, tb.ForeignKeys, func(x *irv1.ForeignKey) string { return x.Name.Stored }, name)
}

func uniqueness(t *testing.T, tb *irv1.Table, name string) *irv1.Uniqueness {
	t.Helper()
	return find(t, tb.Uniqueness, func(x *irv1.Uniqueness) string { return x.Name }, name)
}

func routine(t *testing.T, c *irv1.Catalog, name string) *irv1.Routine {
	t.Helper()
	return find(t, c.Namespaces[0].Routines, func(x *irv1.Routine) string { return x.Name.Stored }, name)
}

func enum(t *testing.T, c *irv1.Catalog, name string) *irv1.Enum {
	t.Helper()
	return find(t, c.Namespaces[0].Enums, func(x *irv1.Enum) string { return x.Name.Stored }, name)
}

func domain(t *testing.T, c *irv1.Catalog, name string) *irv1.Domain {
	t.Helper()
	return find(t, c.Namespaces[0].Domains, func(x *irv1.Domain) string { return x.Name.Stored }, name)
}

func composite(t *testing.T, c *irv1.Catalog, name string) *irv1.Composite {
	t.Helper()
	return find(t, c.Namespaces[0].Composites, func(x *irv1.Composite) string { return x.Name.Stored }, name)
}

// extension unpacks the extension named name: a message whose name field
// holds it.
func extension(t *testing.T, all []*anypb.Any, name string) proto.Message {
	t.Helper()
	for _, a := range all {
		m, err := a.UnmarshalNew()
		if err != nil {
			t.Fatal(err)
		}
		if f := m.ProtoReflect().Descriptor().Fields().ByName("name"); f != nil &&
			m.ProtoReflect().Get(f).String() == name {
			return m
		}
	}
	t.Fatalf("no extension named %q", name)
	return nil
}

func find[T any](t *testing.T, items []T, key func(T) string, want string) T {
	t.Helper()
	i := slices.IndexFunc(items, func(v T) bool { return key(v) == want })
	if i < 0 {
		var zero T
		t.Fatalf("no %T named %q", zero, want)
		return zero
	}
	return items[i]
}
