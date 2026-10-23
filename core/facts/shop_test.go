package facts

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// This file builds the design record's running example (§1.3) as the
// catalog IR pg-live produces, with words written out and config annotations
// already merged over comment annotations.

func ident(stored string, words ...string) *irv1.Identifier {
	return &irv1.Identifier{Stored: stored, Words: words}
}

func i64() *irv1.Type  { return &irv1.Type{Kind: &irv1.Type_Int64{Int64: &irv1.Empty{}}} }
func i32() *irv1.Type  { return &irv1.Type{Kind: &irv1.Type_Int32{Int32: &irv1.Empty{}}} }
func text() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}} }
func uid() *irv1.Type  { return &irv1.Type{Kind: &irv1.Type_Uuid{Uuid: &irv1.Empty{}}} }

// col is a column; nullable columns are written with a trailing "?" on the
// stored name.
func col(stored string, typ *irv1.Type, words ...string) *irv1.Column {
	c := &irv1.Column{Type: typ, NullabilitySource: irv1.NullabilitySource_NULLABILITY_SOURCE_DATABASE}
	if s, ok := strings.CutSuffix(stored, "?"); ok {
		stored = s
		c.Nullable = true
		c.NullabilitySource = irv1.NullabilitySource_NULLABILITY_SOURCE_UNPROVEN
	}
	c.Name = ident(stored, words...)
	return c
}

func table(stored string, words []string, cols ...*irv1.Column) *irv1.Table {
	for i, c := range cols {
		c.Ordinal = uint32(i + 1)
	}
	return &irv1.Table{Name: ident(stored, words...), Kind: irv1.TableKind_RELATION_KIND_TABLE, Columns: cols}
}

func pk(name string, cols ...string) *irv1.Uniqueness {
	return &irv1.Uniqueness{Name: name, Kind: irv1.UniquenessKind_UNIQUENESS_PRIMARY_KEY, Columns: cols, NullsDistinct: true}
}

func uniq(name string, cols ...string) *irv1.Uniqueness {
	return &irv1.Uniqueness{Name: name, Kind: irv1.UniquenessKind_UNIQUENESS_CONSTRAINT, Columns: cols, NullsDistinct: true}
}

func fk(name string, words []string, cols []string, catalog, ns, target string, refs ...string) *irv1.ForeignKey {
	return &irv1.ForeignKey{
		Name:       ident(name, words...),
		Columns:    cols,
		References: &irv1.ColumnSet{Table: &irv1.QualifiedName{Catalog: catalog, Namespace: ns, Object: target}, Columns: refs},
		Match:      irv1.ForeignKeyMatch_FOREIGN_KEY_MATCH_SIMPLE,
	}
}

func wordsValue(words ...string) *irv1.AnnotationValue {
	l, _ := structpb.NewList(toAny(words))
	return &irv1.AnnotationValue{Kind: &irv1.AnnotationValue_Words{Words: l}}
}

func listValue(items ...string) *irv1.AnnotationValue {
	l, _ := structpb.NewList(toAny(items))
	return &irv1.AnnotationValue{Kind: &irv1.AnnotationValue_List{List: l}}
}

func stringValue(s string) *irv1.AnnotationValue {
	return &irv1.AnnotationValue{Kind: &irv1.AnnotationValue_String_{String_: s}}
}

func structValue(fields map[string]any) *irv1.AnnotationValue {
	s, err := structpb.NewStruct(fields)
	if err != nil {
		panic(err)
	}
	return &irv1.AnnotationValue{Kind: &irv1.AnnotationValue_Struct{Struct: s}}
}

