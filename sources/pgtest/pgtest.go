// Package pgtest creates throwaway Postgres databases for the tests of the
// source plugins and of the dql command. The server is the one
// DQL_TEST_POSTGRES_URL names, as a role that may create roles and databases
// and SET ROLE to them, such as the postgres superuser of a throwaway
// Postgres 16 server.
package pgtest

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
)

// URLEnv names the server the tests run against.
const URLEnv = "DQL_TEST_POSTGRES_URL"

// The fixture roles are cluster-wide and created once. Every object is
// created as Owner, so output does not depend on who runs the tests; Reader
// is a role to grant to.
const (
	Owner  = "dql_fixture_owner"
	Reader = "dql_fixture_reader"
)

// ErrNoServer reports that URLEnv is not set. The tests fail rather than
// skip without a server: they are what keeps the packages at full coverage.
var ErrNoServer = errors.New("pgtest: " + URLEnv + " is not set. These tests need a Postgres 16 server, for example\n" +
	"  " + URLEnv + "=postgres://postgres@localhost:5432/postgres\n" +
	"and create fresh databases on it")

// Connect connects to the server URLEnv names and creates the fixture roles
// unless they exist.
func Connect(ctx context.Context) (*pgx.Conn, error) {
	u := os.Getenv(URLEnv)
	if u == "" {
		return nil, ErrNoServer
	}
	admin, err := pgx.Connect(ctx, u)
	if err != nil {
		return nil, fmt.Errorf("pgtest: %w", err)
	}
	for _, role := range []string{Owner, Reader} {
		if _, err := admin.Exec(ctx, `DO $$ BEGIN CREATE ROLE `+role+` NOLOGIN;
			EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL; END $$`); err != nil {
			admin.Close(ctx)
			return nil, fmt.Errorf("pgtest: %w", err)
		}
	}
	return admin, nil
}

// Database is a fresh database on the server.
type Database struct {
	// Conn is connected to the database as the server's role.
	Conn *pgx.Conn
	// URL connects to the database.
	URL   string
	admin *pgx.Conn
	ident string
}

var serial atomic.Int64

// Create makes a fresh C-locale UTF-8 database owned by Owner, so collation
// facts do not depend on the server's default locale, and applies each file
// to it as Owner, in order.
func Create(ctx context.Context, admin *pgx.Conn, files ...string) (*Database, error) {
	name := fmt.Sprintf("dql_test_%d_%d_%d", os.Getpid(), time.Now().UnixNano(), serial.Add(1))
	d := &Database{admin: admin, ident: pgx.Identifier{name}.Sanitize()}
	u, err := url.Parse(admin.Config().ConnString())
	if err == nil {
		u.Path = "/" + name
		d.URL = u.String()
		_, err = admin.Exec(ctx, `CREATE DATABASE `+d.ident+` OWNER `+Owner+
			` TEMPLATE template0 ENCODING 'UTF8' LOCALE 'C'`)
	}
	if err == nil {
		d.Conn, err = pgx.Connect(ctx, d.URL)
	}
	if err == nil {
		_, err = d.Conn.Exec(ctx, `SET ROLE `+Owner)
	}
	for _, f := range files {
		var sql []byte
		if err == nil {
			sql, err = os.ReadFile(f)
		}
		if err == nil {
			if _, err = d.Conn.Exec(ctx, string(sql)); err != nil {
				err = fmt.Errorf("%s: %w", f, err)
			}
		}
	}
	if err == nil {
		_, err = d.Conn.Exec(ctx, `RESET ROLE`)
	}
	if err != nil {
		d.Drop(ctx)
		return nil, fmt.Errorf("pgtest: %w", err)
	}
	return d, nil
}

// Drop closes the connection and drops the database.
func (d *Database) Drop(ctx context.Context) {
	if d.Conn != nil {
		d.Conn.Close(ctx)
	}
	d.admin.Exec(ctx, `DROP DATABASE IF EXISTS `+d.ident+` WITH (FORCE)`)
}
