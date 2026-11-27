package gen

import (
	"strings"
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func ty(k any) *irv1.Type {
	t := &irv1.Type{}
	switch k := k.(type) {
	case *irv1.Type_Bool:
		t.Kind = k
	case *irv1.Type_Int16:
		t.Kind = k
	case *irv1.Type_Int64:
		t.Kind = k
	case *irv1.Type_Uint64:
		t.Kind = k
	case *irv1.Type_Float64:
		t.Kind = k
	case *irv1.Type_Text:
		t.Kind = k
	case *irv1.Type_Bytes:
		t.Kind = k
	case *irv1.Type_Bit:
		t.Kind = k
	case *irv1.Type_Decimal:
		t.Kind = k
	case *irv1.Type_Money:
		t.Kind = k
	case *irv1.Type_Instant:
		t.Kind = k
	case *irv1.Type_InstantWithOffset:
		t.Kind = k
	case *irv1.Type_LocalDatetime:
		t.Kind = k
	case *irv1.Type_Date:
		t.Kind = k
	case *irv1.Type_LocalTime:
		t.Kind = k
	case *irv1.Type_TimeWithOffset:
		t.Kind = k
	case *irv1.Type_Interval:
		t.Kind = k
	case *irv1.Type_Uuid:
		t.Kind = k
	case *irv1.Type_Json:
		t.Kind = k
	case *irv1.Type_Jsonb:
		t.Kind = k
	case *irv1.Type_Xml:
		t.Kind = k
	case *irv1.Type_Inet:
		t.Kind = k
	case *irv1.Type_Cidr:
		t.Kind = k
	case *irv1.Type_Macaddr:
		t.Kind = k
	case *irv1.Type_Range:
		t.Kind = k
	case *irv1.Type_Multirange:
		t.Kind = k
	case *irv1.Type_Array:
		t.Kind = k
	case *irv1.Type_Named:
		t.Kind = k
	case *irv1.Type_Weak:
		t.Kind = k
	}
	return t
}

// §8.1, §8.6, §8.8: the TS default of every logical type.
func TestTsType(t *testing.T) {
	e := &irv1.Empty{}
	text := ty(&irv1.Type_Text{Text: &irv1.Text{}})
	mood := &irv1.QualifiedName{Catalog: "c", Namespace: "n", Object: "mood"}
	email := &irv1.QualifiedName{Catalog: "c", Namespace: "n", Object: "email"}
	money := &irv1.QualifiedName{Catalog: "c", Namespace: "n", Object: "money_amount"}
	int64t := ty(&irv1.Type_Int64{Int64: e})
	opaque := func(ns, name string, fb irv1.OpaqueFallback) *irv1.Type {
		return &irv1.Type{Kind: &irv1.Type_Opaque{Opaque: &irv1.Opaque{Fallback: fb}},
			Native: &irv1.NativeType{Name: &irv1.QualifiedName{Namespace: ns, Object: name}}}
	}
	for _, c := range []struct {
		t        *irv1.Type
		want     string
		runtime  string
		external string
	}{
		{ty(&irv1.Type_Bool{Bool: e}), "boolean", "", ""},
		{ty(&irv1.Type_Int16{Int16: e}), "number", "", ""},
		{int64t, "bigint", "", ""},
		{ty(&irv1.Type_Uint64{Uint64: e}), "bigint", "", ""},
		{ty(&irv1.Type_Float64{Float64: e}), "number", "", ""},
		{text, "string", "", ""},
		{ty(&irv1.Type_Xml{Xml: e}), "string", "", ""},
		{ty(&irv1.Type_Bit{Bit: &irv1.Bit{}}), "string", "", ""},
		{ty(&irv1.Type_Bytes{Bytes: &irv1.Bytes{}}), "Uint8Array", "", ""},
		{ty(&irv1.Type_Decimal{Decimal: &irv1.Decimal{}}), "Decimal", "Decimal", ""},
		{ty(&irv1.Type_Money{Money: e}), "Decimal", "Decimal", ""},
		{ty(&irv1.Type_Instant{Instant: &irv1.TimeType{}}), "Temporal.Instant", "Temporal", ""},
		{ty(&irv1.Type_InstantWithOffset{InstantWithOffset: &irv1.TimeType{}}), "Temporal.ZonedDateTime", "Temporal", ""},
		{ty(&irv1.Type_LocalDatetime{LocalDatetime: &irv1.TimeType{}}), "Temporal.PlainDateTime", "Temporal", ""},
		{ty(&irv1.Type_Date{Date: e}), "Temporal.PlainDate", "Temporal", ""},
		{ty(&irv1.Type_LocalTime{LocalTime: &irv1.TimeType{}}), "Temporal.PlainTime", "Temporal", ""},
		{ty(&irv1.Type_TimeWithOffset{TimeWithOffset: &irv1.TimeType{}}), "OffsetTime", "OffsetTime", ""},
		{ty(&irv1.Type_Interval{Interval: &irv1.TimeType{}}), "Temporal.Duration", "Temporal", ""},
		{ty(&irv1.Type_Uuid{Uuid: e}), "Uuid", "Uuid", ""},
		{ty(&irv1.Type_Json{Json: e}), "JsonValue", "JsonValue", ""},
		{ty(&irv1.Type_Jsonb{Jsonb: e}), "JsonValue", "JsonValue", ""},
		{ty(&irv1.Type_Inet{Inet: e}), "Inet", "Inet", ""},
		{ty(&irv1.Type_Cidr{Cidr: e}), "Cidr", "Cidr", ""},
		{ty(&irv1.Type_Macaddr{Macaddr: e}), "MacAddr", "MacAddr", ""},
		{ty(&irv1.Type_Range{Range: &irv1.RangeType{Element: int64t}}), "Range<bigint>", "Range", ""},
		{ty(&irv1.Type_Multirange{Multirange: &irv1.RangeType{Element: int64t}}), "Range<bigint>[]", "Range", ""},
		{ty(&irv1.Type_Array{Array: &irv1.ArrayType{Element: text, ElementNullable: true}}), "(string | null)[]", "", ""},
		{ty(&irv1.Type_Array{Array: &irv1.ArrayType{Element: text, Dimensions: 2}}), "string[][]", "", ""},
		{ty(&irv1.Type_Array{Array: &irv1.ArrayType{Element: ty(&irv1.Type_Named{Named: &irv1.NamedRef{Kind: &irv1.NamedRef_Domain{Domain: email}}})}}), "EmailAddress[]", "", ""},
		{ty(&irv1.Type_Named{Named: &irv1.NamedRef{Kind: &irv1.NamedRef_Enum{Enum: mood}}}), "Mood", "", ""},
		{ty(&irv1.Type_Named{Named: &irv1.NamedRef{Kind: &irv1.NamedRef_Composite{Composite: money}}}), "MoneyAmount", "", ""},
		{ty(&irv1.Type_Weak{Weak: &irv1.Weak{Affinity: int64t}}), "Weak<bigint>", "Weak", ""},
		{opaque("public", "citext", irv1.OpaqueFallback_OPAQUE_FALLBACK_UNSPECIFIED), "string", "", ""},
		{opaque("public", "vector", irv1.OpaqueFallback_OPAQUE_FALLBACK_UNSPECIFIED), "Vector", "", "pgvector"},
		{opaque("public", "ltree", irv1.OpaqueFallback_OPAQUE_FALLBACK_STRING), "string", "", ""},
		{opaque("public", "blob", irv1.OpaqueFallback_OPAQUE_FALLBACK_BYTES), "Uint8Array", "", ""},
	} {
		g := &generator{
			o: Options{Types: map[string]string{"public.citext": "string", "public.vector": "pgvector#Vector"}},
			named: map[key]named{
				keyOf(mood): {words: []string{"mood"}}, keyOf(email): {words: []string{"email", "address"}},
				keyOf(money): {words: []string{"money", "amount"}},
			},
			decls: map[string]decl{},
		}
		f := &file{runtime: map[string]bool{}, modules: map[string]map[string]bool{}}
		if got := g.tsType(f, c.t, nil, "x"); got != c.want || len(g.diags) != 0 {
			t.Errorf("%v: got %q, want %q (%v)", c.t, got, c.want, g.diags)
		}
		if _, ok := f.runtime[c.runtime]; c.runtime != "" && (!ok || f.runtime[c.runtime]) {
			t.Errorf("%v: runtime imports %v", c.t, f.runtime)
		}
		if c.external != "" && f.modules[c.external] == nil {
			t.Errorf("%v: modules %v", c.t, f.modules)
		}
	}
}

func TestTsTypeUnknown(t *testing.T) {
	for _, c := range []struct {
		t    *irv1.Type
		code string
	}{
		{&irv1.Type{}, CodeTypeUnknown},
		{ty(&irv1.Type_Named{Named: &irv1.NamedRef{Kind: &irv1.NamedRef_Enum{Enum: &irv1.QualifiedName{Object: "gone"}}}}), CodeTypeUnknown},
		{&irv1.Type{Kind: &irv1.Type_Opaque{Opaque: &irv1.Opaque{}}, Native: &irv1.NativeType{Name: &irv1.QualifiedName{Namespace: "public", Object: "x"}}}, CodeOpaqueUnmapped},
	} {
		g := &generator{named: map[key]named{}, decls: map[string]decl{}}
		f := &file{runtime: map[string]bool{}, modules: map[string]map[string]bool{}}
		if got := g.tsType(f, c.t, &irv1.QualifiedName{Object: "o"}, "column o"); got != "unknown" || len(g.diags) != 1 || g.diags[0].GetCode() != c.code ||
			!strings.HasPrefix(g.diags[0].GetMessage(), "column o ") {
			t.Errorf("%v: %q %v", c.t, got, g.diags)
		}
	}
}

func TestParenthesize(t *testing.T) {
	for in, want := range map[string]string{
		"string":                       "string",
		"string | null":                "(string | null)",
		"string & { readonly a: 1 }":   "(string & { readonly a: 1 })",
		"(string | null)[]":            "(string | null)[]",
		"Range<a | b>":                 "Range<a | b>",
		"{ ok: true } | { ok: false }": "({ ok: true } | { ok: false })",
	} {
		if got := parenthesize(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}

func TestSplitImported(t *testing.T) {
	for _, c := range []struct{ spec, module, name string }{
		{"pgvector#Vector", "pgvector", "Vector"},
		{"@scope/pkg/sub#$Name_1", "@scope/pkg/sub", "$Name_1"},
		{"./local.ts#T", "./local.ts", "T"},
	} {
		if m, n, ok := SplitImported(c.spec); !ok || m != c.module || n != c.name {
			t.Errorf("%s: %q %q %v", c.spec, m, n, ok)
		}
	}
	for _, spec := range []string{"string", "a#", "#A", "a b#C", "a#b#C", "a#1"} {
		if _, _, ok := SplitImported(spec); ok {
			t.Errorf("%s: ok", spec)
		}
	}
}
