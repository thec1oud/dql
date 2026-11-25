package gen

import (
	"slices"
	"strings"
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// fileQ is a query-file query of the shop catalog for the querier tests.
func fileQ(name string, words []string, card irv1.Cardinality, scope ...string) *irv1.Query {
	return &irv1.Query{Name: &irv1.Identifier{Stored: name, Words: words}, Source: irv1.QuerySource_QUERY_SOURCE_FILE, Cardinality: card,
		Catalog: "shop", Scope: &irv1.ScopeUse{Columns: scope}, Sql: &irv1.Query_Text{Text: "SELECT 1"}, Location: "q.sql:1"}
}

// querierBundle adds query-file queries to the running example, and binds
// posts by id and users by id too, so a query on @scope.id runs on both
// scoped handles.
func querierBundle(t *testing.T) (*irv1.Bundle, *irv1.Query) {
	b := shopShapes(t)
	int64T := int64Type()
	ping := fileQ("Ping", []string{"ping"}, irv1.Cardinality_CARDINALITY_EXEC)
	ping.Sql, ping.Writes = &irv1.Query_Text{Text: ""}, true
	rowsN := fileQ("Touch", []string{"touch"}, irv1.Cardinality_CARDINALITY_EXEC_ROWS)
	rowsN.Writes = true
	count := fileQ("CountTags", []string{"count", "tags"}, irv1.Cardinality_CARDINALITY_COUNT)
	count.Params = []*irv1.Param{
		{Name: "ids", Type: int64T, List: true},
		{Name: "name", Type: &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}}, Nullable: true},
		{Name: "scope", Type: int64T},
	}
	tag := fileQ("FindTag", []string{"find", "tag"}, irv1.Cardinality_CARDINALITY_OPTIONAL, "id")
	tag.AtMostOne = true
	tag.Result = &irv1.ResultShape{Kind: &irv1.ResultShape_Shape{Shape: &irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: qn("tags")}, Preset: irv1.Preset_PRESET_FULL, Form: irv1.Form_FORM_FLAT}},
		Columns: []string{"name", "id"}}
	one := fileQ("OneTag", []string{"one", "tag"}, irv1.Cardinality_CARDINALITY_ONE)
	one.Result = &irv1.ResultShape{Kind: &irv1.ResultShape_Shape{Shape: tag.GetResult().GetShape()}, Columns: []string{"id", "name"}}
	rows := fileQ("Pairs", []string{"pairs"}, irv1.Cardinality_CARDINALITY_MANY)
	rows.Result = &irv1.ResultShape{Kind: &irv1.ResultShape_Anonymous{Anonymous: &irv1.AnonymousShape{Fields: []*irv1.ResultField{
		{Name: "n", Type: int64T, NullabilitySource: irv1.NullabilitySource_NULLABILITY_SOURCE_ANNOTATION},
		{Name: "first_name", Type: &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}}, Nullable: true},
		{Name: "proven", Type: int64T},
	}}}}
	b.Queries.Queries = append(b.Queries.Queries, ping, rowsN, count, tag, one, rows)
	idField := &irv1.Field{Column: "id", Type: int64T}
	posts := &irv1.Shape{Id: &irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: qn("posts")}, Preset: irv1.Preset_PRESET_SCOPE},
		HostName: &irv1.Identifier{Words: []string{"post", "scope"}}, Entity: qn("posts"), Fields: []*irv1.Field{idField}}
	b.GetShapes().Shapes = append(b.GetShapes().GetShapes(), posts)
	for _, s := range b.GetShapes().GetShapes() {
		if s.GetId().GetPreset() == irv1.Preset_PRESET_SCOPE && s.GetEntity().GetObject() == "users" {
			s.Fields = append([]*irv1.Field{idField}, s.GetFields()...)
		}
	}
	return b, rows
}

