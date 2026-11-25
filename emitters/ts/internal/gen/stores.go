package gen

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Diagnostic codes of the query surface.
const (
	// CodeQueryUnsupported: a builder query whose operation this emitter
	// does not generate, from a newer core (§16.3). It is left out.
	CodeQueryUnsupported = "ts-query-unsupported"
	// CodeFragmentUnknown: a plan names a fragment the table lacks (§16.2).
	CodeFragmentUnknown = "ts-fragment-unknown"
)

// verbs are the operations of builder queries (§26.2), in the order the
// stores write their methods.
var verbs = []string{"get", "find", "count", "exists", "insert", "insert_or_ignore", "upsert", "upsert_set", "patch", "update_where", "delete", "delete_where", "hard_delete", "hard_delete_where"}

// generated are the operations the stores generate methods for. The core
// derives the others for every entity too; they are left out without a
// warning.
var generated = map[string]bool{"get": true, "find": true, "count": true, "exists": true, "insert": true, "insert_or_ignore": true, "upsert": true, "upsert_set": true, "patch": true, "update_where": true, "delete": true, "delete_where": true, "hard_delete": true, "hard_delete_where": true}

// reads are the operations a read handle has (§22.2).
var reads = map[string]bool{"get": true, "find": true, "count": true, "exists": true}

// byKey are the operations that take an entity's Key when they name its
// primary key, or no key.
var byKey = map[string]bool{"get": true, "find": true, "patch": true, "delete": true, "hard_delete": true}

// entry is one builder query of an entity.
type entry struct {
	q    *irv1.Query
	verb string
	// by is the words of the key or uniqueness fact the operation names, nil
	// for the primary key or none.
	by []string
	// v is the module-level constant holding the query.
	v string
	// shape is the custom write shape an insert or patch takes in place of
	// the Draft or Partial (§14.8), nil for the presets.
	shape *irv1.Shape
}

// method is the store method of an entry: its verb, and By and the key's
// words when it names a key other than the primary, as getByTenantIdAndEmail.
func (x entry) method() string {
	words := strings.Split(x.verb, "_")
	if x.by != nil {
		words = append(append(words, "by"), x.by...)
	}
	return MemberName(words)
}

// entity is a table's builder queries and the shapes its store takes.
type entity struct {
	q *irv1.QualifiedName
	t *irv1.Table
	// f is the entity's file, which holds its store interfaces.
	f *file
	// plural names the store types, from the table's own name, as the
	// record's h.users does (§26.1); member names the handles' field.
	plural, member string
	// id names the table in fragment ids (§18.4).
	id    string
	full  *irv1.Shape
	key   *irv1.Shape
	draft *irv1.Shape
	part  *irv1.Shape
	scope *irv1.Shape
	// versioned: the Key holds an expected version (§20.4).
	versioned bool
	// softDelete is the entity's soft-delete column, if any (§21.3).
	softDelete *irv1.Column
	// sensitive holds the entity's sensitive columns (§24).
	sensitive map[string]bool
	entries   map[string]entry
	order     []entry
}

func (e *entity) writes() bool {
	return slices.ContainsFunc(e.order, func(x entry) bool { return !reads[x.verb] })
}

// Names of an entity's store types: the interfaces in its file, and their
// implementations in dql-stores.dql.ts.
func (e *entity) readStore() string         { return e.plural + "ReadStore" }
func (e *entity) store() string             { return e.plural + "Store" }
func (e *entity) unscopedReadStore() string { return e.plural + "UnscopedReadStore" }
func (e *entity) unscopedStore() string     { return e.plural + "UnscopedStore" }
func (e *entity) reads() string             { return e.plural + "Reads" }
func (e *entity) writer() string            { return e.plural + "Writes" }
func (e *entity) unscopedReads() string     { return e.plural + "UnscopedReads" }
func (e *entity) unscopedWrites() string    { return e.plural + "UnscopedWrites" }

// entryName is the name of an entity's entry of verb by the primary key.
func (e *entity) entryName(verb string) string {
	return strings.Join(e.t.GetHostName().GetWords(), "_") + "." + verb + ".by_primary"
}

