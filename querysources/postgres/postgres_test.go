package postgres

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/thec1oud/dql/core/bundle"
	"github.com/thec1oud/dql/plugin"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/testkit/golden"
)

func TestHandshake(t *testing.T) {
	hs := New().Handshake()
	if hs.GetPlugin().GetName() != "pg-queries" || hs.GetPlugin().GetKind() != irv1.PluginKind_PLUGIN_KIND_QUERY_SOURCE ||
		hs.GetIrVersions().GetMin() != "1.0.0" || hs.GetIrVersions().GetMaxMajor() != 1 {
		t.Errorf("handshake = %v", hs)
	}
}

func TestInvokeRefusesOtherDialects(t *testing.T) {
	b := shop(t)
	b.Dialect = irv1.Dialect_DIALECT_MYSQL
	_, err := New().Invoke(context.Background(), &irv1.Invoke{Bundle: b})
	if err == nil || err.Error() != "pg-queries: the bundle's catalogs are DIALECT_MYSQL, not Postgres" {
		t.Errorf("err = %v", err)
	}
}

// TestShopGolden pins the queries of the running example (§17.3).
func TestShopGolden(t *testing.T) {
	text, err := os.ReadFile("../../testdata/shop/queries.sql")
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

func TestQueryFields(t *testing.T) {
	res := run(t, "-- name: Q :one\n-- nonnull: display_name, id\n-- unscoped\nSELECT id, display_name FROM app.users WHERE id = @id")
	q := res.GetBundle().GetQueries().GetQueries()[0]
	if got := describe(q); got != "@id:int64 id:int64 display_name:varchar!" {
		t.Errorf("describe = %s", got)
	}
	if !q.GetScope().GetUnscoped() || q.GetCatalog() != "shop" || q.GetText() != "SELECT id, display_name FROM app.users WHERE id = $1" {
		t.Errorf("query = %v", q)
	}
	res = run(t, "-- name: Q :one\n-- nonnull: nope\nSELECT id FROM app.users")
	if d := res.GetDiagnostics()[0]; d.GetCode() != "query-nonnull-unknown" || d.GetMessage() != "q.sql:1: query Q: -- nonnull: names nope, which is not a result column" {
		t.Errorf("diagnostic = %v", d)
	}
	res = run(t, "-- name: Q :exec\nSELECT 1")
	if q := res.GetBundle().GetQueries().GetQueries()[0]; q.GetResult() != nil || q.GetScope() != nil {
		t.Errorf("query = %v", q)
	}
}

func TestSyntaxErrors(t *testing.T) {
	res := run(t, "-- name: Q :one\nSELECT id\nFROM app.users\nWHERE id = @id AND AND")
	d := res.GetDiagnostics()[0]
	if d.GetCode() != "query-syntax" || d.GetMessage() != `q.sql:4:20: query Q: syntax error at or near "AND"` {
		t.Errorf("diagnostic = %v", d)
	}
	if got := syntaxOffset(errors.New("not the parser's")); got != -1 {
		t.Errorf("syntaxOffset = %d", got)
	}
	if got := (&queryError{msg: "m"}).Error(); got != "m" {
		t.Errorf("Error = %q", got)
	}
	res = run(t, "-- name: Q :one\nSELECT @id::int AS x WHERE")
	if d := res.GetDiagnostics()[0]; d.GetMessage() != "q.sql:2:27: query Q: syntax error at end of input" {
		t.Errorf("diagnostic = %v", d)
	}
}

// more returns the shop bundle with a second catalog, crm, which has an
// app.users table of its own and a non-default namespace sales, and with
// routines that return sets and that are volatile.
func more(t *testing.T) *irv1.Bundle {
	b := shop(t)
	id := func(s string) *irv1.Identifier { return &irv1.Identifier{Stored: s} }
	col := &irv1.Column{Name: id("id"), Type: tInt64()}
	b.Catalogs = append(b.Catalogs, &irv1.Catalog{Name: id("crm"), DefaultNamespace: "app", Namespaces: []*irv1.Namespace{
		{Name: id("app"), Tables: []*irv1.Table{{Name: id("users"), Columns: []*irv1.Column{col}}}},
		{Name: id("sales"), Tables: []*irv1.Table{{Name: id("deals"), Columns: []*irv1.Column{col}}},
			Enums: []*irv1.Enum{{Name: id("stage")}}},
	}})
	ns := b.GetCatalogs()[0].GetNamespaces()[0]
	scalar := func(t *irv1.Type) *irv1.Returns {
		return &irv1.Returns{Kind: &irv1.Returns_Scalar{Scalar: &irv1.ReturnValue{Type: t}}}
	}
	ns.Routines = append(ns.Routines,
		&irv1.Routine{Name: id("series"), Overloads: []*irv1.Overload{{Kind: irv1.RoutineKind_ROUTINE_KIND_FUNCTION,
			Returns: &irv1.Returns{Kind: &irv1.Returns_SetOf{SetOf: &irv1.ReturnValue{Type: tInt64()}}}}}},
		&irv1.Routine{Name: id("bump"), Overloads: []*irv1.Overload{{Kind: irv1.RoutineKind_ROUTINE_KIND_FUNCTION,
			Arguments: []*irv1.Argument{
				{Name: id("a"), Type: tInt64(), Mode: irv1.ArgMode_ARG_MODE_IN},
				{Name: id("b"), Type: tInt32(), Mode: irv1.ArgMode_ARG_MODE_IN, HasDefault: true},
				{Name: id("r"), Type: tInt64(), Mode: irv1.ArgMode_ARG_MODE_OUT},
			},
			Volatility: irv1.Volatility_VOLATILITY_VOLATILE, Returns: scalar(tInt64())}}},
		&irv1.Routine{Name: id("proc"), Overloads: []*irv1.Overload{{Kind: irv1.RoutineKind_ROUTINE_KIND_PROCEDURE}}},
	)
	return b
}

func TestCatalogs(t *testing.T) {
	b := more(t)
	for _, c := range []struct{ body, want string }{
		{"SELECT id FROM users", "query-ambiguous-table: q.sql:2:16: query Q: users names a table in more than one namespace or catalog; qualify it"},
		{"SELECT id FROM crm.app.users", "id:int64"},
		{"SELECT id FROM deals", "id:int64"},
		{"SELECT d.id FROM crm.sales.deals d", "id:int64"},
		{"SELECT u.id FROM shop.app.users u, crm.sales.deals d", "query-cross-catalog: q.sql:1: query Q: reads the catalogs crm and shop; a query runs on one catalog's source"},
		{"SELECT 'a'::sales.stage AS s", "s:enum:stage"},
		{"SELECT 'a'::stage AS s", "query-unknown-type: q.sql:2:13: query Q: type stage is not in the catalog; cast to a built-in type or to an enum, domain or composite of the catalog"},
		{"SELECT series() AS s", "query-unsupported: q.sql:2:8: query Q: function series does not return one value; set-returning functions are not supported in query files"},
		{"SELECT bump(1) AS a, app.bump(@a, 2) AS b", "@a:int64 a:int64! b:int64! w"},
		{"SELECT proc() AS a", "query-unknown-function: q.sql:2:8: query Q: 0 overloads of proc take these 0 arguments; cast the arguments to pick one"},
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

// The shipped plugin also runs as the executable dql-plugin-pg-queries, and
// types the same queries there (ADR 0002).
func TestExecutable(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "dql-plugin-pg-queries")
	if msg, err := exec.Command("go", "build", "-o", bin, "./cmd/dql-plugin-pg-queries").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, msg)
	}
	ctx := context.Background()
	c, err := plugin.Start(ctx, bin, dir, nil, &irv1.CoreHello{IrVersion: &irv1.IrVersion{Major: proto.Uint32(1), Minor: proto.Uint32(0), Patch: proto.Uint32(0)}}, irv1.PluginKind_PLUGIN_KIND_QUERY_SOURCE)
	if err != nil {
		t.Fatal(err)
	}
	in := &irv1.Invoke{Bundle: shop(t), QueryFiles: []*irv1.QueryFile{{Path: "q.sql", Text: "-- name: Q :many\nSELECT id FROM app.users WHERE email = @email"}}}
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
