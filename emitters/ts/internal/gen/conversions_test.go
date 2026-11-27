package gen

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

var (
	tInt   = &irv1.Type{Kind: &irv1.Type_Int64{Int64: &irv1.Empty{}}}
	tText  = &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}}
	tBytes = &irv1.Type{Kind: &irv1.Type_Bytes{Bytes: &irv1.Bytes{}}}
	tSlug  = &irv1.Type{Kind: &irv1.Type_Named{Named: &irv1.NamedRef{Kind: &irv1.NamedRef_Domain{Domain: qn("slug")}}}}
	tMood  = &irv1.Type{Kind: &irv1.Type_Named{Named: &irv1.NamedRef{Kind: &irv1.NamedRef_Enum{Enum: qn("mood")}}}}
	tTags  = &irv1.Type{Kind: &irv1.Type_Array{Array: &irv1.ArrayType{Element: tText, Dimensions: 1}}}
	tRow   = &irv1.Type{Kind: &irv1.Type_Named{Named: &irv1.NamedRef{Kind: &irv1.NamedRef_Composite{Composite: qn("pair")}}}}
	tDec   = &irv1.Type{Kind: &irv1.Type_Decimal{Decimal: &irv1.Decimal{}}}
	tAt    = &irv1.Type{Kind: &irv1.Type_Instant{Instant: &irv1.TimeType{}}}
	tJSON  = &irv1.Type{Kind: &irv1.Type_Jsonb{Jsonb: &irv1.Empty{}}}
)

func opaqueOf(name string, fb irv1.OpaqueFallback) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Opaque{Opaque: &irv1.Opaque{Fallback: fb}}, Native: &irv1.NativeType{Name: &irv1.QualifiedName{Namespace: "public", Object: name}}}
}

func presetID(entity string, p irv1.Preset, form irv1.Form) *irv1.ShapeId {
	return &irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: qn(entity)}, Preset: p, Form: form}
}

func customID(name string) *irv1.ShapeId {
	return &irv1.ShapeId{Base: &irv1.ShapeId_Custom{Custom: name}}
}

// fl is a field of the given column, type, presence and nullability.
func fl(col string, typ *irv1.Type, optional, nullable bool) *irv1.Field {
	p := irv1.Presence_PRESENCE_REQUIRED
	if optional {
		p = irv1.Presence_PRESENCE_OPTIONAL
	}
	return &irv1.Field{Column: col, Type: typ, Presence: p, Nullable: nullable}
}

func shp(id *irv1.ShapeId, words string, entity string, fields []*irv1.Field, nests ...*irv1.Nest) *irv1.Shape {
	return &irv1.Shape{Id: id, HostName: &irv1.Identifier{Words: strings.Fields(words)}, Entity: qn(entity), Fields: fields, Nests: nests}
}

func conv(from, to *irv1.ShapeId, kind irv1.ConversionKind, inputs ...string) *irv1.Conversion {
	c := &irv1.Conversion{From: from, To: to, Kind: kind}
	for _, in := range inputs {
		c.Inputs = append(c.Inputs, &irv1.ConversionInput{Column: in, Reason: irv1.InputReason_INPUT_WRITE_ONLY})
	}
	return c
}

func keyCmp(col string, c irv1.Comparison, fold irv1.ClientFold) *irv1.KeyComparison {
	return &irv1.KeyComparison{Column: col, Comparison: c, Fold: fold}
}

