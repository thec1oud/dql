package ddl

import (
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/testkit/golden"
)

// bundle reads a catalog golden of the repository's testdata.
func bundle(t *testing.T, name string) *irv1.Bundle {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", name, "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	b := &irv1.Bundle{}
	if err := protojson.Unmarshal(data, b); err != nil {
		t.Fatal(err)
	}
	return b
}

// The DDL of each dialect's shop catalog.
func TestShop(t *testing.T) {
	for _, name := range []string{"shop", "sqliteshop", "mysqlshop", "mariadbshop", "sqlservershop"} {
		t.Run(name, func(t *testing.T) {
			b := bundle(t, name)
			got, err := Write(b.GetDialect(), b.GetCatalogs()[0])
			if err != nil {
				t.Fatal(err)
			}
			golden.Assert(t, filepath.Join("testdata", name+".sql"), []byte(got))
		})
	}
}
