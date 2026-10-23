package introspect

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// urlEnv names the server the tests run against. It must be a role that may
// create roles and databases and SET ROLE to them, such as the postgres
// superuser of a throwaway Postgres 16 server. Without it the tests fail
// rather than skip: they are what keeps this package at full coverage.
const urlEnv = "DQL_TEST_POSTGRES_URL"

// The fixture roles are cluster-wide and created once. Every object is
// created as fixtureOwner, so the golden file does not depend on who runs the
// tests.
const (
	fixtureOwner  = "dql_fixture_owner"
	fixtureReader = "dql_fixture_reader"
)

// shopDB holds testdata/shop/schema.sql and fixtureDB testdata/fixture.sql,
// each in a fresh database, so the shop golden holds exactly §1.3 and
// database-wide objects such as the fixture's extension stay out of it.
var shopDB, fixtureDB *pgx.Conn

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	url := os.Getenv(urlEnv)
	if url == "" {
		fmt.Fprintf(os.Stderr, "FAIL: %s is not set. These tests need a Postgres 16 server, for example\n"+
			"  %s=postgres://postgres@localhost:5432/postgres\n"+
			"and create fresh databases on it.\n", urlEnv, urlEnv)
		return 1
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, url)
	if err == nil {
		defer admin.Close(ctx)
		err = createRoles(ctx, admin)
	}
	for _, d := range []struct {
		conn **pgx.Conn
		file string
	}{
		{&shopDB, filepath.Join("..", "..", "..", "..", "testdata", "shop", "schema.sql")},
		{&fixtureDB, filepath.Join("testdata", "fixture.sql")},
	} {
		if err != nil {
			break
		}
		var drop func()
		*d.conn, drop, err = createDatabase(ctx, admin, d.file)
		if err == nil {
			defer drop()
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: setting up the test databases: %v\n", err)
		return 1
	}
	return m.Run()
}

// createRoles creates the fixture roles unless they exist.
func createRoles(ctx context.Context, admin *pgx.Conn) error {
	for _, role := range []string{fixtureOwner, fixtureReader} {
		if _, err := admin.Exec(ctx, `DO $$ BEGIN CREATE ROLE `+role+` NOLOGIN;
			EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL; END $$`); err != nil {
			return err
		}
	}
	return nil
}

// createDatabase creates a fresh C-locale database owned by fixtureOwner, so
// collation facts do not depend on the server's default locale, and applies
// file to it as fixtureOwner. It returns a connection and a function that
// closes it and drops the database.
func createDatabase(ctx context.Context, admin *pgx.Conn, file string) (*pgx.Conn, func(), error) {
	sql, err := os.ReadFile(file)
	if err != nil {
		return nil, nil, err
	}
	name := fmt.Sprintf("dql_test_introspect_%d_%d", os.Getpid(), time.Now().UnixNano())
	ident := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+ident+` OWNER `+fixtureOwner+
		` TEMPLATE template0 ENCODING 'UTF8' LOCALE 'C'`); err != nil {
		return nil, nil, err
	}
	cfg := admin.Config().Copy()
	cfg.Database = name
	conn, err := pgx.ConnectConfig(ctx, cfg)
	drop := func() {
		if conn != nil {
			conn.Close(ctx)
		}
		admin.Exec(ctx, `DROP DATABASE IF EXISTS `+ident+` WITH (FORCE)`)
	}
	if err == nil {
		_, err = conn.Exec(ctx, `SET ROLE `+fixtureOwner)
	}
	if err == nil {
		_, err = conn.Exec(ctx, string(sql))
	}
	if err == nil {
		_, err = conn.Exec(ctx, `RESET ROLE`)
	}
	if err != nil {
		drop()
		return nil, nil, fmt.Errorf("%s: %w", file, err)
	}
	return conn, drop, nil
}
