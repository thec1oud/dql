package gen

import (
	"slices"
	"strings"
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

var (
	tUUID  = &irv1.Type{Kind: &irv1.Type_Uuid{Uuid: &irv1.Empty{}}}
	tRange = &irv1.Type{Kind: &irv1.Type_Range{Range: &irv1.RangeType{Element: tInt}}}
	tMulti = &irv1.Type{Kind: &irv1.Type_Multirange{Multirange: &irv1.RangeType{Element: tDec}}}
	tWeak  = &irv1.Type{Kind: &irv1.Type_Weak{Weak: &irv1.Weak{Affinity: tInt}}}
	tGrid  = &irv1.Type{Kind: &irv1.Type_Array{Array: &irv1.ArrayType{Element: tInt, Dimensions: 2, ElementNullable: true}}}
)

// decodeBundle is itemsBundle with the columns uid, rng, mr, w and grid on
// items, a to-many relation peers from items to items, and the given shapes
// and unions.
func decodeBundle(shapes []*irv1.Shape, unions ...*irv1.RelationUnion) *irv1.Bundle {
	b := itemsBundle(&irv1.ShapeLayer{Shapes: shapes, Unions: unions})
	items := b.GetCatalogs()[0].GetNamespaces()[0].GetTables()[0]
	items.Columns = append(items.Columns, col("uid", tUUID), col("rng", tRange), col("mr", tMulti), col("w", tWeak), col("grid", tGrid))
	items.Relations = append(items.Relations, &irv1.Relation{Name: &irv1.Identifier{Stored: "peers", Words: []string{"peers"}}, Kind: irv1.RelationKind_RELATION_KIND_ONE_TO_MANY})
	return b
}

func userFull(redact bool) *irv1.Shape {
	s := shp(presetID("users", irv1.Preset_PRESET_FULL, irv1.Form_FORM_FLAT), "user full", "users", []*irv1.Field{fl("id", tInt, false, false)})
	s.Fields[0].Redact = redact
	return s
}

// §13: a decoder per read shape, with a converter of every kind, a Date
// column, and nests to one, to one nullable and to many; write shapes get
// none.
func TestDecoders(t *testing.T) {
	user := presetID("users", irv1.Preset_PRESET_FULL, irv1.Form_FORM_FLAT)
	row := shp(customID("item_row"), "item row", "items", []*irv1.Field{
		fl("id", tInt, false, false), fl("code", tText, false, false), fl("blob", tBytes, false, false), fl("tags", tTags, false, false),
		fl("note", tText, false, true), fl("slug", tSlug, false, false), fl("row", tRow, false, false), fl("mood", tMood, false, false),
		fl("price", tDec, false, false), fl("at", tAt, false, false), fl("doc", tJSON, false, false),
		fl("ci", opaqueOf("citext", irv1.OpaqueFallback_OPAQUE_FALLBACK_STRING), false, false),
		fl("vec", opaqueOf("vector", irv1.OpaqueFallback_OPAQUE_FALLBACK_UNSPECIFIED), false, false),
		fl("d", opaqueOf("money2", irv1.OpaqueFallback_OPAQUE_FALLBACK_UNSPECIFIED), false, false),
		fl("uid", tUUID, false, false), fl("rng", tRange, false, false), fl("mr", tMulti, false, false), fl("w", tWeak, false, false),
		fl("grid", tGrid, false, false),
	}, nestOf("owner", user, false, false), nestOf("peers", customID("item_ref"), false, false))
	ref := shp(customID("item_ref"), "item ref", "items", []*irv1.Field{fl("id", tInt, false, false)}, nestOf("owner", user, false, true))
	draft := shp(presetID("items", irv1.Preset_PRESET_DRAFT, irv1.Form_FORM_FLAT), "item draft", "items", []*irv1.Field{fl("code", tText, true, false)})
	files, codes := generate(t, decodeBundle([]*irv1.Shape{row, ref, draft, userFull(false)}), Options{Types: map[string]string{
		"public.vector": "number", "public.money2": "./money.ts#Money",
	}})
	if len(codes) != 0 {
		t.Fatalf("codes %q", codes)
	}
	for file, parts := range map[string][]string{
		"dql-shapes.dql.ts": {
			"export function decodeItemRow(r: Row): ItemRow {\n  const d = new Decoder(\"item_row\", r, 21);\n  return {\n",
			`    id: d.field(0, "id", "", asInt64),`,
			`    tags: d.field(3, "tags", "", array(elem(asString))),`,
			`    note: d.nullable(4, "note", asString),`,
			`    slug: d.field(5, "slug", "", dqlDecodeSlug),`,
			`    row: d.field(6, "row", "", dqlDecodePair),`,
			`    mood: d.field(7, "mood", "", asEnum(Mood)),`,
			`    price: d.field(8, "price", "", asInstanceOf(Decimal)),`,
			`    at: d.field(9, "at", "", asDate),`,
			`    doc: d.field(10, "doc", "", asJson),`,
			`    ci: d.field(11, "ci", "", asString),`,
			`    vec: d.field(12, "vec", "", asFloat64),`,
			`    d: d.field(13, "d", "", unchecked<Money>),`,
			`    uid: d.field(14, "uid", "", asString<Uuid>),`,
			`    rng: d.field(15, "rng", "", asRange(asInt64)),`,
			`    mr: d.field(16, "mr", "", asMultirange(asInstanceOf(Decimal))),`,
			`    w: d.field(17, "w", "", weak(asInt64)),`,
			`    grid: d.field(18, "grid", "", array(array(nullableElem(asInt64)))),`,
			`    owner: d.field(19, "owner", "", nest(decodeUserFull)),`,
			`    peers: d.many(20, "peers", decodeItemRef),`,
			`    owner: d.nullable(1, "owner", nest(decodeUserFull)),`,
		},
		"dql-decoders.dql.ts": {
			"export function dqlDecodeSlug(v: unknown): Slug {\n  return asString(v) as Slug;\n}",
			"export function dqlDecodePair(v: unknown): Pair {\n  const d = new Decoder(\"\", v, 1);\n  return {\n    a: d.field(0, \"a\", \"core.nonnull on shop.app.pair.a\", asInt64),\n  };\n}",
		},
	} {
		for _, part := range parts {
			if !strings.Contains(files[file], part) {
				t.Errorf("%s lacks %q:\n%s", file, part, files[file])
			}
		}
	}
	for _, f := range files {
		if strings.Contains(f, "decodeItemDraft") {
			t.Errorf("a write shape has a decoder:\n%s", f)
		}
	}
	files["money.ts"] = "export interface Money { cents: bigint }\n"
	typecheck(t, files)
}

// §7.5: redaction of fields, optional ones, and nests to one, nullable, to
// many and to many nullable, and of a union whose key is sensitive.
func TestRedaction(t *testing.T) {
	user := presetID("users", irv1.Preset_PRESET_FULL, irv1.Form_FORM_FLAT)
	secret := shp(customID("item_secret"), "item secret", "items", []*irv1.Field{fl("id", tInt, false, false), fl("code", tText, false, false), fl("note", tText, true, true)})
	secret.Fields[1].Redact, secret.Fields[2].Redact = true, true
	many := nestOf("peers", customID("item_secret"), true, true)
	shapes := []*irv1.Shape{
		secret, userFull(true),
		shp(customID("item_a"), "item a", "items", nil, nestOf("owner", user, false, true), nestOf("peers", customID("item_secret"), false, false)),
		shp(customID("item_b"), "item b", "items", []*irv1.Field{fl("id", tInt, false, false)}, many),
	}
	ref := &irv1.RelationUnionId{Kind: irv1.RelationUnionKind_RELATION_UNION_REF, Entity: qn("items"), Relation: "owner"}
	union := &irv1.RelationUnion{Id: ref, HostName: &irv1.Identifier{Words: []string{"item", "owner", "ref"}}, Variants: []*irv1.RelationVariant{
		{Change: irv1.RelationChange_RELATION_CHANGE_LINK, Key: user},
	}}
	shapes = append(shapes, shp(presetID("items", irv1.Preset_PRESET_DRAFT, irv1.Form_FORM_NESTED), "item draft nested", "items", nil,
		&irv1.Nest{Relation: "owner", Target: &irv1.Nest_Union{Union: ref}}))
	files, codes := generate(t, decodeBundle(shapes, union), Options{})
	if len(codes) != 0 {
		t.Fatalf("codes %q", codes)
	}
	for file, parts := range map[string][]string{
		"dql-shapes.dql.ts": {
			"export function redactItemSecret(s: ItemSecret): Record<string, unknown> {\n  return {\n    ...s,\n    code: \"[REDACTED]\",\n" +
				"    ...(s.note === undefined ? {} : { note: \"[REDACTED]\" }),\n  };\n}",
			`    owner: a.owner === null ? null : redactUserFull(a.owner),`,
			`    peers: a.peers.map((x) => redactItemSecret(x)),`,
			`    ...(b.peers === undefined ? {} : { peers: b.peers.map((x) => (x === null ? null : redactItemSecret(x))) }),`,
		},
		"item.dql.ts": {
			`  if (r.kind === "link") return { ...r, key: redactUserFull(r.key) };`,
			`    owner: redactItemOwnerRef(n.owner),`,
		},
	} {
		for _, part := range parts {
			if !strings.Contains(files[file], part) {
				t.Errorf("%s lacks %q:\n%s", file, part, files[file])
			}
		}
	}
}

// Types and nests a decoder cannot read.
func TestDecodeFailures(t *testing.T) {
	gone := func(kind *irv1.NamedRef) *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Named{Named: kind}} }
	write := shp(presetID("users", irv1.Preset_PRESET_DRAFT, irv1.Form_FORM_FLAT), "user draft", "users", []*irv1.Field{fl("id", tInt, true, false)})
	cases := []struct {
		name   string
		fields []*irv1.Field
		nests  []*irv1.Nest
		want   string
	}{
		{"no kind", []*irv1.Field{fl("id", &irv1.Type{}, false, false)}, nil, "ts-type-unknown"},
		{"unknown enum", []*irv1.Field{fl("mood", gone(&irv1.NamedRef{Kind: &irv1.NamedRef_Enum{Enum: qn("gone")}}), false, false)}, nil, "ts-type-unknown: column shop.app.items.mood references a named type"},
		{"unknown domain", []*irv1.Field{fl("slug", gone(&irv1.NamedRef{Kind: &irv1.NamedRef_Domain{Domain: qn("gone")}}), false, false)}, nil, "ts-type-unknown: column shop.app.items.slug references a named type"},
		{"write nest", nil, []*irv1.Nest{nestOf("owner", presetID("users", irv1.Preset_PRESET_DRAFT, irv1.Form_FORM_FLAT), false, false)},
			"ts-decode-unsupported: read shape item_bad nests a write shape or change through owner, which no row holds"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bad := shp(customID("item_bad"), "item bad", "items", c.fields, c.nests...)
			_, ds := Generate(decodeBundle([]*irv1.Shape{bad, write, userFull(false)}), Options{})
			var all []string
			for _, d := range ds {
				all = append(all, d.GetCode()+": "+d.GetMessage())
			}
			if !slices.ContainsFunc(all, func(s string) bool { return strings.Contains(s, c.want) }) {
				t.Errorf("diagnostics %q, want %q", all, c.want)
			}
		})
	}
}

