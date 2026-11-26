package mapping

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/mysqllive/internal/introspect"
)

var update = flag.Bool("update", false, "rewrite the golden catalogs")

// snapshot reads one of introspect's golden snapshots.
func snapshot(t *testing.T, name string) *introspect.Snapshot {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "introspect", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var s introspect.Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return &s
}

// The catalogs of the shop and the fixture match their golden files.
func TestMap(t *testing.T) {
	for _, name := range []string{"shop.json", "fixture.json"} {
		t.Run(name, func(t *testing.T) {
			c, diags, err := Map(snapshot(t, name), Options{Catalog: "shop", Database: "DATABASE"})
			if err != nil {
				t.Fatal(err)
			}
			got, _ := protojson.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(&irv1.InvokeResult{
				Bundle: &irv1.Bundle{Catalogs: []*irv1.Catalog{c}}, Diagnostics: diags})
			// protojson varies its spacing; normalize it for the golden.
			var v any
			json.Unmarshal(got, &v)
			got, _ = json.MarshalIndent(v, "", "  ")
			got = append(got, '\n')
			path := filepath.Join("testdata", name)
			if *update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Errorf("%s differs; rerun with -update and review the diff", name)
			}
		})
	}
}

func TestErrors(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(s *introspect.Snapshot)
		want string
	}{
		{"column type", func(s *introspect.Snapshot) { s.Tables[0].Columns[0].ColumnType = "int(" }, "mapping: column children.id: type int(: unclosed parenthesis"},
		{"check", func(s *introspect.Snapshot) { s.Tables[2].Checks[0].Clause = "((" }, "mapping: check constraint kinds_membership of kinds: (( does not parse"},
		// Proto strings must be valid UTF-8, so a name that is not fails the
		// catalog's fingerprint, and a comment that is not fails its extension.
		{"fingerprint", func(s *introspect.Snapshot) { s.Tables[1].Columns[0].Name = "\xff" }, "mapping: fingerprinting catalog shop"},
		{"extension", func(s *introspect.Snapshot) { s.Tables[2].Comment = "\xff" }, "mapping: string field contains invalid UTF-8"},
		{"routine argument", func(s *introspect.Snapshot) { s.Routines[1].Parameters[0].Type = "int(" }, "mapping: argument x of function pure: type int(: unclosed parenthesis"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := snapshot(t, "fixture.json")
			c.edit(s)
			_, _, err := Map(s, Options{Catalog: "shop", Database: "DATABASE"})
			if err == nil || !strings.HasPrefix(err.Error(), c.want) {
				t.Errorf("got %v, want %s...", err, c.want)
			}
		})
	}
}

// An unclosed annotation value is an error diagnostic; a cross-database
// foreign key names the other database as its catalog (§5.3).
func TestEdges(t *testing.T) {
	s := snapshot(t, "fixture.json")
	s.Tables[0].Columns[0].Comment = "@name(oops"
	s.Tables[0].ForeignKeys[0].RefDB = "billing"
	c, diags, err := Map(s, Options{Catalog: "shop", Database: "DATABASE"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(diags, func(d *irv1.Diagnostic) bool { return d.GetCode() == CodeAnnotationUnclosed }) {
		t.Errorf("diagnostics %v", diags)
	}
	ref := c.GetNamespaces()[0].GetTables()[0].GetForeignKeys()[0].GetReferences().GetTable()
	if ref.GetCatalog() != "billing" {
		t.Errorf("reference %v", ref)
	}
}
