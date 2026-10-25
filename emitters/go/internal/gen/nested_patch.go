package gen

import (
	"fmt"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// nestedPatch is how PatchNested writes an entity's Nested Partial and
// reads back its Full Nested (§14.6, §20.5). Each step's draft holds the
// Nested Partial's nest.
type nestedPatch struct {
	partial, full *irv1.Shape
	patch         entry
	steps         []*nestStep
	scope         *irv1.Shape
	// cur: an owning Update patches the row the foreign key points at, so
	// the row is read first.
	cur bool
}

// scopedSteps reports whether a plan reaches a scoped entity.
func scopedSteps(steps []*nestStep) bool {
	return slices.ContainsFunc(steps, func(s *nestStep) bool { return s.to.scope != nil || s.via != nil && s.via.scope != nil })
}

// variants returns the changes a Partial nest's union allows (§14.6).
func (g *generator) variants(n *irv1.Nest) []irv1.RelationChange {
	var out []irv1.RelationChange
	for _, v := range g.ix.unions[unionKey(n.GetUnion())].GetVariants() {
		out = append(out, v.GetChange())
	}
	return out
}

// payload returns the shape a Partial nest's change of kind c holds.
func (g *generator) payload(n *irv1.Nest, c irv1.RelationChange) *irv1.Shape {
	vs := g.ix.unions[unionKey(n.GetUnion())].GetVariants()
	i := slices.IndexFunc(vs, func(v *irv1.RelationVariant) bool { return v.GetChange() == c })
	return g.ix.shapes[shapeKey(vs[i].GetPayload())]
}

// filterable reports whether builder reads can filter e's rows on cols and
// order them by its key: each column has Eq and Asc (§18.2), and Shapes
// reads e's Full.
func (g *generator) filterable(e *entity, cols []string) bool {
	if !g.fragmentIDs["op.eq"] || !g.fragmentIDs["order.asc"] || !g.fragmentIDs["select."+g.dotted(e.full.GetId())] {
		return false
	}
	for _, fld := range e.key.GetFields() {
		if fld.GetRole() != irv1.FieldRole_FIELD_ROLE_EXPECTED_VERSION {
			cols = append(cols, fld.GetColumn())
		}
	}
	for _, c := range cols {
		col := column(e.t, c)
		if !g.fragmentIDs[e.colFragment(c)] || !slices.ContainsFunc(col.GetOperators(), func(o *irv1.ColumnOperator) bool { return o.GetName() == "eq" }) {
			return false
		}
	}
	return true
}

// nestedPatch plans e's PatchNested on a store with scope sc, as
// nestedInsert plans InsertNested.
func (g *generator) nestedPatch(e *entity, sc *irv1.Shape) (*nestedPatch, bool) {
	patch, ok := e.entry("patch")
	p := &nestedPatch{
		partial: g.preset(e.q, irv1.Preset_PRESET_PARTIAL, irv1.Form_FORM_NESTED),
		full:    g.preset(e.q, irv1.Preset_PRESET_FULL, irv1.Form_FORM_NESTED),
		patch:   patch,
		scope:   sc,
	}
	if !ok || !e.has("get") || p.partial == nil || p.full == nil || len(p.partial.GetNests()) == 0 || g.embedded(p.full) == nil || g.embeddedNests(p.full) == nil {
		return nil, false
	}
	for _, n := range p.full.GetNests() {
		r := relation(e.t, n.GetRelation())
		to := g.byTable[keyOf(r.GetTo().GetTable())]
		s := &nestStep{r: r, to: to, full: n, draft: shapeNest(p.partial, n.GetRelation())}
		if to == nil || to == e || !reachable(sc, to) || !proto.Equal(n.GetShape(), to.full.GetId()) {
			return nil, false
		}
		var ok bool
		switch {
		case s.owning():
			ok = g.owningChange(e, s, p)
		case s.dependent():
			ok = g.childChange(s)
		default:
			ok = g.linkStep(e, s, sc, g.byTable) && g.linkChange(s)
		}
		if !ok {
			return nil, false
		}
		p.steps = append(p.steps, s)
	}
	for _, s := range p.steps {
		if s.via != nil && !slices.ContainsFunc(p.steps, func(d *nestStep) bool { return d.dependent() && toMany(d.r) && d.to == s.via }) {
			return nil, false
		}
	}
	return p, true
}

// owningChange checks an owning relation's change: a Link, Create or
// Unlink sets the foreign key, which the Partial holds; an Update patches
// the row it points at (§14.6).
func (g *generator) owningChange(e *entity, s *nestStep, p *nestedPatch) bool {
	if !keyed(s.to, s.r.GetTo().GetColumns()) {
		return false
	}
	if s.draft == nil {
		return true
	}
	for _, c := range g.variants(s.draft) {
		switch c {
		case irv1.RelationChange_RELATION_CHANGE_UPDATE:
			p.cur = true
			if !s.to.has("patch") {
				return false
			}
			continue
		case irv1.RelationChange_RELATION_CHANGE_CREATE:
			if !s.to.inserts() {
				return false
			}
		}
		for _, col := range s.r.GetFrom().GetColumns() {
			if shapeField(e.part, col) == nil {
				return false
			}
		}
	}
	return true
}

// childChange checks a dependent relation: its rows are read back by the
// foreign key, and each change the Partial allows is one the child's store
// runs (§14.6).
func (g *generator) childChange(s *nestStep) bool {
	fk := s.r.GetTo().GetColumns()
	if !s.to.inserts() || !g.filterable(s.to, fk) || (!toMany(s.r) && !s.full.GetNullable()) {
		return false
	}
	if s.draft == nil {
		return true
	}
	policy := s.draft.GetPolicy()
	if !toMany(s.r) && policy.GetCollection() == irv1.CollectionPolicy_COLLECTION_POLICY_REPLACE {
		return false
	}
	unlink := policy.GetRemoval() == irv1.Removal_REMOVAL_UNLINK
	if unlink && !g.unlinks(s.to, fk) {
		return false
	}
	if policy.GetCollection() == irv1.CollectionPolicy_COLLECTION_POLICY_REPLACE && !s.to.has(map[bool]string{true: "update_where", false: "delete_where"}[unlink]) {
		return false
	}
	for _, c := range g.variants(s.draft) {
		pl := g.payload(s.draft, c)
		switch c {
		case irv1.RelationChange_RELATION_CHANGE_CREATE:
			if !g.fills(s.to.draft, pl, fk) {
				return false
			}
		case irv1.RelationChange_RELATION_CHANGE_UPDATE:
			// An Update with no fields to change only keeps the child.
			if len(pl.GetFields()) > 0 && !s.to.has("patch") || slices.ContainsFunc(pl.GetFields(), func(f *irv1.Field) bool { return shapeField(s.to.part, f.GetColumn()) == nil }) {
				return false
			}
		case irv1.RelationChange_RELATION_CHANGE_DELETE:
			if !unlink && !s.to.has("delete") {
				return false
			}
		}
	}
	return true
}

// unlinks reports whether e's Patch can set the foreign key fk to null.
func (g *generator) unlinks(e *entity, fk []string) bool {
	if !e.has("patch") {
		return false
	}
	for _, c := range fk {
		if f := shapeField(e.part, c); f == nil || !f.GetNullable() {
			return false
		}
	}
	return true
}

// linkChange checks a many-to-many relation's changes: Link and Create
// write a junction row, ignoring one already there, and Unlink deletes it
// by the junction's key, which is the two foreign keys (§14.6).
func (g *generator) linkChange(s *nestStep) bool {
	if s.draft == nil {
		return true
	}
	if !s.via.has("insert_or_ignore") {
		return false
	}
	keyCols := slices.Concat(s.own.GetFrom().GetColumns(), s.far.GetFrom().GetColumns())
	for _, c := range g.variants(s.draft) {
		if c == irv1.RelationChange_RELATION_CHANGE_UNLINK && (!s.via.has("delete") || len(s.via.key.GetFields()) != len(keyCols) ||
			slices.ContainsFunc(s.via.key.GetFields(), func(f *irv1.Field) bool { return !slices.Contains(keyCols, f.GetColumn()) })) {
			return false
		}
	}
	if s.draft.GetPolicy().GetCollection() == irv1.CollectionPolicy_COLLECTION_POLICY_REPLACE &&
		(!s.via.has("delete_where") || !g.filterable(s.via, keyCols)) {
		return false
	}
	return true
}

// keyLit is the literal of to's Key from v, a value of shape src holding
// every key column, the expected version included.
func (g *generator) keyLit(to *entity, v string, src *irv1.Shape) string {
	var fields []string
	for _, fld := range to.key.GetFields() {
		fields = append(fields, g.fieldName(to.key, fld.GetColumn())+": "+v+"."+g.fieldName(src, fld.GetColumn()))
	}
	return Exported(to.key.GetHostName().GetWords()) + "{" + strings.Join(fields, ", ") + "}"
}

// match is the predicate on e's rows that cols of e equal the values vals.
func (g *generator) match(e *entity, cols, vals []string) string {
	var ps []string
	for i, c := range cols {
		ps = append(ps, fmt.Sprintf("%s.%s.Eq(%s)", e.plural, Exported(column(e.t, c).GetHostName().GetWords()), vals[i]))
	}
	return e.f.use(pkgDQL, "dql") + ".And(" + strings.Join(ps, ", ") + ")"
}

// keyCols are e's Key columns but its expected version.
func keyCols(e *entity) []string {
	var out []string
	for _, fld := range e.key.GetFields() {
		if fld.GetRole() != irv1.FieldRole_FIELD_ROLE_EXPECTED_VERSION {
			out = append(out, fld.GetColumn())
		}
	}
	return out
}

// nestedPatchSignature is PatchNested's signature.
func (g *generator) nestedPatchSignature(e *entity, p *nestedPatch) signature {
	return signature{
		name: "PatchNested",
		params: "k " + Exported(e.key.GetHostName().GetWords()) + ", p " + Exported(p.partial.GetHostName().GetWords()) +
			", as ..." + e.f.use(pkgDQL, "dql") + ".Update[" + e.tableType() + "]",
		results: "(" + Exported(p.full.GetHostName().GetWords()) + ", error)",
		x:       p.patch,
		patch:   p,
	}
}

// patchNested writes PatchNested: it validates the whole Partial (§20.3),
// then in one transaction applies owning changes, patches the row, applies
// dependent and many-to-many changes with their collection policies, and
// reads back the Full Nested as it now is (§14.6, §20.5).
func (g *generator) patchNested(e *entity, s signature) {
	f := e.f
	dql := f.use(pkgDQL, "dql")
	ctx := f.use("context", "context")
	o := f.use(pkgOpt, "opt")
	p := s.patch
	fullNested := Exported(p.full.GetHostName().GetWords())
	f.printf("\tif err := p.Validate(); err != nil {\n\t\treturn %s{}, err\n\t}\n", fullNested)
	f.printf("\tvar out %s\n\terr := %s.Atomic(ctx, s.w, func(ctx %s.Context, w %s.Writer) error {\n\t\tout = %s{}\n", fullNested, dql, ctx, dql, fullNested)
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
		store(st.to)
		if st.via != nil {
			store(st.via)
		}
	}
	f.printf("\t\tpart := %s{%s}\n", Exported(e.part.GetHostName().GetWords()), strings.Join(g.copyFields(f, e.part, p.partial, "p", nil), ", "))
	if p.cur {
		f.printf("\t\tcur, err := %s.Get(ctx, k)\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n", self)
	}
	// Owning changes set the foreign key the row is patched with.
	for _, st := range p.steps {
		if !st.owning() || st.draft == nil {
			continue
		}
		f.printf("\t\tif ch, ok := p.%s.Get(); ok {\n\t\t\tswitch ch.change {\n", Exported(st.r.GetName().GetWords()))
		for _, c := range g.variants(st.draft) {
			f.printf("\t\t\tcase %s.%s:\n", dql, changeWords[c])
			switch c {
			case irv1.RelationChange_RELATION_CHANGE_LINK:
				g.setPartialKey(f, e, st, "ch.key", st.to.key)
			case irv1.RelationChange_RELATION_CHANGE_CREATE:
				f.printf("\t\t\t\trelated, err := %s.Insert(ctx, ch.draft)\n\t\t\t\tif err != nil {\n\t\t\t\t\treturn err\n\t\t\t\t}\n", store(st.to))
				g.setPartialKey(f, e, st, "related", st.to.full)
			case irv1.RelationChange_RELATION_CHANGE_UPDATE:
				key, nils := g.fkKey(st, "cur", e.full)
				if len(nils) > 0 {
					f.printf("\t\t\t\tif !(%s) {\n\t\t\t\t\treturn &%s.NotFound{Origin: %s.Origin{Query: %q}}\n\t\t\t\t}\n", strings.Join(nils, " && "), dql, dql, st.to.entryQuery("patch"))
				}
				f.printf("\t\t\t\trelated, err := %s.Get(ctx, %s)\n\t\t\t\tif err != nil {\n\t\t\t\t\treturn err\n\t\t\t\t}\n", store(st.to), key)
				f.printf("\t\t\t\tif _, err := %s.Patch(ctx, %s, ch.partial); err != nil {\n\t\t\t\t\treturn err\n\t\t\t\t}\n", store(st.to), g.keyLit(st.to, "related", st.to.full))
			case irv1.RelationChange_RELATION_CHANGE_UNLINK:
				for _, col := range st.r.GetFrom().GetColumns() {
					fld := shapeField(e.part, col)
					f.printf("\t\t\t\tpart.%s = %s.Some[%s](nil)\n", g.fieldName(e.part, col), o, g.field(f, fld.GetType(), true, e.q, ""))
				}
			}
		}
		f.printf("\t\t\t}\n\t\t}\n")
	}
	f.printf("\t\trow, err := %s.Patch(ctx, k, part, as...)\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n\t\tout.%s = row\n", self, Exported(e.full.GetHostName().GetWords()))
	for _, st := range p.steps {
		switch {
		case st.draft == nil || st.owning():
		case st.dependent():
			g.childChanges(f, e, st)
		default:
			g.linkChanges(f, e, st)
		}
	}
	// The Full Nested as it now is.
	for _, st := range p.steps {
		name := Exported(st.r.GetName().GetWords())
		switch {
		case st.owning():
			key, nils := g.fkKey(st, "row", e.full)
			indent := "\t\t"
			if len(nils) > 0 {
				f.printf("\t\tif %s {\n", strings.Join(nils, " && "))
				indent += "\t"
			} else {
				f.printf("\t\t{\n")
				indent += "\t"
			}
			f.printf("%srelated, err := %s.Get(ctx, %s)\n%sif err != nil {\n%s\treturn err\n%s}\n", indent, store(st.to), key, indent, indent, indent)
			if st.full.GetNullable() {
				f.printf("%sout.%s = &related\n", indent, name)
			} else {
				f.printf("%sout.%s = related\n", indent, name)
			}
			f.printf("\t\t}\n")
		case st.dependent():
			fk := st.r.GetTo().GetColumns()
			var vals, order []string
			for _, c := range st.r.GetFrom().GetColumns() {
				vals = append(vals, "row."+g.fieldName(e.full, c))
			}
			for _, c := range keyCols(st.to) {
				order = append(order, fmt.Sprintf("%s.%s.Asc()", st.to.plural, Exported(column(st.to.t, c).GetHostName().GetWords())))
			}
			sel := fmt.Sprintf("Select(%s, Shapes.%s).Where(%s).OrderBy(%s)", store(st.to), Exported(st.to.full.GetHostName().GetWords()), g.match(st.to, fk, vals), strings.Join(order, ", "))
			if toMany(st.r) {
				f.printf("\t\tif out.%s, err = %s.All(ctx); err != nil {\n\t\t\treturn err\n\t\t}\n", name, sel)
				continue
			}
			f.printf("\t\t{\n\t\t\tchild, err := %s.OptionalOne(ctx)\n\t\t\tif err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t\tif v, ok := child.Get(); ok {\n\t\t\t\tout.%s = &v\n\t\t\t}\n\t\t}\n", sel, name)
		default:
			var junctions string
			for _, d := range p.steps {
				if d.dependent() && toMany(d.r) && d.to == st.via {
					junctions = Exported(d.r.GetName().GetWords())
				}
			}
			f.printf("\t\tout.%s = []%s{}\n\t\tfor _, j := range out.%s {\n", name, Exported(st.to.full.GetHostName().GetWords()), junctions)
			g.readFar(f, st, "j", store(st.to), "\t\t\t")
			f.printf("\t\t}\n")
		}
	}
	f.printf("\t\treturn nil\n\t})\n\treturn out, err\n")
}

