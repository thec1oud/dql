package mysqllive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/thec1oud/dql/core/bundle"
	"github.com/thec1oud/dql/plugin"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/mysqllive/internal/introspect"
	"github.com/thec1oud/dql/sources/mysqllive/internal/mapping"
	"github.com/thec1oud/dql/sources/mysqltest"
	goldens "github.com/thec1oud/dql/testkit/golden"
)

// shop and mariadbShop are fresh databases holding the shop on the MySQL
// and the MariaDB server, and bin is the plugin built as an executable.
var (
	shop, mariadbShop *mysqltest.Database
	bin               string
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "dql-plugin-mysql-live")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	bin = filepath.Join(dir, "dql-plugin-mysql-live")
	build := exec.Command("go", "build", "-o", bin, "github.com/thec1oud/dql/sources/mysqllive/cmd/dql-plugin-mysql-live")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: building the plugin: %v\n", err)
		return 1
	}
	shop, err = mysqltest.Create(ctx, filepath.Join("..", "..", "conformance", "schemas", "mysql", "shop.sql"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: setting up the test database: %v\n", err)
		return 1
	}
	defer shop.Drop(ctx)
	mariadbShop, err = mysqltest.CreateOn(ctx, mysqltest.MariaDBURLEnv, filepath.Join("..", "..", "conformance", "schemas", "mysql", "shop.sql"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: setting up the test database: %v\n", err)
		return 1
	}
	defer mariadbShop.Drop(ctx)
	return m.Run()
}

func hello() *irv1.CoreHello { return &irv1.CoreHello{IrVersion: bundle.Version()} }

// check invokes c, connected to db, and compares the catalog with the
// mapping's golden shop catalog for db's flavor, which the same schema
// produces under the database name DATABASE.
func check(t *testing.T, c plugin.Client, db *mysqltest.Database) {
	t.Helper()
	res, err := c.Invoke(context.Background(), &irv1.Invoke{Bundle: &irv1.Bundle{Version: bundle.Version()}})
	if err != nil {
		t.Fatal(err)
	}
	b := res.GetBundle()
	if b.GetDialect() != irv1.Dialect_DIALECT_MYSQL || b.GetVersion().GetMajor() != 1 || len(b.GetCatalogs()) != 1 {
		t.Fatalf("bundle: %v", b)
	}
	// The golden is the mapping's result, written as its test writes it.
	got, err := protojson.Marshal(&irv1.InvokeResult{
		Bundle: &irv1.Bundle{Catalogs: b.GetCatalogs()}, Diagnostics: res.GetDiagnostics()})
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(got, &v); err != nil {
		t.Fatal(err)
	}
	got, _ = json.MarshalIndent(v, "", "  ")
	golden := "shop.json"
	if db.MariaDB {
		golden = "shop-mariadb.json"
	}
	want, err := os.ReadFile(filepath.Join("internal", "mapping", "testdata", golden))
	if err != nil {
		t.Fatal(err)
	}
	// The input fingerprint covers the database's name.
	fingerprint := regexp.MustCompile(`"inputFingerprint": "[^"]*"`)
	g := goldens.Mask([]byte(strings.ReplaceAll(string(got), db.Name, "DATABASE")), fingerprint)
	if strings.TrimSpace(string(g)) != strings.TrimSpace(string(goldens.Mask(want, fingerprint))) {
		t.Errorf("the plugin's catalog differs from the mapping's %s", golden)
	}
}

func TestInProcess(t *testing.T) {
	for _, db := range []*mysqltest.Database{shop, mariadbShop} {
		t.Setenv("DQL_TEST_SHOP_DSN", db.DSN)
		c, err := plugin.InProcess(New(Settings{Catalog: "shop", Connection: "env:DQL_TEST_SHOP_DSN"}), hello(), irv1.PluginKind_PLUGIN_KIND_SOURCE)
		if err != nil {
			t.Fatal(err)
		}
		check(t, c, db)
		c.Close()
	}
}

func TestExecutable(t *testing.T) {
	for _, db := range []*mysqltest.Database{shop, mariadbShop} {
		t.Setenv("DQL_TEST_SHOP_DSN", db.DSN)
		args := []string{"-catalog", "shop", "-connection", "env:DQL_TEST_SHOP_DSN"}
		c, err := plugin.Start(context.Background(), bin, "", args, hello(), irv1.PluginKind_PLUGIN_KIND_SOURCE)
		if err != nil {
			t.Fatal(err)
		}
		check(t, c, db)
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// unreadable marks a context the failing read refuses.
type unreadable struct{}

func TestFailures(t *testing.T) {
	defer func() { read, build = introspect.Read, mapping.Map }()
	read = func(ctx context.Context, q introspect.Querier, db string) (*introspect.Snapshot, error) {
		if ctx.Value(unreadable{}) != nil {
			return nil, errors.New("boom")
		}
		return introspect.Read(ctx, q, db)
	}
	build = func(s *introspect.Snapshot, o mapping.Options) (*irv1.Catalog, []*irv1.Diagnostic, error) {
		if o.Catalog == "unmappable" {
			return nil, nil, errors.New("boom")
		}
		return mapping.Map(s, o)
	}
	for _, c := range []struct {
		s    Settings
		want string
	}{
		{Settings{}, "no catalog name"},
		{Settings{Catalog: "c", Connection: "env:DQL_TEST_UNSET"}, "env:DQL_TEST_UNSET, which is not set"},
		{Settings{Catalog: "c", Connection: "not a dsn"}, "the connection of catalog c: "},
		{Settings{Catalog: "c", Connection: "root@tcp(127.0.0.1:1)/"}, "names no database"},
		{Settings{Catalog: "c", Connection: "root@tcp(127.0.0.1:1)/x"}, "connecting to catalog c"},
		{Settings{Catalog: "unreadable", Connection: shop.DSN}, "reading catalog unreadable: boom"},
		{Settings{Catalog: "unmappable", Connection: shop.DSN}, "mysql-live: catalog unmappable: boom"},
	} {
		ctx := context.Background()
		if c.s.Catalog == "unreadable" {
			ctx = context.WithValue(ctx, unreadable{}, true)
		}
		_, err := New(c.s).Invoke(ctx, &irv1.Invoke{})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v", c.s, err)
		}
	}
}

func TestHandshake(t *testing.T) {
	hs := New(Settings{}).Handshake()
	if hs.GetPlugin().GetName() != "mysql-live" || hs.GetPlugin().GetVersion() != mapping.Version ||
		hs.GetIrVersions().GetMin() != "1.0.0" || hs.GetIrVersions().GetMaxMajor() != 1 {
		t.Errorf("handshake: %v", hs)
	}
}
