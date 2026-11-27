package fidelity

import (
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/thec1oud/dql/core/bundle"
	dpgextv1 "github.com/thec1oud/dql/proto/dql/ext/dpg/v1"
	pgextv1 "github.com/thec1oud/dql/proto/dql/ext/postgres/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/testkit/golden"
)

func pack(msgs ...proto.Message) []*anypb.Any {
	var out []*anypb.Any
	for _, m := range msgs {
		b, _ := proto.Marshal(m)
		out = append(out, &anypb.Any{TypeUrl: "type.dql.dev/" + string(m.ProtoReflect().Descriptor().FullName()), Value: b})
	}
	return out
}

func native(ns, name string) *irv1.NativeType {
	return &irv1.NativeType{Name: &irv1.QualifiedName{Namespace: ns, Object: name}}
}

func text() *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}, Native: native("pg_catalog", "text")}
}

func domain(name string) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Named{Named: &irv1.NamedRef{Kind: &irv1.NamedRef_Domain{
		Domain: &irv1.QualifiedName{Namespace: "app", Object: name}}}}}
}

func facts(name string) *irv1.Collation {
	t := irv1.Tristate_TRISTATE_TRUE
	return &irv1.Collation{Name: name, CaseSensitive: t, AccentSensitive: t, Deterministic: t,
		PadSpace: irv1.Tristate_TRISTATE_FALSE, ClientFold: irv1.ClientFold_CLIENT_FOLD_NONE}
}

func column(name string, t *irv1.Type) *irv1.Column {
	return &irv1.Column{Name: &irv1.Identifier{Stored: name}, Type: t, Collation: facts("default")}
}

var comment = &irv1.Origin{Kind: irv1.OriginKind_ORIGIN_DB_COMMENT, Plugin: "pg-live", Location: "COMMENT ON COLUMN app.v.a"}