// itemsBundle holds items(id, code, blob, tags, note, slug, key, row, f,
// mood, price, at, doc, ci, raw, vec, d) with relation owner to users(id),
// domain slug over text, enum mood, composite type pair, and the given shape
// layer. at maps to Date (§7.7).
func itemsBundle(layer *irv1.ShapeLayer) *irv1.Bundle {
	c := func(stored string, typ *irv1.Type, nullable bool) *irv1.Column {
		out := col(stored, typ)
		out.Nullable = nullable
		return out
	}
	at := c("at", tAt, false)
	at.Annotations = []*irv1.Annotation{{Key: TimeTypeKey, Value: &irv1.AnnotationValue{Kind: &irv1.AnnotationValue_String_{String_: "date"}}}}
	ns := &irv1.Namespace{
		Name: &irv1.Identifier{Stored: "app"},
		Tables: []*irv1.Table{
			{Name: &irv1.Identifier{Stored: "items"}, HostName: &irv1.Identifier{Words: []string{"item"}},
				Columns: []*irv1.Column{c("id", tInt, false), c("code", tText, false), c("blob", tBytes, false), c("tags", tTags, false),
					c("note", tText, true), c("slug", tSlug, false), c("key", tInt, false), c("row", tRow, false), c("f", tInt, false),
					c("mood", tMood, false), c("price", tDec, false), at, c("doc", tJSON, false),
					c("ci", opaqueOf("citext", irv1.OpaqueFallback_OPAQUE_FALLBACK_STRING), false),
					c("raw", opaqueOf("raw", irv1.OpaqueFallback_OPAQUE_FALLBACK_BYTES), false),
					c("vec", opaqueOf("vector", irv1.OpaqueFallback_OPAQUE_FALLBACK_UNSPECIFIED), false),
					c("d", opaqueOf("money2", irv1.OpaqueFallback_OPAQUE_FALLBACK_UNSPECIFIED), false)},
				Relations: []*irv1.Relation{{Name: &irv1.Identifier{Stored: "owner", Words: []string{"owner"}}, Kind: irv1.RelationKind_RELATION_KIND_MANY_TO_ONE}}},
			{Name: &irv1.Identifier{Stored: "users"}, HostName: &irv1.Identifier{Words: []string{"user"}}, Columns: []*irv1.Column{c("id", tInt, false)}},
		},
		Enums:      []*irv1.Enum{{Name: &irv1.Identifier{Stored: "mood"}, HostName: &irv1.Identifier{Words: []string{"mood"}}, LabelNames: []*irv1.Identifier{{Stored: "ok", Words: []string{"ok"}}}}},
		Domains:    []*irv1.Domain{{Name: &irv1.Identifier{Stored: "slug"}, HostName: &irv1.Identifier{Words: []string{"slug"}}, Base: tText}},
		Composites: []*irv1.Composite{{Name: &irv1.Identifier{Stored: "pair"}, HostName: &irv1.Identifier{Words: []string{"pair"}}, Attributes: []*irv1.Column{c("a", tInt, false)}}},
	}
	b := shapeBundle(nil)
	b.Catalogs[0].Namespaces = []*irv1.Namespace{ns}
	b.Shapes = layer
	return b
}

// The item presets: Full, Key, Partial and a Draft, and a user Full.
func itemPresets() []*irv1.Shape {
	return []*irv1.Shape{
		shp(presetID("items", irv1.Preset_PRESET_FULL, irv1.Form_FORM_FLAT), "item full", "items",
			[]*irv1.Field{fl("id", tInt, false, false), fl("code", tText, false, false), fl("blob", tBytes, false, false), fl("note", tText, false, true), fl("slug", tSlug, false, false)}),
		shp(presetID("items", irv1.Preset_PRESET_KEY, irv1.Form_FORM_UNSPECIFIED), "item key", "items",
			[]*irv1.Field{fl("id", tInt, false, false), fl("code", tText, false, false), fl("blob", tBytes, false, false), fl("slug", tSlug, false, false)}),
		shp(presetID("items", irv1.Preset_PRESET_PARTIAL, irv1.Form_FORM_FLAT), "item partial", "items",
			[]*irv1.Field{fl("code", tText, true, false), fl("note", tText, true, true)}),
		shp(presetID("items", irv1.Preset_PRESET_DRAFT, irv1.Form_FORM_FLAT), "item draft", "items",
			[]*irv1.Field{fl("id", tInt, true, true), fl("code", tText, true, false), fl("note", tText, true, true), fl("slug", tSlug, false, true), fl("f", tInt, false, false)}),
		shp(presetID("users", irv1.Preset_PRESET_FULL, irv1.Form_FORM_FLAT), "user full", "users", []*irv1.Field{fl("id", tInt, false, false)}),
	}
}

