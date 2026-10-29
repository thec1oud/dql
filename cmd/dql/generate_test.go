package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/thec1oud/dql/cmd/dql/internal/config"
	"github.com/thec1oud/dql/core/bundle"
	"github.com/thec1oud/dql/core/catalog"
	"github.com/thec1oud/dql/plugin"
	"github.com/thec1oud/dql/plugin/echo"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/pgtest"
)

var (
	// shopURL connects to a fresh database holding testdata/shop/schema.sql,
	// the running example (§1.3).
	shopURL string
	// serverText is the text of the shop goldens the server writes
	// differently.
	serverText []*regexp.Regexp
	// extrasURL connects to one holding the running example with
	// testdata/extras.sql applied.
	extrasURL string
	// opaqueURL connects to one holding the running example with
	// testdata/opaque.sql applied.
	opaqueURL string
	// driftlessURL connects to one holding the running example without what
	// the dpg snapshot of it lacks, testdata/dpg/drift.sql.
	driftlessURL string
	// dpgBin is the dpg-snapshot source built as an executable.
	dpgBin string
	// echoBin is the echo plugin built as an executable.
	echoBin string
	// probeBin is a third-party source plugin: the echo plugin as a source,
	// which reports the settings it receives. It fails unless it runs in the
	// directory of dql.yaml.
	probeBin string
)

// The running example's files.
var (
	shopDir      = filepath.Join("..", "..", "testdata", "shop")
	shopSnapshot = filepath.Join("..", "..", "sources", "dpgsnapshot", "internal", "format", "testdata", "shop.json")
)

func TestMain(m *testing.M) {
	os.Exit(setup(m))
}

func setup(m *testing.M) int {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "dql-generate")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	dpgBin = filepath.Join(dir, "dql-plugin-dpg-snapshot")
	echoBin = filepath.Join(dir, "dql-plugin-echo")
	for bin, pkg := range map[string]string{
		dpgBin:  "github.com/thec1oud/dql/sources/dpgsnapshot/cmd/dql-plugin-dpg-snapshot",
		echoBin: "github.com/thec1oud/dql/plugin/cmd/dql-plugin-echo",
	} {
		build := exec.Command("go", "build", "-o", bin, pkg)
		build.Stderr = os.Stderr
		if err := build.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "FAIL: building %s: %v\n", filepath.Base(bin), err)
			return 1
		}
	}
	probeBin = filepath.Join(dir, "dql-plugin-probe")
	if err := os.WriteFile(probeBin, []byte("#!/bin/sh\n[ -f dql.yaml ] || { echo \"not run beside dql.yaml\" >&2; exit 3; }\nexec "+echoBin+" -kind source \"$@\"\n"), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	admin, err := pgtest.Connect(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: setting up the test databases: %v\n", err)
		return 1
	}
	defer admin.Close(ctx)
	schema := filepath.Join(shopDir, "schema.sql")
	for _, d := range []struct {
		url   *string
		files []string
	}{
		{&shopURL, []string{schema}},
		{&extrasURL, []string{schema, filepath.Join("testdata", "extras.sql")}},
		{&driftlessURL, []string{schema, filepath.Join("testdata", "dpg", "drift.sql")}},
		{&opaqueURL, []string{schema, filepath.Join("testdata", "opaque.sql")}},
	} {
		db, err := pgtest.Create(ctx, admin, d.files...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAIL: setting up the test databases: %v\n", err)
			return 1
		}
		defer db.Drop(ctx)
		*d.url = db.URL
		serverText = db.ServerText
	}
	return m.Run()
}

// snapshotConfig is a dql.yaml reading the running example's snapshot with
// the dpg-snapshot executable at path. It asserts nonnull on the view's
// columns in config, since a snapshot keeps no comments on view columns
// (fidelity.ViewColumnComments).
func snapshotConfig(catalog, path string) string {
	abs, _ := filepath.Abs(shopSnapshot)
	return fmt.Sprintf(`sources:
  - plugin: dpg-snapshot
    path: %s
    snapshot: %s
    catalog: %s
    namespaces: [app]
    default-namespace: app
annotations:
  %[3]s.app.published_posts.id: { nonnull }
  %[3]s.app.published_posts.title: { nonnull }
`, path, abs, catalog)
}

