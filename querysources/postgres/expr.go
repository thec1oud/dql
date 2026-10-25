package postgres

import (
	"strconv"
	"strings"

	pg "github.com/pganalyze/pg_query_go/v6"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// expr types an expression. A value is non-null only through the proofs of
// the implementation guide §5.4; every other expression is nullable.
func (a *analyzer) expr(n *pg.Node, sc *scope) (value, error) {
	switch x := n.GetNode().(type) {
	case *pg.Node_ColumnRef:
		return a.columnRef(x.ColumnRef, sc)
	case *pg.Node_ParamRef:
		p := a.params[x.ParamRef.GetNumber()-1]
		return value{typ: p.typ, param: int(x.ParamRef.GetNumber()), name: p.raw.name, loc: x.ParamRef.GetLocation()}, nil
	case *pg.Node_AConst:
		return constant(x.AConst), nil
	case *pg.Node_TypeCast:
		return a.cast(x.TypeCast, sc)
	case *pg.Node_AExpr:
		return a.operator(x.AExpr, sc)
	case *pg.Node_BoolExpr:
		return a.boolExpr(x.BoolExpr, sc)
	case *pg.Node_NullTest:
		if _, err := a.expr(x.NullTest.GetArg(), sc); err != nil {
			return value{}, err
		}
		return value{typ: tBool()}, nil
	case *pg.Node_BooleanTest:
		v, err := a.expr(x.BooleanTest.GetArg(), sc)
		if err != nil {
			return value{}, err
		}
		if _, err := a.expect(v, tBool(), true); err != nil {
			return value{}, err
		}
		return value{typ: tBool()}, nil
	case *pg.Node_CoalesceExpr:
		return a.coalesce(x.CoalesceExpr, sc)
	case *pg.Node_CaseExpr:
		return a.caseExpr(x.CaseExpr, sc)
	case *pg.Node_FuncCall:
		return a.call(x.FuncCall, sc)
	case *pg.Node_SqlvalueFunction:
		return sqlValue(x.SqlvalueFunction), nil
	case *pg.Node_SubLink:
		return a.subLink(x.SubLink, sc)
	case *pg.Node_MinMaxExpr:
		return a.minMax(x.MinMaxExpr, sc)
	case *pg.Node_AArrayExpr:
		return a.array(x.AArrayExpr, sc)
	case *pg.Node_CollateClause:
		return a.expr(x.CollateClause.GetArg(), sc)
	}
	return value{}, a.errAt("query-unsupported", location(n), "this expression is not supported in query files")
}

// location returns where n, which is set, starts, or -1 when its kind
// carries no location.
func location(n *pg.Node) int32 {
	m := n.ProtoReflect()
	inner := m.Get(m.WhichOneof(m.Descriptor().Oneofs().Get(0))).Message()
	if l := inner.Descriptor().Fields().ByName("location"); l != nil {
		return int32(inner.Get(l).Int())
	}
	return -1
}

// columnRef types a column reference: a column reached only through inner
// joins keeps its nullability; an outer join makes it nullable.
func (a *analyzer) columnRef(ref *pg.ColumnRef, sc *scope) (value, error) {
	names, ok := nameList(ref.GetFields())
	if !ok || len(names) > 3 {
		return value{}, a.errAt("query-unsupported", ref.GetLocation(), "this column reference is not supported here")
	}
	col := names[len(names)-1]
	if len(names) == 1 {
		r, c, found := a.findColumn(col, sc)
		switch {
		case !found:
			return value{}, a.errAt("query-unknown-column", ref.GetLocation(), "column %s is not a column of the FROM clause", col)
		case r == nil:
			return value{}, a.errAt("query-ambiguous-column", ref.GetLocation(), "column %s is a column of more than one relation; qualify it", col)
		}
		return read(r, c), nil
	}
	r, err := a.findRel(names[:len(names)-1], sc, ref.GetLocation())
	if err != nil {
		return value{}, err
	}
	for _, c := range r.cols {
		if c.name == col {
			return read(r, c), nil
		}
	}
	return value{}, a.errAt("query-unknown-column", ref.GetLocation(), "column %s is not a column of %s", col, r.name)
}

func read(r *rel, c column) value {
	v := c.v
	v.name = c.name
	if r.nullable {
		v.nullable = true
	}
	return v
}

// constant types a literal. An integer is int32 when it fits, as in
// Postgres, then int64, then numeric; a string's type comes from its
// context.
func constant(c *pg.A_Const) value {
	switch {
	case c.GetIsnull():
		return value{nullable: true}
	case c.GetIval() != nil:
		return value{typ: tInt32()}
	case c.GetFval() != nil:
		// The parser writes an integer too large for int32 as a float.
		if _, err := strconv.ParseInt(c.GetFval().GetFval(), 10, 64); err == nil {
			return value{typ: tInt64()}
		}
		return value{typ: tDecimal()}
	case c.GetBoolval() != nil:
		return value{typ: tBool()}
	case c.GetBsval() != nil:
		return value{typ: &irv1.Type{Kind: &irv1.Type_Bit{Bit: &irv1.Bit{Varying: true}}}}
	}
	return value{literal: true}
}

// cast types a cast: the value takes the cast's type and keeps its
// nullability.
func (a *analyzer) cast(c *pg.TypeCast, sc *scope) (value, error) {
	t, err := a.castType(c.GetTypeName())
	if err != nil {
		return value{}, err
	}
	v, err := a.expr(c.GetArg(), sc)
	if err != nil {
		return value{}, err
	}
	// The cast fixes the type of a parameter it casts, unless a column
	// already has (§17.2).
	if v.param != 0 && !a.params[v.param-1].strong {
		a.give(v, t, true)
	}
	name := v.name
	if name == "" {
		ns, _ := nameList(c.GetTypeName().GetNames())
		name = ns[len(ns)-1]
	}
	return value{typ: t, nullable: v.nullable, annotated: v.annotated, name: name}, nil
}

// expect gives an untyped value the type its context needs: a parameter
// takes it, and a literal or NULL becomes it. A typed value is returned as
// it is. strong says the context fixes the type, as a column does: a
// parameter's type from a weak context, such as a function argument, gives
// way to it, and two different strong types are an error.
func (a *analyzer) expect(v value, t *irv1.Type, strong bool) (value, error) {
	if v.param != 0 {
		if p := a.params[v.param-1]; strong && p.strong && !sameType(p.typ, t) {
			return value{}, a.errAt("query-param-conflict", v.loc, "parameter %s is used as two different types; cast it where it is used, such as @%s::bigint", p.raw.name, p.raw.name)
		}
	}
	return a.give(v, t, strong), nil
}

// give is expect for a value that cannot conflict: one from a weak
// context, or an untyped one.
func (a *analyzer) give(v value, t *irv1.Type, strong bool) value {
	if v.param != 0 {
		p := a.params[v.param-1]
		if p.typ == nil || strong && !p.strong {
			p.typ, p.strong = t, strong
		}
		v.typ = p.typ
		return v
	}
	if v.typ == nil {
		v.typ, v.literal = t, false
	}
	return v
}

// unify gives an untyped side of a binary expression the other side's type.
func (a *analyzer) unify(l, r value, strong bool) (value, value, error) {
	var err error
	switch {
	case l.typ != nil && (r.typ == nil || r.literal || r.param != 0):
		r, err = a.expect(r, l.typ, strong && l.param == 0)
	case r.typ != nil && (l.typ == nil || l.literal || l.param != 0):
		l, err = a.expect(l, r.typ, strong && r.param == 0)
	}
	return l, r, err
}

// settle types a value no context typed: a literal becomes text; an untyped
// parameter or NULL stays untyped, and the caller reports it.
func settle(v value) value {
	if v.literal && v.typ == nil {
		v.typ = tText()
	}
	return v
}

func nonnull(vs ...value) bool {
	for _, v := range vs {
		if v.nullable {
			return false
		}
	}
	return true
}

func anyAnnotated(vs ...value) bool {
	for _, v := range vs {
		if v.annotated {
			return true
		}
	}
	return false
}

// strict is the value of a strict operator or function of args: non-null
// when every argument is (guide §5.4).
func strict(t *irv1.Type, args ...value) value {
	return value{typ: t, nullable: !nonnull(args...), annotated: anyAnnotated(args...)}
}

var comparisons = map[string]bool{"=": true, "<>": true, "!=": true, "<": true, ">": true, "<=": true, ">=": true}

var textMatches = map[string]bool{"~": true, "~*": true, "!~": true, "!~*": true, "~~": true, "~~*": true, "!~~": true, "!~~*": true, "^@": true}

var jsonTests = map[string]bool{"@>": true, "<@": true, "?": true, "?|": true, "?&": true, "&&": true, "@?": true, "@@": true}

// operator types an operator expression. Postgres's operators are strict,
// so the result is non-null when the operands are.
func (a *analyzer) operator(e *pg.A_Expr, sc *scope) (value, error) {
	// An operator may be written OPERATOR(pg_catalog.=).
	ns, _ := nameList(e.GetName())
	op := ns[len(ns)-1]
	if len(ns) > 1 && ns[0] != "pg_catalog" {
		op = joinDots(ns)
	}
	switch e.GetKind() {
	case pg.A_Expr_Kind_AEXPR_OP:
		if e.GetLexpr() == nil {
			return a.prefix(e, op, sc)
		}
		return a.binary(e, op, sc)
	case pg.A_Expr_Kind_AEXPR_DISTINCT, pg.A_Expr_Kind_AEXPR_NOT_DISTINCT:
		if _, _, err := a.operands(e, sc, true); err != nil {
			return value{}, err
		}
		return value{typ: tBool()}, nil
	case pg.A_Expr_Kind_AEXPR_NULLIF:
		l, _, err := a.operands(e, sc, true)
		if err != nil {
			return value{}, err
		}
		return value{typ: l.typ, nullable: true, name: "nullif"}, nil
	case pg.A_Expr_Kind_AEXPR_LIKE, pg.A_Expr_Kind_AEXPR_ILIKE, pg.A_Expr_Kind_AEXPR_SIMILAR:
		return a.match(e, sc)
	case pg.A_Expr_Kind_AEXPR_OP_ANY, pg.A_Expr_Kind_AEXPR_OP_ALL:
		return a.quantified(e, sc)
	case pg.A_Expr_Kind_AEXPR_IN:
		return a.in(e, sc)
	}
	// The four kinds of BETWEEN are all that remain.
	return a.between(e, sc)
}

// operands types both sides of a binary operator and unifies their types.
func (a *analyzer) operands(e *pg.A_Expr, sc *scope, strong bool) (value, value, error) {
	l, err := a.expr(e.GetLexpr(), sc)
	if err != nil {
		return value{}, value{}, err
	}
	r, err := a.expr(e.GetRexpr(), sc)
	if err != nil {
		return value{}, value{}, err
	}
	return a.unify(l, r, strong)
}

// prefix types a prefix operator: unary minus or plus.
func (a *analyzer) prefix(e *pg.A_Expr, op string, sc *scope) (value, error) {
	v, err := a.expr(e.GetRexpr(), sc)
	if err != nil {
		return value{}, err
	}
	if op != "-" && op != "+" || rank(a.ix.base(v.typ)) == 0 && v.typ.GetInterval() == nil {
		return value{}, a.errAt("query-unsupported", e.GetLocation(), "prefix operator %s is supported only on a number or an interval", op)
	}
	return strict(a.ix.base(v.typ), v), nil
}

// binary types a binary operator.
func (a *analyzer) binary(e *pg.A_Expr, op string, sc *scope) (value, error) {
	switch {
	case comparisons[op]:
		l, r, err := a.operands(e, sc, true)
		if err != nil {
			return value{}, err
		}
		return strict(tBool(), l, r), nil
	case textMatches[op]:
		return a.match(e, sc)
	case jsonTests[op]:
		l, r, err := a.operands(e, sc, false)
		if err != nil {
			return value{}, err
		}
		return strict(tBool(), l, r), nil
	case op == "->" || op == "#>":
		l, r, err := a.jsonOperands(e, sc)
		if err != nil {
			return value{}, err
		}
		return strict(a.ix.base(l.typ), l, r), nil
	case op == "->>" || op == "#>>":
		l, r, err := a.jsonOperands(e, sc)
		if err != nil {
			return value{}, err
		}
		return strict(tText(), l, r), nil
	case op == "||":
		return a.concat(e, sc)
	case op == "+" || op == "-" || op == "*" || op == "/" || op == "%":
		return a.arithmetic(e, op, sc)
	}
	return value{}, a.errAt("query-unsupported", e.GetLocation(), "operator %s is not supported in query files", op)
}

// jsonOperands types the operands of ->, ->>, #> and #>>, whose left side
// is json or jsonb.
func (a *analyzer) jsonOperands(e *pg.A_Expr, sc *scope) (value, value, error) {
	l, err := a.expr(e.GetLexpr(), sc)
	if err != nil {
		return value{}, value{}, err
	}
	r, err := a.expr(e.GetRexpr(), sc)
	if err != nil {
		return value{}, value{}, err
	}
	b := a.ix.base(l.typ)
	if b.GetJson() == nil && b.GetJsonb() == nil {
		return value{}, value{}, a.errAt("query-unsupported", e.GetLocation(), "this operator needs a json or jsonb value on its left; cast it")
	}
	r = settle(r)
	if r.typ == nil {
		return value{}, value{}, a.errAt("query-param-untyped", e.GetLocation(), "the right side of a json operator needs a type; cast it, such as @key::text")
	}
	return l, r, nil
}

// match types LIKE, ILIKE, SIMILAR TO and the regular expression
// operators, whose operands are text.
func (a *analyzer) match(e *pg.A_Expr, sc *scope) (value, error) {
	var vs []value
	for _, n := range []*pg.Node{e.GetLexpr(), e.GetRexpr()} {
		v, err := a.expr(n, sc)
		if err != nil {
			return value{}, err
		}
		vs = append(vs, a.give(v, tText(), false))
	}
	return strict(tBool(), vs...), nil
}

// concat types ||: text, json or arrays.
func (a *analyzer) concat(e *pg.A_Expr, sc *scope) (value, error) {
	l, r, err := a.operands(e, sc, false)
	if err != nil {
		return value{}, err
	}
	l, r = settle(l), settle(r)
	lb, rb := a.ix.base(l.typ), a.ix.base(r.typ)
	switch {
	case isText(lb) || isText(rb):
		return strict(tText(), a.give(l, tText(), false), a.give(r, tText(), false)), nil
	case lb.GetJsonb() != nil && rb.GetJsonb() != nil:
		return strict(tJSONB(), l, r), nil
	case lb.GetArray() != nil && sameType(lb, rb):
		return strict(bare(lb), l, r), nil
	}
	return value{}, a.errAt("query-unsupported", e.GetLocation(), "|| is supported on text, jsonb and arrays of one type; cast its operands")
}

// arithmetic types +, -, *, / and %: numbers take the wider type, and the
// time types follow Postgres's operators.
func (a *analyzer) arithmetic(e *pg.A_Expr, op string, sc *scope) (value, error) {
	l, r, err := a.operands(e, sc, false)
	if err != nil {
		return value{}, err
	}
	if l.typ == nil || r.typ == nil {
		return value{}, a.errAt("query-param-untyped", e.GetLocation(), "both sides of %s are untyped; cast one, such as @n::bigint", op)
	}
	lb, rb := a.ix.base(l.typ), a.ix.base(r.typ)
	if t := arithmeticType(op, lb, rb); t != nil {
		return strict(t, l, r), nil
	}
	return value{}, a.errAt("query-unsupported", e.GetLocation(), "operator %s is not supported on these types in query files; cast its operands", op)
}

// arithmeticType returns the type of l op r, or nil.
func arithmeticType(op string, l, r *irv1.Type) *irv1.Type {
	lr, rr := rank(l), rank(r)
	if lr > 0 && rr > 0 {
		t := l
		if rr > lr {
			t = r
		}
		// A float with an integer or numeric is double precision.
		if rank(t) == 5 && min(lr, rr) < 5 {
			return tFloat64()
		}
		return bare(t)
	}
	interval := func(t *irv1.Type) bool { return t.GetInterval() != nil }
	timestamp := func(t *irv1.Type) bool { return t.GetInstant() != nil || t.GetLocalDatetime() != nil }
	switch {
	case (op == "+" || op == "-") && timestamp(l) && interval(r):
		return bare(l)
	case op == "+" && interval(l) && timestamp(r):
		return bare(r)
	case op == "-" && timestamp(l) && sameType(l, r):
		return tInterval()
	case (op == "+" || op == "-") && interval(l) && interval(r):
		return tInterval()
	case (op == "*" || op == "/") && interval(l) && rr > 0:
		return tInterval()
	case op == "*" && lr > 0 && interval(r):
		return tInterval()
	case (op == "+" || op == "-") && l.GetDate() != nil && (rr == 1 || rr == 2):
		return tDate()
	case op == "+" && (lr == 1 || lr == 2) && r.GetDate() != nil:
		return tDate()
	case op == "-" && l.GetDate() != nil && r.GetDate() != nil:
		return tInt32()
	case (op == "+" || op == "-") && l.GetDate() != nil && interval(r):
		return tLocalDatetime()
	}
	return nil
}

// quantified types x op ANY (array) and x op ALL (array). A list parameter
// (sqlc.slice) is the array: it takes x's type as its element type (§25).
func (a *analyzer) quantified(e *pg.A_Expr, sc *scope) (value, error) {
	l, err := a.expr(e.GetLexpr(), sc)
	if err != nil {
		return value{}, err
	}
	r, err := a.expr(e.GetRexpr(), sc)
	if err != nil {
		return value{}, err
	}
	if r.param != 0 && a.params[r.param-1].raw.kind == sliceParam {
		if l.typ == nil {
			return value{}, a.errAt("query-param-untyped", e.GetLocation(), "both sides of ANY are untyped; cast the left one")
		}
		if _, err := a.expect(r, l.typ, true); err != nil {
			return value{}, err
		}
		return strict(tBool(), l), nil
	}
	switch {
	case l.typ != nil && r.typ == nil:
		a.give(r, tArray(l.typ, true), true)
	case l.typ == nil && r.typ.GetArray() != nil:
		a.give(l, r.typ.GetArray().GetElement(), true)
	case l.typ == nil:
		return value{}, a.errAt("query-param-untyped", e.GetLocation(), "both sides of ANY are untyped; cast one")
	}
	return strict(tBool(), l, r), nil
}

// in types x IN (a, b, ...). A list parameter is written x = ANY(...) on
// Postgres, where it is one placeholder (§25.3).
func (a *analyzer) in(e *pg.A_Expr, sc *scope) (value, error) {
	l, err := a.expr(e.GetLexpr(), sc)
	if err != nil {
		return value{}, err
	}
	// x IN (SELECT ...) is a sublink; here the right side is a list.
	vs := []value{l}
	for _, it := range e.GetRexpr().GetList().GetItems() {
		v, err := a.expr(it, sc)
		if err != nil {
			return value{}, err
		}
		if v.param != 0 && a.params[v.param-1].raw.kind == sliceParam {
			return value{}, a.errAt("query-list-param", e.GetLocation(), "on Postgres a list parameter is written = ANY(sqlc.slice(%s)), not IN (sqlc.slice(%s))", v.name, v.name)
		}
		if l, v, err = a.unify(l, v, true); err != nil {
			return value{}, err
		}
		vs = append(vs, v)
	}
	if l.typ == nil {
		return value{}, a.errAt("query-param-untyped", e.GetLocation(), "the values of IN are untyped; cast one")
	}
	return strict(tBool(), vs...), nil
}

// between types x BETWEEN a AND b.
func (a *analyzer) between(e *pg.A_Expr, sc *scope) (value, error) {
	l, err := a.expr(e.GetLexpr(), sc)
	if err != nil {
		return value{}, err
	}
	vs := []value{l}
	for _, it := range e.GetRexpr().GetList().GetItems() {
		v, err := a.expr(it, sc)
		if err != nil {
			return value{}, err
		}
		if l, v, err = a.unify(l, v, true); err != nil {
			return value{}, err
		}
		vs = append(vs, v)
	}
	return strict(tBool(), vs...), nil
}

// boolExpr types AND, OR and NOT.
func (a *analyzer) boolExpr(b *pg.BoolExpr, sc *scope) (value, error) {
	var vs []value
	for _, n := range b.GetArgs() {
		v, err := a.expr(n, sc)
		if err != nil {
			return value{}, err
		}
		if v, err = a.expect(v, tBool(), true); err != nil {
			return value{}, err
		}
		vs = append(vs, v)
	}
	return strict(tBool(), vs...), nil
}

// coalesce types COALESCE: non-null when any argument is (guide §5.4).
func (a *analyzer) coalesce(c *pg.CoalesceExpr, sc *scope) (value, error) {
	vs, t, err := a.common(c.GetArgs(), sc, c.GetLocation())
	if err != nil {
		return value{}, err
	}
	v := value{typ: t, nullable: true, name: "coalesce"}
	for _, x := range vs {
		if !x.nullable {
			v.nullable, v.annotated = false, x.annotated
			break
		}
	}
	return v, nil
}

// common types a list of values that share one type, such as COALESCE's
// arguments: text when they are all text, the widest number when they are
// all numbers, and otherwise the one type they all have. Untyped values
// take it.
func (a *analyzer) common(args []*pg.Node, sc *scope, loc int32) ([]value, *irv1.Type, error) {
	vs := make([]value, len(args))
	var t *irv1.Type
	for i, n := range args {
		v, err := a.expr(n, sc)
		if err != nil {
			return nil, nil, err
		}
		vs[i] = v
		if v.typ == nil || v.literal || v.param != 0 {
			continue
		}
		switch tb, vb := a.ix.base(t), a.ix.base(v.typ); {
		case t == nil, sameType(t, v.typ):
			if t == nil {
				t = v.typ
			}
		case isText(tb) && isText(vb):
			t = tText()
		case rank(tb) > 0 && rank(vb) > 0:
			t = arithmeticType("+", tb, vb)
		default:
			return nil, nil, a.errAt("query-unsupported", loc, "these values have different types; cast them to one")
		}
	}
	for i, v := range vs {
		if t == nil {
			v = settle(v)
			t = v.typ
		}
		v, err := a.expect(v, t, true)
		if err != nil {
			return nil, nil, err
		}
		vs[i] = v
	}
	if t == nil {
		return nil, nil, a.errAt("query-param-untyped", loc, "every value here is untyped; cast one")
	}
	return vs, t, nil
}

// caseExpr types CASE: non-null with an ELSE when every branch is
// (guide §5.4).
func (a *analyzer) caseExpr(c *pg.CaseExpr, sc *scope) (value, error) {
	var subject value
	if c.GetArg() != nil {
		var err error
		if subject, err = a.expr(c.GetArg(), sc); err != nil {
			return value{}, err
		}
	}
	var results []*pg.Node
	for _, n := range c.GetArgs() {
		w := n.GetCaseWhen()
		cond, err := a.expr(w.GetExpr(), sc)
		if err != nil {
			return value{}, err
		}
		if c.GetArg() != nil {
			if subject, _, err = a.unify(subject, cond, true); err != nil {
				return value{}, err
			}
		} else if _, err := a.expect(cond, tBool(), true); err != nil {
			return value{}, err
		}
		results = append(results, w.GetResult())
	}
	if c.GetDefresult() != nil {
		results = append(results, c.GetDefresult())
	}
	vs, t, err := a.common(results, sc, c.GetLocation())
	if err != nil {
		return value{}, err
	}
	nn := c.GetDefresult() != nil && nonnull(vs...)
	return value{typ: t, nullable: !nn, annotated: nn && anyAnnotated(vs...), name: "case"}, nil
}

// minMax types GREATEST and LEAST. They ignore NULL arguments, which no
// proof of guide §5.4 covers, so the result is nullable.
func (a *analyzer) minMax(m *pg.MinMaxExpr, sc *scope) (value, error) {
	_, t, err := a.common(m.GetArgs(), sc, m.GetLocation())
	if err != nil {
		return value{}, err
	}
	name := "greatest"
	if m.GetOp() == pg.MinMaxOp_IS_LEAST {
		name = "least"
	}
	return value{typ: t, nullable: true, name: name}, nil
}

// array types ARRAY[...]. The constructor is not among the proofs of guide
// §5.4, so it is nullable.
func (a *analyzer) array(e *pg.A_ArrayExpr, sc *scope) (value, error) {
	vs, t, err := a.common(e.GetElements(), sc, e.GetLocation())
	if err != nil {
		return value{}, err
	}
	return value{typ: tArray(t, !nonnull(vs...)), nullable: true, name: "array"}, nil
}

// subLink types a subquery: EXISTS is non-null (guide §5.4); a scalar
// subquery is nullable, as it may return no row.
func (a *analyzer) subLink(s *pg.SubLink, sc *scope) (value, error) {
	inner := &scope{parent: sc}
	cols, err := a.selectStmt(s.GetSubselect().GetSelectStmt(), inner)
	if err != nil {
		return value{}, err
	}
	switch s.GetSubLinkType() {
	case pg.SubLinkType_EXISTS_SUBLINK:
		return value{typ: tBool(), name: "exists"}, nil
	case pg.SubLinkType_EXPR_SUBLINK, pg.SubLinkType_ARRAY_SUBLINK:
		if len(cols) != 1 {
			return value{}, a.errAt("query-unsupported", s.GetLocation(), "a subquery used as a value returns one column, not %d", len(cols))
		}
		c := settle(cols[0].v)
		if c.typ == nil {
			return value{}, a.errAt("query-param-untyped", s.GetLocation(), "the subquery's column is untyped; cast it")
		}
		if s.GetSubLinkType() == pg.SubLinkType_ARRAY_SUBLINK {
			return value{typ: tArray(c.typ, c.nullable), nullable: true, name: "array"}, nil
		}
		return value{typ: c.typ, nullable: true, name: cols[0].name}, nil
	}
	// ANY and ALL, as IN (SELECT ...) is written, are what remain: the
	// parser writes row comparisons as operators on rows.
	l, err := a.expr(s.GetTestexpr(), sc)
	if err != nil {
		return value{}, err
	}
	if len(cols) != 1 {
		return value{}, a.errAt("query-unsupported", s.GetLocation(), "the subquery of IN, ANY or ALL returns one column, not %d", len(cols))
	}
	if _, _, err := a.unify(l, cols[0].v, true); err != nil {
		return value{}, err
	}
	return value{typ: tBool(), nullable: true}, nil
}

// sqlValue types the SQL value functions, such as CURRENT_TIMESTAMP, which
// take no arguments and are never null, except CURRENT_SCHEMA, which is
// null when no schema of the search path exists. The user and role
// functions are text.
func sqlValue(f *pg.SQLValueFunction) value {
	op := f.GetOp()
	v := value{typ: tText(), nullable: op == pg.SQLValueFunctionOp_SVFOP_CURRENT_SCHEMA,
		name: strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(op.String(), "SVFOP_"), "_N"))}
	if t, ok := sqlValueTypes[op]; ok {
		v.typ = t()
	}
	return v
}

var sqlValueTypes = map[pg.SQLValueFunctionOp]func() *irv1.Type{
	pg.SQLValueFunctionOp_SVFOP_CURRENT_DATE:        tDate,
	pg.SQLValueFunctionOp_SVFOP_CURRENT_TIME:        tTimeWithOffset,
	pg.SQLValueFunctionOp_SVFOP_CURRENT_TIME_N:      tTimeWithOffset,
	pg.SQLValueFunctionOp_SVFOP_CURRENT_TIMESTAMP:   tInstant,
	pg.SQLValueFunctionOp_SVFOP_CURRENT_TIMESTAMP_N: tInstant,
	pg.SQLValueFunctionOp_SVFOP_LOCALTIME:           tLocalTime,
	pg.SQLValueFunctionOp_SVFOP_LOCALTIME_N:         tLocalTime,
	pg.SQLValueFunctionOp_SVFOP_LOCALTIMESTAMP:      tLocalDatetime,
	pg.SQLValueFunctionOp_SVFOP_LOCALTIMESTAMP_N:    tLocalDatetime,
}
