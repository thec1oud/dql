package mysql

import (
	"strings"

	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/charset"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/parser/opcode"
	"github.com/pingcap/tidb/pkg/parser/test_driver"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// expr types an expression. A value is non-null only through the proofs of
// the implementation guide §5.4; every other expression is nullable.
func (a *analyzer) expr(n ast.ExprNode, sc *scope) (value, error) {
	off := n.OriginTextPosition()
	switch x := n.(type) {
	case *ast.ColumnNameExpr:
		return a.columnRef(x, sc)
	case *test_driver.ParamMarkerExpr:
		use := a.r.use(x.Offset)
		i := a.r.uses[use]
		p := a.params[i]
		return value{typ: p.typ, param: i + 1, use: use, name: p.raw.name, off: x.Offset}, nil
	case *test_driver.ValueExpr:
		return a.constant(x)
	case *ast.ParenthesesExpr:
		return a.expr(x.Expr, sc)
	case *ast.SetCollationExpr:
		return a.expr(x.Expr, sc)
	case *ast.FuncCastExpr:
		return a.cast(x, sc)
	case *ast.UnaryOperationExpr:
		return a.unary(x, sc)
	case *ast.BinaryOperationExpr:
		return a.binary(x, sc)
	case *ast.IsNullExpr:
		if _, err := a.expr(x.Expr, sc); err != nil {
			return value{}, err
		}
		return value{typ: tBool()}, nil
	case *ast.IsTruthExpr:
		v, err := a.expr(x.Expr, sc)
		if err != nil {
			return value{}, err
		}
		if _, err := a.expect(v, tBool(), true); err != nil {
			return value{}, err
		}
		return value{typ: tBool()}, nil
	case *ast.PatternInExpr:
		return a.in(x, sc)
	case *ast.PatternLikeOrIlikeExpr:
		return a.match(sc, x.Expr, x.Pattern)
	case *ast.PatternRegexpExpr:
		return a.match(sc, x.Expr, x.Pattern)
	case *ast.BetweenExpr:
		return a.between(x, sc)
	case *ast.CaseExpr:
		return a.caseExpr(x, sc)
	case *ast.FuncCallExpr:
		return a.call(x, sc)
	case *ast.AggregateFuncExpr:
		return a.aggregate(x, sc)
	case *ast.WindowFuncExpr:
		return a.window(x, sc)
	case *ast.SubqueryExpr:
		cols, err := a.subquery(x, sc, off)
		if err != nil {
			return value{}, err
		}
		// A scalar subquery is NULL when it returns no row.
		return value{typ: cols[0].v.typ, nullable: true}, nil
	case *ast.ExistsSubqueryExpr:
		if _, err := a.resultSet(x.Sel.(*ast.SubqueryExpr).Query, &scope{parent: sc}); err != nil {
			return value{}, err
		}
		return value{typ: tBool()}, nil
	case *ast.CompareSubqueryExpr:
		return a.compareSubquery(x, sc)
	case *ast.ValuesExpr:
		return a.valuesRef(x)
	}
	return value{}, a.errAt("query-unsupported", off, "this expression is not supported in query files")
}

// columnRef types a column reference: a column reached only through inner
// joins keeps its nullability; an outer join makes it nullable.
func (a *analyzer) columnRef(ref *ast.ColumnNameExpr, sc *scope) (value, error) {
	n, off := ref.Name, ref.OriginTextPosition()
	col := n.Name.O
	if n.Table.O == "" {
		r, c, found := a.findColumn(col, sc)
		switch {
		case !found:
			return value{}, a.errAt("query-unknown-column", off, "column %s is not a column of the FROM clause", col)
		case r == nil:
			return value{}, a.errAt("query-ambiguous-column", off, "column %s is a column of more than one relation; qualify it", col)
		}
		return read(r, c), nil
	}
	r, err := a.findRel(n.Schema.O, n.Table.O, sc, off)
	if err != nil {
		return value{}, err
	}
	for _, c := range r.cols {
		if strings.EqualFold(c.name, col) {
			return read(r, c), nil
		}
	}
	return value{}, a.errAt("query-unknown-column", off, "column %s is not a column of %s", col, r.name)
}

func read(r *rel, c column) value {
	v := c.v
	v.name = c.name
	if r.nullable {
		v.nullable = true
	}
	return v
}

// valuesRef types VALUES(column), the value an INSERT proposed for a column,
// in its ON DUPLICATE KEY UPDATE.
func (a *analyzer) valuesRef(x *ast.ValuesExpr) (value, error) {
	off := x.OriginTextPosition()
	if a.values == nil {
		return value{}, a.errAt("query-unsupported", off, "VALUES(column) is supported only in ON DUPLICATE KEY UPDATE")
	}
	c, err := a.targetColumn(a.values, x.Column.Name)
	if err != nil {
		return value{}, err
	}
	return value{typ: c.v.typ, nullable: c.v.nullable, annotated: c.v.annotated}, nil
}

// constant types a literal: an integer is BIGINT, as MySQL types it, a
// decimal literal DECIMAL and one with an exponent DOUBLE; TRUE and FALSE
// are booleans, hex and bit literals binary strings; a string's type comes
// from its context.
func (a *analyzer) constant(c *test_driver.ValueExpr) (value, error) {
	switch c.Kind() {
	case test_driver.KindNull:
		return value{nullable: true}, nil
	case test_driver.KindInt64:
		if mysql.HasIsBooleanFlag(c.Type.GetFlag()) {
			return value{typ: tBool()}, nil
		}
		return value{typ: tInt64()}, nil
	case test_driver.KindUint64:
		return value{typ: tUint64()}, nil
	case test_driver.KindFloat64:
		return value{typ: tFloat64()}, nil
	case test_driver.KindMysqlDecimal:
		return value{typ: tDecimal()}, nil
	case test_driver.KindBinaryLiteral:
		return value{typ: tBytes()}, nil
	}
	// A string, the parser's last kind of literal: _binary 'x' is a binary
	// string.
	if c.Type.GetCharset() == charset.CharsetBin {
		return value{typ: tBytes()}, nil
	}
	return value{literal: true}, nil
}

// cast types CAST and CONVERT: the value takes the target type. A cast to
// a date or time is NULL for text that is no date or time, so it is
// nullable; the others keep the value's nullability.
func (a *analyzer) cast(c *ast.FuncCastExpr, sc *scope) (value, error) {
	t := castType(c.Tp)
	if t == nil {
		return value{}, a.errAt("query-unsupported", c.OriginTextPosition(), "a cast to %s is not supported in query files", c.Tp.String())
	}
	v, err := a.expr(c.Expr, sc)
	if err != nil {
		return value{}, err
	}
	// The cast fixes the type of a parameter it casts, unless a column
	// already has (§17.2).
	if v.param != 0 && !a.params[v.param-1].strong {
		a.give(v, t, true)
	}
	out := value{typ: t, nullable: v.nullable, annotated: v.annotated}
	switch t.GetKind().(type) {
	case *irv1.Type_Date, *irv1.Type_LocalDatetime, *irv1.Type_Interval:
		out.nullable, out.annotated = true, false
	}
	return out, nil
}

// expect gives an untyped value the type its context needs: a parameter
// takes it, and a literal or NULL becomes it. A typed value is returned as
// it is. strong says the context fixes the type, as a column does: a
// parameter's type from a weak context, such as a function argument, gives
// way to it, and two different strong types are an error.
func (a *analyzer) expect(v value, t *irv1.Type, strong bool) (value, error) {
	if v.param != 0 {
		if p := a.params[v.param-1]; strong && p.strong && !sameType(p.typ, t) {
			return value{}, a.errAt("query-param-conflict", v.off, "parameter %s is used as two different types; cast it where it is used, such as CAST(@%s AS SIGNED)", p.raw.name, p.raw.name)
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

// strict is the value of an operator or function of args that is NULL
// only for a NULL argument: non-null when every argument is (guide §5.4).
func strict(t *irv1.Type, args ...value) value {
	return value{typ: t, nullable: !nonnull(args...), annotated: anyAnnotated(args...)}
}

// unary types NOT, ! and unary minus and plus.
func (a *analyzer) unary(e *ast.UnaryOperationExpr, sc *scope) (value, error) {
	v, err := a.expr(e.V, sc)
	if err != nil {
		return value{}, err
	}
	switch e.Op {
	case opcode.Not, opcode.Not2:
		if v, err = a.expect(v, tBool(), true); err != nil {
			return value{}, err
		}
		return strict(tBool(), v), nil
	case opcode.Minus, opcode.Plus:
		if rank(v.typ) > 0 {
			return strict(bare(v.typ), v), nil
		}
	}
	return value{}, a.errAt("query-unsupported", e.OriginTextPosition(), "operator %s is supported only on a number in query files", e.Op)
}

// binary types a binary operator.
func (a *analyzer) binary(e *ast.BinaryOperationExpr, sc *scope) (value, error) {
	off := e.OriginTextPosition()
	switch e.Op {
	case opcode.LogicAnd, opcode.LogicOr, opcode.LogicXor:
		var vs []value
		for _, n := range []ast.ExprNode{e.L, e.R} {
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
	case opcode.EQ, opcode.NE, opcode.LT, opcode.LE, opcode.GT, opcode.GE, opcode.NullEQ:
		l, r, err := a.operands(e, sc, true)
		if err != nil {
			return value{}, err
		}
		if e.Op == opcode.NullEQ {
			return value{typ: tBool()}, nil
		}
		return strict(tBool(), l, r), nil
	case opcode.Plus, opcode.Minus, opcode.Mul, opcode.Div, opcode.IntDiv, opcode.Mod:
		l, r, err := a.operands(e, sc, false)
		if err != nil {
			return value{}, err
		}
		if l.typ == nil || r.typ == nil {
			return value{}, a.errAt("query-param-untyped", off, "both sides of %s are untyped; cast one, such as CAST(@n AS SIGNED)", e.Op)
		}
		t := arithmeticType(e.Op, a.base(l.typ), a.base(r.typ))
		if t == nil {
			return value{}, a.errAt("query-unsupported", off, "operator %s is supported only on numbers in query files; use DATE_ADD and DATEDIFF for dates and times", e.Op)
		}
		v := strict(t, l, r)
		if e.Op == opcode.Div || e.Op == opcode.IntDiv || e.Op == opcode.Mod {
			// Division by zero is NULL.
			v.nullable, v.annotated = true, false
		}
		return v, nil
	}
	return value{}, a.errAt("query-unsupported", off, "operator %s is not supported in query files", e.Op)
}

// base is the type arithmetic sees: an enum's text.
func (a *analyzer) base(t *irv1.Type) *irv1.Type {
	if t.GetNamed().GetEnum() != nil {
		return tText()
	}
	return t
}

// operands types both sides of a binary operator and unifies their types.
func (a *analyzer) operands(e *ast.BinaryOperationExpr, sc *scope, strong bool) (value, value, error) {
	l, err := a.expr(e.L, sc)
	if err != nil {
		return value{}, value{}, err
	}
	r, err := a.expr(e.R, sc)
	if err != nil {
		return value{}, value{}, err
	}
	return a.unify(l, r, strong)
}

// arithmeticType returns the type of l op r, or nil, as MySQL computes it:
// in DOUBLE when either side is floating-point, in DECIMAL when either is
// DECIMAL or for /, and otherwise in BIGINT, unsigned when either side is.
func arithmeticType(op opcode.Op, l, r *irv1.Type) *irv1.Type {
	lr, rr := rank(l), rank(r)
	switch {
	case lr == 0 || rr == 0:
		return nil
	case max(lr, rr) >= 6:
		return tFloat64()
	case op == opcode.IntDiv:
		return tInt64()
	case max(lr, rr) == 5 || op == opcode.Div:
		return tDecimal()
	case unsigned(l) || unsigned(r):
		return tUint64()
	}
	return tInt64()
}

func unsigned(t *irv1.Type) bool {
	switch t.GetKind().(type) {
	case *irv1.Type_Uint8, *irv1.Type_Uint16, *irv1.Type_Uint32, *irv1.Type_Uint64:
		return true
	}
	return false
}

// in types x IN (a, b, ...), x IN (SELECT ...) and a list parameter, x IN
// (sqlc.slice(name)). The list is one JSON array placeholder, which the
// statement expands with JSON_TABLE into rows of x's column type (§25.3).
func (a *analyzer) in(e *ast.PatternInExpr, sc *scope) (value, error) {
	off := e.OriginTextPosition()
	l, err := a.expr(e.Expr, sc)
	if err != nil {
		return value{}, err
	}
	if e.Sel != nil {
		cols, err := a.subquery(e.Sel.(*ast.SubqueryExpr), sc, off)
		if err != nil {
			return value{}, err
		}
		if _, _, err := a.unify(l, cols[0].v, true); err != nil {
			return value{}, err
		}
		return value{typ: tBool(), nullable: true}, nil
	}
	if pm, ok := e.List[0].(*test_driver.ParamMarkerExpr); ok && a.params[a.r.uses[a.r.use(pm.Offset)]].raw.kind == sliceParam {
		r, _ := a.expr(pm, sc)
		if l.col == nil {
			return value{}, a.errAt("query-list-param", off, "the left side of IN (sqlc.slice(%s)) must be a column, whose type the list's elements take", r.name)
		}
		if _, err := a.expect(r, l.typ, true); err != nil {
			return value{}, err
		}
		a.lists[r.use] = listType(l.col)
		return strict(tBool(), l), nil
	}
	vs := []value{l}
	for _, it := range e.List {
		v, err := a.expr(it, sc)
		if err != nil {
			return value{}, err
		}
		if l, v, err = a.unify(l, v, true); err != nil {
			return value{}, err
		}
		vs = append(vs, v)
	}
	if l.typ == nil {
		return value{}, a.errAt("query-param-untyped", off, "the values of IN are untyped; cast one")
	}
	return strict(tBool(), vs...), nil
}

// listType is the JSON_TABLE column type of a list compared with column c:
// its own type, with its character set and collation, so the comparison is
// the column's own.
func listType(c *irv1.Column) string {
	t := c.GetType().GetNative().GetRaw()
	if co := c.GetCollation().GetName(); co != "" {
		cs, _, _ := strings.Cut(co, "_")
		t += " CHARACTER SET " + cs + " COLLATE " + co
	}
	return t
}

// match types LIKE and REGEXP, whose operands are text.
func (a *analyzer) match(sc *scope, ns ...ast.ExprNode) (value, error) {
	var vs []value
	for _, n := range ns {
		v, err := a.expr(n, sc)
		if err != nil {
			return value{}, err
		}
		vs = append(vs, a.give(v, tText(), false))
	}
	return strict(tBool(), vs...), nil
}

// between types x BETWEEN a AND b.
func (a *analyzer) between(e *ast.BetweenExpr, sc *scope) (value, error) {
	l, err := a.expr(e.Expr, sc)
	if err != nil {
		return value{}, err
	}
	vs := []value{l}
	for _, n := range []ast.ExprNode{e.Left, e.Right} {
		v, err := a.expr(n, sc)
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

// common types a list of values that share one type, such as COALESCE's
// arguments: text when they are all text, the widest number when they are
// all numbers, and otherwise the one type they all have. Untyped values
// take it.
func (a *analyzer) common(args []ast.ExprNode, sc *scope, off int) ([]value, *irv1.Type, error) {
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
		switch tb, vb := a.base(t), a.base(v.typ); {
		case t == nil:
			t = v.typ
		case sameType(t, v.typ):
		case isText(tb) && isText(vb):
			t = tText()
		case isInteger(tb) && isInteger(vb) && unsigned(tb) == unsigned(vb):
			if rank(vb) > rank(tb) {
				t = v.typ
			}
		case rank(tb) > 0 && rank(vb) > 0:
			t = arithmeticType(opcode.Plus, tb, vb)
		default:
			return nil, nil, a.errAt("query-unsupported", off, "these values have different types; cast them to one")
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
		return nil, nil, a.errAt("query-param-untyped", off, "every value here is untyped; cast one")
	}
	return vs, bare(t), nil
}

// caseExpr types CASE: non-null with an ELSE when every branch is
// (guide §5.4).
func (a *analyzer) caseExpr(c *ast.CaseExpr, sc *scope) (value, error) {
	var subject value
	if c.Value != nil {
		var err error
		if subject, err = a.expr(c.Value, sc); err != nil {
			return value{}, err
		}
	}
	var results []ast.ExprNode
	for _, w := range c.WhenClauses {
		cond, err := a.expr(w.Expr, sc)
		if err != nil {
			return value{}, err
		}
		if c.Value != nil {
			if subject, _, err = a.unify(subject, cond, true); err != nil {
				return value{}, err
			}
		} else if _, err := a.expect(cond, tBool(), true); err != nil {
			return value{}, err
		}
		results = append(results, w.Result)
	}
	if c.ElseClause != nil {
		results = append(results, c.ElseClause)
	}
	vs, t, err := a.common(results, sc, c.OriginTextPosition())
	if err != nil {
		return value{}, err
	}
	nn := c.ElseClause != nil && nonnull(vs...)
	return value{typ: t, nullable: !nn, annotated: nn && anyAnnotated(vs...)}, nil
}

// subquery types a subquery used as a value, which returns one column.
func (a *analyzer) subquery(s *ast.SubqueryExpr, sc *scope, off int) ([]column, error) {
	cols, err := a.resultSet(s.Query, &scope{parent: sc})
	if err != nil {
		return nil, err
	}
	if len(cols) != 1 {
		return nil, a.errAt("query-unsupported", off, "a subquery used as a value returns one column, not %d", len(cols))
	}
	cols[0].v = settle(cols[0].v)
	if cols[0].v.typ == nil {
		return nil, a.errAt("query-param-untyped", off, "the subquery's column is untyped; cast it")
	}
	return cols, nil
}

// compareSubquery types x op ANY (SELECT ...) and x op ALL (SELECT ...).
func (a *analyzer) compareSubquery(e *ast.CompareSubqueryExpr, sc *scope) (value, error) {
	l, err := a.expr(e.L, sc)
	if err != nil {
		return value{}, err
	}
	cols, err := a.subquery(e.R.(*ast.SubqueryExpr), sc, e.OriginTextPosition())
	if err != nil {
		return value{}, err
	}
	if _, _, err := a.unify(l, cols[0].v, true); err != nil {
		return value{}, err
	}
	return value{typ: tBool(), nullable: true}, nil
}
