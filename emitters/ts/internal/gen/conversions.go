package gen

import (
	"cmp"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// CodeConversionUnsupported: a conversion or merge the emitter cannot
// generate as the kind the core computed says.
const CodeConversionUnsupported = "ts-conversion-unsupported"

// member is one function of a shape's companion object: its name, what
// declares it, for diagnostics, and its text, indented as a member.
type member struct {
	name, what, text string
}

// conversions generates every conversion the shape layer lists, as its kind
// dictates (§14.9): a function on the source shape's companion object for
// Total, Needs inputs, Fallible and With base, and nothing for Via database
// and Impossible.
func (g *generator) conversions() {
	for _, c := range g.b.GetShapes().GetConversions() {
		from, to := g.ix.shapes[shapeKey(c.GetFrom())], g.ix.shapes[shapeKey(c.GetTo())]
		if from == nil || to == nil {
			g.errorf(CodeReferenceUnknown, cmp.Or(c.GetFrom().GetEntity(), c.GetTo().GetEntity()),
				"the conversion from %s to %s references a shape the bundle does not hold", g.dotted(c.GetFrom()), g.dotted(c.GetTo()))
			continue
		}
		switch c.GetKind() {
		case irv1.ConversionKind_CONVERSION_TOTAL, irv1.ConversionKind_CONVERSION_NEEDS_INPUTS, irv1.ConversionKind_CONVERSION_FALLIBLE:
			g.convert(c, from, to)
		case irv1.ConversionKind_CONVERSION_WITH_BASE:
			g.apply(from, to)
		case irv1.ConversionKind_CONVERSION_VIA_DATABASE, irv1.ConversionKind_CONVERSION_IMPOSSIBLE:
			// No function: a write returns the result, or nothing can.
		default:
			g.errorf(CodeConversionUnsupported, from.GetEntity(),
				"the conversion from %s to %s is %s, which the ts emitter does not generate; the core classifies shape conversions as total, needs inputs, fallible, with base, via database or impossible (§14.9)",
				g.dotted(from.GetId()), g.dotted(to.GetId()), c.GetKind())
		}
	}
}

// samePreset reports whether to is a preset shape of from's entity.
func samePreset(from, to *irv1.Shape) bool {
	return to.GetId().GetCustom() == "" && proto.Equal(from.GetEntity(), to.GetEntity())
}

// lowerFirst lower-cases a name's first letter: ToPartial is toPartial.
func lowerFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToLower(r)) + s[n:]
}

// conversionName names a conversion's member as Go names its method (§14.9),
// in camel case: key for a Key, flat and nests for a Nested shape's own
// forms, applyTo and applyTo plus the target for With base, else to plus the
// target. A preset of the same entity is named without the entity's words
// (toPartial, applyToDraft); a custom shape keeps its name (toUserSummary).
func (g *generator) conversionName(c *irv1.Conversion, from, to *irv1.Shape) string {
	suffix := TypeName(to.GetHostName().GetWords())
	id := to.GetId()
	if samePreset(from, to) {
		suffix = strings.TrimPrefix(suffix, TypeName(g.ix.tables[keyOf(to.GetEntity())].GetHostName().GetWords()))
		switch {
		case c.GetKind() == irv1.ConversionKind_CONVERSION_WITH_BASE && id.GetPreset() == irv1.Preset_PRESET_FULL && id.GetForm() == irv1.Form_FORM_FLAT:
			return "applyTo"
		case id.GetPreset() == irv1.Preset_PRESET_KEY:
			return "key"
		case from.GetId().GetForm() == irv1.Form_FORM_NESTED && from.GetId().GetPreset() == id.GetPreset() && from.GetId().GetForRelation() == id.GetForRelation():
			if id.GetForm() == irv1.Form_FORM_FLAT {
				return "flat"
			}
			if id.GetForm() == irv1.Form_FORM_NESTS {
				return "nests"
			}
		}
	}
	if c.GetKind() == irv1.ConversionKind_CONVERSION_WITH_BASE {
		return "applyTo" + suffix
	}
	return lowerFirst("To" + suffix)
}

