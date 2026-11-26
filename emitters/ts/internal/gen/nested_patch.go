package gen

import (
	"fmt"
	"slices"
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// nestedPatch is how patchNested writes an entity's Nested Partial and
// reads back its Full Nested (§14.6, §20.5). Each step's draft holds the
// Nested Partial's nest.
type nestedPatch struct {
	partial, full *irv1.Shape
	patch         entry
	steps         []*nestStep
	scope         *irv1.Shape
	// cur: an owning update patches the row the foreign key points at, so
	// the row is read first.
	cur bool
}

// scopedSteps reports whether a plan reaches a scoped entity.
func scopedSteps(steps []*nestStep) bool {
	return slices.ContainsFunc(steps, func(s *nestStep) bool { return s.to.scope != nil || s.via != nil && s.via.scope != nil })
}

// variants returns the changes a nest's union allows (§14.5, §14.6).
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
// order them by its key: each column has eq and asc (§18.2), and shapes
// reads e's Full.
func (g *generator) filterable(e *entity, cols []string) bool {
	if !g.fragmentIDs["op.eq"] || !g.fragmentIDs["order.asc"] || !g.fragmentIDs["select."+g.dotted(e.full.GetId())] {
		return false
	}
	for _, c := range slices.Concat(cols, keyCols(e)) {
		col := column(e.t, c)
		if !g.fragmentIDs[e.colFragment(c)] || !slices.ContainsFunc(col.GetOperators(), func(o *irv1.ColumnOperator) bool { return o.GetName() == "eq" }) {
			return false
		}
	}
	return true
}

// nestedPatch plans e's patchNested on a store with scope sc, as
// nestedInsert plans insertNested.
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
		s, ok := g.step(e, sc, n, shapeNest(p.partial, n.GetRelation()))
		if !ok {
			return nil, false
		}
		switch {
		case s.owning():
			ok = g.owningChange(e, s, p)
		case s.dependent():
			ok = g.childChange(s)
		default:
			ok = g.linkStep(e, s, sc) && g.linkChange(s)
		}
		if !ok {
			return nil, false
		}
		p.steps = append(p.steps, s)
	}
	return p, joined(p.steps)
}

