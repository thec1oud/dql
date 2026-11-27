package mapping

import (
	"fmt"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/internal/pgsql"
	"github.com/thec1oud/dql/sources/pglive/internal/introspect"
)

// resolver answers pgsql.Types for the enums, domains, composites, ranges and
// multiranges of the Snapshot. A type outside the namespaces read is unknown,
// so it is opaque (§8.3): a named reference to it would point outside the
// catalog.
func resolver(s *introspect.Snapshot) pgsql.Resolver {
	known := map[objectKey]pgsql.Resolved{}
	for _, e := range s.Enums {
		known[objectKey{e.Schema, e.Name}] = pgsql.Resolved{Namespace: e.Schema, Kind: pgsql.KindEnum}
	}
	for _, d := range s.Domains {
		known[objectKey{d.Schema, d.Name}] = pgsql.Resolved{Namespace: d.Schema, Kind: pgsql.KindDomain}
	}
	for _, c := range s.Composites {
		known[objectKey{c.Schema, c.Name}] = pgsql.Resolved{Namespace: c.Schema, Kind: pgsql.KindComposite}
	}
	for _, r := range s.Ranges {
		// A range's subtype is a scalar type with no array or typmod of its
		// own, so its native type is its name.
		sub := &pgsql.Native{Namespace: r.Subtype.Schema, Name: r.Subtype.Name, Raw: r.Subtype.Formatted}
		known[objectKey{r.Schema, r.Name}] = pgsql.Resolved{Namespace: r.Schema, Kind: pgsql.KindRange, Subtype: sub}
		mr := r.Multirange
		known[objectKey{mr.Schema, mr.Name}] = pgsql.Resolved{Namespace: mr.Schema, Kind: pgsql.KindMultirange, Subtype: sub}
	}
	return func(namespace, name string) (pgsql.Resolved, bool) {
		r, ok := known[objectKey{namespace, name}]
		return r, ok
	}
}

// native turns a catalog type reference into the native type pgsql maps. The
// modifiers of a built-in type are decoded from its typmod; any other type's
// come from format_type's spelling, which prints them (§8.2).
func native(ref introspect.TypeRef) (pgsql.Native, error) {
	n := pgsql.Native{Namespace: ref.Schema, Name: ref.Name, Raw: ref.Formatted}
	if e := ref.Element; e != nil {
		n.Namespace, n.Name, n.Array, n.ArrayName = e.Schema, e.Name, true, ref.Name
		n.Dimensions = uint32(max(ref.Dimensions, 0))
	}
	if ref.Typmod < 0 {
		return n, nil
	}
	if mods, ok := pgsql.DecodeTypmod(n.Name, ref.Typmod); ok && n.Namespace == "pg_catalog" {
		n.Modifiers = mods
		return n, nil
	}
	parsed, err := pgsql.ParseTypeText(ref.Formatted)
	if err != nil {
		return n, err
	}
	n.Modifiers = parsed.Modifiers
	return n, nil
}

// typeOf returns ref's logical type with its native type (§8.1, §8.2). what
// names the column, attribute or argument for an error.
func (m *mapper) typeOf(what string, ref introspect.TypeRef) *irv1.Type {
	n, err := native(ref)
	var t *irv1.Type
	if err == nil {
		t, err = m.types.LogicalType(n)
	}
	if err != nil {
		m.fail(fmt.Errorf("mapping: type %q of %s: %w", ref.Formatted, what, err))
	}
	return t
}

// baseOf follows a domain to the type it is ultimately over, the type check
// constraints test (§8.5). Any other type is itself.
func (m *mapper) baseOf(ref introspect.TypeRef) introspect.TypeRef {
	for ref.Kind == "d" {
		d, ok := m.domains[objectKey{ref.Schema, ref.Name}]
		if !ok {
			break
		}
		ref = d.Base
	}
	return ref
}

// collation turns a column's, attribute's or domain's collation into its
// comparison facts (§8.9), for the type it is ultimately over.
func (m *mapper) collation(c *introspect.CollationRef, ref introspect.TypeRef) *irv1.Collation {
	if c == nil {
		return nil
	}
	name := c.Name
	if c.Schema != "pg_catalog" {
		name = c.Schema + "." + c.Name
	}
	base := m.baseOf(ref)
	if e := base.Element; e != nil {
		base = introspect.TypeRef{Schema: e.Schema, Name: e.Name}
	}
	return pgsql.CollationFacts(pgsql.CollationDef{Name: name, Deterministic: c.Deterministic,
		ICU: c.Provider == "i", ICULocale: c.ICULocale}, base.Schema, base.Name)
}
