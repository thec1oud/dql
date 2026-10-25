package gen

import (
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func ty(kind string) *irv1.Type {
	t := &irv1.Type{}
	switch kind {
	case "text":
		t.Kind = &irv1.Type_Text{Text: &irv1.Text{}}
	case "varchar":
		t.Kind = &irv1.Type_Varchar{Varchar: &irv1.Varchar{Length: 20}}
	case "int32":
		t.Kind = &irv1.Type_Int32{Int32: e()}
	case "uint16":
		t.Kind = &irv1.Type_Uint16{Uint16: e()}
	case "float64":
		t.Kind = &irv1.Type_Float64{Float64: e()}
	case "decimal":
		t.Kind = &irv1.Type_Decimal{Decimal: &irv1.Decimal{}}
	case "date":
		t.Kind = &irv1.Type_Date{Date: e()}
	case "local_time":
		t.Kind = &irv1.Type_LocalTime{LocalTime: &irv1.TimeType{}}
	case "local_datetime":
		t.Kind = &irv1.Type_LocalDatetime{LocalDatetime: &irv1.TimeType{}}
	case "instant":
		t.Kind = &irv1.Type_Instant{Instant: &irv1.TimeType{}}
	case "bool":
		t.Kind = &irv1.Type_Bool{Bool: e()}
	case "bytes":
		t.Kind = &irv1.Type_Bytes{Bytes: &irv1.Bytes{}}
	default:
		t.Kind = &irv1.Type_Named{Named: &irv1.NamedRef{Kind: &irv1.NamedRef_Domain{Domain: qn(kind)}}}
	}
	return t
}

func vStr(s string) *irv1.Value  { return &irv1.Value{Kind: &irv1.Value_String_{String_: s}} }
func vInt(i int64) *irv1.Value   { return &irv1.Value{Kind: &irv1.Value_Int{Int: i}} }
func vUint(u uint64) *irv1.Value { return &irv1.Value{Kind: &irv1.Value_Uint{Uint: u}} }
func vDec(s string) *irv1.Value  { return &irv1.Value{Kind: &irv1.Value_Decimal{Decimal: s}} }
func vFlt(f float64) *irv1.Value { return &irv1.Value{Kind: &irv1.Value_Float{Float: f}} }
func vBool(b bool) *irv1.Value   { return &irv1.Value{Kind: &irv1.Value_Bool{Bool: b}} }

// cons is a client-validated check constraint of the given portable form.
func cons(name string, p *irv1.PortableConstraint) *irv1.Constraint {
	return &irv1.Constraint{Name: name, Kind: irv1.ConstraintKind_CONSTRAINT_KIND_CHECK, Form: &irv1.Constraint_Portable{Portable: p}, ClientValidated: true}
}

func length(min, max *uint32, unit irv1.LengthUnit) *irv1.PortableConstraint {
	return &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Length{Length: &irv1.LengthConstraint{Min: min, Max: max, Unit: unit}}}
}

func rng(min, max *irv1.Value, minIn, maxIn bool) *irv1.PortableConstraint {
	return &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Range{Range: &irv1.RangeConstraint{Min: min, Max: max, MinInclusive: minIn, MaxInclusive: maxIn}}}
}

func member(values ...*irv1.Value) *irv1.PortableConstraint {
	return &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Membership{Membership: &irv1.MembershipConstraint{Values: values}}}
}

func pattern(regex string) *irv1.PortableConstraint {
	return &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Pattern{Pattern: &irv1.PatternConstraint{Regex: regex}}}
}

func digits(p, s uint32) *irv1.PortableConstraint {
	return &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Digits{Digits: &irv1.DigitsConstraint{Precision: p, Scale: s}}}
}

var notEmpty = &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_NotEmpty{NotEmpty: &irv1.NotEmptyConstraint{}}}

// vfl is a field checked against the named constraints.
func vfl(col string, typ *irv1.Type, optional, nullable bool, validation ...string) *irv1.Field {
	f := fl(col, typ, optional, nullable)
	f.Validation = validation
	return f
}