func mustList(items ...any) *structpb.ListValue {
	l, err := structpb.NewList(items)
	if err != nil {
		panic(err)
	}
	return l
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func configured(key, location string, v *irv1.AnnotationValue) *irv1.Annotation {
	return &irv1.Annotation{Key: key, Value: v, Origin: &irv1.Origin{
		Kind: irv1.OriginKind_ORIGIN_CONFIG, Plugin: "pg-live", Location: "dql.yaml#annotations." + location,
	}}
}

func commented(key, location string, v *irv1.AnnotationValue) *irv1.Annotation {
	return &irv1.Annotation{Key: key, Value: v, Origin: &irv1.Origin{
		Kind: irv1.OriginKind_ORIGIN_DB_COMMENT, Plugin: "pg-live", Location: location,
	}}
}

func arg(stored string, typ *irv1.Type, words ...string) *irv1.Argument {
	return &irv1.Argument{Name: ident(stored, words...), Type: typ, Mode: irv1.ArgMode_ARG_MODE_IN}
}

func publicOverload(args ...*irv1.Argument) *irv1.Overload {
	return &irv1.Overload{
		Kind:         irv1.RoutineKind_ROUTINE_KIND_FUNCTION,
		Arguments:    args,
		Returns:      &irv1.Returns{Kind: &irv1.Returns_Scalar{Scalar: &irv1.ReturnValue{Type: &irv1.Type{Kind: &irv1.Type_Decimal{Decimal: &irv1.Decimal{}}}, Nullable: true}}},
		NullBehavior: irv1.NullBehavior_NULL_BEHAVIOR_STRICT,
		Volatility:   irv1.Volatility_VOLATILITY_STABLE,
		Annotations:  []*irv1.Annotation{commented("core.public", "", nil)},
	}
}

// shop is the running example's catalog.
func shop() *irv1.Catalog {
	tenants := table("tenants", []string{"tenants"},
		col("id", uid(), "id"),
		col("name", text(), "name"))
	tenants.Uniqueness = []*irv1.Uniqueness{pk("tenants_pkey", "id")}

	users := table("users", []string{"users"},
		col("id", i64(), "id"),
		col("tenant_id", uid(), "tenant", "id"),
		col("email", text(), "email"),
		col("display_name?", text(), "display", "name"),
		col("password_hash", text(), "password", "hash"),
		col("created_at", text(), "created", "at"),
		col("updated_at", text(), "updated", "at"),
		col("version", i32(), "version"))
	users.Uniqueness = []*irv1.Uniqueness{uniq("users_tenant_email_key", "tenant_id", "email"), pk("users_pkey", "id")}
	users.ForeignKeys = []*irv1.ForeignKey{
		fk("users_tenant_id_fkey", []string{"users", "tenant", "id", "fkey"}, []string{"tenant_id"}, "shop", "app", "tenants", "id"),
	}

	profiles := table("profiles", []string{"profiles"},
		col("user_id", i64(), "user", "id"),
		col("bio?", text(), "bio"),
		col("birthday?", text(), "birthday"))
	profiles.Uniqueness = []*irv1.Uniqueness{pk("profiles_pkey", "user_id")}
	profileFK := fk("profiles_user_id_fkey", []string{"profiles", "user", "id", "fkey"}, []string{"user_id"}, "shop", "app", "users", "id")
	inverseName := configured(keyInverseName, "shop.app.profiles.profiles_user_id_fkey", wordsValue("profile"))
	inverseName.Superseded = []*irv1.Annotation{commented(keyInverseName,
		"COMMENT ON CONSTRAINT profiles_user_id_fkey ON app.profiles", wordsValue("user", "profile"))}
	profileFK.Annotations = []*irv1.Annotation{inverseName}
	profiles.ForeignKeys = []*irv1.ForeignKey{profileFK}

	posts := table("posts", []string{"posts"},
		col("id", i64(), "id"),
		col("author_id", i64(), "author", "id"),
		col("editor_id?", i64(), "editor", "id"),
		col("title", text(), "title"),
		col("slug?", text(), "slug"),
		col("status", text(), "status"),
		col("price?", text(), "price"),
		col("labels?", text(), "labels"),
		col("published_at?", text(), "published", "at"))
	posts.Uniqueness = []*irv1.Uniqueness{pk("posts_pkey", "id")}
	authorFK := fk("posts_author_id_fkey", []string{"posts", "author", "id", "fkey"}, []string{"author_id"}, "shop", "app", "users", "id")
	authorFK.Annotations = []*irv1.Annotation{
		configured(keyCollectionPolicy, "shop.app.posts.posts_author_id_fkey", stringValue("replace")),
		configured(keyRemoval, "shop.app.posts.posts_author_id_fkey", stringValue("delete")),
	}
	posts.ForeignKeys = []*irv1.ForeignKey{
		fk("posts_editor_id_fkey", []string{"posts", "editor", "id", "fkey"}, []string{"editor_id"}, "shop", "app", "users", "id"),
		authorFK,
	}

	tags := table("tags", []string{"tags"},
		col("id", i64(), "id"),
		col("name", text(), "name"))
	tags.Uniqueness = []*irv1.Uniqueness{pk("tags_pkey", "id"), uniq("tags_name_key", "name")}

	postTags := table("post_tags", []string{"post", "tags"},
		col("post_id", i64(), "post", "id"),
		col("tag_id", i64(), "tag", "id"))
	postTags.Uniqueness = []*irv1.Uniqueness{pk("post_tags_pkey", "post_id", "tag_id")}
	postTags.ForeignKeys = []*irv1.ForeignKey{
		fk("post_tags_tag_id_fkey", []string{"post", "tags", "tag", "id", "fkey"}, []string{"tag_id"}, "shop", "app", "tags", "id"),
		fk("post_tags_post_id_fkey", []string{"post", "tags", "post", "id", "fkey"}, []string{"post_id"}, "shop", "app", "posts", "id"),
	}

	published := table("published_posts", []string{"published", "posts"},
		col("id", i64(), "id"),
		col("title", text(), "title"),
		col("author_id?", i64(), "author", "id"),
		col("published_at?", text(), "published", "at"))
	published.Kind = irv1.TableKind_RELATION_KIND_VIEW
	published.Columns[0].NullabilitySource = irv1.NullabilitySource_NULLABILITY_SOURCE_ANNOTATION
	published.Columns[1].NullabilitySource = irv1.NullabilitySource_NULLABILITY_SOURCE_ANNOTATION
	published.Annotations = []*irv1.Annotation{commented(keyLogicalKey, "COMMENT ON VIEW app.published_posts", listValue("id"))}
	published.DependsOn = []*irv1.QualifiedName{{Catalog: "shop", Namespace: "app", Object: "posts"}}

	postScore := &irv1.Routine{
		Name: ident("post_score", "post", "score"),
		Overloads: []*irv1.Overload{
			publicOverload(arg("p_id", i64(), "p", "id"), arg("window_days", i32(), "window", "days")),
			publicOverload(arg("p_id", i64(), "p", "id")),
		},
	}

	return &irv1.Catalog{
		Name:             ident("shop", "shop"),
		DefaultNamespace: "app",
		Namespaces: []*irv1.Namespace{{
			Name:     ident("app", "app"),
			Tables:   []*irv1.Table{users, posts, tenants, tags, profiles, published, postTags},
			Routines: []*irv1.Routine{postScore},
		}},
	}
}

var postgres = Options{Dialect: irv1.Dialect_DIALECT_POSTGRES}

// tableOf returns the table with stored name object in c's namespaces.
func tableOf(t *testing.T, c *irv1.Catalog, object string) *irv1.Table {
	t.Helper()
	for _, ns := range c.GetNamespaces() {
		for _, tb := range ns.GetTables() {
			if tb.GetName().GetStored() == object {
				return tb
			}
		}
	}
	t.Fatalf("no table %s", object)
	return nil
}

func relationOf(t *testing.T, c *irv1.Catalog, object, name string) *irv1.Relation {
	t.Helper()
	for _, r := range tableOf(t, c, object).GetRelations() {
		if r.GetName().GetStored() == name {
			return r
		}
	}
	t.Fatalf("no relation %s.%s", object, name)
	return nil
}

// assertContains fails unless got holds every field of want, a design record
// example written in protobuf JSON. Fields the example elides are not
// compared (implementation guide §6.1).
func assertContains(t *testing.T, got proto.Message, want string) {
	t.Helper()
	w := got.ProtoReflect().New().Interface()
	if err := protojson.Unmarshal([]byte(want), w); err != nil {
		t.Fatalf("example does not decode: %v", err)
	}
	var gm, wm any
	gb, _ := protojson.Marshal(got)
	wb, _ := protojson.Marshal(w)
	_ = json.Unmarshal(gb, &gm)
	_ = json.Unmarshal(wb, &wm)
	if path, ok := subset(wm, gm, "$"); !ok {
		t.Errorf("output differs from the example at %s\n got: %s\nwant: %s", path, gb, wb)
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

func diagLines(ds []*irv1.Diagnostic) []string {
	var out []string
	for _, d := range ds {
		out = append(out, fmt.Sprintf("%s %s %s", strings.TrimPrefix(d.GetSeverity().String(), "SEVERITY_"), d.GetCode(), dotted(d.GetTarget())))
	}
	return out
}

func codes(ds []*irv1.Diagnostic) []string {
	var out []string
	for _, d := range ds {
		out = append(out, d.GetCode())
	}
	return out
}
