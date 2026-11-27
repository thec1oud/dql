package gen

import (
	"fmt"
	"slices"
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// tableType is the type standing for e's table in builder reads, which
// types its predicates and order keys (§18.2).
func (e *entity) tableType() string { return e.plural + "Table" }

// colFragment is the fragment of e's column col.
func (e *entity) colFragment(col string) string {
	return "col." + e.id + "." + col
}

// tableID is how fragment ids name table q: its stored name, behind its
// namespace when that is not its catalog's default, and behind its catalog
// when there are several (§18.4's col.users.email).
func (g *generator) tableID(q *irv1.QualifiedName) string {
	var id []string
	cats := g.b.GetCatalogs()
	if len(cats) > 1 {
		id = append(id, q.GetCatalog())
	}
	i := slices.IndexFunc(cats, func(c *irv1.Catalog) bool { return c.GetName().GetStored() == q.GetCatalog() })
	if i < 0 || q.GetNamespace() != cats[i].GetDefaultNamespace() {
		id = append(id, q.GetNamespace())
	}
	return strings.Join(append(id, q.GetObject()), ".")
}

// selects generates the builder (§18.2): each entity's table type and
// handle, and in dql-select.dql.ts the shapes stores' select methods read.
// The table types are declared first: a relation's handle names its
// target's.
func (g *generator) selects(es []*entity) {
	byTable := map[key]*entity{}
	for _, e := range es {
		byTable[keyOf(e.q)] = e
		g.declare(e.f, e.tableType(), "the table type of "+displayName(e.q), e.q)
		g.declare(e.f, e.member, "the table handle of "+displayName(e.q), e.q)
	}
	for _, e := range es {
		g.tableHandle(e, byTable)
	}
	g.shapeHandles(byTable)
}

// visible is the predicate node the rows of e hold while not soft-deleted
// (§21.3), "" without soft delete, with its fragments imported into f.
func (g *generator) visible(f *file, e *entity) string {
	c := e.softDelete
	if c == nil {
		return ""
	}
	use := f.rt("use", true)
	col := fmt.Sprintf("%s(%s)", use, jsString(e.colFragment(c.GetName().GetStored())))
	if c.GetType().GetBool() != nil {
		return fmt.Sprintf("%s(%s, { \"0\": %s, \"1\": %s(%s) })", use, jsString("op.is_distinct_from"), col, use, jsString("value.true"))
	}
	return fmt.Sprintf("%s(%s, { \"0\": %s })", use, jsString("op.is_null"), col)
}

// tableHandle generates e's table type, a brand standing for it, and a
// constant of its column and relation handles named after the table, as
// users.email.ilike(...) (§9.4, §18.2).
func (g *generator) tableHandle(e *entity, byTable map[key]*entity) {
	f := e.f
	typ := e.tableType()
	pred := f.rt("Pred", false) + "<" + typ + ">"
	var members []string
	// allRows is a member of the handle.
	used := map[string]string{"allRows": "the filter allRows"}
	member := func(name, what string) bool {
		if prev, ok := used[name]; ok {
			g.errorf(CodeNameCollision, e.q, "%s gets the TypeScript name %s in %s, as %s does (§4.3, §15.2); give one of them a name annotation", what, name, e.member, prev)
			return false
		}
		used[name] = what
		return true
	}
	members = append(members, fmt.Sprintf("  /** allRows keeps every row: a filtered write must be given it to write them all (§20.1). */\n  allRows(): %s {\n    return %s<%s>();\n  },\n", pred, f.rt("allRows", true), typ))
	for _, c := range e.t.GetColumns() {
		frag := e.colFragment(c.GetName().GetStored())
		if !g.fragmentIDs[frag] {
			continue
		}
		name := MemberName(c.GetHostName().GetWords())
		if !member(name, "column "+c.GetName().GetStored()) {
			continue
		}
		var methods []string
		for _, op := range c.GetOperators() {
			params, args, ok := g.operands(f, e, c, op.GetName())
			if !ok {
				continue
			}
			doc := ""
			if op.GetFidelity() == irv1.Fidelity_FIDELITY_APPROXIMATE {
				doc = " Approximate: its results can differ from the operator's (§9.2)."
			}
			methods = append(methods, fmt.Sprintf("    /** %s is operator %s on %s (§9.4).%s */\n    %s(%s): %s {\n      return %s<%s>(%s, %s%s);\n    },\n",
				opMethod(op.GetName()), op.GetName(), c.GetName().GetStored(), doc, opMethod(op.GetName()), params, pred,
				f.rt("compare", true), typ, jsString(g.opFragment(op.GetName(), frag)), jsString(frag), args))
		}
		for _, dir := range []string{"asc", "desc"} {
			if g.fragmentIDs["order."+dir] {
				methods = append(methods, fmt.Sprintf("    /** %s orders by %s, %sending. */\n    %s(): %s<%s> {\n      return %s<%s>(%s, %s);\n    },\n",
					dir, c.GetName().GetStored(), dir, dir, f.rt("Order", false), typ, f.rt("ordered", true), typ, jsString(g.orderFragment(c, dir)), jsString(frag)))
			}
		}
		methods = append(methods, g.assignments(f, e, c, frag)...)
		members = append(members, fmt.Sprintf("  /** %s is column %s in builder reads (§9.4). */\n  %s: {\n%s  },\n", name, c.GetName().GetStored(), name, strings.Join(methods, "")))
	}
	for _, r := range e.t.GetRelations() {
		frag := "rel." + e.id + "." + r.GetName().GetStored()
		target := byTable[keyOf(r.GetTo().GetTable())]
		// A relation to the table itself would need its own alias, which
		// the fragment table does not give.
		if !g.fragmentIDs[frag] || target == nil || target == e {
			continue
		}
		name := MemberName(r.GetName().GetWords())
		if !member(name, "relation "+r.GetName().GetStored()) {
			continue
		}
		other := f.rt("Pred", false) + "<" + g.ref(f, target.tableType(), false) + ">"
		related, not := f.rt("related", true), f.rt("not", true)
		// Related rows that are soft-deleted are left out (§21.3).
		ps, live, doc := "...ps", "", ""
		if v := g.visible(f, target); v != "" {
			live = fmt.Sprintf("%s<%s>(%s), ", f.rt("newPred", true), target.tableType(), v)
			ps = live + ps
			doc = " Soft-deleted related rows are left out (§21.3)."
		}
		rel := fmt.Sprintf("%s<%s, %s>", related, typ, target.tableType())
		members = append(members, fmt.Sprintf(`  /** %[1]s is relation %[2]s in builder reads (§18.2).%[3]s */
  %[1]s: {
    /** any holds where a related row exists that every predicate holds for. */
    any(...ps: %[4]s[]): %[5]s {
      return %[6]s(%[7]s, %[8]s);
    },
    /** none holds where no related row exists that every predicate holds for. */
    none(...ps: %[4]s[]): %[5]s {
      return %[9]s(%[6]s(%[7]s, %[8]s));
    },
    /** all holds where p does not fail for any related row; it holds where there are none. */
    all(p: %[4]s): %[5]s {
      return %[9]s(%[6]s(%[7]s, %[10]s%[9]s(p)));
    },
  },
`, name, r.GetName().GetStored(), doc, other, pred, rel, jsString(frag), ps, not, live))
	}
	f.printf("/** %s stands for table %s in builder reads: it types the table's predicates and order keys (§18.2). */\nexport interface %s {\n  readonly __table: %s;\n}\n\n",
		typ, displayName(e.q), typ, jsString(displayName(e.q)))
	f.printf("/** %s is table %s in builder reads (§18.2): its columns and relations make predicates and order keys on its rows. */\nexport const %s = {\n%s} as const;\n\n",
		e.member, displayName(e.q), e.member, strings.Join(members, ""))
}

// assignments returns the methods of column c's handle that assign to it on
// writes (§20.2): set for a column a Draft or Partial carries, and add and
// sub for one a Partial carries that the value operators take.
func (g *generator) assignments(f *file, e *entity, c *irv1.Column, frag string) []string {
	col := c.GetName().GetStored()
	carries := func(s *irv1.Shape) bool {
		fld := shapeField(s, col)
		return fld != nil && fld.GetValueKind() == irv1.ValueKind_VALUE_KIND_VALUE_OR_EXPRESSION
	}
	if !carries(e.draft) && !carries(e.part) || !e.writes() {
		return nil
	}
	typ := e.tableType()
	expr, what := f.rt("Expr", false)+`<"default">`, "DEFAULT"
	switch t := c.GetType(); {
	case t.GetInstant() != nil, t.GetInstantWithOffset() != nil, t.GetLocalDatetime() != nil, t.GetDate() != nil:
		expr, what = f.rt("Expr", false), "now or DEFAULT"
	}
	out := []string{fmt.Sprintf("    /** set assigns %s to %s on a write, in place of a value (§20.2). */\n    set(e: %s): %s<%s> {\n      return %s<%s>(%s, e);\n    },\n",
		what, col, expr, f.rt("Assignment", false), typ, f.rt("set", true), typ, jsString(col))}
	if !carries(e.part) {
		return out
	}
	for _, op := range c.GetOperators() {
		name := op.GetName()
		if (name != "add" && name != "sub") || !g.fragmentIDs["op."+name] {
			continue
		}
		v := g.tsType(f, c.GetType(), e.q, "column "+col)
		sign := map[string]string{"add": "+", "sub": "-"}[name]
		out = append(out, fmt.Sprintf("    /** %s sets %s to %s %s v on an update (§20.2). */\n    %s(v: %s): %s<%s> {\n      return %s<%s>(%s, %s, %s, %s);\n    },\n",
			name, col, col, sign, name, v, f.rt("Update", false), typ, f.rt("arith", true), typ, jsString("op."+name), jsString(col), jsString(frag),
			argNode(f, "v", e.sensitive[col], g.dbType(c.GetType(), false))))
	}
	return out
}

// opMethod is the column handle method of an operator, as ilike or isNull.
func opMethod(name string) string {
	return MemberName(strings.Split(name, "_"))
}

// operands returns the parameters and the argument nodes of operator op on
// column c, when op returns a boolean and the fragment table holds it; a
// plugin's operator is left out (§9.3).
func (g *generator) operands(f *file, e *entity, c *irv1.Column, op string) (string, string, bool) {
	var o *irv1.Operator
	for _, x := range g.b.GetOperators() {
		if x.GetName() == op {
			o = x
		}
	}
	sig := o.GetSignature()
	if o == nil || strings.Contains(op, ".") || !g.fragmentIDs["op."+op] || sig.GetReturns().GetType().GetBool() == nil || len(sig.GetArgs()) == 0 {
		return "", "", false
	}
	// The first operand is the column, which binds the type variable.
	what := "column " + c.GetName().GetStored()
	var boundType *irv1.Type
	switch a := sig.GetArgs()[0]; {
	case a.GetVar() != "":
		boundType = c.GetType()
	case a.GetArray() != nil && c.GetType().GetArray() != nil:
		boundType = c.GetType().GetArray().GetElement()
	case a.GetRange() != nil && c.GetType().GetRange() != nil:
		boundType = c.GetType().GetRange().GetElement()
	case a.GetType() != nil:
	default:
		return "", "", false
	}
	bound := ""
	if boundType != nil {
		bound = g.tsType(f, boundType, e.q, what)
	}
	sensitive := e.sensitive[c.GetName().GetStored()]
	var params, args []string
	for i, a := range sig.GetArgs()[1:] {
		var t, db string
		switch {
		case a.GetVar() != "":
			t, db = bound, g.dbType(boundType, false)
		case a.GetArray().GetVar() != "":
			t, db = "readonly "+parenthesize(bound)+"[]", g.dbType(boundType, true)
		case a.GetRange().GetVar() != "":
			t = f.rt("Range", false) + "<" + bound + ">"
			if c.GetType().GetRange() != nil {
				db = g.dbType(c.GetType(), false)
			}
		case a.GetType() != nil:
			t, db = g.tsType(f, a.GetType(), e.q, what), g.dbType(a.GetType(), false)
		default:
			return "", "", false
		}
		v := fmt.Sprintf("v%d", i)
		if len(sig.GetArgs()) == 2 {
			v = "v"
		}
		params = append(params, v+": "+t)
		args = append(args, ", "+argNode(f, v, sensitive, db))
	}
	return strings.Join(params, ", "), strings.Join(args, ""), true
}

// argNode is a call of the runtime's arg holding v, a value of database type
// db, sensitive or not.
func argNode(f *file, v string, sensitive bool, db string) string {
	switch {
	case db != "":
		return fmt.Sprintf("%s(%s, %t, %s)", f.rt("arg", true), v, sensitive, jsString(db))
	case sensitive:
		return fmt.Sprintf("%s(%s, true)", f.rt("arg", true), v)
	}
	return fmt.Sprintf("%s(%s)", f.rt("arg", true), v)
}

// from is the From a read store of e builds its reads on: a scoped store's
// are filtered by its scope (§21.1), and a soft-delete entity's leave out
// deleted rows unless they ask (§21.3).
func (g *generator) from(f *file, e *entity, scoped bool) string {
	parts := []string{"h: this.h"}
	if scoped && e.scope != nil {
		use := f.rt("use", true)
		var preds []string
		for _, fld := range e.scope.GetFields() {
			v := argNode(f, "this.scope."+g.memberOf(e.scope, fld.GetColumn()), e.sensitive[fld.GetColumn()], g.dbType(fld.GetType(), false))
			preds = append(preds, fmt.Sprintf("%s(%s, { \"0\": %s(%s), \"1\": %s })", use, jsString("op.eq"), use, jsString(e.colFragment(fld.GetColumn())), v))
		}
		parts = append(parts, "scope: ["+strings.Join(preds, ", ")+"]")
	}
	if v := g.visible(f, e); v != "" {
		parts = append(parts, "visible: "+v)
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// selectMethod writes a read store class's select method.
func (g *generator) selectMethod(f *file, e *entity, scoped bool) {
	typ := g.ref(f, e.tableType(), false)
	f.printf("\n  /** select starts a read of shape through the store (§18.2). */\n  select<T>(shape: %s<T, %s>): %s<T, %s> {\n    return %s(%s, %s, shape);\n  }\n",
		f.rt("Shape", false), typ, f.rt("Builder", false), typ, f.rt("newBuilder", true), g.ref(f, "fragments", true), g.from(f, e, scoped))
}

// shapeHandles declares shapes, a member for each read shape the fragment
// table selects (§18.2), in dql-select.dql.ts.
func (g *generator) shapeHandles(byTable map[key]*entity) {
	var shapes []*irv1.Shape
	for _, s := range g.b.GetShapes().GetShapes() {
		if byTable[keyOf(s.GetEntity())] != nil && !writeShape(s) && g.fragmentIDs["select."+g.dotted(s.GetId())] {
			shapes = append(shapes, s)
		}
	}
	if len(shapes) == 0 {
		return
	}
	var types, values []string
	f := g.newFile("dql-select")
	for _, s := range shapes {
		e := byTable[keyOf(s.GetEntity())]
		sel := "select." + g.dotted(s.GetId())
		name := MemberName(s.GetHostName().GetWords())
		st := fmt.Sprintf("%s<%s, %s>", f.rt("Shape", false), g.ref(f, TypeName(s.GetHostName().GetWords()), false), g.ref(f, e.tableType(), false))
		var cols []string
		for _, fld := range s.GetFields() {
			cols = append(cols, jsString(e.colFragment(fld.GetColumn())))
		}
		entity := strings.Join(e.t.GetHostName().GetWords(), "_")
		types = append(types, fmt.Sprintf("  readonly %s: %s;\n", name, st))
		values = append(values, fmt.Sprintf("  %s: {\n    name: %s,\n    catalog: %s,\n    select: %s,\n    from: %s,\n    columns: [%s],\n    count: %s,\n    exists: %s,\n    countName: %s,\n    existsName: %s,\n    decode: %s,\n  },\n",
			name, jsString(entity+".select."+strings.TrimPrefix(g.dotted(s.GetId()), entity+".")), jsString(e.q.GetCatalog()), jsString(sel), jsString("from."+e.id),
			strings.Join(cols, ", "), jsString("count."+e.id), jsString("exists."+e.id), jsString(entity+".count"), jsString(entity+".exists"),
			g.ref(f, "decode"+TypeName(s.GetHostName().GetWords()), true)))
	}
	g.declare(f, "shapes", "the constant shapes", nil)
	f.printf("/** shapes are the read shapes a store's select reads (§18.2). */\nexport const shapes: {\n%s} = {\n%s};\n", strings.Join(types, ""), strings.Join(values, ""))
}

// opFragment returns the fragment of operator op on the column whose
// fragment is col: the dialect's own for that column, such as MySQL's
// typed list for in, or else the operator's.
func (g *generator) opFragment(op, col string) string {
	if id := "op." + op + "." + strings.TrimPrefix(col, "col."); g.fragmentIDs[id] {
		return id
	}
	return "op." + op
}

// orderFragment is the fragment ordering by column c in direction dir: on a
// nullable column, the dialect's order.<dir>_nullable if it has one, which
// places NULLs as Postgres does, and order.<dir> otherwise.
func (g *generator) orderFragment(c *irv1.Column, dir string) string {
	if id := "order." + dir + "_nullable"; c.GetNullable() && g.fragmentIDs[id] {
		return id
	}
	return "order." + dir
}
