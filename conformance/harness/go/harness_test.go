package harness

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/thec1oud/dql/runtime/go/dql"
)

// root is the repository root, which schemas' DDL paths are relative to.
var root = filepath.Join("..", "..", "..")

// open creates a database of the schema on the server its URLEnv names and
// opens its handle, with the database's copy on the server of
// DQL_TEST_POSTGRES_REPLICA_URL, a streaming replica of
// DQL_TEST_POSTGRES_URL's, as its replica when replica is set, and at most
// maxConns connections to the primary, 0 meaning the schema's default,
// sending statements in mode. Without that server, a test that needs it is
// skipped.
func open(t *testing.T, s Schema, replica bool, maxConns int, mode dql.StatementMode) Target {
	t.Helper()
	ctx := context.Background()
	base := os.Getenv(s.URLEnv)
	if base == "" {
		t.Fatal(s.URLEnv + " is not set")
	}
	replicaBase := os.Getenv("DQL_TEST_POSTGRES_REPLICA_URL")
	if replica && replicaBase == "" {
		t.Skip("DQL_TEST_POSTGRES_REPLICA_URL is not set")
	}
	var ddl []string
	for _, f := range s.DDL {
		ddl = append(ddl, filepath.Join(root, f))
	}
	dsn, drop, err := s.Database(ctx, base, ddl...)
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

// Every scenario passes on every dialect for Go (guide M4, M6), in each
// statement mode its dialect offers (§26.4), except that a scenario
// requiring a feature a dialect's schema lacks is skipped there, and named
// with the missing features. With DQL_CONFORMANCE_OUTCOMES set, each
// scenario's outcomes are written there, as <mode>/<scenario>.json for
// Postgres and <dialect>/<mode>/<scenario>.json for the others, for CI to
// compare with the TypeScript harness's (guide M5).
func TestScenarios(t *testing.T) {
	out := os.Getenv("DQL_CONFORMANCE_OUTCOMES")
	scs, err := Load(filepath.Join("..", "..", "scenarios"))
	if err != nil {
		t.Fatal(err)
	}
	if len(scs) == 0 {
		t.Fatal("no scenarios")
	}
	for _, dialect := range []string{"postgres", "mysql", "mariadb"} {
		for _, mode := range []dql.StatementMode{dql.ModePrepare, dql.ModeDescribe, dql.ModeSimple} {
			for _, sc := range scs {
				name := mode.String() + "/" + sc.Name
				dir := filepath.Join(out, mode.String())
				if dialect != "postgres" {
					name, dir = dialect+"/"+name, filepath.Join(out, dialect, mode.String())
				}
				t.Run(name, func(t *testing.T) {
					s, ok := Matrix[dialect][sc.Schema]
					if !ok && dialect == "postgres" {
						t.Fatalf("%s: no schema %s", sc.File, sc.Schema)
					}
					if !ok {
						t.Skipf("%s has no schema %s", dialect, sc.Schema)
					}
					if !slices.Contains(s.Modes, mode) {
						t.Skipf("%s has no %s mode", dialect, mode)
					}
					if missing := Missing(sc, s); len(missing) > 0 {
						t.Skipf("%s lacks %s", dialect, strings.Join(missing, ", "))
					}
					target := open(t, s, sc.Replica, sc.MaxConns, mode)
					outcomes := []Outcome{}
					target.Record = func(o Outcome) { outcomes = append(outcomes, o) }
					for _, p := range Run(context.Background(), sc, target) {
						t.Error(p)
					}
					if out != "" {
						writeOutcomes(t, filepath.Join(dir, sc.Name+".json"), outcomes)
					}
				})
			}
		}
	}
}

// writeOutcomes writes a scenario's outcomes to file as JSON.
func writeOutcomes(t *testing.T, file string, outcomes []Outcome) {
	t.Helper()
	raw, err := json.MarshalIndent(outcomes, "", " ")
	if err == nil {
		err = os.MkdirAll(filepath.Dir(file), 0o755)
	}
	if err == nil {
		err = os.WriteFile(file, append(raw, '\n'), 0o644)
	}
	if err != nil {
		t.Fatal(err)
	}
}
