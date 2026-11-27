package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thec1oud/dql/core/bundle"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/sqlitetest"
	"github.com/thec1oud/dql/testkit/golden"
)

// A SQLite database file runs through sqlite-live and the sqlite dialect,
// named by a path relative to dql.yaml, by an env: setting, or by a file:
// URI: the bundle holds SQLite's profile and fragment table, and the go
// emitter writes a package for it.
func TestGenerateSQLite(t *testing.T) {
	db, err := sqlitetest.Create(context.Background(), filepath.Join("..", "..", "conformance", "schemas", "sqlite", "shop.sql"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Drop()
	dir := t.TempDir()
	config := filepath.Join(dir, "dql.yaml")
	raw, _ := os.ReadFile(db.Path)
	if err := os.WriteFile(filepath.Join(dir, "shop.db"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHOP_SQLITE", db.Path)
	emit := "emit:\n  - plugin: go\n    dialect: sqlite\n    package: shopdb\n    out: gen\n"
	for _, conn := range []string{"shop.db", "env:SHOP_SQLITE", "file:" + db.Path} {
		os.WriteFile(config, []byte("sources:\n  - plugin: sqlite-live\n    catalog: shop\n    connection: "+conn+"\n"+emit), 0o644)
		out := filepath.Join(dir, "bundle.json")
		if code, _, stderr := dql("generate", "-config", config, "-bundle", out); code != 0 {
			t.Fatalf("%s: %d %q", conn, code, stderr)
		}
		b := &irv1.Bundle{}
		raw, _ := os.ReadFile(out)
		if err := bundle.UnmarshalJSON(raw, b); err != nil {
			t.Fatal(err)
		}
		if b.GetDialect() != irv1.Dialect_DIALECT_SQLITE || b.GetCapabilities().GetReturnedRows() != irv1.ReturnedRows_RETURNED_ROWS_CLAUSE ||
			b.GetQueries().GetFragments().GetPlaceholder() != irv1.Placeholder_PLACEHOLDER_QUESTION {
			t.Errorf("%s: bundle %v %v", conn, b.GetDialect(), b.GetCapabilities())
		}
		if _, err := os.Stat(filepath.Join(dir, "gen", "dql-fragments.dql.go")); err != nil {
			t.Error(err)
		}
	}
	os.WriteFile(config, []byte("sources:\n  - plugin: sqlite-live\n    catalog: shop\n    connection: none.db\n"), 0o644)
	if code, _, stderr := dql("generate", "-config", config); code != 1 || !strings.Contains(stderr, "sqlite-live: opening catalog shop") {
		t.Errorf("missing file: %d %q", code, stderr)
	}
}

// goldenSqliteshopdb and goldenSqliteshopdbTS are the go and ts emitters'
// golden output for the running example on SQLite, which the conformance
// harnesses run.
var (
	goldenSqliteshopdb   = filepath.Join("..", "..", "conformance", "harness", "go", "testdata", "sqliteshopdb")
	goldenSqliteshopdbTS = filepath.Join("..", "..", "conformance", "harness", "ts", "sqliteshopdb")
)

// dql generate types conformance/schemas/sqlite/queries.sql against
// shop.sql through shop.yaml, and writes the conformance harnesses'
// sqliteshopdb packages. The catalog layer is
// testdata/sqliteshop/catalog.json, which the SQLite query source's tests
// type against.
func TestGenerateConformanceSQLite(t *testing.T) {
	schemas := filepath.Join("..", "..", "conformance", "schemas", "sqlite")
	db, err := sqlitetest.Create(context.Background(), filepath.Join(schemas, "shop.sql"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Drop()
	t.Setenv("SHOP_SQLITE_PATH", db.Path)
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
	var names []string
	for _, q := range b.GetQueries().GetQueries() {
		if q.GetSource() == irv1.QuerySource_QUERY_SOURCE_FILE {
			names = append(names, q.GetName().GetStored())
		}
	}
	if got := strings.Join(names, " "); got != "AuthorPostCounts GetUserByEmail RenameUser SearchUsers UsersByEmail" {
		t.Errorf("file queries = %s", got)
	}
	b.Shapes = nil
	js, err := bundle.MarshalJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, filepath.Join("..", "..", "testdata", "sqliteshop", "catalog.json"), js, inputDigest)
	for _, pkg := range []struct{ out, glob, golden string }{
		{"sqliteshopdb", "*.dql.go", goldenSqliteshopdb},
		{filepath.Join("ts", "sqliteshopdb"), "*.dql.ts", goldenSqliteshopdbTS},
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
