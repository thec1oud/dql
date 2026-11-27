package pgsql

import (
	"errors"
	"fmt"

	pg "github.com/pganalyze/pg_query_go/v6"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// serials maps the serial pseudo-types to the integer types they declare.
var serials = map[string]string{
	"smallserial": "int2", "serial2": "int2",
	"serial": "int4", "serial4": "int4",
	"bigserial": "int8", "serial8": "int8",
}

// ParseTypeText reads a type written as in DDL, as dpg's snapshots and
// Postgres's format_type print it, and returns the native type pg-live reads
// from the catalog for the same declaration: "character varying(80)" is
// pg_catalog.varchar with modifier 80, "timestamp with time zone" is
// pg_catalog.timestamptz, "text[]" is an array of pg_catalog.text with one
// dimension. Raw keeps text as given (§8.2).
//
// A built-in name written unqualified gets pg_catalog, which Postgres searches
// first; any other unqualified name keeps an empty namespace for the Resolver.
// Modifiers follow Postgres's own normalization, so they equal DecodeTypmod's:
// numeric(p) is numeric(p,0), char is char(1) and bit is bit(1).
func ParseTypeText(text string) (Native, error) {
	tn, err := parseTypeName(text)
	if err != nil {
		return Native{}, err
	}
	n := Native{Raw: text}
	if tn.Setof {
		return Native{}, fmt.Errorf("pgsql: %q is not a column type", text)
	}
	parts, _ := names(tn.Names)
	switch len(parts) {
	case 1:
		n.Name = parts[0]
		if catalogTypes[n.Name] {
			n.Namespace = "pg_catalog"
		} else if base, ok := serials[n.Name]; ok {
			n.Namespace, n.Name, n.Serial = "pg_catalog", base, true
		}
	case 2:
		n.Namespace, n.Name = parts[0], parts[1]
	default:
		return Native{}, fmt.Errorf("pgsql: type name %q has more than two parts", text)
	}
	for _, m := range tn.Typmods {
		mod, err := typmod(m)
		if err != nil {
			return Native{}, fmt.Errorf("pgsql: type %q: %w", text, err)
		}
		n.Modifiers = append(n.Modifiers, mod)
	}
	if n.Namespace == "pg_catalog" && n.Name == "numeric" && len(n.Modifiers) == 1 {
		n.Modifiers = append(n.Modifiers, intMod(0)...)
	}
	if len(tn.ArrayBounds) > 0 {
		n.Array, n.Dimensions = true, uint32(len(tn.ArrayBounds))
	}
	return n, nil
}

// typmod reads one declared modifier: an integer, a string, or an identifier
// such as PostGIS's geometry(Point, 4326), which the parser lower-cases.
func typmod(m *pg.Node) (*irv1.TypeModifier, error) {
	c := m.GetAConst()
	switch {
	case c.GetIval() != nil:
		return &irv1.TypeModifier{Kind: &irv1.TypeModifier_Int{Int: c.GetIval().Ival}}, nil
	case c.GetSval() != nil:
		return &irv1.TypeModifier{Kind: &irv1.TypeModifier_String_{String_: c.GetSval().Sval}}, nil
	}
	if parts, ok := names(m.GetColumnRef().GetFields()); ok && len(parts) == 1 {
		return &irv1.TypeModifier{Kind: &irv1.TypeModifier_String_{String_: parts[0]}}, nil
	}
	return nil, errors.New("unsupported type modifier")
}
