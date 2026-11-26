package gen

import (
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// vty is a type by its kind's name, or a domain by its name.
func vty(kind string) *irv1.Type {
	t := &irv1.Type{}
	switch kind {
	case "text":
		t.Kind = &irv1.Type_Text{Text: &irv1.Text{}}
	case "varchar":
		t.Kind = &irv1.Type_Varchar{Varchar: &irv1.Varchar{Length: 20}}
	case "int32":
		t.Kind = &irv1.Type_Int32{Int32: &irv1.Empty{}}
	case "int64":
		t.Kind = &irv1.Type_Int64{Int64: &irv1.Empty{}}
	case "uint16":
		t.Kind = &irv1.Type_Uint16{Uint16: &irv1.Empty{}}
	case "float64":
		t.Kind = &irv1.Type_Float64{Float64: &irv1.Empty{}}
	case "decimal":
		t.Kind = &irv1.Type_Decimal{Decimal: &irv1.Decimal{}}
	case "date":
		t.Kind = &irv1.Type_Date{Date: &irv1.Empty{}}
	case "local_time":
		t.Kind = &irv1.Type_LocalTime{LocalTime: &irv1.TimeType{}}
	case "local_datetime":
		t.Kind = &irv1.Type_LocalDatetime{LocalDatetime: &irv1.TimeType{}}
	case "instant":
		t.Kind = &irv1.Type_Instant{Instant: &irv1.TimeType{}}
	case "bool":
		t.Kind = &irv1.Type_Bool{Bool: &irv1.Empty{}}
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

func lengthOf(min, max *uint32, unit irv1.LengthUnit) *irv1.PortableConstraint {
	return &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Length{Length: &irv1.LengthConstraint{Min: min, Max: max, Unit: unit}}}
}

func rng(min, max *irv1.Value, minIn, maxIn bool) *irv1.PortableConstraint {
	return &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Range{Range: &irv1.RangeConstraint{Min: min, Max: max, MinInclusive: minIn, MaxInclusive: maxIn}}}
}

func membership(values ...*irv1.Value) *irv1.PortableConstraint {
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
		{"label", "label"}, {"word", "word"}, {"blob", "bytes"}, {"at", "instant"}, {"big", "int64"}} {
		cols = append(cols, col(c[0], vty(c[1])))
	}
	cols = append(cols, col("note", opaqueOf("citext", irv1.OpaqueFallback_OPAQUE_FALLBACK_STRING)))
	rel := func(stored string, k irv1.RelationKind) *irv1.Relation {
		return &irv1.Relation{Name: &irv1.Identifier{Stored: stored, Words: []string{stored}}, Kind: k}
	}
	ns := &irv1.Namespace{
		Name: &irv1.Identifier{Stored: "app"},
		Tables: []*irv1.Table{
			{Name: &irv1.Identifier{Stored: "things"}, HostName: &irv1.Identifier{Words: []string{"thing"}}, Columns: cols, Constraints: constraints,
				Relations: []*irv1.Relation{rel("parts", irv1.RelationKind_RELATION_KIND_ONE_TO_MANY), rel("partner", irv1.RelationKind_RELATION_KIND_ONE_TO_ONE),
					rel("owner", irv1.RelationKind_RELATION_KIND_MANY_TO_ONE), rel("links", irv1.RelationKind_RELATION_KIND_MANY_TO_MANY)}},
			{Name: &irv1.Identifier{Stored: "parts"}, HostName: &irv1.Identifier{Words: []string{"part"}},
				Columns:     []*irv1.Column{col("id", tInt), col("name", tText)},
				Constraints: []*irv1.Constraint{cons("parts_name_check", notEmpty)}},
			{Name: &irv1.Identifier{Stored: "users"}, HostName: &irv1.Identifier{Words: []string{"user"}}, Columns: []*irv1.Column{col("id", tInt)}},
		},
		Domains: []*irv1.Domain{
			{Name: &irv1.Identifier{Stored: "word"}, HostName: &irv1.Identifier{Words: []string{"word"}}, Base: tText,
				Constraints: []*irv1.Constraint{cons("word_check", lengthOf(nil, proto.Uint32(10), irv1.LengthUnit_LENGTH_UNIT_BYTES)),
					{Name: "word_db_only", Form: &irv1.Constraint_Opaque{Opaque: &irv1.NativeText{Native: "VALUE <> 'x'"}}}}},
			{Name: &irv1.Identifier{Stored: "label"}, HostName: &irv1.Identifier{Words: []string{"label"}}, Base: vty("word"),
				Constraints: []*irv1.Constraint{cons("label_check", pattern(`^[a-z]+$`))}},
			// A domain no check takes still gets a constructor.
			{Name: &irv1.Identifier{Stored: "digest"}, HostName: &irv1.Identifier{Words: []string{"digest"}}, Base: tBytes},
		},
	}
	b := shapeBundle(nil)
	b.Catalogs[0].Namespaces = []*irv1.Namespace{ns}
	b.Shapes = layer
	return b
}

