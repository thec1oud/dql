package gen

import (
	"fmt"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// nestedInsert is how InsertNested writes an entity's Nested Draft and
// assembles its Full Nested (§14.5, §20.5).
type nestedInsert struct {
	draft, full *irv1.Shape
	insert      entry
	steps       []*nestStep
	// scope is the scope of the store the method is on, nil for none.
	scope *irv1.Shape
}

// nestStep is one relation of the Full Nested result: what the Nested Draft
// writes for it, if anything, and how its value is read back.
type nestStep struct {
	r    *irv1.Relation
	to   *entity
	full *irv1.Nest
	// draft is the Nested Draft's nest, nil for a relation the Draft leaves
	// out, such as an owning relation the scope supplies.
	draft *irv1.Nest
	// via and own, far are a many-to-many relation's junction and its
	// relations to the entity and to the far entity.
	via      *entity
	own, far *irv1.Relation
	// v is the local variable collecting the step's result.
	v string
}

func (s *nestStep) owning() bool {
	k := s.r.GetKind()
	return k == irv1.RelationKind_RELATION_KIND_MANY_TO_ONE || k == irv1.RelationKind_RELATION_KIND_ONE_TO_ONE
}

func (s *nestStep) dependent() bool {
	k := s.r.GetKind()
	return k == irv1.RelationKind_RELATION_KIND_ONE_TO_MANY || k == irv1.RelationKind_RELATION_KIND_INVERSE_TO_ONE
}

// nestedSignature is InsertNested's signature.
func (g *generator) nestedSignature(e *entity, p *nestedInsert) signature {
	return signature{
		name:    "InsertNested",
		params:  "d " + Exported(p.draft.GetHostName().GetWords()) + ", as ..." + e.f.use(pkgDQL, "dql") + ".Assignment[" + e.tableType() + "]",
		results: "(" + Exported(p.full.GetHostName().GetWords()) + ", error)",
		x:       p.insert,
		nested:  p,
	}
}

// scopedStore generates, once, the store of e, an entity without a scope,
// on the handles of scope group sg, when e's nested writes reach scoped
// entities, which only such a handle can write (§21.1): an interface adding
// them to e's store, and its implementation. ok is false when there are
// none, and e's store is the same on every handle.
func (g *generator) scopedStore(e *entity, sg *scopeGroup) (iface, impl string, ok bool) {
	iface, impl = e.plural+sg.typ+"dStore", e.lower()+sg.typ+"dWrites"
	if done, seen := g.scopedStores[iface]; seen {
		return iface, impl, done
	}
	var sigs []signature
	if p, ok := g.nestedInsert(e, sg.shape); ok && scopedSteps(p.steps) {
		sigs = append(sigs, g.nestedSignature(e, p))
	}
	if p, ok := g.nestedPatch(e, sg.shape); ok && scopedSteps(p.steps) {
		sigs = append(sigs, g.nestedPatchSignature(e, p))
	}
	g.scopedStores[iface] = len(sigs) > 0
	if len(sigs) == 0 {
		return iface, impl, false
	}
	f := e.f
	ctx := f.use("context", "context")
	what := displayName(e.q)
	g.declare(iface, "the store interface "+iface, e.q)
	g.declare(impl, "the scoped store of "+what, e.q)
	f.printf("// %s reads and writes %s on a handle with scope %s, whose nested\n// writes reach scoped entities (§21.1, §26.3).\ntype %s interface {\n\t%s\n", iface, what, sg.typ, iface, e.store())
	for _, sig := range sigs {
		f.printf("\t%s(ctx %s.Context, %s) %s\n", sig.name, ctx, sig.params, sig.results)
	}
	f.printf("}\n\n// %s implements %s.\ntype %s struct {\n\t%sWrites\n\tscope %s\n}\n\n", impl, iface, impl, e.lower(), sg.typ)
	for _, sig := range sigs {
		f.printf("// %s runs %s%s (§20.5).\nfunc (s %s) %s(ctx %s.Context, %s) %s {\n", sig.name, sig.x.q.GetName().GetStored(), sig.doc(), impl, sig.name, ctx, sig.params, sig.results)
		g.methodBody(e, sig)
		f.printf("}\n\n")
	}
	return iface, impl, true
}

// preset returns an entity's preset shape.
func (g *generator) preset(q *irv1.QualifiedName, p irv1.Preset, form irv1.Form) *irv1.Shape {
	return g.ix.shapes[shapeKey(&irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: q}, Preset: p, Form: form})]
}

