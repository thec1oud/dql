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