// §8.7, §13: a TINYINT(1) annotated bool decodes with asBoolean, and an
// integer other than 0 or 1 names the annotation, by its location when the
// origin records one.
func TestBoolDecoder(t *testing.T) {
	tiny := &irv1.Type{Kind: &irv1.Type_Bool{Bool: &irv1.Empty{}},
		Native: &irv1.NativeType{Name: &irv1.QualifiedName{Object: "tinyint"}, Raw: "tinyint(1)", BooleanStorage: true}}
	s := shp(customID("item_flags"), "item flags", "items", []*irv1.Field{fl("on", tiny, false, false), fl("off", tiny, false, true)})
	b := decodeBundle([]*irv1.Shape{s})
	items := b.GetCatalogs()[0].GetNamespaces()[0].GetTables()[0]
	on := col("on", tiny)
	on.Annotations = []*irv1.Annotation{{Key: "core.bool", Origin: &irv1.Origin{Kind: irv1.OriginKind_ORIGIN_DB_COMMENT, Location: "COMMENT ON COLUMN app.items.on"}}}
	items.Columns = append(items.Columns, on, col("off", tiny))
	files, codes := generate(t, b, Options{})
	if len(codes) != 0 {
		t.Fatalf("codes %q", codes)
	}
	for _, part := range []string{
		`    on: d.field(0, "on", "", asserted(asBoolean, "core.bool from COMMENT ON COLUMN app.items.on")),`,
		`    off: d.nullable(1, "off", asserted(asBoolean, "core.bool on shop.app.items.off")),`,
	} {
		if !strings.Contains(files["dql-shapes.dql.ts"], part) {
			t.Errorf("dql-shapes.dql.ts lacks %q:\n%s", part, files["dql-shapes.dql.ts"])
		}
	}
	typecheck(t, files)
}
