package sqlservertest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreate(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	good := filepath.Join(dir, "good.sql")
	os.WriteFile(good, []byte("CREATE TABLE t (id INT)\nGO\n\ngo  \nINSERT INTO t VALUES (1)\nGO\n"), 0o644)
	d, err := Create(ctx, good)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := d.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM t").Scan(&n); err != nil || n != 1 {
		t.Errorf("count %d, %v", n, err)
	}
	if !strings.Contains(d.URL, "database="+d.Name) {
		t.Errorf("URL %s", d.URL)
	}
	d.Drop(ctx)

	bad := filepath.Join(dir, "bad.sql")
	os.WriteFile(bad, []byte("SELECT 1\nGO\nNOT SQL"), 0o644)
	if _, err := Create(ctx, bad); err == nil || !strings.Contains(err.Error(), "batch 2 of") {
		t.Errorf("bad: %v", err)
	}
	if _, err := Create(ctx, filepath.Join(dir, "missing.sql")); err == nil {
		t.Error("missing file")
	}
}

func TestNoServer(t *testing.T) {
	ctx := context.Background()
	t.Setenv(URLEnv, "")
	if _, err := Create(ctx); !errors.As(err, new(ErrNoServer)) || !strings.Contains(err.Error(), URLEnv+"=sqlserver://") {
		t.Errorf("no server: %v", err)
	}
	t.Setenv(URLEnv, "::")
	if _, err := Create(ctx); err == nil || !strings.Contains(err.Error(), URLEnv) {
		t.Errorf("bad URL: %v", err)
	}
	t.Setenv(URLEnv, "sqlserver://nobody:wrong@127.0.0.1:1")
	if _, err := Create(ctx); err == nil || !strings.Contains(err.Error(), "creating a database") {
		t.Errorf("down: %v", err)
	}
}
