package roundtrip

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/sqlitelive"
	"github.com/thec1oud/dql/sources/sqlitetest"
)

// SQLite's shop and variants survive a round trip.
func TestSQLite(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join("..", "..", "conformance", "schemas", "sqlite")
	for name, files := range map[string][]string{"shop": {"shop.sql"}, "shop_variants": {"shop.sql", "variants.sql"}} {
		t.Run(name, func(t *testing.T) {
			// A database runs one script, so the files are joined.
			read := func(text string) *irv1.Catalog {
				t.Helper()
				f := filepath.Join(t.TempDir(), "schema.sql")
				if err := os.WriteFile(f, []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
				db, err := sqlitetest.Create(ctx, f)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Drop()
				return invoke(t, sqlitelive.New(sqlitelive.Settings{Catalog: "shop", Connection: db.Path}))
			}
			var text strings.Builder
			for _, f := range files {
				b, err := os.ReadFile(filepath.Join(dir, f))
				if err != nil {
					t.Fatal(err)
				}
				text.Write(b)
			}
			roundTrip(t, irv1.Dialect_DIALECT_SQLITE, read(text.String()), read)
		})
	}
}