// nestedInsert plans e's InsertNested, when every relation of its Full
// Nested, which has every relation its Nested Draft has (§14.4, §14.5), is
// one it writes and reads back: entities with the entries the steps run,
// and scopes sc, the scope of the store it is on, covers (§21.1).
// Otherwise that store gets no InsertNested.
func (g *generator) nestedInsert(e *entity, sc *irv1.Shape) (*nestedInsert, bool) {
	es := g.byTable
	insert, ok := e.entry("insert")
	p := &nestedInsert{
		draft:  g.preset(e.q, irv1.Preset_PRESET_DRAFT, irv1.Form_FORM_NESTED),
		full:   g.preset(e.q, irv1.Preset_PRESET_FULL, irv1.Form_FORM_NESTED),
		insert: insert,
		scope:  sc,
	}
	if !ok || p.draft == nil || p.full == nil || len(p.draft.GetNests()) == 0 || g.embedded(p.full) == nil || g.embeddedNests(p.full) == nil {
		return nil, false
	}
	for _, n := range p.full.GetNests() {
		r := relation(e.t, n.GetRelation())
		to := es[keyOf(r.GetTo().GetTable())]
		s := &nestStep{r: r, to: to, full: n, draft: shapeNest(p.draft, n.GetRelation()), v: "created" + Exported(r.GetName().GetWords())}
		if to == nil || to == e || !reachable(sc, to) || !proto.Equal(n.GetShape(), to.full.GetId()) {
			return nil, false
		}
		switch {
		case s.owning():
			if !g.owningStep(s) {
				return nil, false
			}
		case s.dependent():
			if !g.dependentStep(s) {
				return nil, false
			}
		default:
			if !g.linkStep(e, s, sc, es) {
				return nil, false
			}
		}
		p.steps = append(p.steps, s)
	}
	// A link's junction row joins the junction relation's rows.
	for _, s := range p.steps {
		if s.via != nil && !slices.ContainsFunc(p.steps, func(d *nestStep) bool { return d.dependent() && toMany(d.r) && d.to == s.via }) {
			return nil, false
		}
	}
	return p, true
}

// reachable reports whether a store with scope sc can write and read to's
// rows: to has no scope, or sc has its bound columns.
func reachable(sc *irv1.Shape, to *entity) bool {
	if to.scope == nil {
		return true
	}
	if sc == nil {
		return false
	}
	for _, fld := range to.scope.GetFields() {
		if shapeField(sc, fld.GetColumn()) == nil || !proto.Equal(shapeField(sc, fld.GetColumn()).GetType(), fld.GetType()) {
			return false
		}
	}
	return true
}

// keyed reports whether to's Get can be called with the key columns that
// cols of from name, in order: every column of to's Key but its expected
// version.
func keyed(to *entity, cols []string) bool {
	if _, ok := to.entries[to.entryName("get", "")]; !ok {
		return false
	}
	for _, fld := range to.key.GetFields() {
		if fld.GetRole() != irv1.FieldRole_FIELD_ROLE_EXPECTED_VERSION && !slices.Contains(cols, fld.GetColumn()) {
			return false
		}
	}
	return true
}

// presetUnion reports whether InsertNested can write a Draft nest's
// references to to: never null, and a Create only where to inserts. A
// preset's union Links by to's Key and Creates its Draft (§14.5).
func (g *generator) presetUnion(n *irv1.Nest, to *entity) bool {
	if n.GetNullable() {
		return false
	}
	for _, v := range g.ix.unions[unionKey(n.GetUnion())].GetVariants() {
		if v.GetChange() == irv1.RelationChange_RELATION_CHANGE_CREATE && !to.inserts() {
			return false
		}
	}
	return true
}