// addMember adds a member to s's companion object and fails one whose name
// another member of it has (§15.2).
func (g *generator) addMember(s *irv1.Shape, m member) bool {
	k := shapeKey(s.GetId())
	for _, prev := range g.ix.members[k] {
		if prev.name == m.name {
			g.errorf(CodeNameCollision, s.GetEntity(), "%s gets the TypeScript function %s.%s, as %s does (§14.9, §15.2); give the shape a name annotation",
				m.what, TypeName(s.GetHostName().GetWords()), m.name, prev.what)
			return false
		}
	}
	g.ix.members[k] = append(g.ix.members[k], m)
	return true
}

// has reports whether s's companion object has a member of the name.
func (g *generator) has(s *irv1.Shape, name string) bool {
	for _, m := range g.ix.members[shapeKey(s.GetId())] {
		if m.name == name {
			return true
		}
	}
	return false
}

// companions generates each shape's companion object after the shape types
// of its file: a const of the shape type's name holding its conversions, so
// that rows.map(UserFull.toPartial) converts rows (§14.9).
func (g *generator) companions() {
	for _, s := range g.b.GetShapes().GetShapes() {
		ms := g.ix.members[shapeKey(s.GetId())]
		if len(ms) == 0 {
			continue
		}
		name := TypeName(s.GetHostName().GetWords())
		f := g.shapeFile(s)
		f.printf("/** %s converts %s values to other shapes (§14.9). */\nexport const %s = {\n", name, name, name)
		for _, m := range ms {
			f.printf("%s", m.text)
		}
		f.printf("} as const;\n\n")
	}
}

// receiver is a conversion's parameter for its source: the first letter of
// the shape name's last word, as Go's receivers are.
func receiver(words []string) string {
	if len(words) == 0 {
		return "v"
	}
	for _, r := range words[len(words)-1] {
		if unicode.IsLetter(r) {
			return LocalName([]string{string(unicode.ToLower(r))})
		}
	}
	return "v"
}

// fresh returns name, or name with a number, unused in used, and marks it.
func fresh(name string, used map[string]bool) string {
	out := name
	for i := 2; used[out]; i++ {
		out = name + strconv.Itoa(i)
	}
	used[out] = true
	return out
}

func shapeField(s *irv1.Shape, col string) *irv1.Field {
	for _, f := range s.GetFields() {
		if f.GetColumn() == col {
			return f
		}
	}
	return nil
}

func shapeNest(s *irv1.Shape, rel string) *irv1.Nest {
	for _, n := range s.GetNests() {
		if n.GetRelation() == rel {
			return n
		}
	}
	return nil
}

// memberOf is the TypeScript name of a shape's field.
func (g *generator) memberOf(s *irv1.Shape, col string) string {
	return MemberName(column(g.ix.tables[keyOf(s.GetEntity())], col).GetHostName().GetWords())
}

// sameFieldType reports whether two fields of one column have the same host
// type. A column's x.ts.time-type applies to all its fields (§7.7), so the
// logical types decide.
func sameFieldType(a, b *irv1.Field) bool {
	return proto.Equal(a.GetType(), b.GetType())
}

// describe spells a field's presence and nullability for a diagnostic.
func describe(optional, nullable bool) string {
	words := []string{"required"}
	if optional {
		words[0] = "optional"
	}
	if nullable {
		words = append(words, "nullable")
	}
	return strings.Join(words, " ")
}

func isOptional(f *irv1.Field) bool { return f.GetPresence() == irv1.Presence_PRESENCE_OPTIONAL }

// builder collects a conversion's result: the members its object literal
// sets, then statements that set the optional members a value may lack.
type builder struct {
	checks  []string
	literal []string
	after   []string
}