// entryQuery is the query name of e's entry of verb.
func (e *entity) entryQuery(verb string) string {
	x, _ := e.entry(verb)
	return x.q.GetName().GetStored()
}

// fkKey is the literal of an owning step's related Key from v's foreign
// key, a value of shape src, and the conditions that its nullable columns
// are set.
func (g *generator) fkKey(st *nestStep, v string, src *irv1.Shape) (string, []string) {
	var key, nils []string
	for i, c := range st.r.GetFrom().GetColumns() {
		x := v + "." + g.fieldName(src, c)
		if shapeField(src, c).GetNullable() {
			nils = append(nils, x+" != nil")
			x = "*" + x
		}
		key = append(key, g.fieldName(st.to.key, st.r.GetTo().GetColumns()[i])+": "+x)
	}
	return Exported(st.to.key.GetHostName().GetWords()) + "{" + strings.Join(key, ", ") + "}", nils
}

// setPartialKey sets the Partial's foreign key of an owning step from v, a
// value of shape from holding the related row's referenced columns.
func (g *generator) setPartialKey(f *file, e *entity, st *nestStep, v string, from *irv1.Shape) {
	for i, c := range st.r.GetFrom().GetColumns() {
		val := value(f, v+"."+g.fieldName(from, st.r.GetTo().GetColumns()[i]), shapeField(e.part, c), false)
		f.printf("\t\t\t\tpart.%s = %s\n", g.fieldName(e.part, c), val)
	}
}

