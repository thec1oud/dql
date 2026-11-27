package shapes

import (
	"strings"

	"github.com/thec1oud/dql/core/diagnostics"
	"github.com/thec1oud/dql/core/naming"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// CodeHostNameCollision: shapes, relation unions, union constructors, enums,
// domains and composite types all become names of the one generated package,
// and two of them have equal words (§4.3, §5.2).
const CodeHostNameCollision = "host-name-collision"

// checkHostNames fails the names of the shape layer that collide with each
// other or with the catalogs' host types (§4.3): collisions are decided on
// words, before any emitter casing, so every emitter sees the same result.
// Each diagnostic names the shape or union's entity, or the type itself.
func checkHostNames(catalogs []*irv1.Catalog, layer *irv1.ShapeLayer, n *Nester) []*irv1.Diagnostic {
	var items []naming.Item
	targets := map[string]*irv1.QualifiedName{}
	add := func(what string, target *irv1.QualifiedName, words []string) {
		items = append(items, naming.Item{Stored: what, Words: words})
		targets[what] = target
	}
	for _, c := range catalogs {
		for _, ns := range c.GetNamespaces() {
			q := func(object string) *irv1.QualifiedName {
				return &irv1.QualifiedName{Catalog: c.GetName().GetStored(), Namespace: ns.GetName().GetStored(), Object: object}
			}
			for _, e := range ns.GetEnums() {
				add("enum "+diagnostics.Name(q(e.GetName().GetStored())), q(e.GetName().GetStored()), e.GetHostName().GetWords())
			}
			for _, d := range ns.GetDomains() {
				add("domain "+diagnostics.Name(q(d.GetName().GetStored())), q(d.GetName().GetStored()), d.GetHostName().GetWords())
			}
			for _, co := range ns.GetComposites() {
				add("composite type "+diagnostics.Name(q(co.GetName().GetStored())), q(co.GetName().GetStored()), co.GetHostName().GetWords())
			}
		}
	}
	for _, s := range layer.GetShapes() {
		add("shape "+n.spell(s.GetId()), s.GetEntity(), s.GetHostName().GetWords())
	}
	for _, u := range layer.GetUnions() {
		name := strings.Join(u.GetHostName().GetWords(), " ")
		add("relation union "+name, u.GetId().GetEntity(), u.GetHostName().GetWords())
		for _, v := range u.GetVariants() {
			add("constructor "+strings.Join(v.GetHostName().GetWords(), " ")+" of relation union "+name, u.GetId().GetEntity(), v.GetHostName().GetWords())
		}
	}
	var ds []*irv1.Diagnostic
	for _, col := range naming.Collisions(items) {
		for i, what := range col.Stored {
			others := append(append([]string{}, col.Stored[:i]...), col.Stored[i+1:]...)
			ds = append(ds, diagnostics.Errorf(CodeHostNameCollision, targets[what],
				"%s gets the host name [%s], the same as %s, and both generate into one package (§4.3); rename one of them",
				what, strings.Join(col.Words, " "), strings.Join(others, " and ")))
		}
	}
	return ds
}
