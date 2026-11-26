package gen

import (
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// query returns the bundle's query of the given name.
func query(t *testing.T, b *irv1.Bundle, name string) *irv1.Query {
	t.Helper()
	for _, q := range b.GetQueries().GetQueries() {
		if q.GetName().GetStored() == name {
			return q
		}
	}
	t.Fatalf("no query %s", name)
	return nil
}

// contains checks that file holds every part.
func contains(t *testing.T, files map[string]string, file string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(files[file], p) {
			t.Errorf("%s lacks %q:\n%s", file, p, files[file])
		}
	}
}

// table returns the bundle's table of the given stored name.
func table(t *testing.T, b *irv1.Bundle, name string) *irv1.Table {
	t.Helper()
	for _, tb := range b.GetCatalogs()[0].GetNamespaces()[0].GetTables() {
		if tb.GetName().GetStored() == name {
			return tb
		}
	}
	t.Fatalf("no table %s", name)
	return nil
}

// §16.2, §22.5, §23: the dialect file follows the capability profile.
func TestDialectFile(t *testing.T) {
	b := shopShapes(t)
	files, codes := generate(t, b, Options{})
	if len(codes) != 0 {
		t.Fatal(codes)
	}
	contains(t, files, "dql-dialect.dql.ts",
		"  name: \"postgres\",\n  setup: [\n    \"SET TIME ZONE 'UTC'\",\n  ],\n",
		`      "23505": "UniqueViolation",`,
		`      [{ catalog: "shop", schema: "app", object: "users", class: "UniqueViolation", name: "users_pkey" }, { name: "users_pkey", kind: "primary_key", sensitive: false }],`,
		`      [{ catalog: "shop", schema: "app", object: "users", class: "NotNullViolation", name: "email" }, { name: "email", kind: "not_null", sensitive: true }],`,
		"  replication: {\n    position: \"SELECT pg_current_wal_lsn()::text\",",
		"  maxParams: 65535,\n",
		`  statementModes: ["prepare", "describe", "simple"],`,
		`export const catalogs: readonly string[] = ["shop"];`,
		"  return Executor.open(driver, dialect, config, options);\n",
	)
	contains(t, files, "dql-fragments.dql.ts", "  placeholder: \"dollar\",\n", "    \"col.users.email\": \"u.\\\"email\\\"\",\n")
	if strings.Contains(files["index.dql.ts"], "dql-fragments") || strings.Contains(files["index.dql.ts"], "dql-stores") {
		t.Errorf("the index exports the package's own modules:\n%s", files["index.dql.ts"])
	}

	// A dialect with no replicas, limits, setup or rules, other
	// placeholders, and modes this runtime does not know.
	p := b.GetCapabilities()
	p.ConsistencyLevels = []irv1.ConsistencyLevel{irv1.ConsistencyLevel_CONSISTENCY_PRIMARY}
	p.MaxParams, p.SessionSetup, p.Violations = 0, nil, nil
	p.ErrorCodes = append(p.ErrorCodes, &irv1.ErrorCode{Code: "XX000"})
	p.StatementModes = []irv1.StatementMode{irv1.StatementMode_STATEMENT_MODE_PREPARE, 99}
	t2 := b.GetQueries().GetFragments()
	t2.Placeholder = irv1.Placeholder_PLACEHOLDER_QUESTION
	files, codes = generate(t, b, Options{})
	if len(codes) != 0 {
		t.Fatal(codes)
	}
	d := files["dql-dialect.dql.ts"]
	for _, absent := range []string{"replication", "maxParams", "SET TIME", "XX000"} {
		if strings.Contains(d, absent) {
			t.Errorf("dialect has %s:\n%s", absent, d)
		}
	}
	contains(t, files, "dql-dialect.dql.ts", "  setup: [],\n", "    violations: [],\n", `  statementModes: ["prepare"],`)
	contains(t, files, "dql-fragments.dql.ts", "  placeholder: \"question\",\n")

	// Rules whose kind names no constraint are left out; a placeholder
	// style the runtime lacks is its default.
	p.Violations = []*irv1.ViolationReport{{Kind: irv1.ErrorKind_ERROR_KIND_DEADLOCK, Object: qn("users"), Reported: "x", Rule: "x"}}
	p.StatementModes = nil
	t2.Placeholder = irv1.Placeholder_PLACEHOLDER_UNSPECIFIED
	files, _ = generate(t, b, Options{})
	if strings.Contains(files["dql-dialect.dql.ts"], `name: "x"`) || strings.Contains(files["dql-dialect.dql.ts"], "statementModes") {
		t.Error("unknown rule or empty modes kept")
	}
	contains(t, files, "dql-fragments.dql.ts", "  placeholder: \"dollar\",\n")

	// Without a fragment table there are no stores; without a profile there
	// is no query surface.
	b.Queries = nil
	files, _ = generate(t, b, Options{})
	if _, ok := files["dql-dialect.dql.ts"]; !ok {
		t.Error("no dialect without a fragment table")
	}
	for _, name := range []string{"dql-fragments.dql.ts", "dql-stores.dql.ts", "dql-db.dql.ts"} {
		if _, ok := files[name]; ok {
			t.Errorf("%s generated without a fragment table", name)
		}
	}
	b.Capabilities = nil
	files, _ = generate(t, b, Options{})
	if _, ok := files["dql-dialect.dql.ts"]; ok {
		t.Error("dialect generated without a profile")
	}
}

