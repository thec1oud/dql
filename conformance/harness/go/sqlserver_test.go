package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// SQL Server injects no serialization failure and has no replica, and a
// database whose DDL fails is dropped.
func TestSQLServerSchema(t *testing.T) {
	ctx := context.Background()
	base := os.Getenv("DQL_TEST_SQLSERVER_URL")
	if base == "" {
		t.Fatal("DQL_TEST_SQLSERVER_URL is not set")
	}
	if err := injectSQLServer(ctx, "", Inject{Class: "SerializationFailure", Times: 1}); err == nil || err.Error() != "no injected failure of class SerializationFailure on SQL Server" {
		t.Error(err)
	}
	if err := injectSQLServer(ctx, "odbc:server=%zz;=", Inject{}); err == nil {
		t.Error("a plan is set on no server")
	}
	// A plan of no times, or of a stall, needs no session of the harness's.
	if injectHelper("deadlock", 0) != "" || injectHelper("lock_timeout", 0) != "" || injectHelper("stall", 1) != "" {
		t.Error("a helper session for no deadlock or lock timeout")
	}
	if err := injectSQLServer(ctx, "", Inject{Delay: "soon", Times: 1}); err == nil {
		t.Error("a bad delay is injected")
	}
	if err := replaySQLServer(ctx, "", true); !errors.Is(err, errNoReplica) {
		t.Error(err)
	}
	bad := filepath.Join(t.TempDir(), "bad.sql")
	os.WriteFile(bad, []byte("NOT SQL"), 0o644)
	for _, c := range []struct{ base, ddl string }{
		{base, "missing.sql"},
		{base, bad},
		{"%zz", bad},
		{"sqlserver://nobody:wrong@127.0.0.1:1", bad},
	} {
		if _, _, err := databaseSQLServer(ctx, c.base, c.ddl); err == nil {
			t.Errorf("%s on %s: created", c.ddl, c.base)
		}
	}
	for _, f := range []func() error{
		func() error { return runSQLServer(ctx, "odbc:server=%zz;=", "SELECT 1") },
		func() error { return terminateSQLServer(ctx, "sqlserver://nobody:wrong@127.0.0.1:1") },
		func() error { return terminateSQLServer(ctx, "odbc:server=%zz;=") },
	} {
		if err := f(); err == nil {
			t.Error("no error")
		}
	}
}