func itemConversions() []*irv1.Conversion {
	full, key := presetID("items", irv1.Preset_PRESET_FULL, irv1.Form_FORM_FLAT), presetID("items", irv1.Preset_PRESET_KEY, irv1.Form_FORM_UNSPECIFIED)
	partial, draft := presetID("items", irv1.Preset_PRESET_PARTIAL, irv1.Form_FORM_FLAT), presetID("items", irv1.Preset_PRESET_DRAFT, irv1.Form_FORM_FLAT)
	return []*irv1.Conversion{
		conv(full, key, irv1.ConversionKind_CONVERSION_TOTAL),
		conv(full, draft, irv1.ConversionKind_CONVERSION_NEEDS_INPUTS, "f"),
		conv(partial, full, irv1.ConversionKind_CONVERSION_WITH_BASE),
		conv(draft, full, irv1.ConversionKind_CONVERSION_VIA_DATABASE),
		conv(key, full, irv1.ConversionKind_CONVERSION_IMPOSSIBLE),
	}
}

// typecheck writes the generated files to a directory and type checks them
// with tsc against the runtime's declarations, as the golden package is.
func typecheck(t *testing.T, files map[string]string) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "runtime", "ts"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	config, _ := json.Marshal(map[string]any{
		"compilerOptions": map[string]any{
			"target": "es2023", "module": "esnext", "moduleResolution": "bundler", "lib": []string{"es2023"}, "types": []string{},
			"strict": true, "exactOptionalPropertyTypes": true, "noUncheckedIndexedAccess": true, "verbatimModuleSyntax": true,
			"erasableSyntaxOnly": true, "allowImportingTsExtensions": true, "noEmit": true, "skipLibCheck": true,
			"paths": map[string]any{"@dql/runtime": []string{filepath.Join(root, "dql", "dist", "index.d.ts")}},
		},
		"include": []string{"*.ts"},
	})
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), config, 0o644); err != nil {
		t.Fatal(err)
	}
	check := exec.Command(filepath.Join(root, "node_modules", ".bin", "tsc"), "-p", filepath.Join(dir, "tsconfig.json"))
	if msg, err := check.CombinedOutput(); err != nil {
		t.Fatalf("the generated package does not type check (run bun install and bun run build in runtime/ts first): %v\n%s", err, msg)
	}
}