// §17, §21, §22.2, §26.3: query-file queries on the handles their scope and
// capability allow.
func TestQueriers(t *testing.T) {
	b, rows := querierBundle(t)
	files, codes := generate(t, b, Options{})
	if len(codes) != 0 {
		t.Fatal(codes)
	}
	contains(t, files, "dql-queries.dql.ts",
		"export interface ReadQuerier {\n  countTags(ids: readonly bigint[], name: string | null, scope2: bigint): Promise<bigint>;\n  oneTag(): Promise<TagFull>;\n  pairs(): Promise<PairsRow[]>;\n}",
		"export interface Querier extends ReadQuerier {\n  ping(): Promise<void>;\n  touch(): Promise<number>;\n}",
		"  findTag(scope: PostScope): Promise<TagFull | undefined>;\n",
		`    n: d.field(0, "n", "the nonnull header of query Pairs", asInt64),`,
		`    firstName: d.nullable(1, "first_name", asString),`,
		`    proven: d.field(2, "proven", "", asInt64),`,
	)
	contains(t, files, "dql-stores.dql.ts",
		"      kind: \"\",\n      sql: \"\",\n      writes: true,\n    };\n    await execute(this.w, st);\n",
		"    return execute(this.w, st);\n",
		"      args: [ids, name, scope2],\n    };\n    return count(this.h, st);\n",
		"  async findTag(): Promise<TagFull | undefined> {\n    const scope: PostScope = { id: this.scope.id };\n",
		"      args: [scope.id],\n      atMostOne: true,\n",
		"  async findTag(): Promise<TagFull | undefined> {\n    const st: Statement = {",
		"      args: [this.scope.id],\n",
		"(r: Row) => decodeTagFull(new Row([r.values[1], r.values[0]]))",
		"    return oneOf(this.h, st, decodeTagFull);\n",
		"    return allOf(this.h, st, decodePairsRow);\n",
	)
	contains(t, files, "dql-db.dql.ts", "this.queries = new ")

	// A query on @scope.tenant_id runs only where the scope holds tenant_id;
	// its native parameters carry their database types.
	b2, _ := querierBundle(t)
	tenant := fileQ("Members", []string{"members"}, irv1.Cardinality_CARDINALITY_COUNT, "tenant_id")
	tenant.Consistency = &irv1.Consistency{Level: irv1.ConsistencyLevel_CONSISTENCY_PRIMARY}
	for _, c := range table(t, b2, "users").GetColumns() {
		if c.GetName().GetStored() == "tenant_id" {
			tenant.Params = []*irv1.Param{{Name: "other", Type: c.GetType()}}
		}
	}
	b2.Queries.Queries = append(slices.DeleteFunc(b2.Queries.Queries, func(q *irv1.Query) bool {
		return q.GetSource() == irv1.QuerySource_QUERY_SOURCE_FILE
	}), tenant)
	files, codes = generate(t, b2, Options{})
	if len(codes) != 0 {
		t.Fatal(codes)
	}
	contains(t, files, "dql-stores.dql.ts",
		"      args: [other, this.scope.tenantId],\n      types: [\"pg_catalog.uuid\", \"pg_catalog.uuid\"],\n      consistency: Consistency.primary,\n",
	)
	if strings.Contains(files["dql-queries.dql.ts"], "PostScoped") || strings.Contains(files["dql-db.dql.ts"], "new PostScopedReadQueries") {
		t.Error("post-scoped handles run a tenant-scoped query")
	}
	typecheck(t, files)
	typecheck(t, files)

	// Unknown scope columns, columns whose names meet and a store named
	// queries fail.
	lost := fileQ("Lost", []string{"lost"}, irv1.Cardinality_CARDINALITY_EXEC, "region")
	rows.Result.GetAnonymous().Fields = append(rows.Result.GetAnonymous().Fields, &irv1.ResultField{Name: "firstName", Type: int64Type()})
	b.Queries.Queries = append(b.Queries.Queries, lost)
	table(t, b, "tags").Name.Words = []string{"queries"}
	if _, codes := generate(t, b, Options{}); !slices.Equal(codes, []string{
		"SEVERITY_ERROR ts-reference-unknown Lost",
		"SEVERITY_ERROR ts-name-collision Pairs",
		"SEVERITY_ERROR ts-name-collision shop.app.tags",
	}) {
		t.Error(codes)
	}
}
