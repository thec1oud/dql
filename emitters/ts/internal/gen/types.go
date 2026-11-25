package gen

import (
	"regexp"
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// builtins are the logical types whose host type is a TypeScript built-in
// (§8.1): 64-bit integers are bigint, smaller ones number.
var builtins = map[string]string{
	"bool": "boolean", "int8": "number", "int16": "number", "int32": "number", "int64": "bigint",
	"uint8": "number", "uint16": "number", "uint32": "number", "uint64": "bigint",
	"float32": "number", "float64": "number",
	"text": "string", "varchar": "string", "char": "string", "xml": "string", "bit": "string",
	"bytes": "Uint8Array",
}

// runtimeTypes are the logical types whose host type the runtime provides
// (§8.1, §8.8): its name, and for a Temporal type the type within Temporal.
var runtimeTypes = map[string][2]string{
	"decimal":             {"Decimal", ""},
	"money":               {"Decimal", ""},
	"instant":             {"Temporal", "Instant"},
	"instant_with_offset": {"Temporal", "ZonedDateTime"},
	"local_datetime":      {"Temporal", "PlainDateTime"},
	"date":                {"Temporal", "PlainDate"},
	"local_time":          {"Temporal", "PlainTime"},
	"time_with_offset":    {"OffsetTime", ""},
	"interval":            {"Temporal", "Duration"},
	"uuid":                {"Uuid", ""},
	"json":                {"JsonValue", ""},
	"jsonb":               {"JsonValue", ""},
	"inet":                {"Inet", ""},
	"cidr":                {"Cidr", ""},
	"macaddr":             {"MacAddr", ""},
}

// tsType returns the TypeScript type of a non-null value of t (§8.1),
// importing what it needs into f. Instants map to Temporal.Instant and the
// local time types to Temporal's plain types (§8.8 rule 1), decimals to
// Decimal (§8.7), 64-bit integers to bigint. at and what name the place that
// uses t, for diagnostics.
func (g *generator) tsType(f *file, t *irv1.Type, at *irv1.QualifiedName, what string) string {
	kind := t.ProtoReflect().WhichOneof(t.ProtoReflect().Descriptor().Oneofs().ByName("kind"))
	if kind == nil {
		g.errorf(CodeTypeUnknown, at, "%s has a type the TypeScript emitter does not know; use an emitter that reads this bundle's IR version", what)
		return "unknown"
	}
	name := string(kind.Name())
	if s, ok := builtins[name]; ok {
		return s
	}
	if r, ok := runtimeTypes[name]; ok {
		if r[1] != "" {
			return f.rt(r[0], false) + "." + r[1]
		}
		return f.rt(r[0], false)
	}
	switch k := t.GetKind().(type) {
	case *irv1.Type_Range:
		return f.rt("Range", false) + "<" + g.tsType(f, k.Range.GetElement(), at, what) + ">"
	case *irv1.Type_Multirange:
		return f.rt("Range", false) + "<" + g.tsType(f, k.Multirange.GetElement(), at, what) + ">[]"
	case *irv1.Type_Array:
		return g.arrayType(f, k.Array, at, what)
	case *irv1.Type_Named:
		return g.namedType(f, k.Named, at, what)
	case *irv1.Type_Opaque:
		return g.opaqueType(f, t, at, what)
	default: // *irv1.Type_Weak; the oneof has no other kind
		return f.rt("Weak", false) + "<" + g.tsType(f, t.GetWeak().GetAffinity(), at, what) + ">"
	}
}

// field returns the TypeScript type of a value of t that may be NULL: T | null
// when nullable (§14.1).
func (g *generator) field(f *file, t *irv1.Type, nullable bool, at *irv1.QualifiedName, what string) string {
	s := g.tsType(f, t, at, what)
	if nullable {
		return s + " | null"
	}
	return s
}

// arrayType nests one array per declared dimension, with elements T | null
// unless array-elements-nonnull narrows them (§8.6). An array whose
// dimensions the source did not record has one.
func (g *generator) arrayType(f *file, a *irv1.ArrayType, at *irv1.QualifiedName, what string) string {
	elem := g.field(f, a.GetElement(), a.GetElementNullable(), at, what)
	return parenthesize(elem) + strings.Repeat("[]", max(1, int(a.GetDimensions())))
}

// namedType is the generated type of an enum, domain or composite type.
func (g *generator) namedType(f *file, r *irv1.NamedRef, at *irv1.QualifiedName, what string) string {
	q := r.GetEnum()
	if q == nil {
		q = r.GetDomain()
	}
	if q == nil {
		q = r.GetComposite()
	}
	n, ok := g.named[keyOf(q)]
	if !ok {
		g.errorf(CodeTypeUnknown, at, "%s references a named type the bundle does not hold", what)
		return "unknown"
	}
	return g.ref(f, TypeName(n.words), false)
}

// opaqueType maps an opaque type through the types setting, else through the
// fallback the core recorded (§8.3).
func (g *generator) opaqueType(f *file, t *irv1.Type, at *irv1.QualifiedName, what string) string {
	n := t.GetNative().GetName()
	native := n.GetNamespace() + "." + n.GetObject()
	if spec, ok := g.o.Types[native]; ok {
		module, name, ok := SplitImported(spec)
		if !ok {
			return spec
		}
		return f.use(module, name, false)
	}
	switch t.GetOpaque().GetFallback() {
	case irv1.OpaqueFallback_OPAQUE_FALLBACK_STRING:
		return "string"
	case irv1.OpaqueFallback_OPAQUE_FALLBACK_BYTES:
		return "Uint8Array"
	}
	g.errorf(CodeOpaqueUnmapped, at, "%s has opaque type %s, which the ts emitter's types setting does not map (§8.3); add %s: { ts: <type> } under types", what, native, native)
	return "unknown"
}

// BuiltinTypes are the TypeScript types the types setting may name without a
// module.
var BuiltinTypes = []string{"Uint8Array", "bigint", "boolean", "number", "string", "unknown"}

var imported = regexp.MustCompile(`^([^#\s]+)#([A-Za-z_$][A-Za-z0-9_$]*)$`)

// SplitImported splits module#Name, a type the types setting imports, into
// the module specifier and the exported type's name.
func SplitImported(spec string) (module, name string, ok bool) {
	m := imported.FindStringSubmatch(spec)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// parenthesize wraps a union or intersection type in parentheses, so that a
// suffix such as [] or an & applies to the whole of it.
func parenthesize(t string) string {
	depth := 0
	for i, r := range t {
		switch r {
		case '(', '<', '{', '[':
			depth++
		case ')', '>', '}', ']':
			depth--
		case '|', '&':
			if depth == 0 && i > 0 && t[i-1] == ' ' {
				return "(" + t + ")"
			}
		}
	}
	return t
}
