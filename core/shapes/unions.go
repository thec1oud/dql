package shapes

import (
	"slices"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// variant is one change of a union before the union names it. verb holds
// the words that tell it from the union's other variants.
type variant struct {
	v    *irv1.RelationVariant
	verb []string
}

// variant is a change of the union being built. A change that finds a row
// needs key, and a Create or Update needs payload; when the shape it needs
// is nil, such as the Draft of a view, the change does not exist and variant
// returns nil.
func (w *write) variant(change irv1.RelationChange, needKey bool, key, payload *irv1.Shape, verb ...string) *variant {
	v := &irv1.RelationVariant{Change: change}
	if needKey {
		if key == nil {
			return nil
		}
		v.Key = proto.Clone(key.GetId()).(*irv1.ShapeId)
	}
	if change == irv1.RelationChange_RELATION_CHANGE_CREATE || change == irv1.RelationChange_RELATION_CHANGE_UPDATE {
		if payload == nil {
			return nil
		}
		v.Payload = proto.Clone(payload.GetId()).(*irv1.ShapeId)
	}
	return &variant{v: v, verb: verb}
}

// union builds the union of kind for the relation being written, with the
// variants that exist, and returns its id, or nil when no variant exists.
// entity is the entity whose rows the variants write.
//
// Host words follow §14.5 and §14.6: UserRef with UserLink and UserCreate,
// TagLink with TagLinkKey and TagLinkCreate, PostChangeForAuthor with
// PostUpdateForAuthor, PostAuthorChange with its changes. A union whose
// variants hold a custom shape's rows belongs to the custom shape that
// nests it, and its words start with that shape's words and the relation's.
func (w *write) union(kind irv1.RelationUnionKind, entity *irv1.QualifiedName, relation string, variants ...*variant) *irv1.RelationUnionId {
	variants = slices.DeleteFunc(variants, func(v *variant) bool { return v == nil })
	if len(variants) == 0 {
		return nil
	}
	id := &irv1.RelationUnionId{Kind: kind, Entity: proto.Clone(entity).(*irv1.QualifiedName), Relation: relation}
	rel := w.rel.GetName().GetWords()
	var prefix, tail, word []string
	switch kind {
	case irv1.RelationUnionKind_RELATION_UNION_REF:
		prefix, word = w.n.entityWords(entity), []string{"ref"}
	case irv1.RelationUnionKind_RELATION_UNION_LINK:
		prefix, word = w.n.entityWords(entity), []string{"link"}
	case irv1.RelationUnionKind_RELATION_UNION_CHILD_CHANGE:
		prefix, word = w.n.entityWords(entity), []string{"change"}
		tail = append([]string{"for"}, w.n.relationWords(entity, relation)...)
	default:
		prefix, word = slices.Concat(w.n.entityWords(entity), rel), []string{"change"}
	}
	if w.target.GetId().GetCustom() != "" {
		decl := w.shape
		if decl.GetId().GetCustom() == "" {
			decl = w.target
		}
		id.Custom = decl.GetId().GetCustom()
		prefix, tail = slices.Concat(decl.GetHostName().GetWords(), rel), nil
	}
	u := &irv1.RelationUnion{Id: id, HostName: &irv1.Identifier{Words: slices.Concat(prefix, word, tail)}}
	for _, v := range variants {
		v.v.HostName = &irv1.Identifier{Words: slices.Concat(prefix, v.verb, tail)}
		u.Variants = append(u.Variants, v.v)
	}
	slices.SortStableFunc(u.Variants, func(a, b *irv1.RelationVariant) int { return int(a.GetChange()) - int(b.GetChange()) })
	w.unions = append(w.unions, u)
	return proto.Clone(id).(*irv1.RelationUnionId)
}
