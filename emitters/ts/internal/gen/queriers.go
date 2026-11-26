package gen

import (
	"fmt"
	"slices"
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// fileQuery is a query of a query file as its querier method (§17, §26.3).
type fileQuery struct {
	q      *irv1.Query
	method string
	// params are the method's parameters, and args the values of its
	// placeholders in order, scope columns last, which name the scope
	// {scope}.
	params []param
	args   []string
	// scope is the scope group whose scope fills the query's scope columns,
	// nil for a query that has none.
	scope  *scopeGroup
	result string
	// body is the method's body.
	body string
}

// querierNames are a variant's querier interfaces and their implementations.
type querierNames struct{ read, write, readImpl, writeImpl string }

// queriers generates dql-queries.dql.ts: each query-file query as a method
// of the querier of every variant whose scope covers it, a read transaction
// seeing only reads, and an anonymous result's row type and decoder
// (§17.3, §21, §22.2, §26.3). The interfaces' implementations go into impl.
// It returns the variants' names, nil without query-file queries.
func (g *generator) queriers(impl *file, variants []*variant, groups []*scopeGroup) map[*variant]querierNames {
	var qs []*irv1.Query
	for _, q := range g.b.GetQueries().GetQueries() {
		if q.GetSource() == irv1.QuerySource_QUERY_SOURCE_FILE {
			qs = append(qs, q)
		}
	}
	if len(qs) == 0 {
		return nil
	}
	f := g.newFile("dql-queries")
	var fqs []*fileQuery
	for _, q := range qs {
		if fq := g.fileQuery(f, impl, q, groups); fq != nil {
			fqs = append(fqs, fq)
		}
	}
	out := map[*variant]querierNames{}
	for _, v := range variants {
		prefix := strings.TrimSuffix(v.db, "Db")
		n := querierNames{read: prefix + "ReadQuerier", write: prefix + "Querier", readImpl: prefix + "ReadQueries", writeImpl: prefix + "Queries"}
		var mine []*fileQuery
		for _, fq := range fqs {
			if fq.scope == nil || v.unscoped || v.scope != nil && v.scope.carries(fq) {
				mine = append(mine, fq)
			}
		}
		if len(mine) == 0 {
			continue
		}
		out[v] = n
		params := func(fq *fileQuery, file *file) string {
			s := signature{params: fq.params}
			if v.unscoped && fq.scope != nil {
				return s.list(g.ref(file, fq.scope.typ, false))
			}
			return s.list("")
		}
		for _, iface := range []struct {
			name, doc, extends string
			write              bool
		}{{n.read, "reads", "", false}, {n.write, "reads and writes", " extends " + n.read, true}} {
			g.declare(f, iface.name, "the interface "+iface.name, nil)
			f.printf("/** %s %s through the query files' queries on the %s handles (§17, §26.3). */\nexport interface %s%s {\n", iface.name, iface.doc, v.db, iface.name, iface.extends)
			for _, fq := range mine {
				if fq.q.GetWrites() == iface.write {
					f.printf("  %s(%s): Promise<%s>;\n", fq.method, params(fq, f), fq.result)
				}
			}
			f.printf("}\n\n")
		}
		reader, writer := impl.rt("Reader", false), impl.rt("Writer", false)
		scopeField, scopeParam, scopeArg := "", "", ""
		if v.scope != nil {
			t := g.ref(impl, v.scope.typ, false)
			scopeField, scopeParam, scopeArg = "  readonly scope: "+t+";\n", ", scope: "+t, ", scope"
		}
		methods := func(write bool) {
			for _, fq := range mine {
				if fq.q.GetWrites() != write {
					continue
				}
				impl.printf("\n  /** %s runs query %s of %s (§17). */\n  async %s(%s): Promise<%s> {\n", fq.method, fq.q.GetName().GetStored(), fq.q.GetLocation(), fq.method, params(fq, impl), fq.result)
				s := "scope"
				switch {
				case v.scope != nil && v.scope == fq.scope:
					s = "this.scope"
				case !v.unscoped && fq.scope != nil:
					// The handle's scope, by the query's scope group's fields.
					var fields []string
					for _, c := range fq.q.GetScope().GetColumns() {
						fields = append(fields, g.memberOf(fq.scope.shape, c)+": this.scope."+g.memberOf(v.scope.shape, c))
					}
					impl.printf("    const scope: %s = { %s };\n", g.ref(impl, fq.scope.typ, false), strings.Join(fields, ", "))
				}
				impl.printf("%s  }\n", strings.ReplaceAll(fq.body, "{scope}", s))
			}
		}
		g.declare(impl, n.readImpl, "the implementation of "+n.read, nil)
		impl.printf("/** %s implements %s on a handle that reads. */\nexport class %s implements %s {\n  readonly h: %s;\n%s\n  constructor(h: %s%s) {\n    this.h = h;\n",
			n.readImpl, n.read, n.readImpl, g.ref(impl, n.read, false), reader, scopeField, reader, scopeParam)
		if v.scope != nil {
			impl.printf("    this.scope = scope;\n")
		}
		impl.printf("  }\n")
		methods(false)
		impl.printf("}\n\n")
		g.declare(impl, n.writeImpl, "the implementation of "+n.write, nil)
		impl.printf("/** %s implements %s on a handle that writes. */\nexport class %s extends %s implements %s {\n  readonly w: %s;\n\n  constructor(w: %s%s) {\n    super(w%s);\n    this.w = w;\n  }\n",
			n.writeImpl, n.write, n.writeImpl, n.readImpl, g.ref(impl, n.write, false), writer, writer, scopeParam, scopeArg)
		methods(true)
		impl.printf("}\n\n")
	}
	return out
}

// coversQuery reports whether the scope has every scope column of q.
func (sg *scopeGroup) coversQuery(q *irv1.Query) bool {
	for _, c := range q.GetScope().GetColumns() {
		if !slices.Contains(sg.cols, c) {
			return false
		}
	}
	return true
}

// carries reports whether the scope has every scope column of fq, of the
// type its own scope group gives it.
func (sg *scopeGroup) carries(fq *fileQuery) bool {
	for _, c := range fq.q.GetScope().GetColumns() {
		if t, ok := sg.types[c]; !ok || t != fq.scope.types[c] {
			return false
		}
	}
	return true
}

// fileQuery builds q's method, whose types f, the interfaces' file, and
// impl, the implementations', import; it fails a query whose scope columns
// no scope group covers.
func (g *generator) fileQuery(f, impl *file, q *irv1.Query, groups []*scopeGroup) *fileQuery {
	name := q.GetName().GetStored()
	fq := &fileQuery{q: q, method: MemberName(q.GetName().GetWords())}
	at := &irv1.QualifiedName{Object: name}
	if len(q.GetScope().GetColumns()) > 0 {
		i := slices.IndexFunc(groups, func(sg *scopeGroup) bool { return sg.coversQuery(q) })
		if i < 0 {
			g.errorf(CodeReferenceUnknown, at, "query %s filters on scope columns %s, which no entity's Scope shape holds", name, strings.Join(q.GetScope().GetColumns(), ", "))
			return nil
		}
		fq.scope = groups[i]
	}
	used := map[string]bool{"scope": true}
	for n := range runtimeFuncs {
		used[n] = true
	}
	var types []string
	for _, p := range q.GetParams() {
		v := fresh(LocalName(strings.Split(p.GetName(), "_")), used)
		what := "parameter " + p.GetName() + " of query " + name
		t := g.field(f, p.GetType(), p.GetNullable(), at, what)
		g.field(impl, p.GetType(), p.GetNullable(), at, what)
		if p.GetList() {
			t = "readonly " + parenthesize(t) + "[]"
		}
		fq.params = append(fq.params, param{v, t})
		fq.args = append(fq.args, v)
		types = append(types, jsString(g.dbType(p.GetType(), p.GetList())))
	}
	for _, c := range q.GetScope().GetColumns() {
		fq.args = append(fq.args, "{scope}."+g.memberOf(fq.scope.shape, c))
		var typ *irv1.Type
		for _, fld := range fq.scope.shape.GetFields() {
			if fld.GetColumn() == c {
				typ = fld.GetType()
			}
		}
		types = append(types, jsString(g.dbType(typ, false)))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "    const st: %s = {\n      query: %s,\n      catalog: %s,\n      kind: %s,\n      sql: %s,\n", impl.rt("Statement", false), jsString(name), jsString(q.GetCatalog()), jsString(kind(q.GetText())), jsString(q.GetText()))
	if len(fq.args) > 0 {
		fmt.Fprintf(&b, "      args: [%s],\n", strings.Join(fq.args, ", "))
	}
	if slices.ContainsFunc(types, func(t string) bool { return t != `""` }) {
		fmt.Fprintf(&b, "      types: [%s],\n", strings.Join(types, ", "))
	}
	if q.GetWrites() {
		b.WriteString("      writes: true,\n")
	}
	if q.GetAtMostOne() {
		b.WriteString("      atMostOne: true,\n")
	}
	if c := g.consistency(impl, q.GetConsistency()); c != "" {
		fmt.Fprintf(&b, "      consistency: %s,\n", c)
	}
	b.WriteString("    };\n")
	h := "this.h"
	if q.GetWrites() {
		h = "this.w"
	}
	row := g.resultType(f, q)
	if row != "" {
		row = g.ref(impl, row, false)
	}
	decode := g.resultDecoder(impl, q)
	rt := func(n string) string { return impl.rt(n, true) }
	switch q.GetCardinality() {
	case irv1.Cardinality_CARDINALITY_ONE:
		fq.result = row
		fmt.Fprintf(&b, "    return %s(%s, st, %s);\n", rt("oneOf"), h, decode)
	case irv1.Cardinality_CARDINALITY_OPTIONAL:
		fq.result = row + " | undefined"
		fmt.Fprintf(&b, "    return %s(%s, st, %s);\n", rt("optionalOf"), h, decode)
	case irv1.Cardinality_CARDINALITY_MANY:
		fq.result = parenthesize(row) + "[]"
		fmt.Fprintf(&b, "    return %s(%s, st, %s);\n", rt("allOf"), h, decode)
	case irv1.Cardinality_CARDINALITY_COUNT:
		fq.result = "bigint"
		fmt.Fprintf(&b, "    return %s(%s, st);\n", rt("count"), h)
	case irv1.Cardinality_CARDINALITY_EXEC_ROWS:
		fq.result = "number"
		fmt.Fprintf(&b, "    return %s(this.w, st);\n", rt("execute"))
	default: // CARDINALITY_EXEC; the core gives a query file no other
		fq.result = "void"
		fmt.Fprintf(&b, "    await %s(this.w, st);\n", rt("execute"))
	}
	fq.body = b.String()
	return fq
}

// kind is a query's kind for hooks (§24): its statement's first word, such
// as select or update.
func kind(text string) string {
	if w := strings.Fields(text); len(w) > 0 {
		return strings.ToLower(w[0])
	}
	return ""
}

// rowName is the row type of a query with its own result, as
// AuthorPostCountsRow.
func rowName(q *irv1.Query) string {
	return TypeName(append(slices.Clone(q.GetName().GetWords()), "row"))
}

// resultType returns the type of a row q returns: its shape's, or, for its
// own result, a row type <Name>Row, declared in f with its decoder (§13,
// §17.3). It is "" for a query that returns no rows.
func (g *generator) resultType(f *file, q *irv1.Query) string {
	name := q.GetName().GetStored()
	switch r := q.GetResult(); {
	case r.GetShape() != nil:
		return g.ref(f, TypeName(g.ix.shapes[shapeKey(r.GetShape())].GetHostName().GetWords()), false)
	case r.GetAnonymous() != nil:
	default:
		return ""
	}
	typ := rowName(q)
	fn := "decode" + typ
	at := &irv1.QualifiedName{Object: name}
	g.declare(f, typ, "the row type of query "+name, at)
	g.declare(f, fn, "the decoder of query "+name, at)
	fields := map[string]string{}
	var decls, body []string
	for i, rf := range q.GetResult().GetAnonymous().GetFields() {
		fname := MemberName(strings.Split(rf.GetName(), "_"))
		if prev, ok := fields[fname]; ok {
			g.errorf(CodeNameCollision, at, "column %s of query %s gets the TypeScript name %s, as column %s does (§4.3, §15.2); give one of them another alias", rf.GetName(), name, fname, prev)
			continue
		}
		fields[fname] = rf.GetName()
		what := "column " + rf.GetName() + " of query " + name
		decls = append(decls, fmt.Sprintf("  %s: %s;\n", fname, g.field(f, rf.GetType(), rf.GetNullable(), at, what)))
		conv := g.conv(f, rf.GetType(), at, what)
		get := fmt.Sprintf("d.nullable(%d, %s, %s)", i, jsString(rf.GetName()), conv)
		if !rf.GetNullable() {
			assert := ""
			if rf.GetNullabilitySource() == irv1.NullabilitySource_NULLABILITY_SOURCE_ANNOTATION {
				assert = "the nonnull header of query " + name
			}
			get = fmt.Sprintf("d.field(%d, %s, %s, %s)", i, jsString(rf.GetName()), jsString(assert), conv)
		}
		body = append(body, fmt.Sprintf("    %s: %s,\n", fname, get))
	}
	f.printf("/** %s is a row of query %s (§17.3). */\nexport interface %s {\n%s}\n\n", typ, name, typ, strings.Join(decls, ""))
	f.printf("/** %s decodes r, a row of query %s, as decoders of shapes do (§13). */\nexport function %s(r: %s): %s {\n  const d = new %s(%s, r, %d);\n  return {\n%s  };\n}\n\n",
		fn, name, fn, f.rt("Row", false), typ, f.rt("Decoder", true), jsString(name), len(q.GetResult().GetAnonymous().GetFields()), strings.Join(body, ""))
	return typ
}

// resultDecoder is the decoder of a row q returns, imported into impl: its
// shape's, reading the columns in the query's order, or its row type's.
func (g *generator) resultDecoder(impl *file, q *irv1.Query) string {
	r := q.GetResult()
	if r.GetShape() == nil {
		if r.GetAnonymous() == nil {
			return ""
		}
		return g.ref(impl, "decode"+rowName(q), true)
	}
	s := g.ix.shapes[shapeKey(r.GetShape())]
	decode := g.ref(impl, "decode"+TypeName(s.GetHostName().GetWords()), true)
	var order []string
	same := true
	for i, fld := range s.GetFields() {
		j := slices.Index(r.GetColumns(), fld.GetColumn())
		same = same && i == j
		order = append(order, fmt.Sprintf("r.values[%d]", j))
	}
	if same {
		return decode
	}
	row := impl.rt("Row", true)
	return fmt.Sprintf("(r: %s) => %s(new %s([%s]))", row, decode, row, strings.Join(order, ", "))
}
