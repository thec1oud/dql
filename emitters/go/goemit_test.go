package goemit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/thec1oud/dql/emitters/go/internal/gen"
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
	if hs.GetPlugin().GetName() != "go" || hs.GetPlugin().GetKind() != irv1.PluginKind_PLUGIN_KIND_EMITTER ||
		hs.GetIrVersions().GetMin() != "1.0.0" || hs.GetIrVersions().GetMaxMajor() != 1 ||
		!slices.Equal(hs.GetFeatures(), []string{"cross-catalog", "opaque-types", "weak-types"}) {
		t.Errorf("handshake %v", hs)
	}
}

func TestParseSettings(t *testing.T) {
	s, err := ParseSettings(entry(t, map[string]any{
		"plugin": "go", "path": "/bin/x", "dialect": "postgres", "package": "shopdb", "out": "internal/shopdb",
		"types": map[string]any{"public.citext": map[string]any{"go": "string"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if s.Dialect != "postgres" || s.Package != "shopdb" || s.Out != "internal/shopdb" || s.Types["public.citext"] != "string" {
		t.Errorf("settings %+v", s)
	}
	for _, c := range []struct {
		m    map[string]any
		want string
	}{
		{map[string]any{"color": "red"}, "unknown key color; the go emitter reads dialect, out, package, path, plugin, types"},
		{map[string]any{"package": 7.0}, "package must be a string"},
		{map[string]any{"types": "x"}, "types must be a mapping"},
		{map[string]any{"types": map[string]any{"public.a": "string"}}, "types.public.a must be { go: <type> }"},
		{map[string]any{"types": map[string]any{"public.a": map[string]any{"ts": "string"}}}, "types.public.a must be { go: <type> }"},
		{map[string]any{"types": map[string]any{"public.a": map[string]any{"go": "string", "ts": "x"}}}, "types.public.a must be { go: <type> }"},
	} {
		if _, err := ParseSettings(entry(t, c.m)); err == nil || !strings.Contains(err.Error(), c.want) || !strings.HasPrefix(err.Error(), "go: emit entry: ") {
			t.Errorf("%v: %v", c.m, err)
		}
	}
}

func TestSettingsCheck(t *testing.T) {
	b := &irv1.Bundle{Dialect: irv1.Dialect_DIALECT_POSTGRES}
	ok := Settings{Dialect: "postgres", Package: "shopdb", Out: "x", Types: map[string]string{
		"public.a": "string", "public.b": "[]byte", "public.c": "github.com/x/y.Z", "public.d": "map[string]int",
	}}
	if err := ok.check(b); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		s    Settings
		want string
	}{
		{Settings{Dialect: "postgres", Out: "x"}, `package is ""`},
		{Settings{Dialect: "postgres", Package: "type", Out: "x"}, `package is "type"`},
		{Settings{Dialect: "postgres", Package: "_", Out: "x"}, `package is "_"`},
		{Settings{Dialect: "postgres", Package: "p"}, "no out"},
		{Settings{Dialect: "postgres", Package: "p", Out: "x", Types: map[string]string{"citext": "string"}}, "types.citext must name a native type"},
		{Settings{Dialect: "postgres", Package: "p", Out: "x", Types: map[string]string{"public.a": "pgtype.Text"}}, `types.public.a.go is "pgtype.Text"`},
		{Settings{Dialect: "postgres", Package: "p", Out: "x", Types: map[string]string{"public.a": "github.com/x"}}, `types.public.a.go is "github.com/x"`},
		{Settings{Dialect: "postgres", Package: "p", Out: "x", Types: map[string]string{"public.a": "github.com/x/y.z"}}, `types.public.a.go is`},
		{Settings{Dialect: "postgres", Package: "p", Out: "x", Types: map[string]string{"public.a": "[[]"}}, `types.public.a.go is`},
		{Settings{Dialect: "mysql", Package: "p", Out: "x"}, "targets dialect mysql, and the bundle's catalogs are postgres"},
		{Settings{Package: "p", Out: "x"}, "targets dialect , and"},
	} {
		if err := c.s.check(b); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v", c.s, err)
		}
	}
}

func TestFlagsReplaceTheEntry(t *testing.T) {
	s := Settings{Dialect: "postgres", Package: "a", Out: "x", Types: map[string]string{"public.a": "string"}}
	got := s.with(Settings{Package: "b", Types: map[string]string{"public.b": "[]byte"}})
	if got.Dialect != "postgres" || got.Package != "b" || got.Out != "x" || len(got.Types) != 1 || got.Types["public.b"] != "[]byte" {
		t.Errorf("with: %+v", got)
	}
	if got := s.with(Settings{}); got.Package != "a" || got.Types["public.a"] != "string" {
		t.Errorf("no flags: %+v", got)
	}
}

// A relative out resolves against the plugin's directory, earlier generated
// files that are no longer generated go, and hand-written files stay
// (§26.5).
func TestInvokeWrites(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "internal", "shopdb")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"old.dql.go":       gen.Header + "\n\npackage shopdb\n",
		"mine.dql.go":      "package shopdb\n",
		"helpers.go":       gen.Header + "\n\npackage shopdb\n",
		"dql-types.dql.go": "stale",
		"notes.dql.go.x":   "x",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(out, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res, err := New(dir, Settings{}).Invoke(context.Background(), &irv1.Invoke{Bundle: shop(t), Settings: shopSettings(t, "internal/shopdb")})
	if err != nil || len(res.GetDiagnostics()) != 0 {
		t.Fatal(err, res.GetDiagnostics())
	}
	entries, _ := os.ReadDir(out)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if want := []string{"dql-checks.dql.go", "dql-types.dql.go", "dql-version.dql.go", "helpers.go", "mine.dql.go", "notes.dql.go.x",
		"post.dql.go", "post_tag.dql.go", "profile.dql.go", "published_post.dql.go", "tag.dql.go", "tenant.dql.go", "user.dql.go"}; !slices.Equal(names, want) {
		t.Errorf("files %q", names)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "dql-types.dql.go")); !strings.HasPrefix(string(b), gen.Header) {
		t.Errorf("dql-types.dql.go not replaced")
	}
}

func TestInvokeFailures(t *testing.T) {
	ctx := context.Background()
	p := New("", Settings{})
	if _, err := p.Invoke(ctx, &irv1.Invoke{Bundle: shop(t), Settings: entry(t, map[string]any{"x": "y"})}); err == nil || !strings.Contains(err.Error(), "unknown key x") {
		t.Errorf("bad entry: %v", err)
	}
	if _, err := p.Invoke(ctx, &irv1.Invoke{Bundle: shop(t), Settings: entry(t, map[string]any{"dialect": "postgres", "out": "x"})}); err == nil || !strings.Contains(err.Error(), "package is") {
		t.Errorf("no package: %v", err)
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
	os.MkdirAll(filepath.Join(stale, "dir.dql.go"), 0o755)
	occupied := filepath.Join(tmp, "occupied")
	os.MkdirAll(filepath.Join(occupied, "dql-types.dql.go"), 0o755)
	old := filepath.Join(tmp, "old")
	os.MkdirAll(old, 0o755)
	os.WriteFile(filepath.Join(old, "old.dql.go"), []byte(gen.Header+"\n"), 0o644)
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
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.HasPrefix(err.Error(), "go: ") {
			t.Errorf("%s: %v", c.out, err)
		}
	}
}