// The constraint of each kind on each type a check takes.
func thingConstraints() []*irv1.Constraint {
	return []*irv1.Constraint{
		cons("things_name_check", lengthOf(proto.Uint32(1), proto.Uint32(200), irv1.LengthUnit_LENGTH_UNIT_CHARACTERS)),
		cons("things_name_not_empty", notEmpty),
		cons("things_name_min", lengthOf(proto.Uint32(2), nil, irv1.LengthUnit_LENGTH_UNIT_CHARACTERS)),
		cons("code_shape", pattern(`^[A-Z]{3}$`)),
		cons("things_qty_check", rng(vInt(0), vUint(100), true, true)),
		cons("things_qty_in", membership(vInt(1), vUint(2), vDec("3"), vFlt(4))),
		cons("things_size_check", rng(nil, vUint(10), false, true)),
		cons("things_ratio_check", rng(vFlt(0), vDec("1.5"), false, false)),
		cons("things_price_check", rng(vDec("0"), vInt(1000), true, false)),
		cons("things_price_digits", digits(5, 2)),
		cons("things_day_check", rng(vStr("2000-01-01"), nil, true, false)),
		cons("things_day_in", membership(vStr("2000-01-01"))),
		cons("things_opens_check", rng(vStr("09:00:00"), vStr("17:30:00"), true, true)),
		cons("things_stamp_check", rng(nil, vStr("2030-01-01T00:00:00"), false, false)),
		cons("things_flag_check", membership(vBool(true))),
		cons("things_note_check", lengthOf(nil, proto.Uint32(50), irv1.LengthUnit_LENGTH_UNIT_UTF16_CODE_UNITS)),
		cons("things_note_in", membership(vStr("a"), vStr("b\u2028"))),
		cons("things_big_check", rng(vInt(-5), vUint(1<<62), true, true)),
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
	variant := func(change irv1.RelationChange, key, payload *irv1.ShapeId) *irv1.RelationVariant {
		return &irv1.RelationVariant{Change: change, Key: key, Payload: payload}
	}
	unions := []*irv1.RelationUnion{
		{Id: &irv1.RelationUnionId{Kind: irv1.RelationUnionKind_RELATION_UNION_CHILD_CHANGE, Entity: qn("parts")}, HostName: &irv1.Identifier{Words: []string{"part", "change"}},
			Variants: []*irv1.RelationVariant{
				variant(irv1.RelationChange_RELATION_CHANGE_CREATE, nil, partDraft),
				variant(irv1.RelationChange_RELATION_CHANGE_UPDATE, partKey, partPartial),
				variant(irv1.RelationChange_RELATION_CHANGE_DELETE, partKey, nil),
			}},
		{Id: &irv1.RelationUnionId{Kind: irv1.RelationUnionKind_RELATION_UNION_REF, Entity: qn("users")}, HostName: &irv1.Identifier{Words: []string{"user", "ref"}},
			Variants: []*irv1.RelationVariant{variant(irv1.RelationChange_RELATION_CHANGE_LINK, userKey, nil)}},
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
		vfl("name", vty("text"), false, false, "things_name_check", "things_name_not_empty", "things_name_min"),
		vfl("code", vty("varchar"), true, false, "code_shape"),
		vfl("qty", vty("int32"), false, true, "things_qty_check", "things_qty_in"),
		vfl("size", vty("uint16"), true, true, "things_size_check"),
		vfl("ratio", vty("float64"), false, false, "things_ratio_check"),
		vfl("price", vty("decimal"), false, false, "things_price_check", "things_price_digits"),
		vfl("day", vty("date"), false, false, "things_day_check", "things_day_in"),
		vfl("opens", vty("local_time"), false, false, "things_opens_check"),
		vfl("stamp", vty("local_datetime"), false, false, "things_stamp_check"),
		vfl("flag", vty("bool"), false, false, "things_flag_check"),
		vfl("label", vty("label"), false, false, "label_check", "word_check"),
		vfl("note", opaqueOf("citext", irv1.OpaqueFallback_OPAQUE_FALLBACK_STRING), false, false, "things_note_check", "things_note_in"),
		vfl("big", vty("int64"), false, false, "things_big_check"),
		fl("blob", vty("bytes"), false, false),
	},
		nestOf("parts", partDraft, false, false),
		nestOf("partner", partDraft, true, true),
		unionNest("owner", irv1.RelationUnionKind_RELATION_UNION_REF, "users", false),
	)
	partial := shp(thingPartial, "thing partial", "things", []*irv1.Field{
		vfl("name", vty("text"), true, false, "things_name_check"),
		vfl("word", vty("word"), true, true, "word_check"),
	},
		unionNest("parts", irv1.RelationUnionKind_RELATION_UNION_CHILD_CHANGE, "parts", true),
		nestOf("partner", partPartial, false, true),
		nestOf("owner", userKey, false, false),
	)
	// A custom shape derived from a Draft writes, and its optional to-one
	// nest of a Draft validates; one with no checks still validates.
	card := shp(customID("thing_card"), "thing card", "things", []*irv1.Field{vfl("name", vty("text"), false, false, "things_name_check")},
		nestOf("partner", partDraft, true, false))
	card.Lineage = []*irv1.LineageStep{{Op: irv1.ShapeOp_PRESET, Entity: qn("things"), Preset: irv1.Preset_PRESET_DRAFT, Form: irv1.Form_FORM_FLAT}}
	bare := shp(customID("thing_bare"), "thing bare", "things", []*irv1.Field{fl("blob", vty("bytes"), false, false)})
	bare.Lineage = card.Lineage
	files, codes := generate(t, thingsBundle(thingConstraints(), &irv1.ShapeLayer{Shapes: append(parts, draft, partial, card, bare), Unions: unions}), Options{})
	if len(codes) != 0 {
		t.Fatalf("codes %q", codes)
	}
	for file, parts := range map[string][]string{
		"dql-checks.dql.ts": {
			`import { Decimal, DigitsCheck, LengthCheck, MembershipCheck, NotEmptyCheck, PatternCheck, RangeCheck, Temporal } from "@dql/runtime";`,
			// The domain's constructor declares its checks first.
			"/** dqlWordCheck checks shop.app.word.word_check. */\nexport const dqlWordCheck = new LengthCheck(\"shop.app.word.word_check\", \"bytes\", undefined, 10);",
			`export const dqlLabelCheck = new PatternCheck("shop.app.label.label_check", "^[a-z]+$", /^[a-z]+$/u);`,
			`export const dqlPartsNameCheck = new NotEmptyCheck("shop.app.parts.parts_name_check");`,
			`export const dqlThingsNameCheck = new LengthCheck("shop.app.things.things_name_check", "characters", 1, 200);`,
			`export const dqlThingsNameMin = new LengthCheck("shop.app.things.things_name_min", "characters", 2);`,
			// A name that does not start with its table's gets them.
			`export const dqlThingsCodeShape = new PatternCheck("shop.app.things.code_shape", "^[A-Z]{3}$", /^[A-Z]{3}$/u);`,
			`export const dqlThingsQtyCheck = new RangeCheck<number>("shop.app.things.things_qty_check", { min: 0, max: 100, minInclusive: true, maxInclusive: true }, (a, b) => (a < b ? -1 : a > b ? 1 : 0));`,
			`export const dqlThingsQtyIn = new MembershipCheck<number>("shop.app.things.things_qty_in", [1, 2, 3, 4]);`,
			`export const dqlThingsSizeCheck = new RangeCheck<number>("shop.app.things.things_size_check", { max: 10, minInclusive: false, maxInclusive: true }, `,
			`{ min: 0, max: 1.5, minInclusive: false, maxInclusive: false }`,
			`export const dqlThingsPriceCheck = new RangeCheck<Decimal>("shop.app.things.things_price_check", { min: Decimal.parse("0"), max: Decimal.parse("1000"), minInclusive: true, maxInclusive: false }, (a, b) => a.cmp(b));`,
			`export const dqlThingsPriceDigits = new DigitsCheck("shop.app.things.things_price_digits", 5, 2);`,
			`export const dqlThingsDayCheck = new RangeCheck<Temporal.PlainDate>("shop.app.things.things_day_check", { min: Temporal.PlainDate.from("2000-01-01"), minInclusive: true, maxInclusive: false }, Temporal.PlainDate.compare);`,
			`export const dqlThingsDayIn = new MembershipCheck<Temporal.PlainDate>("shop.app.things.things_day_in", [Temporal.PlainDate.from("2000-01-01")], (a, b) => a.equals(b));`,
			`{ min: Temporal.PlainTime.from("09:00:00"), max: Temporal.PlainTime.from("17:30:00"), minInclusive: true, maxInclusive: true }, Temporal.PlainTime.compare`,
			`{ max: Temporal.PlainDateTime.from("2030-01-01T00:00:00"), minInclusive: false, maxInclusive: false }`,
			`export const dqlThingsFlagCheck = new MembershipCheck<boolean>("shop.app.things.things_flag_check", [true]);`,
			`export const dqlThingsNoteCheck = new LengthCheck("shop.app.things.things_note_check", "utf16_code_units", undefined, 50);`,
			"export const dqlThingsNoteIn = new MembershipCheck<string>(\"shop.app.things.things_note_in\", [\"a\", \"b\\u2028\"]);",
			`export const dqlThingsBigCheck = new RangeCheck<bigint>("shop.app.things.things_big_check", { min: -5n, max: 4611686018427387904n, minInclusive: true, maxInclusive: true }, `,
		},
		"dql-types.dql.ts": {
			"/**\n * parseWord returns x as Word when it passes the domain's\n * constraints (§8.5, §10.5), and throws a ValidationError otherwise.\n */\n" +
				"export function parseWord(x: string): Word {\n  const issues: ValidationIssue[] = [];\n" +
				"  dqlWordCheck.check(issues, \"shop.app.word\", \"\", x);\n  throwIfIssues(issues);\n  return x as Word;\n}",
			// A domain over a domain takes the base's type and runs the
			// outer domain's checks first.
			"export function parseLabel(x: string): Label {\n  const issues: ValidationIssue[] = [];\n" +
				"  dqlLabelCheck.check(issues, \"shop.app.label\", \"\", x);\n  dqlWordCheck.check(issues, \"shop.app.word\", \"\", x);\n",
			"export function parseDigest(x: Uint8Array): Digest {\n  return x as Digest;\n}",
		},
		"thing.dql.ts": {
			"export function validateThingDraft(d: ThingDraft): ValidationIssue[] {\n  const v: ValidationIssue[] = [];\n" +
				"  dqlThingsNameCheck.check(v, \"thing.draft.flat\", \"name\", d.name);\n" +
				"  dqlThingsNameNotEmpty.check(v, \"thing.draft.flat\", \"name\", d.name);\n" +
				"  dqlThingsNameMin.check(v, \"thing.draft.flat\", \"name\", d.name);\n" +
				"  if (d.code !== undefined) {\n    dqlThingsCodeShape.check(v, \"thing.draft.flat\", \"code\", d.code);\n  }\n" +
				"  if (d.qty !== null) {\n    dqlThingsQtyCheck.check(v, \"thing.draft.flat\", \"qty\", d.qty);\n    dqlThingsQtyIn.check(v, \"thing.draft.flat\", \"qty\", d.qty);\n  }\n" +
				"  if (d.size !== undefined && d.size !== null) {\n    dqlThingsSizeCheck.check(v, \"thing.draft.flat\", \"size\", d.size);\n  }\n",
			"  dqlLabelCheck.check(v, \"thing.draft.flat\", \"label\", d.label);\n  dqlWordCheck.check(v, \"thing.draft.flat\", \"label\", d.label);\n",
			"  for (const [i, x] of d.parts.entries()) {\n    v.push(...issuesAt(validatePartDraft(x), \"parts[\" + i + \"]\"));\n  }\n" +
				"  if (d.partner !== undefined) {\n    if (d.partner !== null) v.push(...issuesAt(validatePartDraft(d.partner), \"partner\"));\n  }\n" +
				"  return v;\n}",
			"export function validateThingPartial(p: ThingPartial): ValidationIssue[] {\n  const v: ValidationIssue[] = [];\n" +
				"  if (p.name !== undefined) {\n    dqlThingsNameCheck.check(v, \"thing.partial.flat\", \"name\", p.name);\n  }\n" +
				"  if (p.word !== undefined && p.word !== null) {\n    dqlWordCheck.check(v, \"thing.partial.flat\", \"word\", p.word);\n  }\n" +
				"  if (p.parts !== undefined) {\n    for (const [i, x] of p.parts.entries()) {\n      v.push(...issuesAt(validatePartChange(x), \"parts[\" + i + \"]\"));\n    }\n  }\n" +
				"  if (p.partner !== null) v.push(...issuesAt(validatePartPartial(p.partner), \"partner\"));\n  return v;\n}",
			`import { dqlLabelCheck, `,
			`import { type PartChange, type PartDraft, type PartPartial, validatePartChange, validatePartDraft, validatePartPartial } from "./part.dql.ts";`,
		},
		"dql-shapes.dql.ts": {
			"export function validateThingCard(c: ThingCard): ValidationIssue[] {\n  const v: ValidationIssue[] = [];\n" +
				"  dqlThingsNameCheck.check(v, \"thing_card\", \"name\", c.name);\n" +
				"  if (c.partner !== undefined) {\n    v.push(...issuesAt(validatePartDraft(c.partner), \"partner\"));\n  }\n  return v;\n}",
			"export function validateThingBare(b: ThingBare): ValidationIssue[] {\n  const v: ValidationIssue[] = [];\n  return v;\n}",
		},
		"part.dql.ts": {
			"/** validatePartChange checks the Draft c creates or the Partial it updates (§10.5). */\n" +
				"export function validatePartChange(c: PartChange): ValidationIssue[] {\n" +
				"  if (c.kind === \"create\") return validatePartDraft(c.draft);\n" +
				"  if (c.kind === \"update\") return validatePartPartial(c.partial);\n  return [];\n}",
		},
	} {
		for _, part := range parts {
			if !strings.Contains(files[file], part) {
				t.Errorf("%s lacks %q:\n%s", file, part, files[file])
			}
		}
	}
	// A read shape has no validator, a union that only links has none, the
	// Key a Partial nests is not validated, and the checks stay out of the
	// index.
	for file, absent := range map[string]string{
		"part.dql.ts":  "validatePartFull",
		"user.dql.ts":  "validateUserRef",
		"thing.dql.ts": "p.owner",
		"index.dql.ts": "dql-checks",
	} {
		if strings.Contains(files[file], absent) {
			t.Errorf("%s has %q:\n%s", file, absent, files[file])
		}
	}
	typecheck(t, files)
}

