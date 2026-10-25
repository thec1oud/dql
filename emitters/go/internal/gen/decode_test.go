package gen

import (
	"strings"
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// gadgetsBundle holds everyNamespace's types and the tables gadgets(id,
// every, title, nick, owner_id), whose relations parts, owner, buddy and pal
// lead to users(id, secret), with the given shape layer.
func gadgetsBundle(layer *irv1.ShapeLayer) *irv1.Bundle {
	ns := everyNamespace()
	title := attr("title", tText, false)
	title.Annotations = []*irv1.Annotation{{Key: "core.nonnull", Origin: &irv1.Origin{Kind: irv1.OriginKind_ORIGIN_DB_COMMENT, Location: "COMMENT ON COLUMN app.gadgets.title"}}}
	rel := func(stored string, k irv1.RelationKind) *irv1.Relation {
		return &irv1.Relation{Name: &irv1.Identifier{Stored: stored, Words: []string{stored}}, Kind: k}
	}
	ns.Tables = []*irv1.Table{
		{Name: &irv1.Identifier{Stored: "gadgets"}, HostName: &irv1.Identifier{Words: []string{"gadget"}},
			Columns: []*irv1.Column{attr("id", tInt, false), attr("every", tRowOf("every"), false), title, attr("nick", tText, true), attr("owner_id", tInt, true, "owner", "id")},
			Relations: []*irv1.Relation{rel("parts", irv1.RelationKind_RELATION_KIND_ONE_TO_MANY), rel("owner", irv1.RelationKind_RELATION_KIND_MANY_TO_ONE),
				rel("buddy", irv1.RelationKind_RELATION_KIND_ONE_TO_ONE), rel("pal", irv1.RelationKind_RELATION_KIND_ONE_TO_ONE)}},
		{Name: &irv1.Identifier{Stored: "users"}, HostName: &irv1.Identifier{Words: []string{"user"}}, Columns: []*irv1.Column{attr("id", tInt, false), attr("secret", tText, false)}},
	}
	b := bundle(ns)
	b.Shapes = layer
	return b
}

func tRowOf(name string) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Named{Named: &irv1.NamedRef{Kind: &irv1.NamedRef_Composite{Composite: qn(name)}}}}
}

var (
	gadgetFull   = presetID("gadgets", irv1.Preset_PRESET_FULL, irv1.Form_FORM_FLAT)
	gadgetNested = presetID("gadgets", irv1.Preset_PRESET_FULL, irv1.Form_FORM_NESTED)
	gadgetDraft  = presetID("gadgets", irv1.Preset_PRESET_DRAFT, irv1.Form_FORM_FLAT)
	userFull     = presetID("users", irv1.Preset_PRESET_FULL, irv1.Form_FORM_FLAT)
	userDraft    = presetID("users", irv1.Preset_PRESET_DRAFT, irv1.Form_FORM_FLAT)
	userKeyID    = presetID("users", irv1.Preset_PRESET_KEY, irv1.Form_FORM_UNSPECIFIED)
	userRefID    = &irv1.RelationUnionId{Kind: irv1.RelationUnionKind_RELATION_UNION_REF, Entity: qn("users")}
)

// narrowedField is a non-null field of the given nullability source.
func narrowedField(col string, typ *irv1.Type, source irv1.NullabilitySource, by *irv1.ShapeId) *irv1.Field {
	f := fl(col, typ, false, false)
	f.NullabilitySource, f.NullabilityShape = source, by
	return f
}

// The user shapes and the user ref a gadget nests; the secret is sensitive.
func userShapes() ([]*irv1.Shape, *irv1.RelationUnion) {
	secret := fl("secret", tText, false, false)
	secret.Redact = true
	ref := &irv1.RelationUnion{Id: userRefID, HostName: &irv1.Identifier{Words: []string{"user", "ref"}}, Variants: []*irv1.RelationVariant{
		{Change: irv1.RelationChange_RELATION_CHANGE_LINK, HostName: &irv1.Identifier{Words: []string{"user", "ref", "link"}}, Key: userKeyID},
		{Change: irv1.RelationChange_RELATION_CHANGE_CREATE, HostName: &irv1.Identifier{Words: []string{"user", "ref", "create"}}, Payload: userDraft},
	}}
	return []*irv1.Shape{
		shp(userFull, "user full", "users", []*irv1.Field{fl("id", tInt, false, false), secret}),
		shp(userDraft, "user draft", "users", []*irv1.Field{secret}),
		shp(userKeyID, "user key", "users", []*irv1.Field{fl("id", tInt, false, false)}),
	}, ref
}

