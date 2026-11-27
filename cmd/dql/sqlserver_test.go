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
// file's queries. Its catalog layer is testdata/sqlservershop/catalog.json,
// which the SQL Server query source's tests type against.
func TestGenerateSQLServer(t *testing.T) {
	ctx := context.Background()
	db, err := sqlservertest.Create(ctx, filepath.Join("..", "..", "conformance", "schemas", "sqlserver", "shop.sql"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Drop(ctx)
	t.Setenv("SHOP_SQLSERVER_URL", db.URL)
	dir := t.TempDir()
	config := filepath.Join("..", "..", "conformance", "schemas", "sqlserver", "shop.yaml")
	out := filepath.Join(dir, "bundle.json")
	if code, _, stderr := dql("generate", "-config", config, "-bundle", out); code != 0 {
		t.Fatalf("%d %q", code, stderr)
	}
	b := &irv1.Bundle{}
	raw, _ := os.ReadFile(out)
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
}