// thingsBundle holds things, whose columns are of every type a check
// takes, with relations parts (one to many), partner (one to one) and owner
// (many to one) to parts and users; domain label over domain word over
// text; the given table constraints; and the given shape layer.
func thingsBundle(constraints []*irv1.Constraint, layer *irv1.ShapeLayer) *irv1.Bundle {
	var cols []*irv1.Column
	for _, c := range [][2]string{{"name", "text"}, {"code", "varchar"}, {"qty", "int32"}, {"size", "uint16"}, {"ratio", "float64"},
		{"price", "decimal"}, {"day", "date"}, {"opens", "local_time"}, {"stamp", "local_datetime"}, {"flag", "bool"},
		{"label", "label"}, {"word", "word"}, {"blob", "bytes"}, {"at", "instant"}} {
		cols = append(cols, attr(c[0], ty(c[1]), false))
	}
	cols = append(cols, attr("note", opaque("app", "citext", irv1.OpaqueFallback_OPAQUE_FALLBACK_STRING), false))
	rel := func(stored string, k irv1.RelationKind) *irv1.Relation {
		return &irv1.Relation{Name: &irv1.Identifier{Stored: stored, Words: []string{stored}}, Kind: k}
	}
	ns := &irv1.Namespace{
		Tables: []*irv1.Table{
			{Name: &irv1.Identifier{Stored: "things"}, HostName: &irv1.Identifier{Words: []string{"thing"}}, Columns: cols, Constraints: constraints,
				Relations: []*irv1.Relation{rel("parts", irv1.RelationKind_RELATION_KIND_ONE_TO_MANY), rel("partner", irv1.RelationKind_RELATION_KIND_ONE_TO_ONE),
					rel("owner", irv1.RelationKind_RELATION_KIND_MANY_TO_ONE)}},
			{Name: &irv1.Identifier{Stored: "parts"}, HostName: &irv1.Identifier{Words: []string{"part"}},
				Columns:     []*irv1.Column{attr("id", tInt, false), attr("name", tText, false)},
				Constraints: []*irv1.Constraint{cons("parts_name_check", notEmpty)}},
			{Name: &irv1.Identifier{Stored: "users"}, HostName: &irv1.Identifier{Words: []string{"user"}}, Columns: []*irv1.Column{attr("id", tInt, false)}},
		},
		Domains: []*irv1.Domain{
			{Name: &irv1.Identifier{Stored: "word"}, HostName: &irv1.Identifier{Words: []string{"word"}}, Base: tText,
				Constraints: []*irv1.Constraint{cons("word_check", length(nil, proto.Uint32(10), irv1.LengthUnit_LENGTH_UNIT_BYTES)),
					{Name: "word_db_only", Form: &irv1.Constraint_Opaque{Opaque: &irv1.NativeText{Native: "VALUE <> 'x'"}}}}},
			{Name: &irv1.Identifier{Stored: "label"}, HostName: &irv1.Identifier{Words: []string{"label"}}, Base: ty("word"),
				Constraints: []*irv1.Constraint{cons("label_check", pattern(`^[a-z]+$`))}},
			// A domain no check takes still gets a constructor.
			{Name: &irv1.Identifier{Stored: "digest"}, HostName: &irv1.Identifier{Words: []string{"digest"}}, Base: tBytes},
		},
	}
	b := bundle(ns)
	b.Shapes = layer
	return b
}

