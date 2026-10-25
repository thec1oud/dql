package gen

import (
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// CodeTypeUnknown: a type this emitter cannot map, such as a type kind of a
// newer IR minor version, or a reference to a named type the bundle lacks.
const CodeTypeUnknown = "go-type-unknown"

// The Go packages host types come from (§8.1).
const (
	pkgUUID = "github.com/google/uuid"
	pkgDec  = Runtime + "/dec"
	pkgCiv  = Runtime + "/civil"
	pkgDQL  = Runtime + "/dql"
	pkgWeak = Runtime + "/weak"
)

// scalars are the logical types whose host type is a predeclared Go type.
var scalars = map[string]string{
	"bool": "bool", "int8": "int8", "int16": "int16", "int32": "int32", "int64": "int64",
	"uint8": "uint8", "uint16": "uint16", "uint32": "uint32", "uint64": "uint64",
	"float32": "float32", "float64": "float64",
	"text": "string", "varchar": "string", "char": "string", "xml": "string",
	"bytes": "[]byte",
}

// qualified are the logical types whose host type is a named type of a
// package: the package's path, its name and the type's name.
var qualified = map[string][3]string{
	"decimal":             {pkgDec, "dec", "Decimal"},
	"money":               {pkgDec, "dec", "Decimal"},
	"instant":             {"time", "time", "Time"},
	"instant_with_offset": {pkgCiv, "civil", "OffsetDateTime"},
	"local_datetime":      {pkgCiv, "civil", "DateTime"},
	"date":                {pkgCiv, "civil", "Date"},
	"local_time":          {pkgCiv, "civil", "Time"},
	"time_with_offset":    {pkgCiv, "civil", "OffsetTime"},
	"interval":            {pkgDQL, "dql", "Interval"},
	"uuid":                {pkgUUID, "uuid", "UUID"},
	"json":                {"encoding/json", "json", "RawMessage"},
	"jsonb":               {"encoding/json", "json", "RawMessage"},
	"inet":                {"net/netip", "netip", "Addr"},
	"cidr":                {"net/netip", "netip", "Prefix"},
	"macaddr":             {"net", "net", "HardwareAddr"},
	"bit":                 {pkgDQL, "dql", "Bits"},
}

// goType returns the Go type of a non-null value of t (§8.1), importing what
// it needs into f. Instants map to time.Time and the local time types to
// civil types (§8.8 rule 1), decimals to dec.Decimal (§8.7), unsigned
// integers to unsigned types. at and what name the place that uses t, for
// diagnostics.
func (g *generator) goType(f *file, t *irv1.Type, at *irv1.QualifiedName, what string) string {
	kind := t.ProtoReflect().WhichOneof(t.ProtoReflect().Descriptor().Oneofs().ByName("kind"))
	if kind == nil {
		g.errorf(CodeTypeUnknown, at, "%s has a type the Go emitter does not know; use an emitter that reads this bundle's IR version", what)
		return "any"
	}
	name := string(kind.Name())
	if s, ok := scalars[name]; ok {
		return s
	}
	if q, ok := qualified[name]; ok {
		return f.use(q[0], q[1]) + "." + q[2]
	}
	switch k := t.GetKind().(type) {
	case *irv1.Type_Range:
		return f.use(pkgDQL, "dql") + ".Range[" + g.goType(f, k.Range.GetElement(), at, what) + "]"
	case *irv1.Type_Multirange:
		return "[]" + f.use(pkgDQL, "dql") + ".Range[" + g.goType(f, k.Multirange.GetElement(), at, what) + "]"
	case *irv1.Type_Array:
		return g.arrayType(f, k.Array, at, what)
	case *irv1.Type_Named:
		return g.namedType(k.Named, at, what)
	case *irv1.Type_Opaque:
		return g.opaqueType(f, t, at, what)
	default: // *irv1.Type_Weak; the oneof has no other kind
		return f.use(pkgWeak, "weak") + ".Weak[" + g.goType(f, t.GetWeak().GetAffinity(), at, what) + "]"
	}
}

// field returns the Go type of a value of t that may be NULL: *T when
// nullable (§14.1).
func (g *generator) field(f *file, t *irv1.Type, nullable bool, at *irv1.QualifiedName, what string) string {
	s := g.goType(f, t, at, what)
	if nullable {
		return "*" + s
	}
	return s
}

// arrayType nests one slice per declared dimension, with pointer elements
// unless array-elements-nonnull narrows them (§8.6). An array whose
// dimensions the source did not record has one.
func (g *generator) arrayType(f *file, a *irv1.ArrayType, at *irv1.QualifiedName, what string) string {
	elem := g.field(f, a.GetElement(), a.GetElementNullable(), at, what)
	return strings.Repeat("[]", max(1, int(a.GetDimensions()))) + elem
}

// namedType is the generated type of an enum, domain or composite type.
func (g *generator) namedType(r *irv1.NamedRef, at *irv1.QualifiedName, what string) string {
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
		return "any"
	}
	return Exported(n.words)
}

// opaqueType maps an opaque type through the types setting, else through the
// fallback the core recorded (§8.3).
func (g *generator) opaqueType(f *file, t *irv1.Type, at *irv1.QualifiedName, what string) string {
	n := t.GetNative().GetName()
	native := n.GetNamespace() + "." + n.GetObject()
	if spec, ok := g.o.Types[native]; ok {
		path, name, ok := splitQualified(spec)
		if !ok {
			return spec
		}
		return f.use(path, packageName(path)) + "." + name
	}
	switch t.GetOpaque().GetFallback() {
	case irv1.OpaqueFallback_OPAQUE_FALLBACK_STRING:
		return "string"
	case irv1.OpaqueFallback_OPAQUE_FALLBACK_BYTES:
		return "[]byte"
	}
	g.errorf(CodeOpaqueUnmapped, at, "%s has opaque type %s, which the go emitter's types setting does not map (§8.3); add %s: { go: <type> } under types", what, native, native)
	return "any"
}

// splitQualified splits import/path.Name into its import path and type name.
// A spec without a slash, such as string or []byte, is a type of its own.
func splitQualified(spec string) (path, name string, ok bool) {
	if !strings.Contains(spec, "/") {
		return "", "", false
	}
	i := strings.LastIndex(spec, ".")
	if i < strings.LastIndex(spec, "/") {
		return "", "", false
	}
	return spec[:i], spec[i+1:], true
}

// packageName is the name an import path is used under: its last element,
// or the one before a major-version suffix such as v5.
func packageName(path string) string {
	parts := strings.Split(path, "/")
	last := parts[len(parts)-1]
	if len(parts) > 1 && len(last) > 1 && last[0] == 'v' && strings.Trim(last[1:], "0123456789") == "" {
		last = parts[len(parts)-2]
	}
	return strings.Map(func(r rune) rune {
		if r == '-' || r == '.' {
			return '_'
		}
		return r
	}, last)
}
