package mapping

import (
	"path/filepath"
	"testing"

	"github.com/thec1oud/dql/core/bundle"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/dpgsnapshot/internal/format"
	"github.com/thec1oud/dql/testkit/determinism"
	"github.com/thec1oud/dql/testkit/golden"
)

var shopOptions = Options{DefaultNamespace: "app", Namespaces: []string{"app"}}

// load reads a snapshot, failing t on an error.
func load(t *testing.T, path string) *format.Snapshot {
	t.Helper()
	s, err := format.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// mapped maps s, failing t on an error.
func mapped(t *testing.T, s *format.Snapshot, opts Options) (*irv1.Catalog, []*irv1.Diagnostic) {
	t.Helper()
	c, ds, err := Map(s, opts)
	if err != nil {
		t.Fatal(err)
	}
	return c, ds
}

// encode is the canonical JSON of m (§3.1).
func encode(t *testing.T, m *irv1.Catalog) []byte {
	t.Helper()
	b, err := bundle.MarshalJSON(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// shopSnapshot is the snapshot dpg wrote of the running example (§1.3).
var shopSnapshot = filepath.Join("..", "format", "testdata", "shop.json")

// TestShopGolden pins dpg-snapshot's catalog of the running example.
func TestShopGolden(t *testing.T) {
	c, ds := mapped(t, load(t, shopSnapshot), shopOptions)
	if len(ds) != 0 {
		t.Errorf("diagnostics: %v", ds)
	}
	golden.Assert(t, filepath.Join("testdata", "shop.json"), encode(t, c))
}

var fullSnapshot = filepath.Join("testdata", "full.json")

// TestFullGolden pins the catalog of full.json, written by hand in dpg's
// format, which holds every kind of record and field the mapping reads, and
// the diagnostics it reports.
func TestFullGolden(t *testing.T) {
	c, ds := mapped(t, load(t, fullSnapshot), Options{})
	golden.Assert(t, filepath.Join("testdata", "full.catalog.json"), encode(t, c))
	b, err := bundle.MarshalJSON(&irv1.Bundle{Diagnostics: ds})
	if err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, filepath.Join("testdata", "full.diagnostics.json"), b)
}

// TestDeterminism maps each snapshot repeatedly and requires byte-identical
// output (§3.2, implementation guide §6.2). The format package returns
// objects in key order, so nothing depends on the file's own order.
func TestDeterminism(t *testing.T) {
	for _, path := range []string{shopSnapshot, fullSnapshot} {
		s := load(t, path)
		if err := determinism.Repeat(5, func() ([]byte, error) {
			c, _, err := Map(s, Options{})
			if err != nil {
				return nil, err
			}
			return bundle.MarshalJSON(c)
		}); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
}
