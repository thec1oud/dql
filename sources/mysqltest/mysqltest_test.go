package mysqltest

import (
	"context"
	"database/sql/driver"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestCreate(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	good := filepath.Join(dir, "good.sql")
	bad := filepath.Join(dir, "bad.sql")
	os.WriteFile(good, []byte("CREATE TABLE t (id INT); CREATE TABLE u (id INT);"), 0o644)
	os.WriteFile(bad, []byte("CREATE TABLE"), 0o644)

	d, err := Create(ctx, good)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := d.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE()").Scan(&n); err != nil || n != 2 {
		t.Errorf("tables %d, %v", n, err)
	}
	d.Drop(ctx)

	for _, c := range []struct{ file, want string }{
		{bad, "mysqltest: running"},
		{filepath.Join(dir, "missing.sql"), "mysqltest: open"},
	} {
		if _, err := Create(ctx, c.file); err == nil || !strings.HasPrefix(err.Error(), c.want) {
			t.Errorf("%s: got %v", c.file, err)
		}
	}
}

func TestConfig(t *testing.T) {
	ctx := context.Background()
	t.Setenv(URLEnv, "")
	if _, err := Create(ctx); err != ErrNoServer {
		t.Errorf("no server: %v", err)
	}
	t.Setenv(URLEnv, "not a dsn")
	if _, err := Create(ctx); err == nil || !strings.HasPrefix(err.Error(), "mysqltest: "+URLEnv) {
		t.Errorf("bad dsn: %v", err)
	}
	t.Setenv(URLEnv, "root@tcp(127.0.0.1:1)/")
	if _, err := Create(ctx); err == nil || !strings.HasPrefix(err.Error(), "mysqltest: creating a database") {
		t.Errorf("no server listening: %v", err)
	}
}

func TestOpenFails(t *testing.T) {
	ctx := context.Background()
	if _, err := Config(); err != nil {
		t.Fatal(err)
	}
	defer func(f func(*mysql.Config) (driver.Connector, error)) { newConnector = f }(newConnector)
	for _, fail := range []int{1, 2} {
		calls := 0
		newConnector = func(cfg *mysql.Config) (driver.Connector, error) {
			if calls++; calls == fail {
				return nil, errors.New("boom")
			}
			return mysql.NewConnector(cfg)
		}
		if _, err := Create(ctx); err == nil || err.Error() != "mysqltest: boom" {
			t.Errorf("connector %d fails: %v", fail, err)
		}
	}
}
