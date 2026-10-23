package mapping

import (
	"cmp"
	"fmt"
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/internal/pgsql"
)

// candidates are the schemas a name written without one is looked up in, by
// an object of schema: its own, then public (fidelity.SearchPath).
func candidates(schema, written string) []string {
	if written != "" {
		return []string{written}
	}
	return []string{schema, "public"}
}

// resolver answers pgsql.Types for the enums, domains and composites of the
// snapshot, as used by an object of schema. A range records no subtype and a
// base type no definition, so columns of either are opaque
// (fidelity.RangeSubtypes, §8.3), as is a type the snapshot does not hold.
func (m *mapper) resolver(schema string) pgsql.Resolver {
	return func(namespace, name string) (pgsql.Resolved, bool) {
		for _, sc := range candidates(schema, namespace) {
			if t, ok := m.typeDefs[objectKey{sc, name}]; ok {
				return pgsql.Resolved{Namespace: sc, Kind: typeKinds[t.Variant]}, true
			}
		}
		return pgsql.Resolved{}, false
	}
}

// typeKinds maps dpg's type variants; RANGE and BASE are KindOther.
var typeKinds = map[string]pgsql.TypeKind{
	"ENUM":      pgsql.KindEnum,
	"DOMAIN":    pgsql.KindDomain,
	"COMPOSITE": pgsql.KindComposite,
}

// typeOf returns the logical type of a type written as dpg writes it, with
// its native type (§8.1, §8.2), as used by an object of schema. what names
// the column, attribute or argument for an error.
func (m *mapper) typeOf(what, schema, text string) *irv1.Type {
	n, err := pgsql.ParseTypeText(text)
	var t *irv1.Type
	if err == nil {
		t, err = pgsql.Types{Catalog: m.opts.Catalog, Resolve: m.resolver(schema)}.LogicalType(n)
	}
	if err != nil {
		m.fail(fmt.Errorf("mapping: type %q of %s: %w", text, what, err))
	}
	return t
}

// domainOf returns the domain a type written as text names, used by an
// object of schema, or nil.
func (m *mapper) domainOf(schema, text string) (string, bool) {
	n, err := pgsql.ParseTypeText(text)
	if err != nil || n.Array {
		return "", false
	}
	for _, sc := range candidates(schema, n.Namespace) {
		if t, ok := m.typeDefs[objectKey{sc, n.Name}]; ok && t.Variant == "DOMAIN" {
			return sc + "." + n.Name, true
		}
	}
	return "", false
}

// base follows a domain to the type it is ultimately over, the type check
// constraints test and collations compare (§8.5): it returns that type's text
// with the schema it is written in, and the collation the nearest domain on
// the way declares. Any other type is itself.
func (m *mapper) base(schema, text string) (baseSchema, baseText, collation string) {
	seen := map[string]bool{}
	for {
		q, ok := m.domainOf(schema, text)
		if !ok || seen[q] {
			return schema, text, collation
		}
		seen[q] = true
		s, name, _ := strings.Cut(q, ".")
		d := m.typeDefs[objectKey{s, name}]
		if collation == "" {
			collation = d.DomainCollation
		}
		schema, text = d.Schema, d.DomainBaseType
	}
}

// checkType is the type a check constraint on a value of the type written as
// text is recognized against: its base type.
func (m *mapper) checkType(what, schema, text string) *irv1.Type {
	s, t, _ := m.base(schema, text)
	return m.typeOf(what, s, t)
}

// collation returns the collation of a column, attribute or domain of the
// type written as text, with its comparison facts (§8.9), as pg-live reads it
// from the database: the collation declared, else the one its domain
// declares, else the database's, "default". Only the built-in text types are
// known to be collatable, so a value of any other type has no collation here
// (fidelity.CollationFacts).
func (m *mapper) collation(schema, text, declared string) *irv1.Collation {
	_, bt, domain := m.base(schema, text)
	n, err := pgsql.ParseTypeText(bt)
	if err != nil || n.Namespace != "pg_catalog" || collatable[n.Name] == "" {
		return nil
	}
	def := m.collationDef(schema, cmp.Or(declared, domain, collatable[n.Name]))
	if !def.known {
		out := &irv1.Collation{Name: def.Name}
		if n.Name == "bpchar" {
			out.PadSpace = irv1.Tristate_TRISTATE_TRUE
		}
		return out
	}
	return pgsql.CollationFacts(def.CollationDef, n.Namespace, n.Name)
}

// collatable maps the built-in collatable types to the collation a value of
// them has by default: the database's, except for name, whose is C.
var collatable = map[string]string{"text": "default", "varchar": "default", "bpchar": "default", "name": "C"}

// collationDef is a collation as the snapshot knows it.
type collationDef struct {
	pgsql.CollationDef
	// known is set when the facts are known: for pg_catalog's collations,
	// which are all deterministic, as the database's default must be, and for
	// a collation the snapshot defines with its properties.
	known bool
}

// collationDef resolves a collation name as dpg writes it, schema.name or
// name, used by an object of schema. A name without a schema is a collation
// of the snapshot in the object's schema or public, or else pg_catalog's.
func (m *mapper) collationDef(schema, written string) collationDef {
	ns, name, qualified := strings.Cut(written, ".")
	if !qualified {
		ns, name = "", written
		for _, sc := range candidates(schema, "") {
			if _, ok := m.collations[objectKey{sc, name}]; ok {
				ns = sc
				break
			}
		}
	}
	if ns == "" || ns == "pg_catalog" {
		return collationDef{CollationDef: pgsql.CollationDef{Name: name, Deterministic: true}, known: true}
	}
	def := collationDef{CollationDef: pgsql.CollationDef{Name: ns + "." + name}}
	c, ok := m.collations[objectKey{ns, name}]
	if !ok || !c.CollationStructured || c.CollationCopyFrom != nil {
		return def
	}
	def.known = true
	def.Deterministic = c.CollationDeterministic
	def.ICU = c.CollationProvider == "icu"
	def.ICULocale = deref(c.CollationICULocale)
	return def
}
