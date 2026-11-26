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
	if _, err := Create(ctx); err != (ErrNoServer{URLEnv}) || !strings.Contains(err.Error(), "MySQL 8.4") {
		t.Errorf("no server: %v", err)
	}
	t.Setenv(MariaDBURLEnv, "")
	if _, err := CreateOn(ctx, MariaDBURLEnv); err != (ErrNoServer{MariaDBURLEnv}) || !strings.Contains(err.Error(), "MariaDB 10.11") {
		t.Errorf("no MariaDB server: %v", err)
	}
	t.Setenv(URLEnv, "not a dsn")
	if _, err := Create(ctx); err == nil || !strings.HasPrefix(err.Error(), "mysqltest: "+URLEnv) {
		t.Errorf("bad dsn: %v", err)
	}
	t.Setenv(URLEnv, "root@tcp(127.0.0.1:1)/")
	if _, err := Create(ctx); err == nil || !strings.HasPrefix(err.Error(), "mysqltest: reading the server's version") {
		t.Errorf("no server listening: %v", err)
	}
}

func TestOpenFails(t *testing.T) {
	ctx := context.Background()
	if _, err := Config(URLEnv); err != nil {
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

// A user who may not create databases gets the server's refusal.
func TestCreateRefused(t *testing.T) {
	ctx := context.Background()
	cfg, err := Config(URLEnv)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.ExecContext(ctx, "CREATE USER IF NOT EXISTS 'dql_unprivileged'@'%' IDENTIFIED BY 'secret'"); err != nil {
		t.Fatal(err)
	}
	user := cfg.Clone()
	user.User, user.Passwd = "dql_unprivileged", "secret"
	t.Setenv(URLEnv, user.FormatDSN())
	if _, err := Create(ctx); err == nil || !strings.HasPrefix(err.Error(), "mysqltest: creating a database") {
		t.Errorf("refused: %v", err)
	}
}

func TestServer(t *testing.T) {
	for version, want := range map[string]string{"8.4.3": "utf8mb4_0900_ai_ci", "10.11.9-MariaDB": "utf8mb4_uca1400_ai_ci"} {
		if mariadb, collation := server(version); collation != want || mariadb != (want != "utf8mb4_0900_ai_ci") {
			t.Errorf("%s: %v %s", version, mariadb, collation)
		}
	}
}

func TestMariaDB(t *testing.T) {
	ctx := context.Background()
	d, err := CreateOn(ctx, MariaDBURLEnv)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Drop(ctx)
	var collation string
	if err := d.DB.QueryRowContext(ctx, "SELECT @@collation_database").Scan(&collation); err != nil || !d.MariaDB || collation != "utf8mb4_uca1400_ai_ci" {
		t.Errorf("MariaDB %v, collation %s, %v", d.MariaDB, collation, err)
	}
}
