package gen

import (
	"fmt"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// nestedInsert is how insertNested writes an entity's Nested Draft and
// assembles its Full Nested (§14.5, §20.5).
type nestedInsert struct {
	draft, full *irv1.Shape
	insert      entry
	steps       []*nestStep
	// scope is the scope of the store the method is on, nil for none.
	scope *irv1.Shape
}

// nestStep is one relation of the Full Nested result: what the Nested Draft
// or Partial writes for it, if anything, and how its value is read back.
type nestStep struct {
	r    *irv1.Relation
	to   *entity
	full *irv1.Nest
	// draft is the Nested Draft's or Partial's nest, nil for a relation it
	// leaves out, such as an owning relation the scope supplies.
	draft *irv1.Nest
	// via and own, far are a many-to-many relation's junction and its
	// relations to the entity and to the far entity.
	via      *entity
	own, far *irv1.Relation
}

func (s *nestStep) owning() bool {
	k := s.r.GetKind()
	return k == irv1.RelationKind_RELATION_KIND_MANY_TO_ONE || k == irv1.RelationKind_RELATION_KIND_ONE_TO_ONE
}

func (s *nestStep) dependent() bool {
	k := s.r.GetKind()
	return k == irv1.RelationKind_RELATION_KIND_ONE_TO_MANY || k == irv1.RelationKind_RELATION_KIND_INVERSE_TO_ONE
}

// member is the relation's member in the nested shapes.
func (s *nestStep) member() string { return MemberName(s.r.GetName().GetWords()) }

// result is the local holding the step's part of the Full Nested.
func (s *nestStep) result() string {
	if toMany(s.r) {
		return s.member() + "Rows"
	}
	return s.member() + "Row"
}

// created is the local holding the row an owning Create inserted.
func (s *nestStep) created() string { return "created" + TypeName(s.r.GetName().GetWords()) }

// nestedSignature is insertNested's signature, its types imported into f.
func (g *generator) nestedSignature(f *file, e *entity, p *nestedInsert) signature {
	return signature{
		name: "insertNested",
		params: []param{
			{name: "d", typ: g.ref(f, TypeName(p.draft.GetHostName().GetWords()), false)},
			{name: "as", typ: f.rt("Assignment", false) + "<" + g.ref(f, e.tableType(), false) + ">[]", rest: true},
		},
		result: g.ref(f, TypeName(p.full.GetHostName().GetWords()), false),
		x:      p.insert,
		nested: p,
	}
}

// scopedStores names the store of e, an entity without a scope, on the
// handles of scope group sg, when e's nested writes reach scoped entities,
// which only such a handle can write (§21.1): an interface adding them to
// e's store, and the class implementing it. It generates them the first
// time; ok is false when there are none, and e's store is the same on
// every handle.
func (g *generator) scopedStore(e *entity, sg *scopeGroup) (iface, impl string, ok bool) {
	iface, impl = e.plural+sg.typ+"dStore", e.plural+sg.typ+"dWrites"
	if done, seen := g.scopedStores[iface]; seen {
		return iface, impl, done
	}
	sigs := func(f *file) []signature {
		var out []signature
		if p, ok := g.nestedInsert(e, sg.shape); ok && scopedSteps(p.steps) {
			out = append(out, g.nestedSignature(f, e, p))
		}
		if p, ok := g.nestedPatch(e, sg.shape); ok && scopedSteps(p.steps) {
			out = append(out, g.nestedPatchSignature(f, e, p))
		}
		return out
	}
	f, im := e.f, g.impl
	ifaceSigs := sigs(f)
	g.scopedStores[iface] = len(ifaceSigs) > 0
	if len(ifaceSigs) == 0 {
		return iface, impl, false
	}
	what := displayName(e.q)
	g.declare(f, iface, "the store interface "+iface, e.q)
	f.printf("/** %s reads and writes %s on a handle with scope %s, whose nested writes reach scoped entities (§21.1, §26.3). */\nexport interface %s extends %s {\n",
		iface, what, sg.typ, iface, e.store())
	for _, s := range ifaceSigs {
		f.printf("  %s(%s): Promise<%s>;\n", s.name, s.list(""), s.result)
	}
	f.printf("}\n\n")
	scopeType := g.ref(im, sg.typ, false)
	g.declare(im, impl, "the scoped store of "+what, e.q)
	im.printf("/** %s implements %s on a handle with scope %s. */\nexport class %s extends %s implements %s {\n  readonly scope: %s;\n\n  constructor(w: %s, scope: %s) {\n    super(w);\n    this.scope = scope;\n  }\n",
		impl, iface, sg.typ, impl, e.writer(), g.ref(im, iface, false), scopeType, im.rt("Writer", false), scopeType)
	g.methods(im, e, sigs(im), true)
	im.printf("}\n\n")
	return iface, impl, true
}

// preset returns an entity's preset shape.
func (g *generator) preset(q *irv1.QualifiedName, p irv1.Preset, form irv1.Form) *irv1.Shape {
	return g.ix.shapes[shapeKey(&irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: q}, Preset: p, Form: form})]
}

