package roundtrip

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/pglive"
	"github.com/thec1oud/dql/sources/pgtest"
)

// Postgres's shop and its variants survive a round trip.
func TestPostgres(t *testing.T) {
	ctx := context.Background()
	admin, err := pgtest.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	for name, files := range map[string][]string{
		"shop":          {"testdata/shop/schema.sql"},
		"shop_variants": {"testdata/shop/schema.sql", "conformance/schemas/postgres/variants.sql"},
	} {
		t.Run(name, func(t *testing.T) {
			read := func(files ...string) *irv1.Catalog {
				t.Helper()
				db, err := pgtest.Create(ctx, admin, files...)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Drop(ctx)
				return invoke(t, pglive.New(pglive.Settings{Catalog: "shop", Connection: db.URL, Namespaces: []string{"app"}, DefaultNamespace: "app"}))
			}
			for i, f := range files {
				files[i] = filepath.Join("..", "..", f)
			}
			roundTrip(t, irv1.Dialect_DIALECT_POSTGRES, read(files...), func(ddl string) *irv1.Catalog {
				f := filepath.Join(t.TempDir(), "ddl.sql")
				if err := os.WriteFile(f, []byte(ddl), 0o644); err != nil {
					t.Fatal(err)
				}
				return read(f)
			})
		})
	}
}