// entities collects the builder queries of each table in IR order.
func (g *generator) entities() []*entity {
	byTable := map[key]*entity{}
	var out []*entity
	g.eachNamespace(func(q func(string) *irv1.QualifiedName, ns *irv1.Namespace) {
		for _, t := range ns.GetTables() {
			e := &entity{q: q(t.GetName().GetStored()), t: t, id: g.tableID(q(t.GetName().GetStored())), plural: TypeName(t.GetName().GetWords()), member: MemberName(t.GetName().GetWords()),
				sensitive: map[string]bool{}, entries: map[string]entry{}}
			preset := func(p irv1.Preset, form irv1.Form) *irv1.Shape {
				return g.ix.shapes[shapeKey(&irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: e.q}, Preset: p, Form: form})]
			}
			e.full, e.key, e.scope = preset(irv1.Preset_PRESET_FULL, irv1.Form_FORM_FLAT), preset(irv1.Preset_PRESET_KEY, 0), preset(irv1.Preset_PRESET_SCOPE, 0)
			e.draft, e.part = preset(irv1.Preset_PRESET_DRAFT, irv1.Form_FORM_FLAT), preset(irv1.Preset_PRESET_PARTIAL, irv1.Form_FORM_FLAT)
			for _, fld := range e.key.GetFields() {
				e.versioned = e.versioned || fld.GetRole() == irv1.FieldRole_FIELD_ROLE_EXPECTED_VERSION
			}
			for _, c := range t.GetColumns() {
				if slices.ContainsFunc(c.GetAnnotations(), func(a *irv1.Annotation) bool { return a.GetKey() == "core.soft-delete" }) {
					e.softDelete = c
				}
			}
			byTable[keyOf(e.q)] = e
			out = append(out, e)
		}
	})
	for _, s := range g.b.GetShapes().GetShapes() {
		if e := byTable[keyOf(s.GetEntity())]; e != nil {
			for _, fld := range s.GetFields() {
				e.sensitive[fld.GetColumn()] = e.sensitive[fld.GetColumn()] || fld.GetRedact()
			}
		}
	}
	for _, q := range g.b.GetQueries().GetQueries() {
		if q.GetSource() != irv1.QuerySource_QUERY_SOURCE_BUILDER || len(q.GetTables()) == 0 {
			continue
		}
		e := byTable[keyOf(q.GetTables()[0])]
		x, ok := g.parseEntry(e, q)
		if !ok {
			g.diags = append(g.diags, &irv1.Diagnostic{Severity: irv1.Severity_SEVERITY_WARNING, Code: CodeQueryUnsupported, Target: q.GetTables()[0],
				Message: fmt.Sprintf("builder query %s is an operation the ts emitter does not generate; it is left out", q.GetName().GetStored())})
			continue
		}
		if !generated[x.verb] || x.shape != nil {
			continue
		}
		x.v = MemberName(strings.FieldsFunc(q.GetName().GetStored(), func(r rune) bool { return r == '.' || r == '_' }))
		e.entries[q.GetName().GetStored()] = x
	}
	for _, e := range out {
		for _, name := range slices.Sorted(maps.Keys(e.entries)) {
			e.order = append(e.order, e.entries[name])
		}
		slices.SortStableFunc(e.order, func(a, b entry) int { return slices.Index(verbs, a.verb) - slices.Index(verbs, b.verb) })
	}
	out = slices.DeleteFunc(out, func(e *entity) bool { return len(e.order) == 0 })
	for _, e := range out {
		e.f = g.entityFile(e.q)
	}
	return out
}

// parseEntry reads an entry's name: the entity's words joined by _, the
// operation, and by_ with the key's words, as user.get.by_primary or
// post.upsert.by_posts_one_published_slug, or the custom write shape an
// insert or patch takes, as user.insert.quick_invite (§14.8).
func (g *generator) parseEntry(e *entity, q *irv1.Query) (entry, bool) {
	parts := strings.Split(q.GetName().GetStored(), ".")
	if e == nil || len(parts) < 2 || len(parts) > 3 || parts[0] != strings.Join(e.t.GetHostName().GetWords(), "_") || !slices.Contains(verbs, parts[1]) {
		return entry{}, false
	}
	x := entry{q: q, verb: parts[1]}
	if len(parts) == 3 {
		by, ok := strings.CutPrefix(parts[2], "by_")
		if !ok {
			x.shape = g.ix.shapes[shapeKey(&irv1.ShapeId{Base: &irv1.ShapeId_Custom{Custom: parts[2]}})]
			return x, x.shape != nil && (x.verb == "insert" || x.verb == "patch")
		}
		if by != "primary" {
			x.by = strings.Split(by, "_")
		}
	}
	return x, true
}

