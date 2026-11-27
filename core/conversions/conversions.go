// Package conversions computes the conversion table of the shape layer and
// the merge-by-key kind of every keyed entity (design record §14.9, §8.9).
//
// It reads the fully expanded shapes the shape layer already holds and the
// catalog facts behind them. It never re-derives a shape: a conversion's kind
// follows from the two shapes' fields alone, so every emitter generates the
// same functions.
package conversions

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/thec1oud/dql/core/diagnostics"
	"github.com/thec1oud/dql/core/types"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Declared is one entry of conversions: in dql.yaml (§14.9).
type Declared struct {
	From, To *irv1.ShapeId
}

// Diagnostic codes. They are stable: tools and tests match on them.
const (
	// A declared conversion names a shape the shape layer does not have.
	CodeConversionShapeUnknown = "conversion-shape-unknown"
	// A declared conversion is Impossible: information is missing.
	CodeConversionImpossible = "conversion-impossible"
)

// Derive fills b.Shapes.Conversions and b.Shapes.Merges and returns the
// diagnostics, sorted (§3.2). Any error diagnostic fails generation.
//
// The conversion table holds the conversions between each entity's presets
// and forms, those between every custom shape and each shape along its
// lineage, in both directions, and the declared ones, sorted by the dotted
// spelling of from, then of to. A pair reached in more than one way keeps
// the first origin of preset, lineage, declared. Merges hold one entry per
// keyed table, sorted by qualified name.
func Derive(b *irv1.Bundle, declared []Declared) []*irv1.Diagnostic {
	if b.Shapes == nil {
		b.Shapes = &irv1.ShapeLayer{}
	}
	d := &deriver{
		ix:     types.NewIndex(b),
		tables: map[string]*irv1.Table{},
		shapes: map[string]*irv1.Shape{},
		pairs:  map[string]bool{},
	}
	d.index(b)
	d.presets(b.Shapes.GetShapes())
	for _, s := range b.Shapes.GetShapes() {
		if s.GetId().GetCustom() != "" {
			d.lineage(s)
		}
	}
	for _, c := range declared {
		d.declare(c)
	}
	slices.SortFunc(d.table, func(x, y *irv1.Conversion) int {
		return cmp.Or(strings.Compare(d.dotted(x.GetFrom()), d.dotted(y.GetFrom())),
			strings.Compare(d.dotted(x.GetTo()), d.dotted(y.GetTo())))
	})
	b.Shapes.Conversions = d.table
	b.Shapes.Merges = d.merges()
	diagnostics.Sort(d.diags)
	return d.diags
}

// deriver holds the state of one Derive call.
type deriver struct {
	ix     *types.Index
	order  []*irv1.QualifiedName  // every table, in qualified-name order
	tables map[string]*irv1.Table // by qnKey
	shapes map[string]*irv1.Shape // by dotted spelling
	pairs  map[string]bool        // conversions in the table, by pairKey
	table  []*irv1.Conversion
	diags  []*irv1.Diagnostic
}

func (d *deriver) index(b *irv1.Bundle) {
	for _, c := range b.GetCatalogs() {
		for _, ns := range c.GetNamespaces() {
			for _, t := range ns.GetTables() {
				qn := &irv1.QualifiedName{Catalog: c.GetName().GetStored(), Namespace: ns.GetName().GetStored(), Object: t.GetName().GetStored()}
				d.tables[qnKey(qn)] = t
				d.order = append(d.order, qn)
			}
		}
	}
	slices.SortFunc(d.order, diagnostics.CompareNames)
	for _, s := range b.GetShapes().GetShapes() {
		d.shapes[d.dotted(s.GetId())] = s
	}
}

// presetForm picks one of an entity's preset shapes.
type presetForm struct {
	preset irv1.Preset
	form   irv1.Form
}

var (
	fullFlat    = presetForm{irv1.Preset_PRESET_FULL, irv1.Form_FORM_FLAT}
	draftFlat   = presetForm{irv1.Preset_PRESET_DRAFT, irv1.Form_FORM_FLAT}
	partialFlat = presetForm{irv1.Preset_PRESET_PARTIAL, irv1.Form_FORM_FLAT}
	key         = presetForm{irv1.Preset_PRESET_KEY, irv1.Form_FORM_UNSPECIFIED}
	presetTiers = []irv1.Preset{irv1.Preset_PRESET_FULL, irv1.Preset_PRESET_DRAFT, irv1.Preset_PRESET_PARTIAL}
	// The conversions between Flat presets of §14.9, as from and to.
	presetsTable = [][2]presetForm{
		{fullFlat, key},
		{fullFlat, partialFlat},
		{fullFlat, draftFlat},
		{draftFlat, partialFlat},
		{partialFlat, fullFlat},
		{partialFlat, draftFlat},
		{draftFlat, fullFlat},
	}
)

// presets adds the conversions between each entity's presets and forms: the
// pairs of §14.9, and each tier's Nested form to its Flat and Nests forms
// (§14.4). Relation-qualified child shapes (§14.5) take part in none.
func (d *deriver) presets(shapes []*irv1.Shape) {
	byEntity := map[string]map[presetForm]*irv1.Shape{}
	var entities []*irv1.QualifiedName
	for _, s := range shapes {
		id := s.GetId()
		if id.GetEntity() == nil || id.GetForRelation() != "" {
			continue
		}
		k := qnKey(id.GetEntity())
		if byEntity[k] == nil {
			byEntity[k] = map[presetForm]*irv1.Shape{}
			entities = append(entities, id.GetEntity())
		}
		byEntity[k][presetForm{id.GetPreset(), id.GetForm()}] = s
	}
	for _, e := range entities {
		have := byEntity[qnKey(e)]
		pairs := slices.Clone(presetsTable)
		for _, p := range presetTiers {
			nested := presetForm{p, irv1.Form_FORM_NESTED}
			pairs = append(pairs,
				[2]presetForm{nested, {p, irv1.Form_FORM_FLAT}},
				[2]presetForm{nested, {p, irv1.Form_FORM_NESTS}})
		}
		for _, p := range pairs {
			if from, to := have[p[0]], have[p[1]]; from != nil && to != nil {
				d.add(from, to, irv1.ConversionOrigin_CONVERSION_ORIGIN_PRESET)
			}
		}
	}
}

