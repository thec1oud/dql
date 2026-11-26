package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/thec1oud/dql/core/bundle"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/mysqltest"
	"github.com/thec1oud/dql/testkit/golden"
)

// A MySQL database runs through mysql-live and the mysql dialect: the
// bundle holds MySQL's profile and fragment table, and the go emitter
// writes a package for it.
func TestGenerateMySQL(t *testing.T) {
	ctx := context.Background()
	db, err := mysqltest.Create(ctx, filepath.Join("..", "..", "conformance", "schemas", "mysql", "shop.sql"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Drop(ctx)
	t.Setenv("SHOP_MYSQL_DSN", db.DSN)
	config := write(t, "dql.yaml", "sources:\n  - plugin: mysql-live\n    catalog: shop\n    connection: env:SHOP_MYSQL_DSN\n"+
		"emit:\n  - plugin: go\n    dialect: mysql\n    package: shopdb\n    out: gen\n")
	out := filepath.Join(t.TempDir(), "bundle.json")
	if code, _, stderr := dql("generate", "-config", config, "-bundle", out); code != 0 {
		t.Fatalf("%d %q", code, stderr)
	}
	b := &irv1.Bundle{}
	raw, _ := os.ReadFile(out)
	if err := bundle.UnmarshalJSON(raw, b); err != nil {
		t.Fatal(err)
	}
	if b.GetDialect() != irv1.Dialect_DIALECT_MYSQL || b.GetCapabilities().GetReturnedRows() != irv1.ReturnedRows_RETURNED_ROWS_REREAD ||
		b.GetQueries().GetFragments().GetPlaceholder() != irv1.Placeholder_PLACEHOLDER_QUESTION {
		t.Errorf("bundle: %v %v", b.GetDialect(), b.GetCapabilities())
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(config), "gen", "dql-fragments.dql.go")); err != nil {
		t.Error(err)
	}
}

// goldenMysqlshopdb is the go emitter's golden output for the running
// example on MySQL, which the conformance harness runs.
var goldenMysqlshopdb = filepath.Join("..", "..", "conformance", "harness", "go", "testdata", "mysqlshopdb")

// inputDigest is the digest of a catalog's input, which holds the name of
// the database it was read from, fresh in each run.
var inputDigest = regexp.MustCompile(`sha256:[0-9a-f]{64}`)

// databaseName is the name of a fresh test database, which a view's
// definition holds.
var databaseName = regexp.MustCompile(`dql_test_[0-9_]+`)

// dql generate writes the conformance harness's mysqlshopdb package from
// conformance/schemas/mysql/shop.yaml.
func TestGenerateConformanceMySQL(t *testing.T) {
	ctx := context.Background()
	schemas := filepath.Join("..", "..", "conformance", "schemas", "mysql")
	db, err := mysqltest.Create(ctx, filepath.Join(schemas, "shop.sql"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Drop(ctx)
	t.Setenv("SHOP_MYSQL_DSN", db.DSN)
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
	// The catalog layer, which the MySQL query source's tests type against.
	b := &irv1.Bundle{}
	raw, _ = os.ReadFile(out)
	if err := bundle.UnmarshalJSON(raw, b); err != nil {
		t.Fatal(err)
	}
	b.Shapes = nil
	js, err := bundle.MarshalJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, filepath.Join("..", "..", "testdata", "mysqlshop", "catalog.json"), js, inputDigest, databaseName)
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(cfg), "mysqlshopdb", "*.dql.go"))
	want, _ := filepath.Glob(filepath.Join(goldenMysqlshopdb, "*.dql.go"))
	if os.Getenv(golden.UpdateEnv) != "1" && len(want) != len(files) {
		t.Errorf("%d files, golden has %d", len(files), len(want))
	}
	for _, f := range files {
		got, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		golden.Assert(t, filepath.Join(goldenMysqlshopdb, filepath.Base(f)), got, inputDigest)
	}
}
