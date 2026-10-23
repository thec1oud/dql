package mapping

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/thec1oud/dql/sources/pglive/internal/introspect"
	"github.com/thec1oud/dql/sources/pgtest"
)

// The Snapshots the tests map, read once from fresh databases: shop holds
// testdata/shop/schema.sql (§1.3), fixture the introspection fixture of the
// objects shop lacks, and examples shop plus the objects of other record
// examples (testdata/examples.sql).
var shop, fixture, examples *introspect.Snapshot

var shopSchema = filepath.Join("..", "..", "..", "..", "testdata", "shop", "schema.sql")

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
		snap       **introspect.Snapshot
		namespaces []string
		files      []string
	}{
		{&shop, []string{"app"}, []string{shopSchema}},
		{&fixture, []string{"ext", "fx"}, []string{filepath.Join("..", "introspect", "testdata", "fixture.sql")}},
		{&examples, []string{"app"}, []string{shopSchema, filepath.Join("testdata", "examples.sql")}},
	} {
		var db *pgtest.Database
		if err == nil {
			db, err = pgtest.Create(ctx, admin, d.files...)
		}
		if err == nil {
			*d.snap, err = introspect.Read(ctx, db.Conn, d.namespaces)
			db.Drop(ctx)
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: setting up the test databases: %v\n", err)
		return 1
	}
	return m.Run()
}
