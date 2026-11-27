package main

// Generation time (guide question 10): dql generate on the running example
// and on a large catalog, both read from dpg snapshots so the numbers count
// dql's own work and not a database's. Each run writes the Go and
// TypeScript packages to a temporary directory.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestBudgets holds generation to its performance budgets. It runs only
// with DQL_BUDGETS set, as the budgets CI job does: times taken under
// coverage or alongside other tests mean nothing.
func TestBudgets(t *testing.T) {
	if os.Getenv("DQL_BUDGETS") == "" {
		t.Skip("DQL_BUDGETS is not set")
	}
	for _, c := range []struct {
		name   string
		bench  func(*testing.B)
		budget time.Duration
	}{
		{"generate shop", BenchmarkGenerateShop, 500 * time.Millisecond},
		{"generate large", BenchmarkGenerateLarge, 30 * time.Second},
	} {
		r := testing.Benchmark(c.bench)
		if r.N == 0 {
			t.Errorf("%s did not run", c.name)
		} else if per := time.Duration(r.NsPerOp()); per > c.budget {
			t.Errorf("%s took %v, over its budget of %v", c.name, per, c.budget)
		} else {
			t.Logf("%s took %v of %v", c.name, per, c.budget)
		}
	}
}

// benchConfig writes a dql.yaml reading snapshot and emitting both
// packages into a temporary directory, and returns its path. A snapshot
// keeps no comments on view columns, so the nonnull of each view's key
// columns comes from config, as in testdata/dpg/dql.yaml.
func benchConfig(b *testing.B, snapshot string, views ...string) string {
	b.Helper()
	dir := b.TempDir()
	abs, err := filepath.Abs(snapshot)
	if err != nil {
		b.Fatal(err)
	}
	cfg := fmt.Sprintf(`sources:
  - plugin: dpg-snapshot
    snapshot: %s
    catalog: shop
    namespaces: [app]
    default-namespace: app
emit:
  - plugin: go
    dialect: postgres
    package: shopdb
    out: go
  - plugin: ts
    dialect: postgres
    out: ts
annotations:
`, abs)
	for _, v := range views {
		cfg += fmt.Sprintf("  shop.app.%s.id: { nonnull }\n  shop.app.%s.title: { nonnull }\n", v, v)
	}
	path := filepath.Join(dir, "dql.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		b.Fatal(err)
	}
	return path
}

// benchGenerate runs dql generate on config b.N times.
func benchGenerate(b *testing.B, config string) {
	var stderr strings.Builder
	for b.Loop() {
		stderr.Reset()
		if code := generate([]string{"-config", config}, &stderr); code != 0 {
			b.Fatalf("dql generate exited %d:\n%s", code, stderr.String())
		}
	}
}

// BenchmarkGenerateShop generates the running example.
func BenchmarkGenerateShop(b *testing.B) {
	benchGenerate(b, benchConfig(b, shopSnapshot, "published_posts"))
}

// BenchmarkGenerateLarge generates a catalog of about a thousand tables and
// views: the running example's tables and view, copied 143 times.
func BenchmarkGenerateLarge(b *testing.B) {
	path := filepath.Join(b.TempDir(), "large.json")
	const copies = 143
	if err := os.WriteFile(path, largeSnapshot(b, copies), 0o644); err != nil {
		b.Fatal(err)
	}
	views := []string{"published_posts"}
	for i := 1; i < copies; i++ {
		views = append(views, fmt.Sprintf("published_posts_%d", i))
	}
	benchGenerate(b, benchConfig(b, path, views...))
}

// largeSnapshot copies the shop snapshot's tables and views n times, each
// copy's names, the names of its constraints and its references suffixed
// with the copy's number.
func largeSnapshot(b *testing.B, n int) []byte {
	b.Helper()
	data, err := os.ReadFile(shopSnapshot)
	if err != nil {
		b.Fatal(err)
	}
	var snap map[string]any
	if err := json.Unmarshal(data, &snap); err != nil {
		b.Fatal(err)
	}
	objects := snap["objects"].(map[string]any)
	var relations []string
	for key, o := range objects {
		if kind := o.(map[string]any)["kind"]; kind == "table" || kind == "view" {
			relations = append(relations, strings.TrimPrefix(key, "app."))
		}
	}
	// A reference is app.name or, quoted inside the JSON text,
	// \"app\".\"name\".
	ref := regexp.MustCompile(`\b(app(?:\\")?\.(?:\\")?)(` + strings.Join(relations, "|") + `)\b`)
	for i := 1; i < n; i++ {
		suffix := fmt.Sprintf("_%d", i)
		for _, name := range relations {
			text, _ := json.Marshal(objects["app."+name])
			var o map[string]any
			json.Unmarshal([]byte(ref.ReplaceAllString(string(text), "${1}${2}"+suffix)), &o) //nolint:errcheck // it was JSON
			body := o[o["kind"].(string)].(map[string]any)
			body["name"] = name + suffix
			cs, _ := body["constraints"].([]any)
			for _, c := range cs {
				if c := c.(map[string]any); c["name"] != "" {
					c["name"] = c["name"].(string) + suffix
				}
			}
			objects["app."+name+suffix] = o
		}
	}
	out, err := json.Marshal(snap)
	if err != nil {
		b.Fatal(err)
	}
	return out
}