// set sets member to expr: in the literal, or, where present is not "",
// only when present holds, as an optional member is absent when its source
// is.
func (b *builder) set(out, member, expr, present string) {
	if present == "" {
		if expr == member {
			b.literal = append(b.literal, member)
		} else {
			b.literal = append(b.literal, member+": "+expr)
		}
		return
	}
	b.after = append(b.after, "if ("+present+") "+out+"."+member+" = "+expr+";")
}

// body spells the function body: return the literal when nothing follows
// it, else build it and set the rest.
func (b *builder) body(out, typ string, base string) string {
	var lines []string
	lines = append(lines, b.checks...)
	var lit string
	switch {
	case base != "" && len(b.literal) == 0:
		lit = "{ ..." + base + " }"
	case base != "":
		lit = "{\n  ..." + base + ",\n  " + strings.Join(b.literal, ",\n  ") + ",\n}"
	case len(b.literal) == 0:
		lit = "{}"
	default:
		lit = "{\n  " + strings.Join(b.literal, ",\n  ") + ",\n}"
	}
	if len(b.after) == 0 {
		lines = append(lines, "return "+lit+";")
	} else {
		lines = append(lines, "const "+out+": "+typ+" = "+lit+";")
		lines = append(lines, b.after...)
		lines = append(lines, "return "+out+";")
	}
	return strings.Join(lines, "\n")
}

// indent indents each line of s by prefix.
func indent(s, prefix string) string {
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix)
}

// convert generates a Total, Needs inputs or Fallible conversion: a function
// that builds the target from its source's fields and relations, taking the
// inputs as parameters in the target's column order (§14.9). A Fallible
// conversion checks each field the target narrows to non-null and throws a
// DecodeError where it is null, as decoding the target would (§13).
func (g *generator) convert(c *irv1.Conversion, from, to *irv1.Shape) {
	f := g.shapeFile(from)
	name := g.conversionName(c, from, to)
	what := "the conversion from " + g.dotted(from.GetId()) + " to " + g.dotted(to.GetId())
	fromType, toType := g.shapeType(f, from.GetId(), nil, ""), g.shapeType(f, to.GetId(), nil, "")
	recv := receiver(from.GetHostName().GetWords())
	used := map[string]bool{recv: true}
	unsupported := func(format string, args ...any) {
		g.errorf(CodeConversionUnsupported, from.GetEntity(), what+": "+format, args...)
	}

	inputs := map[string]string{}
	params := []string{recv + ": " + fromType}
	var notes []string
	for _, in := range c.GetInputs() {
		fld := shapeField(to, in.GetColumn())
		if fld == nil {
			unsupported("input %s is not a field of %s", in.GetColumn(), g.dotted(to.GetId()))
			return
		}
		col := column(g.ix.tables[keyOf(to.GetEntity())], in.GetColumn())
		p := fresh(LocalName(col.GetHostName().GetWords()), used)
		inputs[in.GetColumn()] = p
		typ, _ := g.fieldType(f, col, fld, to.GetEntity())
		if isOptional(fld) {
			typ += " | undefined"
		}
		params = append(params, p+": "+typ)
		notes = append(notes, p+" supplies "+in.GetColumn())
	}

	fallible := c.GetKind() == irv1.ConversionKind_CONVERSION_FALLIBLE
	out := fresh(receiver(to.GetHostName().GetWords()), used)
	var b builder
	for _, fld := range to.GetFields() {
		mn := g.memberOf(to, fld.GetColumn())
		if p, ok := inputs[fld.GetColumn()]; ok {
			present := ""
			if isOptional(fld) {
				present = p + " !== undefined"
			}
			b.set(out, mn, p, present)
			continue
		}
		src := shapeField(from, fld.GetColumn())
		if src == nil {
			if isOptional(fld) {
				continue
			}
			unsupported("%s has no field %s, which %s requires", g.dotted(from.GetId()), fld.GetColumn(), g.dotted(to.GetId()))
			return
		}
		value := recv + "." + g.memberOf(from, src.GetColumn())
		present := ""
		if isOptional(src) {
			present = value + " !== undefined"
		}
		if isOptional(src) && !isOptional(fld) {
			unsupported("field %s is optional in %s and required in %s, or its types differ", fld.GetColumn(), g.dotted(from.GetId()), g.dotted(to.GetId()))
			return
		}
		if fallible && src.GetNullable() && !fld.GetNullable() && sameFieldType(src, fld) {
			fail := "throw " + g.narrowed(f, to, fld) + ";"
			if present == "" {
				b.checks = append(b.checks, "if ("+value+" === null) "+fail)
			} else {
				// The check sits with the assignment, which narrows value.
				b.after = append(b.after, "if ("+present+") {\n  if ("+value+" === null) "+fail+"\n  "+out+"."+mn+" = "+value+";\n}")
				continue
			}
			b.set(out, mn, value, present)
			continue
		}
		if (src.GetNullable() && !fld.GetNullable()) || !sameFieldType(src, fld) {
			unsupported("field %s is %s in %s and %s in %s, or its types differ",
				fld.GetColumn(), describe(isOptional(src), src.GetNullable()), g.dotted(from.GetId()),
				describe(isOptional(fld), fld.GetNullable()), g.dotted(to.GetId()))
			return
		}
		b.set(out, mn, value, present)
	}
	for _, n := range to.GetNests() {
		src := shapeNest(from, n.GetRelation())
		if src == nil || !sameNestType(src, n) {
			unsupported("relation %s of %s is missing from %s or has another type", n.GetRelation(), g.dotted(to.GetId()), g.dotted(from.GetId()))
			return
		}
		rn := MemberName(relation(g.ix.tables[keyOf(from.GetEntity())], n.GetRelation()).GetName().GetWords())
		present := ""
		if n.GetPresence() == irv1.Presence_PRESENCE_OPTIONAL {
			present = recv + "." + rn + " !== undefined"
		}
		b.set(out, rn, recv+"."+rn, present)
	}

	doc := name + " converts " + recv + " to " + toType + " (§14.9)."
	if fallible {
		doc = name + " converts " + recv + " to " + toType + ", and throws a DecodeError where a field " + toType + " narrows to non-null is null (§13, §14.9)."
	}
	switch name {
	case "flat":
		doc = "flat returns " + recv + "'s own fields, without its relations (§14.4, §14.9)."
	case "nests":
		doc = "nests returns " + recv + "'s relations, without its own fields (§14.4, §14.9)."
	}
	if len(notes) > 0 {
		doc += " " + TypeName(from.GetHostName().GetWords()) + " cannot hold every field, so " + strings.Join(notes, " and ") + "."
	}
	text := "  /** " + doc + " */\n  " + name + "(" + strings.Join(params, ", ") + "): " + toType + " {\n" +
		indent(b.body(out, toType, ""), "    ") + "\n  },\n"
	g.addMember(from, member{name: name, what: what, text: text})
}

