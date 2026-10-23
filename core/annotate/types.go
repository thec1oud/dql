package annotate

import (
	"slices"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// structured lists the logical types that carry another type or a reference
// (§8.1). as(<logical type>) names a scalar type, so these are not values of it.
var structured = []string{"array", "multirange", "named", "opaque", "range", "weak"}

// logicalTypes returns the scalar logical type names, sorted. They are the
// field names of Type's kind oneof, so they follow the IR as it grows.
func logicalTypes() []string {
	var names []string
	fields := (&irv1.Type{}).ProtoReflect().Descriptor().Oneofs().ByName("kind").Fields()
	for i := range fields.Len() {
		if n := string(fields.Get(i).Name()); !slices.Contains(structured, n) {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	return names
}

// typeName returns the logical type's name, such as "int64" or "array", or
// "" when no type is set.
func typeName(t *irv1.Type) string {
	m := t.ProtoReflect()
	f := m.WhichOneof(m.Descriptor().Oneofs().ByName("kind"))
	if f == nil {
		return ""
	}
	return string(f.Name())
}

// The families of §8.1 that annotation rules test.
var (
	integers   = []string{"int8", "int16", "int32", "int64", "uint8", "uint16", "uint32", "uint64"}
	timestamps = []string{"instant", "instant_with_offset", "local_datetime"}
)

// describe names a type for a diagnostic.
func describe(t *irv1.Type) string {
	if n := typeName(t); n != "" {
		return n
	}
	return "untyped"
}
