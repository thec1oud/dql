package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thec1oud/dql/testkit/golden"
)

var (
	// conformanceSchemas holds the conformance scenarios' Postgres schemas.
	conformanceSchemas = filepath.Join("..", "..", "conformance", "schemas", "postgres")
	// goldenVariantdb is the go emitter's golden output for the conformance
	// variants of the running example, which the conformance harness runs.
	goldenVariantdb = filepath.Join("..", "..", "conformance", "harness", "go", "testdata", "variantdb")
	// goldenVariantdbTS is the ts emitter's, which the TypeScript harness
	// runs.
	goldenVariantdbTS = filepath.Join("..", "..", "conformance", "harness", "ts", "variantdb")
)

// dql generate writes the conformance harnesses' variantdb packages from the
// running example with the design record's variants of it: soft delete
// (§21.3), non-null array elements (§8.6) and custom read shapes (§14.8).
func TestGenerateConformanceVariants(t *testing.T) {
	t.Setenv("SHOP_DATABASE_URL", variantsURL)
	raw, err := os.ReadFile(filepath.Join(conformanceSchemas, "variants.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := write(t, "dql.yaml", string(raw))
	// shop's partial index on published slugs says nothing of deleted_at:
	// the record's own soft-delete-unique example, a warning (§21.3).
	const warning = "warning soft-delete-unique shop.app.posts.posts_one_published_slug: unique index on a soft-delete table does not exclude deleted rows; a deleted row's values cannot be reused\n"
	if code, _, stderr := dql("generate", "-config", cfg); code != 0 || stderr != warning {
		t.Fatalf("%d %q", code, stderr)
	}
	for _, pkg := range []struct{ out, glob, golden string }{
		{"variantdb", "*.dql.go", goldenVariantdb},
		{filepath.Join("ts", "variantdb"), "*.dql.ts", goldenVariantdbTS},
	} {
		out := filepath.Join(filepath.Dir(cfg), pkg.out)
		files, err := filepath.Glob(filepath.Join(out, pkg.glob))
		if err != nil {
			t.Fatal(err)
		}
		want, _ := filepath.Glob(filepath.Join(pkg.golden, pkg.glob))
		if os.Getenv(golden.UpdateEnv) != "1" && len(want) != len(files) {
			t.Errorf("%s: %d files, golden has %d", pkg.out, len(files), len(want))
		}
		for _, f := range files {
			got, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			golden.Assert(t, filepath.Join(pkg.golden, filepath.Base(f)), got, serverText...)
		}
	}
}
