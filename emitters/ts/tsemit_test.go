package tsemit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/thec1oud/dql/emitters/ts/internal/gen"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func entry(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestHandshake(t *testing.T) {
	hs := New("", Settings{}).Handshake()
	if hs.GetPlugin().GetName() != "ts" || hs.GetPlugin().GetKind() != irv1.PluginKind_PLUGIN_KIND_EMITTER ||
		hs.GetIrVersions().GetMin() != "1.0.0" || hs.GetIrVersions().GetMaxMajor() != 1 ||
		!slices.Equal(hs.GetFeatures(), []string{"cross-catalog", "opaque-types", "weak-types"}) {
		t.Errorf("handshake %v", hs)
	}
}

func TestParseSettings(t *testing.T) {
	s, err := ParseSettings(entry(t, map[string]any{
		"plugin": "ts", "path": "/bin/x", "dialect": "postgres", "out": "src/shopdb",
		"types": map[string]any{"public.citext": map[string]any{"ts": "string"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if s.Dialect != "postgres" || s.Out != "src/shopdb" || s.Types["public.citext"] != "string" {
		t.Errorf("settings %+v", s)
	}
	for _, c := range []struct {
		m    map[string]any
		want string
	}{
		{map[string]any{"package": "shopdb"}, "unknown key package; the ts emitter reads dialect, out, path, plugin, types"},
		{map[string]any{"out": 7.0}, "out must be a string"},
		{map[string]any{"types": "x"}, "types must be a mapping"},
		{map[string]any{"types": map[string]any{"public.a": "string"}}, "types.public.a must be { ts: <type> }"},
		{map[string]any{"types": map[string]any{"public.a": map[string]any{"go": "string"}}}, "types.public.a must be { ts: <type> }"},
		{map[string]any{"types": map[string]any{"public.a": map[string]any{"ts": "string", "go": "x"}}}, "types.public.a must be { ts: <type> }"},
	} {
		if _, err := ParseSettings(entry(t, c.m)); err == nil || !strings.Contains(err.Error(), c.want) || !strings.HasPrefix(err.Error(), "ts: emit entry: ") {
			t.Errorf("%v: %v", c.m, err)
		}
	}
}

func TestSettingsCheck(t *testing.T) {
	b := &irv1.Bundle{Dialect: irv1.Dialect_DIALECT_POSTGRES}
	ok := Settings{Dialect: "postgres", Out: "x", Types: map[string]string{
		"public.a": "string", "public.b": "Uint8Array", "public.c": "pgvector#Vector", "public.d": "@scope/pkg/sub#Name_1",
	}}
	if err := ok.check(b); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		s    Settings
		want string
	}{
		{Settings{Dialect: "postgres"}, "no out"},
		{Settings{Dialect: "postgres", Out: "x", Types: map[string]string{"citext": "string"}}, "types.citext must name a native type"},
		{Settings{Dialect: "postgres", Out: "x", Types: map[string]string{"public.a": "Map<string, number>"}}, `types.public.a.ts is "Map<string, number>", which is neither one of Uint8Array, bigint, boolean, number, string, unknown nor module#Name`},
		{Settings{Dialect: "postgres", Out: "x", Types: map[string]string{"public.a": "pkg#"}}, `types.public.a.ts is "pkg#"`},
		{Settings{Dialect: "postgres", Out: "x", Types: map[string]string{"public.a": "pkg#1x"}}, `types.public.a.ts is "pkg#1x"`},
		{Settings{Dialect: "postgres", Out: "x", Types: map[string]string{"public.a": "#X"}}, `types.public.a.ts is "#X"`},
		{Settings{Dialect: "mysql", Out: "x"}, "targets dialect mysql, and the bundle's catalogs are postgres"},
		{Settings{Out: "x"}, "targets dialect , and"},
	} {
		if err := c.s.check(b); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v", c.s, err)
		}
	}
}

func TestFlagsReplaceTheEntry(t *testing.T) {
	s := Settings{Dialect: "postgres", Out: "x", Types: map[string]string{"public.a": "string"}}
	got := s.with(Settings{Dialect: "mysql", Out: "y", Types: map[string]string{"public.b": "Uint8Array"}})
	if got.Dialect != "mysql" || got.Out != "y" || len(got.Types) != 1 || got.Types["public.b"] != "Uint8Array" {
		t.Errorf("with: %+v", got)
	}
	if got := s.with(Settings{}); got.Dialect != "postgres" || got.Out != "x" || got.Types["public.a"] != "string" {
		t.Errorf("no flags: %+v", got)
	}
}

// A relative out resolves against the plugin's directory, earlier generated
// files that are no longer generated go, and hand-written files stay
// (§26.5).
func TestInvokeWrites(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "src", "shopdb")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"old.dql.ts":       gen.Header + "\n",
		"mine.dql.ts":      "export {};\n",
		"helpers.ts":       gen.Header + "\n",
		"dql-types.dql.ts": "stale",
		"notes.dql.ts.x":   "x",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(out, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res, err := New(dir, Settings{}).Invoke(context.Background(), &irv1.Invoke{Bundle: shop(t), Settings: shopSettings(t, "src/shopdb")})
	if err != nil || len(res.GetDiagnostics()) != 0 {
		t.Fatal(err, res.GetDiagnostics())
	}
	entries, _ := os.ReadDir(out)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	want, _ := filepath.Glob(filepath.Join("testdata", "shopdb", "*"+gen.Suffix))
	for i, w := range want {
		want[i] = filepath.Base(w)
	}
	want = append(want, "helpers.ts", "mine.dql.ts", "notes.dql.ts.x")
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Errorf("files %q, want %q", names, want)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "dql-types.dql.ts")); !strings.HasPrefix(string(b), gen.Header) {
		t.Errorf("dql-types.dql.ts not replaced")
	}
}

func TestInvokeFailures(t *testing.T) {
	ctx := context.Background()
	p := New("", Settings{})
	if _, err := p.Invoke(ctx, &irv1.Invoke{Bundle: shop(t), Settings: entry(t, map[string]any{"x": "y"})}); err == nil || !strings.Contains(err.Error(), "unknown key x") {
		t.Errorf("bad entry: %v", err)
	}
	if _, err := p.Invoke(ctx, &irv1.Invoke{Bundle: shop(t), Settings: entry(t, map[string]any{"dialect": "postgres"})}); err == nil || !strings.Contains(err.Error(), "no out") {
		t.Errorf("no out: %v", err)
	}

	// Error diagnostics write nothing.
	out := filepath.Join(t.TempDir(), "gen")
	b := shop(t)
	b.Catalogs[0].Namespaces[0].Composites[0].Attributes[0].Type = &irv1.Type{}
	res, err := p.Invoke(ctx, &irv1.Invoke{Bundle: b, Settings: shopSettings(t, out)})
	if err != nil || len(res.GetDiagnostics()) != 1 || res.GetDiagnostics()[0].GetCode() != gen.CodeTypeUnknown {
		t.Fatalf("%v %v", err, res.GetDiagnostics())
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("out written: %v", err)
	}

	// Failures writing the package.
	tmp := t.TempDir()
	file := filepath.Join(tmp, "file")
	os.WriteFile(file, nil, 0o644)
	stale := filepath.Join(tmp, "stale")
	os.MkdirAll(filepath.Join(stale, "dir.dql.ts"), 0o755)
	occupied := filepath.Join(tmp, "occupied")
	os.MkdirAll(filepath.Join(occupied, "dql-types.dql.ts"), 0o755)
	old := filepath.Join(tmp, "old")
	os.MkdirAll(old, 0o755)
	os.WriteFile(filepath.Join(old, "old.dql.ts"), []byte(gen.Header+"\n"), 0o644)
	for _, c := range []struct{ out, want string }{
		{filepath.Join(file, "x"), "not a directory"},
		{filepath.Join(tmp, "a[b"), "syntax error in pattern"},
		{stale, "is a directory"},
		{occupied, "is a directory"},
		{old, "cannot remove"},
	} {
		remove = func(string) error { return errors.New("cannot remove") }
		_, err := p.Invoke(ctx, &irv1.Invoke{Bundle: shop(t), Settings: shopSettings(t, c.out)})
		remove = os.Remove
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.HasPrefix(err.Error(), "ts: ") {
			t.Errorf("%s: %v", c.out, err)
		}
	}
}
