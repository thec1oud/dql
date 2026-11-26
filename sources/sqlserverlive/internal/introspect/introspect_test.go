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

	"github.com/thec1oud/dql/sources/sqlservertest"
)

var update = flag.Bool("update", false, "rewrite the golden snapshots")

var shopDB, fixtureDB *sqlservertest.Database

func TestMain(m *testing.M) {
	flag.Parse()
	ctx := context.Background()
	var err error
	shopDB, err = sqlservertest.Create(ctx, filepath.Join("..", "..", "..", "..", "conformance", "schemas", "sqlserver", "shop.sql"))
	if err == nil {
		fixtureDB, err = sqlservertest.Create(ctx, filepath.Join("testdata", "fixture.sql"))
	}
	if err != nil {
		if shopDB != nil {
			shopDB.Drop(ctx)
		}
		os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
	code := m.Run()
	shopDB.Drop(ctx)
	fixtureDB.Drop(ctx)
	os.Exit(code)
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

// Snapshots of the shop and the fixture match their golden files, with the
// server's version left out.
func TestRead(t *testing.T) {
	for golden, db := range map[string]*sqlservertest.Database{"shop.json": shopDB, "fixture.json": fixtureDB} {
		t.Run(golden, func(t *testing.T) {
			s, err := read(t, db.DB, nil)
			if err != nil {
				t.Fatal(err)
			}
			s.Database.Version = "VERSION"
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
		q = r.query
	}
	return r.Querier.QueryContext(ctx, q, args...)
}

func TestErrors(t *testing.T) {
	for _, c := range []struct{ name, match, query, want string }{
		{"query fails", "FROM sys.databases", "SELECT nope", "reading the database"},
		{"scan fails", "FROM sys.schemas", "SELECT 'x', 'y'", "reading schemas"},
		{"rows fail", "FROM sys.extended_properties", "SELECT 1, 1, 1, 'x' UNION ALL SELECT 1, 1, 1/0, 'x'", "reading comments"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := read(t, shopDB.DB, func(q Querier) Querier { return rewrite{q, c.match, c.query} })
			if err == nil || !strings.HasPrefix(err.Error(), c.want) {
				t.Errorf("got %v, want %s...", err, c.want)
			}
		})
	}
}
