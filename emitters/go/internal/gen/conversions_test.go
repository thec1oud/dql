package gen

import (
	"go/format"
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
	tTags  = &irv1.Type{Kind: &irv1.Type_Array{Array: &irv1.ArrayType{Element: tText, Dimensions: 1}}}
	tRow   = &irv1.Type{Kind: &irv1.Type_Named{Named: &irv1.NamedRef{Kind: &irv1.NamedRef_Composite{Composite: qn("pair")}}}}
)

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

// itemsBundle holds items(id, code, blob, tags, note, slug, key, row, f),
// with relation owner to users(id), domain slug over text, composite type
// pair, and the given shape layer.
func itemsBundle(layer *irv1.ShapeLayer) *irv1.Bundle {
	col := func(stored string, typ *irv1.Type, nullable bool) *irv1.Column { return attr(stored, typ, nullable) }
	ns := &irv1.Namespace{
		Tables: []*irv1.Table{
			{Name: &irv1.Identifier{Stored: "items"}, HostName: &irv1.Identifier{Words: []string{"item"}},
				Columns: []*irv1.Column{col("id", tInt, false), col("code", tText, false), col("blob", tBytes, false), col("tags", tTags, false),
					col("note", tText, true), col("slug", tSlug, false), col("key", tInt, false), col("row", tRow, false), col("f", tInt, false)},
				Relations: []*irv1.Relation{{Name: &irv1.Identifier{Stored: "owner", Words: []string{"owner"}}, Kind: irv1.RelationKind_RELATION_KIND_MANY_TO_ONE}}},
			{Name: &irv1.Identifier{Stored: "users"}, HostName: &irv1.Identifier{Words: []string{"user"}}, Columns: []*irv1.Column{col("id", tInt, false)}},
		},
		Domains:    []*irv1.Domain{{Name: &irv1.Identifier{Stored: "slug"}, HostName: &irv1.Identifier{Words: []string{"slug"}}, Base: tText}},
		Composites: []*irv1.Composite{{Name: &irv1.Identifier{Stored: "pair"}, HostName: &irv1.Identifier{Words: []string{"pair"}}, Attributes: []*irv1.Column{col("a", tInt, false)}}},
	}
	b := bundle(ns)
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

func compile(t *testing.T, src string) {
	t.Helper()
	if _, err := format.Source([]byte(src)); err != nil {
		t.Fatalf("%v:\n%s", err, src)
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
		conv(customID("item_patch"), full, irv1.ConversionKind_CONVERSION_WITH_BASE),
		conv(customID("item_loose"), customID("item_named"), irv1.ConversionKind_CONVERSION_FALLIBLE),
		conv(customID("item_patch"), presetID("items", irv1.Preset_PRESET_DRAFT, irv1.Form_FORM_FLAT), irv1.ConversionKind_CONVERSION_NEEDS_INPUTS, "id", "slug", "f"),
	)}
	files, codes := generate(t, itemsBundle(layer), Options{})
	if len(codes) != 0 {
		t.Fatalf("codes %q", codes)
	}
	for file, parts := range map[string][]string{
		"item.dql.go": {
			"// Key converts f to ItemKey (§14.9).\nfunc (f ItemFull) Key() ItemKey {\n\tvar k ItemKey\n\tk.ID = f.ID\n",
			// The input's name meets the receiver's.
			"// ItemFull cannot hold every field, so f2 supplies f (§14.9).\nfunc (f ItemFull) ToDraft(f2 int64) ItemDraft {",
			"\td.ID = opt.Some(dql.Ptr(f.ID))\n\td.Code = opt.Some(f.Code)\n\td.Note = opt.Some(f.Note)\n\td.Slug = dql.Ptr(f.Slug)\n\td.F = f2\n",
			"func (p ItemPartial) ApplyTo(f ItemFull) ItemFull {\n\tif v, ok := p.Code.Get(); ok {\n\t\tf.Code = v\n\t}\n",
		},
		"dql-shapes.dql.go": {
			"func (c ItemCard) ToItemView() ItemView {\n\tvar v ItemView\n\tv.ID = opt.Some(dql.Ptr(c.ID))\n\tv.Code = opt.Map(c.Code, dql.Ptr)\n\tv.Owner = c.Owner\n\treturn v\n}",
			"func (p ItemPatch) ApplyTo(f ItemFull) ItemFull {\n\tf.Code = p.Code\n",
			"func (l ItemLoose) ToItemNamed() (ItemNamed, error) {\n\tvar n ItemNamed\n" +
				"\tif v, ok := l.Code.Get(); ok {\n\t\tif v == nil {\n" +
				"\t\t\treturn ItemNamed{}, &dql.DecodeError{Shape: \"item_named\", Field: \"code\", Reason: dql.NullIntoNonNull, Assertion: \"core.nonnull on shop.app.items.code\"}\n" +
				"\t\t}\n\t\tn.Code = opt.Some(*v)\n\t}\n" +
				"\tv2 := l.Note\n\tif v2 == nil {\n" +
				"\t\treturn ItemNamed{}, &dql.DecodeError{Shape: \"item_named\", Field: \"note\", Reason: dql.NullIntoNonNull, Assertion: \"nonnull of shape item_named\"}\n" +
				"\t}\n\tn.Note = *v2\n\treturn n, nil\n}",
			"// ItemPatch cannot hold every field, so id supplies id and slug supplies slug and f supplies f (§14.9).\nfunc (p ItemPatch) ToDraft(id opt.Opt[*int64], slug *Slug, f int64) ItemDraft {",
		},
	} {
		for _, part := range parts {
			if !strings.Contains(files[file], part) {
				t.Errorf("%s lacks %q:\n%s", file, part, files[file])
			}
		}
	}
	for _, absent := range []string{"func (d ItemDraft)", "func (k ItemKey)"} {
		if strings.Contains(files["item.dql.go"], absent) {
			t.Errorf("item.dql.go has %q", absent)
		}
	}
}

