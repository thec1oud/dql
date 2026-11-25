package gen

import (
	"strings"
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func ident(stored string, words ...string) (*irv1.Identifier, *irv1.Identifier) {
	return &irv1.Identifier{Stored: stored}, &irv1.Identifier{Stored: stored, Words: words}
}

// bundle holds one namespace of the given enums, domains and composites.
func bundle(enums []*irv1.Enum, domains []*irv1.Domain, composites []*irv1.Composite) *irv1.Bundle {
	return &irv1.Bundle{
		Version: &irv1.IrVersion{},
		Catalogs: []*irv1.Catalog{{
			Name:       &irv1.Identifier{Stored: "c"},
			Provenance: &irv1.Provenance{Source: "pg-live", SourceVersion: "1.0.0", InputFingerprint: "sha256:x"},
			Namespaces: []*irv1.Namespace{{Name: &irv1.Identifier{Stored: "n"}, Enums: enums, Domains: domains, Composites: composites}},
		}},
	}
}

func enum(stored string, words []string, labels ...string) *irv1.Enum {
	name, host := ident(stored, words...)
	e := &irv1.Enum{Name: name, HostName: host}
	for _, l := range labels {
		e.LabelNames = append(e.LabelNames, &irv1.Identifier{Stored: l, Words: []string{l}})
	}
	return e
}

func TestGenerate(t *testing.T) {
	text := &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}}
	en, eh := ident("email", "email")
	cn, ch := ident("pair", "pair")
	an, ah := ident("a", "a")
	bn, bh := ident("b", "b")
	b := bundle(
		[]*irv1.Enum{enum("mood", []string{"mood"}, "happy", `sa"d`), enum("empty", []string{"empty"})},
		[]*irv1.Domain{{Name: en, HostName: eh, Base: &irv1.Type{Kind: &irv1.Type_Array{Array: &irv1.ArrayType{Element: text, ElementNullable: true}}}}},
		[]*irv1.Composite{{Name: cn, HostName: ch, Attributes: []*irv1.Column{
			{Name: an, HostName: ah, Type: &irv1.Type{Kind: &irv1.Type_Named{Named: &irv1.NamedRef{Kind: &irv1.NamedRef_Enum{Enum: &irv1.QualifiedName{Catalog: "c", Namespace: "n", Object: "mood"}}}}}, Nullable: true},
			{Name: bn, HostName: bh, Type: &irv1.Type{Kind: &irv1.Type_Uuid{Uuid: &irv1.Empty{}}}},
		}}},
	)
	files, diags := Generate(b, Options{})
	if len(diags) != 0 || len(files) != 3 {
		t.Fatalf("%v %v", files, diags)
	}
	types := string(files[0].Content)
	for _, want := range []string{
		"// Input: catalog c from pg-live 1.0.0, sha256:x\n",
		"import \"./dql-version.dql.ts\";\nimport type { Uuid } from \"@dql/runtime\";\n\n",
		"export const Mood = [\"happy\", \"sa\\\"d\"] as const;\nexport type Mood = (typeof Mood)[number];\n",
		"export const Empty = [] as const;\n",
		"export type Email = (string | null)[] & { readonly __domain: \"c.n.email\" };\n",
		"export interface Pair {\n  a: Mood | null;\n  b: Uuid;\n}\n",
	} {
		if !strings.Contains(types, want) {
			t.Errorf("dql-types.dql.ts lacks %q:\n%s", want, types)
		}
	}
	if files[0].Name != "dql-types.dql.ts" || files[1].Name != "dql-version.dql.ts" || files[2].Name != Index {
		t.Errorf("names %s %s %s", files[0].Name, files[1].Name, files[2].Name)
	}
}

func TestGenerateCollisions(t *testing.T) {
	text := &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}}
	dn, dh := ident("record", "record")
	dn2, dh2 := ident("decimal", "decimal")
	cn, ch := ident("pair", "pair")
	an, ah := ident("a_b", "a", "b")
	bn, bh := ident("aB", "a", "b")
	b := bundle(
		[]*irv1.Enum{enum("2fa", []string{"2fa"}), enum("x2fa", []string{"x2fa"})},
		[]*irv1.Domain{{Name: dn, HostName: dh, Base: text}, {Name: dn2, HostName: dh2, Base: text}},
		[]*irv1.Composite{{Name: cn, HostName: ch, Attributes: []*irv1.Column{{Name: an, HostName: ah, Type: text}, {Name: bn, HostName: bh, Type: text}}}},
	)
	files, diags := Generate(b, Options{})
	if files != nil {
		t.Fatal("files with errors")
	}
	var got []string
	for _, d := range diags {
		if d.GetCode() != CodeNameCollision {
			t.Errorf("code %s", d.GetCode())
		}
		got = append(got, d.GetMessage())
	}
	for _, want := range []string{
		"composite type c.n.pair",
		"domain c.n.decimal gets the TypeScript name Decimal, as the runtime's Decimal does",
		"domain c.n.record gets the TypeScript name Record, as the TypeScript global Record does",
		"enum c.n.x2fa gets the TypeScript name X2fa, as enum c.n.2fa does",
	} {
		found := false
		for _, m := range got {
			found = found || strings.Contains(m, want)
		}
		if !found {
			t.Errorf("no diagnostic with %q in %q", want, got)
		}
	}
	// Sorted by target.
	if diags[0].GetTarget().GetObject() != "decimal" || diags[len(diags)-1].GetTarget().GetObject() != "x2fa" {
		t.Errorf("order %v", diags)
	}
}

// Imports: side effects first, then other packages, then the package's own
// modules; a module whose names are all types is imported with import type,
// and a type among values is marked type.
func TestRenderImports(t *testing.T) {
	g := newGenerator(bundle(nil, nil, nil), Options{})
	a := g.newFile("a")
	b := g.newFile("b")
	g.declare(b, "Thing", "thing", nil)
	g.declare(b, "make", "make", nil)
	g.declare(a, "Own", "own", nil)
	a.rt("Decimal", false)
	a.rt("enforceVersion", true)
	a.rt("Decimal", false)
	g.ref(a, "Thing", false)
	g.ref(a, "make", true)
	g.ref(a, "Own", true)
	a.use("pgvector", "Vector", false)
	a.printf("x;\n\n\n")
	got := string(g.render(a))
	want := "import \"./dql-version.dql.ts\";\n" +
		"import { type Decimal, enforceVersion } from \"@dql/runtime\";\n" +
		"import type { Vector } from \"pgvector\";\n" +
		"import { Thing, make } from \"./b.dql.ts\";\n\nx;\n"
	want = strings.Replace(want, "{ Thing, make }", "{ type Thing, make }", 1)
	if !strings.HasSuffix(got, want) {
		t.Errorf("got\n%s\nwant suffix\n%s", got, want)
	}
	empty := g.newFile("dql-version")
	if got := string(g.render(empty)); !strings.HasSuffix(got, "generated-code version 1\n\n\n") {
		t.Errorf("empty file %q", got)
	}
}
