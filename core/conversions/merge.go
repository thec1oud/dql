package conversions

import (
	"github.com/thec1oud/dql/core/facts"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// merges returns how Partials merge into Full rows by key for every keyed
// table (§8.9, §11.1): Total when the host can compare every key column
// exactly, after a certified fold where one is needed, and Fallible
// otherwise, because a change whose key matches no row byte for byte might
// match one under the database's comparison. Views get only Full (§14.3),
// so they have nothing to merge.
func (d *deriver) merges() []*irv1.KeyMerge {
	var out []*irv1.KeyMerge
	for _, qn := range d.order {
		t := d.tables[qnKey(qn)]
		cols := facts.Key(t)
		if t.GetKind() != irv1.TableKind_RELATION_KIND_TABLE || cols == nil {
			continue
		}
		m := &irv1.KeyMerge{Entity: qn, Kind: irv1.ConversionKind_CONVERSION_TOTAL}
		for _, name := range cols {
			k := d.compare(d.column(qn, name))
			k.Column = name
			if k.GetComparison() == irv1.Comparison_COMPARISON_INEXACT {
				m.Kind = irv1.ConversionKind_CONVERSION_FALLIBLE
			}
			m.Key = append(m.Key, k)
		}
		out = append(out, m)
	}
	return out
}

// exactKinds are the logical types whose values the host compares exactly as
// the database does: equal values have one representation.
var exactKinds = map[string]bool{
	"bool": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"uuid": true, "date": true, "bytes": true, "enum": true,
}

// compare decides how the host compares a key column (§8.9). A collation,
// the column's own or else the nearest domain's, decides: exact when it is
// case- and accent-sensitive, does not pad and is deterministic, folded when
// its dialect plugin certifies a client fold, and inexact otherwise, which
// includes every unknown fact. Without a collation the type decides: the
// types of exactKinds are exact and any other is inexact, because its values
// can compare equal in the database with different host representations.
func (d *deriver) compare(c *irv1.Column) *irv1.KeyComparison {
	t, coll := d.comparedAs(c)
	if coll != nil {
		switch {
		case coll.GetCaseSensitive() == irv1.Tristate_TRISTATE_TRUE &&
			coll.GetAccentSensitive() == irv1.Tristate_TRISTATE_TRUE &&
			coll.GetPadSpace() == irv1.Tristate_TRISTATE_FALSE &&
			coll.GetDeterministic() == irv1.Tristate_TRISTATE_TRUE:
			return &irv1.KeyComparison{Comparison: irv1.Comparison_COMPARISON_EXACT}
		case coll.GetClientFold() == irv1.ClientFold_CLIENT_FOLD_ASCII_LOWER:
			return &irv1.KeyComparison{Comparison: irv1.Comparison_COMPARISON_FOLDED, Fold: coll.GetClientFold()}
		}
		return &irv1.KeyComparison{Comparison: irv1.Comparison_COMPARISON_INEXACT}
	}
	if exactKinds[kind(t)] {
		return &irv1.KeyComparison{Comparison: irv1.Comparison_COMPARISON_EXACT}
	}
	return &irv1.KeyComparison{Comparison: irv1.Comparison_COMPARISON_INEXACT}
}

// comparedAs follows domains to the type the column's values are compared
// in, and returns it with the collation that decides text comparison: the
// column's own, else the nearest domain's (§8.5, §8.9). The type is nil when
// a domain does not resolve or the chain loops.
func (d *deriver) comparedAs(c *irv1.Column) (*irv1.Type, *irv1.Collation) {
	t, coll := c.GetType(), c.GetCollation()
	seen := map[string]bool{}
	for q := t.GetNamed().GetDomain(); q != nil; q = t.GetNamed().GetDomain() {
		dom := d.ix.Domain(q)
		if dom == nil || seen[qnKey(q)] {
			return nil, coll
		}
		seen[qnKey(q)] = true
		if coll == nil {
			coll = dom.GetCollation()
		}
		t = dom.GetBase()
	}
	return t, coll
}

// kind names a type's logical kind: its oneof field name, or "enum" for a
// reference to an enum.
func kind(t *irv1.Type) string {
	if t.GetNamed().GetEnum() != nil {
		return "enum"
	}
	m := t.ProtoReflect()
	if f := m.WhichOneof(m.Descriptor().Oneofs().ByName("kind")); f != nil {
		return string(f.Name())
	}
	return ""
}
