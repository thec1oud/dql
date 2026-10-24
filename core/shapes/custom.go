package shapes

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/thec1oud/dql/core/diagnostics"
	"github.com/thec1oud/dql/core/naming"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Custom is a custom shape as dql.yaml declares it under shapes: (§14.8). It
// derives from a preset or another custom shape, named in the dotted spelling
// of ShapeId: user.full, user.draft.nested, user_summary. A preset written
// without a form, such as user.draft, is its Flat form.
//
// Every operator names fields of the from shape, so the operators of one
// shape do not depend on each other's order.
type Custom struct {
	Name string
	From string
	// Pick keeps the named fields and drops the others; Omit drops the named
	// ones. Both may name columns and, in a shape with relation fields,
	// relations.
	Pick, Omit []string
	// Require and Optionalize change the presence of columns of write shapes.
	Require, Optionalize []string
	// Nullify and Nonnull change the nullability of columns. A narrowing is
	// enforced when decoding (§13).
	Nullify, Nonnull []string
	// Bind takes columns of a write shape out of the payload; generated
	// functions take them as parameters.
	Bind []string
	// Nest maps a relation the entity owns to the shape it takes: a preset or
	// a custom shape of the related entity, of the same tier.
	Nest map[string]string
}

// WriteNester builds the relation fields of write shapes by the reference
// and change rules of §14.5 and §14.6, which are the same for presets and
// custom shapes.
type WriteNester interface {
	// NestWrite adds to shape the relation field for rel, owned by the
	// shape's entity, taking target, the related or child entity's write
	// shape of the shape's tier. It appends or replaces the Nest, or forbids a
	// bound owning relation, and adjusts foreign-key fields. It returns the
	// relation-qualified child shapes and the Ref, Link and change unions the
	// field needs, or a diagnostic when the relation cannot be nested.
	NestWrite(shape *irv1.Shape, rel *irv1.Relation, target *irv1.Shape) (added []*irv1.Shape, unions []*irv1.RelationUnion, diag *irv1.Diagnostic)
}

// Diagnostic codes of custom shapes. They are stable: tools and tests match
// on them.
const (
	CodeWriteShapeIncomplete    = "write-shape-incomplete"
	CodeShapeNameInvalid        = "shape-name-invalid"
	CodeShapeNameCollision      = "shape-name-collision"
	CodeShapeUnknown            = "shape-unknown"
	CodeShapeCycle              = "shape-cycle"
	CodeShapeFieldUnknown       = "shape-field-unknown"
	CodeShapeFieldForbidden     = "shape-field-forbidden"
	CodeShapeOperatorNotAllowed = "shape-operator-not-allowed"
	CodeShapeOperatorConflict   = "shape-operator-conflict"
	CodeShapeRelationUnknown    = "shape-relation-unknown"
	CodeShapeNestMismatch       = "shape-nest-mismatch"
)

// DeriveCustom expands every custom shape over presets, the entities' preset
// shapes in every form (§14.3, §14.4), together with the relation-qualified
// child shapes. Catalogs supply the entities' words, column order and
// relations; writes builds the relation fields of nests on write shapes.
//
// It returns the custom shapes in name order, followed by the child shapes
// their write nests added, the unions those nests need, and the diagnostics.
// A shape with an error diagnostic is left out, and so is every shape
// derived from it or nesting it, without diagnostics of their own.
func DeriveCustom(customs []Custom, presets []*irv1.Shape, catalogs []*irv1.Catalog, writes WriteNester) ([]*irv1.Shape, []*irv1.RelationUnion, []*irv1.Diagnostic) {
	d := &customDeriver{
		customs: map[string]*Custom{},
		state:   map[string]customState{},
		done:    map[string]*irv1.Shape{},
		tables:  map[string]*irv1.Table{},
		presets: map[string]*irv1.Shape{},
		writes:  writes,
	}
	for _, c := range catalogs {
		for _, ns := range c.GetNamespaces() {
			for _, t := range ns.GetTables() {
				qn := &irv1.QualifiedName{Catalog: c.GetName().GetStored(), Namespace: ns.GetName().GetStored(), Object: t.GetName().GetStored()}
				d.tables[customQN(qn)] = t
			}
		}
	}
	hostNames := map[string]bool{}
	for _, s := range presets {
		if spelling, ok := d.spell(s.GetId()); ok {
			d.presets[spelling] = s
		}
		hostNames[strings.Join(s.GetHostName().GetWords(), "_")] = true
	}
	var names []string
	for i := range customs {
		c := &customs[i]
		switch {
		case c.Name == "" || c.Name != strings.Join(naming.Words(c.Name), "_"):
			d.errorf(c.Name, CodeShapeNameInvalid, "shape name %q is not lower-case words joined by underscores; rename it, such as user_summary", c.Name)
		case hostNames[c.Name]:
			d.errorf(c.Name, CodeShapeNameCollision, "shape %s has the host name of a preset shape; rename it", c.Name)
		default:
			d.customs[c.Name] = c
			names = append(names, c.Name)
		}
	}
	slices.Sort(names)
	var out []*irv1.Shape
	for _, name := range names {
		if s := d.custom(name); s != nil {
			out = append(out, s)
		}
	}
	diagnostics.Sort(d.diags)
	return append(out, d.added...), d.unions, d.diags
}

