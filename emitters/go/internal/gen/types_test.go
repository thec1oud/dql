package gen

import (
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// §26.4: the database type each argument is sent as.
func TestDBType(t *testing.T) {
	native := func(ns, name string) *irv1.NativeType {
		return &irv1.NativeType{Name: &irv1.QualifiedName{Namespace: ns, Object: name}}
	}
	email := &irv1.QualifiedName{Catalog: "shop", Namespace: "app", Object: "email"}
	domain := &irv1.Type{Kind: &irv1.Type_Named{Named: &irv1.NamedRef{Kind: &irv1.NamedRef_Domain{Domain: email}}}, Native: native("app", "email")}
	text := &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}, Native: native("pg_catalog", "text")}
	g := &generator{named: map[key]named{keyOf(email): {base: text}}}
	array := func(elem *irv1.Type, n *irv1.NativeType) *irv1.Type {
		return &irv1.Type{Kind: &irv1.Type_Array{Array: &irv1.ArrayType{Element: elem}}, Native: n}
	}
	for _, c := range []struct {
		t    *irv1.Type
		list bool
		want string
	}{
		{text, false, "pg_catalog.text"},
		{text, true, "pg_catalog._text"},
		{domain, false, "pg_catalog.text"},
		{domain, true, "pg_catalog._text"},
		{array(&irv1.Type{Kind: &irv1.Type_Int32{Int32: &irv1.Empty{}}}, native("pg_catalog", "_int4")), false, "pg_catalog._int4"},
		{array(domain, native("app", "_email")), false, "pg_catalog._text"},
		{array(text, native("pg_catalog", "_text")), true, ""},
		{&irv1.Type{Native: native("", "mood")}, false, "mood"},
		{&irv1.Type{Kind: &irv1.Type_Int32{Int32: &irv1.Empty{}}}, false, ""},
		{nil, false, ""},
	} {
		if got := g.dbType(c.t, c.list); got != c.want {
			t.Errorf("%v %v: got %q, want %q", c.t, c.list, got, c.want)
		}
	}
}
