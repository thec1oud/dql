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

// goldenMysqlshopdb and goldenMysqlshopdbTS are the go and ts emitters'
// golden output for the running example on MySQL, which the conformance
// harnesses run, goldenMariadbshopdb and goldenMariadbshopdbTS on MariaDB,
// and goldenMysqlvariantdb and goldenMysqlvariantdbTS for its variants on
// MySQL.
var (
	goldenMysqlshopdb      = filepath.Join("..", "..", "conformance", "harness", "go", "testdata", "mysqlshopdb")
	goldenMysqlshopdbTS    = filepath.Join("..", "..", "conformance", "harness", "ts", "mysqlshopdb")
	goldenMariadbshopdb    = filepath.Join("..", "..", "conformance", "harness", "go", "testdata", "mariadbshopdb")
	goldenMariadbshopdbTS  = filepath.Join("..", "..", "conformance", "harness", "ts", "mariadbshopdb")
	goldenMysqlvariantdb   = filepath.Join("..", "..", "conformance", "harness", "go", "testdata", "mysqlvariantdb")
	goldenMysqlvariantdbTS = filepath.Join("..", "..", "conformance", "harness", "ts", "mysqlvariantdb")
)

// inputDigest is the digest of a catalog's input, which holds the name of
// the database it was read from, fresh in each run.
var inputDigest = regexp.MustCompile(`sha256:[0-9a-f]{64}`)

// databaseName is the name of a fresh test database, which a view's
// definition holds.
var databaseName = regexp.MustCompile(`dql_test_[0-9_]+`)

// dql generate writes the conformance harnesses' mysqlshopdb packages from
// conformance/schemas/mysql/shop.yaml on MySQL, their mariadbshopdb
// packages from shop-mariadb.yaml on MariaDB, and their mysqlvariantdb
// packages from variants.yaml on MySQL with variants.sql. The variants'
// catalog layer has no golden of its own.
func TestGenerateConformanceMySQL(t *testing.T) {
	for _, c := range []struct {
		name, env, config, dsn, catalog string
		ddl                             []string
		packages                        []struct{ out, glob, golden string }
	}{
		{"mysql", mysqltest.URLEnv, "shop.yaml", "SHOP_MYSQL_DSN", "mysqlshop", []string{"shop.sql"}, []struct{ out, glob, golden string }{
			{"mysqlshopdb", "*.dql.go", goldenMysqlshopdb},
			{filepath.Join("ts", "mysqlshopdb"), "*.dql.ts", goldenMysqlshopdbTS},
		}},
		{"mariadb", mysqltest.MariaDBURLEnv, "shop-mariadb.yaml", "SHOP_MARIADB_DSN", "mariadbshop", []string{"shop.sql"}, []struct{ out, glob, golden string }{
			{"mariadbshopdb", "*.dql.go", goldenMariadbshopdb},
			{filepath.Join("ts", "mariadbshopdb"), "*.dql.ts", goldenMariadbshopdbTS},
		}},
		{"variants", mysqltest.URLEnv, "variants.yaml", "SHOP_MYSQL_DSN", "", []string{"shop.sql", "variants.sql"}, []struct{ out, glob, golden string }{
			{"mysqlvariantdb", "*.dql.go", goldenMysqlvariantdb},
			{filepath.Join("ts", "mysqlvariantdb"), "*.dql.ts", goldenMysqlvariantdbTS},
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			schemas := filepath.Join("..", "..", "conformance", "schemas", "mysql")
			var ddl []string
			for _, f := range c.ddl {
				ddl = append(ddl, filepath.Join(schemas, f))
			}
			db, err := mysqltest.CreateOn(ctx, c.env, ddl...)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Drop(ctx)
			t.Setenv(c.dsn, db.DSN)
			raw, err := os.ReadFile(filepath.Join(schemas, c.config))
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
			if c.catalog != "" {
				golden.Assert(t, filepath.Join("..", "..", "testdata", c.catalog, "catalog.json"), js, inputDigest, databaseName)
			}
			for _, pkg := range c.packages {
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
		})
	}
}
