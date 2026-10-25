package queries

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	_ "github.com/thec1oud/dql/proto/dql/ext/postgres/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// shop returns the running example's bundle with its shape layer (§1.3).
func shop(t *testing.T) *irv1.Bundle {
	t.Helper()
	dir := filepath.Join("..", "..", "testdata", "shop")
	b, layer := &irv1.Bundle{}, &irv1.ShapeLayer{}
	for _, f := range []struct {
		name string
		m    proto.Message
	}{{"catalog.json", b}, {"shapes.json", layer}} {
		raw, err := os.ReadFile(filepath.Join(dir, f.name))
		if err != nil {
			t.Fatal(err)
		}
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, f.m); err != nil {
			t.Fatal(err)
		}
	}
	b.Shapes = layer
	return b
}

func users() *irv1.QualifiedName {
	return &irv1.QualifiedName{Catalog: "shop", Namespace: "app", Object: "users"}
}

// field returns a result field reading users.<name> unchanged.
func field(t *testing.T, b *irv1.Bundle, name string) *irv1.ResultField {
	t.Helper()
	for _, tb := range b.GetCatalogs()[0].GetNamespaces()[0].GetTables() {
		for _, c := range tb.GetColumns() {
			if tb.GetName().GetStored() == "users" && c.GetName().GetStored() == name {
				o := users()
				o.Member = name
				return &irv1.ResultField{Name: name, Type: c.GetType(), Nullable: c.GetNullable(), Origin: o}
			}
		}
	}
	t.Fatalf("no column %s", name)
	return nil
}

// query returns a query of users, filtered on its scope, returning fields.
func query(name string, fields ...*irv1.ResultField) *irv1.Query {
	return &irv1.Query{
		Name:        &irv1.Identifier{Stored: name},
		Cardinality: irv1.Cardinality_CARDINALITY_MANY,
		Tables:      []*irv1.QualifiedName{users()},
		Scope:       &irv1.ScopeUse{Columns: []string{"tenant_id"}},
		Result:      &irv1.ResultShape{Kind: &irv1.ResultShape_Anonymous{Anonymous: &irv1.AnonymousShape{Fields: fields}}},
	}
}

func check(b *irv1.Bundle, qs ...*irv1.Query) string {
	b.Queries = &irv1.QueryLayer{Queries: qs}
	var out []string
	for _, d := range Check(b) {
		out = append(out, d.GetQuery()+" "+d.GetCode()+": "+d.GetMessage())
	}
	return strings.Join(out, "\n")
}

var full = []string{"id", "tenant_id", "email", "display_name", "created_at", "updated_at", "version"}

func fields(t *testing.T, b *irv1.Bundle, names ...string) []*irv1.ResultField {
	var out []*irv1.ResultField
	for _, n := range names {
		out = append(out, field(t, b, n))
	}
	return out
}

// GetUserByEmail of §17.3 returns user.full: its result becomes the shape,
// with the columns in the order the query selects them.
func TestReturns(t *testing.T) {
	b := shop(t)
	order := []string{"version", "id", "tenant_id", "email", "display_name", "created_at", "updated_at"}
	q := query("GetUserByEmail", fields(t, b, order...)...)
	q.Returns = "user.full"
	if got := check(b, q); got != "" {
		t.Fatal(got)
	}
	if id := q.GetResult().GetShape(); id.GetPreset() != irv1.Preset_PRESET_FULL || id.GetForm() != irv1.Form_FORM_FLAT ||
		strings.Join(q.GetResult().GetColumns(), " ") != strings.Join(order, " ") {
		t.Errorf("result = %v", q.GetResult())
	}
}

