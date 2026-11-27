// Package sqlservertest creates throwaway SQL Server databases for the
// tests of the sqlserver-live source plugin and of the dql command. The
// server is the one DQL_TEST_SQLSERVER_URL names, as a go-mssqldb URL
// without a database for a login that may create and drop databases, such
// as sqlserver://sa:password@localhost:1433.
package sqlservertest

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	_ "github.com/microsoft/go-mssqldb" // the sqlserver driver
)

// URLEnv names the SQL Server the tests run against.
const URLEnv = "DQL_TEST_SQLSERVER_URL"

// ErrNoServer reports that URLEnv is not set. The tests fail rather than
// skip without a server: they are what keeps the packages at full coverage.
type ErrNoServer struct{}

func (ErrNoServer) Error() string {
	return "sqlservertest: " + URLEnv + " is not set. These tests need a SQL Server 2022 or newer, for example\n" +
		"  " + URLEnv + "=sqlserver://sa:password@localhost:1433\n" +
		"and create fresh databases on it"
}

// Database is a fresh database on the test server.
type Database struct {
	// DB is a pool connected to the database.
	DB *sql.DB
	// Name is the database's name and URL a URL naming it.
	Name, URL string
	admin     *sql.DB
}

var seq atomic.Int64

// batches splits a script at its GO lines, as sqlcmd does.
var batches = regexp.MustCompile(`(?im)^[ \t]*GO[ \t]*$`)

// Create creates a fresh database and runs the batches of each of files in
// it, in order. A file's batches are separated by GO lines.
func Create(ctx context.Context, files ...string) (*Database, error) {
	base := os.Getenv(URLEnv)
	if base == "" {
		return nil, ErrNoServer{}
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("sqlservertest: %s: %w", URLEnv, err)
	}
	// Opening checks nothing; the first statement connects.
	admin, _ := sql.Open("sqlserver", base)
	name := fmt.Sprintf("dql_test_%d_%d_%d", os.Getpid(), time.Now().UnixNano()%1e6, seq.Add(1))
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		admin.Close()
		return nil, fmt.Errorf("sqlservertest: creating a database: %w", err)
	}
	q := u.Query()
	q.Set("database", name)
	u.RawQuery = q.Encode()
	d := &Database{Name: name, URL: u.String(), admin: admin}
	d.DB, _ = sql.Open("sqlserver", d.URL)
	if err := d.load(ctx, files); err != nil {
		d.Drop(ctx)
		return nil, err
	}
	return d, nil
}

func (d *Database) load(ctx context.Context, files []string) error {
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("sqlservertest: %w", err)
		}
		for i, batch := range batches.Split(string(b), -1) {
			if strings.TrimSpace(batch) == "" {
				continue
			}
			if _, err := d.DB.ExecContext(ctx, batch); err != nil {
				return fmt.Errorf("sqlservertest: running batch %d of %s: %w", i+1, f, err)
			}
		}
	}
	return nil
}

// Drop closes the database's connections and drops it.
func (d *Database) Drop(ctx context.Context) {
	d.DB.Close()
	// Other connections to it, such as a plugin's, may still be open.
	d.admin.ExecContext(ctx, "ALTER DATABASE "+d.Name+" SET SINGLE_USER WITH ROLLBACK IMMEDIATE") //nolint:errcheck // best effort
	d.admin.ExecContext(ctx, "DROP DATABASE IF EXISTS "+d.Name)                                   //nolint:errcheck // best effort
	d.admin.Close()
}