// lineage adds the conversions between a custom shape and every shape along
// its lineage: the preset at its root and each custom shape it derives from
// (§14.8, §14.9), in both directions.
func (d *deriver) lineage(s *irv1.Shape) {
	for _, id := range ancestors(s) {
		if a := d.shapes[d.dotted(id)]; a != nil {
			d.add(a, s, irv1.ConversionOrigin_CONVERSION_ORIGIN_LINEAGE)
			d.add(s, a, irv1.ConversionOrigin_CONVERSION_ORIGIN_LINEAGE)
		}
	}
}

// ancestors returns the shapes a custom shape derives from, root first: the
// preset of its PRESET step, qualified by a FOR_RELATION step, then the shape
// of each FROM step (§14.10).
func ancestors(s *irv1.Shape) []*irv1.ShapeId {
	lineage := s.GetLineage()
	if len(lineage) == 0 || lineage[0].GetOp() != irv1.ShapeOp_PRESET {
		return nil
	}
	root := &irv1.ShapeId{
		Base:   &irv1.ShapeId_Entity{Entity: lineage[0].GetEntity()},
		Preset: lineage[0].GetPreset(),
		Form:   lineage[0].GetForm(),
	}
	out := []*irv1.ShapeId{root}
	for _, st := range lineage[1:] {
		switch st.GetOp() {
		case irv1.ShapeOp_FOR_RELATION:
			root.ForRelation = st.GetRelation()
		case irv1.ShapeOp_FROM:
			out = append(out, st.GetShape())
		}
	}
	return out
}

// declare adds a conversion declared in dql.yaml. Declaring one that names
// an unknown shape or is Impossible is an error.
func (d *deriver) declare(c Declared) {
	from, to := d.shapes[d.dotted(c.From)], d.shapes[d.dotted(c.To)]
	for _, u := range []struct {
		id    *irv1.ShapeId
		found bool
	}{{c.From, from != nil}, {c.To, to != nil}} {
		if !u.found {
			d.errorf(CodeConversionShapeUnknown, u.id,
				"conversion from %s to %s names shape %s, which does not exist; name a preset such as user.full or a shape declared under shapes:",
				d.dotted(c.From), d.dotted(c.To), d.dotted(u.id))
		}
	}
	if from == nil || to == nil {
		return
	}
	if r := classify(d, from, to); r.kind == irv1.ConversionKind_CONVERSION_IMPOSSIBLE {
		d.errorf(CodeConversionImpossible, c.From,
			"conversion from %s to %s is impossible: %s cannot supply %s; remove it from conversions:",
			d.dotted(c.From), d.dotted(c.To), d.dotted(c.From), strings.Join(r.missing, ", "))
		return
	}
	d.add(from, to, irv1.ConversionOrigin_CONVERSION_ORIGIN_DECLARED)
}

// add classifies the conversion from a to b and puts it in the table, unless
// the table already holds it.
func (d *deriver) add(a, b *irv1.Shape, origin irv1.ConversionOrigin) {
	k := d.dotted(a.GetId()) + "\x00" + d.dotted(b.GetId())
	if d.pairs[k] {
		return
	}
	d.pairs[k] = true
	r := classify(d, a, b)
	d.table = append(d.table, &irv1.Conversion{From: a.GetId(), To: b.GetId(), Kind: r.kind, Inputs: r.inputs, Origin: origin})
}

func (d *deriver) errorf(code string, id *irv1.ShapeId, format string, args ...any) {
	diag := diagnostics.Errorf(code, id.GetEntity(), format, args...)
	diag.Shape = d.dotted(id)
	d.diags = append(d.diags, diag)
}

// dotted spells a shape id as dql.yaml writes it (§14.1): a custom shape's
// name, or the entity's host-name words joined with "_", the preset and the
// form, then ".for." and the relation for a relation-qualified shape.
func (d *deriver) dotted(id *irv1.ShapeId) string {
	if c := id.GetCustom(); c != "" {
		return c
	}
	parts := []string{d.entityWords(id.GetEntity()), lower(id.GetPreset().String(), "PRESET_")}
	if f := id.GetForm(); f != irv1.Form_FORM_UNSPECIFIED {
		parts = append(parts, lower(f.String(), "FORM_"))
	}
	if r := id.GetForRelation(); r != "" {
		parts = append(parts, "for", r)
	}
	return strings.Join(parts, ".")
}

// entityWords joins an entity's host-name words with "_", or gives its
// stored name when the catalog holds no host name for it.
func (d *deriver) entityWords(qn *irv1.QualifiedName) string {
	if w := d.tables[qnKey(qn)].GetHostName().GetWords(); len(w) > 0 {
		return strings.Join(w, "_")
	}
	return qn.GetObject()
}

// lower turns an enum value name into its dotted spelling: PRESET_FULL is
// full. Enum names are ASCII, so this needs no locale (§3.2).
func lower(name, prefix string) string {
	return strings.ToLower(strings.TrimPrefix(name, prefix))
}

func qnKey(qn *irv1.QualifiedName) string {
	return fmt.Sprintf("%s\x00%s\x00%s", qn.GetCatalog(), qn.GetNamespace(), qn.GetObject())
}