// stores generates each entity's store interfaces into its file (§26.1,
// §26.3), and its queries and the classes implementing the interfaces into
// dql-stores.dql.ts, which the index leaves out: handles construct them.
func (g *generator) stores(es []*entity) {
	f := g.newFile("dql-stores")
	f.internal = true
	g.impl = f
	for _, e := range es {
		g.storeIfaces(e)
	}
	for _, e := range es {
		g.queryConsts(f, e)
		g.storeClasses(f, e)
	}
}

// queryConsts declares each entry as a Query (§16.3).
func (g *generator) queryConsts(f *file, e *entity) {
	query := f.rt("Query", false)
	f.printf("// The builder queries of %s (§16.3).\n\n", displayName(e.q))
	for _, x := range e.order {
		q := x.q
		g.declare(f, x.v, "builder query "+q.GetName().GetStored(), e.q)
		f.printf("const %s: %s = {\n  name: %s,\n  catalog: %s,\n  kind: %s,\n", x.v, query, jsString(q.GetName().GetStored()), jsString(q.GetCatalog()), jsString(x.verb))
		if q.GetWrites() {
			f.printf("  writes: true,\n")
		}
		if q.GetAtMostOne() {
			f.printf("  atMostOne: true,\n")
		}
		if c := g.consistency(f, q.GetConsistency()); c != "" {
			f.printf("  consistency: %s,\n", c)
		}
		var sensitive []string
		for _, p := range q.GetParams() {
			if e.sensitive[paramColumn(p.GetName())] {
				sensitive = append(sensitive, jsString(p.GetName())+": true")
			}
		}
		if len(sensitive) > 0 {
			f.printf("  sensitive: { %s },\n", strings.Join(sensitive, ", "))
		}
		var types []string
		for _, p := range q.GetParams() {
			if t := g.dbType(p.GetType(), p.GetList()); t != "" {
				types = append(types, jsString(p.GetName())+": "+jsString(t))
			}
		}
		for _, fld := range e.scope.GetFields() {
			name := "scope." + fld.GetColumn()
			if t := g.dbType(fld.GetType(), false); t != "" && usesParam(q.GetPlan().GetParts(), name) {
				types = append(types, jsString(name)+": "+jsString(t))
			}
		}
		if len(types) > 0 {
			f.printf("  types: { %s },\n", strings.Join(types, ", "))
		}
		f.printf("  plan: [\n")
		for _, u := range q.GetPlan().GetParts() {
			f.printf("    %s,\n", g.use(f, u, e.q))
		}
		f.printf("  ],\n};\n\n")
	}
}

// usesParam reports whether parameter name fills a hole of the fragment
// uses.
func usesParam(uses []*irv1.FragmentUse, name string) bool {
	for _, u := range uses {
		for _, h := range u.GetHoles() {
			if h.GetParam() == name || usesParam([]*irv1.FragmentUse{h.GetFragment()}, name) {
				return true
			}
			for _, it := range h.GetList().GetItems() {
				if it.GetParam() == name || usesParam([]*irv1.FragmentUse{it.GetFragment()}, name) {
					return true
				}
			}
		}
	}
	return false
}