// §22.5: a query's default consistency, the primary at a level the dialect
// lacks.
func TestQueryConsistency(t *testing.T) {
	b := shopShapes(t)
	set := func(name string, c *irv1.Consistency) { query(t, b, name).Consistency = c }
	set("tag.count", &irv1.Consistency{Level: irv1.ConsistencyLevel_CONSISTENCY_ANY})
	set("tag.exists", &irv1.Consistency{Level: irv1.ConsistencyLevel_CONSISTENCY_BOUNDED_STALENESS, MaxStaleness: durationpb.New(2 * time.Second)})
	set("tag.get.by_name", &irv1.Consistency{Level: irv1.ConsistencyLevel_CONSISTENCY_READ_YOUR_WRITES})
	set("tag.find.by_name", &irv1.Consistency{Level: irv1.ConsistencyLevel_CONSISTENCY_PRIMARY})
	b.GetCapabilities().ConsistencyLevels = slices.DeleteFunc(b.GetCapabilities().GetConsistencyLevels(), func(l irv1.ConsistencyLevel) bool {
		return l == irv1.ConsistencyLevel_CONSISTENCY_READ_YOUR_WRITES
	})
	files, codes := generate(t, b, Options{})
	if len(codes) != 0 {
		t.Fatal(codes)
	}
	contains(t, files, "dql-stores.dql.ts",
		"  kind: \"count\",\n  consistency: Consistency.any,\n",
		"  kind: \"exists\",\n  consistency: Consistency.bounded(2000),\n",
		// The dialect lacks read-your-writes.
		"  name: \"tag.get.by_name\",\n  catalog: \"shop\",\n  kind: \"get\",\n  atMostOne: true,\n  consistency: Consistency.primary,\n",
		"  name: \"tag.find.by_name\",\n  catalog: \"shop\",\n  kind: \"find\",\n  atMostOne: true,\n  consistency: Consistency.primary,\n",
	)
	// A level this emitter does not know reads from the primary.
	set("tag.count", &irv1.Consistency{Level: 99})
	b.GetCapabilities().ConsistencyLevels = append(b.GetCapabilities().ConsistencyLevels, irv1.ConsistencyLevel_CONSISTENCY_READ_YOUR_WRITES, 99)
	files, _ = generate(t, b, Options{})
	contains(t, files, "dql-stores.dql.ts", "  kind: \"count\",\n  consistency: Consistency.primary,\n", "consistency: Consistency.readYourWrites,")
	typecheck(t, files)
}