// childChanges applies a dependent relation's changes, then its collection
// policy's removal of the children they leave out (§14.6).
func (g *generator) childChanges(f *file, e *entity, st *nestStep) {
	dql := f.use(pkgDQL, "dql")
	o := f.use(pkgOpt, "opt")
	name := Exported(st.r.GetName().GetWords())
	store := st.to.lower() + "Store"
	fk := st.r.GetTo().GetColumns()
	key := Exported(st.to.key.GetHostName().GetWords())
	policy := st.draft.GetPolicy()
	replace := policy.GetCollection() == irv1.CollectionPolicy_COLLECTION_POLICY_REPLACE
	unlink := policy.GetRemoval() == irv1.Removal_REMOVAL_UNLINK
	var unset []string
	if unlink {
		for _, c := range fk {
			unset = append(unset, fmt.Sprintf("%s: %s.Some[%s](nil)", g.fieldName(st.to.part, c), o, g.field(f, shapeField(st.to.part, c).GetType(), true, st.to.q, "")))
		}
	}
	indent := "\t\t\t"
	if toMany(st.r) {
		f.printf("\t\tif chs, ok := p.%s.Get(); ok {\n", name)
		if replace {
			f.printf("\t\t\tvar kept []%s\n", key)
		}
		f.printf("\t\t\tfor _, ch := range chs {\n")
		indent += "\t"
	} else {
		f.printf("\t\tif ch, ok := p.%s.Get(); ok {\n", name)
	}
	keep := func(k string) {
		if replace {
			f.printf("%s\tkept = append(kept, %s)\n", indent, k)
		}
	}
	// owns checks that the child the change names is the row's.
	owns := func() {
		var differ []string
		for i, c := range fk {
			cv, rv := "child."+g.fieldName(st.to.full, c), "row."+g.fieldName(e.full, st.r.GetFrom().GetColumns()[i])
			if shapeField(st.to.full, c).GetNullable() {
				differ = append(differ, fmt.Sprintf("%s == nil || *%s != %s", cv, cv, rv))
			} else {
				differ = append(differ, cv+" != "+rv)
			}
		}
		f.printf("%s\tchild, err := %s.Get(ctx, ch.key)\n%s\tif err != nil {\n%s\t\treturn err\n%s\t}\n", indent, store, indent, indent, indent)
		f.printf("%s\tif %s {\n%s\t\treturn &%s.NotFound{Origin: %s.Origin{Query: %q}}\n%s\t}\n", indent, strings.Join(differ, " || "), indent, dql, dql, st.to.entryQuery("get"), indent)
	}
	f.printf("%sswitch ch.change {\n", indent)
	for _, c := range g.variants(st.draft) {
		f.printf("%scase %s.%s:\n", indent, dql, changeWords[c])
		switch c {
		case irv1.RelationChange_RELATION_CHANGE_CREATE:
			lit := g.copyFields(f, st.to.draft, g.payload(st.draft, c), "ch.draft", fk)
			for i, col := range fk {
				lit = append(lit, g.fieldName(st.to.draft, col)+": "+value(f, "row."+g.fieldName(e.full, st.r.GetFrom().GetColumns()[i]), shapeField(st.to.draft, col), false))
			}
			insert := fmt.Sprintf("%s.Insert(ctx, %s{%s})", store, Exported(st.to.draft.GetHostName().GetWords()), strings.Join(lit, ", "))
			if !replace {
				f.printf("%s\tif _, err := %s; err != nil {\n%s\t\treturn err\n%s\t}\n", indent, insert, indent, indent)
				break
			}
			f.printf("%s\tchild, err := %s\n%s\tif err != nil {\n%s\t\treturn err\n%s\t}\n", indent, insert, indent, indent, indent)
			keep(g.keyLit(st.to, "child", st.to.full))
		case irv1.RelationChange_RELATION_CHANGE_UPDATE:
			owns()
			lit := g.copyFields(f, st.to.part, g.payload(st.draft, c), "ch.partial", nil)
			if len(lit) == 0 {
				keep("ch.key")
				break
			}
			f.printf("%s\tif _, err := %s.Patch(ctx, ch.key, %s{%s}); err != nil {\n%s\t\treturn err\n%s\t}\n", indent, store, Exported(st.to.part.GetHostName().GetWords()), strings.Join(lit, ", "), indent, indent)
			keep("ch.key")
		case irv1.RelationChange_RELATION_CHANGE_DELETE:
			owns()
			if unlink {
				f.printf("%s\tif _, err := %s.Patch(ctx, ch.key, %s{%s}); err != nil {\n%s\t\treturn err\n%s\t}\n", indent, store, Exported(st.to.part.GetHostName().GetWords()), strings.Join(unset, ", "), indent, indent)
			} else {
				f.printf("%s\tif err := %s.Delete(ctx, ch.key); err != nil {\n%s\t\treturn err\n%s\t}\n", indent, store, indent, indent)
			}
		}
	}
	f.printf("%s}\n", indent)
	if toMany(st.r) {
		f.printf("\t\t\t}\n")
		if replace {
			var vals, kcols, kvals []string
			for _, c := range st.r.GetFrom().GetColumns() {
				vals = append(vals, "row."+g.fieldName(e.full, c))
			}
			for _, c := range keyCols(st.to) {
				kcols = append(kcols, c)
				kvals = append(kvals, "k."+g.fieldName(st.to.key, c))
			}
			f.printf("\t\t\t// Replace removes the children the changes leave out.\n\t\t\twhere := %s\n\t\t\tfor _, k := range kept {\n\t\t\t\twhere = %s.And(where, %s.Not(%s))\n\t\t\t}\n",
				g.match(st.to, fk, vals), dql, dql, g.match(st.to, kcols, kvals))
			if unlink {
				f.printf("\t\t\tif _, err := %s.UpdateWhere(ctx, where, %s{%s}); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n", store, Exported(st.to.part.GetHostName().GetWords()), strings.Join(unset, ", "))
			} else {
				f.printf("\t\t\tif _, err := %s.DeleteWhere(ctx, where); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n", store)
			}
		}
	}
	f.printf("\t\t}\n")
}