// The constraint of each kind on each type a check takes.
func thingConstraints() []*irv1.Constraint {
	return []*irv1.Constraint{
		cons("things_name_check", length(proto.Uint32(1), proto.Uint32(200), irv1.LengthUnit_LENGTH_UNIT_CHARACTERS)),
		cons("things_name_not_empty", notEmpty),
		cons("code_shape", pattern(`^[A-Z]{3}$`)),
		cons("things_qty_check", rng(vInt(0), vUint(100), true, true)),
		cons("things_qty_in", member(vInt(1), vUint(2), vDec("3"), vFlt(4))),
		cons("things_size_check", rng(nil, vUint(10), false, true)),
		cons("things_ratio_check", rng(vFlt(0), vDec("1.5"), false, false)),
		cons("things_price_check", rng(vDec("0"), vInt(1000), true, false)),
		cons("things_price_digits", digits(5, 2)),
		cons("things_day_check", rng(vStr("2000-01-01"), nil, true, false)),
		cons("things_opens_check", rng(vStr("09:00:00"), vStr("17:30:00"), true, true)),
		cons("things_stamp_check", rng(nil, vStr("2030-01-01T00:00:00"), false, false)),
		cons("things_flag_check", member(vBool(true))),
		cons("things_note_check", length(nil, proto.Uint32(50), irv1.LengthUnit_LENGTH_UNIT_UTF16_CODE_UNITS)),
		cons("things_note_in", member(vStr("a"), vStr("b"))),
	}
}

var (
	thingDraft   = presetID("things", irv1.Preset_PRESET_DRAFT, irv1.Form_FORM_FLAT)
	thingPartial = presetID("things", irv1.Preset_PRESET_PARTIAL, irv1.Form_FORM_FLAT)
	partDraft    = presetID("parts", irv1.Preset_PRESET_DRAFT, irv1.Form_FORM_FLAT)
	partPartial  = presetID("parts", irv1.Preset_PRESET_PARTIAL, irv1.Form_FORM_FLAT)
	partFull     = presetID("parts", irv1.Preset_PRESET_FULL, irv1.Form_FORM_FLAT)
	partKey      = presetID("parts", irv1.Preset_PRESET_KEY, irv1.Form_FORM_UNSPECIFIED)
	userKey      = presetID("users", irv1.Preset_PRESET_KEY, irv1.Form_FORM_UNSPECIFIED)
)

// The part and user shapes and unions a thing nests.
func partShapes() ([]*irv1.Shape, []*irv1.RelationUnion) {
	shapes := []*irv1.Shape{
		shp(partDraft, "part draft", "parts", []*irv1.Field{vfl("name", tText, false, false, "parts_name_check")}),
		shp(partPartial, "part partial", "parts", []*irv1.Field{vfl("name", tText, true, false, "parts_name_check")}),
		shp(partFull, "part full", "parts", []*irv1.Field{fl("id", tInt, false, false), fl("name", tText, false, false)}),
		shp(partKey, "part key", "parts", []*irv1.Field{fl("id", tInt, false, false)}),
		shp(userKey, "user key", "users", []*irv1.Field{fl("id", tInt, false, false)}),
	}
	variant := func(change irv1.RelationChange, words string, key, payload *irv1.ShapeId) *irv1.RelationVariant {
		return &irv1.RelationVariant{Change: change, HostName: &irv1.Identifier{Words: strings.Fields(words)}, Key: key, Payload: payload}
	}
	unions := []*irv1.RelationUnion{
		{Id: &irv1.RelationUnionId{Kind: irv1.RelationUnionKind_RELATION_UNION_CHILD_CHANGE, Entity: qn("parts")}, HostName: &irv1.Identifier{Words: []string{"part", "change"}},
			Variants: []*irv1.RelationVariant{
				variant(irv1.RelationChange_RELATION_CHANGE_CREATE, "part create", nil, partDraft),
				variant(irv1.RelationChange_RELATION_CHANGE_UPDATE, "part update", partKey, partPartial),
				variant(irv1.RelationChange_RELATION_CHANGE_DELETE, "part delete", partKey, nil),
			}},
		{Id: &irv1.RelationUnionId{Kind: irv1.RelationUnionKind_RELATION_UNION_REF, Entity: qn("users")}, HostName: &irv1.Identifier{Words: []string{"user", "ref"}},
			Variants: []*irv1.RelationVariant{variant(irv1.RelationChange_RELATION_CHANGE_LINK, "user link", userKey, nil)}},
	}
	return shapes, unions
}