// §16.3: builder queries the emitter does not know are left out with a
// warning; plans and parameters it cannot fill are errors.
func TestQueryFailures(t *testing.T) {
	b := shopShapes(t)
	qs := b.GetQueries()
	add := func(name string, tables ...*irv1.QualifiedName) *irv1.Query {
		q := &irv1.Query{Name: &irv1.Identifier{Stored: name}, Source: irv1.QuerySource_QUERY_SOURCE_BUILDER, Tables: tables}
		qs.Queries = append(qs.Queries, q)
		return q
	}
	add("tag.merge", qn("tags"))
	add("tag.get.by_name.extra", qn("tags"))
	add("tag.get.name", qn("tags"))
	add("tag.delete.quick_invite", qn("tags"))
	add("post.count", qn("tags"))
	add("nothing.count", qn("nothing"))
	add("tag.untabled")
	// Operations the stores do not generate methods for are left out
	// without a warning, and so are custom write shapes.
	add("tag.update_where", qn("tags"))
	add("user.insert.quick_invite", qn("users"))
	qs.Queries = append(qs.Queries, &irv1.Query{Name: &irv1.Identifier{Stored: "GetTag"}, Source: irv1.QuerySource_QUERY_SOURCE_FILE})
	_, codes := generate(t, b, Options{})
	want := []string{
		"SEVERITY_WARNING ts-query-unsupported shop.app.nothing",
		"SEVERITY_WARNING ts-query-unsupported shop.app.tags",
		"SEVERITY_WARNING ts-query-unsupported shop.app.tags",
		"SEVERITY_WARNING ts-query-unsupported shop.app.tags",
		"SEVERITY_WARNING ts-query-unsupported shop.app.tags",
		"SEVERITY_WARNING ts-query-unsupported shop.app.tags",
	}
	if !slices.Equal(codes, want) {
		t.Errorf("codes %q", codes)
	}

	b = shopShapes(t)
	q := query(t, b, "tag.count")
	q.GetPlan().GetParts()[0].Fragment = "nope"
	q = query(t, b, "tag.get.by_name")
	q.Params = append(q.Params, &irv1.Param{Name: "stray"})
	q = query(t, b, "tag.exists")
	q.Scope = &irv1.ScopeUse{Columns: []string{"tenant_id"}}
	_, codes = generate(t, b, Options{})
	want = []string{
		"SEVERITY_ERROR ts-fragment-unknown shop.app.tags",
		"SEVERITY_ERROR ts-reference-unknown shop.app.tags",
		"SEVERITY_ERROR ts-reference-unknown shop.app.tags",
	}
	if !slices.Equal(codes, want) {
		t.Errorf("codes %q", codes)
	}
}

// §20.4: without a key lookup, a versioned write cannot tell NotFound from
// ConcurrentModification, so it runs as an unversioned one; without get, an
// empty Partial is not short-cut.
func TestWritesWithoutLookups(t *testing.T) {
	b := shopShapes(t)
	b.GetQueries().Queries = slices.DeleteFunc(b.GetQueries().GetQueries(), func(q *irv1.Query) bool {
		n := q.GetName().GetStored()
		return n == "user.find.by_primary" || n == "profile.get.by_primary"
	})
	files, codes := generate(t, b, Options{})
	if len(codes) != 0 {
		t.Fatal(codes)
	}
	contains(t, files, "dql-stores.dql.ts",
		"    return oneOf(this.w, statementOf(userPatch, fragments, args), decodeUserFull);\n",
		"    return execByKey(this.w, statementOf(userDelete, fragments, { \"key.id\": k.id, \"key.version\": k.version, \"scope.tenant_id\": this.scope.tenantId }));\n",
		"    const args: Args = { \"key.user_id\": k.userId };\n    if (p.bio !== undefined) {\n      args[\"bio\"] = p.bio;\n    }\n",
	)
	stores := files["dql-stores.dql.ts"]
	if profiles := stores[strings.Index(stores, "export class ProfilesWrites"):]; strings.Contains(profiles[:strings.Index(profiles, "\n}\n")], "return this.get(k);") {
		t.Error("empty Partial short-cut without get")
	}
	typecheck(t, files)
}

