package postgres

import (
	"context"
	"os"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	_ "github.com/thec1oud/dql/proto/dql/ext/postgres/v1"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// shop returns the running example's bundle (design record §1.3).
func shop(t *testing.T) *irv1.Bundle {
	t.Helper()
	js, err := os.ReadFile("../../testdata/shop/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	b := &irv1.Bundle{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(js, b); err != nil {
		t.Fatal(err)
	}
	return b
}

// run types text as the query file q.sql against the shop bundle.
func run(t *testing.T, text string) *irv1.InvokeResult {
	t.Helper()
	res, err := New().Invoke(context.Background(), &irv1.Invoke{Bundle: shop(t),
		QueryFiles: []*irv1.QueryFile{{Path: "q.sql", Text: text}}})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// kind names a type by its oneof field, such as int64, or array<int64>.
func kind(t *irv1.Type) string {
	if a := t.GetArray(); a != nil {
		return "array<" + kind(a.GetElement()) + ">"
	}
	if n := t.GetNamed(); n != nil {
		m := n.ProtoReflect()
		f := m.WhichOneof(m.Descriptor().Oneofs().Get(0))
		return string(f.Name()) + ":" + m.Get(f).Message().Interface().(*irv1.QualifiedName).GetObject()
	}
	m := t.ProtoReflect()
	return string(m.WhichOneof(m.Descriptor().Oneofs().Get(0)).Name())
}

// describe sums a query up: its parameters as @name:type, with ? for a
// nullable one and [] for a list; its result fields as name:type, with ?
// for nullable and ! for non-null by annotation; and w when it writes.
func describe(q *irv1.Query) string {
	var parts []string
	for _, p := range q.GetParams() {
		s := "@" + p.GetName() + ":" + kind(p.GetType())
		if p.GetNullable() {
			s += "?"
		}
		if p.GetList() {
			s += "[]"
		}
		parts = append(parts, s)
	}
	for _, c := range q.GetScope().GetColumns() {
		parts = append(parts, "@scope."+c)
	}
	for _, f := range q.GetResult().GetAnonymous().GetFields() {
		s := f.GetName() + ":" + kind(f.GetType())
		switch {
		case f.GetNullable():
			s += "?"
		case f.GetNullabilitySource() == irv1.NullabilitySource_NULLABILITY_SOURCE_ANNOTATION:
			s += "!"
		}
		parts = append(parts, s)
	}
	if q.GetWrites() {
		parts = append(parts, "w")
	}
	return strings.Join(parts, " ")
}

// typed types one query, written as the body of Q :many, or of Q :exec
// when the body starts with ":exec ", and returns its description or its
// diagnostic's code and message.
func typed(t *testing.T, body string) string {
	t.Helper()
	card := ":many"
	if b, ok := strings.CutPrefix(body, ":exec "); ok {
		card, body = ":exec", b
	}
	res := run(t, "-- name: Q "+card+"\n"+body)
	if ds := res.GetDiagnostics(); len(ds) > 0 {
		return ds[0].GetCode() + ": " + ds[0].GetMessage()
	}
	return describe(res.GetBundle().GetQueries().GetQueries()[0])
}