type customState uint8

const (
	customUnvisited customState = iota
	customVisiting
	customDone
)

// customDeriver holds the state of one DeriveCustom call.
type customDeriver struct {
	customs map[string]*Custom
	state   map[string]customState
	// done holds each derived custom shape, nil when it failed.
	done    map[string]*irv1.Shape
	stack   []string
	tables  map[string]*irv1.Table // by customQN
	presets map[string]*irv1.Shape // by spelling
	writes  WriteNester
	added   []*irv1.Shape
	unions  []*irv1.RelationUnion
	diags   []*irv1.Diagnostic
}

func (d *customDeriver) errorf(shape, code, format string, args ...any) {
	d.diags = append(d.diags, &irv1.Diagnostic{Severity: irv1.Severity_SEVERITY_ERROR, Code: code, Shape: shape, Message: fmt.Sprintf(format, args...)})
}

// custom returns the custom shape name derived, or nil when it or a shape it
// depends on failed.
func (d *customDeriver) custom(name string) *irv1.Shape {
	switch d.state[name] {
	case customDone:
		return d.done[name]
	case customVisiting:
		cycle := append(slices.Clone(d.stack[slices.Index(d.stack, name):]), name)
		d.errorf(name, CodeShapeCycle, "shape %s depends on itself through %s; break the cycle", name, strings.Join(cycle, " -> "))
		return nil
	}
	d.state[name] = customVisiting
	d.stack = append(d.stack, name)
	s := d.derive(d.customs[name])
	d.stack = d.stack[:len(d.stack)-1]
	d.state[name] = customDone
	d.done[name] = s
	return s
}

// resolve returns the shape a reference names, or nil. Unknown references
// are reported as written in the field of shape that holds them.
func (d *customDeriver) resolve(shape, field, ref string) *irv1.Shape {
	if _, ok := d.customs[ref]; ok {
		return d.custom(ref)
	}
	if spelling, ok := customParse(ref); ok {
		if s := d.presets[spelling]; s != nil {
			return s
		}
	}
	d.errorf(shape, CodeShapeUnknown, "shape %s: %s names %q, which is neither a preset nor a shape under shapes:; name a preset such as user.full or a custom shape", shape, field, ref)
	return nil
}

