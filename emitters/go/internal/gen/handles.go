package gen

import (
	"fmt"
	"slices"
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// scopeGroup is the entities that share bound columns, which share a scope
// type and a scoped handle (§21.1): the type of the first of them in IR
// order.
type scopeGroup struct {
	shape *irv1.Shape
	typ   string
	cols  []string
	// types are the bound columns' Go types, by column.
	types map[string]string
}

// covers reports whether a handle of g's scope has every bound column of e,
// so e's operations may take their values from it.
func (sg *scopeGroup) covers(g *generator, f *file, e *entity) bool {
	for _, fld := range e.scope.GetFields() {
		if sg.types[fld.GetColumn()] != g.goType(f, fld.GetType(), e.q, "") {
			return false
		}
	}
	return true
}

// variant is one family of handles: on the executor, in a write
// transaction and in a read transaction (§22.2), with no scope, one scope
// group's, or across scopes (§21).
type variant struct {
	db, wtx, rtx string
	scope        *scopeGroup
	unscoped     bool
	entities     []*entity
	// queries names the variant's Querier, when it has query-file queries.
	queries *querierNames
}

// handles generates dql-db.dql.go: New, the handles of each variant, the
// scoped handles WithScope gives and Unscoped (§21, §22.2, §22.3).
func (g *generator) handles(es []*entity) {
	f := g.newFile("dql-db")
	// Bound columns are compared by Go type, spelled in a file nothing
	// writes, so the comparison imports nothing.
	tf := &file{imports: map[string]string{}}
	var groups []*scopeGroup
	for _, e := range es {
		if e.scope == nil {
			continue
		}
		sg := &scopeGroup{shape: e.scope, typ: Exported(e.scope.GetHostName().GetWords()), types: map[string]string{}}
		for _, fld := range e.scope.GetFields() {
			sg.cols = append(sg.cols, fld.GetColumn())
			sg.types[fld.GetColumn()] = g.goType(tf, fld.GetType(), e.q, "")
		}
		if !slices.ContainsFunc(groups, func(o *scopeGroup) bool { return slices.Equal(o.cols, sg.cols) && o.covers(g, tf, e) }) {
			groups = append(groups, sg)
		}
	}
	root := &variant{db: "DB", wtx: "WriteTx", rtx: "ReadTx"}
	unscoped := &variant{db: "UnscopedDB", wtx: "UnscopedWriteTx", rtx: "UnscopedReadTx", unscoped: true, entities: es}
	variants := []*variant{root}
	for _, sg := range groups {
		v := &variant{db: sg.typ + "d", wtx: sg.typ + "dWriteTx", rtx: sg.typ + "dReadTx", scope: sg}
		for _, e := range es {
			if e.scope == nil || sg.covers(g, tf, e) {
				v.entities = append(v.entities, e)
			}
		}
		variants = append(variants, v)
	}
	for _, e := range es {
		if e.scope == nil {
			root.entities = append(root.entities, e)
		}
	}
	if len(groups) > 0 {
		variants = append(variants, unscoped)
	}
	queries := g.queriers(variants, groups)
	for v, n := range queries {
		v.queries = &n
	}
	if len(queries) > 0 && slices.ContainsFunc(es, func(e *entity) bool { return e.plural == "Queries" }) {
		g.errorf(CodeNameCollision, nil, "a table's store gets the handle field Queries, which the query files' queries have (§26.1); give the table a name annotation")
	}
	f.printf("// New returns the handle on exec: it reads and writes every entity without\n// a scope; a scoped entity needs a handle with its scope (§21.1).\n")
	g.declare("New", "the handle constructor New", nil)
	f.printf("func New(exec *%s.Executor) DB { return newDB(exec) }\n\n", f.use(pkgDQL, "dql"))
	for _, v := range variants {
		g.variant(f, v)
	}
	for _, sg := range groups {
		method := "WithScope"
		if len(groups) > 1 {
			method = "With" + sg.typ
		}
		f.printf("// %s returns the handle with scope s: every read through it is filtered\n// by the scope and every write takes the bound values from it (§21.1).\n", method)
		f.printf("func (db DB) %s(s %s) %s { return new%s(db.exec, s) }\n\n", method, sg.typ, sg.typ+"d", sg.typ+"d")
	}
	if len(groups) > 0 {
		g.declare("Unscoped", "the function Unscoped", nil)
		f.printf("// executorHandle is a handle on the executor: DB or a scoped handle.\ntype executorHandle interface{ executor() *%s.Executor }\n\n", f.use(pkgDQL, "dql"))
		f.printf("// Unscoped returns a handle that reads across scopes; its operations on a\n// scoped entity take the scope as an argument (§21.2).\nfunc Unscoped(h executorHandle) UnscopedDB { return newUnscopedDB(h.executor()) }\n\n")
	}
}

// variant generates one variant's three handles.
func (g *generator) variant(f *file, v *variant) {
	dql := f.use(pkgDQL, "dql")
	ctx := f.use("context", "context")
	scopeParam, scopeArg, scopeField := "", "", ""
	if v.scope != nil {
		scopeParam, scopeArg, scopeField = ", s "+v.scope.typ, ", s", "\n\tscope "+v.scope.typ
	}
	what := "every entity without a scope"
	switch {
	case v.unscoped:
		what = "every entity across scopes"
	case v.scope != nil:
		what = "every entity in scope " + v.scope.typ
	}
	for _, h := range []struct {
		name, held, doc string
		write, exec     bool
	}{
		{v.db, "exec *" + dql + ".Executor", "reads and writes " + what + " through the executor", true, true},
		{v.wtx, "tx *" + dql + ".WriteTx", "reads and writes " + what + " in a write transaction", true, false},
		{v.rtx, "tx *" + dql + ".ReadTx", "reads " + what + " in a read transaction", false, false},
	} {
		g.declare(h.name, "the handle "+h.name, nil)
		f.printf("// %s %s (§22.2).\ntype %s struct {\n\t%s%s\n", h.name, h.doc, h.name, h.held, scopeField)
		for _, e := range v.entities {
			f.printf("\t%s %s\n", e.plural, g.storeIface(e, v, h.write))
		}
		if q := v.queries; q != nil {
			iface := q.read
			if h.write {
				iface = q.write
			}
			f.printf("\t// Queries runs the query files' queries (§17).\n\tQueries %s\n", iface)
		}
		f.printf("}\n\n")
		recv := strings.Fields(h.held)[0]
		f.printf("func new%s(%s%s) %s {\n\treturn %s{\n\t\t%s: %s", h.name, h.held, scopeParam, h.name, h.name, recv, recv)
		if v.scope != nil {
			f.printf(", scope: s")
		}
		for _, e := range v.entities {
			f.printf(",\n\t\t%s: %s", e.plural, g.storeValue(e, v, recv, h.write))
		}
		if q := v.queries; q != nil {
			w := "nil"
			if h.write {
				w = recv
			}
			scope := ""
			if v.scope != nil {
				scope = ", s"
			}
			f.printf(",\n\t\tQueries: %s{%s, %s%s}", q.impl, recv, w, scope)
		}
		f.printf(",\n\t}\n}\n\n")
		r := receiver(splitWords(h.name))
		if h.exec {
			f.printf("func (%s %s) executor() *%s.Executor { return %s.exec }\n\n", r, h.name, dql, r)
			f.printf(`// Write runs fn in a write transaction, which commits when fn returns nil
// and reruns after a serialization failure or deadlock (§22.3, §22.4).
func (%[1]s %[2]s) Write(ctx %[3]s.Context, fn func(%[3]s.Context, %[4]s) error, opts ...%[5]s.TxOption) error {
	return %[1]s.exec.Write(ctx, func(ctx %[3]s.Context, tx *%[5]s.WriteTx) error { return fn(ctx, new%[4]s(tx%[7]s)) }, opts...)
}

// Read runs fn in a read transaction (§22.3, §22.5).
func (%[1]s %[2]s) Read(ctx %[3]s.Context, fn func(%[3]s.Context, %[6]s) error, opts ...%[5]s.TxOption) error {
	return %[1]s.exec.Read(ctx, func(ctx %[3]s.Context, tx *%[5]s.ReadTx) error { return fn(ctx, new%[6]s(tx%[7]s)) }, opts...)
}

// BeginWrite begins a write transaction that the caller commits or rolls
// back, and that is never retried (§22.3).
func (%[1]s %[2]s) BeginWrite(ctx %[3]s.Context, opts ...%[5]s.TxOption) (%[4]s, error) {
	tx, err := %[1]s.exec.BeginWrite(ctx, opts...)
	if err != nil {
		return %[4]s{}, err
	}
	return new%[4]s(tx%[7]s), nil
}

// BeginRead begins a read transaction that the caller commits or rolls back
// (§22.3).
func (%[1]s %[2]s) BeginRead(ctx %[3]s.Context, opts ...%[5]s.TxOption) (%[6]s, error) {
	tx, err := %[1]s.exec.BeginRead(ctx, opts...)
	if err != nil {
		return %[6]s{}, err
	}
	return new%[6]s(tx%[7]s), nil
}

`, r, h.name, ctx, v.wtx, dql, v.rtx, strings.Replace(scopeArg, "s", r+".scope", 1))
			continue
		}
		f.printf(`// Commit commits the transaction a Begin call began.
func (%[1]s %[2]s) Commit(ctx %[3]s.Context) error { return %[1]s.tx.Commit(ctx) }

// Rollback rolls the transaction back; after Commit it does nothing.
func (%[1]s %[2]s) Rollback(ctx %[3]s.Context) error { return %[1]s.tx.Rollback(ctx) }

// Context returns ctx carrying the transaction, so statements on any handle
// given that context join it (§22.3).
func (%[1]s %[2]s) Context(ctx %[3]s.Context) %[3]s.Context { return %[1]s.tx.Context(ctx) }

`, r, h.name, ctx)
	}
}

// storeIface is the interface type of e's store in a handle of v.
func (g *generator) storeIface(e *entity, v *variant, write bool) string {
	write = write && e.writes()
	switch {
	case v.unscoped && e.scope != nil && write:
		return e.unscopedStore()
	case v.unscoped && e.scope != nil:
		return e.unscopedReadStore()
	case write:
		return e.store()
	}
	return e.readStore()
}

// storeValue builds e's store on handle h in a handle of v, its scope taken
// from the handle's.
func (g *generator) storeValue(e *entity, v *variant, h string, write bool) string {
	write = write && e.writes()
	lower := e.lower()
	if v.unscoped && e.scope != nil {
		if write {
			return lower + "Unscoped{" + lower + "UnscopedReads{" + h + "}, " + h + "}"
		}
		return lower + "UnscopedReads{" + h + "}"
	}
	reads := lower + "Reads{" + h
	if e.scope != nil {
		var fields []string
		for _, fld := range e.scope.GetFields() {
			fields = append(fields, fmt.Sprintf("%s: s.%s", g.fieldName(e.scope, fld.GetColumn()), g.fieldName(v.scope.shape, fld.GetColumn())))
		}
		reads += ", " + Exported(e.scope.GetHostName().GetWords()) + "{" + strings.Join(fields, ", ") + "}"
	}
	reads += "}"
	if write {
		return lower + "Writes{" + reads + ", " + h + "}"
	}
	return reads
}