// paramColumn is the column a parameter of a builder query fills: key.id
// and scope.tenant_id name theirs after the prefix.
func paramColumn(name string) string {
	if i := strings.IndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// use writes a fragment use as a call of the runtime's use.
func (g *generator) use(f *file, u *irv1.FragmentUse, at *irv1.QualifiedName) string {
	use := f.rt("use", true)
	if !g.fragmentIDs[u.GetFragment()] {
		g.errorf(CodeFragmentUnknown, at, "a builder query uses fragment %s, which the fragment table does not hold", u.GetFragment())
	}
	if len(u.GetHoles()) == 0 {
		return fmt.Sprintf("%s(%s)", use, jsString(u.GetFragment()))
	}
	var holes []string
	for _, h := range u.GetHoles() {
		var v string
		switch fill := h.GetValue().(type) {
		case *irv1.HoleFill_Fragment:
			v = g.use(f, fill.Fragment, at)
		case *irv1.HoleFill_Param:
			v = fmt.Sprintf("%s(%s)", f.rt("param", true), jsString(fill.Param))
		case *irv1.HoleFill_List:
			var items []string
			for _, it := range fill.List.GetItems() {
				if p, ok := it.GetValue().(*irv1.ListItem_Param); ok {
					items = append(items, fmt.Sprintf("%s(%s)", f.rt("param", true), jsString(p.Param)))
				} else {
					items = append(items, g.use(f, it.GetFragment(), at))
				}
			}
			v = fmt.Sprintf("%s([%s], %s)", f.rt("list", true), strings.Join(items, ", "), jsString(fill.List.GetSeparator()))
		}
		holes = append(holes, jsString(h.GetHole())+": "+v)
	}
	return fmt.Sprintf("%s(%s, { %s })", use, jsString(u.GetFragment()), strings.Join(holes, ", "))
}

// param is one parameter of a store method; rest gathers the remaining
// arguments, as ...as.
type param struct {
	name, typ string
	rest      bool
}

// signature is one store method: its name, parameters and result.
type signature struct {
	name   string
	params []param
	// result is what the method's promise resolves to.
	result string
	x      entry
	// many: insertMany, which runs the insert entry for each Draft.
	many bool
}

// list spells the parameters, the scope's first when scope is not "".
func (s signature) list(scope string) string {
	var ps []string
	if scope != "" {
		ps = append(ps, "scope: "+scope)
	}
	for _, p := range s.params {
		if p.rest {
			ps = append(ps, "..."+p.name+": "+p.typ)
			continue
		}
		ps = append(ps, p.name+": "+p.typ)
	}
	return strings.Join(ps, ", ")
}

// args spells the parameters' names, as arguments passing them on.
func (s signature) args() string {
	var names []string
	for _, p := range s.params {
		if p.rest {
			names = append(names, "..."+p.name)
			continue
		}
		names = append(names, p.name)
	}
	return strings.Join(names, ", ")
}

// locals are the names a method body binds or takes from the runtime, which
// a parameter named after a column must not take.
var locals = map[string]bool{"k": true, "d": true, "p": true, "ds": true, "as": true, "q": true, "where": true, "rows": true, "args": true, "found": true, "set": true, "scope": true, "fragments": true}

// keyParam is the parameter of a key column a lookup by another key takes.
func keyParam(c *irv1.Column) string {
	name := LocalName(c.GetHostName().GetWords())
	if locals[name] || strings.HasPrefix(name, "decode") || strings.HasPrefix(name, "validate") || runtimeFuncs[name] {
		name += "_"
	}
	return name
}

// runtimeFuncs are the runtime's functions a store method calls.
var runtimeFuncs = map[string]bool{"assign": true, "count": true, "execByKey": true, "execute": true, "exists": true, "filtered": true, "insertMany": true, "oneOf": true, "optionalOf": true, "statementOf": true, "throwIfIssues": true, "versionedExec": true, "versionedOne": true}

// signatures returns the entity's store methods, reads first, with their
// types imported into f.
func (g *generator) signatures(f *file, e *entity) []signature {
	full := g.ref(f, TypeName(e.full.GetHostName().GetWords()), false)
	draft := g.ref(f, TypeName(e.draft.GetHostName().GetWords()), false)
	tbl := g.ref(f, e.tableType(), false)
	values := param{"as", f.rt("Assignment", false) + "<" + tbl + ">[]", true}
	updates := param{"as", f.rt("Update", false) + "<" + tbl + ">[]", true}
	where := param{"where", f.rt("Pred", false) + "<" + tbl + ">", false}
	var out []signature
	for _, x := range e.order {
		s := signature{name: x.method(), x: x}
		switch {
		case byKey[x.verb] && x.by == nil:
			s.params = []param{{name: "k", typ: g.ref(f, TypeName(e.key.GetHostName().GetWords()), false)}}
		case x.verb == "get" || x.verb == "find":
			for _, p := range x.q.GetParams() {
				if c, ok := strings.CutPrefix(p.GetName(), "key."); ok {
					s.params = append(s.params, param{name: keyParam(column(e.t, c)), typ: g.tsType(f, p.GetType(), e.q, "parameter "+p.GetName())})
				}
			}
		}
		part := func() param { return param{"p", g.ref(f, TypeName(e.part.GetHostName().GetWords()), false), false} }
		switch x.verb {
		case "insert", "insert_or_ignore", "upsert":
			s.params = []param{{"d", draft, false}, values}
		case "upsert_set":
			s.params = []param{{"d", draft, false}, part(), updates}
		case "patch":
			s.params = append(s.params, part(), updates)
		case "update_where":
			s.params = []param{where, part(), updates}
		case "delete_where", "hard_delete_where":
			s.params = []param{where}
		}
		switch x.verb {
		case "get", "insert", "upsert", "upsert_set", "patch":
			s.result = full
		case "find", "insert_or_ignore":
			s.result = full + " | undefined"
		case "count":
			s.result = "bigint"
		case "exists":
			s.result = "boolean"
		case "update_where", "delete_where", "hard_delete_where":
			s.result = "number"
		default:
			s.result = "void"
		}
		out = append(out, s)
		if x.verb == "insert" {
			out = append(out, signature{name: "insertMany", params: []param{{"ds", "readonly " + draft + "[]", false}, values}, result: full + "[]", x: x, many: true})
		}
	}
	return out
}

// storeIfaces generates the store interfaces into the entity's file: a read
// store and a store that also writes, and for a scoped entity the unscoped
// forms, whose methods take the scope (§21.2, §26.3).
func (g *generator) storeIfaces(e *entity) {
	f := e.f
	sigs := g.signatures(f, e)
	what := displayName(e.q)
	scopeType := ""
	if e.scope != nil {
		scopeType = g.ref(f, TypeName(e.scope.GetHostName().GetWords()), false)
	}
	iface := func(name, doc, extends string, write bool, scope string) {
		g.declare(f, name, "the store interface "+name, e.q)
		if extends != "" {
			extends = " extends " + extends
		}
		f.printf("/** %s %s %s (§26.3). */\nexport interface %s%s {\n", name, doc, what, name, extends)
		if extends == "" {
			// Builder reads go through the store (§18.2).
			t := g.ref(f, e.tableType(), false)
			f.printf("  select<T>(shape: %s<T, %s>): %s<T, %s>;\n", f.rt("Shape", false), t, f.rt("Builder", false), t)
		}
		for _, s := range sigs {
			if reads[s.x.verb] != write {
				f.printf("  %s(%s): Promise<%s>;\n", s.name, s.list(scope), s.result)
			}
		}
		f.printf("}\n\n")
	}
	iface(e.readStore(), "reads", "", false, "")
	if e.writes() {
		iface(e.store(), "reads and writes", e.readStore(), true, "")
	}
	if e.scope != nil {
		iface(e.unscopedReadStore(), "reads, across scopes,", "", false, scopeType)
		if e.writes() {
			iface(e.unscopedStore(), "reads and writes, across scopes,", e.unscopedReadStore(), true, scopeType)
		}
	}
}

// storeClasses generates the classes implementing the entity's store
// interfaces into f.
func (g *generator) storeClasses(f *file, e *entity) {
	sigs := g.signatures(f, e)
	what := displayName(e.q)
	reader, writer := f.rt("Reader", false), f.rt("Writer", false)
	scopeType, scopeField, scopeParam, scopeArg := "", "", "", ""
	if e.scope != nil {
		scopeType = g.ref(f, TypeName(e.scope.GetHostName().GetWords()), false)
		scopeField, scopeParam, scopeArg = "  readonly scope: "+scopeType+";\n", ", scope: "+scopeType, ", scope"
	}
	ref := func(name string) string { return g.ref(f, name, false) }

	g.declare(f, e.reads(), "the read store of "+what, e.q)
	f.printf("/** %s implements %s on a handle that reads. */\nexport class %s implements %s {\n  readonly h: %s;\n%s\n", e.reads(), e.readStore(), e.reads(), ref(e.readStore()), reader, scopeField)
	f.printf("  constructor(h: %s%s) {\n    this.h = h;\n", reader, scopeParam)
	if e.scope != nil {
		f.printf("    this.scope = scope;\n")
	}
	f.printf("  }\n")
	g.selectMethod(f, e, true)
	g.methods(f, e, sigs, false)
	f.printf("}\n\n")
	if e.writes() {
		g.declare(f, e.writer(), "the store of "+what, e.q)
		f.printf("/** %s implements %s on a handle that writes. */\nexport class %s extends %s implements %s {\n  readonly w: %s;\n\n", e.writer(), e.store(), e.writer(), e.reads(), ref(e.store()), writer)
		f.printf("  constructor(w: %s%s) {\n    super(w%s);\n    this.w = w;\n  }\n", writer, scopeParam, scopeArg)
		g.methods(f, e, sigs, true)
		f.printf("}\n\n")
	}
	if e.scope == nil {
		return
	}
	g.declare(f, e.unscopedReads(), "the unscoped read store of "+what, e.q)
	f.printf("/** %s implements %s on a handle that reads. */\nexport class %s implements %s {\n  readonly h: %s;\n\n  constructor(h: %s) {\n    this.h = h;\n  }\n",
		e.unscopedReads(), e.unscopedReadStore(), e.unscopedReads(), ref(e.unscopedReadStore()), reader, reader)
	g.selectMethod(f, e, false)
	g.unscopedMethods(f, e, sigs, scopeType, false)
	f.printf("}\n\n")
	if e.writes() {
		g.declare(f, e.unscopedWrites(), "the unscoped store of "+what, e.q)
		f.printf("/** %s implements %s on a handle that writes. */\nexport class %s extends %s implements %s {\n  readonly w: %s;\n\n  constructor(w: %s) {\n    super(w);\n    this.w = w;\n  }\n",
			e.unscopedWrites(), e.unscopedStore(), e.unscopedWrites(), e.unscopedReads(), ref(e.unscopedStore()), writer, writer)
		g.unscopedMethods(f, e, sigs, scopeType, true)
		f.printf("}\n\n")
	}
}

// methods writes the methods of sigs that read, or that write.
func (g *generator) methods(f *file, e *entity, sigs []signature, write bool) {
	for _, s := range sigs {
		if reads[s.x.verb] == write {
			continue
		}
		doc := ""
		if s.many {
			doc = " for each draft, in one transaction"
		}
		f.printf("\n  /** %s runs %s%s (§%s). */\n  async %s(%s): Promise<%s> {\n", s.name, s.x.q.GetName().GetStored(), doc, section(s.x.verb), s.name, s.list(""), s.result)
		g.methodBody(f, e, s)
		f.printf("  }\n")
	}
}

// unscopedMethods writes the methods of sigs that read, or that write, each
// running the scoped store's method in the scope it takes (§21.2).
func (g *generator) unscopedMethods(f *file, e *entity, sigs []signature, scopeType string, write bool) {
	for _, s := range sigs {
		if reads[s.x.verb] == write {
			continue
		}
		inner := "new " + e.reads() + "(this.h, scope)"
		if write {
			inner = "new " + e.writer() + "(this.w, scope)"
		}
		f.printf("\n  /** %s runs %s in scope (§21.2). */\n  %s(%s): Promise<%s> {\n    return %s.%s(%s);\n  }\n",
			s.name, s.x.q.GetName().GetStored(), s.name, s.list(scopeType), s.result, inner, s.name, s.args())
	}
}

// section is the design record's section of an operation.
func section(verb string) string {
	if reads[verb] {
		return "19"
	}
	return "20.1"
}

// methodBody writes a store method: it validates a write's payload before
// anything else (§20.3), fills the query's parameters from the key, the
// payload and the scope, and runs it with the terminal its cardinality
// names (§19).
func (g *generator) methodBody(f *file, e *entity, s signature) {
	x := s.x
	rt := func(name string) string { return f.rt(name, true) }
	if s.many {
		g.insertMany(f, e, s)
		return
	}
	payload, pv := e.draft, "d"
	switch x.verb {
	case "insert", "insert_or_ignore", "upsert", "upsert_set":
	case "patch", "update_where":
		payload, pv = e.part, "p"
	default:
		payload = nil
	}
	if payload != nil {
		f.printf("    %s(%s(%s));\n", rt("throwIfIssues"), g.ref(f, validatorName(payload.GetHostName().GetWords()), true), pv)
	}
	if x.verb == "upsert_set" {
		f.printf("    %s(%s(p));\n", rt("throwIfIssues"), g.ref(f, validatorName(e.part.GetHostName().GetWords()), true))
	}
	get, fallback := e.entries[e.entryName("get")]
	fallback = fallback && x.verb == "patch" && !e.versioned
	// An unversioned updateWhere with nothing set has nothing to write.
	empty := x.verb == "update_where" && !e.versioned
	lit, conds := g.args(f, e, x, payload, pv, fallback || empty)
	st := rt("statementOf")
	frag := g.ref(f, "fragments", true)
	q := x.v
	if strings.HasSuffix(x.verb, "_where") {
		f.printf("    const q = %s(%s, where);\n", rt("filtered"), x.v)
		q = "q"
	}
	var statement string
	if payload == nil {
		statement = fmt.Sprintf("%s(%s, %s, %s)", st, q, frag, lit)
	} else {
		f.printf("    const args: %s = %s;\n", f.rt("Args", false), lit)
		if fallback || empty {
			f.printf("    let set = false;\n")
		}
		for _, c := range conds {
			f.printf("%s", c)
		}
		if x.verb == "upsert_set" {
			// The Draft's assignments are its fields; the call's go to the
			// update side, whose parameters are named set.<column> (§14.7).
			f.printf("    %s(%s, %s, args, as, \"set.\");\n", rt("assign"), q, jsString(g.dotted(e.part.GetId())))
		} else {
			f.printf("    %s(%s, %s, args, as);\n", rt("assign"), q, jsString(g.dotted(payload.GetId())))
		}
		switch {
		case fallback:
			f.printf("    if (!set && as.length === 0) {\n      // Nothing to change: the row as it is.\n      return this.%s(k);\n    }\n", get.method())
		case empty:
			f.printf("    if (!set && as.length === 0) {\n      // Nothing to change.\n      return 0;\n    }\n")
		}
		statement = fmt.Sprintf("%s(%s, %s, args)", st, q, frag)
	}
	decode := g.ref(f, "decode"+TypeName(e.full.GetHostName().GetWords()), true)
	versioned := e.versioned && (x.verb == "patch" || x.verb == "delete" || x.verb == "hard_delete")
	if versioned {
		exists, ok := e.entries[e.entryName("find")]
		if !ok {
			versioned = false
		} else {
			klit, _ := g.args(f, e, exists, nil, "", false)
			f.printf("    const found = %s(%s, %s, %s);\n", st, exists.v, frag, klit)
		}
	}
	switch {
	case versioned && x.verb == "patch":
		f.printf("    return %s(this.w, %s, found, %s);\n", rt("versionedOne"), statement, decode)
	case versioned:
		f.printf("    return %s(this.w, %s, found);\n", rt("versionedExec"), statement)
	case x.verb == "get":
		f.printf("    return %s(this.h, %s, %s);\n", rt("oneOf"), statement, decode)
	case x.verb == "insert" || x.verb == "upsert" || x.verb == "upsert_set" || x.verb == "patch":
		f.printf("    return %s(this.w, %s, %s);\n", rt("oneOf"), statement, decode)
	case x.verb == "find":
		f.printf("    return %s(this.h, %s, %s);\n", rt("optionalOf"), statement, decode)
	case x.verb == "insert_or_ignore":
		f.printf("    return %s(this.w, %s, %s);\n", rt("optionalOf"), statement, decode)
	case x.verb == "count":
		f.printf("    return %s(this.h, %s);\n", rt("count"), statement)
	case x.verb == "exists":
		f.printf("    return %s(this.h, %s);\n", rt("exists"), statement)
	case q == "q":
		f.printf("    return %s(this.w, %s);\n", rt("execute"), statement)
	default:
		// Delete and hardDelete by key: a key that matches no row is
		// NotFound, as for get and patch (§19).
		f.printf("    return %s(this.w, %s);\n", rt("execByKey"), statement)
	}
}

// insertMany writes insertMany: it validates every draft before writing
// any (§20.3), then inserts them with the insert entry's row repeated.
func (g *generator) insertMany(f *file, e *entity, s signature) {
	validate := g.ref(f, validatorName(e.draft.GetHostName().GetWords()), true)
	f.printf("    for (const d of ds) {\n      %s(%s(d));\n    }\n", f.rt("throwIfIssues", true), validate)
	lit, conds := g.args(f, e, s.x, e.draft, "d", false)
	args := f.rt("Args", false)
	f.printf("    const rows: %s[] = [];\n    for (const d of ds) {\n      const args: %s = %s;\n", args, args, lit)
	for _, c := range conds {
		f.printf("%s", strings.ReplaceAll("  "+strings.TrimSuffix(c, "\n"), "\n", "\n  ")+"\n")
	}
	f.printf("      %s(%s, %s, args, as);\n      rows.push(args);\n    }\n", f.rt("assign", true), s.x.v, jsString(g.dotted(e.draft.GetId())))
	f.printf("    return %s(this.w, %s, %s, rows, %s);\n", f.rt("insertMany", true), g.ref(f, "fragments", true), s.x.v,
		g.ref(f, "decode"+TypeName(e.full.GetHostName().GetWords()), true))
}

// args returns the parameters of x as an object literal of Args, and the
// statements adding a payload's optional fields that are set, which set
// set when track is true.
func (g *generator) args(f *file, e *entity, x entry, payload *irv1.Shape, pv string, track bool) (string, []string) {
	var lit, conds []string
	for _, p := range x.q.GetParams() {
		name := p.GetName()
		switch {
		case strings.HasPrefix(name, "key.") && x.by == nil && byKey[x.verb]:
			lit = append(lit, jsString(name)+": k."+g.memberOf(e.key, paramColumn(name)))
		case strings.HasPrefix(name, "key."):
			lit = append(lit, jsString(name)+": "+keyParam(column(e.t, paramColumn(name))))
		case x.verb == "upsert_set" && strings.HasPrefix(name, "set.") && shapeField(e.part, name[len("set."):]) != nil:
			col := name[len("set."):]
			conds = append(conds, fmt.Sprintf("    if (p.%[1]s !== undefined) {\n      args[%[2]s] = p.%[1]s;\n    }\n", g.memberOf(e.part, col), jsString(name)))
		case payload != nil && shapeField(payload, name) != nil:
			v := pv + "." + g.memberOf(payload, name)
			if !isOptional(shapeField(payload, name)) {
				lit = append(lit, jsString(name)+": "+v)
				continue
			}
			set := ""
			if track {
				set = "      set = true;\n"
			}
			conds = append(conds, fmt.Sprintf("    if (%s !== undefined) {\n      args[%s] = %s;\n%s    }\n", v, jsString(name), v, set))
		default:
			g.errorf(CodeReferenceUnknown, e.q, "builder query %s takes parameter %s, which no key, payload field or scope column of %s fills",
				x.q.GetName().GetStored(), name, displayName(e.q))
		}
	}
	for _, c := range x.q.GetScope().GetColumns() {
		if e.scope == nil {
			g.errorf(CodeReferenceUnknown, e.q, "builder query %s filters on scope column %s, and %s has no Scope shape", x.q.GetName().GetStored(), c, displayName(e.q))
			continue
		}
		lit = append(lit, jsString("scope."+c)+": this.scope."+g.memberOf(e.scope, c))
	}
	if len(lit) == 0 {
		return "{}", conds
	}
	return "{ " + strings.Join(lit, ", ") + " }", conds
}