// narrowed is the error of a null in a field shape s narrows to non-null:
// the DecodeError decoding s would throw (§13), naming where the narrowing
// comes from.
func (g *generator) narrowed(f *file, s *irv1.Shape, fld *irv1.Field) string {
	assertion := g.assertion(s, fld, column(g.ix.tables[keyOf(s.GetEntity())], fld.GetColumn()))
	return fmt.Sprintf("new %s({ shape: %s, field: %s, reason: \"null_into_non_null\", assertion: %s })",
		f.rt("DecodeError", true), strconv.Quote(g.dotted(s.GetId())), strconv.Quote(fld.GetColumn()), strconv.Quote(assertion))
}

// assertion names where a field's non-null narrowing comes from (§13): the
// shape operator's shape, or the annotation on its column.
func (g *generator) assertion(s *irv1.Shape, fld *irv1.Field, c *irv1.Column) string {
	switch fld.GetNullabilitySource() {
	case irv1.NullabilitySource_NULLABILITY_SOURCE_SHAPE_OPERATOR:
		id := fld.GetNullabilityShape()
		if id == nil {
			id = s.GetId()
		}
		return "nonnull of shape " + g.dotted(id)
	case irv1.NullabilitySource_NULLABILITY_SOURCE_ANNOTATION:
		e := s.GetEntity()
		return annotated(c.GetAnnotations(), "core.nonnull",
			&irv1.QualifiedName{Catalog: e.GetCatalog(), Namespace: e.GetNamespace(), Object: e.GetObject(), Member: fld.GetColumn()})
	}
	return ""
}

