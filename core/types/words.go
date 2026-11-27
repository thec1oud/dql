package types

import (
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// TypeWords returns the words of t's logical type, which overload names
// append for hosts without overloading (§4.6): int64 is [int64], decimal(12,2)
// is [decimal], instant_with_offset is [instant with offset].
//
// The words are the logical type's spelling in §8.1 split as identifiers are
// (§4.2), and type parameters (lengths, precision, dimensions) are not words:
// array<int32> is [array int32], range<date> is [range date], weak<int64> is
// [weak int64], and an opaque type is [opaque] whatever its native type.
//
// A named type's words are the words of the enum, domain or composite it
// references, which only the catalog holds, so TypeWords returns nil for any
// type that contains a named reference; Index.TypeWords resolves those. It
// also returns nil for a type without a kind.
func TypeWords(t *irv1.Type) []string {
	return typeWords(t, func(*irv1.NamedRef) []string { return nil })
}

// TypeWords is TypeWords with named references resolved: a reference
// contributes the words of the object it names, such as [post status] for
// shop.app.post_status. It returns nil when a reference does not resolve.
func (ix *Index) TypeWords(t *irv1.Type) []string {
	return typeWords(t, func(n *irv1.NamedRef) []string {
		switch k := n.GetKind().(type) {
		case *irv1.NamedRef_Enum:
			return ix.Enum(k.Enum).GetName().GetWords()
		case *irv1.NamedRef_Domain:
			return ix.Domain(k.Domain).GetName().GetWords()
		}
		return ix.Composite(n.GetComposite()).GetName().GetWords()
	})
}

// scalarWords are the words of every logical type without a type parameter
// that is itself a type.
var scalarWords = map[string][]string{
	"bool": {"bool"}, "int8": {"int8"}, "int16": {"int16"}, "int32": {"int32"}, "int64": {"int64"},
	"uint8": {"uint8"}, "uint16": {"uint16"}, "uint32": {"uint32"}, "uint64": {"uint64"},
	"float32": {"float32"}, "float64": {"float64"}, "decimal": {"decimal"}, "money": {"money"},
	"text": {"text"}, "varchar": {"varchar"}, "char": {"char"}, "bytes": {"bytes"},
	"instant": {"instant"}, "instant_with_offset": {"instant", "with", "offset"},
	"local_datetime": {"local", "datetime"}, "date": {"date"}, "local_time": {"local", "time"},
	"time_with_offset": {"time", "with", "offset"}, "interval": {"interval"},
	"uuid": {"uuid"}, "json": {"json"}, "jsonb": {"jsonb"}, "xml": {"xml"},
	"inet": {"inet"}, "cidr": {"cidr"}, "macaddr": {"macaddr"}, "bit": {"bit"},
	"opaque": {"opaque"},
}

func typeWords(t *irv1.Type, named func(*irv1.NamedRef) []string) []string {
	var head string
	var elem *irv1.Type
	switch k := t.GetKind().(type) {
	case nil:
		return nil
	case *irv1.Type_Named:
		return clone(named(k.Named))
	case *irv1.Type_Array:
		head, elem = "array", k.Array.GetElement()
	case *irv1.Type_Range:
		head, elem = "range", k.Range.GetElement()
	case *irv1.Type_Multirange:
		head, elem = "multirange", k.Multirange.GetElement()
	case *irv1.Type_Weak:
		head, elem = "weak", k.Weak.GetAffinity()
	default:
		return clone(scalarWords[KindName(t)])
	}
	rest := typeWords(elem, named)
	if rest == nil {
		return nil
	}
	return append([]string{head}, rest...)
}

// KindName returns the name of t's kind, which is its logical type family's
// name in §8.1: "int64", "varchar", "instant_with_offset", "array", "named",
// "opaque". It returns "" for a type without a kind.
func KindName(t *irv1.Type) string {
	m := t.ProtoReflect()
	if !m.IsValid() {
		return ""
	}
	fd := m.WhichOneof(m.Descriptor().Oneofs().ByName("kind"))
	if fd == nil {
		return ""
	}
	return string(fd.Name())
}

func clone(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return append([]string(nil), s...)
}
