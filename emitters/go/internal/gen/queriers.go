package gen

import (
	"fmt"
	"slices"
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// fileQuery is a query of a query file as its Querier method (§17, §26.3).
type fileQuery struct {
	q      *irv1.Query
	method string
	// params are the method's parameters after the context, and args the
	// values of its placeholders in order, scope columns last.
	params []string
	args   []string
	// scope is the scope group whose scope fills the query's scope columns,
	// nil for a query that has none.
	scope   *scopeGroup
	results string
	// body is the method's body, which names the handle h or w and the
	// scope s.
	body string
}

// querierNames are a variant's Querier interfaces and its implementation.
type querierNames struct{ read, write, impl string }

// queriers generates dql-queries.dql.go: each query-file query as a method
// of the Querier of every variant whose scope covers it, a read transaction
// seeing only reads, and an anonymous result's row type and decoder
// (§17.3, §21, §22.2, §26.3). It returns the variants' names, nil without
// query-file queries.
func (g *generator) queriers(variants []*variant, groups []*scopeGroup) map[*variant]querierNames {
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
	dql := f.use(pkgDQL, "dql")
	ctx := f.use("context", "context")
	var fqs []*fileQuery
	for _, q := range qs {
		if fq := g.fileQuery(f, q, groups); fq != nil {
			fqs = append(fqs, fq)
		}
	}
	out := map[*variant]querierNames{}
	for _, v := range variants {
		prefix := strings.TrimSuffix(v.db, "DB")
		n := querierNames{read: prefix + "ReadQuerier", write: prefix + "Querier", impl: "queries"}
		if prefix != "" {
			n.impl = strings.ToLower(prefix[:1]) + prefix[1:] + "Queries"
		}
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
		sig := func(fq *fileQuery) string {
			params := fq.params
			if v.unscoped && fq.scope != nil {
				params = append([]string{"scope " + fq.scope.typ}, params...)
			}
			return fmt.Sprintf("%s(ctx %s.Context%s) %s", fq.method, ctx, prefixComma(strings.Join(params, ", ")), fq.results)
		}
		for _, iface := range []struct {
			name, doc string
			write     bool
		}{{n.read, "reads", false}, {n.write, "reads and writes", true}} {
			g.declare(iface.name, "the interface "+iface.name, nil)
			f.printf("// %s %s through the query files' queries on the %s handles (§17, §26.3).\ntype %s interface {\n", iface.name, iface.doc, v.db, iface.name)
			if iface.write {
				f.printf("\t%s\n", n.read)
			}
			for _, fq := range mine {
				if fq.q.GetWrites() == iface.write {
					f.printf("\t%s\n", sig(fq))
				}
			}
			f.printf("}\n\n")
		}
		g.declare(n.impl, "the implementation of "+n.write, nil)
		scopeField := ""
		if v.scope != nil {
			scopeField = "\n\tscope " + v.scope.typ
		}
		f.printf("// %s implements %s; w is nil on a read transaction, whose\n// handle holds it as a %s.\ntype %s struct {\n\th %s.Reader\n\tw %s.Writer%s\n}\n\n", n.impl, n.write, n.read, n.impl, dql, dql, scopeField)
		for _, fq := range mine {
			f.printf("// %s runs query %s of %s (§17).\nfunc (q %s) %s {\n", fq.method, fq.q.GetName().GetStored(), fq.q.GetLocation(), n.impl, sig(fq))
			s := "scope"
			switch {
			case v.scope != nil && v.scope == fq.scope:
				s = "q.scope"
			case !v.unscoped && fq.scope != nil:
				// The handle's scope, by the query's scope group's fields.
				var fields []string
				for _, c := range fq.q.GetScope().GetColumns() {
					fields = append(fields, fmt.Sprintf("%s: q.scope.%s", g.scopeField(fq.scope, c), g.scopeField(v.scope, c)))
				}
				f.printf("\tscope := %s{%s}\n", fq.scope.typ, strings.Join(fields, ", "))
			}
			f.printf("%s}\n\n", strings.ReplaceAll(fq.body, "{scope}", s))
		}
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

// scopeField is the Go field of a scope group's scope holding column c.
func (g *generator) scopeField(sg *scopeGroup, c string) string {
	return g.fieldName(sg.shape, c)
}

// fileQuery builds q's method; it fails a query whose scope columns no
// scope group covers.
func (g *generator) fileQuery(f *file, q *irv1.Query, groups []*scopeGroup) *fileQuery {
	dql := f.use(pkgDQL, "dql")
	name := q.GetName().GetStored()
	fq := &fileQuery{q: q, method: Exported(q.GetName().GetWords())}
	at := &irv1.QualifiedName{Object: name}
	if len(q.GetScope().GetColumns()) > 0 {
		i := slices.IndexFunc(groups, func(sg *scopeGroup) bool { return sg.coversQuery(q) })
		if i < 0 {
			g.errorf(CodeReferenceUnknown, at, "query %s filters on scope columns %s, which no entity's Scope shape holds", name, strings.Join(q.GetScope().GetColumns(), ", "))
			return nil
		}
		fq.scope = groups[i]
	}
	used := map[string]bool{"ctx": true, "q": true, "scope": true, "st": true, "err": true}
	for _, p := range q.GetParams() {
		v := fresh(Unexported(strings.Split(p.GetName(), "_")), used)
		t := g.field(f, p.GetType(), p.GetNullable(), at, "parameter "+p.GetName()+" of query "+name)
		if p.GetList() {
			t = "[]" + t
		}
		fq.params = append(fq.params, v+" "+t)
		fq.args = append(fq.args, v)
	}
	for _, c := range q.GetScope().GetColumns() {
		fq.args = append(fq.args, "{scope}."+g.scopeField(fq.scope, c))
	}
	g.declare(fq.method, "the method of query "+name, at)
	var b strings.Builder
	fmt.Fprintf(&b, "\tst := %s.Statement{\n\t\tQuery: %q,\n\t\tCatalog: %q,\n\t\tKind: %q,\n\t\tSQL: %s,\n", dql, name, q.GetCatalog(), kind(q.GetText()), quote(q.GetText()))
	if len(fq.args) > 0 {
		fmt.Fprintf(&b, "\t\tArgs: []any{%s},\n", strings.Join(fq.args, ", "))
	}
	if q.GetWrites() {
		b.WriteString("\t\tWrites: true,\n")
	}
	if q.GetAtMostOne() {
		b.WriteString("\t\tAtMostOne: true,\n")
	}
	if c := g.consistency(f, q.GetConsistency()); c != "" {
		fmt.Fprintf(&b, "\t\tConsistency: %s,\n", c)
	}
	b.WriteString("\t}\n")
	h := "q.h"
	if q.GetWrites() {
		h = "q.w"
	}
	row, decode := g.resultType(f, q)
	switch q.GetCardinality() {
	case irv1.Cardinality_CARDINALITY_ONE:
		fq.results = "(" + row + ", error)"
		fmt.Fprintf(&b, "\treturn %s.OneOf(ctx, %s, st, %s)\n", dql, h, decode)
	case irv1.Cardinality_CARDINALITY_OPTIONAL:
		fq.results = "(" + f.use(pkgOpt, "opt") + ".Opt[" + row + "], error)"
		fmt.Fprintf(&b, "\treturn %s.OptionalOf(ctx, %s, st, %s)\n", dql, h, decode)
	case irv1.Cardinality_CARDINALITY_MANY:
		fq.results = "([]" + row + ", error)"
		fmt.Fprintf(&b, "\treturn %s.AllOf(ctx, %s, st, %s)\n", dql, h, decode)
	case irv1.Cardinality_CARDINALITY_COUNT:
		fq.results = "(int64, error)"
		fmt.Fprintf(&b, "\treturn %s.Count(ctx, %s, st)\n", dql, h)
	case irv1.Cardinality_CARDINALITY_EXEC_ROWS:
		fq.results = "(int64, error)"
		fmt.Fprintf(&b, "\treturn %s.Exec(ctx, q.w, st)\n", dql)
	default: // CARDINALITY_EXEC; the core gives a query file no other
		fq.results = "error"
		fmt.Fprintf(&b, "\t_, err := %s.Exec(ctx, q.w, st)\n\treturn err\n", dql)
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

// resultType returns the Go type of a row q returns and the decoder of one:
// its shape's, reading the columns in the query's order, or, for its own
// result, a row type <Name>Row and its decoder, declared in f (§13, §17.3).
func (g *generator) resultType(f *file, q *irv1.Query) (string, string) {
	dql := f.use(pkgDQL, "dql")
	name := q.GetName().GetStored()
	switch r := q.GetResult(); {
	case r.GetShape() != nil:
		s := g.ix.shapes[shapeKey(r.GetShape())]
		typ := Exported(s.GetHostName().GetWords())
		var order []string
		same := true
		for i, fld := range s.GetFields() {
			j := slices.Index(r.GetColumns(), fld.GetColumn())
			same = same && i == j
			order = append(order, fmt.Sprintf("r[%d]", j))
		}
		if same {
			return typ, "Decode" + typ
		}
		return typ, fmt.Sprintf("func(r %s.Row) (%s, error) { return Decode%s(%s.Row{%s}) }", dql, typ, typ, dql, strings.Join(order, ", "))
	case r.GetAnonymous() != nil:
	default:
		return "", ""
	}
	typ := Exported(append(slices.Clone(q.GetName().GetWords()), "row"))
	fn := "decode" + typ
	at := &irv1.QualifiedName{Object: name}
	g.declare(typ, "the row type of query "+name, at)
	g.declare(fn, "the decoder of query "+name, at)
	fields := map[string]string{}
	var decls, body []string
	for i, rf := range q.GetResult().GetAnonymous().GetFields() {
		fname := Exported(strings.Split(rf.GetName(), "_"))
		if prev, ok := fields[fname]; ok {
			g.errorf(CodeNameCollision, at, "column %s of query %s gets the Go name %s, as column %s does (§4.3, §15.2); give one of them another alias", rf.GetName(), name, fname, prev)
			continue
		}
		fields[fname] = rf.GetName()
		what := "column " + rf.GetName() + " of query " + name
		decls = append(decls, fmt.Sprintf("\t%s %s\n", fname, g.field(f, rf.GetType(), rf.GetNullable(), at, what)))
		conv := g.conv(f, rf.GetType(), at, what)
		get := fmt.Sprintf("%s.Nullable(d, %d, %q, %s)", dql, i, rf.GetName(), conv)
		if !rf.GetNullable() {
			assert := ""
			if rf.GetNullabilitySource() == irv1.NullabilitySource_NULLABILITY_SOURCE_ANNOTATION {
				assert = "the nonnull header of query " + name
			}
			get = fmt.Sprintf("%s.Field(d, %d, %q, %q, %s)", dql, i, rf.GetName(), assert, conv)
		}
		body = append(body, fmt.Sprintf("\tout.%s = %s\n", fname, get))
	}
	f.printf("// %s is a row of query %s (§17.3).\ntype %s struct {\n%s}\n\n", typ, name, typ, strings.Join(decls, ""))
	f.printf("// %s decodes r, a row of query %s, as decoders of shapes do (§13).\nfunc %s(r %s.Row) (%s, error) {\n\td := %s.NewDecoder(%q, r, %d)\n\tvar out %s\n%s\treturn out, d.Err()\n}\n\n",
		fn, name, fn, dql, typ, dql, name, len(q.GetResult().GetAnonymous().GetFields()), typ, strings.Join(body, ""))
	return typ, fn
}
