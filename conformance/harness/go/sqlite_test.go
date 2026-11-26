package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// SQLite has no server to inject failures, end connections or replay, and
// a database file that cannot be made fails to open.
func TestSQLiteSchema(t *testing.T) {
	ctx := context.Background()
	if err := injectSQLite(ctx, "", Inject{}); !errors.Is(err, errNoServer) {
		t.Error(err)
	}
	if err := terminateSQLite(ctx, ""); !errors.Is(err, errNoServer) {
		t.Error(err)
	}
	if err := replaySQLite(ctx, "", true); !errors.Is(err, errNoServer) {
		t.Error(err)
	}
	bad := filepath.Join(t.TempDir(), "bad.sql")
	os.WriteFile(bad, []byte("NOT SQL"), 0o644)
	for _, ddl := range []string{"missing.sql", bad} {
		if _, _, err := databaseSQLite(ctx, "", ddl); err == nil {
			t.Errorf("%s: created", ddl)
		}
	}
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	if _, _, err := databaseSQLite(ctx, ""); err == nil {
		t.Error("created a database in a missing directory")
	}
}