func TestReturnsErrors(t *testing.T) {
	b := shop(t)
	unknown := query("Unknown", fields(t, b, full...)...)
	unknown.Returns = "user.nope"
	wrong := query("Wrong", fields(t, b, full[1:]...)...)
	wrong.Returns = "user.full"
	wrong.GetResult().GetAnonymous().Fields = append(wrong.GetResult().GetAnonymous().GetFields(), field(t, b, "password_hash"))
	wrong.GetResult().GetAnonymous().GetFields()[0].Type = &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}}
	nullable := query("Nullable", fields(t, b, full...)...)
	nullable.Returns = "user.full"
	nullable.GetResult().GetAnonymous().GetFields()[2].Nullable = true
	got := check(b, unknown, wrong, nullable)
	want := []string{
		"Wrong query-returns-extra: query Wrong returns user.full, which has no field password_hash; leave the column out",
		"Wrong query-returns-missing: query Wrong returns user.full but selects no column id; select it",
		"Nullable query-returns-nullable: query Nullable returns user.full, whose field email is non-null, but the column may be null; prove it or narrow it with -- nonnull: email",
		"Wrong query-returns-type: query Wrong returns user.full, whose field tenant_id has another type than the column the query selects; cast it",
		"Unknown query-returns-unknown: query Unknown returns user.nope, which is not a shape; name a preset such as user.full or a custom shape",
		"Wrong query-write-only: query Wrong selects shop.app.users.password_hash, which is write-only; leave it out of the result",
	}
	if got != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", got, strings.Join(want, "\n"))
	}
	if nullable.GetResult().GetAnonymous() == nil {
		t.Error("a failed returns replaced the result")
	}
}

func TestScope(t *testing.T) {
	b := shop(t)
	all := query("AllUsers", field(t, b, "id"))
	all.Scope = nil
	exempt := query("Exempt", field(t, b, "id"))
	exempt.Scope = &irv1.ScopeUse{Unscoped: true}
	stray := query("Stray", field(t, b, "id"))
	stray.Scope.Columns = append(stray.Scope.Columns, "region")
	got := check(b, all, exempt, stray, query("Fine", field(t, b, "id")))
	want := []string{
		"Stray query-scope-unknown: query Stray filters on @scope.region, but no table it reads is scoped by region; use a bound column",
		"AllUsers query-unscoped: query reads shop.app.users, which is scoped by tenant_id, without filtering on @scope.tenant_id; filter on it or mark the query -- unscoped",
	}
	if got != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", got, strings.Join(want, "\n"))
	}

	// A table scoped by two columns names both.
	for _, tb := range b.GetCatalogs()[0].GetNamespaces()[0].GetTables() {
		for _, c := range tb.GetColumns() {
			if tb.GetName().GetStored() == "users" && c.GetName().GetStored() == "email" {
				c.Annotations = append(c.Annotations, &irv1.Annotation{Key: keyBound})
			}
		}
	}
	all.Scope = nil
	if got := check(b, all); got != "AllUsers query-unscoped: query reads shop.app.users, which is scoped by tenant_id and email, without filtering on @scope.tenant_id and @scope.email; filter on them or mark the query -- unscoped" {
		t.Errorf("two columns: %s", got)
	}
}

func TestCount(t *testing.T) {
	b := shop(t)
	n := func(name string, t *irv1.Type) *irv1.ResultField { return &irv1.ResultField{Name: name, Type: t} }
	i64 := &irv1.Type{Kind: &irv1.Type_Int64{Int64: &irv1.Empty{}}}
	var qs []*irv1.Query
	for name, fs := range map[string][]*irv1.ResultField{
		"Count":   {n("count", i64)},
		"Count16": {n("count", &irv1.Type{Kind: &irv1.Type_Int16{Int16: &irv1.Empty{}}})},
		"Count32": {n("count", &irv1.Type{Kind: &irv1.Type_Int32{Int32: &irv1.Empty{}}})},
		"Two":     {n("a", i64), n("b", i64)},
		"Text":    {n("a", &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}})},
	} {
		q := query(name, fs...)
		q.Cardinality = irv1.Cardinality_CARDINALITY_COUNT
		qs = append(qs, q)
	}
	want := []string{
		"Text query-count-shape: query Text is :count, so it returns one integer column, such as count(*)",
		"Two query-count-shape: query Two is :count, so it returns one integer column, such as count(*)",
	}
	if got := check(b, qs...); got != strings.Join(want, "\n") {
		t.Errorf("got\n%s", got)
	}
}
