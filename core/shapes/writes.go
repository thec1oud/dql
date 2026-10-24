package shapes

import (
	"cmp"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Nester adds relation fields to write shapes: the references, links and
// child shapes of nested Drafts (§14.5) and the changes of nested Partials
// (§14.6). Presets and custom shapes that nest (§14.8) share it, so both
// follow one set of rules.
type Nester struct {
	tables map[string]*irv1.Table
	shapes map[string]*irv1.Shape
}

// NewNester indexes the entities of catalogs and the shapes the nests refer
// to: every entity's flat presets and its Key.
func NewNester(catalogs []*irv1.Catalog, shapes []*irv1.Shape) *Nester {
	n := &Nester{tables: map[string]*irv1.Table{}, shapes: map[string]*irv1.Shape{}}
	for _, c := range catalogs {
		for _, ns := range c.GetNamespaces() {
			for _, t := range ns.GetTables() {
				qn := &irv1.QualifiedName{Catalog: c.GetName().GetStored(), Namespace: ns.GetName().GetStored(), Object: t.GetName().GetStored()}
				n.tables[formsEntityKey(qn)] = t
			}
		}
	}
	for _, s := range shapes {
		n.shapes[n.spell(s.GetId())] = s
	}
	return n
}

// NestWrite adds relation r, owned by the entity of shape, to shape, a Draft
// or Partial in any form or a custom shape derived from one. target is the
// shape the related rows take in the same tier: the related entity's flat
// preset, a custom shape, or nil when the related entity has none (a view).
//
// It replaces any nest shape already has for r, or lists r as forbidden when
// the scope supplies its foreign key, and in a Draft makes the foreign-key
// columns r can supply optional (§14.5). It returns the relation-qualified
// child shapes it derived from target, and the unions the nest refers to. A
// relation none of whose changes can be written, such as one to a view
// without a Key, is listed as forbidden with FORBIDDEN_NOT_WRITABLE.
//
// The diagnostic is reserved for the write rule of custom shapes (§14.8) and
// is nil.
func (n *Nester) NestWrite(shape *irv1.Shape, r *irv1.Relation, target *irv1.Shape) ([]*irv1.Shape, []*irv1.RelationUnion, *irv1.Diagnostic) {
	w := &nestWrite{n: n, shape: shape, rel: r, target: target, tier: tierOf(shape)}
	shape.Nests = slices.DeleteFunc(shape.Nests, func(x *irv1.Nest) bool { return x.GetRelation() == r.GetName().GetStored() })
	shape.Forbidden = slices.DeleteFunc(shape.Forbidden, func(x *irv1.ForbiddenField) bool {
		return x.GetRelation() == r.GetName().GetStored()
	})
	var nest *irv1.Nest
	switch r.GetKind() {
	case irv1.RelationKind_RELATION_KIND_MANY_TO_ONE, irv1.RelationKind_RELATION_KIND_ONE_TO_ONE:
		nest = w.owning()
	case irv1.RelationKind_RELATION_KIND_MANY_TO_MANY:
		nest = w.manyToMany()
	default:
		nest = w.dependent()
	}
	if nest == nil && !w.forbidden {
		w.forbid(irv1.ForbiddenReason_FORBIDDEN_NOT_WRITABLE)
	}
	if nest != nil {
		nest.Relation = r.GetName().GetStored()
		shape.Nests = append(shape.Nests, nest)
		slices.SortFunc(shape.Nests, func(a, b *irv1.Nest) int { return strings.Compare(a.GetRelation(), b.GetRelation()) })
	}
	return w.added, w.unions, nil
}

// nestWrite is one NestWrite call.
type nestWrite struct {
	n      *Nester
	shape  *irv1.Shape
	rel    *irv1.Relation
	target *irv1.Shape
	tier   irv1.Preset
	added  []*irv1.Shape
	unions []*irv1.RelationUnion
	// forbidden records that the relation was listed as forbidden.
	forbidden bool
}

// owning is a relation this entity's foreign key points along. In a Draft it
// is a reference, Link or Create; in a Partial it takes one change (§14.5,
// §14.6). Its foreign-key columns and the relation coexist and follow the
// consistency rule.
func (w *nestWrite) owning() *irv1.Nest {
	fk := w.rel.GetFrom().GetColumns()
	base := w.n.preset(w.shape.GetEntity(), w.tier, irv1.Form_FORM_FLAT)
	reason := forbiddenReason(base, fk)
	if reason == irv1.ForbiddenReason_FORBIDDEN_BOUND {
		w.forbid(reason)
		return nil
	}
	if w.tier == irv1.Preset_PRESET_PARTIAL {
		return w.owningChange(fk, reason == irv1.ForbiddenReason_FORBIDDEN_UNSPECIFIED)
	}
	if reason != irv1.ForbiddenReason_FORBIDDEN_UNSPECIFIED {
		// The reference would write a column the Draft cannot hold.
		w.forbid(reason)
		return nil
	}
	u := w.union(irv1.RelationUnionKind_RELATION_UNION_REF, w.rel.GetTo().GetTable(), "",
		w.variant(irv1.RelationChange_RELATION_CHANGE_LINK, true, w.keyOf(w.rel.GetTo().GetTable()), nil, "ref", "link"),
		w.variant(irv1.RelationChange_RELATION_CHANGE_CREATE, false, nil, w.payload(irv1.Preset_PRESET_DRAFT), "ref", "create"),
	)
	if u == nil {
		return nil
	}
	nest := &irv1.Nest{Presence: irv1.Presence_PRESENCE_OPTIONAL, Target: &irv1.Nest_Union{Union: u}}
	// A NOT NULL foreign key needs the column or the relation. When the
	// shape holds neither column, the relation must be given; a Nests form
	// holds no columns but always joins a flat Draft that does.
	required := slices.ContainsFunc(fk, func(c string) bool {
		f := fieldOf(base, c)
		return f != nil && f.GetPresence() == irv1.Presence_PRESENCE_REQUIRED
	})
	nest.Supplies = w.supplies(fk)
	switch {
	case len(nest.Supplies) > 0:
		nest.NeedsOne = required
	case required && w.shape.GetId().GetForm() != irv1.Form_FORM_NESTS:
		nest.Presence = irv1.Presence_PRESENCE_REQUIRED
	}
	// In a nested Draft either the column or the relation supplies the value.
	for _, c := range nest.Supplies {
		fieldOf(w.shape, c).Presence = irv1.Presence_PRESENCE_OPTIONAL
	}
	return nest
}

// dependent is a relation whose rows reference this entity: a one-to-many
// or inverse one-to-one relation. Its rows take the child's shape for the
// forward relation, which forbids the foreign key the parent supplies
// (§14.5). In a Partial each element is a change (§14.6).
func (w *nestWrite) dependent() *irv1.Nest {
	child := w.rel.GetTo().GetTable()
	fk := w.rel.GetTo().GetColumns()
	if forbiddenReason(w.n.preset(child, irv1.Preset_PRESET_DRAFT, irv1.Form_FORM_FLAT), fk) == irv1.ForbiddenReason_FORBIDDEN_BOUND {
		// The scope supplies the child's foreign key, so the parent cannot.
		w.forbid(irv1.ForbiddenReason_FORBIDDEN_BOUND)
		return nil
	}
	if w.tier == irv1.Preset_PRESET_PARTIAL {
		return w.childChange()
	}
	s := w.forShape(w.payload(irv1.Preset_PRESET_DRAFT))
	if s == nil {
		return nil
	}
	return &irv1.Nest{Presence: irv1.Presence_PRESENCE_OPTIONAL, Target: &irv1.Nest_Shape{Shape: s}}
}

// manyToMany is a relation through a junction. In a Draft each element links
// an existing far row or creates one; both write the junction row (§14.5). In
// a Partial it takes Link, Unlink or Create under its policy (§14.6).
func (w *nestWrite) manyToMany() *irv1.Nest {
	if w.tier == irv1.Preset_PRESET_PARTIAL {
		return w.manyToManyChange()
	}
	far := w.rel.GetTo().GetTable()
	u := w.union(irv1.RelationUnionKind_RELATION_UNION_LINK, far, "",
		w.variant(irv1.RelationChange_RELATION_CHANGE_LINK, true, w.keyOf(far), nil, "link", "key"),
		w.variant(irv1.RelationChange_RELATION_CHANGE_CREATE, false, nil, w.payload(irv1.Preset_PRESET_DRAFT), "link", "create"),
	)
	if u == nil {
		return nil
	}
	return &irv1.Nest{Presence: irv1.Presence_PRESENCE_OPTIONAL, Target: &irv1.Nest_Union{Union: u}}
}

func (w *nestWrite) forbid(reason irv1.ForbiddenReason) {
	w.forbidden = true
	w.shape.Forbidden = append(w.shape.Forbidden, &irv1.ForbiddenField{Relation: w.rel.GetName().GetStored(), Reason: reason})
	sortForbidden(w.shape, w.n.tables[formsEntityKey(w.shape.GetEntity())])
}

// supplies lists the foreign-key columns that are fields of the shape, in
// key order.
func (w *nestWrite) supplies(fk []string) []string {
	var out []string
	for _, c := range fk {
		if fieldOf(w.shape, c) != nil {
			out = append(out, c)
		}
	}
	return out
}

// payload is the shape a Create or Update writes the related rows with: the
// target when it is of that tier, otherwise the related entity's flat preset
// of that tier, or nil when there is none.
func (w *nestWrite) payload(tier irv1.Preset) *irv1.Shape {
	if w.target != nil && tierOf(w.target) == tier {
		return w.target
	}
	return w.n.preset(w.rel.GetTo().GetTable(), tier, irv1.Form_FORM_FLAT)
}

// keyOf is the Key preset of entity, or nil when it has none.
func (w *nestWrite) keyOf(entity *irv1.QualifiedName) *irv1.Shape {
	return w.n.preset(entity, irv1.Preset_PRESET_KEY, irv1.Form_FORM_UNSPECIFIED)
}

// forShape is a dependent child's shape for the relation that points at the
// parent: base without the foreign-key columns, which the parent supplies
// (§14.5). PostDraftForAuthor is post.draft.flat for author. It returns the
// new shape's id, or nil when base is nil.
func (w *nestWrite) forShape(base *irv1.Shape) *irv1.ShapeId {
	if base == nil {
		return nil
	}
	forward := w.rel.GetInverseOf()
	s := proto.Clone(base).(*irv1.Shape)
	s.Id.ForRelation = forward
	s.HostName = &irv1.Identifier{Words: slices.Concat(base.GetHostName().GetWords(), []string{"for"}, w.n.relationWords(w.rel.GetTo().GetTable(), forward))}
	s.Lineage = append(s.Lineage, &irv1.LineageStep{Op: irv1.ShapeOp_FOR_RELATION, Relation: forward})
	t := w.n.tables[formsEntityKey(s.GetEntity())]
	for _, c := range w.rel.GetTo().GetColumns() {
		if fieldOf(s, c) == nil {
			continue // already forbidden, for its own reason
		}
		s.Fields = slices.DeleteFunc(s.Fields, func(f *irv1.Field) bool { return f.GetColumn() == c })
		s.Forbidden = append(s.Forbidden, &irv1.ForbiddenField{Column: c, Reason: irv1.ForbiddenReason_FORBIDDEN_PARENT})
	}
	s.Nests = slices.DeleteFunc(s.Nests, func(x *irv1.Nest) bool { return x.GetRelation() == forward })
	sortForbidden(s, t)
	w.added = append(w.added, s)
	return proto.Clone(s.GetId()).(*irv1.ShapeId)
}

// sortForbidden orders forbidden columns by their position in t, then
// forbidden relations by name.
func sortForbidden(s *irv1.Shape, t *irv1.Table) {
	rank := map[string]int{}
	for i, c := range t.GetColumns() {
		rank[c.GetName().GetStored()] = i
	}
	pos := func(f *irv1.ForbiddenField) int {
		if f.GetRelation() != "" {
			return len(rank)
		}
		return rank[f.GetColumn()]
	}
	slices.SortStableFunc(s.Forbidden, func(a, b *irv1.ForbiddenField) int {
		return cmp.Or(cmp.Compare(pos(a), pos(b)), strings.Compare(a.GetRelation(), b.GetRelation()))
	})
}

// forbiddenReason is the reason base forbids the first of cols it forbids,
// FORBIDDEN_BOUND whenever the scope supplies any of them, or
// FORBIDDEN_UNSPECIFIED when base holds them all or is nil.
func forbiddenReason(base *irv1.Shape, cols []string) irv1.ForbiddenReason {
	reason := irv1.ForbiddenReason_FORBIDDEN_UNSPECIFIED
	for _, f := range base.GetForbidden() {
		if f.GetRelation() != "" || !slices.Contains(cols, f.GetColumn()) {
			continue
		}
		if f.GetReason() == irv1.ForbiddenReason_FORBIDDEN_BOUND {
			return f.GetReason()
		}
		if reason == irv1.ForbiddenReason_FORBIDDEN_UNSPECIFIED {
			reason = f.GetReason()
		}
	}
	return reason
}

func fieldOf(s *irv1.Shape, column string) *irv1.Field {
	for _, f := range s.GetFields() {
		if f.GetColumn() == column {
			return f
		}
	}
	return nil
}

// tierOf is the preset a shape derives from: its own, or its lineage root's
// for a custom shape.
func tierOf(s *irv1.Shape) irv1.Preset {
	if p := s.GetId().GetPreset(); p != irv1.Preset_PRESET_UNSPECIFIED {
		return p
	}
	return s.GetLineage()[0].GetPreset()
}

// preset is the flat preset (or Key) of entity, or nil.
func (n *Nester) preset(entity *irv1.QualifiedName, p irv1.Preset, form irv1.Form) *irv1.Shape {
	return n.shapes[n.spell(&irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: entity}, Preset: p, Form: form})]
}

