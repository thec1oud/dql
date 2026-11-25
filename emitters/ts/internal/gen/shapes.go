package gen

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Diagnostic codes of shapes.
const (
	// CodeReferenceUnknown: a shape names a column, relation, shape or union
	// the bundle does not hold.
	CodeReferenceUnknown = "ts-reference-unknown"
	// CodeTimeTypeInvalid: an x.ts.time-type annotation names a host type
	// the emitter does not offer, or sits on a column that is not an
	// instant (§7.7, §8.8).
	CodeTimeTypeInvalid = "ts-time-type-invalid"
)

// TimeTypeKey is the plugin annotation that picks a column's host time type
// (§7.7): "date" maps an instant to JavaScript's Date, accepting its
// millisecond precision; "temporal" is the default.
const TimeTypeKey = "x.ts.time-type"

// presetWords and formWords spell presets and forms in dotted shape ids.
var (
	presetWords = map[irv1.Preset]string{
		irv1.Preset_PRESET_FULL: "full", irv1.Preset_PRESET_DRAFT: "draft", irv1.Preset_PRESET_PARTIAL: "partial",
		irv1.Preset_PRESET_KEY: "key", irv1.Preset_PRESET_SCOPE: "scope",
	}
	formWords = map[irv1.Form]string{irv1.Form_FORM_FLAT: "flat", irv1.Form_FORM_NESTS: "nests", irv1.Form_FORM_NESTED: "nested"}
)

// shapeKey identifies a shape id within the bundle.
func shapeKey(id *irv1.ShapeId) string {
	e := id.GetEntity()
	return fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%d\x00%d\x00%s",
		id.GetCustom(), e.GetCatalog(), e.GetNamespace(), e.GetObject(), id.GetPreset(), id.GetForm(), id.GetForRelation())
}

// unionKey identifies a relation union within the bundle.
func unionKey(id *irv1.RelationUnionId) string {
	e := id.GetEntity()
	return fmt.Sprintf("%d\x00%s\x00%s\x00%s\x00%s\x00%s", id.GetKind(), e.GetCatalog(), e.GetNamespace(), e.GetObject(), id.GetRelation(), id.GetCustom())
}

// shapeIndex finds the shape layer's shapes and unions and the catalog's
// tables by reference.
type shapeIndex struct {
	tables map[key]*irv1.Table
	shapes map[string]*irv1.Shape
	unions map[string]*irv1.RelationUnion
	// files holds each entity's file, custom the custom shapes' file.
	files  map[key]*file
	custom *file
	// members holds each shape's companion members, by shape key, in the
	// order the conversion table lists them (§14.9).
	members map[string][]member
}

func (g *generator) indexShapes() {
	g.ix = shapeIndex{tables: map[key]*irv1.Table{}, shapes: map[string]*irv1.Shape{}, unions: map[string]*irv1.RelationUnion{}, files: map[key]*file{}, members: map[string][]member{}}
	g.eachNamespace(func(q func(string) *irv1.QualifiedName, ns *irv1.Namespace) {
		for _, t := range ns.GetTables() {
			g.ix.tables[keyOf(q(t.GetName().GetStored()))] = t
		}
	})
	for _, s := range g.b.GetShapes().GetShapes() {
		g.ix.shapes[shapeKey(s.GetId())] = s
	}
	for _, u := range g.b.GetShapes().GetUnions() {
		g.ix.unions[unionKey(u.GetId())] = u
	}
}

// dotted spells a shape id as the IR's display form (§14.1): the entity's
// host-name words joined by _, the preset and the form, and .for. and the
// relation for a relation-qualified shape; a custom shape by its name.
func (g *generator) dotted(id *irv1.ShapeId) string {
	if id.GetCustom() != "" {
		return id.GetCustom()
	}
	parts := []string{strings.Join(g.ix.tables[keyOf(id.GetEntity())].GetHostName().GetWords(), "_"), presetWords[id.GetPreset()]}
	if f, ok := formWords[id.GetForm()]; ok {
		parts = append(parts, f)
	}
	if id.GetForRelation() != "" {
		parts = append(parts, "for", id.GetForRelation())
	}
	return strings.Join(parts, ".")
}

