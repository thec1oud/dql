package sqlitetest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreate(t *testing.T) {
	dir := t.TempDir()
	good, bad := filepath.Join(dir, "good.sql"), filepath.Join(dir, "bad.sql")
	os.WriteFile(good, []byte("CREATE TABLE t (a INTEGER); INSERT INTO t VALUES (1);"), 0o644)
	os.WriteFile(bad, []byte("CREATE TABLE"), 0o644)
	d, err := Create(context.Background(), good)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := d.DB.QueryRow("SELECT a FROM t").Scan(&n); err != nil || n != 1 {
		t.Errorf("got %d, %v", n, err)
	}
	d.Drop()
	if _, err := os.Stat(d.Path); !os.IsNotExist(err) {
		t.Errorf("the file outlives Drop: %v", err)
	}
	if _, err := Create(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "running") {
		t.Errorf("bad script: %v", err)
	}
	if _, err := Create(context.Background(), filepath.Join(dir, "none.sql")); err == nil {
		t.Error("missing script: no error")
	}
	t.Setenv("TMPDIR", filepath.Join(dir, "none"))
	if _, err := Create(context.Background(), good); err == nil {
		t.Error("no temporary directory: no error")
	}
}
