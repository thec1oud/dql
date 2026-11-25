package gen

import (
	"slices"
	"strings"
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// §9.4, §18.2: a column handle has the operators the column supports and
// the fragment table holds; a relation handle reaches another table.
func TestTableHandles(t *testing.T) {
	b := shopShapes(t)
	frag := func(ids ...string) {
		for _, id := range ids {
			b.GetQueries().GetFragments().Fragments = append(b.GetQueries().GetFragments().GetFragments(), &irv1.Fragment{Id: id, Template: "x"})
		}
	}
	text := &irv1.OperandType{Kind: &irv1.OperandType_Type{Type: &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}}}}
	tv := &irv1.OperandType{Kind: &irv1.OperandType_Var{Var: "T"}}
	rangeOf := &irv1.OperandType{Kind: &irv1.OperandType_Range{Range: tv}}
	arrayOf := &irv1.OperandType{Kind: &irv1.OperandType_Array{Array: tv}}
	nested := &irv1.OperandType{Kind: &irv1.OperandType_Array{Array: arrayOf}}
	boolean := &irv1.OperandType{Kind: &irv1.OperandType_Type{Type: &irv1.Type{Kind: &irv1.Type_Bool{Bool: &irv1.Empty{}}}}}
	op := func(name string, args ...*irv1.OperandType) {
		b.Operators = append(b.Operators, &irv1.Operator{Name: name, Signature: &irv1.Signature{Args: args, Returns: boolean}})
		frag("op." + name)
	}
	op("within", rangeOf, tv)
	op("meets", rangeOf, rangeOf)
	op("deep", tv, nested)
	op("approx", text, text)
	op("first_array", arrayOf, tv)
	frag("op.postgres.native")
	users := table(t, b, "users")
	period := &irv1.Column{
		Name: &irv1.Identifier{Stored: "period"}, HostName: &irv1.Identifier{Words: []string{"period"}},
		Type: &irv1.Type{Kind: &irv1.Type_Range{Range: &irv1.RangeType{Element: int64Type()}}},
		Operators: []*irv1.ColumnOperator{
			{Name: "within"}, {Name: "meets"}, {Name: "deep"}, {Name: "first_array"}, {Name: "postgres.native"}, {Name: "unknown"},
			{Name: "approx", Fidelity: irv1.Fidelity_FIDELITY_APPROXIMATE},
		},
	}
	// A column without a fragment.
	hidden := &irv1.Column{Name: &irv1.Identifier{Stored: "hidden"}, HostName: &irv1.Identifier{Words: []string{"hidden"}}, Type: period.GetType()}
	users.Columns = append(users.Columns, period, hidden)
	frag("col.users.period")
	rel := func(name, to string) *irv1.Relation {
		frag("rel.users." + name)
		return &irv1.Relation{Name: &irv1.Identifier{Stored: name, Words: []string{name}}, To: &irv1.ColumnSet{Table: qn(to)}}
	}
	users.Relations = append(users.Relations, rel("manager", "users"), rel("audit", "audits"),
		&irv1.Relation{Name: &irv1.Identifier{Stored: "unfragmented", Words: []string{"unfragmented"}}, To: &irv1.ColumnSet{Table: qn("posts")}})
	files, codes := generate(t, b, Options{})
	if len(codes) != 0 {
		t.Fatal(codes)
	}
	contains(t, files, "user.dql.ts",
		"    within(v: bigint): Pred<UsersTable> {\n      return compare<UsersTable>(\"op.within\", \"col.users.period\", arg(v));\n",
		"    meets(v: Range<bigint>): Pred<UsersTable> {\n      return compare<UsersTable>(\"op.meets\", \"col.users.period\", arg(v));\n",
		"    /** approx is operator approx on period (§9.4). Approximate: its results can differ from the operator's (§9.2). */\n    approx(v: string): Pred<UsersTable> {",
		// A sensitive column's arguments are sensitive.
		"      return compare<UsersTable>(\"op.eq\", \"col.users.password_hash\", arg(v, true, \"pg_catalog.text\"));\n",
	)
	for _, absent := range []string{"deep(", "firstArray(", "native(", "unknown(", "hidden:", "manager:", "audit:", "unfragmented:"} {
		if strings.Contains(files["user.dql.ts"], absent) {
			t.Errorf("user.dql.ts has %s", absent)
		}
	}
	typecheck(t, files)

	// Member names that meet fail.
	users.Columns = append(users.Columns, &irv1.Column{Name: &irv1.Identifier{Stored: "Period"}, HostName: &irv1.Identifier{Words: []string{"period"}}, Type: period.GetType()})
	frag("col.users.Period")
	users.Relations = append(users.Relations, rel("email", "posts"))
	if _, codes := generate(t, b, Options{}); !slices.Equal(codes, []string{
		"SEVERITY_ERROR ts-name-collision shop.app.users",
		"SEVERITY_ERROR ts-name-collision shop.app.users",
	}) {
		t.Error(codes)
	}
}

