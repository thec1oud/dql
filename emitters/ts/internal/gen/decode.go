package gen

import (
	"fmt"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// CodeDecodeUnsupported: a read shape the emitter cannot decode a row into:
// one that nests a write shape or a change.
const CodeDecodeUnsupported = "ts-decode-unsupported"

// decoders generates decode<Shape> for every read shape: Full, Key and the
// custom shapes derived from them (§13). Write shapes are never decoded.
// Every name is declared first, as a decoder calls those of its nests.
func (g *generator) decoders() {
	var shapes []*irv1.Shape
	for _, s := range g.b.GetShapes().GetShapes() {
		if !writeShape(s) && g.ix.tables[keyOf(s.GetEntity())] != nil {
			g.declare(g.shapeFile(s), "decode"+TypeName(s.GetHostName().GetWords()), "the decoder of shape "+g.dotted(s.GetId()), s.GetEntity())
			shapes = append(shapes, s)
		}
	}
	for _, s := range shapes {
		g.decoder(s)
	}
}

// decoder generates a shape's decoder, which reads a Row of its fields in
// order, then its nests, and enforces every fact they declare or assert:
// NULL in a non-null field, an unknown enum label, an array of other
// dimensions and an off-type value each throw a DecodeError (§13).
func (g *generator) decoder(s *irv1.Shape) {
	f := g.shapeFile(s)
	typ := g.shapeType(f, s.GetId(), nil, "")
	name := "decode" + TypeName(s.GetHostName().GetWords())
	shape := g.dotted(s.GetId())
	q := s.GetEntity()
	t := g.ix.tables[keyOf(q)]
	var members []string
	for i, fld := range s.GetFields() {
		at := &irv1.QualifiedName{Catalog: q.GetCatalog(), Namespace: q.GetNamespace(), Object: q.GetObject(), Member: fld.GetColumn()}
		col := column(t, fld.GetColumn())
		if col == nil {
			return // reported with the shape
		}
		conv := g.conv(f, fld.GetType(), at, "column "+displayName(at))
		if timeType(col) == "date" && fld.GetType().GetInstant() != nil {
			conv = f.rt("asDate", true)
		}
		get := fmt.Sprintf("d.field(%d, %s, %s, %s)", i, jsString(fld.GetColumn()), jsString(g.assertion(s, fld, col)), conv)
		if fld.GetNullable() {
			get = fmt.Sprintf("d.nullable(%d, %s, %s)", i, jsString(fld.GetColumn()), conv)
		}
		members = append(members, MemberName(col.GetHostName().GetWords())+": "+get)
	}
	for j, n := range s.GetNests() {
		i := len(s.GetFields()) + j
		r := relation(t, n.GetRelation())
		target := g.ix.shapes[shapeKey(n.GetShape())]
		if r == nil || (n.GetUnion() == nil && target == nil) || (n.GetUnion() != nil && g.ix.unions[unionKey(n.GetUnion())] == nil) {
			return // reported with the shape
		}
		if n.GetUnion() != nil || writeShape(target) {
			at := &irv1.QualifiedName{Catalog: q.GetCatalog(), Namespace: q.GetNamespace(), Object: q.GetObject(), Member: n.GetRelation()}
			g.errorf(CodeDecodeUnsupported, at, "read shape %s nests a write shape or change through %s, which no row holds", shape, n.GetRelation())
			return
		}
		decode := g.ref(f, "decode"+TypeName(target.GetHostName().GetWords()), true)
		var get string
		switch {
		case toMany(r):
			get = fmt.Sprintf("d.many(%d, %s, %s)", i, jsString(n.GetRelation()), decode)
		case n.GetNullable():
			get = fmt.Sprintf("d.nullable(%d, %s, %s(%s))", i, jsString(n.GetRelation()), f.rt("nest", true), decode)
		default:
			get = fmt.Sprintf("d.field(%d, %s, \"\", %s(%s))", i, jsString(n.GetRelation()), f.rt("nest", true), decode)
		}
		members = append(members, MemberName(r.GetName().GetWords())+": "+get)
	}
	f.printf("/**\n * %s decodes r, a row of %s.\n * The row holds the shape's fields in order, then its nests. A value that\n * breaks what a field declares or asserts throws a DecodeError, never a\n * default (§13).\n */\n", name, shape)
	f.printf("export function %s(r: %s): %s {\n  const d = new %s(%s, r, %d);\n", name, f.rt("Row", false), typ, f.rt("Decoder", true), jsString(shape), len(members))
	f.printf("  return %s;\n}\n\n", objectLiteral(members, "  "))
}

// objectLiteral spells an object literal of the members, one per line,
// indented for a statement at indent.
func objectLiteral(members []string, indent string) string {
	if len(members) == 0 {
		return "{}"
	}
	return "{\n" + indent + "  " + strings.Join(members, ",\n"+indent+"  ") + ",\n" + indent + "}"
}

// scalarConverters are the runtime's converters of the logical types that
// need no parameter.
var scalarConverters = map[string]string{
	"bool": "asBoolean", "int8": "asInt8", "int16": "asInt16", "int32": "asInt32", "int64": "asInt64",
	"uint8": "asUint8", "uint16": "asUint16", "uint32": "asUint32", "uint64": "asUint64",
	"float32": "asFloat32", "float64": "asFloat64",
	"text": "asString", "varchar": "asString", "char": "asString", "xml": "asString", "bit": "asString",
	"bytes": "asBytes", "json": "asJson", "jsonb": "asJson",
}

// brands are the logical types decoded as a branded string.
var brands = map[string]string{"uuid": "Uuid", "inet": "Inet", "cidr": "Cidr", "macaddr": "MacAddr"}

// instances are the logical types decoded as an instance of the runtime's
// classes, by the class's expression.
var instances = map[string][2]string{
	"decimal": {"Decimal", ""}, "money": {"Decimal", ""}, "time_with_offset": {"OffsetTime", ""},
	"instant": {"Temporal", ".Instant"}, "instant_with_offset": {"Temporal", ".ZonedDateTime"},
	"local_datetime": {"Temporal", ".PlainDateTime"}, "date": {"Temporal", ".PlainDate"},
	"local_time": {"Temporal", ".PlainTime"}, "interval": {"Temporal", ".Duration"},
}

// builtinConverters decode an opaque type the types setting maps to a
// built-in type.
var builtinConverters = map[string]string{
	"string": "asString", "number": "asFloat64", "bigint": "asBigint", "boolean": "asBoolean", "Uint8Array": "asBytes",
}

// conv returns the converter of a non-null value of t: an expression of type
// Converter<T>, where T is t's TypeScript type (§13).
func (g *generator) conv(f *file, t *irv1.Type, at *irv1.QualifiedName, what string) string {
	if n := t.GetNarrowed(); n != nil {
		return f.rt("asserted", true) + "(" + g.narrowedConv(f, t, at, what) + ", " + jsString(n.GetAssertion()) + ")"
	}
	kind := t.ProtoReflect().WhichOneof(t.ProtoReflect().Descriptor().Oneofs().ByName("kind"))
	if kind == nil {
		g.tsType(f, t, at, what) // reports the unknown type
		return f.rt("unchecked", true)
	}
	name := string(kind.Name())
	if b := t.GetBit(); b != nil && !b.GetVarying() && b.Length != nil {
		// A bit(n) holds exactly n bits, which MySQL sends padded to bytes.
		return f.rt("fixedBits", true) + "(" + strconv.Itoa(int(b.GetLength())) + ")"
	}
	if c, ok := scalarConverters[name]; ok {
		return f.rt(c, true)
	}
	if b, ok := brands[name]; ok {
		return f.rt("asString", true) + "<" + f.rt(b, false) + ">"
	}
	if c, ok := instances[name]; ok {
		return f.rt("asInstanceOf", true) + "(" + f.rt(c[0], true) + c[1] + ")"
	}
	switch k := t.GetKind().(type) {
	case *irv1.Type_Range:
		return f.rt("asRange", true) + "(" + g.conv(f, k.Range.GetElement(), at, what) + ")"
	case *irv1.Type_Multirange:
		return f.rt("asMultirange", true) + "(" + g.conv(f, k.Multirange.GetElement(), at, what) + ")"
	case *irv1.Type_Array:
		a := k.Array
		elem := g.conv(f, a.GetElement(), at, what)
		if a.GetElementNullable() {
			elem = f.rt("nullableElem", true) + "(" + elem + ")"
		} else {
			elem = f.rt("elem", true) + "(" + elem + ")"
		}
		for range max(1, int(a.GetDimensions())) {
			elem = f.rt("array", true) + "(" + elem + ")"
		}
		return elem
	case *irv1.Type_Named:
		if e := k.Named.GetEnum(); e != nil {
			n, ok := g.named[keyOf(e)]
			if !ok {
				g.tsType(f, t, at, what)
				return f.rt("unchecked", true)
			}
			return f.rt("asEnum", true) + "(" + g.ref(f, TypeName(n.words), true) + ")"
		}
		q := firstName(k.Named.GetDomain(), k.Named.GetComposite())
		if _, ok := g.named[keyOf(q)]; !ok {
			g.tsType(f, t, at, what)
			return f.rt("unchecked", true)
		}
		return g.ref(f, g.namedDecoder(q), true)
	case *irv1.Type_Opaque:
		host := g.tsType(f, t, at, what)
		if c, ok := builtinConverters[g.opaqueHost(t)]; ok {
			return f.rt(c, true)
		}
		// An imported type, or one the emitter could not map, which the
		// types diagnostic reports.
		return f.rt("unchecked", true) + "<" + host + ">"
	default: // *irv1.Type_Weak; the oneof has no other kind
		return f.rt("weak", true) + "(" + g.conv(f, t.GetWeak().GetAffinity(), at, what) + ")"
	}
}

// narrowedConv returns the converter of a type an annotation narrowed, which
// reads the type the database stores: text holding a uuid or a time, a TIME
// holding a time of day, or a TINYINT(1) holding 0 or 1 (§7.2, §8.7, §13).
func (g *generator) narrowedConv(f *file, t *irv1.Type, at *irv1.QualifiedName, what string) string {
	temporal := func(class string) string {
		c := f.rt("Temporal", true) + "." + class
		return f.rt("asParsed", true) + "((s) => " + c + ".from(s), " + c + ")"
	}
	switch t.GetKind().(type) {
	case *irv1.Type_Uuid:
		return f.rt("asParsed", true) + "(" + f.rt("parseUuid", true) + ")"
	case *irv1.Type_Date:
		return temporal("PlainDate")
	case *irv1.Type_LocalDatetime:
		return temporal("PlainDateTime")
	case *irv1.Type_Instant:
		return f.rt("asParsed", true) + "(" + f.rt("parseInstant", true) + ", " + f.rt("Temporal", true) + ".Instant)"
	case *irv1.Type_LocalTime:
		if t.GetNarrowed().GetStored().GetInterval() != nil {
			return f.rt("asTimeOfDay", true)
		}
		return temporal("PlainTime")
	}
	plain := proto.CloneOf(t)
	plain.Narrowed = nil
	return g.conv(f, plain, at, what)
}

// firstName returns the first of two names that is set.
func firstName(a, b *irv1.QualifiedName) *irv1.QualifiedName {
	if a != nil {
		return a
	}
	return b
}

// namedDecoder returns the decoder of a domain or composite type, declaring
// it in dql-decoders.dql.ts the first time: a domain's converts its base
// type, and a composite type's decodes a Row of its attributes in order
// (§13).
func (g *generator) namedDecoder(q *irv1.QualifiedName) string {
	k := keyOf(q)
	if name, ok := g.namedDecoders[k]; ok {
		return name
	}
	n := g.named[k]
	typ := TypeName(n.words)
	name := "dqlDecode" + typ
	g.namedDecoders[k] = name
	if g.decodeFile == nil {
		g.decodeFile = g.newFile("dql-decoders")
		g.decodeFile.internal = true
	}
	f := g.decodeFile
	g.declare(f, name, "the decoder of "+displayName(q), q)
	typ = g.ref(f, typ, false)
	if n.composite == nil {
		conv := g.conv(f, n.base, q, "domain "+displayName(q))
		f.printf("/** %s decodes a value of domain %s (§13). */\nexport function %s(v: unknown): %s {\n  return %s(v) as %s;\n}\n\n",
			name, displayName(q), name, typ, conv, typ)
		return name
	}
	var members []string
	for i, a := range n.composite.GetAttributes() {
		aq := &irv1.QualifiedName{Catalog: q.GetCatalog(), Namespace: q.GetNamespace(), Object: q.GetObject(), Member: a.GetName().GetStored()}
		conv := g.conv(f, a.GetType(), aq, "attribute "+displayName(aq))
		get := fmt.Sprintf("d.nullable(%d, %s, %s)", i, jsString(a.GetName().GetStored()), conv)
		if !a.GetNullable() {
			get = fmt.Sprintf("d.field(%d, %s, %s, %s)", i, jsString(a.GetName().GetStored()), jsString(annotated(a.GetAnnotations(), "core.nonnull", aq)), conv)
		}
		members = append(members, MemberName(a.GetHostName().GetWords())+": "+get)
	}
	f.printf("/**\n * %s decodes a value of composite type\n * %s: a Row of its attributes in order (§13).\n */\n", name, displayName(q))
	f.printf("export function %s(v: unknown): %s {\n  const d = new %s(\"\", v, %d);\n  return %s;\n}\n\n",
		name, typ, f.rt("Decoder", true), len(members), objectLiteral(members, "  "))
	return name
}
