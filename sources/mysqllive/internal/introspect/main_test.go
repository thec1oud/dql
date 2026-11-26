package introspect

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/thec1oud/dql/sources/mysqltest"
)

// shopDB holds the MySQL shop and fixtureDB testdata/fixture.sql, each in a
// fresh database.
var shopDB, fixtureDB *mysqltest.Database

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()
	var err error
	shopDB, err = mysqltest.Create(ctx, filepath.Join("..", "..", "..", "..", "conformance", "schemas", "mysql", "shop.sql"))
	if err == nil {
		defer shopDB.Drop(ctx)
		fixtureDB, err = mysqltest.Create(ctx, filepath.Join("testdata", "fixture.sql"))
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: setting up the test databases: %v\n", err)
		return 1
	}
	defer fixtureDB.Drop(ctx)
	return m.Run()
}
