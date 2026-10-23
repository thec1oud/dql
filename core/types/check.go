package types

import (
	"github.com/thec1oud/dql/core/diagnostics"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Diagnostic codes of Check.
const (
	CodeTypeMissing         = "type-missing"
	CodeReferenceEmpty      = "type-reference-empty"
	CodeReferenceUnresolved = "type-reference-unresolved"
	CodeArrayElementMissing = "type-array-element-missing"
	CodeArrayDimensions     = "type-array-dimensions"
	CodeRangeElementMissing = "type-range-element-missing"
	CodeWeakAffinityMissing = "type-weak-affinity-missing"
	CodeOpaqueNativeMissing = "type-opaque-native-missing"
	CodeWeakColumn          = "type-weak-column"
	CodeOpaqueUnmapped      = "type-opaque-unmapped"
	CodeOpaqueFallback      = "type-opaque-fallback"
)

// Check checks every type in b and returns its diagnostics, sorted (§3.2
// rule 5). The checks are structural: a malformed type is a bug in the
// source plugin that produced it.
//
//   - Every typed place has a logical type; one the source cannot classify is
//     opaque and keeps its native type (§8.3).
//   - Enum, domain and composite references resolve to existing objects (§8.1).
//   - Arrays have an element type and at least one dimension (§8.6); ranges
//     have an element type and weak types an affinity (§8.1, §8.10).
//   - Every weak column produces a warning (§8.10).
//
// Opaque types are checked against their mappings by CheckOpaque.
func Check(b *irv1.Bundle) []*irv1.Diagnostic {
	ix := NewIndex(b)
	var ds []*irv1.Diagnostic
	sites(b, func(s site) {
		ds = append(ds, ix.checkType(s, s.typ, s.what)...)
		if s.column && s.typ.GetWeak() != nil {
			// §8.10: every weak column produces a generation warning.
			ds = append(ds, diagnostics.Warningf(CodeWeakColumn, s.target,
				"%s is weak: it can hold any storage class, so reads may return raw values of another class (§8.10); declare the table STRICT, or write only values of its declared affinity",
				s.what))
		}
	})
	diagnostics.Sort(ds)
	return ds
}

func (ix *Index) checkType(s site, t *irv1.Type, what string) []*irv1.Diagnostic {
	bug := func(code, format string, args ...any) []*irv1.Diagnostic {
		return []*irv1.Diagnostic{diagnostics.Errorf(code, s.target, format+"; this is a bug in the source plugin that produced the catalog", args...)}
	}
	if t.GetKind() == nil {
		return bug(CodeTypeMissing, "%s has no logical type: a type the source cannot classify must be opaque with its native type (§8.3)", what)
	}
	switch k := t.GetKind().(type) {
	case *irv1.Type_Named:
		if k.Named.GetKind() == nil {
			return bug(CodeReferenceEmpty, "%s is a named type that names no enum, domain or composite (§8.1)", what)
		}
		if !ix.resolves(k.Named) {
			return []*irv1.Diagnostic{diagnostics.Errorf(CodeReferenceUnresolved, s.target,
				"%s references %s %s, which is not in any catalog of the bundle (§8.1); include its namespace in the source's namespaces, or fix the source plugin",
				what, namedKind(k.Named), diagnostics.Name(namedName(k.Named)))}
		}
	case *irv1.Type_Array:
		var ds []*irv1.Diagnostic
		if k.Array.GetDimensions() < 1 {
			ds = bug(CodeArrayDimensions, "%s is an array with %d dimensions; an array has at least one (§8.6)", what, k.Array.GetDimensions())
		}
		if k.Array.GetElement() == nil {
			return append(ds, bug(CodeArrayElementMissing, "%s is an array without an element type (§8.6)", what)...)
		}
		return append(ds, ix.checkType(s, k.Array.GetElement(), "the element type of "+what)...)
	case *irv1.Type_Range:
		return ix.checkElement(s, k.Range, what)
	case *irv1.Type_Multirange:
		return ix.checkElement(s, k.Multirange, what)
	case *irv1.Type_Weak:
		if k.Weak.GetAffinity() == nil {
			return bug(CodeWeakAffinityMissing, "%s is weak without a declared affinity (§8.10)", what)
		}
		return ix.checkType(s, k.Weak.GetAffinity(), "the affinity of "+what)
	case *irv1.Type_Opaque:
		if t.GetNative().GetName().GetObject() == "" {
			return bug(CodeOpaqueNativeMissing, "%s is opaque without a native type name; an opaque type keeps its native type (§8.3)", what)
		}
	}
	return nil
}

func (ix *Index) checkElement(s site, r *irv1.RangeType, what string) []*irv1.Diagnostic {
	if r.GetElement() == nil {
		return []*irv1.Diagnostic{diagnostics.Errorf(CodeRangeElementMissing, s.target,
			"%s is a range without an element type (§8.1); this is a bug in the source plugin that produced the catalog", what)}
	}
	return ix.checkType(s, r.GetElement(), "the element type of "+what)
}

func namedKind(n *irv1.NamedRef) string {
	switch n.GetKind().(type) {
	case *irv1.NamedRef_Enum:
		return "enum"
	case *irv1.NamedRef_Domain:
		return "domain"
	}
	return "composite type"
}

func namedName(n *irv1.NamedRef) *irv1.QualifiedName {
	switch k := n.GetKind().(type) {
	case *irv1.NamedRef_Enum:
		return k.Enum
	case *irv1.NamedRef_Domain:
		return k.Domain
	}
	return n.GetComposite()
}