// relationWords are the words of the relation name owned by entity.
func (n *Nester) relationWords(entity *irv1.QualifiedName, name string) []string {
	for _, r := range n.tables[formsEntityKey(entity)].GetRelations() {
		if r.GetName().GetStored() == name {
			return r.GetName().GetWords()
		}
	}
	return strings.Split(name, "_")
}

// entityWords are the entity's host words, or its name's words before the
// core has computed host names.
func (n *Nester) entityWords(entity *irv1.QualifiedName) []string {
	t := n.tables[formsEntityKey(entity)]
	if w := t.GetHostName().GetWords(); len(w) > 0 {
		return w
	}
	return t.GetName().GetWords()
}

// spell is a shape id's dotted spelling (§14.1): user.draft.flat, user.key,
// post.draft.flat.for.author, user_summary.
func (n *Nester) spell(id *irv1.ShapeId) string {
	var parts []string
	if c := id.GetCustom(); c != "" {
		parts = append(parts, c)
	} else {
		parts = append(parts, strings.Join(n.entityWords(id.GetEntity()), "_"), presetName(id.GetPreset()))
		if id.GetForm() != irv1.Form_FORM_UNSPECIFIED {
			parts = append(parts, formName(id.GetForm()))
		}
	}
	if r := id.GetForRelation(); r != "" {
		parts = append(parts, "for", r)
	}
	return strings.Join(parts, ".")
}