// §13: every read shape decodes a row of its fields, then its nests,
// enforcing each fact and naming each assertion; composite types and
// domains decode through their own decoders.
func TestDecoders(t *testing.T) {
	users, ref := userShapes()
	fields := []*irv1.Field{
		fl("id", tInt, false, false),
		fl("every", tRowOf("every"), false, false),
		narrowedField("title", tText, irv1.NullabilitySource_NULLABILITY_SOURCE_ANNOTATION, nil),
		fl("nick", tText, false, true),
		narrowedField("owner_id", tInt, irv1.NullabilitySource_NULLABILITY_SOURCE_SHAPE_OPERATOR, nil),
	}
	view := shp(customID("gadget_view"), "gadget view", "gadgets", []*irv1.Field{
		fl("id", tInt, true, false),
		narrowedField("nick", tText, irv1.NullabilitySource_NULLABILITY_SOURCE_SHAPE_OPERATOR, customID("gadget_named")),
	},
		&irv1.Nest{Relation: "pal", Presence: irv1.Presence_PRESENCE_OPTIONAL, Target: &irv1.Nest_Shape{Shape: userFull}},
		&irv1.Nest{Relation: "buddy", Presence: irv1.Presence_PRESENCE_OPTIONAL, Nullable: true, Target: &irv1.Nest_Shape{Shape: userFull}})
	view.Lineage = []*irv1.LineageStep{{Op: irv1.ShapeOp_PRESET, Entity: qn("gadgets"), Preset: irv1.Preset_PRESET_FULL, Form: irv1.Form_FORM_FLAT}}
	shapes := append(users,
		shp(gadgetFull, "gadget full", "gadgets", fields),
		shp(gadgetNested, "gadget full nested", "gadgets", fields,
			&irv1.Nest{Relation: "parts", Target: &irv1.Nest_Shape{Shape: userFull}},
			&irv1.Nest{Relation: "owner", Target: &irv1.Nest_Shape{Shape: userFull}},
			&irv1.Nest{Relation: "buddy", Nullable: true, Target: &irv1.Nest_Shape{Shape: userFull}}),
		shp(gadgetDraft, "gadget draft", "gadgets", []*irv1.Field{fl("title", tText, false, false)},
			&irv1.Nest{Relation: "owner", Target: &irv1.Nest_Union{Union: userRefID}}),
		view,
	)
	files, codes := generate(t, gadgetsBundle(&irv1.ShapeLayer{Shapes: shapes, Unions: []*irv1.RelationUnion{ref}}), everyTypeOptions)
	if len(codes) != 0 {
		t.Fatalf("codes %q", codes)
	}
	for file, parts := range map[string][]string{
		"gadget.dql.go": {
			"// DecodeGadgetFull decodes r, a row of gadget.full.flat. The row holds the shape's\n// fields in order, then its nests. A value that breaks what a field declares\n// or asserts is a *dql.DecodeError, never a zero value (§13).\n" +
				"func DecodeGadgetFull(r dql.Row) (GadgetFull, error) {\n\td := dql.NewDecoder(\"gadget.full.flat\", r, 5)\n\tvar out GadgetFull\n" +
				"\tout.ID = dql.Field(d, 0, \"id\", \"\", dql.Int[int64])\n" +
				"\tout.Every = dql.Field(d, 1, \"every\", \"\", dqlDecodeEvery)\n" +
				"\tout.Title = dql.Field(d, 2, \"title\", \"core.nonnull from COMMENT ON COLUMN app.gadgets.title\", dql.String)\n" +
				"\tout.Nick = dql.Nullable(d, 3, \"nick\", dql.String)\n" +
				"\tout.OwnerID = dql.Field(d, 4, \"owner_id\", \"nonnull of shape gadget.full.flat\", dql.Int[int64])\n" +
				"\treturn out, d.Err()\n}",
			"\td := dql.NewDecoder(\"gadget.full.nested\", r, 8)\n",
			"\tout.Parts = dql.Many(d, 5, \"parts\", DecodeUserFull)\n" +
				"\tout.Owner = dql.Field(d, 6, \"owner\", \"\", dql.Nest(DecodeUserFull))\n" +
				"\tout.Buddy = dql.Nullable(d, 7, \"buddy\", dql.Nest(DecodeUserFull))\n",
		},
		"dql-shapes.dql.go": {
			"\tout.ID = opt.Some(dql.Field(d, 0, \"id\", \"\", dql.Int[int64]))\n" +
				"\tout.Nick = dql.Field(d, 1, \"nick\", \"nonnull of shape gadget_named\", dql.String)\n" +
				"\tout.Pal = opt.Some(dql.Field(d, 2, \"pal\", \"\", dql.Nest(DecodeUserFull)))\n" +
				"\tout.Buddy = opt.Some(dql.Nullable(d, 3, \"buddy\", dql.Nest(DecodeUserFull)))\n",
		},
		"dql-types.dql.go": {
			"// dqlDecodeEvery decodes a value of composite type shop.app.every:\n// a dql.Row of its attributes in order (§13).\nfunc dqlDecodeEvery(v any) (Every, error) {\n\td := dql.NewDecoder(\"\", v, 46)\n",
			"\tout.A0 = dql.Field(d, 0, \"a0\", \"core.nonnull on shop.app.every.a0\", dql.Bool)\n",
			"(d, 1, \"a1\", \"core.nonnull on shop.app.every.a1\", dql.Int[int8])\n",
			"(d, 5, \"a5\", \"core.nonnull on shop.app.every.a5\", dql.Uint[uint8])\n",
			"(d, 9, \"a9\", \"core.nonnull on shop.app.every.a9\", dql.Float[float32])\n",
			"(d, 11, \"a11\", \"core.nonnull on shop.app.every.a11\", dql.As[dec.Decimal])\n",
			"(d, 13, \"a13\", \"core.nonnull on shop.app.every.a13\", dql.String)\n",
			"(d, 16, \"a16\", \"core.nonnull on shop.app.every.a16\", dql.Bytes)\n",
			"(d, 17, \"a17\", \"core.nonnull on shop.app.every.a17\", dql.As[time.Time])\n",
			"(d, 32, \"a32\", \"core.nonnull on shop.app.every.a32\", dql.As[dql.Range[civil.Date]])\n",
			"(d, 34, \"a34\", \"core.nonnull on shop.app.every.a34\", dql.Array(dql.NullableElem(dql.String)))\n",
			"(d, 35, \"a35\", \"core.nonnull on shop.app.every.a35\", dql.Array(dql.Array(dql.Elem(dql.String))))\n",
			"(d, 36, \"a36\", \"core.nonnull on shop.app.every.a36\", dql.Array(dql.Elem(dql.Int[int32])))\n",
			"(d, 37, \"a37\", \"core.nonnull on shop.app.every.a37\", dql.Weak(dql.Int[int64]))\n",
			"(d, 38, \"a38\", \"core.nonnull on shop.app.every.a38\", dql.Enum(Mood.Valid))\n",
			"(d, 39, \"a39\", \"core.nonnull on shop.app.every.a39\", dqlDecodeEmail)\n",
			"(d, 40, \"a40\", \"core.nonnull on shop.app.every.a40\", dqlDecodeEvery)\n",
			"(d, 41, \"a41\", \"core.nonnull on shop.app.every.a41\", dql.String)\n",
			"(d, 42, \"a42\", \"core.nonnull on shop.app.every.a42\", dql.As[pgtype.Point])\n",
			"(d, 44, \"a44\", \"core.nonnull on shop.app.every.a44\", dql.Bytes)\n",
			"\tout.Maybe = dql.Nullable(d, 45, \"maybe\", dql.Int[int64])\n",
			"// dqlDecodeEmail decodes a value of domain shop.app.email (§13).\nfunc dqlDecodeEmail(v any) (Email, error) {\n\tx, err := dql.String(v)\n\treturn Email(x), err\n}",
		},
		"user.dql.go": {
			"func DecodeUserFull(r dql.Row) (UserFull, error) {",
			"func DecodeUserKey(r dql.Row) (UserKey, error) {",
		},
	} {
		compile(t, files[file])
		for _, part := range parts {
			if !strings.Contains(files[file], part) {
				t.Errorf("%s lacks %q:\n%s", file, part, files[file])
			}
		}
	}
	// Write shapes are never decoded.
	for _, absent := range []string{"func DecodeGadgetDraft", "func DecodeUserDraft"} {
		for name, src := range files {
			if strings.Contains(src, absent) {
				t.Errorf("%s has %s", name, absent)
			}
		}
	}
}