// shapeType is the type of the shape id names, imported into f.
func (g *generator) shapeType(f *file, id *irv1.ShapeId, at *irv1.QualifiedName, what string) string {
	s, ok := g.ix.shapes[shapeKey(id)]
	if !ok {
		g.errorf(CodeReferenceUnknown, at, "%s references a shape the bundle does not hold", what)
		return "unknown"
	}
	return g.ref(f, TypeName(s.GetHostName().GetWords()), false)
}

// unionType is the type of the union id names, imported into f.
func (g *generator) unionType(f *file, id *irv1.RelationUnionId, at *irv1.QualifiedName, what string) string {
	u, ok := g.ix.unions[unionKey(id)]
	if !ok {
		g.errorf(CodeReferenceUnknown, at, "%s references a relation union the bundle does not hold", what)
		return "unknown"
	}
	return g.ref(f, TypeName(u.GetHostName().GetWords()), false)
}

// shapes generates every shape and union (§14, §15.5): an entity's presets
// and its unions into one file per entity, custom shapes into
// dql-shapes.dql.ts. Every name is declared first, so that a shape can
// reference one declared after it in another file. Fields keep column order
// (§3.2).
func (g *generator) shapes() {
	var shapes []*irv1.Shape
	for _, s := range g.b.GetShapes().GetShapes() {
		if _, ok := g.ix.tables[keyOf(s.GetEntity())]; !ok {
			g.errorf(CodeReferenceUnknown, s.GetEntity(), "shape %s belongs to an entity the bundle does not hold", s.GetId().GetCustom())
			continue
		}
		g.declare(g.shapeFile(s), TypeName(s.GetHostName().GetWords()), "shape "+g.dotted(s.GetId()), s.GetEntity())
		shapes = append(shapes, s)
	}
	var unions []*irv1.RelationUnion
	for _, u := range g.b.GetShapes().GetUnions() {
		if _, ok := g.ix.tables[keyOf(u.GetId().GetEntity())]; !ok {
			g.errorf(CodeReferenceUnknown, u.GetId().GetEntity(), "relation union %s belongs to an entity the bundle does not hold", strings.Join(u.GetHostName().GetWords(), " "))
			continue
		}
		g.declare(g.entityFile(u.GetId().GetEntity()), TypeName(u.GetHostName().GetWords()), "relation union "+strings.Join(u.GetHostName().GetWords(), " "), u.GetId().GetEntity())
		unions = append(unions, u)
	}
	for _, s := range shapes {
		g.shape(g.shapeFile(s), s)
	}
	for _, u := range unions {
		g.union(g.entityFile(u.GetId().GetEntity()), u)
	}
}

// entityFile is the file of an entity's presets, unions and merges.
func (g *generator) entityFile(q *irv1.QualifiedName) *file {
	k := keyOf(q)
	if f, ok := g.ix.files[k]; ok {
		return f
	}
	f := g.newFile(strings.Join(g.ix.tables[k].GetHostName().GetWords(), "_"))
	g.ix.files[k] = f
	return f
}

// shapeFile is the file that declares s: its entity's file, or
// dql-shapes.dql.ts for a custom shape.
func (g *generator) shapeFile(s *irv1.Shape) *file {
	if s.GetId().GetCustom() == "" {
		return g.entityFile(s.GetEntity())
	}
	if g.ix.custom == nil {
		g.ix.custom = g.newFile("dql-shapes")
	}
	return g.ix.custom
}

// rootPreset is the preset a shape derives from: the first step of its
// lineage (§14.10).
func rootPreset(s *irv1.Shape) irv1.Preset {
	if len(s.GetLineage()) == 0 {
		return s.GetId().GetPreset()
	}
	return s.GetLineage()[0].GetPreset()
}