// linkChanges applies a many-to-many relation's changes to its junction
// rows, then its collection policy's removal of the links they leave out
// (§14.6).
func (g *generator) linkChanges(f *file, e *entity, st *nestStep) {
	dql := f.use(pkgDQL, "dql")
	name := Exported(st.r.GetName().GetWords())
	via, far := st.via.lower()+"Store", st.to.lower()+"Store"
	replace := st.draft.GetPolicy().GetCollection() == irv1.CollectionPolicy_COLLECTION_POLICY_REPLACE
	farKey := Exported(st.to.key.GetHostName().GetWords())
	// junction is the literal of a junction row of shape s, its far columns
	// from v, a value of shape from.
	junction := func(s *irv1.Shape, v string, from *irv1.Shape) string {
		var lit []string
		for i, c := range st.own.GetFrom().GetColumns() {
			lit = append(lit, g.fieldName(s, c)+": "+value(f, "row."+g.fieldName(e.full, st.own.GetTo().GetColumns()[i]), shapeField(s, c), false))
		}
		for i, c := range st.far.GetFrom().GetColumns() {
			lit = append(lit, g.fieldName(s, c)+": "+value(f, v+"."+g.fieldName(from, st.far.GetTo().GetColumns()[i]), shapeField(s, c), false))
		}
		return Exported(s.GetHostName().GetWords()) + "{" + strings.Join(lit, ", ") + "}"
	}
	f.printf("\t\tif chs, ok := p.%s.Get(); ok {\n", name)
	if replace {
		f.printf("\t\t\tvar kept []%s\n", farKey)
	}
	f.printf("\t\t\tfor _, ch := range chs {\n\t\t\t\tswitch ch.change {\n")
	keep := func(k string) {
		if replace {
			f.printf("\t\t\t\t\tkept = append(kept, %s)\n", k)
		}
	}
	for _, c := range g.variants(st.draft) {
		f.printf("\t\t\t\tcase %s.%s:\n", dql, changeWords[c])
		switch c {
		case irv1.RelationChange_RELATION_CHANGE_LINK:
			f.printf("\t\t\t\t\tif _, err := %s.InsertOrIgnore(ctx, %s); err != nil {\n\t\t\t\t\t\treturn err\n\t\t\t\t\t}\n", via, junction(st.via.draft, "ch.key", st.to.key))
			keep("ch.key")
		case irv1.RelationChange_RELATION_CHANGE_CREATE:
			f.printf("\t\t\t\t\trelated, err := %s.Insert(ctx, ch.draft)\n\t\t\t\t\tif err != nil {\n\t\t\t\t\t\treturn err\n\t\t\t\t\t}\n", far)
			f.printf("\t\t\t\t\tif _, err := %s.InsertOrIgnore(ctx, %s); err != nil {\n\t\t\t\t\t\treturn err\n\t\t\t\t\t}\n", via, junction(st.via.draft, "related", st.to.full))
			keep(g.keyLit(st.to, "related", st.to.full))
		case irv1.RelationChange_RELATION_CHANGE_UNLINK:
			f.printf("\t\t\t\t\tif err := %s.Delete(ctx, %s); err != nil {\n\t\t\t\t\t\treturn err\n\t\t\t\t\t}\n", via, junction(st.via.key, "ch.key", st.to.key))
		}
	}
	f.printf("\t\t\t\t}\n\t\t\t}\n")
	if replace {
		var vals, fvals []string
		for i := range st.own.GetFrom().GetColumns() {
			vals = append(vals, "row."+g.fieldName(e.full, st.own.GetTo().GetColumns()[i]))
		}
		for i := range st.far.GetFrom().GetColumns() {
			fvals = append(fvals, "k."+g.fieldName(st.to.key, st.far.GetTo().GetColumns()[i]))
		}
		f.printf("\t\t\t// Replace unlinks the rows the changes leave out.\n\t\t\twhere := %s\n\t\t\tfor _, k := range kept {\n\t\t\t\twhere = %s.And(where, %s.Not(%s))\n\t\t\t}\n\t\t\tif _, err := %s.DeleteWhere(ctx, where); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n",
			g.match(st.via, st.own.GetFrom().GetColumns(), vals), dql, dql, g.match(st.via, st.far.GetFrom().GetColumns(), fvals), via)
	}
	f.printf("\t\t}\n")
}
