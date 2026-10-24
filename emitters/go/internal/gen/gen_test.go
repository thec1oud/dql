package gen

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/testkit/determinism"
)

func e() *irv1.Empty { return &irv1.Empty{} }

func qn(object string) *irv1.QualifiedName {
	return &irv1.QualifiedName{Catalog: "shop", Namespace: "app", Object: object}
}

func attr(stored string, typ *irv1.Type, nullable bool, words ...string) *irv1.Column {
	if words == nil {
		words = []string{stored}
	}
	return &irv1.Column{Name: &irv1.Identifier{Stored: stored}, HostName: &irv1.Identifier{Words: words}, Type: typ, Nullable: nullable}
}

func opaque(ns, object string, fb irv1.OpaqueFallback) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Opaque{Opaque: &irv1.Opaque{Fallback: fb}},
		Native: &irv1.NativeType{Name: &irv1.QualifiedName{Namespace: ns, Object: object}}}
}

// bundle holds one namespace with ns's objects, in a catalog shop.
func bundle(ns *irv1.Namespace) *irv1.Bundle {
	ns.Name = &irv1.Identifier{Stored: "app"}
	return &irv1.Bundle{
		Version: &irv1.IrVersion{Major: proto.Uint32(1), Minor: proto.Uint32(2), Patch: proto.Uint32(3)},
		Dialect: irv1.Dialect_DIALECT_POSTGRES,
		Catalogs: []*irv1.Catalog{{
			Name:       &irv1.Identifier{Stored: "shop"},
			Provenance: &irv1.Provenance{Source: "pg-live", SourceVersion: "1.0.0", InputFingerprint: "sha256:00"},
			Namespaces: []*irv1.Namespace{ns},
		}},
	}
}

func generate(t *testing.T, b *irv1.Bundle, o Options) (map[string]string, []string) {
	t.Helper()
	if o.Package == "" {
		o.Package = "shopdb"
	}
	files, ds := Generate(b, o)
	out := map[string]string{}
	for _, f := range files {
		out[f.Name] = string(f.Content)
	}
	var codes []string
	for _, d := range ds {
		codes = append(codes, fmt.Sprintf("%s %s %s", d.GetSeverity(), d.GetCode(), displayName(d.GetTarget())))
	}
	return out, codes
}

func composite(attrs ...*irv1.Column) *irv1.Namespace {
	return &irv1.Namespace{Composites: []*irv1.Composite{{
		Name: &irv1.Identifier{Stored: "every"}, HostName: &irv1.Identifier{Words: []string{"every"}}, Attributes: attrs,
	}}}
}