// §18.2, §21.1: without select fragments there are no shapes; a sensitive
// scope column is a sensitive argument.
func TestSelectShapes(t *testing.T) {
	b := shopShapes(t)
	b.GetQueries().GetFragments().Fragments = slices.DeleteFunc(b.GetQueries().GetFragments().GetFragments(), func(f *irv1.Fragment) bool {
		return strings.HasPrefix(f.GetId(), "select.")
	})
	b.GetQueries().Queries = slices.DeleteFunc(b.GetQueries().GetQueries(), func(q *irv1.Query) bool {
		return !strings.Contains(q.GetName().GetStored(), ".count") && !strings.Contains(q.GetName().GetStored(), ".insert")
	})
	for _, s := range b.GetShapes().GetShapes() {
		if s.GetId().GetPreset() == irv1.Preset_PRESET_FULL && s.GetEntity().GetObject() == "users" {
			for _, f := range s.GetFields() {
				f.Redact = f.Redact || f.GetColumn() == "tenant_id"
			}
		}
	}
	files, codes := generate(t, b, Options{})
	if len(codes) != 0 {
		t.Fatal(codes)
	}
	if _, ok := files["dql-select.dql.ts"]; ok {
		t.Error("shapes without select fragments")
	}
	contains(t, files, "dql-stores.dql.ts", `arg(this.scope.tenantId, true, "pg_catalog.uuid")`)
	// The shapes of the running example.
	files, _ = generate(t, shopShapes(t), Options{})
	contains(t, files, "dql-select.dql.ts", "  readonly userFull: Shape<UserFull, UsersTable>;\n", "    decode: decodeUserFull,\n")
	typecheck(t, files)
}

// §18.4: fragment ids name a table behind its namespace when that is not
// its catalog's default, and behind its catalog when there are several.
func TestTableID(t *testing.T) {
	cat := func(name string) *irv1.Catalog {
		return &irv1.Catalog{Name: &irv1.Identifier{Stored: name}, DefaultNamespace: "app"}
	}
	g := &generator{b: &irv1.Bundle{Catalogs: []*irv1.Catalog{cat("shop")}}}
	for q, want := range map[*irv1.QualifiedName]string{
		{Catalog: "shop", Namespace: "app", Object: "users"}:   "users",
		{Catalog: "shop", Namespace: "audit", Object: "users"}: "audit.users",
		{Catalog: "other", Namespace: "app", Object: "users"}:  "app.users",
	} {
		if got := g.tableID(q); got != want {
			t.Errorf("%v: %s", q, got)
		}
	}
	g.b.Catalogs = append(g.b.Catalogs, cat("crm"))
	if got := g.tableID(&irv1.QualifiedName{Catalog: "crm", Namespace: "app", Object: "leads"}); got != "crm.leads" {
		t.Error(got)
	}
}

// softDelete marks column col of table name soft-delete (§21.3).
func softDelete(t *testing.T, b *irv1.Bundle, name, col string) *irv1.Column {
	t.Helper()
	for _, c := range table(t, b, name).GetColumns() {
		if c.GetName().GetStored() == col {
			c.Annotations = append(c.Annotations, &irv1.Annotation{Key: "core.soft-delete"})
			return c
		}
	}
	t.Fatalf("no column %s.%s", name, col)
	return nil
}

// §21.3: reads leave out soft-deleted rows, through the store and through
// relations, unless they ask for them.
func TestSoftDeleteReads(t *testing.T) {
	files, codes := generate(t, shopShapes(t), Options{})
	if len(codes) != 0 {
		t.Fatal(codes)
	}
	for name, f := range files {
		if strings.Contains(f, "visible:") || strings.Contains(f, "op.is_null\", { \"0\"") {
			t.Errorf("%s reads soft deletes without a soft-delete table", name)
		}
	}

	b := shopShapes(t)
	softDelete(t, b, "posts", "published_at")
	softDelete(t, b, "users", "updated_at")
	files, codes = generate(t, b, Options{})
	if len(codes) != 0 {
		t.Fatal(codes)
	}
	contains(t, files, "dql-stores.dql.ts",
		"    return newBuilder(fragments, { h: this.h, visible: use(\"op.is_null\", { \"0\": use(\"col.posts.published_at\") }) }, shape);\n",
		"\") })], visible: use(\"op.is_null\", { \"0\": use(\"col.users.updated_at\") }) }, shape);\n",
	)
	contains(t, files, "user.dql.ts",
		"Soft-deleted related rows are left out (§21.3).",
		`return related<UsersTable, PostsTable>("rel.users.posts_to_author", newPred<PostsTable>(use("op.is_null", { "0": use("col.posts.published_at") })), ...ps);`,
		`return not(related<UsersTable, PostsTable>("rel.users.posts_to_author", newPred<PostsTable>(use("op.is_null", { "0": use("col.posts.published_at") })), not(p)));`,
	)
	typecheck(t, files)

	// A boolean column is deleted when true.
	b = shopShapes(t)
	softDelete(t, b, "posts", "published_at").Type = &irv1.Type{Kind: &irv1.Type_Bool{Bool: &irv1.Empty{}}}
	files, _ = generate(t, b, Options{})
	contains(t, files, "dql-stores.dql.ts", `visible: use("op.is_distinct_from", { "0": use("col.posts.published_at"), "1": use("value.true") })`)
}