// derive expands c, or reports why it cannot and returns nil.
func (d *customDeriver) derive(c *Custom) *irv1.Shape {
	src := d.resolve(c.Name, "from", c.From)
	if src == nil {
		return nil
	}
	n := len(d.diags)
	tier := src.GetLineage()[0].GetPreset()
	write := tier == irv1.Preset_PRESET_DRAFT || tier == irv1.Preset_PRESET_PARTIAL
	t := d.tables[customQN(src.GetEntity())]
	ops := []customOp{
		{irv1.ShapeOp_PICK, "pick", c.Pick, true, true},
		{irv1.ShapeOp_OMIT, "omit", c.Omit, true, true},
		{irv1.ShapeOp_REQUIRE, "require", c.Require, write, false},
		{irv1.ShapeOp_OPTIONALIZE, "optionalize", c.Optionalize, write, false},
		{irv1.ShapeOp_NULLIFY, "nullify", c.Nullify, true, false},
		{irv1.ShapeOp_NONNULL, "nonnull", c.Nonnull, true, false},
		{irv1.ShapeOp_BIND, "bind", c.Bind, write, false},
	}
	fields := map[string]*irv1.Field{}
	for _, f := range src.GetFields() {
		fields[f.GetColumn()] = f
	}
	nests := map[string]*irv1.Nest{}
	for _, nest := range src.GetNests() {
		nests[nest.GetRelation()] = nest
	}
	named := map[string]string{} // field to the first of pick, omit, bind naming it
	for _, op := range ops {
		if len(op.names) > 0 && !op.allowed {
			d.errorf(c.Name, CodeShapeOperatorNotAllowed, "shape %s: %s applies to write shapes only, and %s derives from %s; remove it", c.Name, op.key, c.Name, c.From)
			continue
		}
		seen := map[string]bool{}
		for _, name := range op.names {
			if seen[name] {
				d.errorf(c.Name, CodeShapeOperatorConflict, "shape %s: %s names %s twice; name it once", c.Name, op.key, name)
				continue
			}
			seen[name] = true
			if fields[name] == nil && (!op.relations || nests[name] == nil) {
				d.unavailable(c, src, op.key, name)
				continue
			}
			if op.op == irv1.ShapeOp_PICK || op.op == irv1.ShapeOp_OMIT || op.op == irv1.ShapeOp_BIND {
				if prev, ok := named[name]; ok {
					d.errorf(c.Name, CodeShapeOperatorConflict, "shape %s: %s and %s both name %s; keep one", c.Name, prev, op.key, name)
				}
				named[name] = op.key
			}
		}
	}
	dropped := func(name string) bool {
		return (len(c.Pick) > 0 && !slices.Contains(c.Pick, name)) || slices.Contains(c.Omit, name) || (write && slices.Contains(c.Bind, name))
	}
	for _, pair := range [][2]customOp{{ops[2], ops[3]}, {ops[4], ops[5]}} {
		for _, name := range pair[1].names {
			if slices.Contains(pair[0].names, name) {
				d.errorf(c.Name, CodeShapeOperatorConflict, "shape %s: %s and %s both name %s; keep one", c.Name, pair[0].key, pair[1].key, name)
			}
		}
	}
	for _, op := range ops[2:6] {
		for _, name := range op.names {
			if fields[name] != nil && dropped(name) {
				d.errorf(c.Name, CodeShapeOperatorConflict, "shape %s: %s names %s, which the shape drops or binds; keep the field or remove it from %s", c.Name, op.key, name, op.key)
			}
		}
	}
	relations := slices.Sorted(maps.Keys(c.Nest))
	if len(relations) > 0 && tier != irv1.Preset_PRESET_FULL && !write {
		d.errorf(c.Name, CodeShapeOperatorNotAllowed, "shape %s: nest applies to Full, Draft and Partial shapes, and %s derives from %s; remove it", c.Name, c.Name, c.From)
		relations = nil
	}
	targets := map[string]*irv1.Shape{}
	rels := map[string]*irv1.Relation{}
	for _, name := range relations {
		if prev, ok := named[name]; ok {
			d.errorf(c.Name, CodeShapeOperatorConflict, "shape %s: %s and nest both name %s; keep one", c.Name, prev, name)
			continue
		}
		rel := customRelation(t, name)
		if rel == nil {
			d.errorf(c.Name, CodeShapeRelationUnknown, "shape %s: nest names %s, which is not a relation of %s; %s", c.Name, name, customName(src.GetEntity()), customRelationNames(t))
			continue
		}
		target := d.resolve(c.Name, "nest."+name, c.Nest[name])
		if target == nil {
			continue
		}
		if !proto.Equal(target.GetEntity(), rel.GetTo().GetTable()) {
			d.errorf(c.Name, CodeShapeNestMismatch, "shape %s: relation %s leads to %s, but %s is a shape of %s; nest a shape of %s", c.Name, name, customName(rel.GetTo().GetTable()), c.Nest[name], customName(target.GetEntity()), customName(rel.GetTo().GetTable()))
			continue
		}
		if tt := target.GetLineage()[0].GetPreset(); tt != tier {
			d.errorf(c.Name, CodeShapeNestMismatch, "shape %s: a %s shape cannot nest %s, a %s shape; nest a %s shape of %s", c.Name, customTier(tier), c.Nest[name], customTier(tt), customTier(tier), customName(target.GetEntity()))
			continue
		}
		rels[name], targets[name] = rel, target
	}
	if len(d.diags) > n {
		return nil
	}

	out := proto.Clone(src).(*irv1.Shape)
	out.Id = &irv1.ShapeId{Base: &irv1.ShapeId_Custom{Custom: c.Name}}
	out.HostName = &irv1.Identifier{Words: naming.Words(c.Name)}
	if src.GetId().GetCustom() != "" {
		out.Lineage = append(out.Lineage, &irv1.LineageStep{Op: irv1.ShapeOp_FROM, Shape: proto.Clone(src.GetId()).(*irv1.ShapeId)})
	}
	for _, op := range ops {
		if len(op.names) > 0 {
			out.Lineage = append(out.Lineage, &irv1.LineageStep{Op: op.op, Columns: slices.Clone(op.names)})
		}
	}
	for _, name := range relations {
		out.Lineage = append(out.Lineage, &irv1.LineageStep{Op: irv1.ShapeOp_NEST, Relation: name, Shape: proto.Clone(targets[name].GetId()).(*irv1.ShapeId)})
	}

	// Relation fields first: a dropped owning relation leaves its foreign-key
	// columns to supply the key alone, with the presence of the Flat form.
	presence := map[string]irv1.Presence{}
	for _, f := range src.GetFields() {
		presence[f.GetColumn()] = f.GetPresence()
	}
	var kept []*irv1.Nest
	for _, nest := range out.GetNests() {
		if !dropped(nest.GetRelation()) {
			kept = append(kept, nest)
			continue
		}
		out.Forbidden = append(out.Forbidden, &irv1.ForbiddenField{Relation: nest.GetRelation(), Reason: irv1.ForbiddenReason_FORBIDDEN_OMITTED})
		if flat := d.flat(src); flat != nil {
			for _, f := range flat.GetFields() {
				if slices.Contains(nest.GetSupplies(), f.GetColumn()) && presence[f.GetColumn()] != irv1.Presence_PRESENCE_REQUIRED {
					presence[f.GetColumn()] = f.GetPresence()
				}
			}
		}
	}
	out.Nests = kept

	var keptFields []*irv1.Field
	for _, f := range out.GetFields() {
		col := f.GetColumn()
		switch {
		case slices.Contains(c.Bind, col):
			out.Bind = append(out.Bind, col)
		case dropped(col):
			out.Forbidden = append(out.Forbidden, &irv1.ForbiddenField{Column: col, Reason: irv1.ForbiddenReason_FORBIDDEN_OMITTED})
			if tier == irv1.Preset_PRESET_DRAFT && presence[col] == irv1.Presence_PRESENCE_REQUIRED {
				d.errorf(c.Name, CodeWriteShapeIncomplete, "shape derived from %s drops required field %s; keep it or bind it", c.From, col)
			}
		default:
			f.Presence = presence[col]
			keptFields = append(keptFields, f)
		}
	}
	out.Fields = keptFields
	if len(d.diags) > n {
		return nil
	}

	// A kept relation whose foreign-key columns left the payload is now the
	// only one that can supply them: required when the key is NOT NULL, and
	// no longer when a bound parameter supplies it.
	for _, nest := range out.GetNests() {
		before := len(nest.GetSupplies())
		var bound bool
		nest.Supplies = slices.DeleteFunc(nest.Supplies, func(col string) bool {
			bound = bound || slices.Contains(out.GetBind(), col)
			return dropped(col)
		})
		if before > 0 && len(nest.GetSupplies()) == 0 && nest.GetNeedsOne() {
			nest.NeedsOne = false
			if !bound {
				nest.Presence = irv1.Presence_PRESENCE_REQUIRED
			}
		}
	}

	for _, f := range out.GetFields() {
		col := f.GetColumn()
		switch {
		case slices.Contains(c.Require, col):
			f.Presence = irv1.Presence_PRESENCE_REQUIRED
		case slices.Contains(c.Optionalize, col):
			f.Presence = irv1.Presence_PRESENCE_OPTIONAL
		}
		if want := slices.Contains(c.Nullify, col); (want || slices.Contains(c.Nonnull, col)) && f.GetNullable() != want {
			f.Nullable = want
			f.NullabilitySource = irv1.NullabilitySource_NULLABILITY_SOURCE_SHAPE_OPERATOR
			f.NullabilityShape = proto.Clone(out.GetId()).(*irv1.ShapeId)
		}
	}

	for _, name := range relations {
		out.Forbidden = slices.DeleteFunc(out.Forbidden, func(f *irv1.ForbiddenField) bool { return f.GetRelation() == name })
		if !write {
			customSetNest(out, &irv1.Nest{
				Relation: name,
				Presence: irv1.Presence_PRESENCE_REQUIRED,
				Nullable: rels[name].GetOptional() && customToOne(rels[name].GetKind()),
				Target:   &irv1.Nest_Shape{Shape: proto.Clone(targets[name].GetId()).(*irv1.ShapeId)},
			})
			continue
		}
		added, unions, diag := d.writes.NestWrite(out, rels[name], targets[name])
		if diag != nil {
			d.diags = append(d.diags, diag)
			return nil
		}
		d.added = append(d.added, added...)
		d.unions = append(d.unions, unions...)
	}

	order := map[string]uint32{}
	for _, col := range t.GetColumns() {
		order[col.GetName().GetStored()] = col.GetOrdinal()
	}
	slices.SortStableFunc(out.Forbidden, func(a, b *irv1.ForbiddenField) int {
		return cmp.Or(
			cmp.Compare(min(len(a.GetRelation()), 1), min(len(b.GetRelation()), 1)),
			cmp.Compare(order[a.GetColumn()], order[b.GetColumn()]),
			strings.Compare(a.GetRelation(), b.GetRelation()),
		)
	})
	slices.SortFunc(out.Bind, func(a, b string) int { return cmp.Compare(order[a], order[b]) })
	return out
}

