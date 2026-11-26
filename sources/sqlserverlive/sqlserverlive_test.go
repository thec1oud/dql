package sqlserverlive

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
	"github.com/thec1oud/dql/sources/sqlserverlive/internal/introspect"
	"github.com/thec1oud/dql/sources/sqlserverlive/internal/mapping"
	"github.com/thec1oud/dql/sources/sqlservertest"
	goldens "github.com/thec1oud/dql/testkit/golden"
)

// shop is a fresh database holding the shop, and bin is the plugin built
// as an executable.
var (
	shop *sqlservertest.Database
	bin  string
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "dql-plugin-sqlserver-live")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	bin = filepath.Join(dir, "dql-plugin-sqlserver-live")
	build := exec.Command("go", "build", "-o", bin, "github.com/thec1oud/dql/sources/sqlserverlive/cmd/dql-plugin-sqlserver-live")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: building the plugin: %v\n", err)
		return 1
	}
	shop, err = sqlservertest.Create(ctx, filepath.Join("..", "..", "conformance", "schemas", "sqlserver", "shop.sql"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: setting up the test database: %v\n", err)
		return 1
	}
	defer shop.Drop(ctx)
	return m.Run()
}

func hello() *irv1.CoreHello { return &irv1.CoreHello{IrVersion: bundle.Version()} }

// check invokes c and compares the catalog with the mapping's golden shop
// catalog, which the same schema produces with the server's version as
// VERSION.
func check(t *testing.T, c plugin.Client) {
	t.Helper()
	res, err := c.Invoke(context.Background(), &irv1.Invoke{Bundle: &irv1.Bundle{Version: bundle.Version()}})
	if err != nil {
		t.Fatal(err)
	}
	b := res.GetBundle()
	if b.GetDialect() != irv1.Dialect_DIALECT_SQLSERVER || b.GetVersion().GetMajor() != 1 || len(b.GetCatalogs()) != 1 {
		t.Fatalf("bundle: %v", b)
	}
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
	want, err := os.ReadFile(filepath.Join("internal", "mapping", "testdata", "shop.json"))
	if err != nil {
		t.Fatal(err)
	}
	// The Database extension holds the server's version, and the
	// fingerprint covers it.
	masks := []*regexp.Regexp{regexp.MustCompile(`"inputFingerprint": "[^"]*"`), regexp.MustCompile(`"version": "[^"]*"`)}
	if strings.TrimSpace(string(goldens.Mask(got, masks...))) != strings.TrimSpace(string(goldens.Mask(want, masks...))) {
		t.Errorf("the plugin's catalog differs from the mapping's shop.json")
	}
}

func TestInProcess(t *testing.T) {
	t.Setenv("DQL_TEST_SHOP_URL", shop.URL)
	c, err := plugin.InProcess(New(Settings{Catalog: "shop", Connection: "env:DQL_TEST_SHOP_URL"}), hello(), irv1.PluginKind_PLUGIN_KIND_SOURCE)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	check(t, c)
}

func TestExecutable(t *testing.T) {
	t.Setenv("DQL_TEST_SHOP_URL", shop.URL)
	args := []string{"-catalog", "shop", "-connection", "env:DQL_TEST_SHOP_URL"}
	c, err := plugin.Start(context.Background(), bin, "", args, hello(), irv1.PluginKind_PLUGIN_KIND_SOURCE)
	if err != nil {
		t.Fatal(err)
	}
	check(t, c)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultNamespace(t *testing.T) {
	res, err := New(Settings{Catalog: "shop", Connection: shop.URL, DefaultNamespace: "app"}).Invoke(context.Background(), &irv1.Invoke{})
	if err != nil || res.GetBundle().GetCatalogs()[0].GetDefaultNamespace() != "app" {
		t.Errorf("got %v, %v", res, err)
	}
}

// unreadable marks a context the failing read refuses.
type unreadable struct{}

func TestFailures(t *testing.T) {
	defer func() { read, build = introspect.Read, mapping.Map }()
	read = func(ctx context.Context, q introspect.Querier) (*introspect.Snapshot, error) {
		if ctx.Value(unreadable{}) != nil {
			return nil, errors.New("boom")
		}
		return introspect.Read(ctx, q)
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
		{Settings{Catalog: "c", Connection: "sqlserver://h?dial+timeout=x"}, "the connection of catalog c: "},
		{Settings{Catalog: "c", Connection: "sqlserver://sa:x@127.0.0.1:1"}, "names no database"},
		{Settings{Catalog: "c", Connection: "sqlserver://sa:x@127.0.0.1:1?database=x&dial+timeout=1"}, "connecting to catalog c"},
		{Settings{Catalog: "unreadable", Connection: shop.URL}, "reading catalog unreadable: boom"},
		{Settings{Catalog: "unmappable", Connection: shop.URL}, "sqlserver-live: catalog unmappable: boom"},
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
	if hs.GetPlugin().GetName() != "sqlserver-live" || hs.GetPlugin().GetVersion() != mapping.Version ||
		hs.GetIrVersions().GetMin() != "1.0.0" || hs.GetIrVersions().GetMaxMajor() != 1 {
		t.Errorf("handshake: %v", hs)
	}
}