// embedded returns the shape whose fields s holds unchanged, which s extends
// instead of repeating them: the Flat form of its preset, as UserFullNested
// is UserFull & UserFullNests (§14.4) and post_with_tags extends PostFull
// (§14.8). It returns nil when s is that shape, holds no fields, or changes
// them, as a Nested Draft makes FK columns optional (§14.5).
func (g *generator) embedded(s *irv1.Shape) *irv1.Shape {
	if len(s.GetFields()) == 0 {
		return nil
	}
	id := &irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: s.GetEntity()}, Preset: rootPreset(s), Form: irv1.Form_FORM_FLAT, ForRelation: s.GetId().GetForRelation()}
	if id.GetPreset() == irv1.Preset_PRESET_KEY || id.GetPreset() == irv1.Preset_PRESET_SCOPE {
		return nil
	}
	flat := g.ix.shapes[shapeKey(id)]
	if flat == nil || flat == s || !equalFields(flat.GetFields(), s.GetFields()) {
		return nil
	}
	return flat
}

// embeddedNests returns the Nests form a Nested preset is the intersection
// of, with its Flat form, when the two hold the same relations as types
// (§14.4, §14.6).
func (g *generator) embeddedNests(s *irv1.Shape) *irv1.Shape {
	if s.GetId().GetForm() != irv1.Form_FORM_NESTED || len(s.GetNests()) == 0 {
		return nil
	}
	id := proto.Clone(s.GetId()).(*irv1.ShapeId)
	id.Form = irv1.Form_FORM_NESTS
	n := g.ix.shapes[shapeKey(id)]
	if n == nil || !slices.EqualFunc(n.GetNests(), s.GetNests(), sameNestType) {
		return nil
	}
	return n
}

// sameNestType reports whether two nests get the same member: what a nest
// supplies, needs and its policy shape writes, not types (§14.5, §14.6).
func sameNestType(a, b *irv1.Nest) bool {
	return a.GetRelation() == b.GetRelation() && a.GetPresence() == b.GetPresence() && a.GetNullable() == b.GetNullable() &&
		proto.Equal(a.GetShape(), b.GetShape()) && proto.Equal(a.GetUnion(), b.GetUnion())
}

func equalFields(a, b []*irv1.Field) bool {
	return slices.EqualFunc(a, b, func(x, y *irv1.Field) bool { return proto.Equal(x, y) })
}

// shape generates one shape's type (§14.1, §15.4): each field typed by
// presence and nullability, never collapsing the two (f: T, f: T | null,
// f?: T, f?: T | null), then its relations (§12.5, §14.4 to §14.6). A shape
// that holds its Flat form's fields unchanged extends it, and a Nested form
// whose relations are its Nests form's is the intersection of the two.
func (g *generator) shape(f *file, s *irv1.Shape) {
	name := TypeName(s.GetHostName().GetWords())
	q := s.GetEntity()
	t := g.ix.tables[keyOf(q)]
	if len(s.GetBind()) > 0 {
		f.printf("/**\n * %s is the shape %s of %s.\n *\n * Bound, so its functions take them as parameters (§14.8): %s.\n */\n",
			name, g.dotted(s.GetId()), displayName(q), strings.Join(s.GetBind(), ", "))
	} else {
		f.printf("/** %s is the shape %s of %s. */\n", name, g.dotted(s.GetId()), displayName(q))
	}
	base := g.embedded(s)
	if n := g.embeddedNests(s); n != nil && base != nil {
		f.printf("export type %s = %s & %s;\n\n", name, g.shapeType(f, base.GetId(), q, ""), g.shapeType(f, n.GetId(), q, ""))
		return
	}
	members := map[string]string{}
	member := func(tsName, stored string, at *irv1.QualifiedName) {
		if prev, ok := members[tsName]; ok {
			g.errorf(CodeNameCollision, at, "%s and %s of shape %s both get the TypeScript name %s (§4.3, §15.2); give one of them a name annotation",
				prev, stored, g.dotted(s.GetId()), tsName)
		}
		members[tsName] = stored
	}
	if base != nil {
		f.printf("export interface %s extends %s {\n", name, g.shapeType(f, base.GetId(), q, ""))
	} else {
		f.printf("export interface %s {\n", name)
		for _, fld := range s.GetFields() {
			at := &irv1.QualifiedName{Catalog: q.GetCatalog(), Namespace: q.GetNamespace(), Object: q.GetObject(), Member: fld.GetColumn()}
			col := column(t, fld.GetColumn())
			if col == nil {
				g.errorf(CodeReferenceUnknown, at, "shape %s has field %s, which is not a column of %s", g.dotted(s.GetId()), fld.GetColumn(), displayName(q))
				continue
			}
			fn := MemberName(col.GetHostName().GetWords())
			member(fn, fld.GetColumn(), at)
			typ, note := g.fieldType(f, col, fld, at)
			f.printf("  %s%s: %s;%s\n", fn, optional(fld.GetPresence()), typ, fieldComment(fld, note))
		}
	}
	for _, n := range s.GetNests() {
		at := &irv1.QualifiedName{Catalog: q.GetCatalog(), Namespace: q.GetNamespace(), Object: q.GetObject(), Member: n.GetRelation()}
		r := relation(t, n.GetRelation())
		if r == nil {
			g.errorf(CodeReferenceUnknown, at, "shape %s nests %s, which is not a relation of %s", g.dotted(s.GetId()), n.GetRelation(), displayName(q))
			continue
		}
		fn := MemberName(r.GetName().GetWords())
		member(fn, n.GetRelation(), at)
		f.printf("  %s%s: %s;\n", fn, optional(n.GetPresence()), g.nestType(f, n, r, at))
	}
	f.printf("}\n\n")
}

