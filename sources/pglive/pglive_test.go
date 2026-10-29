package pglive

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thec1oud/dql/core/bundle"
	"github.com/thec1oud/dql/plugin"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/pglive/internal/introspect"
	"github.com/thec1oud/dql/sources/pglive/internal/mapping"
	"github.com/thec1oud/dql/sources/pgtest"
	"github.com/thec1oud/dql/testkit/golden"
)

// shopURL connects to a fresh database holding the running example (§1.3),
// and bin is the plugin built as an executable.
var shopURL, bin string

// serverText is the text of the golden the server writes differently.
var serverText []*regexp.Regexp

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "dql-plugin-pg-live")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	bin = filepath.Join(dir, "dql-plugin-pg-live")
	build := exec.Command("go", "build", "-o", bin, "github.com/thec1oud/dql/sources/pglive/cmd/dql-plugin-pg-live")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: building the plugin: %v\n", err)
		return 1
	}
	admin, err := pgtest.Connect(ctx)
	var db *pgtest.Database
	if err == nil {
		defer admin.Close(ctx)
		db, err = pgtest.Create(ctx, admin, filepath.Join("..", "..", "testdata", "shop", "schema.sql"))
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: setting up the test database: %v\n", err)
		return 1
	}
	defer db.Drop(ctx)
	shopURL, serverText = db.URL, db.ServerText
	return m.Run()
}

var shopSettings = Settings{Catalog: "shop", Connection: "env:DQL_TEST_SHOP_URL", Namespaces: []string{"app"}, DefaultNamespace: "app"}

func hello() *irv1.CoreHello { return &irv1.CoreHello{IrVersion: bundle.Version()} }

// check invokes c and compares the catalog with the mapping's golden shop
// catalog, which the same schema and settings produce.
func check(t *testing.T, c plugin.Client) {
	t.Helper()
	res, err := c.Invoke(context.Background(), &irv1.Invoke{Bundle: &irv1.Bundle{Version: bundle.Version()}})
	if err != nil {
		t.Fatal(err)
	}
	b := res.GetBundle()
	if b.GetDialect() != irv1.Dialect_DIALECT_POSTGRES || b.GetVersion().GetMajor() != 1 || len(b.GetCatalogs()) != 1 {
		t.Fatalf("bundle: %v", b)
	}
	got, err := bundle.MarshalJSON(b.GetCatalogs()[0])
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("internal", "mapping", "testdata", "shop.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(golden.Mask(got, serverText...)) != string(golden.Mask(want, serverText...)) {
		t.Error("the plugin's catalog differs from the mapping's shop golden")
	}
}

func TestInProcess(t *testing.T) {
	t.Setenv("DQL_TEST_SHOP_URL", shopURL)
	c, err := plugin.InProcess(New(shopSettings), hello(), irv1.PluginKind_PLUGIN_KIND_SOURCE)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	check(t, c)
}

func TestExecutable(t *testing.T) {
	t.Setenv("DQL_TEST_SHOP_URL", shopURL)
	args := []string{"-catalog", "shop", "-connection", "env:DQL_TEST_SHOP_URL", "-namespaces", "app", "-default-namespace", "app"}
	c, err := plugin.Start(context.Background(), bin, "", args, hello(), irv1.PluginKind_PLUGIN_KIND_SOURCE)
	if err != nil {
		t.Fatal(err)
	}
	check(t, c)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestDefaultNamespace reads only the default namespace when none is listed.
// shop has nothing in public.
func TestDefaultNamespace(t *testing.T) {
	res, err := New(Settings{Catalog: "shop", Connection: shopURL}).Invoke(context.Background(), &irv1.Invoke{})
	if err != nil {
		t.Fatal(err)
	}
	c := res.GetBundle().GetCatalogs()[0]
	if c.GetDefaultNamespace() != "public" || len(c.GetNamespaces()) != 1 || c.GetNamespaces()[0].GetName().GetStored() != "public" {
		t.Errorf("catalog: %v", c)
	}
}

func TestFailures(t *testing.T) {
	defer func() { read, build = introspect.Read, mapping.Map }()
	read = func(ctx context.Context, c introspect.Conn, ns []string) (*introspect.Snapshot, error) {
		if ns[0] == "unreadable" {
			return nil, errors.New("boom")
		}
		return introspect.Read(ctx, c, ns)
	}
	build = func(s *introspect.Snapshot, o mapping.Options) (*irv1.Catalog, []*irv1.Diagnostic, error) {
		if o.DefaultNamespace == "unmappable" {
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
		{Settings{Catalog: "c", Connection: "postgres://nobody@127.0.0.1:1/x"}, "connecting to catalog c"},
		{Settings{Catalog: "c", Connection: shopURL, Namespaces: []string{"unreadable"}}, "reading catalog c: boom"},
		{Settings{Catalog: "c", Connection: shopURL, DefaultNamespace: "unmappable"}, "pg-live: catalog c: boom"},
	} {
		_, err := New(c.s).Invoke(context.Background(), &irv1.Invoke{})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v", c.s, err)
		}
	}
}

func TestHandshake(t *testing.T) {
	hs := New(Settings{}).Handshake()
	if hs.GetPlugin().GetName() != "pg-live" || hs.GetPlugin().GetVersion() != mapping.Version ||
		hs.GetIrVersions().GetMin() != "1.0.0" || hs.GetIrVersions().GetMaxMajor() != 1 {
		t.Errorf("handshake: %v", hs)
	}
}
