package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thec1oud/dql/sources/sqlitetest"
)

// A SQLite database file runs through sqlite-live, named by a path relative
// to dql.yaml, by an env: setting, or by a file: URI. No dialect plugin
// serves SQLite yet, so generation stops after the sources have run.
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
	for conn, want := range map[string]string{
		"shop.db":         "dql generate: no dialect plugin serves DIALECT_SQLITE\n",
		"env:SHOP_SQLITE": "dql generate: no dialect plugin serves DIALECT_SQLITE\n",
		"file:" + db.Path: "dql generate: no dialect plugin serves DIALECT_SQLITE\n",
		"none.db":         "sqlite-live: opening catalog shop",
	} {
		os.WriteFile(config, []byte("sources:\n  - plugin: sqlite-live\n    catalog: shop\n    connection: "+conn+"\n"), 0o644)
		code, _, stderr := dql("generate", "-config", config)
		if code != 1 || !strings.HasPrefix(stderr, want) && !strings.Contains(stderr, ": "+want) {
			t.Errorf("%s: %d %q; want %q", conn, code, stderr, want)
		}
	}
}
