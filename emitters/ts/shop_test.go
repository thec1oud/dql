package tsemit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/thec1oud/dql/emitters/ts/internal/gen"
	// The catalog carries Postgres extensions (§6.2).
	_ "github.com/thec1oud/dql/proto/dql/ext/postgres/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/testkit/golden"
)

// shopDir holds the running example's golden bundle (§1.3).
var shopDir = filepath.Join("..", "..", "testdata", "shop")

// tsc is the TypeScript compiler of the runtime's workspace, which
// `bun install` in runtime/ts puts there. The golden package is checked
// against the runtime's declarations, which `bun run build` writes.
var tsc = filepath.Join("..", "..", "runtime", "ts", "node_modules", ".bin", "tsc")

// shop reads the running example's bundle: its catalog layer and its shape
// layer, as the core's gate tests pin them, and its query files' queries.
func shop(t *testing.T) *irv1.Bundle {
	t.Helper()
	b := &irv1.Bundle{}
	layer := &irv1.ShapeLayer{}
	read := func(name string, into func([]byte) error) {
		raw, err := os.ReadFile(filepath.Join(shopDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := into(raw); err != nil {
			t.Fatal(err)
		}
	}
	read("catalog.json", func(raw []byte) error { return protojson.Unmarshal(raw, b) })
	read("shapes.json", func(raw []byte) error { return protojson.Unmarshal(raw, layer) })
	b.Shapes = layer
	files := &irv1.QueryLayer{}
	read("queries.json", func(raw []byte) error { return protojson.Unmarshal(raw, files) })
	b.Queries.Queries = append(b.Queries.Queries, files.GetQueries()...)
	return b
}

// shopSettings is the ts entry of the running example's dql.yaml (§1.3).
func shopSettings(t *testing.T, out string) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(map[string]any{"plugin": "ts", "dialect": "postgres", "out": out})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestShopGolden generates the running example's shopdb package, compares
// every file with testdata/shopdb, and type checks it against the runtime
// (implementation guide §4 M5, §6.1).
func TestShopGolden(t *testing.T) {
	out := t.TempDir()
	res, err := New("", Settings{}).Invoke(context.Background(), &irv1.Invoke{Bundle: shop(t), Settings: shopSettings(t, out)})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.GetDiagnostics()) != 0 {
		t.Fatalf("diagnostics: %v", res.GetDiagnostics())
	}
	files, err := filepath.Glob(filepath.Join(out, "*"))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.Glob(filepath.Join("testdata", "shopdb", "*"+gen.Suffix))
	if os.Getenv(golden.UpdateEnv) != "1" && len(want) != len(files) {
		t.Errorf("%d files, golden has %d", len(files), len(want))
	}
	for _, f := range files {
		got, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		golden.Assert(t, filepath.Join("testdata", "shopdb", filepath.Base(f)), got)
	}
	// Erasable syntax only, so Node, Bun and Deno run the modules as they are.
	// The configuration maps the runtime's packages to their builds without
	// an extension: tsc reads the declarations and Bun runs the JavaScript.
	for _, config := range []string{"tsconfig.json", "tsconfig.test.json"} {
		check := exec.Command(tsc, "-p", filepath.Join("testdata", config))
		if msg, err := check.CombinedOutput(); err != nil {
			t.Fatalf("the golden package does not type check with %s (run bun install and bun run build in runtime/ts first): %v\n%s", config, err, msg)
		}
	}
	// The package's hand-written test runs it on Postgres (§26.5).
	run := exec.Command("bun", "test")
	run.Dir = filepath.Join("testdata", "shopdb")
	if msg, err := run.CombinedOutput(); err != nil {
		t.Fatalf("the golden package's test fails: %v\n%s", err, msg)
	}
}