// entry returns e's entry of verb by its primary key, or of a verb that
// names no key, as user.patch.
func (e *entity) entry(verb string) (entry, bool) {
	if x, ok := e.entries[e.entryName(verb, "")]; ok {
		return x, true
	}
	x, ok := e.entries[strings.Join(e.t.GetHostName().GetWords(), "_")+"."+verb]
	return x, ok
}

func (e *entity) has(verbs ...string) bool {
	for _, v := range verbs {
		if _, ok := e.entry(v); !ok {
			return false
		}
	}
	return true
}

func (e *entity) inserts() bool { return e.has("insert") }

// owningStep checks an owning relation: a Draft nest, if any, Links or
// Creates the related row, whose foreign key the flat Draft holds, and the
// related row is read back by the foreign key.
func (g *generator) owningStep(s *nestStep) bool {
	if !keyed(s.to, s.r.GetTo().GetColumns()) {
		return false
	}
	if s.draft == nil {
		return true
	}
	return g.presetUnion(s.draft, s.to)
}

// dependentStep checks a dependent relation: a Draft nest, if any, is the
// child's flat Draft without the foreign key to the parent, which the
// parent's row fills.
func (g *generator) dependentStep(s *nestStep) bool {
	if !s.to.inserts() {
		return false
	}
	if s.draft == nil {
		return true
	}
	// A preset child is the child's flat Draft for the relation.
	return !s.draft.GetNullable() && g.fills(s.to.draft, g.ix.shapes[shapeKey(s.draft.GetShape())], s.r.GetTo().GetColumns())
}

// fills reports whether src fills every field of the flat Draft dst but
// fk, which the caller fills: each field src leaves out is optional in
// dst, and none is optional in src and required in dst.
func (g *generator) fills(dst, src *irv1.Shape, fk []string) bool {
	for _, fld := range dst.GetFields() {
		if slices.Contains(fk, fld.GetColumn()) {
			continue
		}
		sf := shapeField(src, fld.GetColumn())
		switch {
		case sf == nil && fld.GetPresence() != irv1.Presence_PRESENCE_OPTIONAL:
			return false
		case sf != nil && sf.GetPresence() == irv1.Presence_PRESENCE_OPTIONAL && fld.GetPresence() != irv1.Presence_PRESENCE_OPTIONAL:
			return false
		}
	}
	return true
}

// linkStep checks a many-to-many relation: a Draft nest, if any, Links or
// Creates far rows, and each writes a junction row holding nothing but the
// two foreign keys.
func (g *generator) linkStep(e *entity, s *nestStep, sc *irv1.Shape, es map[key]*entity) bool {
	s.via = es[keyOf(s.r.GetThrough())]
	if s.via == nil || !s.via.inserts() || !reachable(sc, s.via) {
		return false
	}
	for _, r := range s.via.t.GetRelations() {
		owning := r.GetKind() == irv1.RelationKind_RELATION_KIND_MANY_TO_ONE
		switch to := keyOf(r.GetTo().GetTable()); {
		case owning && to == keyOf(e.q):
			s.own = r
		case owning && to == keyOf(s.to.q):
			s.far = r
		}
	}
	if s.own == nil || s.far == nil || !keyed(s.to, s.far.GetTo().GetColumns()) {
		return false
	}
	if s.draft == nil {
		return true
	}
	return g.presetUnion(s.draft, s.to) &&
		g.fills(s.via.draft, &irv1.Shape{}, slices.Concat(s.own.GetFrom().GetColumns(), s.far.GetFrom().GetColumns()))
}

// storeIn is e's store on the transaction's writer w, inside a method of a
// store with scope sc: its scope taken from that store's.
func (g *generator) storeIn(sc *irv1.Shape, e *entity) string {
	lower := e.lower()
	reads := lower + "Reads{w"
	if e.scope != nil {
		var fields []string
		for _, fld := range e.scope.GetFields() {
			fields = append(fields, fmt.Sprintf("%s: s.scope.%s", g.fieldName(e.scope, fld.GetColumn()), g.fieldName(sc, fld.GetColumn())))
		}
		reads += ", " + Exported(e.scope.GetHostName().GetWords()) + "{" + strings.Join(fields, ", ") + "}"
	}
	return lower + "Writes{" + reads + "}, w}"
}

