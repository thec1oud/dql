package roundtrip

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/mysqllive"
	"github.com/thec1oud/dql/sources/mysqltest"
)

// MySQL's and MariaDB's shop and variants survive a round trip. A view's
// definition names the database it was read from, so the written DDL names
// none, and the catalogs are compared with each database's name masked.
func TestMySQL(t *testing.T) {
	ctx := context.Background()
	shop := filepath.Join("..", "..", "conformance", "schemas", "mysql", "shop.sql")
	variants := filepath.Join("..", "..", "conformance", "schemas", "mysql", "variants.sql")
	for _, env := range []string{mysqltest.URLEnv, mysqltest.MariaDBURLEnv} {
		for name, files := range map[string][]string{"shop": {shop}, "shop_variants": {shop, variants}} {
			t.Run(env+"/"+name, func(t *testing.T) {
				read := func(files ...string) (*irv1.Catalog, string) {
					t.Helper()
					db, err := mysqltest.CreateOn(ctx, env, files...)
					if err != nil {
						t.Fatal(err)
					}
					defer db.Drop(ctx)
					c := invoke(t, mysqllive.New(mysqllive.Settings{Catalog: "shop", Connection: db.DSN}))
					return c, db.Name
				}
				first, firstDB := read(files...)
				roundTrip(t, irv1.Dialect_DIALECT_MYSQL, first, func(ddl string) *irv1.Catalog {
					f := filepath.Join(t.TempDir(), "ddl.sql")
					ddl = strings.ReplaceAll(ddl, "`"+firstDB+"`.", "")
					if err := os.WriteFile(f, []byte(ddl), 0o644); err != nil {
						t.Fatal(err)
					}
					second, secondDB := read(f)
					return renamed(t, second, secondDB, firstDB)
				})
			})
		}
	}
}
