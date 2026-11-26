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
	"github.com/thec1oud/dql/sources/sqlserverlive/internal/introspect"
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

// table returns the table schema.name of s.
func table(s *introspect.Snapshot, schema, name string) *introspect.Table {
	for i := range s.Schemas {
		for j := range s.Schemas[i].Tables {
			if t := &s.Schemas[i].Tables[j]; s.Schemas[i].Name == schema && t.Name == name {
				return t
			}
		}
	}
	panic(schema + "." + name)
}

// routine returns the routine schema.name of s.
func routine(s *introspect.Snapshot, schema, name string) *introspect.Routine {
	for i := range s.Schemas {
		for j := range s.Schemas[i].Routines {
			if r := &s.Schemas[i].Routines[j]; s.Schemas[i].Name == schema && r.Name == name {
				return r
			}
		}
	}
	panic(schema + "." + name)
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

func TestDefaultNamespace(t *testing.T) {
	c, _, err := Map(snapshot(t, "shop.json"), Options{Catalog: "shop", DefaultNamespace: "app"})
	if err != nil || c.DefaultNamespace != "app" {
		t.Errorf("got %v, %v", c.GetDefaultNamespace(), err)
	}
}

func TestDiagnostics(t *testing.T) {
	s := snapshot(t, "fixture.json")
	table(s, "billing", "invoices").Columns[1].Comment = "@name(bill"
	routine(s, "billing", "invoice_total").Comment = "@name(x)"
	_, diags, err := Map(s, Options{Catalog: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range diags {
		got = append(got, d.Code+": "+d.Message)
	}
	want := []string{
		"sqlserverlive-annotation-ignored: the MS_Description of function billing.invoice_total: the routine is not exposed, so sqlserver-live reads no annotations on it, so @name is ignored; remove the annotation tokens from the description",
		"sqlserverlive-annotation-ignored: the MS_Description of index payments_by_day of billing.payments: sqlserver-live reads no annotations on keys and indexes, so @unique is ignored; remove the annotation tokens from the description",
		"sqlserverlive-annotation-unclosed: the MS_Description of column code of table billing.invoices: the value of @name has no closing parenthesis, so it is not read; close it",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestErrors(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(s *introspect.Snapshot)
		want string
	}{
		{"check", func(s *introspect.Snapshot) { table(s, "billing", "invoices").Checks[0].Definition = "([amount]>=" },
			"mapping: check constraint invoices_amount_check of billing.invoices: "},
		{"filter", func(s *introspect.Snapshot) {
			x := &table(s, "billing", "payments").Indexes[0]
			x.Unique, x.Filter = true, "([amount]>"
		}, "mapping: the filter of index payments_by_day of billing.payments: "},
		// Proto strings must be valid UTF-8, so a name that is not fails the
		// catalog's fingerprint, and a comment that is not fails its extension.
		{"fingerprint", func(s *introspect.Snapshot) { table(s, "billing", "invoices").Columns[1].Name = "\xff" }, "mapping: fingerprinting catalog shop"},
		{"extension", func(s *introspect.Snapshot) { table(s, "billing", "payments").Triggers[0].Name = "\xff" }, "mapping: string field contains invalid UTF-8"},
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