// §7.5: a shape or union that holds a sensitive field, itself or through
// what it nests, prints it as [REDACTED] through slog and fmt; one that
// holds none gets no printing methods.
func TestRedaction(t *testing.T) {
	users, ref := userShapes()
	shapes := append(users,
		shp(gadgetFull, "gadget full", "gadgets", []*irv1.Field{fl("id", tInt, false, false)}),
		shp(gadgetNested, "gadget full nested", "gadgets", []*irv1.Field{fl("id", tInt, false, false)},
			&irv1.Nest{Relation: "owner", Target: &irv1.Nest_Shape{Shape: userFull}}),
		shp(gadgetDraft, "gadget draft", "gadgets", []*irv1.Field{fl("title", tText, false, false)},
			&irv1.Nest{Relation: "owner", Target: &irv1.Nest_Union{Union: userRefID}}),
	)
	files, codes := generate(t, gadgetsBundle(&irv1.ShapeLayer{Shapes: shapes, Unions: []*irv1.RelationUnion{ref}}), everyTypeOptions)
	if len(codes) != 0 {
		t.Fatalf("codes %q", codes)
	}
	for file, parts := range map[string][]string{
		"user.dql.go": {
			"// LogValue logs f with its sensitive fields as [REDACTED] (§7.5).\nfunc (f UserFull) LogValue() slog.Value {\n\treturn slog.GroupValue(\n" +
				"\t\tslog.Any(\"ID\", f.ID),\n\t\tslog.String(\"Secret\", \"[REDACTED]\"),\n\t)\n}\n\n" +
				"// String formats f as LogValue does, so fmt never prints a sensitive\n// field (§7.5).\nfunc (f UserFull) String() string { return f.LogValue().String() }\n\n" +
				"// GoString formats f as String does, for %#v.\nfunc (f UserFull) GoString() string { return f.String() }",
			"func (d UserDraft) LogValue() slog.Value {",
			"func (r UserRef) LogValue() slog.Value {\n\treturn slog.GroupValue(\n\t\tslog.Any(\"Change\", r.change),\n\t\tslog.Any(\"Key\", r.key),\n\t\tslog.Any(\"Draft\", r.draft),\n\t)\n}",
		},
		"gadget.dql.go": {
			"func (n GadgetFullNested) LogValue() slog.Value {\n\treturn slog.GroupValue(\n\t\tslog.Any(\"ID\", n.ID),\n\t\tslog.Any(\"Owner\", n.Owner),\n\t)\n}",
			"func (d GadgetDraft) LogValue() slog.Value {",
		},
	} {
		compile(t, files[file])
		for _, part := range parts {
			if !strings.Contains(files[file], part) {
				t.Errorf("%s lacks %q:\n%s", file, part, files[file])
			}
		}
	}
	for _, absent := range []string{"func (k UserKey) LogValue", "func (f GadgetFull) LogValue"} {
		if strings.Contains(files["user.dql.go"]+files["gadget.dql.go"], absent) {
			t.Errorf("%s generated for a shape without sensitive fields", absent)
		}
	}
}