// optional is the ? of an optional member (§14.1): absent means DEFAULT in a
// Draft and unchanged in a Partial.
func optional(p irv1.Presence) string {
	if p == irv1.Presence_PRESENCE_OPTIONAL {
		return "?"
	}
	return ""
}

// fieldType is the type of a shape's field: its column's host type, or Date
// for an instant the project mapped with x.ts.time-type (§7.7, §8.8), and
// then | null when the field is nullable (§14.1). note says what the
// mapping costs, for the field's comment.
func (g *generator) fieldType(f *file, col *irv1.Column, fld *irv1.Field, at *irv1.QualifiedName) (typ, note string) {
	typ = g.tsType(f, fld.GetType(), at, "column "+displayName(at))
	switch v := timeType(col); v {
	case "", "temporal":
	case "date":
		if fld.GetType().GetInstant() == nil {
			g.errorf(CodeTimeTypeInvalid, at, "column %s has %s: date, which applies only to instant columns (§7.7, §8.8); remove it", displayName(at), TimeTypeKey)
			break
		}
		typ, note = "Date", "Date, millisecond precision ("+TimeTypeKey+": date)"
	default:
		g.errorf(CodeTimeTypeInvalid, at, "column %s has %s: %s; use date or temporal (§7.7)", displayName(at), TimeTypeKey, strconv.Quote(v))
	}
	if fld.GetNullable() {
		typ += " | null"
	}
	return typ, note
}

// timeType is the value of a column's x.ts.time-type annotation, or "".
func timeType(col *irv1.Column) string {
	for _, a := range col.GetAnnotations() {
		if a.GetKey() == TimeTypeKey {
			return a.GetValue().GetString_()
		}
	}
	return ""
}

// fieldComment notes what a field's type does not say: a sensitive value
// (§7.5), a Key's expected version (§14.3), a non-null narrowing that
// decoding enforces (§6.4, §13, §14.8), and a lossy host type (§7.7).
func fieldComment(fld *irv1.Field, note string) string {
	var notes []string
	if fld.GetRole() == irv1.FieldRole_FIELD_ROLE_EXPECTED_VERSION {
		notes = append(notes, "expected version, compared on writes")
	}
	if !fld.GetNullable() {
		switch fld.GetNullabilitySource() {
		case irv1.NullabilitySource_NULLABILITY_SOURCE_ANNOTATION:
			notes = append(notes, "asserted non-null; checked when decoding")
		case irv1.NullabilitySource_NULLABILITY_SOURCE_SHAPE_OPERATOR:
			notes = append(notes, "narrowed; enforced when decoding")
		}
	}
	if note != "" {
		notes = append(notes, note)
	}
	if fld.GetRedact() {
		notes = append(notes, "sensitive")
	}
	if len(notes) == 0 {
		return ""
	}
	return " // " + strings.Join(notes, "; ")
}