// customOp is one operator of a custom shape as derive checks it.
type customOp struct {
	op    irv1.ShapeOp
	key   string
	names []string
	// allowed reports whether the operator applies to the from shape's tier.
	allowed bool
	// relations reports whether the operator may name relation fields.
	relations bool
}

// unavailable reports a name an operator of c gives that src has no field
// for.
func (d *customDeriver) unavailable(c *Custom, src *irv1.Shape, key, name string) {
	for _, f := range src.GetForbidden() {
		if f.GetColumn() == name || f.GetRelation() == name {
			reason := strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(f.GetReason().String(), "FORBIDDEN_")), "_", "-")
			d.errorf(c.Name, CodeShapeFieldForbidden, "shape %s: %s names %s, which %s leaves out (%s); remove it from %s", c.Name, key, name, c.From, reason, key)
			return
		}
	}
	for _, nest := range src.GetNests() {
		if nest.GetRelation() == name {
			d.errorf(c.Name, CodeShapeFieldUnknown, "shape %s: %s names relation %s, and %s applies to columns only; remove it from %s", c.Name, key, name, key, key)
			return
		}
	}
	if slices.Contains(src.GetBind(), name) {
		d.errorf(c.Name, CodeShapeFieldForbidden, "shape %s: %s names %s, which %s binds; remove it from %s", c.Name, key, name, c.From, key)
		return
	}
	d.errorf(c.Name, CodeShapeFieldUnknown, "shape %s: %s names %s, which is not a field of %s (%s); use the names of its fields", c.Name, key, name, c.From, customName(src.GetEntity()))
}