// Checks a field's type cannot take, bounds and values not of the type, a
// regex outside the subset, an unknown portable kind and a missing
// constraint.
func TestValidationFailures(t *testing.T) {
	bad := []*irv1.Constraint{
		cons("blob_len", lengthOf(nil, proto.Uint32(1), irv1.LengthUnit_LENGTH_UNIT_BYTES)),
		cons("no_unit", lengthOf(nil, proto.Uint32(1), irv1.LengthUnit_LENGTH_UNIT_UNSPECIFIED)),
		cons("blob_not_empty", notEmpty),
		cons("blob_pattern", pattern("x")),
		cons("bad_regex", pattern("(")),
		cons("folded_regex", pattern("(?i)x")),
		cons("qty_digits", digits(1, 0)),
		cons("at_range", rng(vStr("2000-01-01T00:00:00Z"), nil, true, false)),
		cons("qty_frac", rng(vDec("1.5"), nil, true, false)),
		cons("qty_text", rng(nil, vStr("1"), false, true)),
		cons("qty_bad", rng(nil, vDec("x"), false, true)),
		cons("qty_ratio", rng(nil, vDec("1/2"), false, true)),
		cons("qty_unset", rng(&irv1.Value{}, nil, true, false)),
		cons("price_bool", rng(vBool(true), nil, true, false)),
		cons("price_ratio", rng(vDec("1/2"), nil, true, false)),
		cons("price_in", membership(vDec("1"))),
		cons("blob_in", membership(&irv1.Value{})),
		cons("name_in", membership(vInt(1))),
		cons("flag_in", membership(vStr("true"))),
		cons("day_in", membership(vInt(1))),
		cons("day_bad", membership(vStr("2000-13-01"))),
		cons("opens_bad", membership(vStr("25:00"))),
		cons("stamp_bad", membership(vStr("2000-01-01"))),
		cons("ratio_in", membership(vFlt(1), vBool(true))),
		cons("opaque", &irv1.PortableConstraint{}),
		{Name: "untouched", Form: &irv1.Constraint_Portable{Portable: digits(1, 0)}},
	}
	fields := []*irv1.Field{
		vfl("blob", vty("bytes"), false, false, "blob_len", "blob_not_empty", "blob_pattern", "blob_in", "opaque"),
		vfl("name", vty("text"), false, false, "no_unit", "bad_regex", "folded_regex", "name_in", "missing", "no_unit"),
		vfl("qty", vty("int32"), false, false, "qty_digits", "qty_frac", "qty_text", "qty_bad", "qty_ratio", "qty_unset"),
		vfl("at", vty("instant"), false, false, "at_range"),
		vfl("price", vty("decimal"), false, false, "price_bool", "price_ratio", "price_in"),
		vfl("flag", vty("bool"), false, false, "flag_in"),
		vfl("day", vty("date"), false, false, "day_in", "day_bad"),
		vfl("opens", vty("local_time"), false, false, "opens_bad"),
		vfl("stamp", vty("local_datetime"), false, false, "stamp_bad"),
		vfl("ratio", vty("float64"), false, false, "ratio_in"),
		vfl("gone", vty("text"), false, false, "missing"),
	}
	b := thingsBundle(bad, &irv1.ShapeLayer{Shapes: []*irv1.Shape{shp(thingDraft, "thing draft", "things", fields)}})
	_, ds := Generate(b, Options{})
	var messages []string
	for _, d := range ds {
		messages = append(messages, d.GetCode()+" "+displayName(d.GetTarget())+": "+d.GetMessage())
	}
	all := strings.Join(messages, "\n")
	for _, s := range []string{
		"ts-validation-unsupported shop.app.things.blob: constraint shop.app.things.blob_len is client-validated, but the ts emitter cannot check it: a length needs text and a unit (§10.2)",
		"shop.app.things.no_unit is client-validated, but the ts emitter cannot check it: a length needs text and a unit",
		"blob_not_empty is client-validated, but the ts emitter cannot check it: not_empty needs text",
		"blob_pattern is client-validated, but the ts emitter cannot check it: a pattern needs text",
		"bad_regex is client-validated, but the ts emitter cannot check it: its regex does not compile: error parsing regexp: missing closing ): `(`",
		"folded_regex is client-validated, but the ts emitter cannot check it: its regex is not in the portable subset: flags are not in the portable subset",
		"qty_digits is client-validated, but the ts emitter cannot check it: digits needs a decimal",
		"at_range is client-validated, but the ts emitter cannot check it: a range needs a number, a date or a time",
		"qty_frac is client-validated, but the ts emitter cannot check it: its bound 1.5 is not a value of its type",
		"qty_text is client-validated, but the ts emitter cannot check it: its bound \"1\" is not a value of its type",
		"qty_bad is client-validated, but the ts emitter cannot check it: its bound x is not a value of its type",
		"qty_ratio is client-validated, but the ts emitter cannot check it: its bound 1/2 is not a value of its type",
		"qty_unset is client-validated, but the ts emitter cannot check it: its bound (unset) is not a value of its type",
		"price_bool is client-validated, but the ts emitter cannot check it: its bound true is not a value of its type",
		"price_ratio is client-validated, but the ts emitter cannot check it: its bound 1/2 is not a value of its type",
		"price_in is client-validated, but the ts emitter cannot check it: membership needs a comparable value",
		"blob_in is client-validated, but the ts emitter cannot check it: membership needs a comparable value",
		"name_in is client-validated, but the ts emitter cannot check it: its value 1 is not a value of its type",
		"flag_in is client-validated, but the ts emitter cannot check it: its value \"true\" is not a value of its type",
		"day_in is client-validated, but the ts emitter cannot check it: its value 1 is not a value of its type",
		"day_bad is client-validated, but the ts emitter cannot check it: its value \"2000-13-01\" is not a value of its type",
		"opens_bad is client-validated, but the ts emitter cannot check it: its value \"25:00\" is not a value of its type",
		"stamp_bad is client-validated, but the ts emitter cannot check it: its value \"2000-01-01\" is not a value of its type",
		"ratio_in is client-validated, but the ts emitter cannot check it: its value true is not a value of its type",
		"opaque is client-validated, but the ts emitter cannot check it: it is not a portable kind this emitter knows",
		"ts-reference-unknown shop.app.things.name: field name is checked against constraint missing, which neither its domains nor shop.app.things hold",
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

// A union whose payload the bundle does not hold, and a nest of a missing
// relation, are reported by the shapes; their validators skip them.
func TestValidatorMissing(t *testing.T) {
	parts, _ := partShapes()
	union := &irv1.RelationUnion{Id: &irv1.RelationUnionId{Kind: irv1.RelationUnionKind_RELATION_UNION_CHILD_CHANGE, Entity: qn("parts")},
		HostName: &irv1.Identifier{Words: []string{"part", "change"}},
		Variants: []*irv1.RelationVariant{{Change: irv1.RelationChange_RELATION_CHANGE_CREATE, Payload: customID("gone")}}}
	draft := shp(thingDraft, "thing draft", "things", []*irv1.Field{vfl("nope", vty("text"), false, false, "x")},
		unionNest("nope", irv1.RelationUnionKind_RELATION_UNION_CHILD_CHANGE, "parts", false))
	_, codes := generate(t, thingsBundle(nil, &irv1.ShapeLayer{Shapes: append(parts, draft), Unions: []*irv1.RelationUnion{union}}), Options{})
	want := []string{
		"SEVERITY_ERROR ts-reference-unknown shop.app.parts",
		"SEVERITY_ERROR ts-reference-unknown shop.app.things.nope",
		"SEVERITY_ERROR ts-reference-unknown shop.app.things.nope",
	}
	if !slices.Equal(codes, want) {
		t.Errorf("codes %q", codes)
	}
}

func TestJSString(t *testing.T) {
	if got := jsString("a\"<\u2028"); got != `"a\"<\u2028"` {
		t.Errorf("jsString = %s", got)
	}
}