// §14.9: every kind as it dictates, and the field conversions between
// presences and nullabilities.
func TestConversions(t *testing.T) {
	shapes := append(itemPresets(),
		shp(customID("item_card"), "item card", "items", []*irv1.Field{fl("id", tInt, false, false), fl("code", tText, true, false)},
			&irv1.Nest{Relation: "owner", Target: &irv1.Nest_Shape{Shape: presetID("users", irv1.Preset_PRESET_FULL, irv1.Form_FORM_FLAT)}}),
		shp(customID("item_view"), "item view", "items", []*irv1.Field{fl("id", tInt, true, true), fl("code", tText, true, true)},
			&irv1.Nest{Relation: "owner", Target: &irv1.Nest_Shape{Shape: presetID("users", irv1.Preset_PRESET_FULL, irv1.Form_FORM_FLAT)}}),
		shp(customID("item_link"), "item link", "items", nil,
			&irv1.Nest{Relation: "owner", Presence: irv1.Presence_PRESENCE_OPTIONAL, Target: &irv1.Nest_Shape{Shape: presetID("users", irv1.Preset_PRESET_FULL, irv1.Form_FORM_FLAT)}}),
		shp(customID("item_patch"), "item patch", "items", []*irv1.Field{fl("code", tText, false, false), fl("note", tText, true, true)}),
		shp(customID("item_loose"), "item loose", "items", []*irv1.Field{fl("code", tText, true, true), fl("note", tText, false, true)}),
		shp(customID("item_named"), "item named", "items", []*irv1.Field{
			{Column: "code", Type: tText, Presence: irv1.Presence_PRESENCE_OPTIONAL, NullabilitySource: irv1.NullabilitySource_NULLABILITY_SOURCE_ANNOTATION},
			{Column: "note", Type: tText, Presence: irv1.Presence_PRESENCE_REQUIRED, NullabilitySource: irv1.NullabilitySource_NULLABILITY_SOURCE_SHAPE_OPERATOR},
		}),
	)
	full := presetID("items", irv1.Preset_PRESET_FULL, irv1.Form_FORM_FLAT)
	layer := &irv1.ShapeLayer{Shapes: shapes, Conversions: append(itemConversions(),
		conv(customID("item_card"), customID("item_view"), irv1.ConversionKind_CONVERSION_TOTAL),
		conv(customID("item_link"), customID("item_link"), irv1.ConversionKind_CONVERSION_TOTAL),
		conv(customID("item_patch"), full, irv1.ConversionKind_CONVERSION_WITH_BASE),
		conv(customID("item_loose"), customID("item_named"), irv1.ConversionKind_CONVERSION_FALLIBLE),
		conv(customID("item_patch"), presetID("items", irv1.Preset_PRESET_DRAFT, irv1.Form_FORM_FLAT), irv1.ConversionKind_CONVERSION_NEEDS_INPUTS, "id", "slug", "f"),
		conv(customID("item_patch"), presetID("items", irv1.Preset_PRESET_KEY, irv1.Form_FORM_UNSPECIFIED), irv1.ConversionKind_CONVERSION_WITH_BASE),
	)}
	files, codes := generate(t, itemsBundle(layer), Options{})
	if len(codes) != 0 {
		t.Fatalf("codes %q", codes)
	}
	for file, parts := range map[string][]string{
		"item.dql.ts": {
			"/** ItemFull converts ItemFull values to other shapes (§14.9). */\nexport const ItemFull = {\n",
			"  /** key converts f to ItemKey (§14.9). */\n  key(f: ItemFull): ItemKey {\n    return {\n      id: f.id,\n      code: f.code,\n",
			// The input's name meets the parameter's.
			"  /** toDraft converts f to ItemDraft (§14.9). ItemFull cannot hold every field, so f2 supplies f. */\n  toDraft(f: ItemFull, f2: bigint): ItemDraft {\n",
			"    return {\n      id: f.id,\n      code: f.code,\n      note: f.note,\n      slug: f.slug,\n      f: f2,\n    };\n",
			"  applyTo(p: ItemPartial, f: ItemFull): ItemFull {\n    const out: ItemFull = { ...f };\n    if (p.code !== undefined) out.code = p.code;\n",
			"} as const;\n",
		},
		"dql-shapes.dql.ts": {
			"  toItemView(c: ItemCard): ItemView {\n    const v: ItemView = {\n      id: c.id,\n      owner: c.owner,\n    };\n    if (c.code !== undefined) v.code = c.code;\n    return v;\n  },",
			"  toItemLink(l: ItemLink): ItemLink {\n    const l2: ItemLink = {};\n    if (l.owner !== undefined) l2.owner = l.owner;\n    return l2;\n  },",
			"  applyTo(p: ItemPatch, f: ItemFull): ItemFull {\n    const out: ItemFull = {\n      ...f,\n      code: p.code,\n    };\n    if (p.note !== undefined) out.note = p.note;\n",
			"  /** toItemNamed converts l to ItemNamed, and throws a DecodeError where a field ItemNamed narrows to non-null is null (§13, §14.9). */\n" +
				"  toItemNamed(l: ItemLoose): ItemNamed {\n" +
				"    if (l.note === null) throw new DecodeError({ shape: \"item_named\", field: \"note\", reason: \"null_into_non_null\", assertion: \"nonnull of shape item_named\" });\n" +
				"    const n: ItemNamed = {\n      note: l.note,\n    };\n" +
				"    if (l.code !== undefined) {\n" +
				"      if (l.code === null) throw new DecodeError({ shape: \"item_named\", field: \"code\", reason: \"null_into_non_null\", assertion: \"core.nonnull on shop.app.items.code\" });\n" +
				"      n.code = l.code;\n    }\n    return n;\n  },",
			"  toDraft(p: ItemPatch, id: bigint | null | undefined, slug: Slug | null, f: bigint): ItemDraft {\n" +
				"    const d: ItemDraft = {\n      code: p.code,\n      slug,\n      f,\n    };\n" +
				"    if (id !== undefined) d.id = id;\n    if (p.note !== undefined) d.note = p.note;\n    return d;\n  },",
			// A Partial's nullable field onto a field the base lacks is
			// ignored.
			// Named key, as Go names it (§14.9).
			"  key(p: ItemPatch, k: ItemKey): ItemKey {\n    return {\n      ...k,\n      code: p.code,\n    };\n  },",
			"import { DecodeError, Decoder, type Row, asInt64, asString, nest } from \"@dql/runtime\";",
		},
	} {
		for _, part := range parts {
			if !strings.Contains(files[file], part) {
				t.Errorf("%s lacks %q:\n%s", file, part, files[file])
			}
		}
	}
	for _, absent := range []string{"export const ItemDraft", "export const ItemKey"} {
		if strings.Contains(files["item.dql.ts"], absent) {
			t.Errorf("item.dql.ts has %q", absent)
		}
	}
	typecheck(t, files)
}

