package roundtrip

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/sqlserverlive"
	"github.com/thec1oud/dql/sources/sqlservertest"
)

// SQL Server's shop and variants survive a round trip.
func TestSQLServer(t *testing.T) {
	ctx := context.Background()
	shop := filepath.Join("..", "..", "conformance", "schemas", "sqlserver", "shop.sql")
	variants := filepath.Join("..", "..", "conformance", "schemas", "sqlserver", "variants.sql")
	for name, files := range map[string][]string{"shop": {shop}, "shop_variants": {shop, variants}} {
		t.Run(name, func(t *testing.T) {
			read := func(files ...string) *irv1.Catalog {
				t.Helper()
				db, err := sqlservertest.Create(ctx, files...)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Drop(ctx)
				return invoke(t, sqlserverlive.New(sqlserverlive.Settings{Catalog: "shop", Connection: db.URL, DefaultNamespace: "app"}))
			}
			roundTrip(t, irv1.Dialect_DIALECT_SQLSERVER, read(files...), func(ddl string) *irv1.Catalog {
				f := filepath.Join(t.TempDir(), "ddl.sql")
				if err := os.WriteFile(f, []byte(ddl), 0o644); err != nil {
					t.Fatal(err)
				}
				return read(f)
			})
		})
	}
}