func TestGenerateWritesTheBundle(t *testing.T) {
	t.Setenv("SHOP_DATABASE_URL", shopURL)
	out := filepath.Join(t.TempDir(), "bundle.json")
	code, _, stderr := dql("generate", "-config", shopConfig(t, ""), "-bundle", out)
	want := "dql generate: wrote the bundle to " + out + "\n" +
		"dql generate: skipped emit[1] (ts): the ts emitter arrives in a later milestone\n"
	if code != 0 || stderr != want {
		t.Fatalf("%d %q", code, stderr)
	}
	b := &irv1.Bundle{}
	raw, _ := os.ReadFile(out)
	if err := bundle.UnmarshalJSON(raw, b); err != nil || len(b.GetCatalogs()) != 1 {
		t.Fatalf("bundle: %v", err)
	}
	// Without -bundle nothing is written.
	if code, _, stderr := dql("generate", "-config", shopConfig(t, "")); code != 0 || strings.Contains(stderr, "wrote") {
		t.Errorf("no -bundle: %d %q", code, stderr)
	}
}

// Two sources produce two catalogs; each config annotation names the plugin
// of the source whose catalog its key names, the longest that matches, and
// warnings are printed without failing generation.
func TestGenerateSeveralSources(t *testing.T) {
	t.Setenv("SHOP_DATABASE_URL", shopURL)
	abs, _ := filepath.Abs(shopSnapshot)
	cfg := `sources:
  - plugin: dpg-snapshot
    snapshot: ` + abs + `
    catalog: shop.eu
    namespaces: [app]
    default-namespace: app
  - plugin: pg-live
    catalog: shop
    connection: env:SHOP_DATABASE_URL
    namespaces: [app]
    default-namespace: app
default-catalog: shop
annotations:
  shop.eu.app.published_posts.id: { nonnull }
  shop.eu.app.published_posts.title: { nonnull }
  shop.app.users.email: { nonnull }
`
	out := filepath.Join(t.TempDir(), "bundle.json")
	code, _, stderr := dql("generate", "-config", write(t, "dql.yaml", cfg), "-bundle", out)
	if code != 0 || !strings.HasPrefix(stderr, "warning annotation-redundant: core.nonnull on shop.app.users.email changes nothing") {
		t.Fatalf("%d %q", code, stderr)
	}
	b := &irv1.Bundle{}
	raw, _ := os.ReadFile(out)
	if err := bundle.UnmarshalJSON(raw, b); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range b.GetCatalogs() {
		for _, tb := range c.GetNamespaces()[0].GetTables() {
			for _, col := range tb.GetColumns() {
				for _, a := range col.GetAnnotations() {
					if a.GetOrigin().GetKind() == irv1.OriginKind_ORIGIN_CONFIG {
						got = append(got, c.GetName().GetStored()+"."+tb.GetName().GetStored()+"."+col.GetName().GetStored()+" "+a.GetOrigin().GetPlugin())
					}
				}
			}
		}
	}
	want := []string{"shop.users.email pg-live", "shop.eu.published_posts.id dpg-snapshot", "shop.eu.published_posts.title dpg-snapshot"}
	if !slices.Equal(got, want) {
		t.Errorf("config origins: %v", got)
	}
}

func TestGenerateFailures(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("SHOP_DATABASE_URL", "")
	sources["echo"] = func(*config.Config, config.Source) plugin.Plugin {
		return echo.New(irv1.PluginKind_PLUGIN_KIND_SOURCE, "1.0.0", 1)
	}
	defer delete(sources, "echo")
	pgLive := "  - plugin: pg-live\n    catalog: shop\n    connection: env:SHOP_DATABASE_URL\n"
	for _, c := range []struct {
		name, config string
		want         []string
	}{
		{"invalid config", "", []string{"error config-no-sources"}},
		{"invalid annotations", "sources:\n" + pgLive + "annotations:\n  shop.app.users: user\n",
			[]string{"error config-annotations-invalid: dql.yaml line 6"}},
		{"plugin not found", "sources:\n  - plugin: mysql-live\n",
			[]string{"dql generate: sources[0] (mysql-live): plugin mysql-live is not built in and dql-plugin-mysql-live is not on PATH"}},
		{"plugin errors", "sources:\n  - plugin: mysql-live\n    path: " + dpgBin + "\n",
			[]string{"dql generate: sources[0] (mysql-live): plugin dpg-snapshot: dpg-snapshot: no snapshot file"}},
		{"source fails", "sources:\n" + pgLive + "  - plugin: dpg-snapshot\n    path: /nonexistent/dql-plugin-dpg-snapshot\n    snapshot: shop.json\n",
			[]string{"dql generate: sources[0] (pg-live): pg-live: connection names env:SHOP_DATABASE_URL, which is not set",
				"dql generate: sources[1] (dpg-snapshot): plugin: starting /nonexistent/dql-plugin-dpg-snapshot"}},
		{"assembly fails", "sources:\n  - plugin: echo\nemit:\n  - plugin: go\n    dialect: postgres\n    package: p\n    out: gen\n",
			[]string{"error catalog-none: the sources produced no catalog"}},
	} {
		code, _, stderr := dql("generate", "-config", write(t, "dql.yaml", c.config))
		for _, w := range c.want {
			if code != 1 || !strings.Contains(stderr, w) {
				t.Errorf("%s: %d %q; want %q", c.name, code, stderr, w)
			}
		}
		if strings.Contains(stderr, "skipped") {
			t.Errorf("%s: emit entries reported after a failure: %q", c.name, stderr)
		}
	}
	if code, _, _ := dql("generate", "-bogus"); code != 2 {
		t.Errorf("bad flag: %d", code)
	}
}

