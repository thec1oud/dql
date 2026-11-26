package introspect

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/thec1oud/dql/sources/mysqltest"
)

// server is one test server, with the shop and testdata's fixture for its
// flavor each in a fresh database. Its goldens are suffixed by suffix.
type server struct {
	name, suffix string
	shop, fixture *mysqltest.Database
}

// mysqlServer and mariadbServer are the MySQL and MariaDB servers.
var mysqlServer, mariadbServer = &server{name: "mysql"}, &server{name: "mariadb", suffix: "-mariadb"}

// shopDB is the MySQL shop.
var shopDB *mysqltest.Database

// file names the flavor's form of a testdata file, such as
// fixture-mariadb.sql for fixture.sql.
func (s *server) file(name string) string {
	ext := filepath.Ext(name)
	return filepath.Join("testdata", name[:len(name)-len(ext)]+s.suffix+ext)
}

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()
	for env, s := range map[string]*server{mysqltest.URLEnv: mysqlServer, mysqltest.MariaDBURLEnv: mariadbServer} {
		var err error
		s.shop, err = mysqltest.CreateOn(ctx, env, filepath.Join("..", "..", "..", "..", "conformance", "schemas", "mysql", "shop.sql"))
		if err == nil {
			defer s.shop.Drop(ctx)
			s.fixture, err = mysqltest.CreateOn(ctx, env, s.file("fixture.sql"))
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAIL: setting up the test databases: %v\n", err)
			return 1
		}
		defer s.fixture.Drop(ctx)
	}
	shopDB = mysqlServer.shop
	return m.Run()
}
