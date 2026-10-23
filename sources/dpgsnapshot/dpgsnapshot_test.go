package dpgsnapshot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thec1oud/dql/core/bundle"
	"github.com/thec1oud/dql/plugin"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/dpgsnapshot/internal/format"
	"github.com/thec1oud/dql/sources/dpgsnapshot/internal/mapping"
)

// bin is the plugin built as an executable.
var bin string

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	dir, err := os.MkdirTemp("", "dql-plugin-dpg-snapshot")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	bin = filepath.Join(dir, "dql-plugin-dpg-snapshot")
	build := exec.Command("go", "build", "-o", bin, "github.com/thec1oud/dql/sources/dpgsnapshot/cmd/dql-plugin-dpg-snapshot")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: building the plugin: %v\n", err)
		return 1
	}
	return m.Run()
}

// shop is the snapshot dpg recorded of the running example (§1.3).
var shop = filepath.Join("internal", "format", "testdata", "shop.json")

var shopSettings = Settings{Snapshot: shop, Namespaces: []string{"app"}, DefaultNamespace: "app"}

func hello() *irv1.CoreHello { return &irv1.CoreHello{IrVersion: bundle.Version()} }

// check invokes c and compares the catalog with the mapping's golden shop
// catalog, which the same snapshot and settings produce.
func check(t *testing.T, c plugin.Client) {
	t.Helper()
	res, err := c.Invoke(context.Background(), &irv1.Bundle{Version: bundle.Version()})
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
	if string(got) != string(want) {
		t.Error("the plugin's catalog differs from the mapping's shop golden")
	}
}

func TestInProcess(t *testing.T) {
	c, err := plugin.InProcess(New(shopSettings), hello(), irv1.PluginKind_PLUGIN_KIND_SOURCE)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	check(t, c)
}

func TestExecutable(t *testing.T) {
	args := []string{"-snapshot", shop, "-namespaces", "app", "-default-namespace", "app"}
	c, err := plugin.Start(context.Background(), bin, args, hello(), irv1.PluginKind_PLUGIN_KIND_SOURCE)
	if err != nil {
		t.Fatal(err)
	}
	check(t, c)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestDefaultNamespace reads only the default namespace when none is listed,
// and names the catalog after the snapshot's database unless told otherwise.
// shop has nothing in public.
func TestDefaultNamespace(t *testing.T) {
	res, err := New(Settings{Snapshot: shop}).Invoke(context.Background(), &irv1.Invoke{})
	if err != nil {
		t.Fatal(err)
	}
	c := res.GetBundle().GetCatalogs()[0]
	if c.GetName().GetStored() != "shop" || c.GetDefaultNamespace() != "public" || len(c.GetNamespaces()) != 0 {
		t.Errorf("catalog: %v", c)
	}
	res, err = New(Settings{Snapshot: shop, Catalog: "store", Namespaces: []string{"app"}}).Invoke(context.Background(), &irv1.Invoke{})
	if err != nil {
		t.Fatal(err)
	}
	if c := res.GetBundle().GetCatalogs()[0]; c.GetName().GetStored() != "store" || len(c.GetNamespaces()) != 1 {
		t.Errorf("catalog: %v", c)
	}
}

func TestFailures(t *testing.T) {
	defer func() { build = mapping.Map }()
	build = func(s *format.Snapshot, o mapping.Options) (*irv1.Catalog, []*irv1.Diagnostic, error) {
		if o.DefaultNamespace == "unmappable" {
			return nil, nil, errors.New("boom")
		}
		return mapping.Map(s, o)
	}
	for _, c := range []struct {
		s    Settings
		want string
	}{
		{Settings{}, "no snapshot file"},
		{Settings{Snapshot: "missing.json"}, "dpg-snapshot: dpg snapshot: open missing.json"},
		{Settings{Snapshot: filepath.Join("internal", "format", "testdata", "wrong-version.json")}, "is not supported"},
		{Settings{Snapshot: shop, DefaultNamespace: "unmappable"}, "dpg-snapshot: " + shop + ": boom"},
	} {
		_, err := New(c.s).Invoke(context.Background(), &irv1.Invoke{})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v", c.s, err)
		}
	}
}

func TestHandshake(t *testing.T) {
	hs := New(Settings{}).Handshake()
	if hs.GetPlugin().GetName() != "dpg-snapshot" || hs.GetPlugin().GetVersion() != mapping.Version ||
		hs.GetIrVersions().GetMin() != "1.0.0" || hs.GetIrVersions().GetMaxMajor() != 1 {
		t.Errorf("handshake: %v", hs)
	}
}