// annotated names the annotation with the key as the record's example does:
// core.nonnull from COMMENT ON COLUMN app.published_posts.title, or, where
// its origin records no location, core.nonnull on the column.
func annotated(annotations []*irv1.Annotation, key string, at *irv1.QualifiedName) string {
	for _, a := range annotations {
		if a.GetKey() == key && a.GetOrigin().GetLocation() != "" {
			return key + " from " + a.GetOrigin().GetLocation()
		}
	}
	return key + " on " + displayName(at)
}

// apply generates a With base conversion: a function that returns a copy of
// the base with the fields its source holds set (§14.9). Fields the base
// cannot hold, such as a write-only column in a Full, are ignored, so the
// result is what a read would return.
func (g *generator) apply(from, to *irv1.Shape) {
	f := g.shapeFile(from)
	name := g.conversionName(&irv1.Conversion{Kind: irv1.ConversionKind_CONVERSION_WITH_BASE}, from, to)
	what := "the conversion from " + g.dotted(from.GetId()) + " to " + g.dotted(to.GetId())
	fromType, toType := g.shapeType(f, from.GetId(), nil, ""), g.shapeType(f, to.GetId(), nil, "")
	recv := receiver(from.GetHostName().GetWords())
	used := map[string]bool{recv: true}
	base := fresh(receiver(to.GetHostName().GetWords()), used)
	out := fresh("out", used)
	var b builder
	for _, src := range from.GetFields() {
		dst := shapeField(to, src.GetColumn())
		if dst == nil {
			continue
		}
		if (src.GetNullable() && !dst.GetNullable()) || !sameFieldType(src, dst) {
			g.errorf(CodeConversionUnsupported, from.GetEntity(), "%s: field %s is %s in %s and %s in %s, or its types differ",
				what, src.GetColumn(), describe(isOptional(src), src.GetNullable()), g.dotted(from.GetId()),
				describe(isOptional(dst), dst.GetNullable()), g.dotted(to.GetId()))
			return
		}
		value := recv + "." + g.memberOf(from, src.GetColumn())
		present := ""
		if isOptional(src) {
			present = value + " !== undefined"
		}
		b.set(out, g.memberOf(to, dst.GetColumn()), value, present)
	}
	doc := name + " returns a copy of " + base + " with the fields " + recv + " holds set; fields " + toType + " cannot hold are ignored (§14.9)."
	text := "  /** " + doc + " */\n  " + name + "(" + recv + ": " + fromType + ", " + base + ": " + toType + "): " + toType + " {\n" +
		indent(b.body(out, toType, base), "    ") + "\n  },\n"
	g.addMember(from, member{name: name, what: what, text: text})
}