// value converts the value expression v, never absent and nullable or
// not, to a field's to.
func value(f *file, v string, to *irv1.Field, nullable bool) string {
	if to.GetNullable() && !nullable {
		v = f.use(pkgDQL, "dql") + ".Ptr(" + v + ")"
	}
	if to.GetPresence() == irv1.Presence_PRESENCE_OPTIONAL {
		v = f.use(pkgOpt, "opt") + ".Some(" + v + ")"
	}
	return v
}

// copyFields returns the elements of a literal of the flat Draft dst
// filled from src's value sv, leaving out fk.
func (g *generator) copyFields(f *file, dst, src *irv1.Shape, sv string, fk []string) []string {
	var out []string
	for _, fld := range dst.GetFields() {
		sf := shapeField(src, fld.GetColumn())
		if slices.Contains(fk, fld.GetColumn()) || sf == nil {
			continue
		}
		v := sv + "." + g.fieldName(src, fld.GetColumn())
		if sf.GetPresence() != fld.GetPresence() {
			v = f.use(pkgOpt, "opt") + ".Some(" + v + ")"
		}
		out = append(out, g.fieldName(dst, fld.GetColumn())+": "+v)
	}
	return out
}

// insertNested writes InsertNested: it validates the whole graph (§20.3),
// then in one transaction inserts owning Creates, the entity and its
// dependents, then reads back the related rows the Full Nested holds
// (§20.5).
func (g *generator) insertNested(e *entity, s signature) {
	f := e.f
	dql := f.use(pkgDQL, "dql")
	ctx := f.use("context", "context")
	p := s.nested
	fullNested := Exported(p.full.GetHostName().GetWords())
	full := Exported(e.full.GetHostName().GetWords())
	draft := Exported(e.draft.GetHostName().GetWords())
	f.printf("\tif err := d.Validate(); err != nil {\n\t\treturn %s{}, err\n\t}\n", fullNested)
	f.printf("\tvar out %s\n\terr := %s.Atomic(ctx, s.w, func(ctx %s.Context, w %s.Writer) error {\n", fullNested, dql, ctx, dql)
	f.printf("\t\tout = %s{}\n", fullNested)
	stores := map[*entity]string{}
	store := func(x *entity) string {
		if v, ok := stores[x]; ok {
			return v
		}
		v := x.lower() + "Store"
		stores[x] = v
		f.printf("\t\t%s := %s\n", v, g.storeIn(p.scope, x))
		return v
	}
	self := store(e)
	for _, st := range p.steps {
		switch {
		case st.owning(), st.draft != nil:
			store(st.to)
		case !st.dependent() && slices.ContainsFunc(p.steps, func(d *nestStep) bool { return d.dependent() && d.draft != nil && d.to == st.via }):
			store(st.to)
		}
		if st.via != nil && st.draft != nil {
			store(st.via)
		}
	}
	// The flat Draft, its foreign keys filled by owning relations.
	var fk []string
	for _, st := range p.steps {
		if st.owning() && st.draft != nil {
			fk = append(fk, st.r.GetFrom().GetColumns()...)
		}
	}
	f.printf("\t\tdraft := %s{%s}\n", draft, strings.Join(g.copyFields(f, e.draft, p.draft, "d", fk), ", "))
	for _, st := range p.steps {
		if !st.owning() || st.draft == nil {
			continue
		}
		f.printf("\t\tvar %s %s.Opt[%s]\n", st.v, f.use(pkgOpt, "opt"), Exported(st.to.full.GetHostName().GetWords()))
		for _, c := range st.r.GetFrom().GetColumns() {
			name := g.fieldName(e.draft, c)
			from := shapeField(p.draft, c)
			v := value(f, "v", shapeField(e.draft, c), from.GetNullable())
			if from.GetPresence() == shapeField(e.draft, c).GetPresence() {
				f.printf("\t\tdraft.%s = d.%s\n", name, name)
				continue
			}
			f.printf("\t\tif v, ok := d.%s.Get(); ok {\n\t\t\tdraft.%s = %s\n\t\t}\n", name, name, v)
		}
		f.printf("\t\tif ref, ok := d.%s.Get(); ok {\n\t\t\tswitch ref.change {\n", Exported(st.r.GetName().GetWords()))
		f.printf("\t\t\tcase %s.ChangeCreate:\n\t\t\t\trow, err := %s.Insert(ctx, ref.draft)\n\t\t\t\tif err != nil {\n\t\t\t\t\treturn err\n\t\t\t\t}\n\t\t\t\t%s = %s.Some(row)\n",
			dql, store(st.to), st.v, f.use(pkgOpt, "opt"))
		g.setForeignKey(f, e, st, "row", st.to.full, "\t\t\t\t")
		f.printf("\t\t\tcase %s.ChangeLink:\n", dql)
		g.setForeignKey(f, e, st, "ref.key", st.to.key, "\t\t\t\t")
		f.printf("\t\t\t}\n\t\t}\n")
	}
	f.printf("\t\trow, err := %s.Insert(ctx, draft, as...)\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n\t\tout.%s = row\n", self, full)
	// Dependents, then many-to-many links, collecting the junction rows
	// and their far rows.
	collected := map[*entity]string{}
	for _, st := range p.steps {
		if st.dependent() && toMany(st.r) {
			f.printf("\t\tout.%s = []%s{}\n", Exported(st.r.GetName().GetWords()), Exported(st.to.full.GetHostName().GetWords()))
			collected[st.to] = "out." + Exported(st.r.GetName().GetWords())
		}
		if !st.owning() && !st.dependent() {
			f.printf("\t\tout.%s = []%s{}\n", Exported(st.r.GetName().GetWords()), Exported(st.to.full.GetHostName().GetWords()))
		}
	}
	for _, st := range p.steps {
		if !st.dependent() || st.draft == nil {
			continue
		}
		child := g.ix.shapes[shapeKey(st.draft.GetShape())]
		lit := g.copyFields(f, st.to.draft, child, "c", st.r.GetTo().GetColumns())
		for i, c := range st.r.GetTo().GetColumns() {
			v := value(f, "row."+g.fieldName(e.full, st.r.GetFrom().GetColumns()[i]), shapeField(st.to.draft, c), false)
			lit = append(lit, g.fieldName(st.to.draft, c)+": "+v)
		}
		name := Exported(st.r.GetName().GetWords())
		if toMany(st.r) {
			f.printf("\t\tfor _, c := range d.%s {\n", name)
		} else {
			f.printf("\t\tif c, ok := d.%s.Get(); ok {\n", name)
		}
		f.printf("\t\t\tchild, err := %s.Insert(ctx, %s{%s})\n\t\t\tif err != nil {\n\t\t\t\treturn err\n\t\t\t}\n", store(st.to), Exported(st.to.draft.GetHostName().GetWords()), strings.Join(lit, ", "))
		if toMany(st.r) {
			f.printf("\t\t\tout.%s = append(out.%s, child)\n", name, name)
		} else {
			f.printf("\t\t\tout.%s = &child\n", name)
		}
		for _, m := range p.steps {
			if !m.owning() && !m.dependent() && m.via == st.to {
				g.readFar(f, m, "child", store(m.to), "\t\t\t")
			}
		}
		f.printf("\t\t}\n")
	}
	for _, st := range p.steps {
		if st.owning() || st.dependent() || st.draft == nil {
			continue
		}
		name := Exported(st.r.GetName().GetWords())
		far := Exported(st.to.full.GetHostName().GetWords())
		f.printf("\t\tfor _, ref := range d.%s {\n\t\t\tvar far %s\n\t\t\tswitch ref.change {\n", name, far)
		f.printf("\t\t\tcase %s.ChangeCreate:\n\t\t\t\tif far, err = %s.Insert(ctx, ref.draft); err != nil {\n\t\t\t\t\treturn err\n\t\t\t\t}\n", dql, store(st.to))
		f.printf("\t\t\tcase %s.ChangeLink:\n\t\t\t\tif far, err = %s.Get(ctx, ref.key); err != nil {\n\t\t\t\t\treturn err\n\t\t\t\t}\n\t\t\t}\n", dql, store(st.to))
		var lit []string
		for i, c := range st.own.GetFrom().GetColumns() {
			v := value(f, "row."+g.fieldName(e.full, st.own.GetTo().GetColumns()[i]), shapeField(st.via.draft, c), false)
			lit = append(lit, g.fieldName(st.via.draft, c)+": "+v)
		}
		for i, c := range st.far.GetFrom().GetColumns() {
			v := value(f, "far."+g.fieldName(st.to.full, st.far.GetTo().GetColumns()[i]), shapeField(st.via.draft, c), false)
			lit = append(lit, g.fieldName(st.via.draft, c)+": "+v)
		}
		f.printf("\t\t\tjunction, err := %s.Insert(ctx, %s{%s})\n\t\t\tif err != nil {\n\t\t\t\treturn err\n\t\t\t}\n", store(st.via), Exported(st.via.draft.GetHostName().GetWords()), strings.Join(lit, ", "))
		f.printf("\t\t\t%s = append(%s, junction)\n", collected[st.via], collected[st.via])
		f.printf("\t\t\tout.%s = append(out.%s, far)\n\t\t}\n", name, name)
	}
	// Owning relations, read by the foreign key unless created.
	for _, st := range p.steps {
		if !st.owning() {
			continue
		}
		name := Exported(st.r.GetName().GetWords())
		var key []string
		var nils []string
		for i, c := range st.r.GetFrom().GetColumns() {
			v := "row." + g.fieldName(e.full, c)
			if shapeField(e.full, c).GetNullable() {
				nils = append(nils, v+" != nil")
				v = "*" + v
			}
			key = append(key, g.fieldName(st.to.key, st.r.GetTo().GetColumns()[i])+": "+v)
		}
		indent := "\t\t\t"
		if len(nils) > 0 {
			f.printf("\t\tif %s {\n", strings.Join(nils, " && "))
		} else {
			f.printf("\t\t{\n")
		}
		get := fmt.Sprintf("%s.Get(ctx, %s{%s})", store(st.to), Exported(st.to.key.GetHostName().GetWords()), strings.Join(key, ", "))
		if st.draft != nil {
			f.printf("%srelated, ok := %s.Get()\n%sif !ok {\n%s\tif related, err = %s; err != nil {\n%s\t\treturn err\n%s\t}\n%s}\n", indent, st.v, indent, indent, get, indent, indent, indent)
		} else {
			f.printf("%srelated, err := %s\n%sif err != nil {\n%s\treturn err\n%s}\n", indent, get, indent, indent, indent)
		}
		if st.full.GetNullable() {
			f.printf("%sout.%s = &related\n", indent, name)
		} else {
			f.printf("%sout.%s = related\n", indent, name)
		}
		f.printf("\t\t}\n")
	}
	f.printf("\t\treturn nil\n\t})\n\treturn out, err\n")
}