// flat returns the Flat form of the preset src derives from, or nil.
func (d *customDeriver) flat(src *irv1.Shape) *irv1.Shape {
	root := src.GetLineage()[0]
	id := &irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: root.GetEntity()}, Preset: root.GetPreset(), Form: irv1.Form_FORM_FLAT}
	for _, step := range src.GetLineage() {
		if step.GetOp() == irv1.ShapeOp_FOR_RELATION {
			id.ForRelation = step.GetRelation()
		}
	}
	spelling, _ := d.spell(id)
	return d.presets[spelling]
}

// spell returns the dotted spelling of a preset shape's id, as ShapeId
// documents it, or false when its entity is not in the catalogs.
func (d *customDeriver) spell(id *irv1.ShapeId) (string, bool) {
	t := d.tables[customQN(id.GetEntity())]
	if t == nil {
		return "", false
	}
	parts := []string{strings.Join(t.GetHostName().GetWords(), "_"), strings.ToLower(strings.TrimPrefix(id.GetPreset().String(), "PRESET_"))}
	if id.GetForm() != irv1.Form_FORM_UNSPECIFIED {
		parts = append(parts, strings.ToLower(strings.TrimPrefix(id.GetForm().String(), "FORM_")))
	}
	if id.GetForRelation() != "" {
		parts = append(parts, "for", id.GetForRelation())
	}
	return strings.Join(parts, "."), true
}

