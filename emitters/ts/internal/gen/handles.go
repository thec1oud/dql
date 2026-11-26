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
	// types are the bound columns' TypeScript types, by column.
	types map[string]string
}

// covers reports whether a handle of sg's scope has every bound column of
// e, so e's operations may take their values from it.
func (sg *scopeGroup) covers(g *generator, f *file, e *entity) bool {
	for _, fld := range e.scope.GetFields() {
		if sg.types[fld.GetColumn()] != g.tsType(f, fld.GetType(), e.q, "") {
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
	// queries names the variant's queriers, when it has query-file queries.
	queries *querierNames
}

// handleMembers are the members every handle class has, which no store
// may take.
var handleMembers = []string{"executor", "tx", "scope", "raw", "write", "read", "beginWrite", "beginRead", "commit", "rollback", "run", "queries"}

// handles generates dql-db.dql.ts: the handle classes of each variant, the
// scoped handles withScope gives and unscoped (§21, §22.2, §22.3).
func (g *generator) handles(es []*entity) {
	f := g.newFile("dql-db")
	// Bound columns are compared by TypeScript type, spelled in a file
	// nothing writes, so the comparison imports nothing.
	tf := &file{runtime: map[string]bool{}, modules: map[string]map[string]bool{}, sideEffects: map[string]bool{}}
	var groups []*scopeGroup
	for _, e := range es {
		if e.scope == nil {
			continue
		}
		sg := &scopeGroup{shape: e.scope, typ: TypeName(e.scope.GetHostName().GetWords()), types: map[string]string{}}
		for _, fld := range e.scope.GetFields() {
			sg.cols = append(sg.cols, fld.GetColumn())
			sg.types[fld.GetColumn()] = g.tsType(tf, fld.GetType(), e.q, "")
		}
		if !slices.ContainsFunc(groups, func(o *scopeGroup) bool { return slices.Equal(o.cols, sg.cols) && o.covers(g, tf, e) }) {
			groups = append(groups, sg)
		}
	}
	root := &variant{db: "Db", wtx: "DbWriteTx", rtx: "DbReadTx"}
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
		if slices.Contains(handleMembers, e.member) || slices.ContainsFunc(groups, func(sg *scopeGroup) bool { return e.member == withScope(sg, groups) }) {
			g.errorf(CodeNameCollision, e.q, "table %s gets the handle field %s, which handles have (§26.1); give the table a name annotation", displayName(e.q), e.member)
		}
	}
	if len(groups) > 0 {
		variants = append(variants, &variant{db: "UnscopedDb", wtx: "UnscopedDbWriteTx", rtx: "UnscopedDbReadTx", unscoped: true, entities: es})
	}
	for v, n := range g.queriers(g.impl, variants, groups) {
		v.queries = &n
	}
	for _, v := range variants {
		g.variant(f, v, v == root, groups)
	}
	if len(groups) > 0 {
		g.declare(f, "unscoped", "the function unscoped", nil)
		f.printf("/** unscoped returns a handle that reads across scopes; its operations on a scoped entity take the scope as an argument (§21.2). */\n")
		f.printf("export function unscoped(h: { readonly executor: %s }): UnscopedDb {\n  return new UnscopedDb(h.executor);\n}\n", f.rt("Executor", false))
	}
}

// variant generates one variant's three handle classes; root's executor
// handle also has the withScope methods of groups.
func (g *generator) variant(f *file, v *variant, root bool, groups []*scopeGroup) {
	scopeType, scopeParam, scopeArg := "", "", ""
	if v.scope != nil {
		scopeType = g.ref(f, v.scope.typ, false)
		scopeParam, scopeArg = ", scope: "+scopeType, ", this.scope"
	}
	what := "every entity without a scope"
	switch {
	case v.unscoped:
		what = "every entity across scopes"
	case v.scope != nil:
		what = "every entity in scope " + v.scope.typ
	}
	for _, h := range []struct {
		name, held, typ, doc string
		write, exec          bool
	}{
		{v.db, "executor", f.rt("Executor", false), "reads and writes " + what + " through the executor", true, true},
		{v.wtx, "tx", f.rt("WriteTx", false), "reads and writes " + what + " in a write transaction", true, false},
		{v.rtx, "tx", f.rt("ReadTx", false), "reads " + what + " in a read transaction", false, false},
	} {
		g.declare(f, h.name, "the handle "+h.name, nil)
		f.printf("/** %s %s (§22.2). */\nexport class %s {\n", h.name, h.doc, h.name)
		if h.exec {
			f.printf("  /** executor is the executor the handle runs on. */\n")
		} else {
			f.printf("  /** tx is the transaction the handle is bound to. */\n")
		}
		f.printf("  readonly %s: %s;\n", h.held, h.typ)
		if v.scope != nil {
			f.printf("  /** scope filters every read and gives every write its bound values (§21.1). */\n  readonly scope: %s;\n", scopeType)
		}
		for _, e := range v.entities {
			f.printf("  readonly %s: %s;\n", e.member, g.ref(f, storeIface(e, v, h.write), false))
		}
		if q := v.queries; q != nil {
			iface := q.read
			if h.write {
				iface = q.write
			}
			f.printf("  /** queries runs the query files' queries (§17). */\n  readonly queries: %s;\n", g.ref(f, iface, false))
		}
		f.printf("\n  constructor(%s: %s%s) {\n    this.%s = %s;\n", h.held, h.typ, scopeParam, h.held, h.held)
		if v.scope != nil {
			f.printf("    this.scope = scope;\n")
		}
		for _, e := range v.entities {
			f.printf("    this.%s = %s;\n", e.member, g.storeValue(f, e, v, h.held, h.write))
		}
		if q := v.queries; q != nil {
			impl := q.readImpl
			if h.write {
				impl = q.writeImpl
			}
			scope := ""
			if v.scope != nil {
				scope = ", scope"
			}
			f.printf("    this.queries = new %s(%s%s);\n", g.ref(f, impl, true), h.held, scope)
		}
		f.printf("  }\n\n")
		// The raw escape hatch, typed by the handle's capability (§17,
		// §22.1, §22.2).
		raw, rawType := f.rt("rawWrite", true), f.rt("RawWriter", false)
		if !h.write {
			raw, rawType = f.rt("raw", true), f.rt("RawReader", false)
		}
		f.printf("  /**\n   * raw runs SQL text on the source named source: on its primary, or in the\n"+
			"   * transaction there that the handle or the context holds, with the\n"+
			"   * driver's values undecoded (§17, §22.1).\n   */\n"+
			"  raw(source: string): %s {\n    return %s(this.%s, source);\n  }\n", rawType, raw, h.held)
		if h.exec {
			opts := f.rt("TxOptions", false)
			f.printf(`
  /**
   * write runs fn in a write transaction, which commits when fn returns and
   * reruns fn after a serialization failure or deadlock (§22.3, §22.4).
   */
  write<T>(fn: (tx: %[2]s) => T | Promise<T>, options?: %[1]s): Promise<T> {
    return this.executor.write((tx) => fn(new %[2]s(tx%[4]s)), options);
  }

  /** read runs fn in a read transaction (§22.3, §22.5). */
  read<T>(fn: (tx: %[3]s) => T | Promise<T>, options?: %[1]s): Promise<T> {
    return this.executor.read((tx) => fn(new %[3]s(tx%[4]s)), options);
  }

  /** beginWrite begins a write transaction that the caller commits or rolls back, and that is never retried (§22.3). */
  async beginWrite(options?: %[1]s): Promise<%[2]s> {
    return new %[2]s(await this.executor.beginWrite(options)%[4]s);
  }

  /** beginRead begins a read transaction that the caller commits or rolls back (§22.3). */
  async beginRead(options?: %[1]s): Promise<%[3]s> {
    return new %[3]s(await this.executor.beginRead(options)%[4]s);
  }
`, opts, v.wtx, v.rtx, scopeArg)
			if root {
				for _, sg := range groups {
					method := withScope(sg, groups)
					f.printf("\n  /**\n   * %s returns the handle with scope s: every read through it is filtered\n   * by the scope and every write takes the bound values from it (§21.1).\n   */\n", method)
					f.printf("  %s(s: %s): %s {\n    return new %s(this.executor, s);\n  }\n", method, g.ref(f, sg.typ, false), sg.typ+"d", sg.typ+"d")
				}
			}
			f.printf("}\n\n")
			continue
		}
		f.printf(`
  /** commit commits the transaction a begin call began; in a closure's transaction it does nothing. */
  commit(): Promise<void> {
    return this.tx.commit();
  }

  /** rollback rolls the transaction back; after commit it does nothing. */
  rollback(): Promise<void> {
    return this.tx.rollback();
  }

  /** run runs fn with the transaction in its context, so statements on any handle inside fn join it (§22.3). */
  run<T>(fn: () => T): T {
    return this.tx.run(fn);
  }
}

`)
	}
}

// storeIface is the interface type of e's store in a handle of v.
func storeIface(e *entity, v *variant, write bool) string {
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

// storeValue builds e's store on the handle's field held in a handle of v,
// its scope taken from the handle's.
func (g *generator) storeValue(f *file, e *entity, v *variant, held string, write bool) string {
	write = write && e.writes()
	if v.unscoped && e.scope != nil {
		if write {
			return fmt.Sprintf("new %s(%s)", g.ref(f, e.unscopedWrites(), true), held)
		}
		return fmt.Sprintf("new %s(%s)", g.ref(f, e.unscopedReads(), true), held)
	}
	args := held
	if e.scope != nil {
		var fields []string
		for _, fld := range e.scope.GetFields() {
			fields = append(fields, g.memberOf(e.scope, fld.GetColumn())+": scope."+g.memberOf(v.scope.shape, fld.GetColumn()))
		}
		args += ", { " + strings.Join(fields, ", ") + " }"
	}
	if write {
		return fmt.Sprintf("new %s(%s)", g.ref(f, e.writer(), true), args)
	}
	return fmt.Sprintf("new %s(%s)", g.ref(f, e.reads(), true), args)
}

// withScope is the name of the method giving the handle of sg's scope:
// withScope, or with and the scope type's name when there are several.
func withScope(sg *scopeGroup, groups []*scopeGroup) string {
	if len(groups) > 1 {
		return "with" + sg.typ
	}
	return "withScope"
}
