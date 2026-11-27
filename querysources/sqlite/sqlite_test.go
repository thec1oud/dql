package sqlite

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"

	"github.com/thec1oud/dql/core/bundle"
	"github.com/thec1oud/dql/plugin"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/testkit/golden"
)

func TestHandshake(t *testing.T) {
	hs := New().Handshake()
	if hs.GetPlugin().GetName() != "sqlite-queries" || hs.GetPlugin().GetKind() != irv1.PluginKind_PLUGIN_KIND_QUERY_SOURCE ||
		hs.GetIrVersions().GetMin() != "1.0.0" || hs.GetIrVersions().GetMaxMajor() != 1 {
		t.Errorf("handshake = %v", hs)
	}
}

func TestInvokeRefusesOtherDialects(t *testing.T) {
	b := shop(t)
	b.Dialect = irv1.Dialect_DIALECT_POSTGRES
	_, err := New().Invoke(context.Background(), &irv1.Invoke{Bundle: b})
	if err == nil || err.Error() != "sqlite-queries: the bundle's catalogs are DIALECT_POSTGRES, not SQLite" {
		t.Errorf("err = %v", err)
	}
}

// TestShopGolden pins the queries of the running example on SQLite (§17.3).
func TestShopGolden(t *testing.T) {
	text, err := os.ReadFile("../../conformance/schemas/sqlite/queries.sql")
	if err != nil {
		t.Fatal(err)
	}
	res := run(t, string(text))
	if len(res.GetDiagnostics()) > 0 {
		t.Fatalf("diagnostics: %v", res.GetDiagnostics())
	}
	js, err := bundle.MarshalJSON(res.GetBundle())
	if err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, filepath.Join("testdata", "shop.json"), js)
}

// Every query the typing cases type is one SQLite prepares, against the
// running example's schema.
func TestQueriesPrepare(t *testing.T) {
	ddl, err := os.ReadFile("../../conformance/schemas/sqlite/shop.sql")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(string(ddl)); err != nil {
		t.Fatal(err)
	}
	for _, c := range typingCases {
		body := strings.TrimPrefix(c.body, ":exec ")
		res := run(t, "-- name: Q :exec\n"+body)
		if len(res.GetDiagnostics()) > 0 {
			t.Errorf("%s: %v", c.body, res.GetDiagnostics())
			continue
		}
		text := res.GetBundle().GetQueries().GetQueries()[0].GetText()
		st, err := db.Prepare(text)
		if err != nil {
			t.Errorf("%s: %v", text, err)
			continue
		}
		st.Close()
	}
}

