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
)

// dql generate writes the conformance harness's variantdb package from the
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
	out := filepath.Join(filepath.Dir(cfg), "variantdb")
	files, err := filepath.Glob(filepath.Join(out, "*.dql.go"))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.Glob(filepath.Join(goldenVariantdb, "*.dql.go"))
	if os.Getenv(golden.UpdateEnv) != "1" && len(want) != len(files) {
		t.Errorf("%d files, golden has %d", len(files), len(want))
	}
	for _, f := range files {
		got, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		golden.Assert(t, filepath.Join(goldenVariantdb, filepath.Base(f)), got, serverText...)
	}
}