// §8.9: merges compare exact keys with ===, bytes with bytesEqual, the
// runtime's values with equals, Date with getTime and folded keys after
// foldAscii, and a fallible merge throws on an unmatched key.
func TestMerges(t *testing.T) {
	keyed := []*irv1.Field{
		fl("id", tInt, false, false), fl("code", tText, false, false), fl("blob", tBytes, false, false), fl("slug", tSlug, false, false),
		fl("mood", tMood, false, false), fl("price", tDec, false, false), fl("at", tAt, false, false),
		fl("ci", opaqueOf("citext", irv1.OpaqueFallback_OPAQUE_FALLBACK_STRING), false, false),
		fl("raw", opaqueOf("raw", irv1.OpaqueFallback_OPAQUE_FALLBACK_BYTES), false, false),
		fl("vec", opaqueOf("vector", irv1.OpaqueFallback_OPAQUE_FALLBACK_UNSPECIFIED), false, false),
	}
	shapes := append(itemPresets()[2:],
		shp(presetID("items", irv1.Preset_PRESET_FULL, irv1.Form_FORM_FLAT), "item full", "items", append(slices.Clone(keyed), fl("note", tText, false, true))),
		shp(presetID("items", irv1.Preset_PRESET_KEY, irv1.Form_FORM_UNSPECIFIED), "item key", "items", keyed))
	merge := func(kind irv1.ConversionKind, keys ...*irv1.KeyComparison) *irv1.KeyMerge {
		return &irv1.KeyMerge{Entity: qn("items"), Kind: kind, Key: keys}
	}
	exact := func(col string) *irv1.KeyComparison {
		return keyCmp(col, irv1.Comparison_COMPARISON_EXACT, irv1.ClientFold_CLIENT_FOLD_NONE)
	}
	folded := func(col string) *irv1.KeyComparison {
		return keyCmp(col, irv1.Comparison_COMPARISON_FOLDED, irv1.ClientFold_CLIENT_FOLD_ASCII_LOWER)
	}
	layer := &irv1.ShapeLayer{Shapes: shapes, Conversions: itemConversions()[2:3], Merges: []*irv1.KeyMerge{
		merge(irv1.ConversionKind_CONVERSION_FALLIBLE, exact("id"), keyCmp("code", irv1.Comparison_COMPARISON_INEXACT, 0), exact("blob"), folded("slug")),
	}}
	o := Options{Types: map[string]string{"public.vector": "number"}}
	files, codes := generate(t, itemsBundle(layer), o)
	if len(codes) != 0 {
		t.Fatalf("codes %q", codes)
	}
	src := files["item.dql.ts"]
	for _, part := range []string{
		"export function mergeItemByKey(rows: readonly ItemFull[], changes: readonly Keyed<ItemKey, ItemPartial>[]): ItemFull[] {",
		"if (r.id === c.key.id && r.code === c.key.code && bytesEqual(r.blob, c.key.blob) && foldAscii(r.slug) === foldAscii(c.key.slug)) {",
		"merged[j] = ItemPartial.applyTo(c.value, r);",
		`if (!matched) throw new UnmatchedKeyError("shop.app.items", i);`,
		`import { Decimal, Decoder, type Keyed, type Row, Temporal, UnmatchedKeyError, type ValidationIssue, asBytes, asDate, asEnum, asFloat64, asInstanceOf, asInt64, asString, bytesEqual, foldAscii } from "@dql/runtime";`,
	} {
		if !strings.Contains(src, part) {
			t.Errorf("item.dql.ts lacks %q:\n%s", part, src)
		}
	}
	typecheck(t, files)

	layer.Merges = []*irv1.KeyMerge{merge(irv1.ConversionKind_CONVERSION_TOTAL,
		exact("mood"), folded("mood"), exact("price"), exact("at"), folded("ci"), exact("raw"), exact("vec"))}
	files, codes = generate(t, itemsBundle(layer), o)
	if len(codes) != 0 {
		t.Fatalf("codes %q", codes)
	}
	src = files["item.dql.ts"]
	for _, part := range []string{
		"): { merged: ItemFull[]; unmatched: ItemKey[] } {",
		"if (r.mood === c.key.mood && foldAscii(r.mood) === foldAscii(c.key.mood) && r.price.equals(c.key.price) && r.at.getTime() === c.key.at.getTime() && " +
			"foldAscii(r.ci) === foldAscii(c.key.ci) && bytesEqual(r.raw, c.key.raw) && r.vec === c.key.vec) {",
		"if (!matched) unmatched.push(c.key);",
	} {
		if !strings.Contains(src, part) {
			t.Errorf("item.dql.ts lacks %q:\n%s", part, src)
		}
	}
	typecheck(t, files)
}

