package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thec1oud/dql/sources/sqlitetest"
	"github.com/thec1oud/dql/sources/sqlservertest"
	"github.com/thec1oud/dql/testkit/golden"
)

// generateVariants runs dql generate on the dialect's variants config in
// schemas, with the source's connection in env set to connection, and
// compares the go and ts packages it writes into name with the conformance
// harnesses' goldens. It returns generate's standard error.
func generateVariants(t *testing.T, schemas, config, env, connection, name string) string {
	t.Helper()
	t.Setenv(env, connection)
	raw, err := os.ReadFile(filepath.Join(schemas, config))
	if err != nil {
		t.Fatal(err)
	}
	cfg := write(t, "dql.yaml", string(raw))
	code, _, stderr := dql("generate", "-config", cfg)
	if code != 0 {
		t.Fatalf("%d %q", code, stderr)
	}
	for _, pkg := range []struct{ out, glob, golden string }{
		{name, "*.dql.go", filepath.Join("..", "..", "conformance", "harness", "go", "testdata", name)},
		{filepath.Join("ts", name), "*.dql.ts", filepath.Join("..", "..", "conformance", "harness", "ts", name)},
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
			golden.Assert(t, filepath.Join(pkg.golden, filepath.Base(f)), got, inputDigest, databaseName)
		}
	}
	return stderr
}

// dql generate writes the conformance harnesses' sqlitevariantdb packages
// from conformance/schemas/sqlite/variants.yaml over shop.sql and
// variants.sql: soft delete on posts.deleted_at, an instant stored as text
// (§21.3, §7.2), and the custom shapes of §14.8.
func TestGenerateConformanceVariantsSQLite(t *testing.T) {
	schemas := filepath.Join("..", "..", "conformance", "schemas", "sqlite")
	var script []byte
	for _, f := range []string{"shop.sql", "variants.sql"} {
		raw, err := os.ReadFile(filepath.Join(schemas, f))
		if err != nil {
			t.Fatal(err)
		}
		script = append(script, raw...)
	}
	ddl := filepath.Join(t.TempDir(), "variants.sql")
	if err := os.WriteFile(ddl, script, 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitetest.Create(context.Background(), ddl)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Drop()
	if stderr := generateVariants(t, schemas, "variants.yaml", "SHOP_SQLITE_PATH", db.Path, "sqlitevariantdb"); stderr != wantVariantsSQLite {
		t.Errorf("stderr %q", stderr)
	}
}

// dql generate writes the conformance harnesses' sqlservervariantdb packages
// from conformance/schemas/sqlserver/variants.yaml over shop.sql and
// variants.sql.
func TestGenerateConformanceVariantsSQLServer(t *testing.T) {
	ctx := context.Background()
	schemas := filepath.Join("..", "..", "conformance", "schemas", "sqlserver")
	db, err := sqlservertest.Create(ctx, filepath.Join(schemas, "shop.sql"), filepath.Join(schemas, "variants.sql"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Drop(ctx)
	if stderr := generateVariants(t, schemas, "variants.yaml", "SHOP_SQLSERVER_URL", db.URL, "sqlservervariantdb"); stderr != wantVariantsSQLServer {
		t.Errorf("stderr %q", stderr)
	}
}

// wantVariantsSQLite and wantVariantsSQLServer are generate's warnings: on
// SQL Server, shop's filtered index on published slugs says nothing of
// deleted_at, the record's own soft-delete-unique example (§21.3).
const (
	wantVariantsSQLite    = ""
	wantVariantsSQLServer = "warning soft-delete-unique shop.app.posts.posts_one_published_slug: unique index on a soft-delete table does not exclude deleted rows; a deleted row's values cannot be reused\n"
)
