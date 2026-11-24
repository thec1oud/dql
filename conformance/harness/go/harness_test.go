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
func open(t *testing.T, name string) Target {
	t.Helper()
	ctx := context.Background()
	base := os.Getenv("DQL_TEST_POSTGRES_URL")
	if base == "" {
		t.Fatal("DQL_TEST_POSTGRES_URL is not set")
	}
	s := Schemas[name]
	var ddl []string
	for _, f := range s.DDL {
		ddl = append(ddl, filepath.Join(root, f))
	}
	dsn, drop, err := Database(ctx, base, ddl...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(drop)
	db, closeDB, err := s.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeDB)
	return Target{Schema: s, DB: db, DSN: dsn}
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
			for _, p := range Run(context.Background(), sc, open(t, sc.Schema)) {
				t.Error(p)
			}
		})
	}
}