// Conversions and merges the emitter cannot generate as their kinds say.
func TestConversionFailures(t *testing.T) {
	full, key := presetID("items", irv1.Preset_PRESET_FULL, irv1.Form_FORM_FLAT), presetID("items", irv1.Preset_PRESET_KEY, irv1.Form_FORM_UNSPECIFIED)
	partial, draft := presetID("items", irv1.Preset_PRESET_PARTIAL, irv1.Form_FORM_FLAT), presetID("items", irv1.Preset_PRESET_DRAFT, irv1.Form_FORM_FLAT)
	nested := shp(customID("nested"), "nested", "items", nil, &irv1.Nest{Relation: "owner", Target: &irv1.Nest_Shape{Shape: presetID("users", irv1.Preset_PRESET_FULL, irv1.Form_FORM_FLAT)}})
	coded := shp(customID("coded"), "coded", "items", []*irv1.Field{fl("code", tText, false, false)})
	noted := shp(customID("noted"), "noted", "items", []*irv1.Field{fl("note", tText, false, false)})
	retyped := shp(customID("retyped"), "retyped", "items", []*irv1.Field{fl("code", tInt, true, false)})
	keyOf := func(fields ...*irv1.Field) []*irv1.Shape {
		return []*irv1.Shape{
			shp(key, "item key", "items", fields),
			shp(full, "item full", "items", fields),
		}
	}
	cases := []struct {
		name   string
		shapes []*irv1.Shape
		convs  []*irv1.Conversion
		merges []*irv1.KeyMerge
		types  map[string]string
		want   string
	}{
		{"unknown shape", nil, []*irv1.Conversion{conv(customID("gone"), full, irv1.ConversionKind_CONVERSION_TOTAL)},
			nil, nil, "ts-reference-unknown: the conversion from gone to item.full.flat references a shape the bundle does not hold"},
		{"kind unknown", nil, []*irv1.Conversion{conv(full, key, irv1.ConversionKind_CONVERSION_UNSPECIFIED)},
			nil, nil, "ts-conversion-unsupported: the conversion from item.full.flat to item.key is CONVERSION_UNSPECIFIED"},
		{"fallible optional to required", []*irv1.Shape{noted}, []*irv1.Conversion{conv(partial, customID("noted"), irv1.ConversionKind_CONVERSION_FALLIBLE)},
			nil, nil, "field note is optional in item.partial.flat and required in noted"},
		{"fallible types differ", []*irv1.Shape{retyped}, []*irv1.Conversion{conv(partial, customID("retyped"), irv1.ConversionKind_CONVERSION_FALLIBLE)},
			nil, nil, "field code is optional in item.partial.flat and optional in retyped, or its types differ"},
		{"twice", nil, []*irv1.Conversion{conv(full, key, irv1.ConversionKind_CONVERSION_TOTAL), conv(full, key, irv1.ConversionKind_CONVERSION_TOTAL)},
			nil, nil, "ts-name-collision: the conversion from item.full.flat to item.key gets the TypeScript function ItemFull.key, as the conversion from item.full.flat to item.key does"},
		{"input unknown", nil, []*irv1.Conversion{conv(full, draft, irv1.ConversionKind_CONVERSION_NEEDS_INPUTS, "nope")},
			nil, nil, "ts-conversion-unsupported: the conversion from item.full.flat to item.draft.flat: input nope is not a field of item.draft.flat"},
		{"no source", nil, []*irv1.Conversion{conv(full, draft, irv1.ConversionKind_CONVERSION_TOTAL)},
			nil, nil, "ts-conversion-unsupported: the conversion from item.full.flat to item.draft.flat: item.full.flat has no field f, which item.draft.flat requires"},
		{"optional to required", []*irv1.Shape{coded}, []*irv1.Conversion{conv(partial, customID("coded"), irv1.ConversionKind_CONVERSION_TOTAL)},
			nil, nil, "field code is optional in item.partial.flat and required in coded, or its types differ"},
		{"nullable to non-null", nil, []*irv1.Conversion{conv(draft, key, irv1.ConversionKind_CONVERSION_NEEDS_INPUTS, "id", "code", "blob")},
			nil, nil, "field slug is required nullable in item.draft.flat and required in item.key, or its types differ"},
		{"nest missing", []*irv1.Shape{nested}, []*irv1.Conversion{conv(full, customID("nested"), irv1.ConversionKind_CONVERSION_TOTAL)},
			nil, nil, "relation owner of nested is missing from item.full.flat or has another type"},
		{"apply mismatch", nil, []*irv1.Conversion{conv(draft, key, irv1.ConversionKind_CONVERSION_WITH_BASE)},
			nil, nil, "the conversion from item.draft.flat to item.key: field id is optional nullable in item.draft.flat and required in item.key, or its types differ"},
		{"apply collides", nil, []*irv1.Conversion{conv(partial, full, irv1.ConversionKind_CONVERSION_WITH_BASE), conv(partial, full, irv1.ConversionKind_CONVERSION_WITH_BASE)},
			nil, nil, "gets the TypeScript function ItemPartial.applyTo"},
		{"merge entity", nil, nil, []*irv1.KeyMerge{{Entity: qn("gone")}},
			nil, "ts-reference-unknown: the merge by key of shop.app.gone belongs to an entity the bundle does not hold"},
		{"merge shapes", nil, nil, []*irv1.KeyMerge{{Entity: qn("users")}},
			nil, "the merge by key of shop.app.users: it needs the entity's Full, Key and Partial shapes"},
		{"merge apply", nil, nil, []*irv1.KeyMerge{{Entity: qn("items")}},
			nil, "the merge by key of shop.app.items: it needs the conversion from item.partial.flat to item.full.flat"},
		{"merge nullable", nil, itemConversions(), []*irv1.KeyMerge{{Entity: qn("items"), Key: []*irv1.KeyComparison{keyCmp("note", irv1.Comparison_COMPARISON_EXACT, 0)}}},
			nil, "key column note must be a non-null field of item.key and item.full.flat"},
		{"merge array", keyOf(fl("tags", tTags, false, false)), itemConversions()[2:3],
			[]*irv1.KeyMerge{{Entity: qn("items"), Key: []*irv1.KeyComparison{keyCmp("tags", irv1.Comparison_COMPARISON_EXACT, 0)}}},
			nil, "the ts emitter cannot compare key column tags exactly"},
		{"merge composite", keyOf(fl("row", tRow, false, false)), itemConversions()[2:3],
			[]*irv1.KeyMerge{{Entity: qn("items"), Key: []*irv1.KeyComparison{keyCmp("row", irv1.Comparison_COMPARISON_EXACT, 0)}}},
			nil, "the ts emitter cannot compare key column row exactly"},
		{"merge json", keyOf(fl("doc", tJSON, false, false)), itemConversions()[2:3],
			[]*irv1.KeyMerge{{Entity: qn("items"), Key: []*irv1.KeyComparison{keyCmp("doc", irv1.Comparison_COMPARISON_EXACT, 0)}}},
			nil, "the ts emitter cannot compare key column doc exactly"},
		{"merge imported", keyOf(fl("d", opaqueOf("money2", irv1.OpaqueFallback_OPAQUE_FALLBACK_UNSPECIFIED), false, false)), itemConversions()[2:3],
			[]*irv1.KeyMerge{{Entity: qn("items"), Key: []*irv1.KeyComparison{keyCmp("d", irv1.Comparison_COMPARISON_EXACT, 0)}}},
			map[string]string{"public.money2": "money#Money"}, "the ts emitter cannot compare key column d exactly"},
		{"merge unmapped", keyOf(fl("d", opaqueOf("money2", irv1.OpaqueFallback_OPAQUE_FALLBACK_UNSPECIFIED), false, false)), itemConversions()[2:3],
			[]*irv1.KeyMerge{{Entity: qn("items"), Key: []*irv1.KeyComparison{keyCmp("d", irv1.Comparison_COMPARISON_EXACT, 0)}}},
			nil, "the ts emitter cannot compare key column d exactly"},
		{"merge fold", nil, itemConversions(), []*irv1.KeyMerge{
			{Entity: qn("items"), Key: []*irv1.KeyComparison{keyCmp("blob", irv1.Comparison_COMPARISON_FOLDED, irv1.ClientFold_CLIENT_FOLD_ASCII_LOWER)}},
			{Entity: qn("items"), Key: []*irv1.KeyComparison{keyCmp("code", irv1.Comparison_COMPARISON_FOLDED, irv1.ClientFold_CLIENT_FOLD_NONE)}},
			{Entity: qn("items"), Key: []*irv1.KeyComparison{keyCmp("code", irv1.Comparison_COMPARISON_UNSPECIFIED, 0)}},
		}, nil, "the ts emitter cannot compare key column blob after a fold"},
		{"merge kind", nil, itemConversions(), []*irv1.KeyMerge{{Entity: qn("items"), Key: []*irv1.KeyComparison{keyCmp("id", irv1.Comparison_COMPARISON_EXACT, 0)}}},
			nil, "the merge by key of shop.app.items: its kind is CONVERSION_UNSPECIFIED; a merge is total or fallible"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			shapes := itemPresets()
			for _, s := range c.shapes {
				shapes = slices.DeleteFunc(shapes, func(p *irv1.Shape) bool { return shapeKey(p.GetId()) == shapeKey(s.GetId()) })
				shapes = append(shapes, s)
			}
			b := itemsBundle(&irv1.ShapeLayer{Shapes: shapes, Conversions: c.convs, Merges: c.merges})
			files, ds := Generate(b, Options{Types: c.types})
			var all []string
			for _, d := range ds {
				all = append(all, d.GetCode()+": "+d.GetMessage())
			}
			if len(files) != 0 || !strings.Contains(strings.Join(all, "\n"), c.want) {
				t.Errorf("diagnostics %q, want %q", all, c.want)
			}
		})
	}
}

