package pgtest

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreate(t *testing.T) {
	ctx := context.Background()
	admin, err := Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	dir := t.TempDir()
	schema := filepath.Join(dir, "schema.sql")
	if err := os.WriteFile(schema, []byte(`CREATE TABLE t (a int)`), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := Create(ctx, admin, schema)
	if err != nil {
		t.Fatal(err)
	}
	var owner string
	if err := d.Conn.QueryRow(ctx, `SELECT tableowner FROM pg_tables WHERE tablename = 't'`).Scan(&owner); err != nil || owner != Owner {
		t.Errorf("owner %q, %v", owner, err)
	}
	d.Drop(ctx)
	var n int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_database WHERE datname = $1`,
		strings.Trim(d.ident, `"`)).Scan(&n); err != nil || n != 0 {
		t.Errorf("database left behind: %d, %v", n, err)
	}

	bad := filepath.Join(dir, "bad.sql")
	if err := os.WriteFile(bad, []byte(`CREATE TABLE`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, files := range [][]string{{bad}, {filepath.Join(dir, "missing.sql")}} {
		if _, err := Create(ctx, admin, files...); err == nil {
			t.Errorf("%v: want an error", files)
		}
	}
}

func TestServerText(t *testing.T) {
	if serverText(160000) != nil || serverText(180001) != nil {
		t.Error("masks from Postgres 16 on")
	}
	view := []byte(`"definition": " SELECT posts.id,\n    \"a\"\n   FROM app.posts;", "fingerprint": "sha256:` + strings.Repeat("0a", 32) + `"`)
	got := view
	for _, m := range serverText(150019) {
		got = m.ReplaceAll(got, nil)
	}
	if string(got) != `, "fingerprint": ""` {
		t.Errorf("masked %s", got)
	}
}

func TestConnectFailures(t *testing.T) {
	ctx := context.Background()
	admin, err := Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	// A login role without CREATEROLE cannot create the fixture roles. It
	// gets a password, since servers such as CI's require one over TCP.
	if _, err := admin.Exec(ctx, `DO $$ BEGIN CREATE ROLE dql_test_plain LOGIN;
		EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `ALTER ROLE dql_test_plain PASSWORD 'dql_test_plain'`); err != nil {
		t.Fatal(err)
	}
	server := os.Getenv(URLEnv)
	plain, err := url.Parse(server)
	if err != nil {
		t.Fatal(err)
	}
	plain.User = url.UserPassword("dql_test_plain", "dql_test_plain")
	for _, c := range []struct{ url, want string }{
		{"", "is not set"},
		{"postgres://nobody@127.0.0.1:1/x", "pgtest:"},
		{plain.String(), "permission denied"},
	} {
		t.Setenv(URLEnv, c.url)
		if _, err := Connect(ctx); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: %v", c.url, err)
		}
	}
	t.Setenv(URLEnv, server)

	closed, err := Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	closed.Close(ctx)
	if _, err := Create(ctx, closed); err == nil {
		t.Error("want an error from a closed connection")
	}
}