// nestedInsert plans e's insertNested, when every relation of its Full
// Nested, which has every relation its Nested Draft has (§14.4, §14.5), is
// one it writes and reads back: entities with the entries the steps run,
// and scopes sc, the scope of the store it is on, covers (§21.1).
// Otherwise that store gets no insertNested.
func (g *generator) nestedInsert(e *entity, sc *irv1.Shape) (*nestedInsert, bool) {
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
		s, ok := g.step(e, sc, n, shapeNest(p.draft, n.GetRelation()))
		if !ok {
			return nil, false
		}
		switch {
		case s.owning():
			ok = g.owningStep(e, s)
		case s.dependent():
			ok = g.dependentStep(s)
		default:
			ok = g.linkStep(e, s, sc)
		}
		if !ok {
			return nil, false
		}
		p.steps = append(p.steps, s)
	}
	return p, joined(p.steps)
}

// step starts the step of Full Nested nest n, whose relation leads to an
// entity a store with scope sc reaches and reads as its Full.
func (g *generator) step(e *entity, sc *irv1.Shape, n, draft *irv1.Nest) (*nestStep, bool) {
	r := relation(e.t, n.GetRelation())
	to := g.byTable[keyOf(r.GetTo().GetTable())]
	if to == nil || to == e || !reachable(sc, to) || !proto.Equal(n.GetShape(), to.full.GetId()) {
		return nil, false
	}
	return &nestStep{r: r, to: to, full: n, draft: draft}, true
}