func nestOf(relation string, id *irv1.ShapeId, optional, nullable bool) *irv1.Nest {
	n := &irv1.Nest{Relation: relation, Nullable: nullable, Target: &irv1.Nest_Shape{Shape: id}}
	if optional {
		n.Presence = irv1.Presence_PRESENCE_OPTIONAL
	}
	return n
}

func unionNest(relation string, kind irv1.RelationUnionKind, entity string, optional bool) *irv1.Nest {
	n := &irv1.Nest{Relation: relation, Target: &irv1.Nest_Union{Union: &irv1.RelationUnionId{Kind: kind, Entity: qn(entity)}}}
	if optional {
		n.Presence = irv1.Presence_PRESENCE_OPTIONAL
	}
	return n
}

// §10.2, §10.3, §10.5: a check of every portable kind on every type it
// takes, fields of every presence and nullability, nested shapes and unions,
// and the domain constructors, outermost domain first.
func TestValidators(t *testing.T) {
	parts, unions := partShapes()
	draft := shp(thingDraft, "thing draft", "things", []*irv1.Field{
		vfl("name", ty("text"), false, false, "things_name_check", "things_name_not_empty"),
		vfl("code", ty("varchar"), true, false, "code_shape"),
		vfl("qty", ty("int32"), false, true, "things_qty_check", "things_qty_in"),
		vfl("size", ty("uint16"), true, true, "things_size_check"),
		vfl("ratio", ty("float64"), false, false, "things_ratio_check"),
		vfl("price", ty("decimal"), false, false, "things_price_check", "things_price_digits"),
		vfl("day", ty("date"), false, false, "things_day_check"),
		vfl("opens", ty("local_time"), false, false, "things_opens_check"),
		vfl("stamp", ty("local_datetime"), false, false, "things_stamp_check"),
		vfl("flag", ty("bool"), false, false, "things_flag_check"),
		vfl("label", ty("label"), false, false, "label_check", "word_check"),
		vfl("note", opaque("app", "citext", irv1.OpaqueFallback_OPAQUE_FALLBACK_STRING), false, false, "things_note_check", "things_note_in"),
		fl("blob", ty("bytes"), false, false),
	},
		nestOf("parts", partDraft, false, false),
		nestOf("partner", partDraft, true, true),
		unionNest("owner", irv1.RelationUnionKind_RELATION_UNION_REF, "users", false),
	)
	partial := shp(thingPartial, "thing partial", "things", []*irv1.Field{
		vfl("name", ty("text"), true, false, "things_name_check"),
		vfl("word", ty("word"), true, true, "word_check"),
	},
		unionNest("parts", irv1.RelationUnionKind_RELATION_UNION_CHILD_CHANGE, "parts", true),
		nestOf("partner", partPartial, false, true),
		nestOf("owner", userKey, false, false),
	)
	// A custom shape derived from a Draft writes, and its optional to-one
	// nest of a Draft validates; one with no checks still validates.
	card := shp(customID("thing_card"), "thing card", "things", []*irv1.Field{vfl("name", ty("text"), false, false, "things_name_check")},
		nestOf("partner", partDraft, true, false))
	card.Lineage = []*irv1.LineageStep{{Op: irv1.ShapeOp_PRESET, Entity: qn("things"), Preset: irv1.Preset_PRESET_DRAFT, Form: irv1.Form_FORM_FLAT}}
	bare := shp(customID("thing_bare"), "thing bare", "things", []*irv1.Field{fl("blob", ty("bytes"), false, false)})
	bare.Lineage = card.Lineage
	files, codes := generate(t, thingsBundle(thingConstraints(), &irv1.ShapeLayer{Shapes: append(parts, draft, partial, card, bare), Unions: unions}), Options{})
	if len(codes) != 0 {
		t.Fatalf("codes %q", codes)
	}
	for file, parts := range map[string][]string{
		"dql-checks.dql.go": {
			"import (\n\t\"cmp\"\n\t\"regexp\"\n\n" +
				"\t\"github.com/thec1oud/dql/runtime/go/civil\"\n\t\"github.com/thec1oud/dql/runtime/go/dec\"\n\t\"github.com/thec1oud/dql/runtime/go/dql\"\n)",
			// The domain's constructor declares its checks first.
			"var dqlWordCheck = dql.LengthCheck{Constraint: \"shop.app.word.word_check\", Max: dql.Ptr(10), Unit: dql.UnitBytes}",
			"var dqlLabelCheck = dql.PatternCheck{Constraint: \"shop.app.label.label_check\", Regex: regexp.MustCompile(\"^[a-z]+$\")}",
			"var dqlPartsNameCheck = dql.NotEmptyCheck{Constraint: \"shop.app.parts.parts_name_check\"}",
			"var dqlThingsNameCheck = dql.LengthCheck{Constraint: \"shop.app.things.things_name_check\", Min: dql.Ptr(1), Max: dql.Ptr(200), Unit: dql.UnitCharacters}",
			// A name that does not start with its table's gets them.
			"var dqlThingsCodeShape = dql.PatternCheck{",
			"var dqlThingsQtyCheck = dql.RangeCheck[int32]{Constraint: \"shop.app.things.things_qty_check\", Min: dql.Ptr[int32](0), Max: dql.Ptr[int32](100), MinInclusive: true, MaxInclusive: true, Compare: cmp.Compare[int32]}",
			"var dqlThingsQtyIn = dql.MembershipCheck[int32]{Constraint: \"shop.app.things.things_qty_in\", Values: []int32{1, 2, 3, 4}}",
			"var dqlThingsSizeCheck = dql.RangeCheck[uint16]{Constraint: \"shop.app.things.things_size_check\", Max: dql.Ptr[uint16](10), MinInclusive: false, MaxInclusive: true, Compare: cmp.Compare[uint16]}",
			"var dqlThingsRatioCheck = dql.RangeCheck[float64]{Constraint: \"shop.app.things.things_ratio_check\", Min: dql.Ptr[float64](0), Max: dql.Ptr[float64](1.5), MinInclusive: false, MaxInclusive: false, Compare: cmp.Compare[float64]}",
			"var dqlThingsPriceCheck = dql.RangeCheck[dec.Decimal]{Constraint: \"shop.app.things.things_price_check\", Min: dql.Ptr[dec.Decimal](dec.MustParse(\"0\")), Max: dql.Ptr[dec.Decimal](dec.MustParse(\"1000\")), MinInclusive: true, MaxInclusive: false, Compare: dec.Decimal.Cmp}",
			"var dqlThingsPriceDigits = dql.DigitsCheck{Constraint: \"shop.app.things.things_price_digits\", Precision: 5, Scale: 2}",
			"var dqlThingsDayCheck = dql.RangeCheck[civil.Date]{Constraint: \"shop.app.things.things_day_check\", Min: dql.Ptr[civil.Date](dql.Must(civil.ParseDate(\"2000-01-01\"))), MinInclusive: true, MaxInclusive: false, Compare: civil.Date.Compare}",
			"Min: dql.Ptr[civil.Time](dql.Must(civil.ParseTime(\"09:00:00\"))), Max: dql.Ptr[civil.Time](dql.Must(civil.ParseTime(\"17:30:00\")))",
			"Max: dql.Ptr[civil.DateTime](dql.Must(civil.ParseDateTime(\"2030-01-01T00:00:00\")))",
			"var dqlThingsFlagCheck = dql.MembershipCheck[bool]{Constraint: \"shop.app.things.things_flag_check\", Values: []bool{true}}",
			"var dqlThingsNoteCheck = dql.LengthCheck{Constraint: \"shop.app.things.things_note_check\", Max: dql.Ptr(50), Unit: dql.UnitUTF16CodeUnits}",
			"var dqlThingsNoteIn = dql.MembershipCheck[string]{Constraint: \"shop.app.things.things_note_in\", Values: []string{\"a\", \"b\"}}",
		},
		"dql-types.dql.go": {
			"// ParseWord converts x to Word when it passes the\n// domain's constraints (§8.5, §10.5), and returns dql.ValidationErrors\n// otherwise.\n" +
				"func ParseWord(x string) (Word, error) {\n\tvar issues dql.ValidationErrors\n" +
				"\tdqlWordCheck.Check(&issues, \"shop.app.word\", \"\", x)\n" +
				"\tif err := issues.Err(); err != nil {\n\t\treturn *new(Word), err\n\t}\n\treturn Word(x), nil\n}",
			// A domain over a domain takes the base's Go type and runs the
			// outer domain's checks first.
			"func ParseLabel(x string) (Label, error) {\n\tvar issues dql.ValidationErrors\n" +
				"\tdqlLabelCheck.Check(&issues, \"shop.app.label\", \"\", x)\n\tdqlWordCheck.Check(&issues, \"shop.app.word\", \"\", x)\n",
			"func ParseDigest(x []byte) (Digest, error) {\n\tvar issues dql.ValidationErrors\n\tif err",
		},
		"thing.dql.go": {
			"func (d ThingDraft) Validate() error { return d.validate().Err() }",
			"func (d ThingDraft) validate() dql.ValidationErrors {\n\tvar v dql.ValidationErrors\n" +
				"\tdqlThingsNameCheck.Check(&v, \"thing.draft.flat\", \"name\", d.Name)\n" +
				"\tdqlThingsNameNotEmpty.Check(&v, \"thing.draft.flat\", \"name\", d.Name)\n" +
				"\tif x, ok := d.Code.Get(); ok {\n\t\tdqlThingsCodeShape.Check(&v, \"thing.draft.flat\", \"code\", x)\n\t}\n" +
				"\tif d.Qty != nil {\n\t\tdqlThingsQtyCheck.Check(&v, \"thing.draft.flat\", \"qty\", *d.Qty)\n\t\tdqlThingsQtyIn.Check(&v, \"thing.draft.flat\", \"qty\", *d.Qty)\n\t}\n" +
				"\tif x, ok := d.Size.Get(); ok && x != nil {\n\t\tdqlThingsSizeCheck.Check(&v, \"thing.draft.flat\", \"size\", *x)\n\t}\n",
			"\tdqlLabelCheck.Check(&v, \"thing.draft.flat\", \"label\", string(d.Label))\n\tdqlWordCheck.Check(&v, \"thing.draft.flat\", \"label\", string(d.Label))\n",
			"\tfor i, x := range d.Parts {\n\t\tv = append(v, x.validate().At(\"parts[\"+strconv.Itoa(i)+\"]\")...)\n\t}\n" +
				"\tif x, ok := d.Partner.Get(); ok {\n\t\tif x != nil {\n\t\t\tv = append(v, x.validate().At(\"partner\")...)\n\t\t}\n\t}\n" +
				"\tv = append(v, d.Owner.validate().At(\"owner\")...)\n\treturn v\n}",
			"func (p ThingPartial) validate() dql.ValidationErrors {\n\tvar v dql.ValidationErrors\n" +
				"\tif x, ok := p.Name.Get(); ok {\n\t\tdqlThingsNameCheck.Check(&v, \"thing.partial.flat\", \"name\", x)\n\t}\n" +
				"\tif x, ok := p.Word.Get(); ok && x != nil {\n\t\tdqlWordCheck.Check(&v, \"thing.partial.flat\", \"word\", string(*x))\n\t}\n" +
				"\tif xs, ok := p.Parts.Get(); ok {\n\t\tfor i, x := range xs {\n\t\t\tv = append(v, x.validate().At(\"parts[\"+strconv.Itoa(i)+\"]\")...)\n\t\t}\n\t}\n" +
				"\tif p.Partner != nil {\n\t\tv = append(v, p.Partner.validate().At(\"partner\")...)\n\t}\n\treturn v\n}",
		},
		"dql-shapes.dql.go": {
			"func (c ThingCard) validate() dql.ValidationErrors {\n\tvar v dql.ValidationErrors\n" +
				"\tdqlThingsNameCheck.Check(&v, \"thing_card\", \"name\", c.Name)\n" +
				"\tif x, ok := c.Partner.Get(); ok {\n\t\tv = append(v, x.validate().At(\"partner\")...)\n\t}\n\treturn v\n}",
			"func (b ThingBare) validate() dql.ValidationErrors {\n\tvar v dql.ValidationErrors\n\treturn v\n}",
		},
		"part.dql.go": {
			"func (c PartChange) validate() dql.ValidationErrors {\n" +
				"\tif c.change == dql.ChangeCreate {\n\t\treturn c.draft.validate()\n\t}\n" +
				"\tif c.change == dql.ChangeUpdate {\n\t\treturn c.partial.validate()\n\t}\n\treturn nil\n}",
		},
		"user.dql.go": {
			"func (r UserRef) validate() dql.ValidationErrors {\n\treturn nil\n}",
		},
	} {
		compile(t, files[file])
		for _, part := range parts {
			if !strings.Contains(files[file], part) {
				t.Errorf("%s lacks %q:\n%s", file, part, files[file])
			}
		}
	}
	// A read shape has no validator, and the Key a Partial nests is not
	// validated.
	if strings.Contains(files["part.dql.go"], "func (f PartFull) Validate") || strings.Contains(files["thing.dql.go"], "p.Owner.validate") {
		t.Errorf("read shapes validate:\n%s", files["part.dql.go"])
	}
}