// customParse turns a preset reference as dql.yaml writes it into the
// dotted spelling spell gives the preset, adding the Flat form a Full, Draft
// or Partial reference leaves out. It returns false for anything that cannot
// spell a preset.
func customParse(ref string) (string, bool) {
	parts := strings.Split(ref, ".")
	if len(parts) < 2 || parts[0] == "" {
		return "", false
	}
	formed := false
	switch parts[1] {
	case "full", "draft", "partial":
		formed = true
	case "key", "scope":
	default:
		return "", false
	}
	rest := parts[2:]
	if formed {
		form := "flat"
		if len(rest) > 0 && (rest[0] == "flat" || rest[0] == "nests" || rest[0] == "nested") {
			form, rest = rest[0], rest[1:]
		}
		parts = append(parts[:2:2], form)
	} else {
		parts = parts[:2]
	}
	switch {
	case len(rest) == 0:
	case len(rest) == 2 && rest[0] == "for" && rest[1] != "":
		parts = append(parts, rest...)
	default:
		return "", false
	}
	return strings.Join(parts, "."), true
}

// customSetNest puts nest into s's relation fields, which are sorted by
// relation, replacing the field of the same relation.
func customSetNest(s *irv1.Shape, nest *irv1.Nest) {
	i, found := slices.BinarySearchFunc(s.Nests, nest.GetRelation(), func(n *irv1.Nest, rel string) int { return strings.Compare(n.GetRelation(), rel) })
	if found {
		s.Nests[i] = nest
		return
	}
	s.Nests = slices.Insert(s.Nests, i, nest)
}

func customRelation(t *irv1.Table, name string) *irv1.Relation {
	for _, r := range t.GetRelations() {
		if r.GetName().GetStored() == name {
			return r
		}
	}
	return nil
}

func customRelationNames(t *irv1.Table) string {
	var names []string
	for _, r := range t.GetRelations() {
		names = append(names, r.GetName().GetStored())
	}
	if len(names) == 0 {
		return "it has no relations, so remove the entry"
	}
	return "name one of " + strings.Join(names, ", ")
}

func customToOne(k irv1.RelationKind) bool {
	return k == irv1.RelationKind_RELATION_KIND_MANY_TO_ONE || k == irv1.RelationKind_RELATION_KIND_ONE_TO_ONE || k == irv1.RelationKind_RELATION_KIND_INVERSE_TO_ONE
}

// customTier names a preset as messages do: Full, Draft.
func customTier(p irv1.Preset) string {
	s := strings.TrimPrefix(p.String(), "PRESET_")
	return s[:1] + strings.ToLower(s[1:])
}

// customQN is a map key for an object's qualified name.
func customQN(qn *irv1.QualifiedName) string {
	return qn.GetCatalog() + "\x00" + qn.GetNamespace() + "\x00" + qn.GetObject()
}

// customName renders an object's qualified name for messages.
func customName(qn *irv1.QualifiedName) string {
	return qn.GetCatalog() + "." + qn.GetNamespace() + "." + qn.GetObject()
}