// merges generates merging Partials into Full rows by key for every keyed
// entity (§8.9, §14.9): total when every key column compares exactly or
// folds, returning the keys that matched no row, and fallible otherwise,
// throwing on the first change that matches no row exactly.
func (g *generator) merges() {
	for _, m := range g.b.GetShapes().GetMerges() {
		q := m.GetEntity()
		t := g.ix.tables[keyOf(q)]
		if t == nil {
			g.errorf(CodeReferenceUnknown, q, "the merge by key of %s belongs to an entity the bundle does not hold", displayName(q))
			continue
		}
		preset := func(p irv1.Preset, form irv1.Form) *irv1.Shape {
			return g.ix.shapes[shapeKey(&irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: q}, Preset: p, Form: form})]
		}
		full, key, partial := preset(irv1.Preset_PRESET_FULL, irv1.Form_FORM_FLAT), preset(irv1.Preset_PRESET_KEY, irv1.Form_FORM_UNSPECIFIED), preset(irv1.Preset_PRESET_PARTIAL, irv1.Form_FORM_FLAT)
		unsupported := func(format string, args ...any) {
			g.errorf(CodeConversionUnsupported, q, "the merge by key of %s: "+format, append([]any{displayName(q)}, args...)...)
		}
		if full == nil || key == nil || partial == nil {
			unsupported("it needs the entity's Full, Key and Partial shapes")
			continue
		}
		if !g.has(partial, "applyTo") {
			unsupported("it needs the conversion from %s to %s", g.dotted(partial.GetId()), g.dotted(full.GetId()))
			continue
		}
		f := g.entityFile(q)
		var eqs []string
		for _, k := range m.GetKey() {
			kf, ff := shapeField(key, k.GetColumn()), shapeField(full, k.GetColumn())
			if kf == nil || ff == nil || kf.GetNullable() || ff.GetNullable() {
				unsupported("key column %s must be a non-null field of %s and %s", k.GetColumn(), g.dotted(key.GetId()), g.dotted(full.GetId()))
				eqs = nil
				break
			}
			col := column(t, k.GetColumn())
			eq, ok := g.equal(f, "r."+g.memberOf(full, k.GetColumn()), "c.key."+g.memberOf(key, k.GetColumn()), kf.GetType(), timeType(col) == "date", k)
			if !ok {
				unsupported("the ts emitter cannot compare key column %s %s", k.GetColumn(), comparisonWords[k.GetComparison()])
				eqs = nil
				break
			}
			eqs = append(eqs, eq)
		}
		if len(eqs) == 0 {
			continue
		}
		name := "merge" + TypeName(t.GetHostName().GetWords()) + "ByKey"
		g.declare(f, name, "the merge by key of "+displayName(q), q)
		fullType, keyType := g.shapeType(f, full.GetId(), q, ""), g.shapeType(f, key.GetId(), q, "")
		partialType := g.ref(f, TypeName(partial.GetHostName().GetWords()), true)
		changes := "readonly " + f.rt("Keyed", false) + "<" + keyType + ", " + partialType + ">[]"
		match := strings.Join(eqs, " && ")
		switch m.GetKind() {
		case irv1.ConversionKind_CONVERSION_TOTAL:
			f.printf(`/**
 * %[1]s applies each change to the rows its key matches and
 * returns them, with the keys of changes that matched no row. Keys compare
 * exactly, or after a certified fold (§8.9, §14.9).
 */
export function %[1]s(
  rows: readonly %[2]s[],
  changes: %[3]s,
): { merged: %[2]s[]; unmatched: %[4]s[] } {
  const merged = [...rows];
  const unmatched: %[4]s[] = [];
  for (const c of changes) {
    let matched = false;
    for (const [i, r] of merged.entries()) {
      if (%[5]s) {
        merged[i] = %[6]s.applyTo(c.value, r);
        matched = true;
      }
    }
    if (!matched) unmatched.push(c.key);
  }
  return { merged, unmatched };
}

`, name, fullType, changes, keyType, match, partialType)
		case irv1.ConversionKind_CONVERSION_FALLIBLE:
			f.printf(`/**
 * %[1]s applies each change to the rows its key matches and returns them.
 * A key's collation may equate values the client cannot compare, so a change
 * that matches no row exactly throws an UnmatchedKeyError (§8.9, §14.9).
 */
export function %[1]s(rows: readonly %[2]s[], changes: %[3]s): %[2]s[] {
  const merged = [...rows];
  for (const [i, c] of changes.entries()) {
    let matched = false;
    for (const [j, r] of merged.entries()) {
      if (%[4]s) {
        merged[j] = %[5]s.applyTo(c.value, r);
        matched = true;
      }
    }
    if (!matched) throw new %[6]s(%[7]s, i);
  }
  return merged;
}

`, name, fullType, changes, match, partialType, f.rt("UnmatchedKeyError", true), strconv.Quote(displayName(q)))
		default:
			unsupported("its kind is %s; a merge is total or fallible (§8.9)", m.GetKind())
		}
	}
}

