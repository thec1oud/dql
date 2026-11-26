package introspect

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thec1oud/dql/sources/sqlitetest"
)

var update = flag.Bool("update", false, "rewrite the golden snapshots")

// shop and fixture hold the shop and testdata's fixture.
var shop, fixture *sqlitetest.Database

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()
	var err error
	shop, err = sqlitetest.Create(ctx, filepath.Join("..", "..", "..", "..", "conformance", "schemas", "sqlite", "shop.sql"))
	if err == nil {
		defer shop.Drop()
		fixture, err = sqlitetest.Create(ctx, filepath.Join("testdata", "fixture.sql"))
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: setting up the test databases: %v\n", err)
		return 1
	}
	defer fixture.Drop()
	return m.Run()
}

// read reads db through q, in a transaction.
func read(t *testing.T, db *sql.DB, wrap func(Querier) Querier) (*Snapshot, error) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var q Querier = tx
	if wrap != nil {
		q = wrap(q)
	}
	return Read(context.Background(), q)
}

// Snapshots of the shop and the fixture match their golden files.
func TestRead(t *testing.T) {
	for golden, db := range map[string]*sqlitetest.Database{"shop.json": shop, "fixture.json": fixture} {
		t.Run(golden, func(t *testing.T) {
			s, err := read(t, db.DB, nil)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := json.MarshalIndent(s, "", "  ")
			got = append(got, '\n')
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
				t.Errorf("%s differs; rerun with -update and review the diff", path)
			}
		})
	}
}

// rewrite replaces the query that contains match with query.
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

func TestErrors(t *testing.T) {
	for _, c := range []struct{ name, match, query, want string }{
		{"query fails", "pragma_table_xinfo", "SELECT nope", "reading the columns of "},
		{"scan fails", "FROM pragma_foreign_key_list", "SELECT 'x'", "reading the foreign keys of "},
		{"rows fail", "pragma_table_list", "SELECT 'a', 'table', 0, 0, '' UNION ALL SELECT 'b', 'table', 0, 0, abs(-9223372036854775807 - 1)", "reading the tables"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := read(t, fixture.DB, func(q Querier) Querier { return rewrite{q, c.match, c.query} })
			if err == nil || !strings.HasPrefix(err.Error(), c.want) {
				t.Errorf("got %v, want %s...", err, c.want)
			}
		})
	}
}
