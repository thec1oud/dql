package types

import (
	"slices"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Optional IR capabilities a bundle may require (§3.3).
const (
	FeatureCrossCatalog = "cross-catalog"
	FeatureOpaqueTypes  = "opaque-types"
	FeatureWeakTypes    = "weak-types"
)

// RequiredFeatures returns the optional IR capabilities the catalogs of b
// actually use, sorted (§3.3):
//
//   - opaque-types when an opaque type appears anywhere, nested types included;
//   - weak-types when a weak type appears anywhere;
//   - cross-catalog when a reference crosses catalogs (§5.2): a relation, a
//     foreign key, a view dependency or a named type reference.
//
// Features of the shape and query layers, such as nested-writes, are not
// computed here.
func RequiredFeatures(b *irv1.Bundle) []string {
	used := map[string]bool{}
	sites(b, func(s site) {
		walk(s.typ, func(t *irv1.Type) {
			switch t.GetKind().(type) {
			case *irv1.Type_Opaque:
				used[FeatureOpaqueTypes] = true
			case *irv1.Type_Weak:
				used[FeatureWeakTypes] = true
			case *irv1.Type_Named:
				if crosses(s.catalog, namedName(t.GetNamed())) {
					used[FeatureCrossCatalog] = true
				}
			}
		})
	})
	if crossCatalogRefs(b) {
		used[FeatureCrossCatalog] = true
	}
	var out []string
	for f := range used {
		out = append(out, f)
	}
	slices.Sort(out)
	return out
}

// crosses reports whether q names an object outside catalog cat. A name with
// no catalog is local to its owner.
func crosses(cat string, q *irv1.QualifiedName) bool {
	return q.GetCatalog() != "" && q.GetCatalog() != cat
}

// crossCatalogRefs reports whether a relation, foreign key or view
// dependency of b crosses catalogs.
func crossCatalogRefs(b *irv1.Bundle) bool {
	for _, c := range b.GetCatalogs() {
		cat := c.GetName().GetStored()
		for _, ns := range c.GetNamespaces() {
			for _, t := range ns.GetTables() {
				for _, r := range t.GetRelations() {
					if r.GetCrossCatalog() || slices.ContainsFunc(
						[]*irv1.QualifiedName{r.GetOwner(), r.GetVia(), r.GetFrom().GetTable(), r.GetTo().GetTable(), r.GetThrough()},
						func(q *irv1.QualifiedName) bool { return crosses(cat, q) }) {
						return true
					}
				}
				for _, fk := range t.GetForeignKeys() {
					if crosses(cat, fk.GetReferences().GetTable()) {
						return true
					}
				}
				for _, dep := range t.GetDependsOn() {
					if crosses(cat, dep) {
						return true
					}
				}
			}
		}
	}
	return false
}
