package types

import (
	"slices"

	"github.com/thec1oud/dql/core/diagnostics"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// The values of the project-wide opaque-fallback setting (§8.3).
const (
	FallbackNone   = ""
	FallbackString = "string"
	FallbackBytes  = "bytes"
)

// fallbacks are the IR spellings of the opaque-fallback values.
var fallbacks = map[string]irv1.OpaqueFallback{
	FallbackString: irv1.OpaqueFallback_OPAQUE_FALLBACK_STRING,
	FallbackBytes:  irv1.OpaqueFallback_OPAQUE_FALLBACK_BYTES,
}

// CheckOpaque checks that every opaque type is mapped (§8.3). mapped holds
// the native types that type plugins and emitter config map, such as
// public.citext; a native type matches on namespace and object, since native
// type names carry no catalog. fallback is the opaque-fallback setting.
//
// An opaque type that nothing maps fails generation with an error at every
// place that uses it. With opaque-fallback set to string or bytes it falls
// back instead, with a warning for every affected place. Columns of a domain
// whose base is opaque are affected too. Any other fallback value counts as
// unset; config validation rejects it (config-opaque-fallback-invalid).
//
// Where the fallback applies, CheckOpaque records it on the opaque type
// itself, so emitters know the host type without reading project config;
// the native type stays as it is.
func CheckOpaque(b *irv1.Bundle, mapped []*irv1.QualifiedName, fallback string) []*irv1.Diagnostic {
	ix := NewIndex(b)
	isMapped := func(n *irv1.QualifiedName) bool {
		return slices.ContainsFunc(mapped, func(m *irv1.QualifiedName) bool {
			return m.GetNamespace() == n.GetNamespace() && m.GetObject() == n.GetObject()
		})
	}
	if mark, ok := fallbacks[fallback]; ok {
		sites(b, func(s site) {
			walk(s.typ, func(t *irv1.Type) {
				if o := t.GetOpaque(); o != nil && t.GetNative().GetName().GetObject() != "" && !isMapped(t.GetNative().GetName()) {
					o.Fallback = mark
				}
			})
		})
	}
	var ds []*irv1.Diagnostic
	sites(b, func(s site) {
		for _, n := range ix.opaqueNatives(s.typ) {
			if isMapped(n) {
				continue
			}
			name := diagnostics.Name(n)
			if fallback == FallbackString || fallback == FallbackBytes {
				ds = append(ds, diagnostics.Warningf(CodeOpaqueFallback, s.target,
					"%s has opaque type %s, which no type plugin or emitter config maps, so it falls back to %s (opaque-fallback, §8.3); map %s in the emitter's types config to choose its host type",
					s.what, name, fallback, name))
				continue
			}
			ds = append(ds, diagnostics.Errorf(CodeOpaqueUnmapped, s.target,
				"%s has opaque type %s, which no type plugin or emitter config maps (§8.3); map %s in the emitter's types config, add a type plugin for it, or set opaque-fallback to string or bytes",
				s.what, name, name))
		}
	})
	diagnostics.Sort(ds)
	return ds
}

// opaqueNatives returns the native names of the opaque types t uses, through
// arrays, ranges, weak affinities and domains, without duplicates, in the
// order found. Opaque types without a native name are Check's concern.
func (ix *Index) opaqueNatives(t *irv1.Type) []*irv1.QualifiedName {
	var out []*irv1.QualifiedName
	natives, domains := map[key]bool{}, map[key]bool{}
	var visit func(*irv1.Type)
	visit = func(t *irv1.Type) {
		walk(t, func(t *irv1.Type) {
			if t.GetOpaque() != nil {
				n := t.GetNative().GetName()
				if k := keyOf(n); n.GetObject() != "" && !natives[k] {
					natives[k] = true
					out = append(out, n)
				}
			}
			if q := t.GetNamed().GetDomain(); q != nil {
				if d := ix.Domain(q); d != nil && !domains[keyOf(q)] {
					domains[keyOf(q)] = true
					visit(d.GetBase())
				}
			}
		})
	}
	visit(t)
	return out
}
