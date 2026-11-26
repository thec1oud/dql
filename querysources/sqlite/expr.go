package sqlite

import (
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// expr types an expression. A value is non-null only through the proofs of
// the implementation guide §5.4; every other expression is nullable.
func (a *analyzer) expr(n expr, sc *scope) (value, error) {
	switch x := n.(type) {
	case *colRef:
		return a.columnRef(x, sc)
	case *placeholder:
		use := a.r.use(x.off)
		i := a.r.uses[use]
		p := a.params[i]
		return value{typ: p.typ, param: i + 1, use: use, name: p.raw.name, off: x.off}, nil
	case *literal:
		return constant(x), nil
	case *unary:
		return a.unary(x, sc)
	case *binary:
		return a.binary(x, sc)
	case *isExpr:
		if _, _, err := a.operands(x.l, x.r, sc, true); err != nil {
			return value{}, err
		}
		return value{typ: tBool(), off: x.off}, nil
	case *nullTest:
		if _, err := a.expr(x.x, sc); err != nil {
			return value{}, err
		}
		return value{typ: tBool(), off: x.off}, nil
	case *between:
		return a.between(x, sc)
	case *inExpr:
		return a.in(x, sc)
	case *likeExpr:
		return a.like(x, sc)
	case *collate:
		return a.expr(x.x, sc)
	case *call:
		return a.call(x, sc)
	case *cast:
		return a.cast(x, sc)
	case *caseExpr:
		return a.caseExpr(x, sc)
	case *subquery:
		cols, err := a.subquery(x.sel, sc, x.off)
		if err != nil {
			return value{}, err
		}
		// A scalar subquery is NULL when it returns no row.
		return value{typ: cols[0].v.typ, nullable: true, off: x.off}, nil
	case *exists:
		if _, err := a.selectStmt(x.sel, &scope{parent: sc}); err != nil {
			return value{}, err
		}
		return value{typ: tBool(), off: x.off}, nil
	}
	u := n.(*unsupported)
	return value{}, a.errAt("query-unsupported", u.off, "%s", u.msg)
}

// columnRef types a column reference: a column reached only through inner
// joins keeps its nullability; an outer join makes it nullable.
func (a *analyzer) columnRef(ref *colRef, sc *scope) (value, error) {
	if ref.table == "" {
		r, c, found := a.findColumn(ref.name, sc)
		switch {
		case !found:
			return value{}, a.errAt("query-unknown-column", ref.off, "column %s is not a column of the FROM clause", ref.name)
		case r == nil:
			return value{}, a.errAt("query-ambiguous-column", ref.off, "column %s is a column of more than one relation; qualify it", ref.name)
		}
		return at(read(r, c), ref.off), nil
	}
	r, err := a.findRel(ref.schema, ref.table, sc, ref.off)
	if err != nil {
		return value{}, err
	}
	for _, c := range r.cols {
		if strings.EqualFold(c.name, ref.name) {
			return at(read(r, c), ref.off), nil
		}
	}
	return value{}, a.errAt("query-unknown-column", ref.off, "column %s is not a column of %s", ref.name, r.name)
}

func at(v value, off int) value {
	v.off = off
	return v
}

func read(r *rel, c column) value {
	v := c.v
	v.name = c.name
	if r.nullable {
		v.nullable = true
	}
	return v
}

// constant types a literal: an integer is INTEGER and one with a point or
// an exponent REAL, as SQLite reads them; TRUE and FALSE are booleans, a
// blob is bytes, and CURRENT_DATE, CURRENT_TIME and CURRENT_TIMESTAMP are
// the date, time and date and time they write as text. A string's type
// comes from its context.
func constant(l *literal) value {
	v := value{off: l.off}
	switch l.kind {
	case litNull:
		v.nullable = true
	case litInteger:
		v.typ, v.positional, v.position = tInt64(), true, position(l.text)
	case litReal:
		v.typ = tFloat64()
	case litString:
		v.literal = true
	case litBlob:
		v.typ = tBytes()
	case litBool:
		v.typ = tBool()
	case litTime:
		v.typ = tLocalTime()
	case litDate:
		v.typ = tDate()
	case litTimestamp:
		v.typ = tLocalDatetime()
	}
	return v
}

// cast types CAST: the value takes the type of the target's affinity, and
// keeps its nullability, as SQLite's casts never fail.
func (a *analyzer) cast(c *cast, sc *scope) (value, error) {
	t := castType(c.typ)
	if t == nil {
		return value{}, a.errAt("query-unsupported", c.off, "a cast to %s is not supported in query files: its result's storage class depends on the value; cast to INTEGER, REAL, TEXT or BLOB", c.typ)
	}
	v, err := a.expr(c.x, sc)
	if err != nil {
		return value{}, err
	}
	// The cast fixes the type of a parameter it casts, unless a column
	// already has (§17.2).
	if v.param != 0 && !a.params[v.param-1].strong {
		a.give(v, t, true)
	}
	return value{typ: t, nullable: v.nullable, annotated: v.annotated, off: c.off}, nil
}

// expect gives an untyped value the type its context needs: a parameter
// takes it, and a literal or NULL becomes it. A typed value is returned as
// it is. strong says the context fixes the type, as a column does: a
// parameter's type from a weak context, such as a function argument, gives
// way to it, and two different strong types are an error.
func (a *analyzer) expect(v value, t *irv1.Type, strong bool) (value, error) {
	if v.param != 0 {
		if p := a.params[v.param-1]; strong && p.strong && !sameType(p.typ, logical(t)) {
			return value{}, a.errAt("query-param-conflict", v.off, "parameter %s is used as two different types; cast it where it is used, such as CAST(@%s AS INTEGER)", p.raw.name, p.raw.name)
		}
	}
	return a.give(v, t, strong), nil
}

// give is expect for a value that cannot conflict: one from a weak
// context, or an untyped one. A weak column's affinity is what its
// parameters and literals take (§8.10).
func (a *analyzer) give(v value, t *irv1.Type, strong bool) value {
	t = logical(t)
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

// unary types NOT, ~ and unary minus and plus.
func (a *analyzer) unary(e *unary, sc *scope) (value, error) {
	v, err := a.expr(e.x, sc)
	if err != nil {
		return value{}, err
	}
	switch e.op {
	case "NOT":
		v, _ = a.expect(v, tBool(), false)
		return strict(tBool(), v), nil
	case "~":
		v, _ = a.expect(v, tInt64(), false)
		return strict(tInt64(), v), nil
	}
	if rank(v.typ) == 0 {
		return value{}, a.errAt("query-unsupported", e.off, "operator %s is supported only on a number in query files", e.op)
	}
	return strict(bare(logical(v.typ)), v), nil
}

// binary types a binary operator.
func (a *analyzer) binary(e *binary, sc *scope) (value, error) {
	switch e.op {
	case "AND", "OR":
		var vs []value
		for _, n := range []expr{e.l, e.r} {
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
	case "=", "<>", "<", "<=", ">", ">=":
		l, r, err := a.operands(e.l, e.r, sc, true)
		if err != nil {
			return value{}, err
		}
		return strict(tBool(), l, r), nil
	case "&", "|", "<<", ">>", "||":
		t := tInt64()
		if e.op == "||" {
			t = tText()
		}
		var vs []value
		for _, n := range []expr{e.l, e.r} {
			v, err := a.expr(n, sc)
			if err != nil {
				return value{}, err
			}
			vs = append(vs, a.give(v, t, false))
		}
		return strict(t, vs...), nil
	case "->", "->>":
		l, err := a.expr(e.l, sc)
		if err != nil {
			return value{}, err
		}
		r, err := a.expr(e.r, sc)
		if err != nil {
			return value{}, err
		}
		a.give(l, tJSON(), false)
		a.give(r, tText(), false)
		// -> returns JSON; ->> an SQL value, whose storage class only the
		// document decides, so it needs a cast or a typed context.
		if e.op == "->" {
			return value{typ: tJSON(), nullable: true}, nil
		}
		return value{nullable: true, off: e.off}, nil
	}
	l, r, err := a.operands(e.l, e.r, sc, false)
	if err != nil {
		return value{}, err
	}
	if l.typ == nil || r.typ == nil {
		return value{}, a.errAt("query-param-untyped", e.off, "both sides of %s are untyped; cast one, such as CAST(@n AS INTEGER)", e.op)
	}
	t := arithmeticType(l.typ, r.typ)
	if t == nil {
		return value{}, a.errAt("query-unsupported", e.off, "operator %s is supported only on numbers in query files; use the date and time functions for dates and times", e.op)
	}
	v := strict(t, l, r)
	if e.op == "/" || e.op == "%" {
		// Division by zero is NULL.
		v.nullable, v.annotated = true, false
	}
	return v, nil
}

// operands types both sides of a binary operator and unifies their types.
func (a *analyzer) operands(ln, rn expr, sc *scope, strong bool) (value, value, error) {
	l, err := a.expr(ln, sc)
	if err != nil {
		return value{}, value{}, err
	}
	r, err := a.expr(rn, sc)
	if err != nil {
		return value{}, value{}, err
	}
	return a.unify(l, r, strong)
}

// arithmeticType returns the type of arithmetic on l and r, or nil, as
// SQLite computes it: in REAL when either side is REAL, and otherwise in
// INTEGER, or a NUMERIC column's decimal.
func arithmeticType(l, r *irv1.Type) *irv1.Type {
	switch lr, rr := rank(l), rank(r); {
	case lr == 0 || rr == 0:
		return nil
	case max(lr, rr) == 3:
		return tFloat64()
	case max(lr, rr) == 2:
		return tDecimal()
	}
	return tInt64()
}

// in types x IN (a, b, ...), x IN (SELECT ...) and a list parameter, x IN
// (sqlc.slice(name)). The list is one JSON array placeholder, which the
// statement expands with json_each into rows of its elements (§25.3).
func (a *analyzer) in(e *inExpr, sc *scope) (value, error) {
	l, err := a.expr(e.x, sc)
	if err != nil {
		return value{}, err
	}
	if e.sel != nil {
		cols, err := a.subquery(e.sel, sc, e.off)
		if err != nil {
			return value{}, err
		}
		if _, _, err := a.unify(l, cols[0].v, true); err != nil {
			return value{}, err
		}
		return value{typ: tBool(), nullable: true}, nil
	}
	if len(e.list) == 1 {
		if pm, ok := e.list[0].(*placeholder); ok && a.params[a.r.uses[a.r.use(pm.off)]].raw.kind == sliceParam {
			r, _ := a.expr(pm, sc)
			if l.col == nil {
				return value{}, a.errAt("query-list-param", e.off, "the left side of IN (sqlc.slice(%s)) must be a column, whose type the list's elements take", r.name)
			}
			if _, err := a.expect(r, l.typ, true); err != nil {
				return value{}, err
			}
			a.lists[r.use] = true
			return strict(tBool(), l), nil
		}
	}
	vs := []value{l}
	for _, it := range e.list {
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
		return value{}, a.errAt("query-param-untyped", e.off, "the values of IN are untyped; cast one")
	}
	return strict(tBool(), vs...), nil
}

// like types LIKE and GLOB, whose operands are text.
func (a *analyzer) like(e *likeExpr, sc *scope) (value, error) {
	var vs []value
	for _, n := range []expr{e.x, e.pattern, e.escape} {
		if n == nil {
			continue
		}
		v, err := a.expr(n, sc)
		if err != nil {
			return value{}, err
		}
		vs = append(vs, a.give(v, tText(), false))
	}
	return strict(tBool(), vs...), nil
}

// between types x BETWEEN a AND b.
func (a *analyzer) between(e *between, sc *scope) (value, error) {
	l, err := a.expr(e.x, sc)
	if err != nil {
		return value{}, err
	}
	vs := []value{l}
	for _, n := range []expr{e.lo, e.hi} {
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
func (a *analyzer) common(args []expr, sc *scope, off int) ([]value, *irv1.Type, error) {
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
		switch {
		case t == nil:
			t = v.typ
		case sameType(t, v.typ):
		case isText(t) && isText(v.typ):
			t = tText()
		case rank(t) > 0 && rank(v.typ) > 0:
			t = arithmeticType(t, v.typ)
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
func (a *analyzer) caseExpr(c *caseExpr, sc *scope) (value, error) {
	var subject value
	if c.subject != nil {
		var err error
		if subject, err = a.expr(c.subject, sc); err != nil {
			return value{}, err
		}
	}
	for _, w := range c.whens {
		cond, err := a.expr(w, sc)
		if err != nil {
			return value{}, err
		}
		if c.subject != nil {
			if subject, _, err = a.unify(subject, cond, true); err != nil {
				return value{}, err
			}
		} else if _, err := a.expect(cond, tBool(), true); err != nil {
			return value{}, err
		}
	}
	results := c.thens
	if c.els != nil {
		results = append(results, c.els)
	}
	vs, t, err := a.common(results, sc, c.off)
	if err != nil {
		return value{}, err
	}
	nn := c.els != nil && nonnull(vs...)
	return value{typ: t, nullable: !nn, annotated: nn && anyAnnotated(vs...)}, nil
}

// subquery types a subquery used as a value, which returns one column.
func (a *analyzer) subquery(s *selectStmt, sc *scope, off int) ([]column, error) {
	cols, err := a.selectStmt(s, &scope{parent: sc})
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
