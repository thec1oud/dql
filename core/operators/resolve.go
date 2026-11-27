package operators

import (
	"cmp"
	"slices"

	"github.com/thec1oud/dql/core/diagnostics"
	"github.com/thec1oud/dql/core/types"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// CodeOperatorUnknown: operators: allow-approximate in dql.yaml names an
// operator the catalog does not have.
const CodeOperatorUnknown = "operator-unknown"

// Options are the operators: settings of dql.yaml.
type Options struct {
	// AllowApproximate names the operators generated where the dialect
	// performs them only approximately (§9.3).
	AllowApproximate []string
}

// Resolve sets the operators of every table column in b: those of
// b.Operators whose first operand the column fits, that the dialect's
// capability profile supports on the column exactly or by emulation, or
// approximately when opts allows it (§9.3). It returns the diagnostics,
// sorted; an error fails generation.
func Resolve(b *irv1.Bundle, opts Options) []*irv1.Diagnostic {
	var ds []*irv1.Diagnostic
	for _, name := range opts.AllowApproximate {
		if !slices.ContainsFunc(b.GetOperators(), func(o *irv1.Operator) bool { return o.GetName() == name }) {
			ds = append(ds, diagnostics.Errorf(CodeOperatorUnknown, nil,
				"operators: allow-approximate names %q, which is no operator of the catalog (§9.1)", name))
		}
	}
	if len(ds) > 0 {
		return ds
	}
	r := resolver{ix: types.NewIndex(b), support: map[string]*irv1.OperatorSupport{}}
	for _, s := range b.GetCapabilities().GetOperators() {
		r.support[s.GetName()] = s
	}
	for _, c := range b.GetCatalogs() {
		for _, ns := range c.GetNamespaces() {
			for _, t := range ns.GetTables() {
				for _, col := range t.GetColumns() {
					col.Operators = r.column(b.GetOperators(), col, opts.AllowApproximate)
				}
			}
		}
	}
	return nil
}

type resolver struct {
	ix      *types.Index
	support map[string]*irv1.OperatorSupport
}

// column returns the operators col supports, sorted by name.
func (r *resolver) column(ops []*irv1.Operator, col *irv1.Column, approximate []string) []*irv1.ColumnOperator {
	t, coll := r.underlying(col.GetType(), col.GetCollation())
	var out []*irv1.ColumnOperator
	for _, op := range ops {
		args := op.GetSignature().GetArgs()
		if len(args) == 0 || !r.fits(op, args[0], t, col.GetNullable()) {
			continue
		}
		f := fidelity(r.support[op.GetName()], t, coll)
		if f == irv1.Fidelity_FIDELITY_UNSPECIFIED || f == irv1.Fidelity_FIDELITY_APPROXIMATE && !slices.Contains(approximate, op.GetName()) {
			continue
		}
		out = append(out, &irv1.ColumnOperator{Name: op.GetName(), Fidelity: f})
	}
	slices.SortFunc(out, func(a, b *irv1.ColumnOperator) int { return cmp.Compare(a.GetName(), b.GetName()) })
	return out
}

// fits reports whether a value of type t, nullable or not, can be the
// operand a of op.
func (r *resolver) fits(op *irv1.Operator, a *irv1.OperandType, t *irv1.Type, nullable bool) bool {
	switch k := a.GetKind().(type) {
	case *irv1.OperandType_Var:
		for _, req := range op.GetRequires() {
			if req == Nullable && !nullable || req != Nullable && !r.meets(t, req) {
				return false
			}
		}
		return true
	case *irv1.OperandType_Type:
		return textual(k.Type) && textual(t)
	case *irv1.OperandType_Array:
		e, _ := r.underlying(t.GetArray().GetElement(), nil)
		return t.GetArray() != nil && r.fits(op, k.Array, e, false)
	case *irv1.OperandType_Range:
		e, _ := r.underlying(t.GetRange().GetElement(), nil)
		return t.GetRange() != nil && r.fits(op, k.Range, e, false)
	}
	return false
}

// underlying follows domains to their base type, and a weak type to its
// affinity, with the collation that decides text comparison: the given one,
// else the nearest domain's (§8.5, §8.9, §8.10). It returns nil when a
// domain does not resolve or a chain loops.
func (r *resolver) underlying(t *irv1.Type, coll *irv1.Collation) (*irv1.Type, *irv1.Collation) {
	seen := map[*irv1.Domain]bool{}
	for {
		if w := t.GetWeak(); w != nil {
			t = w.GetAffinity()
			continue
		}
		q := t.GetNamed().GetDomain()
		if q == nil {
			return t, coll
		}
		d := r.ix.Domain(q)
		if d == nil || seen[d] {
			return nil, coll
		}
		seen[d] = true
		if coll == nil {
			coll = d.GetCollation()
		}
		t = d.GetBase()
	}
}

// meets reports whether type t meets requirement req
// (plans/operator-catalog-proposal.md §2). An opaque type meets none until
// type plugins declare its semantics (§8.3).
func (r *resolver) meets(t *irv1.Type, req string) bool {
	switch req {
	case Comparable:
		return r.comparable(t)
	case Orderable:
		return r.orderable(t)
	case Numeric:
		return numeric(t)
	case JSON:
		return t.GetJson() != nil || t.GetJsonb() != nil
	}
	return false
}

func (r *resolver) comparable(t *irv1.Type) bool {
	t, _ = r.underlying(t, nil)
	switch k := t.GetKind().(type) {
	case nil, *irv1.Type_Json, *irv1.Type_Xml, *irv1.Type_Opaque:
		return false
	case *irv1.Type_Array:
		return r.comparable(k.Array.GetElement())
	case *irv1.Type_Range:
		return r.comparable(k.Range.GetElement())
	case *irv1.Type_Multirange:
		return r.comparable(k.Multirange.GetElement())
	case *irv1.Type_Named:
		if q := k.Named.GetComposite(); q != nil {
			c := r.ix.Composite(q)
			return c != nil && !slices.ContainsFunc(c.GetAttributes(), func(a *irv1.Column) bool { return !r.comparable(a.GetType()) })
		}
		return r.ix.Enum(k.Named.GetEnum()) != nil
	}
	return true
}

func (r *resolver) orderable(t *irv1.Type) bool {
	switch k := t.GetKind().(type) {
	case *irv1.Type_Named:
		return r.ix.Enum(k.Named.GetEnum()) != nil
	case *irv1.Type_Int8, *irv1.Type_Int16, *irv1.Type_Int32, *irv1.Type_Int64,
		*irv1.Type_Uint8, *irv1.Type_Uint16, *irv1.Type_Uint32, *irv1.Type_Uint64,
		*irv1.Type_Float32, *irv1.Type_Float64, *irv1.Type_Decimal, *irv1.Type_Money,
		*irv1.Type_Text, *irv1.Type_Varchar, *irv1.Type_Char, *irv1.Type_Bytes,
		*irv1.Type_Instant, *irv1.Type_InstantWithOffset, *irv1.Type_LocalDatetime, *irv1.Type_Date,
		*irv1.Type_LocalTime, *irv1.Type_TimeWithOffset, *irv1.Type_Interval,
		*irv1.Type_Uuid, *irv1.Type_Inet, *irv1.Type_Cidr, *irv1.Type_Macaddr:
		return true
	}
	return false
}

func numeric(t *irv1.Type) bool {
	switch t.GetKind().(type) {
	case *irv1.Type_Int8, *irv1.Type_Int16, *irv1.Type_Int32, *irv1.Type_Int64,
		*irv1.Type_Uint8, *irv1.Type_Uint16, *irv1.Type_Uint32, *irv1.Type_Uint64,
		*irv1.Type_Float32, *irv1.Type_Float64, *irv1.Type_Decimal:
		return true
	}
	return false
}

// textual reports whether t is text, varchar or char.
func textual(t *irv1.Type) bool {
	return t.GetText() != nil || t.GetVarchar() != nil || t.GetChar() != nil
}

// fidelity returns the fidelity s gives a column of type t and collation
// coll: unspecified when the dialect does not support the operator (§9.2).
func fidelity(s *irv1.OperatorSupport, t *irv1.Type, coll *irv1.Collation) irv1.Fidelity {
	if s.GetWhen() == nil || holds(s.GetWhen(), t, coll) {
		return s.GetFidelity()
	}
	return s.GetOtherwise()
}

// holds reports whether the column facts match the condition: every fact
// the condition sets is known and equal on the column.
func holds(c *irv1.OperatorCondition, t *irv1.Type, coll *irv1.Collation) bool {
	if c.GetScalar() && t.GetArray() != nil {
		return false
	}
	w := c.GetCollation()
	if w.GetName() != "" && w.GetName() != coll.GetName() {
		return false
	}
	for _, f := range [][2]irv1.Tristate{
		{w.GetCaseSensitive(), coll.GetCaseSensitive()},
		{w.GetAccentSensitive(), coll.GetAccentSensitive()},
		{w.GetPadSpace(), coll.GetPadSpace()},
		{w.GetDeterministic(), coll.GetDeterministic()},
	} {
		if f[0] != irv1.Tristate_TRISTATE_UNSPECIFIED && f[0] != f[1] {
			return false
		}
	}
	return true
}
