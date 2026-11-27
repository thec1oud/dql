package shapes

import (
	"fmt"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Forms derives the Nests and Nested forms (§14.4) of every flat Full, Draft
// and Partial preset in flat, with the relation-qualified child shapes and
// the relation unions their nests refer to (§14.5, §14.6).
//
// flat holds the flat presets of every entity in catalogs, with their Keys.
// Shapes come back sorted by their dotted spelling and unions by their host
// words (§3.2). Forms fails only on input it cannot have come from: a flat
// shape whose entity is not in catalogs, or a relation to an entity without
// a flat Full.
func Forms(catalogs []*irv1.Catalog, flat []*irv1.Shape) ([]*irv1.Shape, []*irv1.RelationUnion, error) {
	n := NewNester(catalogs, flat)
	out := newFormSet(n)
	for _, s := range flat {
		id := s.GetId()
		if id.GetEntity() == nil || id.GetForm() != irv1.Form_FORM_FLAT || id.GetForRelation() != "" {
			continue
		}
		t := n.tables[formsEntityKey(id.GetEntity())]
		if t == nil {
			return nil, nil, fmt.Errorf("shapes: flat shape %s names an entity that is not in the catalogs", n.spell(id))
		}
		var err error
		if id.GetPreset() == irv1.Preset_PRESET_FULL {
			err = out.read(s, t)
		} else {
			out.write(s, t)
		}
		if err != nil {
			return nil, nil, err
		}
	}
	shapes, unions := out.result()
	return shapes, unions, nil
}

// formSet collects derived shapes and unions, each once.
type formSet struct {
	n      *Nester
	shapes map[string]*irv1.Shape
	unions map[string]*irv1.RelationUnion
}

func newFormSet(n *Nester) *formSet {
	return &formSet{n: n, shapes: map[string]*irv1.Shape{}, unions: map[string]*irv1.RelationUnion{}}
}

func (f *formSet) addShape(s *irv1.Shape) {
	k := f.n.spell(s.GetId())
	if _, ok := f.shapes[k]; !ok {
		f.shapes[k] = s
	}
}

func (f *formSet) addUnion(u *irv1.RelationUnion) {
	k := unionKey(u.GetId())
	if _, ok := f.unions[k]; !ok {
		f.unions[k] = u
	}
}

func (f *formSet) result() ([]*irv1.Shape, []*irv1.RelationUnion) {
	keys := make([]string, 0, len(f.shapes))
	for k := range f.shapes {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	shapes := make([]*irv1.Shape, 0, len(keys))
	for _, k := range keys {
		shapes = append(shapes, f.shapes[k])
	}
	unions := make([]*irv1.RelationUnion, 0, len(f.unions))
	for _, u := range f.unions {
		unions = append(unions, u)
	}
	slices.SortFunc(unions, func(a, b *irv1.RelationUnion) int {
		return slices.Compare(a.GetHostName().GetWords(), b.GetHostName().GetWords())
	})
	return shapes, unions
}

// read derives Full Nests and Full Nested. Every direct relation is a field
// holding the related entity's flat Full, one level deep, always loaded, so
// required; a to-one relation is nullable when it may be absent (§12.3,
// §12.5, §14.4).
func (f *formSet) read(flat *irv1.Shape, t *irv1.Table) error {
	nested := formsDerive(flat, irv1.Form_FORM_NESTED)
	for _, r := range t.GetRelations() {
		target := f.n.preset(r.GetTo().GetTable(), irv1.Preset_PRESET_FULL, irv1.Form_FORM_FLAT)
		if target == nil {
			return fmt.Errorf("shapes: relation %s of %s targets an entity without a flat Full",
				r.GetName().GetStored(), f.n.spell(flat.GetId()))
		}
		nested.Nests = append(nested.Nests, &irv1.Nest{
			Relation: r.GetName().GetStored(),
			Presence: irv1.Presence_PRESENCE_REQUIRED,
			Nullable: !toMany(r) && r.GetOptional(),
			Target:   &irv1.Nest_Shape{Shape: proto.Clone(target.GetId()).(*irv1.ShapeId)},
		})
	}
	// Relation fields are sorted by relation, as in write shapes, whatever
	// order the table lists its relations in (§3.2).
	slices.SortFunc(nested.Nests, func(a, b *irv1.Nest) int { return strings.Compare(a.GetRelation(), b.GetRelation()) })
	f.addShape(nested)
	f.addShape(nestsOf(flat, nested))
	return nil
}

// write derives Draft or Partial Nests and Nested through the Nester, with
// each relation taking the related entity's flat preset of the same tier.
func (f *formSet) write(flat *irv1.Shape, t *irv1.Table) {
	nested := formsDerive(flat, irv1.Form_FORM_NESTED)
	preset := flat.GetId().GetPreset()
	for _, r := range t.GetRelations() {
		target := f.n.preset(r.GetTo().GetTable(), preset, irv1.Form_FORM_FLAT)
		added, unions, _ := f.n.NestWrite(nested, r, target)
		for _, s := range added {
			f.addShape(s)
		}
		for _, u := range unions {
			f.addUnion(u)
		}
	}
	f.addShape(nested)
	f.addShape(nestsOf(flat, nested))
}

// formsDerive starts a form of flat: its fields, forbidden columns and scope,
// under the form's id, host words and lineage.
func formsDerive(flat *irv1.Shape, form irv1.Form) *irv1.Shape {
	s := proto.Clone(flat).(*irv1.Shape)
	s.Id.Form = form
	s.HostName = &irv1.Identifier{Words: append(slices.Clone(flat.GetHostName().GetWords()), formName(form))}
	s.Lineage = []*irv1.LineageStep{{
		Op: irv1.ShapeOp_PRESET, Entity: proto.Clone(flat.GetEntity()).(*irv1.QualifiedName),
		Preset: flat.GetId().GetPreset(), Form: form,
	}}
	return s
}

// nestsOf is the Nests form of flat: only the relations of its Nested form,
// and the relations that leaves out (§14.4). Nests holds no columns, so no
// column supplies a relation in it.
func nestsOf(flat, nested *irv1.Shape) *irv1.Shape {
	s := formsDerive(flat, irv1.Form_FORM_NESTS)
	s.Fields, s.Scope, s.Bind, s.Forbidden = nil, nil, nil, nil
	for _, x := range nested.GetForbidden() {
		if x.GetRelation() != "" {
			s.Forbidden = append(s.Forbidden, proto.Clone(x).(*irv1.ForbiddenField))
		}
	}
	for _, x := range nested.GetNests() {
		x = proto.Clone(x).(*irv1.Nest)
		x.Supplies, x.NeedsOne = nil, false
		s.Nests = append(s.Nests, x)
	}
	return s
}

func formName(f irv1.Form) string {
	return strings.ToLower(strings.TrimPrefix(f.String(), "FORM_"))
}

func presetName(p irv1.Preset) string {
	return strings.ToLower(strings.TrimPrefix(p.String(), "PRESET_"))
}

// formsEntityKey is a map key for an entity's qualified name. NUL cannot occur
// in an identifier the IR carries, so the key is unambiguous.
func formsEntityKey(qn *irv1.QualifiedName) string {
	return qn.GetCatalog() + "\x00" + qn.GetNamespace() + "\x00" + qn.GetObject()
}

func unionKey(id *irv1.RelationUnionId) string {
	return fmt.Sprintf("%d\x00%s\x00%s\x00%s", id.GetKind(), formsEntityKey(id.GetEntity()), id.GetRelation(), id.GetCustom())
}

func toMany(r *irv1.Relation) bool {
	k := r.GetKind()
	return k == irv1.RelationKind_RELATION_KIND_ONE_TO_MANY || k == irv1.RelationKind_RELATION_KIND_MANY_TO_MANY
}