func TestFresh(t *testing.T) {
	used := map[string]bool{"f": true, "f2": true}
	if got := fresh("f", used); got != "f3" || !used["f3"] {
		t.Errorf("fresh = %q", got)
	}
	if got := fresh("g", used); got != "g" {
		t.Errorf("fresh = %q", got)
	}
}

func TestReceiver(t *testing.T) {
	for words, want := range map[string]string{"": "v", "item 2x": "x", "item 42": "v", "user full": "f", "item do": "d"} {
		if got := receiver(strings.Fields(words)); got != want {
			t.Errorf("receiver(%q) = %q, want %q", words, got, want)
		}
	}
}

func TestDescribe(t *testing.T) {
	if got := describe(true, true); got != "optional nullable" {
		t.Errorf("describe = %q", got)
	}
}

func TestAssertion(t *testing.T) {
	g := newGenerator(itemsBundle(&irv1.ShapeLayer{Shapes: itemPresets()}), Options{})
	s := itemPresets()[0]
	c := &irv1.Column{Annotations: []*irv1.Annotation{{Key: "core.nonnull", Origin: &irv1.Origin{Location: "COMMENT ON COLUMN app.items.note"}}}}
	fld := &irv1.Field{Column: "note", NullabilitySource: irv1.NullabilitySource_NULLABILITY_SOURCE_ANNOTATION}
	if got := g.assertion(s, fld, c); got != "core.nonnull from COMMENT ON COLUMN app.items.note" {
		t.Errorf("assertion = %q", got)
	}
	fld = &irv1.Field{Column: "note", NullabilitySource: irv1.NullabilitySource_NULLABILITY_SOURCE_SHAPE_OPERATOR, NullabilityShape: customID("summary")}
	if got := g.assertion(s, fld, c); got != "nonnull of shape summary" {
		t.Errorf("assertion = %q", got)
	}
	if got := g.assertion(s, &irv1.Field{}, c); got != "" {
		t.Errorf("assertion = %q", got)
	}
}
