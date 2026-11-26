// Package sqlitetest creates throwaway SQLite databases for the tests of the
// sqlite-live source plugin and of the dql command. Each is a file in a
// fresh temporary directory, so the tests need no server.
package sqlitetest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// Database is a fresh database file.
type Database struct {
	// DB is a pool connected to the database.
	DB *sql.DB
	// Path is the database file's path.
	Path string
}

// Create makes a database file and runs the SQL script at script in it.
func Create(ctx context.Context, script string) (*Database, error) {
	b, err := os.ReadFile(script)
	if err != nil {
		return nil, fmt.Errorf("sqlitetest: %w", err)
	}
	dir, err := os.MkdirTemp("", "dql-sqlitetest")
	if err != nil {
		return nil, fmt.Errorf("sqlitetest: %w", err)
	}
	d := &Database{Path: filepath.Join(dir, "test.db")}
	d.DB, _ = sql.Open("sqlite", d.Path) // Open only checks the driver's name.
	if _, err := d.DB.ExecContext(ctx, string(b)); err != nil {
		d.Drop()
		return nil, fmt.Errorf("sqlitetest: running %s: %w", script, err)
	}
	return d, nil
}

// Drop closes the database and removes its file.
func (d *Database) Drop() {
	d.DB.Close()
	os.RemoveAll(filepath.Dir(d.Path))
}
