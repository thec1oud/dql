package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thec1oud/dql/cmd/dql/internal/config"
)

// goldenShopdb is the go emitter's golden output for the running example.
var goldenShopdb = filepath.Join("..", "..", "emitters", "go", "testdata", "shopdb")

// sameFiles fails unless dir holds exactly the files of want, byte for byte.
func sameFiles(t *testing.T, dir, want string) {
	t.Helper()
	names := func(d string) []string {
		entries, err := os.ReadDir(d)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range entries {
			out = append(out, e.Name())
		}
		return out
	}
	got, exp := names(dir), names(want)
	if !slices.Equal(got, exp) {
		t.Fatalf("files %q, want %q", got, exp)
	}
	for _, n := range got {
		a, _ := os.ReadFile(filepath.Join(dir, n))
		b, _ := os.ReadFile(filepath.Join(want, n))
		if string(a) != string(b) {
			t.Errorf("%s differs from %s", n, filepath.Join(want, n))
		}
	}
}

// dql generate runs the shipped go emitter in-process on the running
// example, writing the golden shopdb package to out relative to dql.yaml
// (§1.3, §15).
func TestGenerateEmitsGo(t *testing.T) {
	cfg := shopConfig(t, "")
	if code, _, stderr := dql("generate", "-config", cfg); code != 0 {
		t.Fatalf("%d %q", code, stderr)
	}
	sameFiles(t, filepath.Join(filepath.Dir(cfg), "internal", "shopdb"), goldenShopdb)
}

// The shipped emitter's executable, named by path:, takes its entry as flags
// and writes the same package.
func TestGenerateEmitsGoAsExecutable(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "dql-plugin-go")
	if msg, err := exec.Command("go", "build", "-o", bin, "github.com/thec1oud/dql/emitters/go/cmd/dql-plugin-go").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, msg)
	}
	t.Setenv("SHOP_DATABASE_URL", shopURL)
	raw, _ := os.ReadFile(filepath.Join(shopDir, "dql.yaml"))
	cfg := write(t, "dql.yaml", strings.Replace(string(raw), "  - plugin: go\n", "  - plugin: go\n    path: "+bin+"\n", 1))
	if code, _, stderr := dql("generate", "-config", cfg); code != 0 {
		t.Fatalf("%d %q", code, stderr)
	}
	sameFiles(t, filepath.Join(filepath.Dir(cfg), "internal", "shopdb"), goldenShopdb)
}

func TestGenerateEmitterFailures(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("SHOP_DATABASE_URL", extrasURL)
	echoEmitter := write(t, "dql-plugin-noop", "#!/bin/sh\nexec "+echoBin+" -kind emitter \"$@\"\n")
	os.Chmod(echoEmitter, 0o755)
	raw, _ := os.ReadFile(filepath.Join("testdata", "extras.yaml"))
	extras, _, _ := strings.Cut(string(raw), "emit:\n")
	for _, c := range []struct{ name, emit, want string }{
		{"emitter not found", "  - plugin: kotlin\n    dialect: postgres\n",
			"dql generate: emit[0] (kotlin): plugin kotlin is not built in and dql-plugin-kotlin is not on PATH"},
		{"emitter refused", "  - plugin: noop\n    path: " + echoEmitter + "\n    dialect: postgres\n",
			"dql generate: emit[0] (noop): plugin echo lacks required features: opaque-types"},
		{"emitter errors", "  - plugin: go\n    dialect: mysql\n    package: shopdb\n    out: gen\n",
			"dql generate: emit[0] (go): go: the emit entry targets dialect mysql, and the bundle's catalogs are postgres"},
	} {
		code, _, stderr := dql("generate", "-config", write(t, "dql.yaml", extras+"opaque-fallback: string\nemit:\n"+c.emit))
		if code != 1 || !strings.Contains(stderr, c.want) {
			t.Errorf("%s: %d %q; want %q", c.name, code, stderr, c.want)
		}
	}

	// Another emitter maps citext, so the core accepts it, and the go
	// emitter reports the domain over it that its own types leave unmapped.
	t.Setenv("SHOP_DATABASE_URL", opaqueURL)
	emit := "emit:\n  - plugin: go\n    dialect: postgres\n    package: shopdb\n    out: gen\n" +
		"  - plugin: kotlin\n    path: " + echoEmitter + "\n    dialect: postgres\n    types:\n      public.citext: { kotlin: String }\n"
	code, _, stderr := dql("generate", "-config", write(t, "dql.yaml", extras+emit))
	want := "error go-opaque-unmapped: domain shop.app.handle has opaque type public.citext, which the go emitter's types setting does not map"
	if code != 1 || !strings.Contains(stderr, want) || !strings.Contains(stderr, "emit[1] (kotlin)") {
		t.Errorf("emitter diagnostics: %d %q; want %q", code, stderr, want)
	}
}

// A shipped emitter's executable takes its emit entry as flags, out
// absolute, and each type mapped for that emitter (ADR 0003).
func TestEmitFlags(t *testing.T) {
	c, err := config.Parse([]byte(""))
	if err != nil {
		t.Fatal(err)
	}
	got := emitFlags(c, config.Emit{Plugin: "go", Dialect: "postgres", Package: "shopdb", Out: "/abs/gen",
		Types: map[string]map[string]any{"public.vector": {"go": "[]float32"}, "public.citext": {"go": "string"}, "public.x": {"ts": "string"}}})
	want := []string{"-dialect", "postgres", "-package", "shopdb", "-out", "/abs/gen", "-type", "public.citext=string", "-type", "public.vector=[]float32"}
	if !slices.Equal(got, want) {
		t.Errorf("flags = %q", got)
	}
	if got := emitFlags(c, config.Emit{Plugin: "go"}); len(got) != 0 {
		t.Errorf("flags = %q", got)
	}
}
