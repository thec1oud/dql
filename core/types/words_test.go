package types

import (
	"slices"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Every kind of Type without a type-valued parameter has words: a new kind
// added to the proto fails here until it gets them.
func TestScalarWordsCoverEveryKind(t *testing.T) {
	parametric := map[string]bool{"array": true, "range": true, "multirange": true, "weak": true, "named": true}
	fields := (&irv1.Type{}).ProtoReflect().Descriptor().Oneofs().ByName("kind").Fields()
	for i := range fields.Len() {
		name := string(fields.Get(i).Name())
		if _, ok := scalarWords[name]; !ok && !parametric[name] {
			t.Errorf("kind %s has no words", name)
		}
	}
}

func TestTypeWords(t *testing.T) {
	kind := func(name string) *irv1.Type {
		ty := &irv1.Type{}
		fd := ty.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(name))
		ty.ProtoReflect().Set(fd, ty.ProtoReflect().NewField(fd))
		return ty
	}
	cases := []struct {
		t    *irv1.Type
		want []string
	}{
		{tInt64, []string{"int64"}},
		{kind("uint16"), []string{"uint16"}},
		{&irv1.Type{Kind: &irv1.Type_Decimal{Decimal: &irv1.Decimal{Precision: new(uint32)}}}, []string{"decimal"}},
		{&irv1.Type{Kind: &irv1.Type_Varchar{Varchar: &irv1.Varchar{Length: 80}}}, []string{"varchar"}},
		{kind("instant_with_offset"), []string{"instant", "with", "offset"}},
		{kind("local_datetime"), []string{"local", "datetime"}},
		{kind("time_with_offset"), []string{"time", "with", "offset"}},
		{kind("macaddr"), []string{"macaddr"}},
		{tArray(tInt32, 1), []string{"array", "int32"}},
		{tArray(tArray(tText, 1), 2), []string{"array", "array", "text"}},
		{tRange(kind("date")), []string{"range", "date"}},
		{tMultirange(tInt64), []string{"multirange", "int64"}},
		{tWeak(tInt64), []string{"weak", "int64"}},
		{tOpaque("public", "citext"), []string{"opaque"}},
		{tEnum(qn("shop", "app", "post_status")), nil},
		{tArray(tEnum(qn("shop", "app", "post_status")), 1), nil},
		{tArray(nil, 1), nil},
		{&irv1.Type{}, nil},
		{nil, nil},
	}
	for _, c := range cases {
		if got := TypeWords(c.t); !slices.Equal(got, c.want) || (c.want == nil) != (got == nil) {
			t.Errorf("TypeWords(%v) = %q, want %q", c.t, got, c.want)
		}
	}
	// The result is a copy.
	w := TypeWords(kind("local_time"))
	w[0] = "x"
	if TypeWords(kind("local_time"))[0] != "local" {
		t.Error("TypeWords returned shared storage")
	}
}

func TestKindName(t *testing.T) {
	for ty, want := range map[*irv1.Type]string{
		nil: "", {}: "", tInt64: "int64", tArray(tText, 1): "array", tOpaque("public", "citext"): "opaque",
		tEnum(qn("shop", "app", "post_status")): "named",
	} {
		if got := KindName(ty); got != want {
			t.Errorf("KindName(%v) = %q, want %q", ty, got, want)
		}
	}
}

func TestIndexTypeWords(t *testing.T) {
	ix := NewIndex(shop())
	app := func(o string) *irv1.QualifiedName { return qn("shop", "app", o) }
	cases := []struct {
		t    *irv1.Type
		want []string
	}{
		{tInt64, []string{"int64"}},
		{tEnum(app("post_status")), []string{"post", "status"}},
		{tArray(tEnum(app("post_status")), 1), []string{"array", "post", "status"}},
		{tDomain(app("slug_text")), []string{"slug", "text"}},
		{tComposite(app("money_amount")), []string{"money", "amount"}},
		{tEnum(app("missing")), nil},
		{tArray(tComposite(app("missing")), 1), nil},
	}
	for _, c := range cases {
		if got := ix.TypeWords(c.t); !slices.Equal(got, c.want) {
			t.Errorf("Index.TypeWords(%v) = %q, want %q", c.t, got, c.want)
		}
	}
}
