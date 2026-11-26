package mapping

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/sqlitelive/internal/introspect"
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

// table returns s's table name.
func table(s *introspect.Snapshot, name string) *introspect.Table {
	for i := range s.Tables {
		if s.Tables[i].Name == name {
			return &s.Tables[i]
		}
	}
	panic(name)
}

// The catalogs of the shop and the fixture match their golden files.
func TestMap(t *testing.T) {
	for _, name := range []string{"shop.json", "fixture.json"} {
		t.Run(name, func(t *testing.T) {
			c, diags, err := Map(snapshot(t, name), Options{Catalog: "shop"})
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
		{"table", func(s *introspect.Snapshot) { table(s, "loose").SQL = "CREATE TABLE" }, "mapping: table loose: "},
		{"view", func(s *introspect.Snapshot) { table(s, "recent").SQL = "CREATE VIEW" }, "mapping: view recent: "},
		{"index", func(s *introspect.Snapshot) { table(s, "refs").Indexes[0].SQL = "CREATE INDEX" }, "mapping: index refs_live of refs: "},
		// Proto strings must be valid UTF-8, so a name that is not fails the
		// catalog's fingerprint, and a comment that is not fails its extension.
		{"fingerprint", func(s *introspect.Snapshot) { table(s, "loose").Columns[1].Name = "\xff" }, "mapping: fingerprinting catalog shop"},
		{"extension", func(s *introspect.Snapshot) { table(s, "refs").Triggers[0].Name = "\xff" }, "mapping: string field contains invalid UTF-8"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := snapshot(t, "fixture.json")
			c.edit(s)
			_, _, err := Map(s, Options{Catalog: "shop"})
			if err == nil || !strings.HasPrefix(err.Error(), c.want) {
				t.Errorf("got %v, want %s...", err, c.want)
			}
		})
	}
}