func TestGenerateWriteFailures(t *testing.T) {
	cfg := write(t, "dql.yaml", snapshotConfig("shop", dpgBin))
	if code, _, stderr := dql("generate", "-config", cfg, "-bundle", filepath.Join(t.TempDir(), "none", "b.json")); code != 1 ||
		!strings.Contains(stderr, "no such file or directory") {
		t.Errorf("unwritable: %d %q", code, stderr)
	}
	defer func(f func(proto.Message) ([]byte, error)) { marshalJSON = f }(marshalJSON)
	marshalJSON = func(proto.Message) ([]byte, error) { return nil, fmt.Errorf("boom") }
	if code, _, stderr := dql("generate", "-config", cfg, "-bundle", filepath.Join(t.TempDir(), "b.json")); code != 1 ||
		!strings.Contains(stderr, "dql generate: boom") {
		t.Errorf("encoding: %d %q", code, stderr)
	}
}

// A third-party source receives its whole sources entry, keys of its own
// included, with the invocation and no flags. It runs in the directory of
// dql.yaml, which its relative path: is relative to.
func TestGenerateHandsSettingsToThirdPartySources(t *testing.T) {
	dir := t.TempDir()
	rel, _ := filepath.Rel(dir, probeBin)
	cfg := "sources:\n  - plugin: probe\n    path: " + rel + "\n    catalog: shop\n    dsn: env:PROBE_URL\n    pool: {size: 4}\n"
	os.WriteFile(filepath.Join(dir, "dql.yaml"), []byte(cfg), 0o644)
	code, _, stderr := dql("generate", "-config", filepath.Join(dir, "dql.yaml"))
	want := "warning echo-settings: map[catalog:shop dsn:env:PROBE_URL path:" + rel + " plugin:probe pool:map[size:4]]\n"
	if code != 1 || !strings.Contains(stderr, want) {
		t.Errorf("%d %q; want %q", code, stderr, want)
	}
}

// A shipped plugin's executable takes the settings of its sources entry as
// flags, with the snapshot relative to dql.yaml.
func TestFlags(t *testing.T) {
	c, err := config.Parse([]byte(""))
	if err != nil {
		t.Fatal(err)
	}
	got := flags(c, config.Source{Catalog: "shop", Snapshot: "db/shop.json", Namespaces: []string{"app", "audit"}, DefaultNamespace: "app"})
	want := []string{"-catalog", "shop", "-snapshot", "db/shop.json", "-namespaces", "app,audit", "-default-namespace", "app"}
	if !slices.Equal(got, want) {
		t.Errorf("flags = %q", got)
	}
	if got := flags(c, config.Source{Connection: "env:URL"}); !slices.Equal(got, []string{"-connection", "env:URL"}) {
		t.Errorf("flags = %q", got)
	}
}

// §8.3: emitter config maps opaque types by namespace.type.
func TestOpaqueMapped(t *testing.T) {
	c, err := config.Parse([]byte(`
emit:
  - plugin: go
    types:
      public.citext: { go: string }
      public.vector: { go: "[]float32" }
  - plugin: ts
    types:
      public.citext: { ts: string }
`))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, q := range opaqueMapped(c) {
		got = append(got, q.GetNamespace()+" "+q.GetObject())
	}
	if want := []string{"public citext", "public vector", "public citext"}; !slices.Equal(got, want) {
		t.Errorf("mapped = %q", got)
	}
}

// A config key that names no produced catalog goes to the first output with
// no plugin, so Assemble reports it.
func TestAttachUnknownCatalog(t *testing.T) {
	outputs := []catalog.SourceOutput{{Catalogs: []*irv1.Catalog{{Name: &irv1.Identifier{Stored: "shop"}}}}}
	anns := map[string][]*irv1.Annotation{"billing.invoices": {{Key: "core.name", Origin: &irv1.Origin{Kind: irv1.OriginKind_ORIGIN_CONFIG}}}}
	attach(outputs, []config.Source{{Plugin: "pg-live"}}, anns)
	if a := outputs[0].Config["billing.invoices"]; len(a) != 1 || a[0].GetOrigin().GetPlugin() != "" {
		t.Errorf("config = %v", outputs[0].Config)
	}
}
