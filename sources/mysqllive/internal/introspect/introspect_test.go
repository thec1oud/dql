package introspect

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden snapshots")

// read reads db through q, in a read-only transaction.
func read(t *testing.T, db *sql.DB, name string, wrap func(Querier) Querier) (*Snapshot, error) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var q Querier = tx
	if wrap != nil {
		q = wrap(q)
	}
	return Read(context.Background(), q, name)
}

// Snapshots of the shop and the fixture match their golden files, with the
// database's generated name left out.
func TestRead(t *testing.T) {
	for golden, db := range map[string]struct {
		db   *sql.DB
		name string
	}{"shop.json": {shopDB.DB, shopDB.Name}, "fixture.json": {fixtureDB.DB, fixtureDB.Name}} {
		t.Run(golden, func(t *testing.T) {
			s, err := read(t, db.db, db.name, nil)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := json.MarshalIndent(s, "", "  ")
			got = []byte(strings.ReplaceAll(string(got), db.name, "DATABASE") + "\n")
			path := filepath.Join("testdata", golden)
			if *update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Errorf("%s differs; rerun with -update and review the diff", golden)
			}
		})
	}
}

// rewrite replaces the query that contains match with query, once per call.
type rewrite struct {
	Querier
	match, query string
}

func (r rewrite) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if strings.Contains(q, r.match) {
		q, args = r.query, nil
	}
	return r.Querier.QueryContext(ctx, q, args...)
}

// MariaDB has no PAD_ATTRIBUTE and no VIEW_TABLE_USAGE: its collations pad
// unless named nopad, and its views list nothing they read.
func TestMariaDB(t *testing.T) {
	s, err := read(t, shopDB.DB, shopDB.Name, func(q Querier) Querier {
		return rewrite{rewrite{q, "SELECT VERSION()", "SELECT '11.4.2-MariaDB'"},
			"TABLE_NAME = 'VIEW_TABLE_USAGE'", "SELECT false"}
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.Database.Flavor != "mariadb" {
		t.Errorf("flavor %q", s.Database.Flavor)
	}
	for _, tb := range s.Tables {
		if tb.ViewDef != nil && len(tb.ViewDef.Reads) > 0 {
			t.Errorf("view %s reads %v", tb.Name, tb.ViewDef.Reads)
		}
	}
}

func TestErrors(t *testing.T) {
	if _, err := read(t, shopDB.DB, "no_such_database", nil); err == nil || err.Error() != "database no_such_database does not exist" {
		t.Errorf("missing database: %v", err)
	}
	for _, c := range []struct{ name, match, query, want string }{
		{"query fails", "SELECT TABLE_NAME, COLUMN_NAME", "SELECT nope", "reading columns"},
		{"scan fails", "FROM information_schema.ROUTINES", "SELECT 1", "reading routines"},
		{"rows fail", "FROM information_schema.TRIGGERS", "SELECT 1, 1, 1, 1, 1, 1 UNION ALL SELECT (SELECT 1 UNION SELECT 2), 1, 1, 1, 1, 1", "reading triggers"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := read(t, shopDB.DB, shopDB.Name, func(q Querier) Querier { return rewrite{q, c.match, c.query} })
			if err == nil || !strings.HasPrefix(err.Error(), c.want) {
				t.Errorf("got %v, want %s...", err, c.want)
			}
		})
	}
}