// §8.9: merges compare exact keys with == or bytes.Equal, folded keys after
// dql.FoldASCII, and a fallible merge fails on an unmatched key.
func TestMerges(t *testing.T) {
	merge := &irv1.KeyMerge{Entity: qn("items"), Kind: irv1.ConversionKind_CONVERSION_FALLIBLE, Key: []*irv1.KeyComparison{
		keyCmp("id", irv1.Comparison_COMPARISON_EXACT, irv1.ClientFold_CLIENT_FOLD_NONE),
		keyCmp("code", irv1.Comparison_COMPARISON_INEXACT, irv1.ClientFold_CLIENT_FOLD_NONE),
		keyCmp("blob", irv1.Comparison_COMPARISON_EXACT, irv1.ClientFold_CLIENT_FOLD_NONE),
		keyCmp("slug", irv1.Comparison_COMPARISON_FOLDED, irv1.ClientFold_CLIENT_FOLD_ASCII_LOWER),
	}}
	layer := &irv1.ShapeLayer{Shapes: itemPresets(), Conversions: itemConversions(), Merges: []*irv1.KeyMerge{merge}}
	files, codes := generate(t, itemsBundle(layer), Options{})
	if len(codes) != 0 {
		t.Fatalf("codes %q", codes)
	}
	src := files["item.dql.go"]
	for _, part := range []string{
		"func MergeItemByKey(rows []ItemFull, changes []dql.Keyed[ItemKey, ItemPartial]) ([]ItemFull, error) {",
		"if r.ID == c.Key.ID && r.Code == c.Key.Code && bytes.Equal(r.Blob, c.Key.Blob) && dql.FoldASCII(r.Slug) == dql.FoldASCII(c.Key.Slug) {",
		`return nil, &dql.UnmatchedKeyError{Entity: "shop.app.items", Index: i}`,
	} {
		if !strings.Contains(src, part) {
			t.Errorf("item.dql.go lacks %q:\n%s", part, src)
		}
	}
	compile(t, src)
}

