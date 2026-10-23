package mapping

import (
	"path/filepath"
	"testing"

	"github.com/thec1oud/dql/core/bundle"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/pglive/internal/introspect"
	"github.com/thec1oud/dql/testkit/golden"
)

var shopOptions = Options{Catalog: "shop", DefaultNamespace: "app"}

// mapped maps s, failing t on an error.
func mapped(t *testing.T, s *introspect.Snapshot, opts Options) (*irv1.Catalog, []*irv1.Diagnostic) {
	t.Helper()
	c, ds, err := Map(s, opts)
	if err != nil {
		t.Fatal(err)
	}
	return c, ds
}

// encode is the catalog's canonical JSON (§3.1).
func encode(t *testing.T, c *irv1.Catalog) []byte {
	t.Helper()
	b, err := bundle.MarshalJSON(c)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestShopGolden pins pg-live's catalog of the running example (§1.3).
func TestShopGolden(t *testing.T) {
	c, ds := mapped(t, shop, shopOptions)
	if len(ds) != 0 {
		t.Errorf("diagnostics: %v", ds)
	}
	golden.Assert(t, filepath.Join("testdata", "shop.json"), encode(t, c))
}

// TestFixtureGolden pins the catalog of the introspection fixture, which
// holds every kind of object and extension shop lacks.
func TestFixtureGolden(t *testing.T) {
	c, ds := mapped(t, fixture, Options{Catalog: "fixture"})
	if len(ds) != 0 {
		t.Errorf("diagnostics: %v", ds)
	}
	golden.Assert(t, filepath.Join("testdata", "fixture.json"), encode(t, c))
}
