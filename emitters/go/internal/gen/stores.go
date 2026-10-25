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
	CodeQueryUnsupported = "go-query-unsupported"
	// CodeFragmentUnknown: a plan names a fragment the table lacks (§16.2).
	CodeFragmentUnknown = "go-fragment-unknown"
)

// The operations of builder queries the stores generate (§26.2), in the
// order their methods are written.
var verbs = []string{"get", "find", "count", "exists", "insert", "upsert", "patch", "delete", "hard_delete"}

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
	// v is the Go variable holding the query.
	v string
}

// method is the store method of an entry: its verb, and By and the key's
// words when it names a key other than the primary.
func (e entry) method() string {
	m := Exported(strings.Split(e.verb, "_"))
	if e.by != nil {
		m += "By" + Exported(e.by)
	}
	return m
}

// entity is a table's builder queries and the shapes its store takes.
type entity struct {
	q *irv1.QualifiedName
	t *irv1.Table
	f *file
	// id names the table in fragment ids (§18.4).
	id string
	// plural names the store, its handle field and its types, from the
	// table's own name, as the record's h.Users does (§26.1).
	plural string
	full   *irv1.Shape
	key    *irv1.Shape
	draft  *irv1.Shape
	part   *irv1.Shape
	scope  *irv1.Shape
	// versioned: the Key holds an expected version (§20.4).
	versioned bool
	// sensitive holds the entity's sensitive columns (§24).
	sensitive map[string]bool
	entries   map[string]entry
	order     []entry
}

func (e *entity) writes() bool {
	return slices.ContainsFunc(e.order, func(x entry) bool { return !reads[x.verb] })
}