// §8.1, §8.6, §8.8, §8.10: every logical type's host type.
func TestTypes(t *testing.T) {
	cases := []struct {
		typ  *irv1.Type
		want string
	}{
		{&irv1.Type{Kind: &irv1.Type_Bool{Bool: e()}}, "bool"},
		{&irv1.Type{Kind: &irv1.Type_Int8{Int8: e()}}, "int8"},
		{&irv1.Type{Kind: &irv1.Type_Int16{Int16: e()}}, "int16"},
		{&irv1.Type{Kind: &irv1.Type_Int32{Int32: e()}}, "int32"},
		{&irv1.Type{Kind: &irv1.Type_Int64{Int64: e()}}, "int64"},
		{&irv1.Type{Kind: &irv1.Type_Uint8{Uint8: e()}}, "uint8"},
		{&irv1.Type{Kind: &irv1.Type_Uint16{Uint16: e()}}, "uint16"},
		{&irv1.Type{Kind: &irv1.Type_Uint32{Uint32: e()}}, "uint32"},
		{&irv1.Type{Kind: &irv1.Type_Uint64{Uint64: e()}}, "uint64"},
		{&irv1.Type{Kind: &irv1.Type_Float32{Float32: e()}}, "float32"},
		{&irv1.Type{Kind: &irv1.Type_Float64{Float64: e()}}, "float64"},
		{&irv1.Type{Kind: &irv1.Type_Decimal{Decimal: &irv1.Decimal{}}}, "dec.Decimal"},
		{&irv1.Type{Kind: &irv1.Type_Money{Money: e()}}, "dec.Decimal"},
		{&irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}}, "string"},
		{&irv1.Type{Kind: &irv1.Type_Varchar{Varchar: &irv1.Varchar{Length: 80}}}, "string"},
		{&irv1.Type{Kind: &irv1.Type_Char{Char: &irv1.Char{Length: 3}}}, "string"},
		{&irv1.Type{Kind: &irv1.Type_Bytes{Bytes: &irv1.Bytes{}}}, "[]byte"},
		{&irv1.Type{Kind: &irv1.Type_Instant{Instant: &irv1.TimeType{}}}, "time.Time"},
		{&irv1.Type{Kind: &irv1.Type_InstantWithOffset{InstantWithOffset: &irv1.TimeType{}}}, "civil.OffsetDateTime"},
		{&irv1.Type{Kind: &irv1.Type_LocalDatetime{LocalDatetime: &irv1.TimeType{}}}, "civil.DateTime"},
		{&irv1.Type{Kind: &irv1.Type_Date{Date: e()}}, "civil.Date"},
		{&irv1.Type{Kind: &irv1.Type_LocalTime{LocalTime: &irv1.TimeType{}}}, "civil.Time"},
		{&irv1.Type{Kind: &irv1.Type_TimeWithOffset{TimeWithOffset: &irv1.TimeType{}}}, "civil.OffsetTime"},
		{&irv1.Type{Kind: &irv1.Type_Interval{Interval: &irv1.TimeType{}}}, "dql.Interval"},
		{&irv1.Type{Kind: &irv1.Type_Uuid{Uuid: e()}}, "uuid.UUID"},
		{&irv1.Type{Kind: &irv1.Type_Json{Json: e()}}, "json.RawMessage"},
		{&irv1.Type{Kind: &irv1.Type_Jsonb{Jsonb: e()}}, "json.RawMessage"},
		{&irv1.Type{Kind: &irv1.Type_Xml{Xml: e()}}, "string"},
		{&irv1.Type{Kind: &irv1.Type_Inet{Inet: e()}}, "netip.Addr"},
		{&irv1.Type{Kind: &irv1.Type_Cidr{Cidr: e()}}, "netip.Prefix"},
		{&irv1.Type{Kind: &irv1.Type_Macaddr{Macaddr: e()}}, "net.HardwareAddr"},
		{&irv1.Type{Kind: &irv1.Type_Bit{Bit: &irv1.Bit{}}}, "dql.Bits"},
		{&irv1.Type{Kind: &irv1.Type_Range{Range: &irv1.RangeType{Element: &irv1.Type{Kind: &irv1.Type_Date{Date: e()}}}}}, "dql.Range[civil.Date]"},
		{&irv1.Type{Kind: &irv1.Type_Multirange{Multirange: &irv1.RangeType{Element: &irv1.Type{Kind: &irv1.Type_Int64{Int64: e()}}}}}, "[]dql.Range[int64]"},
		{&irv1.Type{Kind: &irv1.Type_Array{Array: &irv1.ArrayType{Element: &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}}, ElementNullable: true, Dimensions: 1}}}, "[]*string"},
		{&irv1.Type{Kind: &irv1.Type_Array{Array: &irv1.ArrayType{Element: &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}}, Dimensions: 2}}}, "[][]string"},
		{&irv1.Type{Kind: &irv1.Type_Array{Array: &irv1.ArrayType{Element: &irv1.Type{Kind: &irv1.Type_Int32{Int32: e()}}}}}, "[]int32"},
		{&irv1.Type{Kind: &irv1.Type_Weak{Weak: &irv1.Weak{Affinity: &irv1.Type{Kind: &irv1.Type_Int64{Int64: e()}}}}}, "weak.Weak[int64]"},
		{&irv1.Type{Kind: &irv1.Type_Named{Named: &irv1.NamedRef{Kind: &irv1.NamedRef_Enum{Enum: qn("mood")}}}}, "Mood"},
		{&irv1.Type{Kind: &irv1.Type_Named{Named: &irv1.NamedRef{Kind: &irv1.NamedRef_Domain{Domain: qn("email")}}}}, "Email"},
		{&irv1.Type{Kind: &irv1.Type_Named{Named: &irv1.NamedRef{Kind: &irv1.NamedRef_Composite{Composite: qn("every")}}}}, "Every"},
		{opaque("public", "citext", irv1.OpaqueFallback_OPAQUE_FALLBACK_UNSPECIFIED), "string"},
		{opaque("public", "geometry", irv1.OpaqueFallback_OPAQUE_FALLBACK_UNSPECIFIED), "pgtype.Point"},
		{opaque("public", "ltree", irv1.OpaqueFallback_OPAQUE_FALLBACK_STRING), "string"},
		{opaque("public", "hstore", irv1.OpaqueFallback_OPAQUE_FALLBACK_BYTES), "[]byte"},
	}
	var attrs []*irv1.Column
	for i, c := range cases {
		attrs = append(attrs, attr(fmt.Sprintf("a%d", i), c.typ, false))
	}
	attrs = append(attrs, attr("maybe", &irv1.Type{Kind: &irv1.Type_Int64{Int64: e()}}, true))
	ns := composite(attrs...)
	ns.Enums = []*irv1.Enum{{Name: &irv1.Identifier{Stored: "mood"}, HostName: &irv1.Identifier{Words: []string{"mood"}}}}
	ns.Domains = []*irv1.Domain{{Name: &irv1.Identifier{Stored: "email"}, HostName: &irv1.Identifier{Words: []string{"email"}},
		Base: &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}}}}
	files, codes := generate(t, bundle(ns), Options{Types: map[string]string{
		"public.citext":   "string",
		"public.geometry": "github.com/jackc/pgx/v5/pgtype.Point",
	}})
	if codes != nil {
		t.Fatalf("diagnostics %q", codes)
	}
	src := files["dql-types.dql.go"]
	for i, c := range cases {
		if re := regexp.MustCompile(fmt.Sprintf(`\tA%d +%s\n`, i, regexp.QuoteMeta(c.want))); !re.MatchString(src) {
			t.Errorf("A%d is not %s", i, c.want)
		}
	}
	if !strings.Contains(src, "Maybe *int64") {
		t.Errorf("nullable attribute: %s", src)
	}
	for _, imp := range []string{`"encoding/json"`, `"net"`, `"net/netip"`, `"time"`, `"github.com/google/uuid"`,
		`"github.com/jackc/pgx/v5/pgtype"`, `"github.com/thec1oud/dql/runtime/go/civil"`, `"github.com/thec1oud/dql/runtime/go/weak"`} {
		if !strings.Contains(src, imp) {
			t.Errorf("no import %s", imp)
		}
	}
	// An empty enum has no constants.
	if !strings.Contains(src, "func (m Mood) Valid() bool {\n\treturn false\n}") || !strings.Contains(src, "return []Mood{}") {
		t.Errorf("empty enum:\n%s", src)
	}
	if !strings.Contains(src, "// IR version: 1.2.3\n// Input: catalog shop from pg-live 1.0.0, sha256:00\n") {
		t.Errorf("header:\n%s", src)
	}
}

// The same bundle always generates the same bytes (§3.2, §26.5).
func TestDeterministic(t *testing.T) {
	ns := composite(attr("a", &irv1.Type{Kind: &irv1.Type_Uuid{Uuid: e()}}, false), attr("b", &irv1.Type{Kind: &irv1.Type_Instant{Instant: &irv1.TimeType{}}}, true))
	ns.Enums = []*irv1.Enum{enum("mood", []string{"mood"}, "happy", "sad")}
	err := determinism.Repeat(20, func() ([]byte, error) {
		files, _ := Generate(bundle(ns), Options{Package: "p", Types: map[string]string{"a.b": "x/y.Z", "c.d": "string"}})
		var out []byte
		for _, f := range files {
			out = append(append(out, f.Name...), f.Content...)
		}
		return out, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
