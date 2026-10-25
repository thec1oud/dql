package gen

import (
	"cmp"
	"fmt"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// CodeConversionUnsupported: a conversion or merge the emitter cannot
// generate as the kind the core computed says.
const CodeConversionUnsupported = "go-conversion-unsupported"

// conversions generates every conversion the shape layer lists, as its kind
// dictates (§14.9): a method on the source shape for Total, Needs inputs and
// With base, and nothing for Via database and Impossible.
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
				"the conversion from %s to %s is %s, which the go emitter does not generate; the core classifies shape conversions as total, needs inputs, with base, via database or impossible (§14.9)",
				g.dotted(from.GetId()), g.dotted(to.GetId()), c.GetKind())
		}
	}
}

// samePreset reports whether to is a preset shape of from's entity.
func samePreset(from, to *irv1.Shape) bool {
	return to.GetId().GetCustom() == "" && proto.Equal(from.GetEntity(), to.GetEntity())
}

// conversionName names a conversion's method as §14.9 does: Key for a Key,
// Flat and Nests for a Nested shape's own forms, ApplyTo and ApplyTo plus
// the target for With base, else To plus the target. A preset of the same
// entity is named without the entity's words (ToPartial, ApplyToDraft); a
// custom shape keeps its name (ToUserSummary).
func (g *generator) conversionName(c *irv1.Conversion, from, to *irv1.Shape) string {
	suffix := Exported(to.GetHostName().GetWords())
	id := to.GetId()
	if samePreset(from, to) {
		suffix = strings.TrimPrefix(suffix, Exported(g.ix.tables[keyOf(to.GetEntity())].GetHostName().GetWords()))
		switch {
		case c.GetKind() == irv1.ConversionKind_CONVERSION_WITH_BASE && id.GetPreset() == irv1.Preset_PRESET_FULL && id.GetForm() == irv1.Form_FORM_FLAT:
			return "ApplyTo"
		case id.GetPreset() == irv1.Preset_PRESET_KEY:
			return "Key"
		case from.GetId().GetForm() == irv1.Form_FORM_NESTED && from.GetId().GetPreset() == id.GetPreset() && from.GetId().GetForRelation() == id.GetForRelation():
			if id.GetForm() == irv1.Form_FORM_FLAT {
				return "Flat"
			}
			if id.GetForm() == irv1.Form_FORM_NESTS {
				return "Nests"
			}
		}
	}
	if c.GetKind() == irv1.ConversionKind_CONVERSION_WITH_BASE {
		return "ApplyTo" + suffix
	}
	return "To" + suffix
}