// entities collects the builder queries of each table in IR order.
func (g *generator) entities() []*entity {
	byTable := map[key]*entity{}
	var out []*entity
	g.eachNamespace(func(q func(string) *irv1.QualifiedName, ns *irv1.Namespace) {
		for _, t := range ns.GetTables() {
			e := &entity{q: q(t.GetName().GetStored()), t: t, id: g.tableID(q(t.GetName().GetStored())), plural: Exported(t.GetName().GetWords()), sensitive: map[string]bool{}, entries: map[string]entry{}}
			preset := func(p irv1.Preset, form irv1.Form) *irv1.Shape {
				return g.ix.shapes[shapeKey(&irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: e.q}, Preset: p, Form: form})]
			}
			e.full, e.key, e.scope = preset(irv1.Preset_PRESET_FULL, irv1.Form_FORM_FLAT), preset(irv1.Preset_PRESET_KEY, 0), preset(irv1.Preset_PRESET_SCOPE, 0)
			e.draft, e.part = preset(irv1.Preset_PRESET_DRAFT, irv1.Form_FORM_FLAT), preset(irv1.Preset_PRESET_PARTIAL, irv1.Form_FORM_FLAT)
			for _, fld := range e.key.GetFields() {
				e.versioned = e.versioned || fld.GetRole() == irv1.FieldRole_FIELD_ROLE_EXPECTED_VERSION
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
		x, ok := parseEntry(e, q)
		if !ok {
			g.diags = append(g.diags, &irv1.Diagnostic{Severity: irv1.Severity_SEVERITY_WARNING, Code: CodeQueryUnsupported, Target: q.GetTables()[0],
				Message: fmt.Sprintf("builder query %s is an operation the go emitter does not generate; it is left out", q.GetName().GetStored())})
			continue
		}
		x.v = Unexported(strings.FieldsFunc(q.GetName().GetStored(), func(r rune) bool { return r == '.' || r == '_' }))
		e.entries[q.GetName().GetStored()] = x
	}
	for _, e := range out {
		for _, name := range slices.Sorted(maps.Keys(e.entries)) {
			e.order = append(e.order, e.entries[name])
		}
		slices.SortStableFunc(e.order, func(a, b entry) int { return slices.Index(verbs, a.verb) - slices.Index(verbs, b.verb) })
	}
	return slices.DeleteFunc(out, func(e *entity) bool { return len(e.order) == 0 })
}

// parseEntry reads an entry's name: the entity's words joined by _, the
// operation, and by_ with the key's words, as user.get.by_primary or
// post.upsert.by_posts_one_published_slug.
func parseEntry(e *entity, q *irv1.Query) (entry, bool) {
	parts := strings.Split(q.GetName().GetStored(), ".")
	if e == nil || len(parts) < 2 || len(parts) > 3 || parts[0] != strings.Join(e.t.GetHostName().GetWords(), "_") || !slices.Contains(verbs, parts[1]) {
		return entry{}, false
	}
	x := entry{q: q, verb: parts[1]}
	if len(parts) == 3 {
		by, ok := strings.CutPrefix(parts[2], "by_")
		if !ok {
			return entry{}, false
		}
		if by != "primary" {
			x.by = strings.Split(by, "_")
		}
	}
	return x, true
}

// stores generates each entity's queries, its store interfaces and the
// types implementing them (§26.1, §26.3), into the entity's file.
func (g *generator) stores(es []*entity) {
	for _, e := range es {
		e.f = g.entityFile(e.q)
		g.queryVars(e)
		g.storeTypes(e)
	}
}

// queryVars declares each entry as a dql.Query (§16.3).
func (g *generator) queryVars(e *entity) {
	f := e.f
	dql := f.use(pkgDQL, "dql")
	f.printf("// The builder queries of %s (§16.3).\nvar (\n", displayName(e.q))
	for _, x := range e.order {
		q := x.q
		g.declare(x.v, "builder query "+q.GetName().GetStored(), e.q)
		f.printf("\t%s = &%s.Query{\n\t\tName: %q,\n\t\tCatalog: %q,\n\t\tKind: %q,\n", x.v, dql, q.GetName().GetStored(), q.GetCatalog(), x.verb)
		if q.GetWrites() {
			f.printf("\t\tWrites: true,\n")
		}
		if q.GetAtMostOne() {
			f.printf("\t\tAtMostOne: true,\n")
		}
		if c := g.consistency(f, q.GetConsistency()); c != "" {
			f.printf("\t\tConsistency: %s,\n", c)
		}
		var sensitive []string
		for _, p := range q.GetParams() {
			if e.sensitive[paramColumn(p.GetName())] {
				sensitive = append(sensitive, fmt.Sprintf("%q: true", p.GetName()))
			}
		}
		if len(sensitive) > 0 {
			f.printf("\t\tSensitive: map[string]bool{%s},\n", strings.Join(sensitive, ", "))
		}
		f.printf("\t\tPlan: []%s.Node{\n", dql)
		for _, u := range q.GetPlan().GetParts() {
			f.printf("\t\t\t%s,\n", g.use(f, u, e.q))
		}
		f.printf("\t\t},\n\t}\n")
	}
	f.printf(")\n\n")
}

// paramColumn is the column a parameter of a builder query fills: key.id
// and scope.tenant_id name theirs after the prefix.
func paramColumn(name string) string {
	if i := strings.IndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// use writes a fragment use as a dql.Use literal.
func (g *generator) use(f *file, u *irv1.FragmentUse, at *irv1.QualifiedName) string {
	dql := f.use(pkgDQL, "dql")
	if !g.fragmentIDs[u.GetFragment()] {
		g.errorf(CodeFragmentUnknown, at, "a builder query uses fragment %s, which the fragment table does not hold", u.GetFragment())
	}
	if len(u.GetHoles()) == 0 {
		return fmt.Sprintf("%s.Use{Fragment: %q}", dql, u.GetFragment())
	}
	var holes []string
	for _, h := range u.GetHoles() {
		var v string
		switch fill := h.GetValue().(type) {
		case *irv1.HoleFill_Fragment:
			v = g.use(f, fill.Fragment, at)
		case *irv1.HoleFill_Param:
			v = fmt.Sprintf("%s.Param(%q)", dql, fill.Param)
		case *irv1.HoleFill_List:
			var items []string
			for _, it := range fill.List.GetItems() {
				if p, ok := it.GetValue().(*irv1.ListItem_Param); ok {
					items = append(items, fmt.Sprintf("%s.Param(%q)", dql, p.Param))
				} else {
					items = append(items, g.use(f, it.GetFragment(), at))
				}
			}
			v = fmt.Sprintf("%s.List{Items: []%s.Node{%s}, Separator: %q}", dql, dql, strings.Join(items, ", "), fill.List.GetSeparator())
		}
		holes = append(holes, fmt.Sprintf("%q: %s", h.GetHole(), v))
	}
	return fmt.Sprintf("%s.Use{Fragment: %q, Holes: map[string]%s.Node{%s}}", dql, u.GetFragment(), dql, strings.Join(holes, ", "))
}

// Names of an entity's store types.
func (e *entity) readStore() string     { return e.plural + "ReadStore" }
func (e *entity) store() string         { return e.plural + "Store" }
func (e *entity) unscopedStore() string { return e.plural + "UnscopedStore" }
func (e *entity) unscopedReadStore() string {
	return e.plural + "UnscopedReadStore"
}
func (e *entity) lower() string { return Unexported(e.t.GetName().GetWords()) }

// signature is one store method: its name, parameters after the context
// and its results.
type signature struct {
	name, params, results string
	x                     entry
}

// signatures returns the entity's store methods, reads first.
func (g *generator) signatures(e *entity) []signature {
	f := e.f
	full := Exported(e.full.GetHostName().GetWords())
	var out []signature
	for _, x := range e.order {
		s := signature{name: x.method(), x: x}
		switch {
		case byKey[x.verb] && x.by == nil:
			s.params = "k " + Exported(e.key.GetHostName().GetWords())
		case x.verb == "get" || x.verb == "find":
			var ps []string
			for _, p := range x.q.GetParams() {
				if c, ok := strings.CutPrefix(p.GetName(), "key."); ok {
					ps = append(ps, Unexported(column(e.t, c).GetHostName().GetWords())+" "+g.goType(f, p.GetType(), e.q, "parameter "+p.GetName()))
				}
			}
			s.params = strings.Join(ps, ", ")
		}
		switch x.verb {
		case "insert", "upsert":
			s.params = "d " + Exported(e.draft.GetHostName().GetWords())
		case "patch":
			s.params += ", p " + Exported(e.part.GetHostName().GetWords())
		}
		switch x.verb {
		case "get", "insert", "upsert", "patch":
			s.results = "(" + full + ", error)"
		case "find":
			s.results = "(" + f.use(pkgOpt, "opt") + ".Opt[" + full + "], error)"
		case "count":
			s.results = "(int64, error)"
		case "exists":
			s.results = "(bool, error)"
		default:
			s.results = "error"
		}
		out = append(out, s)
	}
	return out
}

// storeTypes generates the store interfaces and their implementations: a
// read store and a store that also writes, and for a scoped entity the
// unscoped forms, whose methods take the scope (§21.2, §26.3).
func (g *generator) storeTypes(e *entity) {
	f := e.f
	ctx := f.use("context", "context")
	dql := f.use(pkgDQL, "dql")
	sigs := g.signatures(e)
	what := displayName(e.q)
	scopeType := ""
	if e.scope != nil {
		scopeType = Exported(e.scope.GetHostName().GetWords())
	}
	iface := func(name, doc, embed string, write, unscoped bool) {
		g.declare(name, "the store interface "+name, e.q)
		f.printf("// %s %s %s (§26.3).\ntype %s interface {\n", name, doc, what, name)
		if embed != "" {
			f.printf("\t%s\n", embed)
		} else {
			// Select reads through the store (§18.2).
			f.printf("\tfrom(%s) %s.From\n", e.tableType(), dql)
		}
		for _, s := range sigs {
			if reads[s.x.verb] == write {
				continue
			}
			params := s.params
			if unscoped {
				params = strings.TrimSuffix("scope "+scopeType+", "+params, ", ")
			}
			f.printf("\t%s(ctx %s.Context%s) %s\n", s.name, ctx, prefixComma(params), s.results)
		}
		f.printf("}\n\n")
	}
	iface(e.readStore(), "reads", "", false, false)
	if e.writes() {
		iface(e.store(), "reads and writes", e.readStore(), true, false)
	}
	if e.scope != nil {
		iface(e.unscopedReadStore(), "reads, across scopes,", "", false, true)
		if e.writes() {
			iface(e.unscopedStore(), "reads and writes, across scopes,", e.unscopedReadStore(), true, true)
		}
	}

	lower := e.lower()
	rtype, wtype := lower+"Reads", lower+"Writes"
	g.declare(rtype, "the read store of "+what, e.q)
	scopeField := ""
	if e.scope != nil {
		scopeField = "\n\tscope " + scopeType
	}
	f.printf("// %s implements %s on a handle that reads.\ntype %s struct {\n\th %s.Reader%s\n}\n\n", rtype, e.readStore(), rtype, dql, scopeField)
	if e.writes() {
		g.declare(wtype, "the store of "+what, e.q)
		f.printf("// %s implements %s on a handle that writes.\ntype %s struct {\n\t%s\n\tw %s.Writer\n}\n\n", wtype, e.store(), wtype, rtype, dql)
	}
	for _, s := range sigs {
		recv, typ := "s", wtype
		if reads[s.x.verb] {
			typ = rtype
		}
		f.printf("// %s runs %s (§%s).\nfunc (%s %s) %s(ctx %s.Context%s) %s {\n", s.name, s.x.q.GetName().GetStored(), section(s.x.verb), recv, typ, s.name, ctx, prefixComma(s.params), s.results)
		g.methodBody(e, s)
		f.printf("}\n\n")
	}
	if e.scope == nil {
		return
	}
	ureads, uwrites := lower+"UnscopedReads", lower+"Unscoped"
	g.declare(ureads, "the unscoped read store of "+what, e.q)
	f.printf("// %s implements %s on a handle that reads.\ntype %s struct{ h %s.Reader }\n\n", ureads, e.unscopedReadStore(), ureads, dql)
	if e.writes() {
		g.declare(uwrites, "the unscoped store of "+what, e.q)
		f.printf("// %s implements %s on a handle that writes.\ntype %s struct {\n\t%s\n\tw %s.Writer\n}\n\n", uwrites, e.unscopedStore(), uwrites, ureads, dql)
	}
	for _, s := range sigs {
		typ, inner := uwrites, wtype+"{"+rtype+"{u.w, scope}, u.w}"
		if reads[s.x.verb] {
			typ, inner = ureads, rtype+"{u.h, scope}"
		}
		f.printf("// %s runs %s in scope (§21.2).\nfunc (u %s) %s(ctx %s.Context, scope %s%s) %s {\n\treturn %s.%s(ctx%s)\n}\n\n",
			s.name, s.x.q.GetName().GetStored(), typ, s.name, ctx, scopeType, prefixComma(s.params), s.results, inner, s.name, prefixComma(argNames(s.params)))
	}
}

// section is the design record's section of an operation.
func section(verb string) string {
	if reads[verb] {
		return "19"
	}
	return "20.1"
}

func prefixComma(s string) string {
	if s == "" {
		return ""
	}
	return ", " + s
}

// argNames turns a parameter list into the arguments that pass it on.
func argNames(params string) string {
	if params == "" {
		return ""
	}
	var names []string
	for _, p := range strings.Split(params, ", ") {
		names = append(names, strings.Fields(p)[0])
	}
	return strings.Join(names, ", ")
}

// methodBody writes a store method: it validates a write's payload before
// anything else (§20.3), fills the query's parameters from the key, the
// payload and the scope, and runs it with the terminal its cardinality
// names (§19).
func (g *generator) methodBody(e *entity, s signature) {
	f := e.f
	dql := f.use(pkgDQL, "dql")
	x := s.x
	zero := ""
	switch x.verb {
	case "get", "insert", "upsert", "patch":
		zero = Exported(e.full.GetHostName().GetWords()) + "{}, "
	case "find":
		zero = f.use(pkgOpt, "opt") + ".None[" + Exported(e.full.GetHostName().GetWords()) + "](), "
	case "count":
		zero = "0, "
	case "exists":
		zero = "false, "
	}
	payload, pv := e.draft, "d"
	switch x.verb {
	case "insert", "upsert":
	case "patch":
		payload, pv = e.part, "p"
	default:
		payload = nil
	}
	if payload != nil {
		f.printf("\tif err := %s.Validate(); err != nil {\n\t\treturn %serr\n\t}\n", pv, zero)
	}
	get, fallback := e.entries[e.entryName("get", "")]
	fallback = fallback && x.verb == "patch" && !e.versioned
	lit, conds := g.args(e, x, payload, pv, fallback)
	if len(conds) == 0 {
		f.printf("\tst, err := %s.Statement(fragments, %s.Args{%s})\n", x.v, dql, lit)
	} else {
		f.printf("\targs := %s.Args{%s}\n", dql, lit)
		if fallback {
			f.printf("\tset := false\n")
		}
		for _, c := range conds {
			f.printf("%s", c)
		}
		if fallback {
			f.printf("\tif !set {\n\t\t// Nothing to change: the row as it is.\n\t\treturn s.%s(ctx, k)\n\t}\n", get.method())
		}
		f.printf("\tst, err := %s.Statement(fragments, args)\n", x.v)
	}
	f.printf("\tif err != nil {\n\t\treturn %serr\n\t}\n", zero)
	full := "Decode" + Exported(e.full.GetHostName().GetWords())
	versioned := e.versioned && (x.verb == "patch" || x.verb == "delete" || x.verb == "hard_delete")
	var exists entry
	if versioned {
		var ok bool
		if exists, ok = e.entries[e.entryName("find", "")]; !ok {
			versioned = false
		} else {
			klit, _ := g.args(e, exists, nil, "", false)
			f.printf("\texists, err := %s.Statement(fragments, %s.Args{%s})\n\tif err != nil {\n\t\treturn %serr\n\t}\n", exists.v, dql, klit, zero)
		}
	}
	switch {
	case versioned && x.verb == "patch":
		f.printf("\treturn %s.VersionedOne(ctx, s.w, st, exists, %s)\n", dql, full)
	case versioned:
		f.printf("\treturn %s.VersionedExec(ctx, s.w, st, exists)\n", dql)
	case x.verb == "get" || x.verb == "insert" || x.verb == "upsert" || x.verb == "patch":
		h := "s.w"
		if x.verb == "get" {
			h = "s.h"
		}
		f.printf("\treturn %s.OneOf(ctx, %s, st, %s)\n", dql, h, full)
	case x.verb == "find":
		f.printf("\treturn %s.OptionalOf(ctx, s.h, st, %s)\n", dql, full)
	case x.verb == "count":
		f.printf("\treturn %s.Count(ctx, s.h, st)\n", dql)
	case x.verb == "exists":
		f.printf("\treturn %s.Exists(ctx, s.h, st)\n", dql)
	default:
		f.printf("\t_, err = %s.Exec(ctx, s.w, st)\n\treturn err\n", dql)
	}
}

// entryName is the name of an entity's entry of verb by the key by names,
// the primary key when by is empty.
func (e *entity) entryName(verb, by string) string {
	if by == "" {
		by = "primary"
	}
	return strings.Join(e.t.GetHostName().GetWords(), "_") + "." + verb + ".by_" + by
}

// args returns the parameters of x as the elements of a dql.Args literal,
// and the statements adding a payload's optional fields that are set.
func (g *generator) args(e *entity, x entry, payload *irv1.Shape, pv string, track bool) (string, []string) {
	var lit, conds []string
	for _, p := range x.q.GetParams() {
		name := p.GetName()
		switch {
		case strings.HasPrefix(name, "key.") && x.by == nil && byKey[x.verb]:
			lit = append(lit, fmt.Sprintf("%q: k.%s", name, g.fieldName(e.key, paramColumn(name))))
		case strings.HasPrefix(name, "key."):
			lit = append(lit, fmt.Sprintf("%q: %s", name, Unexported(column(e.t, paramColumn(name)).GetHostName().GetWords())))
		case payload != nil && shapeField(payload, name) != nil:
			fld := shapeField(payload, name)
			v := pv + "." + g.fieldName(payload, name)
			if fld.GetPresence() != irv1.Presence_PRESENCE_OPTIONAL {
				lit = append(lit, fmt.Sprintf("%q: %s", name, v))
				continue
			}
			set := ""
			if track {
				set = "\t\tset = true\n"
			}
			conds = append(conds, fmt.Sprintf("\tif v, ok := %s.Get(); ok {\n\t\targs[%q] = v\n%s\t}\n", v, name, set))
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
		lit = append(lit, fmt.Sprintf("%q: s.scope.%s", "scope."+c, g.fieldName(e.scope, c)))
	}
	return strings.Join(lit, ", "), conds
}
