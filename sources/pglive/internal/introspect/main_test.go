package introspect

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/thec1oud/dql/sources/internal/pgtest"
)

// Every object is created as the fixture owner, so the golden file does not
// depend on who runs the tests.
const (
	fixtureOwner  = pgtest.Owner
	fixtureReader = pgtest.Reader
)

// shopDB holds testdata/shop/schema.sql and fixtureDB testdata/fixture.sql,
// each in a fresh database, so the shop golden holds exactly §1.3 and
// database-wide objects such as the fixture's extension stay out of it.
var shopDB, fixtureDB *pgx.Conn

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()
	admin, err := pgtest.Connect(ctx)
	if err == nil {
		defer admin.Close(ctx)
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
		var db *pgtest.Database
		if db, err = pgtest.Create(ctx, admin, d.file); err == nil {
			defer db.Drop(ctx)
			*d.conn = db.Conn
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: setting up the test databases: %v\n", err)
		return 1
	}
	return m.Run()
}
