package shapes

import (
	"slices"

	"github.com/thec1oud/dql/core/diagnostics"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Layer builds the shape layer of catalogs (§14): every entity's presets in
// every form with the relation-qualified child shapes and the unions their
// nests refer to (§14.3 to §14.6), the custom shapes of customs (§14.8) and
// the upserts (§14.7). The conversion table and the merges are left to
// core/conversions, which reads the shapes this returns.
//
// Shapes are sorted by their dotted spelling and unions by their host-name
// words (§3.2); a shape or union reached both through the forms and through
// a custom shape's nest is listed once. The diagnostics are those of the
// custom shapes and the host-name collisions of the layer, sorted. The error is Forms', for catalogs no assembled
// bundle has.
func Layer(catalogs []*irv1.Catalog, customs []Custom) (*irv1.ShapeLayer, []*irv1.Diagnostic, error) {
	flat := Presets(catalogs)
	forms, unions, err := Forms(catalogs, flat)
	if err != nil {
		return nil, nil, err
	}
	presets := append(slices.Clone(flat), forms...)
	n := NewNester(catalogs, flat)
	custom, customUnions, diags := DeriveCustom(customs, presets, catalogs, n)
	set := newFormSet(n)
	for _, s := range append(presets, custom...) {
		set.addShape(s)
	}
	for _, u := range append(unions, customUnions...) {
		set.addUnion(u)
	}
	shapes, unions := set.result()
	layer := &irv1.ShapeLayer{Shapes: shapes, Unions: unions, Upserts: Upserts(catalogs)}
	diags = append(diags, checkHostNames(catalogs, layer, n)...)
	diagnostics.Sort(diags)
	return layer, diags, nil
}

// Ref returns the id of the shape of layer that ref names as dql.yaml writes
// it: a custom shape's name, or a preset's dotted spelling, where a Full,
// Draft or Partial written without a form is its Flat form (§14.8). A ref
// that names no shape of layer comes back as a custom id holding ref as
// written, which names no shape either, so whoever looks it up reports it
// in the user's own spelling.
func Ref(catalogs []*irv1.Catalog, layer *irv1.ShapeLayer, ref string) *irv1.ShapeId {
	n := NewNester(catalogs, nil)
	spelling := ref
	if s, ok := customParse(ref); ok {
		spelling = s
	}
	for _, s := range layer.GetShapes() {
		if n.spell(s.GetId()) == spelling {
			return s.GetId()
		}
	}
	return &irv1.ShapeId{Base: &irv1.ShapeId_Custom{Custom: ref}}
}
