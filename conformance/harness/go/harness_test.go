package harness

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/thec1oud/dql/runtime/go/dql"
)

// root is the repository root, which schemas' DDL paths are relative to.
var root = filepath.Join("..", "..", "..")

// open creates a database of the schema and opens its handle, with the
// database's copy on the server of DQL_TEST_POSTGRES_REPLICA_URL, a
// streaming replica of DQL_TEST_POSTGRES_URL's, as its replica when replica
// is set, and at most maxConns connections to the primary, 0 meaning the
// schema's default, sending statements in mode. Without that server, a
// test that needs it is skipped.
func open(t *testing.T, name string, replica bool, maxConns int, mode dql.StatementMode) Target {
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
	db, exec, err := s.Open(ctx, dsn, replicas, maxConns, mode, dql.WithHooks(rec.Hooks()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(exec.Close)
	target := Target{Schema: s, DB: db, Exec: exec, DSN: dsn, Events: rec}
	if replica {
		target.ReplicaDSN = replicas[0]
	}
	return target
}

// replicaOf returns the URL of the database at dsn on the replica server at
// base, once the replica has replayed everything the primary had written
// when it was called: the database's creation and its DDL.
func replicaOf(t *testing.T, dsn, base string) string {
	t.Helper()
	ctx := context.Background()
	d, _ := url.Parse(dsn)
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = d.Path
	var lsn string
	if err := query(ctx, dsn, "SELECT pg_current_wal_lsn()::text", &lsn); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		var replayed bool
		err := query(ctx, u.String(), "SELECT pg_last_wal_replay_lsn() >= '"+lsn+"'::pg_lsn", &replayed)
		if err == nil && replayed {
			return u.String()
		}
		if time.Now().After(deadline) {
			t.Fatalf("the replica has not replayed %s: %v", lsn, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// query runs sql, which returns one value, on a connection of its own to
// dsn, and scans the value into dest.
func query(ctx context.Context, dsn, sql string, dest any) error {
	c, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer c.Close(ctx)
	return c.QueryRow(ctx, sql).Scan(dest)
}

// Every scenario passes on Postgres for Go (guide M4), in each statement
// mode (§26.4).
func TestScenarios(t *testing.T) {
	scs, err := Load(filepath.Join("..", "..", "scenarios"))
	if err != nil {
		t.Fatal(err)
	}
	if len(scs) == 0 {
		t.Fatal("no scenarios")
	}
	for _, mode := range []dql.StatementMode{dql.ModePrepare, dql.ModeDescribe, dql.ModeSimple} {
		for _, sc := range scs {
			t.Run(mode.String()+"/"+sc.Name, func(t *testing.T) {
				if _, ok := Schemas[sc.Schema]; !ok {
					t.Fatalf("%s: no schema %s", sc.File, sc.Schema)
				}
				for _, p := range Run(context.Background(), sc, open(t, sc.Schema, sc.Replica, sc.MaxConns, mode)) {
					t.Error(p)
				}
			})
		}
	}
}
