package harness

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thec1oud/dql/runtime/go/dql"
)

// root is the repository root, which schemas' DDL paths are relative to.
var root = filepath.Join("..", "..", "..")

// open creates a database of the schema and opens its handle, with the
// database's copy on the server of DQL_TEST_POSTGRES_REPLICA_URL, a
// streaming replica of DQL_TEST_POSTGRES_URL's, as its replica when replica
// is set. Without that server, a test that needs it is skipped.
func open(t *testing.T, name string, replica bool) Target {
	t.Helper()
	ctx := context.Background()
	base := os.Getenv("DQL_TEST_POSTGRES_URL")
	if base == "" {
		t.Fatal("DQL_TEST_POSTGRES_URL is not set")
	}
	replicaBase := os.Getenv("DQL_TEST_POSTGRES_REPLICA_URL")
	if replica && replicaBase == "" {
		t.Skip("DQL_TEST_POSTGRES_REPLICA_URL is not set")
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
	var replicas []string
	if replica {
		replicas = append(replicas, replicaOf(t, dsn, replicaBase))
	}
	rec := &Recorder{}
	db, closeDB, err := s.Open(ctx, dsn, replicas, dql.WithHooks(rec.Hooks()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeDB)
	target := Target{Schema: s, DB: db, DSN: dsn, Events: rec}
	if replica {
		target.ReplicaDSN = replicas[0]
	}
	return target
}

// replicaOf returns the URL of the database at dsn on the replica server at
// base, once the replica has replayed its creation.
func replicaOf(t *testing.T, dsn, base string) string {
	t.Helper()
	d, _ := url.Parse(dsn)
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = d.Path
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := run(context.Background(), u.String(), "SELECT 1")
		if err == nil {
			return u.String()
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
	}
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
			for _, p := range Run(context.Background(), sc, open(t, sc.Schema, sc.Replica)) {
				t.Error(p)
			}
		})
	}
}