// A read shape that nests a change or a write shape has no row to decode,
// and a printing method that meets a field fails.
func TestDecodeFailures(t *testing.T) {
	users, ref := userShapes()
	stringField := fl("id", tInt, false, false)
	b := gadgetsBundle(&irv1.ShapeLayer{Shapes: append(users,
		shp(customID("gadget_union"), "gadget union", "gadgets", nil, &irv1.Nest{Relation: "owner", Target: &irv1.Nest_Union{Union: userRefID}}),
		shp(customID("gadget_write"), "gadget write", "gadgets", nil, &irv1.Nest{Relation: "owner", Target: &irv1.Nest_Shape{Shape: userDraft}}),
		shp(customID("gadget_string"), "gadget string", "gadgets", []*irv1.Field{stringField}, &irv1.Nest{Relation: "owner", Target: &irv1.Nest_Shape{Shape: userFull}}),
	), Unions: []*irv1.RelationUnion{ref}})
	gadgets := b.GetCatalogs()[0].GetNamespaces()[0].GetTables()[0]
	gadgets.GetColumns()[0].HostName = &irv1.Identifier{Words: []string{"string"}}
	o := everyTypeOptions
	o.Package = "p"
	_, ds := Generate(b, o)
	var messages []string
	for _, d := range ds {
		messages = append(messages, d.GetCode()+" "+displayName(d.GetTarget())+": "+d.GetMessage())
	}
	all := strings.Join(messages, "\n")
	for _, s := range []string{
		"go-decode-unsupported shop.app.gadgets.owner: read shape gadget_union nests a write shape or change through owner, which no row holds",
		"go-decode-unsupported shop.app.gadgets.owner: read shape gadget_write nests a write shape or change through owner, which no row holds",
		"go-name-collision shop.app.gadgets: the redacting String of gadget_string gets the Go method GadgetString.String, as a field does",
	} {
		if !strings.Contains(all, s) {
			t.Errorf("messages lack %q:\n%s", s, all)
		}
	}
}
