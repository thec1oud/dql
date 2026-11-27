package sqlserver

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// more is the shop bundle with a second catalog, crm, whose app schema
// holds a tags table, as the shop's does, and a deals table with an xml
// column.
func more(t *testing.T) *irv1.Bundle {
	b := shop(t)
	id := func(s string) *irv1.Identifier { return &irv1.Identifier{Stored: s} }
	native := func(raw string) *irv1.NativeType { return &irv1.NativeType{Raw: raw} }
	idCol := &irv1.Column{Name: id("id"), Type: &irv1.Type{Kind: &irv1.Type_Int64{Int64: empty}, Native: native("bigint")}}
	doc := &irv1.Column{Name: id("doc"), Type: &irv1.Type{Kind: &irv1.Type_Xml{Xml: empty}, Native: native("xml")}, Nullable: true}
	b.Catalogs = append(b.Catalogs, &irv1.Catalog{Name: id("crm"), Namespaces: []*irv1.Namespace{
		{Name: id("app"), Tables: []*irv1.Table{{Name: id("tags"), Columns: []*irv1.Column{idCol}}, {Name: id("deals"), Columns: []*irv1.Column{idCol, doc}}}},
	}})
	return b
}

func TestCatalogs(t *testing.T) {
	b := more(t)
	for _, c := range []struct{ body, want string }{
		{"SELECT id FROM app.tags", "query-ambiguous-table: q.sql:2:16: query Q: app.tags names a table in more than one catalog"},
		{"SELECT id FROM app.deals", "id:int64"},
		{"SELECT u.id FROM app.users u, app.deals d", "query-cross-catalog: q.sql:1: query Q: reads the catalogs crm and shop; a query runs on one catalog's source"},
		{"SELECT id FROM app.deals WHERE doc IN (sqlc.slice(docs))", "query-list-param: q.sql:2:32: query Q: a list cannot hold column doc's type, which OPENJSON does not read"},
	} {
		res, err := New().Invoke(context.Background(), &irv1.Invoke{Bundle: proto.Clone(b).(*irv1.Bundle),
			QueryFiles: []*irv1.QueryFile{{Path: "q.sql", Text: "-- name: Q :many\n" + c.body}}})
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		if ds := res.GetDiagnostics(); len(ds) > 0 {
			got = ds[0].GetCode() + ": " + ds[0].GetMessage()
		} else {
			got = describe(res.GetBundle().GetQueries().GetQueries()[0])
		}
		if got != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.body, got, c.want)
		}
	}
}

// TestErrors checks the error types' messages, which the plugin reads as
// diagnostics rather than printing.
func TestErrors(t *testing.T) {
	if got := (&queryError{msg: "m"}).Error(); got != "m" {
		t.Errorf("queryError = %q", got)
	}
	if got := (&syntaxError{}).Error(); got != "syntax error" {
		t.Errorf("syntaxError = %q", got)
	}
}