// Checks a field's type cannot take, bounds and values not of the type, a
// regex Go cannot compile, an unknown portable kind, a missing constraint,
// and a Validate that meets a field.
func TestValidationFailures(t *testing.T) {
	bad := []*irv1.Constraint{
		cons("blob_len", length(nil, proto.Uint32(1), irv1.LengthUnit_LENGTH_UNIT_BYTES)),
		cons("no_unit", length(nil, proto.Uint32(1), irv1.LengthUnit_LENGTH_UNIT_UNSPECIFIED)),
		cons("blob_not_empty", notEmpty),
		cons("blob_pattern", pattern("x")),
		cons("bad_regex", pattern("(")),
		cons("qty_digits", digits(1, 0)),
		cons("at_range", rng(vStr("2000-01-01T00:00:00Z"), nil, true, false)),
		cons("qty_frac", rng(vDec("1.5"), nil, true, false)),
		cons("qty_text", rng(nil, vStr("1"), false, true)),
		cons("qty_bad", rng(nil, vDec("x"), false, true)),
		cons("qty_unset", rng(&irv1.Value{}, nil, true, false)),
		cons("price_bool", rng(vBool(true), nil, true, false)),
		cons("price_in", member(vDec("1"))),
		cons("blob_in", member(&irv1.Value{})),
		cons("name_in", member(vInt(1))),
		cons("flag_in", member(vStr("true"))),
		cons("day_in", member(vInt(1))),
		cons("day_bad", member(vStr("2000-13-01"))),
		cons("ratio_in", member(vFlt(1), vBool(true))),
		cons("opaque", &irv1.PortableConstraint{}),
		{Name: "untouched", Form: &irv1.Constraint_Portable{Portable: digits(1, 0)}},
	}
	fields := []*irv1.Field{
		vfl("blob", ty("bytes"), false, false, "blob_len", "blob_not_empty", "blob_pattern", "blob_in", "opaque"),
		vfl("name", ty("text"), false, false, "no_unit", "bad_regex", "name_in", "missing", "no_unit"),
		vfl("qty", ty("int32"), false, false, "qty_digits", "qty_frac", "qty_text", "qty_bad", "qty_unset"),
		vfl("at", ty("instant"), false, false, "at_range"),
		vfl("price", ty("decimal"), false, false, "price_bool", "price_in"),
		vfl("flag", ty("bool"), false, false, "flag_in"),
		vfl("day", ty("date"), false, false, "day_in", "day_bad"),
		vfl("ratio", ty("float64"), false, false, "ratio_in"),
	}
	// A field named validate meets nothing, a field named Validate meets the
	// validator.
	clash := shp(thingPartial, "thing partial", "things", []*irv1.Field{fl("validate", ty("text"), false, false)})
	clash.GetFields()[0].Column = "Validate"
	b := thingsBundle(bad, &irv1.ShapeLayer{Shapes: []*irv1.Shape{shp(thingDraft, "thing draft", "things", fields)}})
	b.GetCatalogs()[0].GetNamespaces()[0].GetTables()[0].Columns = append(b.GetCatalogs()[0].GetNamespaces()[0].GetTables()[0].GetColumns(),
		attr("Validate", ty("text"), false, "validate"))
	b.Shapes.Shapes = append(b.Shapes.Shapes, clash)
	_, ds := Generate(b, Options{Package: "p"})
	var messages []string
	for _, d := range ds {
		messages = append(messages, d.GetCode()+" "+displayName(d.GetTarget())+": "+d.GetMessage())
	}
	all := strings.Join(messages, "\n")
	for _, s := range []string{
		"go-validation-unsupported shop.app.things.blob: constraint shop.app.things.blob_len is client-validated, but the go emitter cannot check it: a length needs text and a unit (§10.2)",
		"shop.app.things.no_unit is client-validated, but the go emitter cannot check it: a length needs text and a unit",
		"blob_not_empty is client-validated, but the go emitter cannot check it: not_empty needs text",
		"blob_pattern is client-validated, but the go emitter cannot check it: a pattern needs text",
		"bad_regex is client-validated, but the go emitter cannot check it: its regex does not compile: error parsing regexp: missing closing ): `(`",
		"qty_digits is client-validated, but the go emitter cannot check it: digits needs a decimal",
		"at_range is client-validated, but the go emitter cannot check it: a range needs a number, a date or a time",
		"qty_frac is client-validated, but the go emitter cannot check it: its bound 1.5 is not a value of its type",
		"qty_text is client-validated, but the go emitter cannot check it: its bound \"1\" is not a value of its type",
		"qty_bad is client-validated, but the go emitter cannot check it: its bound x is not a value of its type",
		"qty_unset is client-validated, but the go emitter cannot check it: its bound (unset) is not a value of its type",
		"price_bool is client-validated, but the go emitter cannot check it: its bound true is not a value of its type",
		"price_in is client-validated, but the go emitter cannot check it: membership needs a comparable value",
		"blob_in is client-validated, but the go emitter cannot check it: membership needs a comparable value",
		"name_in is client-validated, but the go emitter cannot check it: its value 1 is not a value of its type",
		"flag_in is client-validated, but the go emitter cannot check it: its value \"true\" is not a value of its type",
		"day_in is client-validated, but the go emitter cannot check it: its value 1 is not a value of its type",
		"day_bad is client-validated, but the go emitter cannot check it: its value \"2000-13-01\" is not a value of its type",
		"ratio_in is client-validated, but the go emitter cannot check it: its value true is not a value of its type",
		"opaque is client-validated, but the go emitter cannot check it: it is not a portable kind this emitter knows",
		"go-reference-unknown shop.app.things.name: field name is checked against constraint missing, which neither its domains nor shop.app.things hold",
		"go-name-collision",
	} {
		if !strings.Contains(all, s) {
			t.Errorf("messages lack %q:\n%s", s, all)
		}
	}
	// no_unit fails once, though name lists it twice.
	if n := strings.Count(all, "no_unit is"); n != 1 {
		t.Errorf("no_unit reported %d times:\n%s", n, all)
	}
	if slices.ContainsFunc(messages, func(m string) bool { return strings.Contains(m, "untouched") }) {
		t.Errorf("an unlisted constraint was checked:\n%s", all)
	}
}
