package harness

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// root is the repository root, which schemas' DDL paths are relative to.
var root = filepath.Join("..", "..", "..")

// open creates a database of the schema and opens its handle.
func open(t *testing.T, name string) (any, Schema) {
	t.Helper()
	ctx := context.Background()
	base := os.Getenv("DQL_TEST_POSTGRES_URL")
	if base == "" {
		t.Fatal("DQL_TEST_POSTGRES_URL is not set")
	}
	s := Schemas[name]
	dsn, drop, err := Database(ctx, base, filepath.Join(root, s.DDL))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(drop)
	db, closeDB, err := s.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeDB)
	return db, s
}

// Every scenario passes on Postgres for Go (guide M4).
func TestScenarios(t *testing.T) {
	scs, err := Load(filepath.Join("..", "..", "scenarios"))
	if err != nil {
		t.Fatal(err)
	}
	if len(scs) == 0 {
		t.Fatal("no scenarios")
	}
	for _, sc := range scs {
		t.Run(sc.Name, func(t *testing.T) {
			if _, ok := Schemas[sc.Schema]; !ok {
				t.Fatalf("%s: no schema %s", sc.File, sc.Schema)
			}
			db, s := open(t, sc.Schema)
			for _, p := range Run(context.Background(), sc, db, s) {
				t.Error(p)
			}
		})
	}
}
