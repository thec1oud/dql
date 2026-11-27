package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/thec1oud/dql/core/bundle"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/sqlservertest"
	"github.com/thec1oud/dql/testkit/golden"
)

// conformance/schemas/sqlserver/shop.yaml runs shop.sql through
// sqlserver-live and the sqlserver dialect, and queries.sql through
// sqlserver-queries: the bundle holds SQL Server's profile and fragment
// table, with @p placeholders and a MERGE for each upsert, and the query
// file's queries, and the go and ts emitters write the conformance
// harnesses' sqlservershopdb packages. Its catalog layer is testdata/sqlservershop/catalog.json,
// which the SQL Server query source's tests type against.
func TestGenerateSQLServer(t *testing.T) {
	ctx := context.Background()
	db, err := sqlservertest.Create(ctx, filepath.Join("..", "..", "conformance", "schemas", "sqlserver", "shop.sql"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Drop(ctx)
	t.Setenv("SHOP_SQLSERVER_URL", db.URL)
	schemas := filepath.Join("..", "..", "conformance", "schemas", "sqlserver")
	raw, err := os.ReadFile(filepath.Join(schemas, "shop.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := write(t, "dql.yaml", string(raw))
	queries, err := os.ReadFile(filepath.Join(schemas, "queries.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(cfg), "queries.sql"), queries, 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "bundle.json")
	if code, _, stderr := dql("generate", "-config", cfg, "-bundle", out); code != 0 || stderr != "dql generate: wrote the bundle to "+out+"\n" {
		t.Fatalf("%d %q", code, stderr)
	}
	b := &irv1.Bundle{}
	raw, _ = os.ReadFile(out)
	if err := bundle.UnmarshalJSON(raw, b); err != nil {
		t.Fatal(err)
	}
	ft := b.GetQueries().GetFragments()
	if b.GetDialect() != irv1.Dialect_DIALECT_SQLSERVER || b.GetCapabilities().GetMaxParams() != 2098 || ft.GetPlaceholder() != irv1.Placeholder_PLACEHOLDER_AT_NUMBERED {
		t.Errorf("bundle %v %v", b.GetDialect(), b.GetCapabilities())
	}
	found := false
	for _, f := range ft.GetFragments() {
		found = found || f.GetId() == "upsert.users.users_tenant_email_key"
	}
	if !found {
		t.Error("no upsert on users_tenant_email_key")
	}
	if q := slices.IndexFunc(b.GetQueries().GetQueries(), func(q *irv1.Query) bool { return q.GetName().GetStored() == "UsersByEmail" }); q < 0 || !b.GetQueries().GetQueries()[q].GetParams()[0].GetList() {
		t.Error("no query UsersByEmail with a list")
	}
	b.Shapes = nil
	js, err := bundle.MarshalJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, filepath.Join("..", "..", "testdata", "sqlservershop", "catalog.json"), js, inputDigest, databaseName)
	if q := slices.IndexFunc(b.GetQueries().GetQueries(), func(q *irv1.Query) bool { return q.GetName().GetStored() == "user.upsert.by_tenant_id_and_email" }); q < 0 {
		t.Error("no query user.upsert.by_tenant_id_and_email")
	}
	for _, pkg := range []struct{ out, glob, golden string }{
		{"sqlservershopdb", "*.dql.go", goldenSqlservershopdb},
		{filepath.Join("ts", "sqlservershopdb"), "*.dql.ts", goldenSqlservershopdbTS},
	} {
		files, _ := filepath.Glob(filepath.Join(filepath.Dir(cfg), pkg.out, pkg.glob))
		want, _ := filepath.Glob(filepath.Join(pkg.golden, pkg.glob))
		if os.Getenv(golden.UpdateEnv) != "1" && len(want) != len(files) {
			t.Errorf("%s: %d files, golden has %d", pkg.out, len(files), len(want))
		}
		for _, f := range files {
			got, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			golden.Assert(t, filepath.Join(pkg.golden, filepath.Base(f)), got, inputDigest)
		}
	}
}

// goldenSqlservershopdb and goldenSqlservershopdbTS are the go and ts
// emitters' golden output for the running example on SQL Server, which the
// conformance harnesses run.
var (
	goldenSqlservershopdb   = filepath.Join("..", "..", "conformance", "harness", "go", "testdata", "sqlservershopdb")
	goldenSqlservershopdbTS = filepath.Join("..", "..", "conformance", "harness", "ts", "sqlservershopdb")
)
