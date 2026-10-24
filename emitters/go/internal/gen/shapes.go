package gen

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// CodeReferenceUnknown: a shape names a column, relation, shape or union the
// bundle does not hold.
const CodeReferenceUnknown = "go-reference-unknown"

const pkgOpt = Runtime + "/opt"

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
}

func (g *generator) indexShapes() {
	g.ix = shapeIndex{tables: map[key]*irv1.Table{}, shapes: map[string]*irv1.Shape{}, unions: map[string]*irv1.RelationUnion{}}
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

// shapeType is the Go type of the shape id names.
func (g *generator) shapeType(id *irv1.ShapeId, at *irv1.QualifiedName, what string) string {
	s, ok := g.ix.shapes[shapeKey(id)]
	if !ok {
		g.errorf(CodeReferenceUnknown, at, "%s references a shape the bundle does not hold", what)
		return "any"
	}
	return Exported(s.GetHostName().GetWords())
}

// unionType is the Go type of the union id names.
func (g *generator) unionType(id *irv1.RelationUnionId, at *irv1.QualifiedName, what string) string {
	u, ok := g.ix.unions[unionKey(id)]
	if !ok {
		g.errorf(CodeReferenceUnknown, at, "%s references a relation union the bundle does not hold", what)
		return "any"
	}
	return Exported(u.GetHostName().GetWords())
}

// shapes generates every shape and union (§14, §15.5): an entity's presets
// and its unions into one file per entity, custom shapes into
// dql-shapes.dql.go. Fields keep column order (§3.2).
func (g *generator) shapes() {
	files := map[key]*file{}
	entityFile := func(q *irv1.QualifiedName) *file {
		k := keyOf(q)
		if f, ok := files[k]; ok {
			return f
		}
		f := g.newFile(strings.Join(g.ix.tables[k].GetHostName().GetWords(), "_"))
		files[k] = f
		return f
	}
	var custom *file
	for _, s := range g.b.GetShapes().GetShapes() {
		if _, ok := g.ix.tables[keyOf(s.GetEntity())]; !ok {
			g.errorf(CodeReferenceUnknown, s.GetEntity(), "shape %s belongs to an entity the bundle does not hold", s.GetId().GetCustom())
			continue
		}
		if s.GetId().GetCustom() != "" {
			if custom == nil {
				custom = g.newFile("dql-shapes")
			}
			g.shape(custom, s)
			continue
		}
		g.shape(entityFile(s.GetEntity()), s)
	}
	for _, u := range g.b.GetShapes().GetUnions() {
		if _, ok := g.ix.tables[keyOf(u.GetId().GetEntity())]; !ok {
			g.errorf(CodeReferenceUnknown, u.GetId().GetEntity(), "relation union %s belongs to an entity the bundle does not hold", strings.Join(u.GetHostName().GetWords(), " "))
			continue
		}
		g.union(entityFile(u.GetId().GetEntity()), u)
	}
}

// rootPreset is the preset a shape derives from: the first step of its
// lineage (§14.10).
func rootPreset(s *irv1.Shape) irv1.Preset {
	if len(s.GetLineage()) == 0 {
		return s.GetId().GetPreset()
	}
	return s.GetLineage()[0].GetPreset()
}

// embedded returns the shape whose fields s holds unchanged, which s embeds
// instead of repeating them: the Flat form of its preset, as UserFullNested
// embeds UserFull (§14.4) and post_with_tags embeds PostFull (§14.8). It
// returns nil when s is that shape, holds no fields, or changes them, as a
// Nested Draft makes FK columns optional (§14.5).
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

// embeddedNests returns the Nests form a Nested preset embeds next to its
// embedded Flat form when the two hold the same relations as Go types, as
// UserPartialNested embeds UserPartialNests (§14.4, §14.6). A Nested shape
// that spells out its own fields spells out its relations too, as
// PostDraftNested does (§14.5).
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

// sameNestType reports whether two nests get the same Go field: what a nest
// supplies, needs and its policy shape writes, not types (§14.5, §14.6).
func sameNestType(a, b *irv1.Nest) bool {
	return a.GetRelation() == b.GetRelation() && a.GetPresence() == b.GetPresence() && a.GetNullable() == b.GetNullable() &&
		proto.Equal(a.GetShape(), b.GetShape()) && proto.Equal(a.GetUnion(), b.GetUnion())
}

func equalFields(a, b []*irv1.Field) bool {
	return slices.EqualFunc(a, b, func(x, y *irv1.Field) bool { return proto.Equal(x, y) })
}

// shape generates one shape's struct (§14.1): each field typed by presence
// and nullability, never collapsing the two (§14.1, §15.4), then its
// relations (§12.5, §14.4 to §14.6).
func (g *generator) shape(f *file, s *irv1.Shape) {
	name := Exported(s.GetHostName().GetWords())
	q := s.GetEntity()
	g.declare(name, "shape "+g.dotted(s.GetId()), q)
	t := g.ix.tables[keyOf(q)]
	f.printf("// %s is the shape %s of %s.\n", name, g.dotted(s.GetId()), displayName(q))
	if len(s.GetBind()) > 0 {
		f.printf("//\n// Bound, so its functions take them as parameters (§14.8): %s.\n", strings.Join(s.GetBind(), ", "))
	}
	f.printf("type %s struct {\n", name)
	fields := map[string]string{}
	member := func(goName, stored string, at *irv1.QualifiedName) {
		if prev, ok := fields[goName]; ok {
			g.errorf(CodeNameCollision, at, "%s and %s of shape %s both get the Go name %s (§4.3, §15.2); give one of them a name annotation",
				prev, stored, g.dotted(s.GetId()), goName)
		}
		fields[goName] = stored
	}
	base := g.embedded(s)
	if base != nil {
		f.printf("\t%s\n", Exported(base.GetHostName().GetWords()))
	} else {
		for _, fld := range s.GetFields() {
			at := &irv1.QualifiedName{Catalog: q.GetCatalog(), Namespace: q.GetNamespace(), Object: q.GetObject(), Member: fld.GetColumn()}
			col := column(t, fld.GetColumn())
			if col == nil {
				g.errorf(CodeReferenceUnknown, at, "shape %s has field %s, which is not a column of %s", g.dotted(s.GetId()), fld.GetColumn(), displayName(q))
				continue
			}
			fn := Exported(col.GetHostName().GetWords())
			member(fn, fld.GetColumn(), at)
			typ := g.field(f, fld.GetType(), fld.GetNullable(), at, "column "+displayName(at))
			if fld.GetPresence() == irv1.Presence_PRESENCE_OPTIONAL {
				typ = f.use(pkgOpt, "opt") + ".Opt[" + typ + "]"
			}
			f.printf("\t%s %s%s\n", fn, typ, fieldComment(fld))
		}
	}
	if n := g.embeddedNests(s); n != nil && base != nil {
		f.printf("\t%s\n", Exported(n.GetHostName().GetWords()))
	} else {
		for _, n := range s.GetNests() {
			at := &irv1.QualifiedName{Catalog: q.GetCatalog(), Namespace: q.GetNamespace(), Object: q.GetObject(), Member: n.GetRelation()}
			r := relation(t, n.GetRelation())
			if r == nil {
				g.errorf(CodeReferenceUnknown, at, "shape %s nests %s, which is not a relation of %s", g.dotted(s.GetId()), n.GetRelation(), displayName(q))
				continue
			}
			fn := Exported(r.GetName().GetWords())
			member(fn, n.GetRelation(), at)
			f.printf("\t%s %s\n", fn, g.nestType(f, s, n, r, at))
		}
	}
	f.printf("}\n\n")
}

// fieldComment notes what a field's type does not say: a sensitive value
// (§7.5), a Key's expected version (§14.3), and a non-null narrowing that
// decoding enforces (§6.4, §13, §14.8).
func fieldComment(fld *irv1.Field) string {
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

// nestType is the Go type of a relation field. Read shapes load every
// relation, so a to-one relation is the related shape, or a pointer when
// optional, and a to-many relation a slice (§12.5, §14.4). In a write shape
// an optional to-one relation is opt.Opt; a to-many relation is a slice in a
// Draft, where absent and empty mean the same, and opt.Opt of a slice in a
// Partial, where absent leaves the relation alone (§14.5, §14.6).
func (g *generator) nestType(f *file, s *irv1.Shape, n *irv1.Nest, r *irv1.Relation, at *irv1.QualifiedName) string {
	what := "relation " + displayName(at)
	var elem string
	if n.GetUnion() != nil {
		elem = g.unionType(n.GetUnion(), at, what)
	} else {
		elem = g.shapeType(n.GetShape(), at, what)
	}
	if n.GetNullable() {
		elem = "*" + elem
	}
	if toMany(r) {
		elem = "[]" + elem
		if n.GetPresence() == irv1.Presence_PRESENCE_OPTIONAL && rootPreset(s) == irv1.Preset_PRESET_PARTIAL {
			return f.use(pkgOpt, "opt") + ".Opt[" + elem + "]"
		}
		return elem
	}
	if n.GetPresence() == irv1.Presence_PRESENCE_OPTIONAL {
		return f.use(pkgOpt, "opt") + ".Opt[" + elem + "]"
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

// changeWords name each change's constant in the runtime.
var changeWords = map[irv1.RelationChange]string{
	irv1.RelationChange_RELATION_CHANGE_LINK:   "ChangeLink",
	irv1.RelationChange_RELATION_CHANGE_CREATE: "ChangeCreate",
	irv1.RelationChange_RELATION_CHANGE_UPDATE: "ChangeUpdate",
	irv1.RelationChange_RELATION_CHANGE_DELETE: "ChangeDelete",
	irv1.RelationChange_RELATION_CHANGE_UNLINK: "ChangeUnlink",
}

// union generates a reference or change type (§14.5, §14.6): a struct that
// holds exactly one variant, built only through the variants' constructors,
// so a change the relation's policy forbids cannot be written. Change says
// which variant it holds.
func (g *generator) union(f *file, u *irv1.RelationUnion) {
	name := Exported(u.GetHostName().GetWords())
	q := u.GetId().GetEntity()
	what := "relation union " + strings.Join(u.GetHostName().GetWords(), " ")
	g.declare(name, what, q)
	dql := f.use(pkgDQL, "dql")
	var keyType, draftType, partialType string
	var target *irv1.QualifiedName
	for _, v := range u.GetVariants() {
		if target == nil {
			target = cmp.Or(v.GetKey(), v.GetPayload()).GetEntity()
		}
		if v.GetKey() != nil {
			keyType = g.shapeType(v.GetKey(), q, what)
		}
		switch v.GetChange() {
		case irv1.RelationChange_RELATION_CHANGE_CREATE:
			draftType = g.shapeType(v.GetPayload(), q, what)
		case irv1.RelationChange_RELATION_CHANGE_UPDATE:
			partialType = g.shapeType(v.GetPayload(), q, what)
		}
	}
	var changes []string
	for _, v := range u.GetVariants() {
		changes = append(changes, strings.TrimPrefix(changeWords[v.GetChange()], "Change"))
	}
	f.printf("// %s holds one change to a related %s: %s (§14.5, §14.6).\ntype %s struct {\n\tchange %s.Change\n",
		name, displayName(target), strings.ToLower(strings.Join(changes, ", ")), name, dql)
	for _, fld := range [][2]string{{"key", keyType}, {"draft", draftType}, {"partial", partialType}} {
		if fld[1] != "" {
			f.printf("\t%s %s\n", fld[0], fld[1])
		}
	}
	r := receiver(u.GetHostName().GetWords())
	f.printf("}\n\n// Change returns the change %s holds.\nfunc (%s %s) Change() %s.Change { return %s.change }\n\n", r, r, name, dql, r)
	for _, v := range u.GetVariants() {
		ctor := Exported(v.GetHostName().GetWords())
		g.declare(ctor, "a constructor of "+what, q)
		var params, sets []string
		if v.GetKey() != nil {
			params = append(params, "k "+keyType)
			sets = append(sets, "key: k")
		}
		switch v.GetChange() {
		case irv1.RelationChange_RELATION_CHANGE_CREATE:
			params = append(params, "d "+draftType)
			sets = append(sets, "draft: d")
		case irv1.RelationChange_RELATION_CHANGE_UPDATE:
			params = append(params, "p "+partialType)
			sets = append(sets, "partial: p")
		}
		f.printf("// %s returns the %s that %ss.\nfunc %s(%s) %s {\n\treturn %s{%s}\n}\n\n",
			ctor, name, strings.ToLower(strings.TrimPrefix(changeWords[v.GetChange()], "Change")), ctor, strings.Join(params, ", "), name,
			name, strings.Join(append([]string{"change: " + dql + "." + changeWords[v.GetChange()]}, sets...), ", "))
	}
}