var comparisonWords = map[irv1.Comparison]string{
	irv1.Comparison_COMPARISON_UNSPECIFIED: "without a comparison",
	irv1.Comparison_COMPARISON_EXACT:       "exactly",
	irv1.Comparison_COMPARISON_FOLDED:      "after a fold",
	irv1.Comparison_COMPARISON_INEXACT:     "byte for byte",
}

// equalities say how two values of a logical type compare (§8.9): primitives
// and branded strings with ===, bytes with bytesEqual, and the runtime's
// value types with their equals method. Types missing here (JSON, intervals,
// arrays, ranges, composite types, weak values) have no host equality.
var equalities = map[string]string{
	"bool": "===", "int8": "===", "int16": "===", "int32": "===", "int64": "===",
	"uint8": "===", "uint16": "===", "uint32": "===", "uint64": "===", "float32": "===", "float64": "===",
	"text": "===", "varchar": "===", "char": "===", "xml": "===", "bit": "===",
	"uuid": "===", "inet": "===", "cidr": "===", "macaddr": "===",
	"bytes":   "bytes",
	"decimal": "equals", "money": "equals", "instant": "equals", "instant_with_offset": "equals",
	"local_datetime": "equals", "date": "equals", "local_time": "equals", "time_with_offset": "equals",
}

// textLike are the logical types a fold may apply to.
var textLike = map[string]bool{"text": true, "varchar": true, "char": true}

// equal compares two key values of type t as the key comparison says (§8.9);
// asDate says the column maps to Date (§7.7).
func (g *generator) equal(f *file, a, b string, t *irv1.Type, asDate bool, k *irv1.KeyComparison) (string, bool) {
	for {
		n, ok := t.GetKind().(*irv1.Type_Named)
		if !ok {
			break
		}
		if n.Named.GetEnum() != nil {
			t = &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}}
			break
		}
		if n.Named.GetDomain() == nil {
			return "", false
		}
		t = g.named[keyOf(n.Named.GetDomain())].base
	}
	kind := t.ProtoReflect().WhichOneof(t.ProtoReflect().Descriptor().Oneofs().ByName("kind"))
	name := ""
	if kind != nil {
		name = string(kind.Name())
	}
	how := equalities[name]
	if name == "opaque" {
		switch g.opaqueHost(t) {
		case "string", "number", "bigint", "boolean":
			how, name = "===", "text"
		case "Uint8Array":
			how = "bytes"
		}
	}
	if asDate {
		how = "date"
	}
	switch k.GetComparison() {
	case irv1.Comparison_COMPARISON_EXACT, irv1.Comparison_COMPARISON_INEXACT:
	case irv1.Comparison_COMPARISON_FOLDED:
		if !textLike[name] || how != "===" || k.GetFold() != irv1.ClientFold_CLIENT_FOLD_ASCII_LOWER {
			return "", false
		}
		fold := f.rt("foldAscii", true)
		return fold + "(" + a + ") === " + fold + "(" + b + ")", true
	default:
		return "", false
	}
	switch how {
	case "===":
		return a + " === " + b, true
	case "bytes":
		return f.rt("bytesEqual", true) + "(" + a + ", " + b + ")", true
	case "equals":
		return a + ".equals(" + b + ")", true
	case "date":
		return a + ".getTime() === " + b + ".getTime()", true
	}
	return "", false
}

// opaqueHost is the TypeScript type an opaque type maps to: through the
// types setting, else its fallback, else "".
func (g *generator) opaqueHost(t *irv1.Type) string {
	n := t.GetNative().GetName()
	if spec, ok := g.o.Types[n.GetNamespace()+"."+n.GetObject()]; ok {
		return spec
	}
	switch t.GetOpaque().GetFallback() {
	case irv1.OpaqueFallback_OPAQUE_FALLBACK_STRING:
		return "string"
	case irv1.OpaqueFallback_OPAQUE_FALLBACK_BYTES:
		return "Uint8Array"
	}
	return ""
}
