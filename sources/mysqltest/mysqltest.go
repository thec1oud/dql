// Package mysqltest creates throwaway MySQL or MariaDB databases for the
// tests of the mysql-live source plugin and of the dql command. The server
// is the one DQL_TEST_MYSQL_URL names, as a go-sql-driver/mysql DSN without
// a database for a user that may create and drop databases, such as
// root:root@tcp(localhost:3306)/.
package mysqltest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-sql-driver/mysql"
)

// URLEnv names the MySQL server the tests run against, and MariaDBURLEnv
// the MariaDB server.
const (
	URLEnv        = "DQL_TEST_MYSQL_URL"
	MariaDBURLEnv = "DQL_TEST_MARIADB_URL"
)

// ErrNoServer reports that URLEnv or MariaDBURLEnv is not set. The tests
// fail rather than skip without a server: they are what keeps the packages
// at full coverage.
type ErrNoServer struct{ Env string }

func (e ErrNoServer) Error() string {
	server, port := "a MySQL 8.4", "3306"
	if e.Env == MariaDBURLEnv {
		server, port = "a MariaDB 10.11 or newer", "3307"
	}
	return "mysqltest: " + e.Env + " is not set. These tests need " + server + " server, for example\n" +
		"  " + e.Env + "=root:root@tcp(localhost:" + port + ")/\n" +
		"and create fresh databases on it"
}

// Config returns the parsed DSN of the server env names.
func Config(env string) (*mysql.Config, error) {
	dsn := os.Getenv(env)
	if dsn == "" {
		return nil, ErrNoServer{env}
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("mysqltest: %s: %w", env, err)
	}
	return cfg, nil
}

// Database is a fresh database on the test server.
type Database struct {
	// DB is a pool connected to the database.
	DB *sql.DB
	// Name is the database's name and DSN a DSN naming it.
	Name, DSN string
	// MariaDB is set when the server is MariaDB rather than MySQL.
	MariaDB bool
	admin   *sql.DB
}

var seq atomic.Int64

// Create creates a fresh database on the MySQL server and runs the
// statements of each of files in it, in order. A file may hold several
// statements.
func Create(ctx context.Context, files ...string) (*Database, error) {
	return CreateOn(ctx, URLEnv, files...)
}

// CreateOn is Create on the server env names: URLEnv or MariaDBURLEnv.
func CreateOn(ctx context.Context, env string, files ...string) (*Database, error) {
	cfg, err := Config(env)
	if err != nil {
		return nil, err
	}
	cfg = cfg.Clone()
	cfg.DBName = ""
	admin, err := open(cfg)
	if err != nil {
		return nil, err
	}
	name := fmt.Sprintf("dql_test_%d_%d_%d", os.Getpid(), time.Now().UnixNano()%1e6, seq.Add(1))
	var version string
	if err := admin.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		admin.Close()
		return nil, fmt.Errorf("mysqltest: reading the server's version: %w", err)
	}
	mariadb, collation := server(version)
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name+" CHARACTER SET utf8mb4 COLLATE "+collation); err != nil {
		admin.Close()
		return nil, fmt.Errorf("mysqltest: creating a database: %w", err)
	}
	d := &Database{Name: name, MariaDB: mariadb, admin: admin}
	cfg.DBName = name
	cfg.MultiStatements = true
	d.DSN = cfg.FormatDSN()
	if d.DB, err = open(cfg); err == nil {
		err = d.load(ctx, files)
	}
	if err != nil {
		d.Drop(ctx)
		return nil, err
	}
	return d, nil
}

func (d *Database) load(ctx context.Context, files []string) error {
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("mysqltest: %w", err)
		}
		if _, err := d.DB.ExecContext(ctx, string(b)); err != nil {
			return fmt.Errorf("mysqltest: running %s: %w", f, err)
		}
	}
	return nil
}

// Drop closes the database's connections and drops it.
func (d *Database) Drop(ctx context.Context) {
	if d.DB != nil {
		d.DB.Close()
	}
	d.admin.ExecContext(ctx, "DROP DATABASE IF EXISTS "+d.Name) //nolint:errcheck // best effort
	d.admin.Close()
}

// newConnector is mysql.NewConnector, a variable so that tests can make it
// fail.
var newConnector = mysql.NewConnector

func open(cfg *mysql.Config) (*sql.DB, error) {
	c, err := newConnector(cfg)
	if err != nil {
		return nil, fmt.Errorf("mysqltest: %w", err)
	}
	return sql.OpenDB(c), nil
}

// server reports whether the server of version is MariaDB, and the
// collation of its test databases: MySQL's default, utf8mb4_0900_ai_ci, or
// on MariaDB, which has that only from 11.4, the UCA 14.0 collation of the
// same meaning, which it has from 10.10.
func server(version string) (mariadb bool, collation string) {
	if strings.Contains(version, "MariaDB") {
		return true, "utf8mb4_uca1400_ai_ci"
	}
	return false, "utf8mb4_0900_ai_ci"
}