// owningChange checks an owning relation's change: a link, create or
// unlink sets the foreign key, which the Partial holds; an update patches
// the row it points at (§14.6).
func (g *generator) owningChange(e *entity, s *nestStep, p *nestedPatch) bool {
	if !keyed(s.to, s.r.GetTo().GetColumns()) || !g.readsBack(e, s) {
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
		// An update or delete first reads the child it names.
		if (c == irv1.RelationChange_RELATION_CHANGE_UPDATE || c == irv1.RelationChange_RELATION_CHANGE_DELETE) && !s.to.has("get") {
			return false
		}
		switch c {
		case irv1.RelationChange_RELATION_CHANGE_CREATE:
			if !g.fills(s.to.draft, pl, fk) {
				return false
			}
		case irv1.RelationChange_RELATION_CHANGE_UPDATE:
			// An update with no fields to change only keeps the child.
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

// unlinks reports whether e's patch can set the foreign key fk to null.
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

// linkChange checks a many-to-many relation's changes: link and create
// write a junction row, ignoring one already there, and unlink deletes it
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

// match is the predicate on e's rows that cols of e equal the values vals.
func (g *generator) match(f *file, e *entity, cols, vals []string) string {
	var ps []string
	for i, c := range cols {
		ps = append(ps, fmt.Sprintf("%s.%s.eq(%s)", g.ref(f, e.member, true), MemberName(column(e.t, c).GetHostName().GetWords()), vals[i]))
	}
	if len(ps) == 1 {
		return ps[0]
	}
	return f.rt("and", true) + "(" + strings.Join(ps, ", ") + ")"
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

// nestedPatchSignature is patchNested's signature, its types imported into
// f.
func (g *generator) nestedPatchSignature(f *file, e *entity, p *nestedPatch) signature {
	return signature{
		name: "patchNested",
		params: []param{
			{name: "k", typ: g.ref(f, TypeName(e.key.GetHostName().GetWords()), false)},
			{name: "p", typ: g.ref(f, TypeName(p.partial.GetHostName().GetWords()), false)},
			{name: "as", typ: f.rt("Update", false) + "<" + g.ref(f, e.tableType(), false) + ">[]", rest: true},
		},
		result: g.ref(f, TypeName(p.full.GetHostName().GetWords()), false),
		x:      p.patch,
		patch:  p,
	}
}

// patchNested writes patchNested: it validates the whole Partial (§20.3),
// then in one transaction applies owning changes, patches the row, applies
// dependent and many-to-many changes with their collection policies, and
// reads back the Full Nested as it now is (§14.6, §20.5).
func (g *generator) patchNested(f *file, e *entity, s signature) {
	p := s.patch
	rt := func(name string) string { return f.rt(name, true) }
	f.printf("    %s(%s(p));\n", rt("throwIfIssues"), g.ref(f, validatorName(p.partial.GetHostName().GetWords()), true))
	f.printf("    return %s(this.w, async (w: %s) => {\n", rt("atomic"), f.rt("Writer", false))
	st := &stores{g: g, f: f, sc: p.scope, done: map[*entity]bool{}}
	self := st.use(e)
	for _, s := range p.steps {
		st.use(s.to)
		if s.via != nil {
			st.use(s.via)
		}
	}
	f.printf("      const part: %s = { ...p };\n", g.ref(f, TypeName(e.part.GetHostName().GetWords()), false))
	if p.cur {
		f.printf("      const cur = await %s.get(k);\n", self)
	}
	// Owning changes set the foreign key the row is patched with.
	for _, s := range p.steps {
		if !s.owning() || s.draft == nil {
			continue
		}
		f.printf("      if (p.%s !== undefined) {\n        const ch = p.%s;\n        switch (ch.kind) {\n", s.member(), s.member())
		for _, c := range g.variants(s.draft) {
			f.printf("          case %s: {\n", jsString(changeKinds[c]))
			switch c {
			case irv1.RelationChange_RELATION_CHANGE_LINK:
				g.setPartialKey(f, e, s, "ch.key", s.to.key)
			case irv1.RelationChange_RELATION_CHANGE_CREATE:
				f.printf("            const related = await %s.insert(ch.draft);\n", storeVar(s.to))
				g.setPartialKey(f, e, s, "related", s.to.full)
			case irv1.RelationChange_RELATION_CHANGE_UPDATE:
				key, nulls := g.fkKey(s, "cur", e.full)
				if len(nulls) > 0 {
					f.printf("            if (%s) {\n              throw new %s({ query: %s });\n            }\n", strings.Join(nulls, " || "), rt("NotFound"), jsString(s.to.entryQuery("patch")))
				}
				f.printf("            const related = await %s.get(%s);\n", storeVar(s.to), key)
				f.printf("            await %s.patch(%s, ch.partial);\n", storeVar(s.to), g.keyLit(s.to, "related", s.to.full))
			case irv1.RelationChange_RELATION_CHANGE_UNLINK:
				for _, col := range s.r.GetFrom().GetColumns() {
					f.printf("            part.%s = null;\n", g.memberOf(e.part, col))
				}
			}
			f.printf("            break;\n          }\n")
		}
		f.printf("        }\n      }\n")
	}
	f.printf("      const row = await %s.patch(k, part, ...as);\n", self)
	for _, s := range p.steps {
		switch {
		case s.draft == nil || s.owning():
		case s.dependent():
			g.childChanges(f, e, s)
		default:
			g.linkChanges(f, e, s)
		}
	}
	// The Full Nested as it now is: the owning and dependent rows, then the
	// far rows of the junction rows read back.
	for _, s := range p.steps {
		switch {
		case s.owning():
			key, nulls := g.fkKey(s, "row", e.full)
			read := fmt.Sprintf("await %s.get(%s)", storeVar(s.to), key)
			if len(nulls) > 0 {
				read = strings.Join(nulls, " || ") + " ? null : " + read
			}
			f.printf("      const %s = %s;\n", s.result(), read)
		case s.dependent():
			var order []string
			for _, c := range keyCols(s.to) {
				order = append(order, fmt.Sprintf("%s.%s.asc()", g.ref(f, s.to.member, true), MemberName(column(s.to.t, c).GetHostName().GetWords())))
			}
			sel := fmt.Sprintf("%s.select(%s.%s).where(%s).orderBy(%s)", storeVar(s.to), g.ref(f, "shapes", true), MemberName(s.to.full.GetHostName().GetWords()),
				g.match(f, s.to, s.r.GetTo().GetColumns(), g.valuesOf(e.full, "row", s.r.GetFrom().GetColumns())), strings.Join(order, ", "))
			if toMany(s.r) {
				f.printf("      const %s = await %s.all();\n", s.result(), sel)
			} else {
				f.printf("      const %s = (await %s.optionalOne()) ?? null;\n", s.result(), sel)
			}
		}
	}
	for _, s := range p.steps {
		if s.owning() || s.dependent() {
			continue
		}
		f.printf("      const %s: %s[] = [];\n", s.result(), g.ref(f, TypeName(s.to.full.GetHostName().GetWords()), false))
		for _, d := range p.steps {
			if d.dependent() && toMany(d.r) && d.to == s.via {
				f.printf("      for (const j of %s) {\n", d.result())
				g.readFar(f, s, "j", "        ")
				f.printf("      }\n")
			}
		}
	}
	g.fullNested(f, p.full, p.steps)
	f.printf("    });\n")
}

// entryQuery is the query name of e's entry of verb.
func (e *entity) entryQuery(verb string) string {
	x, _ := e.entry(verb)
	return x.q.GetName().GetStored()
}

// setPartialKey sets the Partial's foreign key of an owning step from v, a
// value of shape from holding the related row's referenced columns.
func (g *generator) setPartialKey(f *file, e *entity, st *nestStep, v string, from *irv1.Shape) {
	for i, c := range st.r.GetFrom().GetColumns() {
		f.printf("            part.%s = %s.%s;\n", g.memberOf(e.part, c), v, g.memberOf(from, st.r.GetTo().GetColumns()[i]))
	}
}

// childChanges applies a dependent relation's changes, then its collection
// policy's removal of the children they leave out (§14.6).
func (g *generator) childChanges(f *file, e *entity, st *nestStep) {
	store := storeVar(st.to)
	fk := st.r.GetTo().GetColumns()
	policy := st.draft.GetPolicy()
	replace := policy.GetCollection() == irv1.CollectionPolicy_COLLECTION_POLICY_REPLACE
	unlink := policy.GetRemoval() == irv1.Removal_REMOVAL_UNLINK
	var unset []string
	for _, c := range fk {
		unset = append(unset, g.memberOf(st.to.part, c)+": null")
	}
	unsetLit := "{ " + strings.Join(unset, ", ") + " }"
	rows := g.valuesOf(e.full, "row", st.r.GetFrom().GetColumns())
	indent := "        "
	if toMany(st.r) {
		f.printf("      if (p.%s !== undefined) {\n", st.member())
		if replace {
			f.printf("        const kept: %s[] = [];\n", g.ref(f, TypeName(st.to.key.GetHostName().GetWords()), false))
		}
		f.printf("        for (const ch of p.%s) {\n", st.member())
		indent += "  "
	} else {
		f.printf("      if (p.%s !== undefined) {\n        const ch = p.%s;\n", st.member(), st.member())
	}
	keep := func(k string) {
		if replace {
			f.printf("%s    kept.push(%s);\n", indent, k)
		}
	}
	// owns checks that the child the change names is the row's.
	owns := func() {
		var differ []string
		for i, c := range fk {
			differ = append(differ, fmt.Sprintf("child.%s !== %s", g.memberOf(st.to.full, c), rows[i]))
		}
		f.printf("%s    const child = await %s.get(ch.key);\n", indent, store)
		f.printf("%s    if (%s) {\n%s      throw new %s({ query: %s });\n%s    }\n", indent, strings.Join(differ, " || "), indent, f.rt("NotFound", true), jsString(st.to.entryQuery("get")), indent)
	}
	f.printf("%sswitch (ch.kind) {\n", indent)
	for _, c := range g.variants(st.draft) {
		f.printf("%s  case %s: {\n", indent, jsString(changeKinds[c]))
		switch c {
		case irv1.RelationChange_RELATION_CHANGE_CREATE:
			insert := fmt.Sprintf("%s.insert(%s)", store, g.spread(st.to.draft, g.payload(st.draft, c), "ch.draft", fk, rows))
			if !replace {
				f.printf("%s    await %s;\n", indent, insert)
				break
			}
			f.printf("%s    const child = await %s;\n", indent, insert)
			keep(g.keyLit(st.to, "child", st.to.full))
		case irv1.RelationChange_RELATION_CHANGE_UPDATE:
			owns()
			if len(g.payload(st.draft, c).GetFields()) > 0 {
				f.printf("%s    await %s.patch(ch.key, ch.partial);\n", indent, store)
			}
			keep("ch.key")
		case irv1.RelationChange_RELATION_CHANGE_DELETE:
			owns()
			if unlink {
				f.printf("%s    await %s.patch(ch.key, %s);\n", indent, store, unsetLit)
			} else {
				f.printf("%s    await %s.delete(ch.key);\n", indent, store)
			}
		}
		f.printf("%s    break;\n%s  }\n", indent, indent)
	}
	f.printf("%s}\n", indent)
	if toMany(st.r) {
		f.printf("        }\n")
		if replace {
			f.printf("        // Replace removes the children the changes leave out.\n        let where = %s;\n        for (const key of kept) {\n          where = %s(where, %s(%s));\n        }\n",
				g.match(f, st.to, fk, rows), f.rt("and", true), f.rt("not", true), g.match(f, st.to, keyCols(st.to), g.valuesOf(st.to.key, "key", keyCols(st.to))))
			if unlink {
				f.printf("        await %s.updateWhere(where, %s);\n", store, unsetLit)
			} else {
				f.printf("        await %s.deleteWhere(where);\n", store)
			}
		}
	}
	f.printf("      }\n")
}

// linkChanges applies a many-to-many relation's changes to its junction
// rows, then its collection policy's removal of the links they leave out
// (§14.6).
func (g *generator) linkChanges(f *file, e *entity, st *nestStep) {
	via, far := storeVar(st.via), storeVar(st.to)
	replace := st.draft.GetPolicy().GetCollection() == irv1.CollectionPolicy_COLLECTION_POLICY_REPLACE
	f.printf("      if (p.%s !== undefined) {\n", st.member())
	if replace {
		f.printf("        const kept: %s[] = [];\n", g.ref(f, TypeName(st.to.key.GetHostName().GetWords()), false))
	}
	f.printf("        for (const ch of p.%s) {\n          switch (ch.kind) {\n", st.member())
	keep := func(k string) {
		if replace {
			f.printf("              kept.push(%s);\n", k)
		}
	}
	for _, c := range g.variants(st.draft) {
		f.printf("            case %s: {\n", jsString(changeKinds[c]))
		switch c {
		case irv1.RelationChange_RELATION_CHANGE_LINK:
			f.printf("              await %s.insertOrIgnore(%s);\n", via, g.junction(e, st, st.via.draft, "ch.key", st.to.key))
			keep("ch.key")
		case irv1.RelationChange_RELATION_CHANGE_CREATE:
			f.printf("              const related = await %s.insert(ch.draft);\n", far)
			f.printf("              await %s.insertOrIgnore(%s);\n", via, g.junction(e, st, st.via.draft, "related", st.to.full))
			keep(g.keyLit(st.to, "related", st.to.full))
		case irv1.RelationChange_RELATION_CHANGE_UNLINK:
			f.printf("              await %s.delete(%s);\n", via, g.junction(e, st, st.via.key, "ch.key", st.to.key))
		}
		f.printf("              break;\n            }\n")
	}
	f.printf("          }\n        }\n")
	if replace {
		f.printf("        // Replace unlinks the rows the changes leave out.\n        let where = %s;\n        for (const key of kept) {\n          where = %s(where, %s(%s));\n        }\n        await %s.deleteWhere(where);\n",
			g.match(f, st.via, st.own.GetFrom().GetColumns(), g.valuesOf(e.full, "row", st.own.GetTo().GetColumns())), f.rt("and", true), f.rt("not", true),
			g.match(f, st.via, st.far.GetFrom().GetColumns(), g.valuesOf(st.to.key, "key", st.far.GetTo().GetColumns())), via)
	}
	f.printf("      }\n")
}
