package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thec1oud/dql/core/bundle"
	"github.com/thec1oud/dql/plugin"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/testkit/golden"
)

// queriesConfig is a queries: entry naming the running example's query
// file by its absolute path.
func queriesConfig(t *testing.T) string {
	abs, err := filepath.Abs(filepath.Join(shopDir, "queries.sql"))
	if err != nil {
		t.Fatal(err)
	}
	return "queries: [" + abs + "]\n"
}

// dql generate types the query files dql.yaml lists with the Postgres
// query-source plugin, and the bundle carries their queries (§16, §17).
func TestGenerateQueryLayer(t *testing.T) {
	code, stderr, b := generateShapes(t, queriesConfig(t))
	if code != 0 {
		t.Fatalf("%d %q", code, stderr)
	}
	var names []string
	for _, q := range b.GetQueries().GetQueries() {
		if q.GetSource() == irv1.QuerySource_QUERY_SOURCE_FILE {
			names = append(names, q.GetName().GetStored())
		}
	}
	if got := strings.Join(names, " "); got != "AuthorPostCounts GetUserByEmail RenameUser" {
		t.Errorf("queries %s", got)
	}
	// The core resolved GetUserByEmail's -- returns: user.full (§17.2).
	if r := b.GetQueries().GetQueries()[1].GetResult(); r.GetShape().GetPreset() != irv1.Preset_PRESET_FULL || len(r.GetColumns()) != 7 {
		t.Errorf("GetUserByEmail result %v", r)
	}
	// Without query files the query layer holds the builder's queries.
	if code, _, b := generateShapes(t, ""); code != 0 || b.GetQueries().GetQueries()[0].GetName().GetStored() != "post.count" {
		t.Errorf("without query files: %d %v", code, b.GetQueries())
	}
}

// withQueries copies the running example's query file next to the config
// at cfg and lists it there, so locations name it queries.sql; it returns
// cfg.
func withQueries(t *testing.T, cfg string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(shopDir, "queries.sql"))
	if err != nil {
		t.Fatal(err)
	}
	conf, _ := os.ReadFile(cfg)
	if err := os.WriteFile(filepath.Join(filepath.Dir(cfg), "queries.sql"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, append(conf, "queries: [queries.sql]\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// TestQueriesGolden pins the running example's query-file queries, as the
// core resolved and checked them, in testdata/shop/queries.json: a query
// layer without the builder's queries, for emitters' tests (guide §6.1).
func TestQueriesGolden(t *testing.T) {
	cfg := withQueries(t, shopConfig(t, ""))
	out := filepath.Join(t.TempDir(), "bundle.json")
	if code, _, stderr := dql("generate", "-config", cfg, "-bundle", out); code != 0 {
		t.Fatalf("%d %q", code, stderr)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	b := &irv1.Bundle{}
	if err := bundle.UnmarshalJSON(raw, b); err != nil {
		t.Fatal(err)
	}
	layer := &irv1.QueryLayer{}
	for _, q := range b.GetQueries().GetQueries() {
		if q.GetSource() == irv1.QuerySource_QUERY_SOURCE_FILE {
			layer.Queries = append(layer.Queries, q)
		}
	}
	js, err := bundle.MarshalJSON(layer)
	if err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, filepath.Join(shopDir, "queries.json"), js)
}

func TestGenerateQueryFailures(t *testing.T) {
	bad := write(t, "bad.sql", "-- name: Bad :one\nSELECT nope FROM app.users")
	code, stderr, b := generateShapes(t, "queries: ["+bad+"]\n")
	if code != 1 || b != nil || !strings.Contains(stderr, "/bad.sql:2:8: query Bad: column nope is not a column of the FROM clause") {
		t.Errorf("bad query: %d %q", code, stderr)
	}
	unscoped := write(t, "unscoped.sql", "-- name: AllUsers :many\nSELECT id FROM app.users")
	if code, stderr, _ := generateShapes(t, "queries: ["+unscoped+"]\n"); code != 1 || !strings.Contains(stderr, "error query-unscoped: query reads shop.app.users, which is scoped by tenant_id") {
		t.Errorf("unscoped: %d %q", code, stderr)
	}
	if code, stderr, _ := generateShapes(t, "queries: [none/*.sql]\n"); code != 1 || !strings.Contains(stderr, `dql generate: queries: "none/*.sql" matches no file`) {
		t.Errorf("no files: %d %q", code, stderr)
	}

	defer func(q map[irv1.Dialect]func() plugin.Plugin) { querySources = q }(querySources)
	for _, c := range []struct {
		name   string
		plugin plugin.Plugin
		want   string
	}{
		{"none", nil, "dql generate: no query-source plugin types DIALECT_POSTGRES query files\n"},
		{"wrong kind", fakeQuerySource{kind: irv1.PluginKind_PLUGIN_KIND_DIALECT}, "dql generate: query source fake: "},
		{"invoke fails", fakeQuerySource{kind: irv1.PluginKind_PLUGIN_KIND_QUERY_SOURCE, err: errors.New("boom")}, "dql generate: query source fake: boom\n"},
	} {
		querySources = map[irv1.Dialect]func() plugin.Plugin{}
		if c.plugin != nil {
			querySources[irv1.Dialect_DIALECT_POSTGRES] = func() plugin.Plugin { return c.plugin }
		}
		if code, stderr, b := generateShapes(t, queriesConfig(t)); code != 1 || b != nil || !strings.Contains(stderr, c.want) {
			t.Errorf("%s: %d %q", c.name, code, stderr)
		}
	}
}

type fakeQuerySource struct {
	kind irv1.PluginKind
	err  error
}

func (f fakeQuerySource) Handshake() *irv1.PluginHandshake {
	return &irv1.PluginHandshake{
		Plugin:     &irv1.PluginInfo{Name: "fake", Kind: f.kind, Version: "1.0.0"},
		IrVersions: &irv1.IrVersionRange{Min: "1.0.0", MaxMajor: 1},
	}
}

func (f fakeQuerySource) Invoke(context.Context, *irv1.Invoke) (*irv1.InvokeResult, error) {
	return nil, f.err
}