// toMany reports whether a relation holds a list.
func toMany(r *irv1.Relation) bool {
	return r.GetKind() == irv1.RelationKind_RELATION_KIND_ONE_TO_MANY || r.GetKind() == irv1.RelationKind_RELATION_KIND_MANY_TO_MANY
}

// nestType is the type of a relation member (§12.5, §14.4 to §14.6): the
// related shape, or a union of the changes the relation allows, with
// | null for a nullable to-one relation and [] for a to-many one. Its
// presence is the member's ?.
func (g *generator) nestType(f *file, n *irv1.Nest, r *irv1.Relation, at *irv1.QualifiedName) string {
	what := "relation " + displayName(at)
	var elem string
	if n.GetUnion() != nil {
		elem = g.unionType(f, n.GetUnion(), at, what)
	} else {
		elem = g.shapeType(f, n.GetShape(), at, what)
	}
	if n.GetNullable() {
		elem += " | null"
	}
	if toMany(r) {
		return parenthesize(elem) + "[]"
	}
	return elem
}

func column(t *irv1.Table, stored string) *irv1.Column {
	for _, c := range t.GetColumns() {
		if c.GetName().GetStored() == stored {
			return c
		}
	}
	return nil
}

func relation(t *irv1.Table, stored string) *irv1.Relation {
	for _, r := range t.GetRelations() {
		if r.GetName().GetStored() == stored {
			return r
		}
	}
	return nil
}

// changeKinds spell each change as its variant's kind.
var changeKinds = map[irv1.RelationChange]string{
	irv1.RelationChange_RELATION_CHANGE_LINK:   "link",
	irv1.RelationChange_RELATION_CHANGE_CREATE: "create",
	irv1.RelationChange_RELATION_CHANGE_UPDATE: "update",
	irv1.RelationChange_RELATION_CHANGE_DELETE: "delete",
	irv1.RelationChange_RELATION_CHANGE_UNLINK: "unlink",
}

// union generates a reference or change type (§14.5, §14.6): a union of one
// object per change the relation's policy allows, told apart by kind, so a
// change the policy forbids cannot be written:
//
//	export type PostEditorChange =
//	  | { kind: "link"; key: UserKey }
//	  | { kind: "unlink" };
func (g *generator) union(f *file, u *irv1.RelationUnion) {
	name := TypeName(u.GetHostName().GetWords())
	q := u.GetId().GetEntity()
	what := "relation union " + strings.Join(u.GetHostName().GetWords(), " ")
	var target *irv1.QualifiedName
	var kinds []string
	for _, v := range u.GetVariants() {
		if target == nil {
			target = cmp.Or(v.GetKey(), v.GetPayload()).GetEntity()
		}
		kinds = append(kinds, changeKinds[v.GetChange()])
	}
	f.printf("/** %s holds one change to a related %s: %s (§14.5, §14.6). */\nexport type %s =\n", name, displayName(target), strings.Join(kinds, ", "), name)
	for i, v := range u.GetVariants() {
		parts := []string{"kind: " + strconv.Quote(kinds[i])}
		if v.GetKey() != nil {
			parts = append(parts, "key: "+g.shapeType(f, v.GetKey(), q, what))
		}
		switch v.GetChange() {
		case irv1.RelationChange_RELATION_CHANGE_CREATE:
			parts = append(parts, "draft: "+g.shapeType(f, v.GetPayload(), q, what))
		case irv1.RelationChange_RELATION_CHANGE_UPDATE:
			parts = append(parts, "partial: "+g.shapeType(f, v.GetPayload(), q, what))
		}
		end := ""
		if i == len(u.GetVariants())-1 {
			end = ";"
		}
		f.printf("  | { %s }%s\n", strings.Join(parts, "; "), end)
	}
	f.printf("\n")
}
