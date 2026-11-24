package gen

import (
	"fmt"
	"slices"
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// tableType is the Go type standing for e's table in builder reads, which
// types its predicates and order keys (§18.2).
func (e *entity) tableType() string { return e.plural + "Table" }

// selects generates the builder (§18.2): each entity's table handle, the
// stores' sources, and in dql-select.dql.go Select, Builder and Shapes.
func (g *generator) selects(es []*entity) {
	byTable := map[key]*entity{}
	for _, e := range es {
		byTable[keyOf(e.q)] = e
	}
	for _, e := range es {
		g.tableHandle(e, byTable)
		g.sources(e)
	}
	f := g.newFile("dql-select")
	dql := f.use(pkgDQL, "dql")
	ctx := f.use("context", "context")
	g.declare("Select", "the function Select", nil)
	g.declare("Builder", "the type Builder", nil)
	f.printf(`// source is a store Select reads through: E is its table's type, so a
// shape of one table cannot be read through another's store.
type source[E any] interface{ from(E) %[1]s.From }

// Select starts a read of shape s through store src, filtered by the
// store's scope (§18.2, §21.1).
func Select[T, E any](src source[E], s %[1]s.Shape[T, E]) Builder[T, E] {
	var e E
	return Builder[T, E]{%[1]s.NewBuilder(fragments, src.from(e), s)}
}

// Builder is a read of a shape of rows of E's table (§18.2). Each method
// returns a new builder, so a builder can be reused.
type Builder[T, E any] struct{ b %[1]s.Builder[T, E] }

// Where keeps the rows every predicate holds for, and those of earlier
// calls; combine predicates with dql.And, dql.Or and dql.Not.
func (b Builder[T, E]) Where(ps ...%[1]s.Pred[E]) Builder[T, E] { return Builder[T, E]{b.b.Where(ps...)} }

// OrderBy orders the rows by keys, after those of earlier calls.
func (b Builder[T, E]) OrderBy(keys ...%[1]s.Order[E]) Builder[T, E] {
	return Builder[T, E]{b.b.OrderBy(keys...)}
}

// Limit returns at most n rows.
func (b Builder[T, E]) Limit(n int64) Builder[T, E] { return Builder[T, E]{b.b.Limit(n)} }

// Offset skips the first n rows.
func (b Builder[T, E]) Offset(n int64) Builder[T, E] { return Builder[T, E]{b.b.Offset(n)} }

// NoLog turns query events off for this read (§24).
func (b Builder[T, E]) NoLog() Builder[T, E] { return Builder[T, E]{b.b.NoLog()} }

// Consistency sets how fresh the read must be (§22.5).
func (b Builder[T, E]) Consistency(c %[1]s.Consistency) Builder[T, E] {
	return Builder[T, E]{b.b.Consistency(c)}
}

`, dql)
	// The dialect's optional clauses, each generated only where its table
	// holds the fragment (§18.2).
	for _, m := range []struct{ fragment, name, doc string }{
		{"select.distinct", "Distinct", "returns each distinct row once"},
		{"lock.update", "ForUpdate", "locks the rows read for update; the read runs on the primary"},
		{"lock.share", "ForShare", "locks the rows read for share; the read runs on the primary"},
		{"lock.skip_locked", "SkipLocked", "skips the rows another transaction locked"},
		{"lock.nowait", "NoWait", "fails on rows another transaction locked"},
	} {
		if g.fragmentIDs[m.fragment] {
			f.printf("// %s %s.\nfunc (b Builder[T, E]) %s() Builder[T, E] { return Builder[T, E]{b.b.%s()} }\n\n", m.name, m.doc, m.name, m.name)
		}
	}
	f.printf(`// One returns the one row: NotFound for none and TooManyRows for more (§19).
func (b Builder[T, E]) One(ctx %[2]s.Context) (T, error) { return b.b.One(ctx) }

// OptionalOne returns the row, if any: TooManyRows for more than one (§19).
func (b Builder[T, E]) OptionalOne(ctx %[2]s.Context) (%[3]s.Opt[T], error) { return b.b.OptionalOne(ctx) }

// All returns every row, an empty slice for none (§19).
func (b Builder[T, E]) All(ctx %[2]s.Context) ([]T, error) { return b.b.All(ctx) }

// Iter streams the rows, holding a connection until iteration ends (§25.1).
func (b Builder[T, E]) Iter(ctx %[2]s.Context) %[4]s.Seq2[T, error] { return b.b.Iter(ctx) }

// Count returns how many rows the filter keeps.
func (b Builder[T, E]) Count(ctx %[2]s.Context) (int64, error) { return b.b.Count(ctx) }

// Exists reports whether the filter keeps any row.
func (b Builder[T, E]) Exists(ctx %[2]s.Context) (bool, error) { return b.b.Exists(ctx) }

`, dql, ctx, f.use(pkgOpt, "opt"), f.use("iter", "iter"))
	g.shapeHandles(f, byTable)
}

// shapeHandles declares Shapes, a field for each read shape the fragment
// table selects (§18.2).
func (g *generator) shapeHandles(f *file, byTable map[key]*entity) {
	dql := f.use(pkgDQL, "dql")
	var fields, values []string
	for _, s := range g.b.GetShapes().GetShapes() {
		e := byTable[keyOf(s.GetEntity())]
		sel := "select." + g.dotted(s.GetId())
		if e == nil || writeShape(s) || !g.fragmentIDs[sel] {
			continue
		}
		typ := Exported(s.GetHostName().GetWords())
		st := fmt.Sprintf("%s.Shape[%s, %s]", dql, typ, e.tableType())
		var cols []string
		for _, fld := range s.GetFields() {
			cols = append(cols, fmt.Sprintf("%q", e.colFragment(fld.GetColumn())))
		}
		table := e.id
		entity := strings.Join(e.t.GetHostName().GetWords(), "_")
		fields = append(fields, fmt.Sprintf("\t%s %s\n", typ, st))
		values = append(values, fmt.Sprintf("\t%s: %s{\n\t\tName: %q,\n\t\tCatalog: %q,\n\t\tSelect: %q,\n\t\tFrom: %q,\n\t\tColumns: []string{%s},\n\t\tCount: %q,\n\t\tExists: %q,\n\t\tCountName: %q,\n\t\tExistsName: %q,\n\t\tDecode: Decode%s,\n\t},\n",
			typ, st, entity+".select."+strings.TrimPrefix(g.dotted(s.GetId()), entity+"."), e.q.GetCatalog(), sel, "from."+table, strings.Join(cols, ", "), "count."+table, "exists."+table, entity+".count", entity+".exists", typ))
	}
	if len(fields) == 0 {
		return
	}
	g.declare("Shapes", "the variable Shapes", nil)
	f.printf("// Shapes are the read shapes Select reads (§18.2).\nvar Shapes = struct {\n%s}{\n%s}\n", strings.Join(fields, ""), strings.Join(values, ""))
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

// colFragment is the fragment of e's column col.
func (e *entity) colFragment(col string) string {
	return "col." + e.id + "." + col
}

// tableHandle generates e's table type, a variable of it named after the
// table, and the handles of its columns and relations (§9.4, §18.2).
func (g *generator) tableHandle(e *entity, byTable map[key]*entity) {
	f := e.f
	dql := f.use(pkgDQL, "dql")
	typ := e.tableType()
	g.declare(typ, "the table type of "+displayName(e.q), e.q)
	g.declare(e.plural, "the table handle of "+displayName(e.q), e.q)
	var fields []string
	var decls strings.Builder
	// AllRows is a method of the table type.
	used := map[string]string{"AllRows": "the filter AllRows"}
	field := func(name, handle, what string) bool {
		if prev, ok := used[name]; ok {
			g.errorf(CodeNameCollision, e.q, "%s gets the Go name %s in %s, as %s does (§4.3, §15.2); give one of them a name annotation", what, name, typ, prev)
			return false
		}
		used[name] = what
		g.declare(handle, what+"'s handle", e.q)
		fields = append(fields, fmt.Sprintf("\t%s %s\n", name, handle))
		return true
	}
	pred := fmt.Sprintf("%s.Pred[%s]", dql, typ)
	for _, c := range e.t.GetColumns() {
		frag := e.colFragment(c.GetName().GetStored())
		if !g.fragmentIDs[frag] {
			continue
		}
		name := Exported(c.GetHostName().GetWords())
		handle := e.lower() + name + "Column"
		if !field(name, handle, "column "+c.GetName().GetStored()) {
			continue
		}
		fmt.Fprintf(&decls, "// %s is column %s in builder reads (§9.4).\ntype %s struct{}\n\n", handle, c.GetName().GetStored(), handle)
		for _, op := range c.GetOperators() {
			params, args, ok := g.operands(f, e, c, op.GetName())
			if !ok {
				continue
			}
			doc := ""
			if op.GetFidelity() == irv1.Fidelity_FIDELITY_APPROXIMATE {
				doc = "\n//\n// Approximate: its results can differ from the operator's (§9.2)."
			}
			fmt.Fprintf(&decls, "// %s is operator %s on %s (§9.4).%s\nfunc (%s) %s(%s) %s {\n\treturn %s.Compare[%s](%q, %q%s)\n}\n\n",
				opMethod(op.GetName()), op.GetName(), c.GetName().GetStored(), doc,
				handle, opMethod(op.GetName()), params, pred, dql, typ, "op."+op.GetName(), frag, args)
		}
		for _, dir := range []string{"asc", "desc"} {
			if g.fragmentIDs["order."+dir] {
				fmt.Fprintf(&decls, "// %s orders by %s, %sending.\nfunc (%s) %s() %s.Order[%s] { return %s.Ordered[%s](%q, %q) }\n\n",
					capitalize(dir), c.GetName().GetStored(), dir, handle, capitalize(dir), dql, typ, dql, typ, "order."+dir, frag)
			}
		}
	}
	for _, r := range e.t.GetRelations() {
		frag := "rel." + e.id + "." + r.GetName().GetStored()
		target := byTable[keyOf(r.GetTo().GetTable())]
		// A relation to the table itself would need its own alias, which
		// the fragment table does not give.
		if !g.fragmentIDs[frag] || target == nil || target == e {
			continue
		}
		name := Exported(r.GetName().GetWords())
		handle := e.lower() + name + "Relation"
		if !field(name, handle, "relation "+r.GetName().GetStored()) {
			continue
		}
		other := fmt.Sprintf("%s.Pred[%s]", dql, target.tableType())
		fmt.Fprintf(&decls, `// %[1]s is relation %[2]s in builder reads (§18.2).
type %[1]s struct{}

// Any holds where a related row exists that every predicate holds for.
func (%[1]s) Any(ps ...%[3]s) %[4]s { return %[5]s.Related[%[6]s](%[7]q, ps...) }

// None holds where no related row exists that every predicate holds for.
func (%[1]s) None(ps ...%[3]s) %[4]s { return %[5]s.Not(%[5]s.Related[%[6]s](%[7]q, ps...)) }

// All holds where p does not fail for any related row; it holds where
// there are none.
func (%[1]s) All(p %[3]s) %[4]s { return %[5]s.Not(%[5]s.Related[%[6]s](%[7]q, %[5]s.Not(p))) }

`, handle, r.GetName().GetStored(), other, pred, dql, typ, frag)
	}
	f.printf("// %s is table %s in builder reads (§18.2): its columns and relations\n// make predicates and order keys on its rows.\ntype %s struct {\n%s}\n\n", typ, displayName(e.q), typ, strings.Join(fields, ""))
	f.printf("// %s is table %s in builder reads (§18.2).\nvar %s %s\n\n", e.plural, displayName(e.q), e.plural, typ)
	f.printf("// AllRows keeps every row: a filtered write must be given it to write them\n// all (§20.1).\nfunc (%s) AllRows() %s { return %s.AllRows[%s]() }\n\n%s", typ, pred, dql, typ, decls.String())
}

// opMethod is the column handle method of an operator.
func opMethod(name string) string {
	if name == "ilike" {
		return "ILike"
	}
	return Exported(strings.Split(name, "_"))
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
	var bound string
	switch a := sig.GetArgs()[0]; {
	case a.GetVar() != "":
		bound = g.goType(f, c.GetType(), e.q, what)
	case a.GetArray() != nil && c.GetType().GetArray() != nil:
		bound = strings.TrimPrefix(g.goType(f, c.GetType(), e.q, what), "[]")
	case a.GetRange() != nil && c.GetType().GetRange() != nil:
		bound = g.goType(f, c.GetType().GetRange().GetElement(), e.q, what)
	case a.GetType() != nil:
	default:
		return "", "", false
	}
	dql := f.use(pkgDQL, "dql")
	sensitive := ""
	if e.sensitive[c.GetName().GetStored()] {
		sensitive = ", Sensitive: true"
	}
	var params, args []string
	for i, a := range sig.GetArgs()[1:] {
		var t string
		switch {
		case a.GetVar() != "":
			t = bound
		case a.GetArray().GetVar() != "":
			t = "[]" + bound
		case a.GetRange().GetVar() != "":
			t = dql + ".Range[" + bound + "]"
		case a.GetType() != nil:
			t = g.goType(f, a.GetType(), e.q, what)
		default:
			return "", "", false
		}
		v := fmt.Sprintf("v%d", i)
		if len(sig.GetArgs()) == 2 {
			v = "v"
		}
		params = append(params, v+" "+t)
		args = append(args, fmt.Sprintf(", %s.Arg{Value: %s%s}", dql, v, sensitive))
	}
	return strings.Join(params, ", "), strings.Join(args, ""), true
}

// sources generates the method that makes each of e's read stores a source
// of Select: a scoped store's reads are filtered by its scope (§21.1).
func (g *generator) sources(e *entity) {
	f := e.f
	dql := f.use(pkgDQL, "dql")
	var preds []string
	for _, fld := range e.scope.GetFields() {
		sensitive := ""
		if e.sensitive[fld.GetColumn()] {
			sensitive = ", Sensitive: true"
		}
		preds = append(preds, fmt.Sprintf("%s.Compare[%s](%q, %q, %s.Arg{Value: s.scope.%s%s}).Node()",
			dql, e.tableType(), "op.eq", e.colFragment(fld.GetColumn()), dql, g.fieldName(e.scope, fld.GetColumn()), sensitive))
	}
	scope := ""
	if len(preds) > 0 {
		scope = fmt.Sprintf(", Scope: []%s.Node{%s}", dql, strings.Join(preds, ", "))
	}
	f.printf("func (s %sReads) from(%s) %s.From { return %s.From{H: s.h%s} }\n\n", e.lower(), e.tableType(), dql, dql, scope)
	if e.scope != nil {
		f.printf("func (u %sUnscopedReads) from(%s) %s.From { return %s.From{H: u.h} }\n\n", e.lower(), e.tableType(), dql, dql)
	}
}