// joined reports whether every many-to-many step's junction rows are a
// dependent relation's rows, which they are read back through.
func joined(steps []*nestStep) bool {
	for _, s := range steps {
		if s.via != nil && !slices.ContainsFunc(steps, func(d *nestStep) bool { return d.dependent() && toMany(d.r) && d.to == s.via }) {
			return false
		}
	}
	return true
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

// keyed reports whether to's get can be called with the key columns that
// cols of from name, in order: every column of to's Key but its expected
// version.
func keyed(to *entity, cols []string) bool {
	if !to.has("get") {
		return false
	}
	for _, fld := range to.key.GetFields() {
		if fld.GetRole() != irv1.FieldRole_FIELD_ROLE_EXPECTED_VERSION && !slices.Contains(cols, fld.GetColumn()) {
			return false
		}
	}
	return true
}

// presetUnion reports whether insertNested can write a Draft nest's
// references to to: never null, and a create only where to inserts. A
// preset's union links by to's Key and creates its Draft (§14.5).
func (g *generator) presetUnion(n *irv1.Nest, to *entity) bool {
	if n.GetNullable() {
		return false
	}
	return !slices.Contains(g.variants(n), irv1.RelationChange_RELATION_CHANGE_CREATE) || to.inserts()
}

// entry returns e's entry of verb by its primary key, or of a verb that
// names no key, as user.patch.
func (e *entity) entry(verb string) (entry, bool) {
	if x, ok := e.entries[e.entryName(verb)]; ok {
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

// owningStep checks an owning relation: a Draft nest, if any, links or
// creates the related row, whose foreign key the flat Draft holds, and the
// related row is read back by the foreign key; a nullable foreign key
// needs a nullable relation to read nothing into.
func (g *generator) owningStep(e *entity, s *nestStep) bool {
	if !keyed(s.to, s.r.GetTo().GetColumns()) || !g.readsBack(e, s) {
		return false
	}
	return s.draft == nil || g.presetUnion(s.draft, s.to)
}

// readsBack reports whether an owning relation's row can be read back by
// the Full's foreign key: the relation is nullable where the key is.
func (g *generator) readsBack(e *entity, s *nestStep) bool {
	return s.full.GetNullable() || !slices.ContainsFunc(s.r.GetFrom().GetColumns(), func(c string) bool { return shapeField(e.full, c).GetNullable() })
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
		case sf == nil && !isOptional(fld):
			return false
		case sf != nil && isOptional(sf) && !isOptional(fld):
			return false
		}
	}
	return true
}

// linkStep checks a many-to-many relation: a Draft nest, if any, links or
// creates far rows, and each writes a junction row holding nothing but the
// two foreign keys.
func (g *generator) linkStep(e *entity, s *nestStep, sc *irv1.Shape) bool {
	s.via = g.byTable[keyOf(s.r.GetThrough())]
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

// storeVar is the local holding e's store in a nested write.
func storeVar(e *entity) string { return e.member + "Store" }

// storeIn is e's store on the transaction's writer w, inside a method of a
// store with scope sc: its scope taken from that store's.
func (g *generator) storeIn(f *file, sc *irv1.Shape, e *entity) string {
	scope := ""
	if e.scope != nil {
		var fields []string
		for _, fld := range e.scope.GetFields() {
			fields = append(fields, fmt.Sprintf("%s: this.scope.%s", g.memberOf(e.scope, fld.GetColumn()), g.memberOf(sc, fld.GetColumn())))
		}
		scope = ", { " + strings.Join(fields, ", ") + " }"
	}
	return fmt.Sprintf("new %s(w%s)", g.ref(f, e.writer(), true), scope)
}

// stores declares the stores a nested write's steps use, each once, in the
// order of need.
type stores struct {
	g    *generator
	f    *file
	sc   *irv1.Shape
	done map[*entity]bool
}

func (s *stores) use(e *entity) string {
	if !s.done[e] {
		s.done[e] = true
		s.f.printf("      const %s = %s;\n", storeVar(e), s.g.storeIn(s.f, s.sc, e))
	}
	return storeVar(e)
}

// spread is a literal of the flat shape dst copying v, a value of shape
// src: its fields, and fk set from vals. A field dst requires and src
// leaves optional, which the caller sets, is asserted present.
func (g *generator) spread(dst, src *irv1.Shape, v string, fk, vals []string) string {
	parts := []string{"..." + v}
	for _, fld := range dst.GetFields() {
		sf := shapeField(src, fld.GetColumn())
		if !slices.Contains(fk, fld.GetColumn()) && sf != nil && isOptional(sf) && !isOptional(fld) {
			parts = append(parts, fmt.Sprintf("%s: %s.%s!", g.memberOf(dst, fld.GetColumn()), v, g.memberOf(src, fld.GetColumn())))
		}
	}
	for i, c := range fk {
		parts = append(parts, g.memberOf(dst, c)+": "+vals[i])
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// keyLit is the literal of to's Key from v, a value of shape src holding
// every key column, the expected version included.
func (g *generator) keyLit(to *entity, v string, src *irv1.Shape) string {
	var fields []string
	for _, fld := range to.key.GetFields() {
		fields = append(fields, g.memberOf(to.key, fld.GetColumn())+": "+v+"."+g.memberOf(src, fld.GetColumn()))
	}
	return "{ " + strings.Join(fields, ", ") + " }"
}

// fkKey is the literal of an owning step's related Key from v's foreign
// key, a value of shape src, and the conditions that one of its nullable
// columns is null.
func (g *generator) fkKey(st *nestStep, v string, src *irv1.Shape) (string, []string) {
	var key, nulls []string
	for i, c := range st.r.GetFrom().GetColumns() {
		x := v + "." + g.memberOf(src, c)
		if shapeField(src, c).GetNullable() {
			nulls = append(nulls, x+" === null")
		}
		key = append(key, g.memberOf(st.to.key, st.r.GetTo().GetColumns()[i])+": "+x)
	}
	return readKey(st.to, key), nulls
}

// readKey is the literal of to's Key holding fields, for a read, which
// ignores the expected version (§14.1): a version of zero fills it.
func readKey(to *entity, fields []string) string {
	for _, fld := range to.key.GetFields() {
		if fld.GetRole() == irv1.FieldRole_FIELD_ROLE_EXPECTED_VERSION {
			zero := "0"
			if fld.GetType().GetInt64() != nil {
				zero = "0n"
			}
			fields = append(fields, MemberName(column(to.t, fld.GetColumn()).GetHostName().GetWords())+": "+zero)
		}
	}
	return "{ " + strings.Join(fields, ", ") + " }"
}

// valuesOf are the fields of v, a value of shape s, holding cols.
func (g *generator) valuesOf(s *irv1.Shape, v string, cols []string) []string {
	var out []string
	for _, c := range cols {
		out = append(out, v+"."+g.memberOf(s, c))
	}
	return out
}

// insertNested writes insertNested: it validates the whole graph (§20.3),
// then in one transaction inserts owning creates, the entity and its
// dependents, then reads back the related rows the Full Nested holds
// (§20.5).
func (g *generator) insertNested(f *file, e *entity, s signature) {
	p := s.nested
	rt := func(name string) string { return f.rt(name, true) }
	f.printf("    %s(%s(d));\n", rt("throwIfIssues"), g.ref(f, validatorName(p.draft.GetHostName().GetWords()), true))
	f.printf("    return %s(this.w, async (w: %s) => {\n", rt("atomic"), f.rt("Writer", false))
	st := &stores{g: g, f: f, sc: p.scope, done: map[*entity]bool{}}
	self := st.use(e)
	for _, s := range p.steps {
		switch {
		case s.owning(), s.draft != nil:
			st.use(s.to)
		case !s.dependent() && slices.ContainsFunc(p.steps, func(d *nestStep) bool { return d.dependent() && d.draft != nil && d.to == s.via }):
			st.use(s.to)
		}
		if s.via != nil && s.draft != nil {
			st.use(s.via)
		}
	}
	// The flat Draft; owning relations fill its foreign keys.
	f.printf("      const draft: %s = %s;\n", g.ref(f, TypeName(e.draft.GetHostName().GetWords()), false), g.spread(e.draft, p.draft, "d", nil, nil))
	for _, s := range p.steps {
		if !s.owning() || s.draft == nil {
			continue
		}
		f.printf("      let %s: %s | undefined;\n", s.created(), g.ref(f, TypeName(s.to.full.GetHostName().GetWords()), false))
		f.printf("      if (d.%s !== undefined) {\n        const ref = d.%s;\n        switch (ref.kind) {\n", s.member(), s.member())
		for _, c := range g.variants(s.draft) {
			f.printf("          case %s: {\n", jsString(changeKinds[c]))
			from, v := s.to.key, "ref.key"
			if c == irv1.RelationChange_RELATION_CHANGE_CREATE {
				f.printf("            const related = await %s.insert(ref.draft);\n            %s = related;\n", storeVar(s.to), s.created())
				from, v = s.to.full, "related"
			}
			for i, col := range s.r.GetFrom().GetColumns() {
				f.printf("            draft.%s = %s.%s;\n", g.memberOf(e.draft, col), v, g.memberOf(from, s.r.GetTo().GetColumns()[i]))
			}
			f.printf("            break;\n          }\n")
		}
		f.printf("        }\n      }\n")
	}
	f.printf("      const row = await %s.insert(draft, ...as);\n", self)
	// Dependents, then many-to-many links, collecting the junction rows
	// and their far rows.
	for _, s := range p.steps {
		switch {
		case s.dependent() && toMany(s.r), !s.owning() && !s.dependent():
			f.printf("      const %s: %s[] = [];\n", s.result(), g.ref(f, TypeName(s.to.full.GetHostName().GetWords()), false))
		case s.dependent():
			f.printf("      let %s: %s | null = null;\n", s.result(), g.ref(f, TypeName(s.to.full.GetHostName().GetWords()), false))
		}
	}
	for _, s := range p.steps {
		if !s.dependent() || s.draft == nil {
			continue
		}
		child := g.ix.shapes[shapeKey(s.draft.GetShape())]
		lit := g.spread(s.to.draft, child, "c", s.r.GetTo().GetColumns(), g.valuesOf(e.full, "row", s.r.GetFrom().GetColumns()))
		if toMany(s.r) {
			f.printf("      for (const c of d.%s ?? []) {\n", s.member())
		} else {
			f.printf("      if (d.%s !== undefined) {\n        const c = d.%s;\n", s.member(), s.member())
		}
		f.printf("        const child = await %s.insert(%s);\n", storeVar(s.to), lit)
		if toMany(s.r) {
			f.printf("        %s.push(child);\n", s.result())
		} else {
			f.printf("        %s = child;\n", s.result())
		}
		for _, m := range p.steps {
			if !m.owning() && !m.dependent() && m.via == s.to {
				g.readFar(f, m, "child", "        ")
			}
		}
		f.printf("      }\n")
	}
	for _, s := range p.steps {
		if s.owning() || s.dependent() || s.draft == nil {
			continue
		}
		f.printf("      for (const ref of d.%s ?? []) {\n        let far: %s;\n        switch (ref.kind) {\n", s.member(), g.ref(f, TypeName(s.to.full.GetHostName().GetWords()), false))
		for _, c := range g.variants(s.draft) {
			call := fmt.Sprintf("%s.get(ref.key)", storeVar(s.to))
			if c == irv1.RelationChange_RELATION_CHANGE_CREATE {
				call = fmt.Sprintf("%s.insert(ref.draft)", storeVar(s.to))
			}
			f.printf("          case %s:\n            far = await %s;\n            break;\n", jsString(changeKinds[c]), call)
		}
		f.printf("        }\n        const junction = await %s.insert(%s);\n", storeVar(s.via), g.junction(e, s, s.via.draft, "far", s.to.full))
		for _, d := range p.steps {
			if d.dependent() && toMany(d.r) && d.to == s.via {
				f.printf("        %s.push(junction);\n", d.result())
			}
		}
		f.printf("        %s.push(far);\n      }\n", s.result())
	}
	// Owning relations, read by the foreign key unless created.
	for _, s := range p.steps {
		if !s.owning() {
			continue
		}
		key, nulls := g.fkKey(s, "row", e.full)
		read := fmt.Sprintf("(await %s.get(%s))", storeVar(s.to), key)
		if s.draft != nil {
			read = s.created() + " ?? " + read
		}
		if len(nulls) > 0 {
			read = strings.Join(nulls, " || ") + " ? null : " + read
		}
		f.printf("      const %s = %s;\n", s.result(), read)
	}
	g.fullNested(f, p.full, p.steps)
	f.printf("    });\n")
}

// fullNested returns the Full Nested: the row and each step's result.
func (g *generator) fullNested(f *file, full *irv1.Shape, steps []*nestStep) {
	parts := []string{"...row"}
	for _, s := range steps {
		parts = append(parts, s.member()+": "+s.result())
	}
	f.printf("      return { %s };\n", strings.Join(parts, ", "))
}

// junction is the literal of a junction row of shape s: its own columns
// from the entity's row, and its far columns from v, a value of shape from.
func (g *generator) junction(e *entity, st *nestStep, s *irv1.Shape, v string, from *irv1.Shape) string {
	var lit []string
	for i, c := range st.own.GetFrom().GetColumns() {
		lit = append(lit, g.memberOf(s, c)+": row."+g.memberOf(e.full, st.own.GetTo().GetColumns()[i]))
	}
	for i, c := range st.far.GetFrom().GetColumns() {
		lit = append(lit, g.memberOf(s, c)+": "+v+"."+g.memberOf(from, st.far.GetTo().GetColumns()[i]))
	}
	return "{ " + strings.Join(lit, ", ") + " }"
}

// readFar appends to a many-to-many step's result the far row a junction
// row jv refers to. A junction's foreign keys are its key, never null.
func (g *generator) readFar(f *file, m *nestStep, jv, indent string) {
	var key []string
	for i, c := range m.far.GetFrom().GetColumns() {
		key = append(key, g.memberOf(m.to.key, m.far.GetTo().GetColumns()[i])+": "+jv+"."+g.memberOf(m.via.full, c))
	}
	f.printf("%s%s.push(await %s.get(%s));\n", indent, m.result(), storeVar(m.to), readKey(m.to, key))
}
