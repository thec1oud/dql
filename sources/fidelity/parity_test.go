package fidelity_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/thec1oud/dql/core/bundle"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/dpgsnapshot"
	"github.com/thec1oud/dql/sources/fidelity"
	"github.com/thec1oud/dql/sources/pglive"
	"github.com/thec1oud/dql/sources/pgtest"
	"github.com/thec1oud/dql/testkit/golden"
)

// shopURL connects to a fresh database holding testdata/shop/schema.sql, the
// running example (§1.3).
var shopURL string

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()
	admin, err := pgtest.Connect(ctx)
	var db *pgtest.Database
	if err == nil {
		defer admin.Close(ctx)
		db, err = pgtest.Create(ctx, admin, filepath.Join("..", "..", "testdata", "shop", "schema.sql"))
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: setting up the test database: %v\n", err)
		return 1
	}
	defer db.Drop(ctx)
	shopURL = db.URL
	return m.Run()
}

// catalog invokes a source plugin and returns its one catalog.
func catalog(t *testing.T, p interface {
	Invoke(context.Context, *irv1.Invoke) (*irv1.InvokeResult, error)
}) *irv1.Catalog {
	t.Helper()
	res, err := p.Invoke(context.Background(), &irv1.Invoke{})
	if err != nil {
		t.Fatal(err)
	}
	return res.GetBundle().GetCatalogs()[0]
}

func encode(t *testing.T, c *irv1.Catalog) []byte {
	t.Helper()
	b, err := bundle.MarshalJSON(c)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestDPGSnapshotParity is the M1 gate (implementation guide M1): pg-live's
// catalog of the shop database and dpg-snapshot's catalog of the snapshot dpg
// recorded of the same schema are equal once the limits dpg-snapshot's
// provenance names are normalized away. The normalized catalog is pinned, so
// a change to either source shows in review.
func TestDPGSnapshotParity(t *testing.T) {
	live := catalog(t, pglive.New(pglive.Settings{Catalog: "shop", Connection: shopURL,
		Namespaces: []string{"app"}, DefaultNamespace: "app"}))
	snap := catalog(t, dpgsnapshot.New(dpgsnapshot.Settings{
		Snapshot:   filepath.Join("..", "dpgsnapshot", "internal", "format", "testdata", "shop.json"),
		Namespaces: []string{"app"}, DefaultNamespace: "app"}))
	limits := snap.GetProvenance().GetFidelityLimits()
	if !slices.Equal(limits, fidelity.DPGSnapshot) || snap.GetProvenance().GetFidelity() != irv1.SourceFidelity_SOURCE_FIDELITY_PARTIAL {
		t.Fatalf("provenance: %v", snap.GetProvenance())
	}
	removeDrift(t, live)
	want := encode(t, fidelity.Normalize(live, limits))
	got := encode(t, fidelity.Normalize(snap, limits))
	golden.Assert(t, filepath.Join("testdata", "shop.normalized.json"), got)
	if string(got) != string(want) {
		t.Errorf("the normalized catalogs differ:\n--- pg-live\n%s\n--- dpg-snapshot\n%s", want, got)
	}
}

// removeDrift removes from pg-live's catalog the two objects that
// testdata/shop/schema.sql declares and the dpg source the snapshot was
// recorded from, dpgsnapshot/internal/format/testdata/shop.dpg, does not:
// the unique index posts_one_published_slug and the comment on the foreign
// key profiles_user_id_fkey. They are differences of content, not of
// fidelity, so no limit covers them; adding both to shop.dpg and recording
// shop.json again removes them, and this test then fails until removeDrift
// goes.
func removeDrift(t *testing.T, c *irv1.Catalog) {
	t.Helper()
	found := 0
	for _, tbl := range c.Namespaces[0].Tables {
		switch tbl.Name.Stored {
		case "posts":
			n := len(tbl.Uniqueness)
			tbl.Uniqueness = slices.DeleteFunc(tbl.Uniqueness, func(u *irv1.Uniqueness) bool { return u.Name == "posts_one_published_slug" })
			found += n - len(tbl.Uniqueness)
		case "profiles":
			for _, fk := range tbl.ForeignKeys {
				if fk.Name.Stored == "profiles_user_id_fkey" && len(fk.Annotations) > 0 {
					fk.Annotations, fk.Extensions = nil, nil
					found++
				}
			}
		}
	}
	if found != 2 {
		t.Fatalf("expected the two drifted objects in pg-live's catalog, found %d", found)
	}
}