// scopeShape is a synthetic Scope shape of table, bound by column id of
// type typ.
func scopeShape(table string, typ *irv1.Type, words ...string) *irv1.Shape {
	return &irv1.Shape{
		Id:       &irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: qn(table)}, Preset: irv1.Preset_PRESET_SCOPE},
		HostName: &irv1.Identifier{Words: words}, Entity: qn(table),
		Fields: []*irv1.Field{{Column: "id", Type: typ}},
	}
}

// §21.1: entities whose bound columns differ get scope types and handles of
// their own, entities with the same bound columns share one, and a handle
// covers the entities whose bound columns it holds.
func TestScopeGroups(t *testing.T) {
	b := shopShapes(t)
	uuid := &irv1.Type{Kind: &irv1.Type_Uuid{Uuid: &irv1.Empty{}}}
	// Posts and tags are bound by id, an int64; tenants by id too, a uuid.
	b.GetShapes().Shapes = append(b.GetShapes().GetShapes(),
		scopeShape("posts", int64Type(), "post", "scope"), scopeShape("tags", int64Type(), "tag", "scope"), scopeShape("tenants", uuid, "tenant", "scope"))
	files, codes := generate(t, b, Options{})
	if len(codes) != 0 {
		t.Fatal(codes)
	}
	contains(t, files, "dql-db.dql.ts",
		"  withPostScope(s: PostScope): PostScoped {\n",
		"  withUserScope(s: UserScope): UserScoped {\n",
		"  withTenantScope(s: TenantScope): TenantScoped {\n",
		"    this.tags = new TagsWrites(executor, { id: scope.id });\n",
		"    this.posts = new PostsUnscopedWrites(executor);\n",
		"    this.publishedPosts = new PublishedPostsReads(tx);\n",
		"export function unscoped(h: { readonly executor: Executor }): UnscopedDb {\n",
	)
	db := files["dql-db.dql.ts"]
	if strings.Contains(db, "TagScoped") {
		t.Error("tags got a handle of their own")
	}
	user := db[strings.Index(db, "export class UserScoped {"):]
	if user = user[:strings.Index(user, "constructor")]; strings.Contains(user, "posts:") || !strings.Contains(user, "users:") {
		t.Errorf("UserScoped covers %s", user)
	}
	typecheck(t, files)
}

// §26.1: a table may not take a field name handles have, and a module may
// not take a name it imports from the runtime.
func TestHandleCollisions(t *testing.T) {
	b := shopShapes(t)
	table(t, b, "tags").GetName().Words = []string{"run"}
	table(t, b, "tenants").GetName().Words = []string{"with", "scope"}
	_, codes := generate(t, b, Options{})
	want := []string{"SEVERITY_ERROR ts-name-collision shop.app.tags", "SEVERITY_ERROR ts-name-collision shop.app.tenants"}
	if !slices.Equal(codes, want) {
		t.Errorf("codes %q", codes)
	}

	// dql-types takes ValidationIssue from the runtime, and dql-stores Query.
	b = shopShapes(t)
	ns := b.GetCatalogs()[0].GetNamespaces()[0]
	ns.Enums = append(ns.Enums, enum("validation_issue", []string{"validation", "issue"}, "x"))
	qName, qHost := ident("query", "query")
	ns.Domains = append(ns.Domains, &irv1.Domain{Name: qName, HostName: qHost, Base: &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}}})
	for _, p := range query(t, b, "tag.get.by_name").GetParams() {
		p.Type = &irv1.Type{Kind: &irv1.Type_Named{Named: &irv1.NamedRef{Kind: &irv1.NamedRef_Domain{Domain: qn("query")}}}}
	}
	_, codes = generate(t, b, Options{})
	want = []string{"SEVERITY_ERROR ts-name-collision ", "SEVERITY_ERROR ts-name-collision "}
	if !slices.Equal(codes, want) {
		t.Errorf("codes %q", codes)
	}
}