// method declares a method of a shape type and fails one that meets a field
// or another method of that type (§15.2).
func (g *generator) method(s *irv1.Shape, name, what string) bool {
	typ := Exported(s.GetHostName().GetWords())
	if g.ix.methods[typ] == nil {
		g.ix.methods[typ] = map[string]string{}
	}
	prev, ok := g.ix.methods[typ][name]
	if !ok && g.ix.fields[typ][name] {
		prev, ok = "a field", true
	}
	if ok {
		g.errorf(CodeNameCollision, s.GetEntity(), "%s gets the Go method %s.%s, as %s does (§14.9, §15.2); give the field or shape a name annotation",
			what, typ, name, prev)
		return false
	}
	g.ix.methods[typ][name] = what
	return true
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

// fieldName is the Go name of a shape's field.
func (g *generator) fieldName(s *irv1.Shape, col string) string {
	return Exported(column(g.ix.tables[keyOf(s.GetEntity())], col).GetHostName().GetWords())
}

// fieldType is the Go type of a shape's field.
func (g *generator) fieldType(f *file, s *irv1.Shape, fld *irv1.Field) string {
	typ := g.field(f, fld.GetType(), fld.GetNullable(), s.GetEntity(), "")
	if fld.GetPresence() == irv1.Presence_PRESENCE_OPTIONAL {
		typ = f.use(pkgOpt, "opt") + ".Opt[" + typ + "]"
	}
	return typ
}

// carry converts expr, a value of a field with the given presence and
// nullability, to a value of dst: opt.Some where dst is optional and
// dql.Ptr where dst is nullable. It fails where dst is required and the
// value optional, or dst non-null and the value nullable.
func carry(f *file, expr string, optional, nullable bool, dst *irv1.Field) (string, bool) {
	toOpt := dst.GetPresence() == irv1.Presence_PRESENCE_OPTIONAL
	if (optional && !toOpt) || (nullable && !dst.GetNullable()) {
		return "", false
	}
	ptr := !nullable && dst.GetNullable()
	switch {
	case optional && ptr:
		return f.use(pkgOpt, "opt") + ".Map(" + expr + ", " + f.use(pkgDQL, "dql") + ".Ptr)", true
	case ptr:
		expr = f.use(pkgDQL, "dql") + ".Ptr(" + expr + ")"
	}
	if toOpt && !optional {
		expr = f.use(pkgOpt, "opt") + ".Some(" + expr + ")"
	}
	return expr, true
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

// convert generates a Total, Needs inputs or Fallible conversion: a method
// that builds the target from the receiver's fields and relations, taking the
// inputs as parameters in the target's column order (§14.9). A Fallible
// conversion checks each field the target narrows to non-null and fails with
// a *dql.DecodeError where it is NULL, as decoding the target would (§13).
func (g *generator) convert(c *irv1.Conversion, from, to *irv1.Shape) {
	f := g.shapeFile(from)
	name := g.conversionName(c, from, to)
	what := "the conversion from " + g.dotted(from.GetId()) + " to " + g.dotted(to.GetId())
	if !g.method(from, name, what) {
		return
	}
	fromType, toType := Exported(from.GetHostName().GetWords()), Exported(to.GetHostName().GetWords())
	recv := receiver(from.GetHostName().GetWords())
	used := map[string]bool{recv: true}
	unsupported := func(format string, args ...any) {
		g.errorf(CodeConversionUnsupported, from.GetEntity(), what+": "+format, args...)
	}

	inputs := map[string]string{}
	var params, notes []string
	for _, in := range c.GetInputs() {
		fld := shapeField(to, in.GetColumn())
		if fld == nil {
			unsupported("input %s is not a field of %s", in.GetColumn(), g.dotted(to.GetId()))
			return
		}
		col := column(g.ix.tables[keyOf(to.GetEntity())], in.GetColumn())
		p := fresh(Unexported(col.GetHostName().GetWords()), used)
		inputs[in.GetColumn()] = p
		params = append(params, p+" "+g.fieldType(f, to, fld))
		notes = append(notes, p+" supplies "+in.GetColumn())
	}

	fallible := c.GetKind() == irv1.ConversionKind_CONVERSION_FALLIBLE
	result, ret := toType, ""
	if fallible {
		result, ret = "("+toType+", error)", ", nil"
	}
	var body []string
	base := g.embedded(from)
	switch {
	case base == to:
		body = append(body, "return "+recv+"."+toType+ret)
	case base != nil && g.embeddedNests(from) == to:
		body = append(body, "return "+recv+"."+toType+ret)
	default:
		out := fresh(receiver(to.GetHostName().GetWords()), used)
		body = append(body, "var "+out+" "+toType)
		for _, fld := range to.GetFields() {
			fn := g.fieldName(to, fld.GetColumn())
			if p, ok := inputs[fld.GetColumn()]; ok {
				body = append(body, out+"."+fn+" = "+p)
				continue
			}
			src := shapeField(from, fld.GetColumn())
			if src == nil {
				if fld.GetPresence() == irv1.Presence_PRESENCE_OPTIONAL {
					continue
				}
				unsupported("%s has no field %s, which %s requires", g.dotted(from.GetId()), fld.GetColumn(), g.dotted(to.GetId()))
				return
			}
			value := recv + "." + g.fieldName(from, src.GetColumn())
			optional := src.GetPresence() == irv1.Presence_PRESENCE_OPTIONAL
			if fallible && src.GetNullable() && !fld.GetNullable() && proto.Equal(src.GetType(), fld.GetType()) {
				v := fresh("v", used)
				// A non-null value carries into any field of its type.
				expr, _ := carry(f, "*"+v, false, false, fld)
				if optional && fld.GetPresence() != irv1.Presence_PRESENCE_OPTIONAL {
					unsupported("field %s is optional in %s and required in %s", fld.GetColumn(), g.dotted(from.GetId()), g.dotted(to.GetId()))
					return
				}
				fail := "if " + v + " == nil {\n\t\treturn " + toType + "{}, " + g.narrowed(f, to, fld) + "\n\t}\n\t" + out + "." + fn + " = " + expr
				if optional {
					fail = "if " + v + ", ok := " + value + ".Get(); ok {\n\t" + strings.ReplaceAll(fail, "\n\t", "\n\t\t") + "\n\t}"
				} else {
					fail = v + " := " + value + "\n\t" + fail
				}
				body = append(body, fail)
				continue
			}
			expr, ok := carry(f, value, optional, src.GetNullable(), fld)
			if !ok || !proto.Equal(src.GetType(), fld.GetType()) {
				unsupported("field %s is %s in %s and %s in %s, or its types differ",
					fld.GetColumn(), describe(src.GetPresence() == irv1.Presence_PRESENCE_OPTIONAL, src.GetNullable()), g.dotted(from.GetId()),
					describe(fld.GetPresence() == irv1.Presence_PRESENCE_OPTIONAL, fld.GetNullable()), g.dotted(to.GetId()))
				return
			}
			body = append(body, out+"."+fn+" = "+expr)
		}
		for _, n := range to.GetNests() {
			src := shapeNest(from, n.GetRelation())
			if src == nil || !sameNestType(src, n) {
				unsupported("relation %s of %s is missing from %s or has another type", n.GetRelation(), g.dotted(to.GetId()), g.dotted(from.GetId()))
				return
			}
			rn := Exported(relation(g.ix.tables[keyOf(from.GetEntity())], n.GetRelation()).GetName().GetWords())
			body = append(body, out+"."+rn+" = "+recv+"."+rn)
		}
		body = append(body, "return "+out+ret)
	}

	doc := "converts " + recv + " to " + toType + " (§14.9)."
	if fallible {
		doc = "converts " + recv + " to " + toType + ", and fails where a field " + toType + " narrows\n// to non-null is NULL (§13, §14.9)."
	}
	switch name {
	case "Flat":
		doc = "returns " + recv + "'s own fields, without its relations (§14.4, §14.9)."
	case "Nests":
		doc = "returns " + recv + "'s relations, without its own fields (§14.4, §14.9)."
	}
	f.printf("// %s %s\n", name, doc)
	if len(notes) > 0 {
		f.printf("// %s cannot hold every field, so %s (§14.9).\n", fromType, strings.Join(notes, " and "))
	}
	f.printf("func (%s %s) %s(%s) %s {\n\t%s\n}\n\n", recv, fromType, name, strings.Join(params, ", "), result, strings.Join(body, "\n\t"))
}

// narrowed is the error of a NULL in a field shape s narrows to non-null:
// the DecodeError decoding s would return (§13), naming where the narrowing
// comes from.
func (g *generator) narrowed(f *file, s *irv1.Shape, fld *irv1.Field) string {
	var assertion string
	switch fld.GetNullabilitySource() {
	case irv1.NullabilitySource_NULLABILITY_SOURCE_SHAPE_OPERATOR:
		assertion = "nonnull of shape " + g.dotted(s.GetId())
	case irv1.NullabilitySource_NULLABILITY_SOURCE_ANNOTATION:
		e := s.GetEntity()
		assertion = "core.nonnull on " + displayName(&irv1.QualifiedName{Catalog: e.GetCatalog(), Namespace: e.GetNamespace(), Object: e.GetObject(), Member: fld.GetColumn()})
	}
	return fmt.Sprintf("&%[1]s.DecodeError{Shape: %[2]q, Field: %[3]q, Reason: %[1]s.NullIntoNonNull, Assertion: %[4]q}",
		f.use(pkgDQL, "dql"), g.dotted(s.GetId()), fld.GetColumn(), assertion)
}

// apply generates a With base conversion: a method that sets the base's
// fields from the fields the receiver holds and returns it (§14.9). Fields
// the base cannot hold, such as a write-only column in a Full, are ignored,
// so the result is what a read would return.
func (g *generator) apply(from, to *irv1.Shape) {
	f := g.shapeFile(from)
	name := g.conversionName(&irv1.Conversion{Kind: irv1.ConversionKind_CONVERSION_WITH_BASE}, from, to)
	what := "the conversion from " + g.dotted(from.GetId()) + " to " + g.dotted(to.GetId())
	if !g.method(from, name, what) {
		return
	}
	fromType, toType := Exported(from.GetHostName().GetWords()), Exported(to.GetHostName().GetWords())
	recv := receiver(from.GetHostName().GetWords())
	used := map[string]bool{recv: true, "ok": true}
	base := fresh(receiver(to.GetHostName().GetWords()), used)
	v := fresh("v", used)
	var body []string
	for _, src := range from.GetFields() {
		dst := shapeField(to, src.GetColumn())
		if dst == nil {
			continue
		}
		optional := src.GetPresence() == irv1.Presence_PRESENCE_OPTIONAL
		value := recv + "." + g.fieldName(from, src.GetColumn())
		if optional {
			value = v
		}
		expr, ok := carry(f, value, false, src.GetNullable(), dst)
		if !ok || !proto.Equal(src.GetType(), dst.GetType()) {
			g.errorf(CodeConversionUnsupported, from.GetEntity(), "%s: field %s is %s in %s and %s in %s, or its types differ",
				what, src.GetColumn(), describe(optional, src.GetNullable()), g.dotted(from.GetId()),
				describe(dst.GetPresence() == irv1.Presence_PRESENCE_OPTIONAL, dst.GetNullable()), g.dotted(to.GetId()))
			return
		}
		set := base + "." + g.fieldName(to, dst.GetColumn()) + " = " + expr
		if optional {
			set = "if " + v + ", ok := " + recv + "." + g.fieldName(from, src.GetColumn()) + ".Get(); ok {\n\t\t" + set + "\n\t}"
		}
		body = append(body, set)
	}
	body = append(body, "return "+base)
	f.printf("// %s returns %s with the fields %s holds set; fields %s cannot hold are\n// ignored (§14.9).\n", name, base, recv, toType)
	f.printf("func (%s %s) %s(%s %s) %s {\n\t%s\n}\n\n", recv, fromType, name, base, toType, toType, strings.Join(body, "\n\t"))
}

// merges generates merging Partials into Full rows by key for every keyed
// entity (§8.9, §14.9): total when every key column compares exactly or
// folds, returning the keys that matched no row, and fallible otherwise,
// failing on the first change that matches no row byte for byte.
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
		fullType, keyType, partialType := Exported(full.GetHostName().GetWords()), Exported(key.GetHostName().GetWords()), Exported(partial.GetHostName().GetWords())
		if _, ok := g.ix.methods[partialType]["ApplyTo"]; !ok {
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
			eq, ok := g.equal(f, "r."+g.fieldName(full, k.GetColumn()), "c.Key."+g.fieldName(key, k.GetColumn()), kf.GetType(), k)
			if !ok {
				unsupported("the go emitter cannot compare key column %s %s", k.GetColumn(), comparisonWords[k.GetComparison()])
				eqs = nil
				break
			}
			eqs = append(eqs, eq)
		}
		if len(eqs) == 0 {
			continue
		}
		name := "Merge" + Exported(t.GetHostName().GetWords()) + "ByKey"
		g.declare(name, "the merge by key of "+displayName(q), q)
		changes := "[]" + f.use(pkgDQL, "dql") + ".Keyed[" + keyType + ", " + partialType + "]"
		match := strings.Join(eqs, " && ")
		f.use("slices", "slices")
		switch m.GetKind() {
		case irv1.ConversionKind_CONVERSION_TOTAL:
			f.printf(`// %[1]s applies each change to the rows its key matches and returns
// them, with the keys of changes that matched no row. Keys compare exactly, or
// after a certified fold (§8.9, §14.9).
func %[1]s(rows []%[2]s, changes %[3]s) (merged []%[2]s, unmatched []%[4]s) {
	merged = slices.Clone(rows)
	for _, c := range changes {
		matched := false
		for i, r := range merged {
			if %[5]s {
				merged[i] = c.Value.ApplyTo(r)
				matched = true
			}
		}
		if !matched {
			unmatched = append(unmatched, c.Key)
		}
	}
	return merged, unmatched
}

`, name, fullType, changes, keyType, match)
		case irv1.ConversionKind_CONVERSION_FALLIBLE:
			f.printf(`// %[1]s applies each change to the rows its key matches and returns
// them. A key's collation may equate values the client cannot compare, so a
// change that matches no row byte for byte fails with *dql.UnmatchedKeyError
// (§8.9, §14.9).
func %[1]s(rows []%[2]s, changes %[3]s) ([]%[2]s, error) {
	merged := slices.Clone(rows)
	for i, c := range changes {
		matched := false
		for j, r := range merged {
			if %[4]s {
				merged[j] = c.Value.ApplyTo(r)
				matched = true
			}
		}
		if !matched {
			return nil, &dql.UnmatchedKeyError{Entity: %[5]q, Index: i}
		}
	}
	return merged, nil
}

`, name, fullType, changes, match, displayName(q))
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

// equal compares two key values of type t as the key comparison says (§8.9):
// byte slices with bytes.Equal, folded text after dql.FoldASCII, and every
// other comparable type with ==. Slices of other elements and composite
// types have no host equality.
func (g *generator) equal(f *file, a, b string, t *irv1.Type, k *irv1.KeyComparison) (string, bool) {
	for {
		n, ok := t.GetKind().(*irv1.Type_Named)
		if !ok || n.Named.GetDomain() == nil {
			break
		}
		t = g.named[keyOf(n.Named.GetDomain())].base
	}
	kind := t.ProtoReflect().WhichOneof(t.ProtoReflect().Descriptor().Oneofs().ByName("kind"))
	name := ""
	if kind != nil {
		name = string(kind.Name())
	}
	_, mapped := g.o.Types[t.GetNative().GetName().GetNamespace()+"."+t.GetNative().GetName().GetObject()]
	bytesLike := name == "bytes" || name == "json" || name == "jsonb" || name == "macaddr" ||
		(name == "opaque" && !mapped && t.GetOpaque().GetFallback() == irv1.OpaqueFallback_OPAQUE_FALLBACK_BYTES)
	textLike := name == "text" || name == "varchar" || name == "char" ||
		(name == "opaque" && !mapped && t.GetOpaque().GetFallback() == irv1.OpaqueFallback_OPAQUE_FALLBACK_STRING)
	switch k.GetComparison() {
	case irv1.Comparison_COMPARISON_EXACT, irv1.Comparison_COMPARISON_INEXACT:
	case irv1.Comparison_COMPARISON_FOLDED:
		if !textLike || k.GetFold() != irv1.ClientFold_CLIENT_FOLD_ASCII_LOWER {
			return "", false
		}
		dql := f.use(pkgDQL, "dql")
		return dql + ".FoldASCII(" + a + ") == " + dql + ".FoldASCII(" + b + ")", true
	default:
		return "", false
	}
	switch {
	case bytesLike:
		return f.use("bytes", "bytes") + ".Equal(" + a + ", " + b + ")", true
	case name == "array" || name == "multirange" || (name == "named" && t.GetNamed().GetComposite() != nil):
		return "", false
	}
	return a + " == " + b, true
}
