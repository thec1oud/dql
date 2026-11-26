package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thec1oud/dql/core/bundle"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/mysqltest"
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