func TestInvokeAcrossFiles(t *testing.T) {
	res, err := New().Invoke(context.Background(), &irv1.Invoke{Bundle: shop(t), QueryFiles: []*irv1.QueryFile{
		{Path: "a.sql", Text: "-- name: B :one\nSELECT 1 AS x\n-- name: A :one\nSELECT 2 AS x\n-- name: Bad :one\nSELECT nope"},
		{Path: "b.sql", Text: "-- name: A :one\nSELECT 3 AS x\n-- name: 9 :one\nSELECT 1"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, q := range res.GetBundle().GetQueries().GetQueries() {
		names = append(names, q.GetName().GetStored()+"@"+q.GetLocation())
	}
	if got := strings.Join(names, " "); got != "A@a.sql:3 B@a.sql:1" {
		t.Errorf("queries = %s", got)
	}
	var ds []string
	for _, d := range res.GetDiagnostics() {
		ds = append(ds, d.GetQuery()+" "+d.GetCode()+": "+d.GetMessage())
	}
	want := []string{
		"A query-duplicate: b.sql:1: query A is also defined at a.sql:3; give each query its own name",
		`9 query-header: b.sql:3: "9" is not a query name; write -- name: <Name> :<cardinality>`,
		"Bad query-unknown-column: a.sql:6:8: query Bad: column nope is not a column of the FROM clause",
	}
	if got := strings.Join(ds, "\n"); got != strings.Join(want, "\n") {
		t.Errorf("diagnostics:\n%s\nwant\n%s", got, strings.Join(want, "\n"))
	}
}

// A query's text is SQLite's, with ? placeholders, and its placeholders say
// which parameter each takes: the query's parameters first, then its scope
// columns. A list is one JSON array, which json_each expands.
func TestText(t *testing.T) {
	res := run(t, "-- name: Q :many\n-- unscoped\nSELECT id FROM users\nWHERE tenant_id = @scope.tenant_id AND email IN (sqlc.slice(emails))\n"+
		"  AND id IN ( sqlc.slice(ids) ) AND (@name = '' OR display_name = @name)")
	q := res.GetBundle().GetQueries().GetQueries()[0]
	want := "SELECT id FROM users\nWHERE tenant_id = ? AND email IN (SELECT value FROM json_each(?))\n" +
		"  AND id IN (SELECT value FROM json_each(?)) AND (? = '' OR display_name = ?)"
	if q.GetText() != want || describe(q) != "@emails:text[] @ids:int64[] @name:text @scope.tenant_id id:int64" {
		t.Errorf("query %s\n%s", describe(q), q.GetText())
	}
	if got := q.GetPlaceholders(); !slices32(got, 3, 0, 1, 2, 2) || !q.GetScope().GetUnscoped() || q.GetCatalog() != "shop" {
		t.Errorf("placeholders %v, scope %v, catalog %q", got, q.GetScope(), q.GetCatalog())
	}
	res = run(t, "-- name: Q :exec\nSELECT 1")
	if q := res.GetBundle().GetQueries().GetQueries()[0]; q.GetResult() != nil || q.GetScope() != nil || q.GetPlaceholders() != nil {
		t.Errorf("query = %v", q)
	}
}

func slices32(got []uint32, want ...uint32) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestSyntaxErrors(t *testing.T) {
	res := run(t, "-- name: Q :one\nSELECT id\nFROM users\nWHERE id = @id AND AND")
	d := res.GetDiagnostics()[0]
	if d.GetCode() != "query-syntax" || d.GetMessage() != `q.sql:4:20: query Q: syntax error at "AND"` {
		t.Errorf("diagnostic = %v", d)
	}
	if got := syntaxMessage("SELECT "+strings.Repeat("x", 50), 7); got != `syntax error at "`+strings.Repeat("x", 40)+`..."` {
		t.Errorf("syntaxMessage = %s", got)
	}
	if got := (&queryError{msg: "m"}).Error(); got != "m" {
		t.Errorf("Error = %q", got)
	}
	if got := (&syntaxError{}).Error(); got != "syntax error" {
		t.Errorf("Error = %q", got)
	}
}

// more returns the shop bundle with a second catalog, crm, which has a
// tags table of its own and a table deals; with an annotated non-null
// column, users.display_name; and with a table loose of weak columns, as a
// table that is not STRICT has (§8.10).
func more(t *testing.T) *irv1.Bundle {
	b := shop(t)
	id := func(s string) *irv1.Identifier { return &irv1.Identifier{Stored: s} }
	col := &irv1.Column{Name: id("id"), Type: tInt64()}
	b.Catalogs = append(b.Catalogs, &irv1.Catalog{Name: id("crm"), Namespaces: []*irv1.Namespace{
		{Name: id(""), Tables: []*irv1.Table{{Name: id("tags"), Columns: []*irv1.Column{col}}, {Name: id("deals"), Columns: []*irv1.Column{col}}}},
	}})
	ns := b.GetCatalogs()[0].GetNamespaces()[0]
	for _, tb := range ns.GetTables() {
		for _, c := range tb.GetColumns() {
			if tb.GetName().GetStored() == "users" && c.GetName().GetStored() == "display_name" {
				c.Nullable, c.NullabilitySource = false, irv1.NullabilitySource_NULLABILITY_SOURCE_ANNOTATION
			}
		}
	}
	weak := func(name string, affinity *irv1.Type) *irv1.Column {
		return &irv1.Column{Name: id(name), Type: &irv1.Type{Kind: &irv1.Type_Weak{Weak: &irv1.Weak{Affinity: affinity}}}, Nullable: true}
	}
	ns.Tables = append(ns.Tables, &irv1.Table{Name: id("loose"), Columns: []*irv1.Column{
		weak("n", tDecimal()), weak("i", tInt64()), weak("s", tText()), weak("r", tFloat64()), weak("b", tBytes()),
	}})
	return b
}

func TestCatalogs(t *testing.T) {
	b := more(t)
	for _, c := range []struct{ body, want string }{
		{"SELECT id FROM tags", "query-ambiguous-table: q.sql:2:16: query Q: tags names a table in more than one catalog"},
		{"SELECT id FROM deals", "id:int64"},
		{"SELECT u.id FROM users u, deals d", "query-cross-catalog: q.sql:1: query Q: reads the catalogs crm and shop; a query runs on one catalog's source"},
		{"SELECT display_name, coalesce(display_name, 'x') AS a, CASE WHEN id > 1 THEN display_name ELSE email END AS b, display_name = 'x' AS c FROM users", "display_name:text! a:text! b:text! c:bool!"},
		{"SELECT tenant_id, max(display_name) AS m FROM users GROUP BY tenant_id", "tenant_id:uuid m:text!"},
		{"SELECT id FROM users FULL JOIN users AS u2 USING (display_name)", "query-ambiguous-column: q.sql:2:8: query Q: column id is a column of more than one relation; qualify it"},
		{"SELECT display_name FROM users FULL JOIN users AS u2 USING (display_name)", "display_name:text!"},
		{"SELECT coalesce(s, (SELECT email FROM users)) AS a FROM loose", "a:text?"},
		{"SELECT n FROM loose WHERE n = @n AND i = @i AND s = @s AND r = @r AND b = @b", "@n:decimal @i:int64 @s:text @r:float64 @b:bytes n:weak?"},
		{"SELECT n + 1 AS a, i + 1 AS b, -n AS c, coalesce(n, i) AS d, coalesce(s, 'x') AS e, sum(n) AS f, abs(n) AS g, r + n AS h FROM loose", "a:decimal? b:int64? c:decimal? d:decimal? e:weak f:float64? g:decimal? h:float64?"},
	} {
		res, err := New().Invoke(context.Background(), &irv1.Invoke{Bundle: proto.Clone(b).(*irv1.Bundle),
			QueryFiles: []*irv1.QueryFile{{Path: "q.sql", Text: "-- name: Q :many\n" + c.body}}})
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		if ds := res.GetDiagnostics(); len(ds) > 0 {
			got = ds[0].GetCode() + ": " + ds[0].GetMessage()
		} else {
			got = describe(res.GetBundle().GetQueries().GetQueries()[0])
		}
		if got != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.body, got, c.want)
		}
	}
}

// The types SQLite computes arithmetic in.
func TestArithmetic(t *testing.T) {
	for _, c := range []struct {
		l, r *irv1.Type
		want string
	}{
		{tInt64(), tBool(), "int64"},
		{tInt64(), tDecimal(), "decimal"},
		{tDecimal(), tFloat64(), "float64"},
		{tInt64(), tText(), "<nil>"},
	} {
		got := "<nil>"
		if r := arithmeticType(c.l, c.r); r != nil {
			got = kind(r)
		}
		if got != c.want {
			t.Errorf("%v + %v = %s, want %s", c.l, c.r, got, c.want)
		}
	}
}

// The shipped plugin also runs as the executable dql-plugin-sqlite-queries,
// and types the same queries there (ADR 0002).
func TestExecutable(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "dql-plugin-sqlite-queries")
	if msg, err := exec.Command("go", "build", "-o", bin, "./cmd/dql-plugin-sqlite-queries").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, msg)
	}
	ctx := context.Background()
	c, err := plugin.Start(ctx, bin, dir, nil, &irv1.CoreHello{IrVersion: &irv1.IrVersion{Major: proto.Uint32(1), Minor: proto.Uint32(0), Patch: proto.Uint32(0)}}, irv1.PluginKind_PLUGIN_KIND_QUERY_SOURCE)
	if err != nil {
		t.Fatal(err)
	}
	in := &irv1.Invoke{Bundle: shop(t), QueryFiles: []*irv1.QueryFile{{Path: "q.sql", Text: "-- name: Q :many\nSELECT id FROM users WHERE email = @email"}}}
	res, err := c.Invoke(ctx, in)
	if cerr := c.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
	want, err := New().Invoke(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(res, want) {
		t.Errorf("the executable returned %v, want %v", res, want)
	}
}
