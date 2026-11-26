package sqlitelive

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
	"github.com/thec1oud/dql/sources/sqlitelive/internal/introspect"
	"github.com/thec1oud/dql/sources/sqlitelive/internal/mapping"
	"github.com/thec1oud/dql/sources/sqlitetest"
	goldens "github.com/thec1oud/dql/testkit/golden"
)

// shop is a database holding the shop, and bin is the plugin built as an
// executable.
var (
	shop *sqlitetest.Database
	bin  string
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	dir, err := os.MkdirTemp("", "dql-plugin-sqlite-live")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	bin = filepath.Join(dir, "dql-plugin-sqlite-live")
	build := exec.Command("go", "build", "-o", bin, "github.com/thec1oud/dql/sources/sqlitelive/cmd/dql-plugin-sqlite-live")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: building the plugin: %v\n", err)
		return 1
	}
	shop, err = sqlitetest.Create(context.Background(), filepath.Join("..", "..", "conformance", "schemas", "sqlite", "shop.sql"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: setting up the test database: %v\n", err)
		return 1
	}
	defer shop.Drop()
	return m.Run()
}

func hello() *irv1.CoreHello { return &irv1.CoreHello{IrVersion: bundle.Version()} }

// check invokes c and compares the catalog with the mapping's golden shop
// catalog.
func check(t *testing.T, c plugin.Client) {
	t.Helper()
	res, err := c.Invoke(context.Background(), &irv1.Invoke{Bundle: &irv1.Bundle{Version: bundle.Version()}})
	if err != nil {
		t.Fatal(err)
	}
	b := res.GetBundle()
	if b.GetDialect() != irv1.Dialect_DIALECT_SQLITE || b.GetVersion().GetMajor() != 1 || len(b.GetCatalogs()) != 1 {
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
	want, err := os.ReadFile(filepath.Join("internal", "mapping", "testdata", "shop.json"))
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := regexp.MustCompile(`"inputFingerprint": "[^"]*"`)
	if strings.TrimSpace(string(goldens.Mask(got, fingerprint))) != strings.TrimSpace(string(goldens.Mask(want, fingerprint))) {
		t.Error("the plugin's catalog differs from the mapping's shop.json")
	}
}

func TestInProcess(t *testing.T) {
	for _, conn := range []string{"env:DQL_TEST_SHOP_DB", shop.Path, "file:" + shop.Path + "?cache=private"} {
		t.Setenv("DQL_TEST_SHOP_DB", shop.Path)
		c, err := plugin.InProcess(New(Settings{Catalog: "shop", Connection: conn}), hello(), irv1.PluginKind_PLUGIN_KIND_SOURCE)
		if err != nil {
			t.Fatal(err)
		}
		check(t, c)
		c.Close()
	}
}

func TestExecutable(t *testing.T) {
	args := []string{"-catalog", "shop", "-connection", shop.Path}
	c, err := plugin.Start(context.Background(), bin, "", args, hello(), irv1.PluginKind_PLUGIN_KIND_SOURCE)
	if err != nil {
		t.Fatal(err)
	}
	check(t, c)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

// The plugin never writes: it opens the file read-only.
func TestReadOnly(t *testing.T) {
	if got := dsn("a b?c#d%e"); got != "file:a b%3fc%23d%25e?mode=ro" {
		t.Errorf("dsn: %s", got)
	}
	if got := dsn("file:x?cache=shared"); got != "file:x?cache=shared&mode=ro" {
		t.Errorf("dsn: %s", got)
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
	missing := filepath.Join(t.TempDir(), "missing.db")
	for _, c := range []struct {
		s    Settings
		want string
	}{
		{Settings{}, "no catalog name"},
		{Settings{Catalog: "c", Connection: "env:DQL_TEST_UNSET"}, "env:DQL_TEST_UNSET, which is not set"},
		{Settings{Catalog: "c"}, "names no database file"},
		{Settings{Catalog: "c", Connection: missing}, "opening catalog c"},
		{Settings{Catalog: "unreadable", Connection: shop.Path}, "reading catalog unreadable: boom"},
		{Settings{Catalog: "unmappable", Connection: shop.Path}, "sqlite-live: catalog unmappable: boom"},
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
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Errorf("a missing file was created: %v", err)
	}
}

func TestHandshake(t *testing.T) {
	hs := New(Settings{}).Handshake()
	if hs.GetPlugin().GetName() != "sqlite-live" || hs.GetPlugin().GetVersion() != mapping.Version ||
		hs.GetIrVersions().GetMin() != "1.0.0" || hs.GetIrVersions().GetMaxMajor() != 1 {
		t.Errorf("handshake: %v", hs)
	}
}
