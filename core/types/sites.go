package types

import (
	"fmt"
	"strings"

	"github.com/thec1oud/dql/core/diagnostics"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// site is a place in a catalog that holds a type: a column, a composite
// attribute, a domain's base, a sequence, a routine argument or result.
type site struct {
	catalog string
	target  *irv1.QualifiedName
	// what describes the site for messages: "column shop.app.users.email".
	what string
	typ  *irv1.Type
	// column is set for table, view, composite and result-table columns.
	column bool
}

// sites calls fn for every typed place in b, in catalog order.
func sites(b *irv1.Bundle, fn func(site)) {
	for _, c := range b.GetCatalogs() {
		cat := c.GetName().GetStored()
		for _, ns := range c.GetNamespaces() {
			nsName := ns.GetName().GetStored()
			obj := func(name string) *irv1.QualifiedName {
				return &irv1.QualifiedName{Catalog: cat, Namespace: nsName, Object: name}
			}
			member := func(name, m string) *irv1.QualifiedName {
				q := obj(name)
				q.Member = m
				return q
			}
			for _, t := range ns.GetTables() {
				for _, col := range t.GetColumns() {
					q := member(t.GetName().GetStored(), col.GetName().GetStored())
					fn(site{cat, q, "column " + diagnostics.Name(q), col.GetType(), true})
				}
			}
			for _, co := range ns.GetComposites() {
				for _, a := range co.GetAttributes() {
					q := member(co.GetName().GetStored(), a.GetName().GetStored())
					fn(site{cat, q, "attribute " + diagnostics.Name(q), a.GetType(), true})
				}
			}
			for _, d := range ns.GetDomains() {
				q := obj(d.GetName().GetStored())
				fn(site{cat, q, "domain " + diagnostics.Name(q), d.GetBase(), false})
			}
			for _, s := range ns.GetSequences() {
				q := obj(s.GetName().GetStored())
				fn(site{cat, q, "sequence " + diagnostics.Name(q), s.GetType(), false})
			}
			for _, r := range ns.GetRoutines() {
				name := r.GetName().GetStored()
				for _, o := range r.GetOverloads() {
					sig := fmt.Sprintf("%s(%s)", diagnostics.Name(obj(name)), strings.Join(o.GetSignature(), ", "))
					for i, a := range o.GetArguments() {
						q := member(name, a.GetName().GetStored())
						fn(site{cat, q, fmt.Sprintf("argument %d of %s", i+1, sig), a.GetType(), false})
					}
					ret := o.GetReturns()
					switch {
					case ret.GetScalar() != nil:
						fn(site{cat, obj(name), "the result of " + sig, ret.GetScalar().GetType(), false})
					case ret.GetSetOf() != nil:
						fn(site{cat, obj(name), "the result of " + sig, ret.GetSetOf().GetType(), false})
					}
					for _, col := range ret.GetTable().GetColumns() {
						q := member(name, col.GetName().GetStored())
						fn(site{cat, q, fmt.Sprintf("result column %s of %s", col.GetName().GetStored(), sig), col.GetType(), true})
					}
				}
			}
		}
	}
}

// walk calls fn for t and every type nested in it: array and range elements
// and weak affinities. It does not follow named references. A missing type
// reaches fn as nil, which every getter of irv1.Type accepts.
func walk(t *irv1.Type, fn func(*irv1.Type)) {
	fn(t)
	switch k := t.GetKind().(type) {
	case *irv1.Type_Array:
		walk(k.Array.GetElement(), fn)
	case *irv1.Type_Range:
		walk(k.Range.GetElement(), fn)
	case *irv1.Type_Multirange:
		walk(k.Multirange.GetElement(), fn)
	case *irv1.Type_Weak:
		walk(k.Weak.GetAffinity(), fn)
	}
}