// sample holds something for every normalization: what pg-live reads of a
// database, and a dpg record.
func sample() *irv1.Catalog {
	member := pack(&pgextv1.ExtensionMember{Extension: "x"})
	ext := &pgextv1.Routine{Name: "e", Kind: "FUNCTION", Extension: "x"}
	rangeType := &irv1.Type{Kind: &irv1.Type_Range{Range: &irv1.RangeType{Element: &irv1.Type{Kind: &irv1.Type_Int32{}}}}}
	builtinRange := proto.CloneOf(rangeType)
	rangeType.Native, builtinRange.Native = native("app", "r"), native("pg_catalog", "int4range")
	array := &irv1.Type{Kind: &irv1.Type_Array{Array: &irv1.ArrayType{Element: &irv1.Type{Kind: &irv1.Type_Text{}}}}, Native: native("pg_catalog", "_text")}
	return &irv1.Catalog{
		Name:       &irv1.Identifier{Stored: "c"},
		Provenance: &irv1.Provenance{Source: "pg-live"},
		Namespaces: []*irv1.Namespace{{
			Name: &irv1.Identifier{Stored: "app"},
			Tables: []*irv1.Table{
				{
					Name: &irv1.Identifier{Stored: "t"}, Kind: irv1.TableKind_RELATION_KIND_TABLE,
					Columns: []*irv1.Column{
						column("text", text()),
						column("dom", domain("d")),
						column("int_dom", domain("dn")),
						column("cycle", domain("x")),
						column("unknown", domain("zz")),
						column("array", array),
						column("range", rangeType),
						column("builtin_range", builtinRange),
						{Name: &irv1.Identifier{Stored: "filled"}, Type: text(),
							Default:    &irv1.Default{Class: irv1.DefaultClass_DEFAULT_CONSTANT, Native: "'x'::text"},
							Generation: &irv1.Generation{Native: "lower(text)"}},
					},
					Constraints: []*irv1.Constraint{{Name: "c", Form: &irv1.Constraint_Opaque{Opaque: &irv1.NativeText{Native: "CHECK (x)"}}}},
					Uniqueness: []*irv1.Uniqueness{{Name: "u", Expressions: []*irv1.IndexExpression{{Native: "lower(a)"}},
						Predicate: &irv1.NativeText{Native: "(a > 0)"}}},
					Extensions: pack(
						&pgextv1.Policy{Name: "p", Using: "u", WithCheck: "w"},
						&pgextv1.Index{Name: "i", Definition: "CREATE INDEX"},
						&pgextv1.Trigger{Name: "tr", Definition: "CREATE TRIGGER"},
						&pgextv1.PartitionKey{Definition: "RANGE (a)"},
						&pgextv1.PartitionOf{Bound: "DEFAULT"},
						&pgextv1.Partition{Namespace: "app"},
						&pgextv1.Grant{Grantee: "web"},
						&pgextv1.Owner{Role: "o"},
						&dpgextv1.Record{Kind: "table", Fields: &structpb.Struct{}},
						&pgextv1.ViewDefinition{Definition: "SELECT 1"},
					),
				},
				{Name: &irv1.Identifier{Stored: "owned"}, Extensions: member},
				{
					Name: &irv1.Identifier{Stored: "v"}, Kind: irv1.TableKind_RELATION_KIND_VIEW,
					Columns: []*irv1.Column{{Name: &irv1.Identifier{Stored: "a"},
						Annotations: []*irv1.Annotation{
							{Key: "core.nonnull", Origin: comment},
							{Key: "core.sensitive", Origin: &irv1.Origin{Kind: irv1.OriginKind_ORIGIN_CONFIG}},
						},
						Extensions: pack(&pgextv1.Comment{Text: "@nonnull"}, &pgextv1.Grant{Grantee: "web"})}},
				},
				{Name: &irv1.Identifier{Stored: "mv"}, Kind: irv1.TableKind_RELATION_KIND_MATERIALIZED_VIEW,
					Annotations: []*irv1.Annotation{{Key: "core.logical-key", Origin: comment}}},
			},
			Enums:      []*irv1.Enum{{Name: &irv1.Identifier{Stored: "e"}}, {Name: &irv1.Identifier{Stored: "owned"}, Extensions: member}},
			Composites: []*irv1.Composite{{Name: &irv1.Identifier{Stored: "owned"}, Extensions: member}},
			Domains: []*irv1.Domain{
				{Name: &irv1.Identifier{Stored: "d"}, Base: text(), Collation: facts("app.ci")},
				{Name: &irv1.Identifier{Stored: "dn"}, Base: &irv1.Type{Kind: &irv1.Type_Int32{}, Native: native("pg_catalog", "int4")}, Collation: facts("default")},
				{Name: &irv1.Identifier{Stored: "x"}, Base: domain("y")},
				{Name: &irv1.Identifier{Stored: "y"}, Base: domain("x")},
				{Name: &irv1.Identifier{Stored: "owned"}, Base: text(), Extensions: member},
			},
			Routines: []*irv1.Routine{
				{Name: &irv1.Identifier{Stored: "f"}, Overloads: []*irv1.Overload{
					{Kind: irv1.RoutineKind_ROUTINE_KIND_FUNCTION, Arguments: []*irv1.Argument{
						{Name: &irv1.Identifier{Stored: "b"}, Type: text(), Mode: irv1.ArgMode_ARG_MODE_IN, HasDefault: true},
						{Name: &irv1.Identifier{Stored: "o"}, Type: text(), Mode: irv1.ArgMode_ARG_MODE_OUT},
					}, Extensions: pack(&pgextv1.RoutineSource{Language: "sql", Body: "SELECT 1"},
						&pgextv1.RoutineProperties{ArgumentDefaults: []string{"'x'"}})},
					{Kind: irv1.RoutineKind_ROUTINE_KIND_FUNCTION},
				}},
				{Name: &irv1.Identifier{Stored: "p"}, Overloads: []*irv1.Overload{{Kind: irv1.RoutineKind_ROUTINE_KIND_PROCEDURE,
					Extensions: pack(&pgextv1.Comment{Text: "@public"}, &pgextv1.RoutineSource{Language: "plpgsql", Body: "BEGIN END"})}}},
				{Name: &irv1.Identifier{Stored: "owned"}, Overloads: []*irv1.Overload{{Extensions: member}}},
			},
			Extensions: pack(
				&pgextv1.Statistics{Name: "s"},
				&pgextv1.Collation{Name: "ci"},
				&pgextv1.BaseType{Name: "b"},
				&pgextv1.BaseType{Name: "bx", Extension: "x"},
				&pgextv1.RangeType{Name: "r", Owner: "o", Grants: []*pgextv1.Grant{{Grantee: "web"}}},
				&pgextv1.Routine{Name: "agg", Kind: "AGGREGATE"},
				&pgextv1.Routine{Name: "proc", Kind: "PROCEDURE", Language: "plpgsql", Body: "BEGIN END", IdentityArguments: "a integer",
					ArgumentDefaults: []string{"1"}, Owner: "o", Grants: []*pgextv1.Grant{{Grantee: "web"}}},
				ext,
			),
		}},
		Extensions: pack(&pgextv1.Database{Encoding: "UTF8"}, &pgextv1.InstalledExtension{Name: "plpgsql"}),
	}
}

func encode(t *testing.T, c *irv1.Catalog) []byte {
	t.Helper()
	b, err := bundle.MarshalJSON(c)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestNormalize pins what each of dpg-snapshot's limits removes, and what
// is removed whatever the limits.
func TestNormalize(t *testing.T) {
	c := sample()
	before := encode(t, c)
	golden.Assert(t, filepath.Join("testdata", "normalize.dpg-snapshot.json"), encode(t, Normalize(c, DPGSnapshot)))
	golden.Assert(t, filepath.Join("testdata", "normalize.none.json"), encode(t, Normalize(c, nil)))
	if string(encode(t, c)) != string(before) {
		t.Error("Normalize changed its argument")
	}
}

// TestUnknownExtension keeps an extension of a type it does not know.
func TestUnknownExtension(t *testing.T) {
	unknown := &anypb.Any{TypeUrl: "type.dql.dev/unknown.Message", Value: []byte{1}}
	c := Normalize(&irv1.Catalog{Extensions: []*anypb.Any{unknown}}, DPGSnapshot)
	if len(c.Extensions) != 1 || !proto.Equal(c.Extensions[0], unknown) {
		t.Errorf("extensions: %v", c.Extensions)
	}
}