// A key column's parameter does not take a name the method body binds.
func TestKeyParams(t *testing.T) {
	b := shopShapes(t)
	for _, c := range table(t, b, "tags").GetColumns() {
		if c.GetName().GetStored() == "name" {
			c.HostName.Words = []string{"args"}
		}
	}
	files, codes := generate(t, b, Options{})
	if len(codes) != 0 {
		t.Fatal(codes)
	}
	contains(t, files, "dql-stores.dql.ts", "  async getByName(args_: SlugText): Promise<TagFull> {\n", `{ "key.name": args_ }`)
	contains(t, files, "tag.dql.ts", "  getByName(args_: SlugText): Promise<TagFull>;\n")
	typecheck(t, files)
}

// §26.4: the database type a value is sent as.
func TestDBType(t *testing.T) {
	native := func(ns, name string) *irv1.NativeType {
		return &irv1.NativeType{Name: &irv1.QualifiedName{Namespace: ns, Object: name}}
	}
	email := &irv1.QualifiedName{Catalog: "shop", Namespace: "app", Object: "email"}
	domain := &irv1.Type{Kind: &irv1.Type_Named{Named: &irv1.NamedRef{Kind: &irv1.NamedRef_Domain{Domain: email}}}, Native: native("app", "email")}
	text := &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}, Native: native("pg_catalog", "text")}
	g := &generator{named: map[key]named{keyOf(email): {base: text}}}
	array := func(elem *irv1.Type, n *irv1.NativeType) *irv1.Type {
		return &irv1.Type{Kind: &irv1.Type_Array{Array: &irv1.ArrayType{Element: elem}}, Native: n}
	}
	for _, c := range []struct {
		t    *irv1.Type
		list bool
		want string
	}{
		{text, false, "pg_catalog.text"},
		{text, true, "pg_catalog._text"},
		{domain, false, "pg_catalog.text"},
		{domain, true, "pg_catalog._text"},
		{array(&irv1.Type{Kind: &irv1.Type_Int32{Int32: &irv1.Empty{}}}, native("pg_catalog", "_int4")), false, "pg_catalog._int4"},
		{array(domain, native("app", "_email")), false, "pg_catalog._text"},
		{array(text, native("pg_catalog", "_text")), true, ""},
		{&irv1.Type{Native: native("", "mood")}, false, "mood"},
		{&irv1.Type{Kind: &irv1.Type_Int32{Int32: &irv1.Empty{}}}, false, ""},
		{nil, false, ""},
	} {
		if got := g.dbType(c.t, c.list); got != c.want {
			t.Errorf("%v %v: got %q, want %q", c.t, c.list, got, c.want)
		}
	}
}

// A dialect's own fragment for an operator on a column, such as MySQL's
// typed list for in (§25.3), takes the place of the operator's.
func TestColumnOperatorFragment(t *testing.T) {
	b := shopShapes(t)
	ft := b.GetQueries().GetFragments()
	ft.Fragments = append(ft.Fragments, &irv1.Fragment{Id: "op.in.posts.id", Template: "{0} IN ({1})"})
	files, codes := generate(t, b, Options{})
	if len(codes) != 0 {
		t.Fatal(codes)
	}
	contains(t, files, "post.dql.ts",
		`return compare<PostsTable>("op.in.posts.id", "col.posts.id", `,
		`return compare<PostsTable>("op.in", "col.posts.author_id", `)
}
