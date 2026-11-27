package shapes

import (
	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// owningChange is an owning relation in a Partial: at most one change (§14.6).
// Link, Create and Unlink point the foreign key elsewhere, so they exist only
// when the Partial can set the foreign-key columns (settable), and Unlink
// only when the foreign key is nullable. Update patches the row the foreign
// key points at. There is no Delete.
func (w *nestWrite) owningChange(fk []string, settable bool) *irv1.Nest {
	related := w.rel.GetTo().GetTable()
	var link, create, unlink *variant
	if settable {
		link = w.variant(irv1.RelationChange_RELATION_CHANGE_LINK, true, w.keyOf(related), nil, "link")
		create = w.variant(irv1.RelationChange_RELATION_CHANGE_CREATE, false, nil, w.payload(irv1.Preset_PRESET_DRAFT), "create")
		if w.rel.GetOptional() {
			unlink = w.variant(irv1.RelationChange_RELATION_CHANGE_UNLINK, false, nil, nil, "unlink")
		}
	}
	update := w.variant(irv1.RelationChange_RELATION_CHANGE_UPDATE, false, nil, w.payload(irv1.Preset_PRESET_PARTIAL), "update")
	u := w.union(irv1.RelationUnionKind_RELATION_UNION_RELATION_CHANGE, w.shape.GetEntity(), w.rel.GetName().GetStored(),
		link, create, update, unlink)
	if u == nil {
		return nil
	}
	nest := &irv1.Nest{Presence: irv1.Presence_PRESENCE_OPTIONAL, Target: &irv1.Nest_Union{Union: u}}
	if settable {
		// With neither the columns nor the relation, the foreign key is
		// unchanged, so neither is needed.
		nest.Supplies = w.supplies(fk)
	}
	return nest
}

// childChange is a dependent relation in a Partial: a list of changes for a
// to-many relation, at most one for a to-one relation. The relation's
// collection policy decides which changes exist: append allows only Create
// (§14.6). Update and Create write the child's shapes for the forward
// relation, which forbid the foreign key the parent supplies.
func (w *nestWrite) childChange() *irv1.Nest {
	child := w.rel.GetTo().GetTable()
	key := w.keyOf(child)
	var update, del *variant
	// Update and Delete find a child by its Key, and only a child with a
	// Partial, which a view never has, can be changed.
	writable := w.n.preset(child, irv1.Preset_PRESET_PARTIAL, irv1.Form_FORM_FLAT) != nil
	if w.rel.GetPolicy().GetCollection() != irv1.CollectionPolicy_COLLECTION_POLICY_APPEND && key != nil && writable {
		update = w.forVariant(irv1.RelationChange_RELATION_CHANGE_UPDATE, key, irv1.Preset_PRESET_PARTIAL, "update")
		del = w.variant(irv1.RelationChange_RELATION_CHANGE_DELETE, true, key, nil, "delete")
	}
	create := w.forVariant(irv1.RelationChange_RELATION_CHANGE_CREATE, nil, irv1.Preset_PRESET_DRAFT, "create")
	u := w.union(irv1.RelationUnionKind_RELATION_UNION_CHILD_CHANGE, child, w.rel.GetInverseOf(), create, update, del)
	if u == nil {
		return nil
	}
	return &irv1.Nest{
		Presence: irv1.Presence_PRESENCE_OPTIONAL,
		Target:   &irv1.Nest_Union{Union: u},
		Policy:   proto.Clone(w.rel.GetPolicy()).(*irv1.RelationPolicy),
	}
}

// forVariant is a child change whose payload is the child's shape of tier
// for the forward relation, or nil when the child has no shape of that tier.
func (w *nestWrite) forVariant(change irv1.RelationChange, key *irv1.Shape, tier irv1.Preset, verb ...string) *variant {
	id := w.forShape(w.payload(tier))
	if id == nil {
		return nil
	}
	return w.variant(change, key != nil, key, &irv1.Shape{Id: id}, verb...)
}

// manyToManyChange is a many-to-many relation in a Partial: Link, Unlink or
// Create, under the relation's collection policy. Unlink removes only the
// junction row. Under append only the changes that add a row, Link and
// Create, exist (§14.6).
func (w *nestWrite) manyToManyChange() *irv1.Nest {
	far := w.rel.GetTo().GetTable()
	link := w.variant(irv1.RelationChange_RELATION_CHANGE_LINK, true, w.keyOf(far), nil, "link")
	create := w.variant(irv1.RelationChange_RELATION_CHANGE_CREATE, false, nil, w.payload(irv1.Preset_PRESET_DRAFT), "create")
	var unlink *variant
	if w.rel.GetPolicy().GetCollection() != irv1.CollectionPolicy_COLLECTION_POLICY_APPEND {
		unlink = w.variant(irv1.RelationChange_RELATION_CHANGE_UNLINK, true, w.keyOf(far), nil, "unlink")
	}
	u := w.union(irv1.RelationUnionKind_RELATION_UNION_RELATION_CHANGE, w.shape.GetEntity(), w.rel.GetName().GetStored(),
		link, create, unlink)
	if u == nil {
		return nil
	}
	return &irv1.Nest{
		Presence: irv1.Presence_PRESENCE_OPTIONAL,
		Target:   &irv1.Nest_Union{Union: u},
		Policy:   proto.Clone(w.rel.GetPolicy()).(*irv1.RelationPolicy),
	}
}