// Conversions and merges the emitter cannot generate as their kinds say.
func TestConversionFailures(t *testing.T) {
	full, key := presetID("items", irv1.Preset_PRESET_FULL, irv1.Form_FORM_FLAT), presetID("items", irv1.Preset_PRESET_KEY, irv1.Form_FORM_UNSPECIFIED)
	partial, draft := presetID("items", irv1.Preset_PRESET_PARTIAL, irv1.Form_FORM_FLAT), presetID("items", irv1.Preset_PRESET_DRAFT, irv1.Form_FORM_FLAT)
	keyed := shp(customID("keyed"), "keyed", "items", []*irv1.Field{fl("key", tInt, false, false), fl("id", tInt, false, false)})
	nested := shp(customID("nested"), "nested", "items", nil, &irv1.Nest{Relation: "owner", Target: &irv1.Nest_Shape{Shape: presetID("users", irv1.Preset_PRESET_FULL, irv1.Form_FORM_FLAT)}})
	coded := shp(customID("coded"), "coded", "items", []*irv1.Field{fl("code", tText, false, false)})
	noted := shp(customID("noted"), "noted", "items", []*irv1.Field{fl("note", tText, false, false)})
	cases := []struct {
		name   string
		shapes []*irv1.Shape
		convs  []*irv1.Conversion
		merges []*irv1.KeyMerge
		want   string
	}{
		{"unknown shape", nil, []*irv1.Conversion{conv(customID("gone"), full, irv1.ConversionKind_CONVERSION_TOTAL)},
			nil, "go-reference-unknown: the conversion from gone to item.full.flat references a shape the bundle does not hold"},
		{"kind unknown", nil, []*irv1.Conversion{conv(full, key, irv1.ConversionKind_CONVERSION_UNSPECIFIED)},
			nil, "go-conversion-unsupported: the conversion from item.full.flat to item.key is CONVERSION_UNSPECIFIED"},
		{"fallible optional to required", []*irv1.Shape{noted}, []*irv1.Conversion{conv(partial, customID("noted"), irv1.ConversionKind_CONVERSION_FALLIBLE)},
			nil, "field note is optional in item.partial.flat and required in noted"},
		{"method meets field", []*irv1.Shape{keyed}, []*irv1.Conversion{conv(customID("keyed"), key, irv1.ConversionKind_CONVERSION_TOTAL)},
			nil, "go-name-collision: the conversion from keyed to item.key gets the Go method Keyed.Key, as a field does"},
		{"method twice", nil, []*irv1.Conversion{conv(full, key, irv1.ConversionKind_CONVERSION_TOTAL), conv(full, key, irv1.ConversionKind_CONVERSION_TOTAL)},
			nil, "go-name-collision: the conversion from item.full.flat to item.key gets the Go method ItemFull.Key, as the conversion from item.full.flat to item.key does"},
		{"input unknown", nil, []*irv1.Conversion{conv(full, draft, irv1.ConversionKind_CONVERSION_NEEDS_INPUTS, "nope")},
			nil, "go-conversion-unsupported: the conversion from item.full.flat to item.draft.flat: input nope is not a field of item.draft.flat"},
		{"no source", nil, []*irv1.Conversion{conv(full, draft, irv1.ConversionKind_CONVERSION_TOTAL)},
			nil, "go-conversion-unsupported: the conversion from item.full.flat to item.draft.flat: item.full.flat has no field f, which item.draft.flat requires"},
		{"optional to required", []*irv1.Shape{coded}, []*irv1.Conversion{conv(partial, customID("coded"), irv1.ConversionKind_CONVERSION_TOTAL)},
			nil, "field code is optional in item.partial.flat and required in coded, or its types differ"},
		{"nullable to non-null", nil, []*irv1.Conversion{conv(draft, key, irv1.ConversionKind_CONVERSION_NEEDS_INPUTS, "id", "code", "blob")},
			nil, "field slug is required nullable in item.draft.flat and required in item.key, or its types differ"},
		{"nest missing", []*irv1.Shape{nested}, []*irv1.Conversion{conv(full, customID("nested"), irv1.ConversionKind_CONVERSION_TOTAL)},
			nil, "relation owner of nested is missing from item.full.flat or has another type"},
		{"apply nullable", nil, []*irv1.Conversion{conv(partial, key, irv1.ConversionKind_CONVERSION_WITH_BASE)},
			nil, ""},
		{"apply mismatch", nil, []*irv1.Conversion{conv(draft, key, irv1.ConversionKind_CONVERSION_WITH_BASE)},
			nil, "the conversion from item.draft.flat to item.key: field id is optional nullable in item.draft.flat and required in item.key, or its types differ"},
		{"apply collides", nil, []*irv1.Conversion{conv(partial, full, irv1.ConversionKind_CONVERSION_WITH_BASE), conv(partial, full, irv1.ConversionKind_CONVERSION_WITH_BASE)},
			nil, "gets the Go method ItemPartial.ApplyTo"},
		{"merge entity", nil, nil, []*irv1.KeyMerge{{Entity: qn("gone")}},
			"go-reference-unknown: the merge by key of shop.app.gone belongs to an entity the bundle does not hold"},
		{"merge shapes", nil, nil, []*irv1.KeyMerge{{Entity: qn("users")}},
			"the merge by key of shop.app.users: it needs the entity's Full, Key and Partial shapes"},
		{"merge apply", nil, nil, []*irv1.KeyMerge{{Entity: qn("items")}},
			"the merge by key of shop.app.items: it needs the conversion from item.partial.flat to item.full.flat"},
		{"merge nullable", nil, itemConversions(), []*irv1.KeyMerge{{Entity: qn("items"), Key: []*irv1.KeyComparison{keyCmp("note", irv1.Comparison_COMPARISON_EXACT, 0)}}},
			"key column note must be a non-null field of item.key and item.full.flat"},
		{"merge array", []*irv1.Shape{
			shp(presetID("items", irv1.Preset_PRESET_KEY, irv1.Form_FORM_UNSPECIFIED), "item key", "items", []*irv1.Field{fl("tags", tTags, false, false), fl("row", tRow, false, false)}),
			shp(presetID("items", irv1.Preset_PRESET_FULL, irv1.Form_FORM_FLAT), "item full", "items", []*irv1.Field{fl("tags", tTags, false, false), fl("row", tRow, false, false)}),
		}, []*irv1.Conversion{conv(partial, full, irv1.ConversionKind_CONVERSION_WITH_BASE)},
			[]*irv1.KeyMerge{
				{Entity: qn("items"), Key: []*irv1.KeyComparison{keyCmp("tags", irv1.Comparison_COMPARISON_EXACT, 0)}},
				{Entity: qn("items"), Key: []*irv1.KeyComparison{keyCmp("row", irv1.Comparison_COMPARISON_EXACT, 0)}},
			},
			"the go emitter cannot compare key column tags exactly"},
		{"merge fold", nil, itemConversions(), []*irv1.KeyMerge{
			{Entity: qn("items"), Key: []*irv1.KeyComparison{keyCmp("blob", irv1.Comparison_COMPARISON_FOLDED, irv1.ClientFold_CLIENT_FOLD_ASCII_LOWER)}},
			{Entity: qn("items"), Key: []*irv1.KeyComparison{keyCmp("code", irv1.Comparison_COMPARISON_FOLDED, irv1.ClientFold_CLIENT_FOLD_NONE)}},
			{Entity: qn("items"), Key: []*irv1.KeyComparison{keyCmp("code", irv1.Comparison_COMPARISON_UNSPECIFIED, 0)}},
		}, "the go emitter cannot compare key column blob after a fold"},
		{"merge kind", nil, itemConversions(), []*irv1.KeyMerge{{Entity: qn("items"), Key: []*irv1.KeyComparison{keyCmp("id", irv1.Comparison_COMPARISON_EXACT, 0)}}},
			"the merge by key of shop.app.items: its kind is CONVERSION_UNSPECIFIED; a merge is total or fallible"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			shapes := itemPresets()
			for _, s := range c.shapes {
				shapes = slices.DeleteFunc(shapes, func(p *irv1.Shape) bool { return shapeKey(p.GetId()) == shapeKey(s.GetId()) })
				shapes = append(shapes, s)
			}
			b := itemsBundle(&irv1.ShapeLayer{Shapes: shapes, Conversions: c.convs, Merges: c.merges})
			files, ds := Generate(b, Options{Package: "p"})
			var all []string
			for _, d := range ds {
				all = append(all, d.GetCode()+": "+d.GetMessage())
			}
			if c.want == "" {
				// A Partial's nullable field onto a non-null one is
				// skipped only when the target lacks it: item.key has no
				// note, so the conversion applies code alone.
				if len(ds) != 0 {
					t.Fatalf("diagnostics %q", all)
				}
				return
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

func TestDescribe(t *testing.T) {
	if got := describe(true, true); got != "optional nullable" {
		t.Errorf("describe = %q", got)
	}
}