// setForeignKey sets the Draft's foreign key of an owning step from v, a
// value of shape from holding the related row's referenced columns.
func (g *generator) setForeignKey(f *file, e *entity, st *nestStep, v string, from *irv1.Shape, indent string) {
	for i, c := range st.r.GetFrom().GetColumns() {
		ref := st.r.GetTo().GetColumns()[i]
		val := value(f, v+"."+g.fieldName(from, ref), shapeField(e.draft, c), false)
		f.printf("%sdraft.%s = %s\n", indent, g.fieldName(e.draft, c), val)
	}
}

// readFar appends to a many-to-many step's result the far row a junction
// row jv refers to. A junction's foreign keys are its key, never null.
func (g *generator) readFar(f *file, m *nestStep, jv, store, indent string) {
	var key []string
	for i, c := range m.far.GetFrom().GetColumns() {
		key = append(key, g.fieldName(m.to.key, m.far.GetTo().GetColumns()[i])+": "+jv+"."+g.fieldName(m.via.full, c))
	}
	name := Exported(m.r.GetName().GetWords())
	f.printf("%sfar, err := %s.Get(ctx, %s{%s})\n%sif err != nil {\n%s\treturn err\n%s}\n%sout.%s = append(out.%s, far)\n",
		indent, store, Exported(m.to.key.GetHostName().GetWords()), strings.Join(key, ", "), indent, indent, indent, indent, name, name)
}
