package postgres

import (
	pg "github.com/pganalyze/pg_query_go/v6"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

type funcKind int

const (
	scalarFunc funcKind = iota
	aggregateFunc
	windowFunc
)

// builtin is a pg_catalog function a query file may call.
type builtin struct {
	kind funcKind
	// The argument types an untyped argument takes; the last repeats.
	// Nil leaves untyped arguments untyped.
	args     []func() *irv1.Type
	min, max int
	// The result type from the arguments, which are typed; nil is an
	// error the caller reports.
	result func(a *analyzer, args []value) *irv1.Type
	// The function is strict and never returns NULL for non-null
	// arguments, so its result is non-null when they are (guide §5.4).
	proves   bool
	volatile bool
}

func returns(t func() *irv1.Type) func(*analyzer, []value) *irv1.Type {
	return func(*analyzer, []value) *irv1.Type { return t() }
}

// firstArg returns the first argument's type, as abs and min do.
func firstArg(a *analyzer, args []value) *irv1.Type { return bare(args[0].typ) }

// numericArg returns the first argument's type when it is a number.
func numericArg(a *analyzer, args []value) *irv1.Type {
	if b := a.ix.base(args[0].typ); rank(b) > 0 {
		return bare(b)
	}
	return nil
}

// secondArg returns the second argument's type, as date_trunc does.
func secondArg(a *analyzer, args []value) *irv1.Type { return bare(args[1].typ) }

func text() []func() *irv1.Type { return []func() *irv1.Type{tText} }

var builtins = func() map[string]builtin {
	m := map[string]builtin{}
	for _, n := range []string{"lower", "upper", "btrim", "ltrim", "rtrim", "initcap", "md5", "reverse"} {
		m[n] = builtin{args: text(), min: 1, max: 2, result: returns(tText), proves: true}
	}
	for _, n := range []string{"replace", "translate"} {
		m[n] = builtin{args: text(), min: 3, max: 3, result: returns(tText), proves: true}
	}
	// SIMILAR TO's pattern, as the parser writes it.
	m["similar_to_escape"] = builtin{args: text(), min: 1, max: 2, result: returns(tText), proves: true}
	m["regexp_replace"] = builtin{args: text(), min: 3, max: 4, result: returns(tText), proves: true}
	for _, n := range []string{"substr", "substring"} {
		m[n] = builtin{args: []func() *irv1.Type{tText, tInt32}, min: 2, max: 3, result: returns(tText), proves: true}
	}
	for _, n := range []string{"left", "right", "repeat"} {
		m[n] = builtin{args: []func() *irv1.Type{tText, tInt32}, min: 2, max: 2, result: returns(tText), proves: true}
	}
	for _, n := range []string{"lpad", "rpad"} {
		m[n] = builtin{args: []func() *irv1.Type{tText, tInt32, tText}, min: 2, max: 3, result: returns(tText), proves: true}
	}
	m["split_part"] = builtin{args: []func() *irv1.Type{tText, tText, tInt32}, min: 3, max: 3, result: returns(tText), proves: true}
	for _, n := range []string{"length", "char_length", "character_length", "octet_length"} {
		m[n] = builtin{args: text(), min: 1, max: 1, result: returns(tInt32), proves: true}
	}
	m["strpos"] = builtin{args: text(), min: 2, max: 2, result: returns(tInt32), proves: true}
	for _, n := range []string{"abs", "ceil", "ceiling", "floor", "sign"} {
		m[n] = builtin{min: 1, max: 1, result: numericArg, proves: true}
	}
	for _, n := range []string{"round", "trunc"} {
		m[n] = builtin{args: []func() *irv1.Type{tDecimal, tInt32}, min: 1, max: 2, result: func(a *analyzer, args []value) *irv1.Type {
			if len(args) == 2 {
				return tDecimal()
			}
			return numericArg(a, args)
		}, proves: true}
	}
	for _, n := range []string{"now", "transaction_timestamp", "statement_timestamp"} {
		m[n] = builtin{result: returns(tInstant), proves: true}
	}
	m["clock_timestamp"] = builtin{result: returns(tInstant), proves: true, volatile: true}
	m["date_trunc"] = builtin{args: text(), min: 2, max: 3, result: secondArg, proves: true}
	m["date_part"] = builtin{args: text(), min: 2, max: 2, result: returns(tFloat64), proves: true}
	m["extract"] = builtin{args: text(), min: 2, max: 2, result: returns(tDecimal), proves: true}
	m["age"] = builtin{min: 1, max: 2, result: returns(tInterval), proves: true}
	m["to_char"] = builtin{min: 2, max: 2, args: []func() *irv1.Type{tText}, result: returns(tText), proves: true}
	m["gen_random_uuid"] = builtin{result: returns(tUUID), proves: true, volatile: true}
	m["random"] = builtin{result: returns(tFloat64), proves: true, volatile: true}
	for _, n := range []string{"nextval", "currval"} {
		m[n] = builtin{args: text(), min: 1, max: 1, result: returns(tInt64), proves: true, volatile: true}
	}
	m["setval"] = builtin{args: []func() *irv1.Type{tText, tInt64, tBool}, min: 2, max: 3, result: returns(tInt64), proves: true, volatile: true}
	m["lastval"] = builtin{result: returns(tInt64), proves: true, volatile: true}
	m["cardinality"] = builtin{min: 1, max: 1, result: returns(tInt32), proves: true}
	// NULL for an empty array.
	m["array_length"] = builtin{args: []func() *irv1.Type{nil, tInt32}, min: 2, max: 2, result: returns(tInt32)}
	m["to_jsonb"] = builtin{min: 1, max: 1, result: returns(tJSONB), proves: true}
	m["to_json"] = builtin{min: 1, max: 1, result: returns(tJSON), proves: true}
	for n, t := range map[string]func() *irv1.Type{"jsonb_build_object": tJSONB, "jsonb_build_array": tJSONB, "json_build_object": tJSON, "json_build_array": tJSON} {
		m[n] = builtin{max: 100, result: returns(t)}
	}

	m["count"] = builtin{kind: aggregateFunc, max: 1, result: returns(tInt64)}
	for _, n := range []string{"min", "max"} {
		m[n] = builtin{kind: aggregateFunc, min: 1, max: 1, result: firstArg}
	}
	m["sum"] = builtin{kind: aggregateFunc, min: 1, max: 1, result: sumType}
	m["avg"] = builtin{kind: aggregateFunc, min: 1, max: 1, result: avgType}
	for _, n := range []string{"bool_and", "bool_or", "every"} {
		m[n] = builtin{kind: aggregateFunc, args: []func() *irv1.Type{tBool}, min: 1, max: 1, result: returns(tBool)}
	}
	m["array_agg"] = builtin{kind: aggregateFunc, min: 1, max: 1, result: func(_ *analyzer, args []value) *irv1.Type {
		return tArray(args[0].typ, args[0].nullable)
	}}
	m["string_agg"] = builtin{kind: aggregateFunc, args: text(), min: 2, max: 2, result: returns(tText)}
	m["json_agg"] = builtin{kind: aggregateFunc, min: 1, max: 1, result: returns(tJSON)}
	m["jsonb_agg"] = builtin{kind: aggregateFunc, min: 1, max: 1, result: returns(tJSONB)}

	for _, n := range []string{"row_number", "rank", "dense_rank"} {
		m[n] = builtin{kind: windowFunc, result: returns(tInt64), proves: true}
	}
	for _, n := range []string{"percent_rank", "cume_dist"} {
		m[n] = builtin{kind: windowFunc, result: returns(tFloat64), proves: true}
	}
	m["ntile"] = builtin{kind: windowFunc, args: []func() *irv1.Type{tInt32}, min: 1, max: 1, result: returns(tInt32), proves: true}
	for _, n := range []string{"lag", "lead"} {
		m[n] = builtin{kind: windowFunc, args: []func() *irv1.Type{nil, tInt32}, min: 1, max: 3, result: firstArg}
	}
	for _, n := range []string{"first_value", "last_value"} {
		m[n] = builtin{kind: windowFunc, min: 1, max: 1, result: firstArg}
	}
	m["nth_value"] = builtin{kind: windowFunc, args: []func() *irv1.Type{nil, tInt32}, min: 2, max: 2, result: firstArg}
	return m
}()

// sumType is sum's result type, as Postgres widens it.
func sumType(a *analyzer, args []value) *irv1.Type {
	b := a.ix.base(args[0].typ)
	switch b.GetKind().(type) {
	case *irv1.Type_Int16, *irv1.Type_Int32:
		return tInt64()
	case *irv1.Type_Int64, *irv1.Type_Decimal:
		return tDecimal()
	case *irv1.Type_Float32, *irv1.Type_Float64, *irv1.Type_Interval, *irv1.Type_Money:
		return bare(b)
	}
	return nil
}

// avgType is avg's result type.
func avgType(a *analyzer, args []value) *irv1.Type {
	b := a.ix.base(args[0].typ)
	switch b.GetKind().(type) {
	case *irv1.Type_Int16, *irv1.Type_Int32, *irv1.Type_Int64, *irv1.Type_Decimal:
		return tDecimal()
	case *irv1.Type_Float32, *irv1.Type_Float64:
		return tFloat64()
	case *irv1.Type_Interval:
		return tInterval()
	}
	return nil
}

// call types a function call: a built-in of pg_catalog, or a routine of
// the catalog.
func (a *analyzer) call(f *pg.FuncCall, sc *scope) (value, error) {
	names, _ := nameList(f.GetFuncname())
	name := names[len(names)-1]
	if f.GetFuncVariadic() || f.GetAggWithinGroup() {
		return value{}, a.errAt("query-unsupported", f.GetLocation(), "VARIADIC and WITHIN GROUP are not supported in query files")
	}
	if b, ok := builtins[name]; ok && (len(names) == 1 || names[0] == "pg_catalog") {
		return a.callBuiltin(f, name, b, sc)
	}
	if len(names) > 2 || names[0] == "pg_catalog" {
		return value{}, a.errAt("query-unknown-function", f.GetLocation(), "function %s is not supported in query files", joinDots(names))
	}
	namespace := ""
	if len(names) == 2 {
		namespace = names[0]
	}
	return a.callRoutine(f, namespace, name, sc)
}

// args types a call's arguments.
func (a *analyzer) args(list []*pg.Node, sc *scope) ([]value, error) {
	vs := make([]value, len(list))
	for i, n := range list {
		v, err := a.expr(n, sc)
		if err != nil {
			return nil, err
		}
		vs[i] = v
	}
	return vs, nil
}

// callBuiltin types a call of a built-in function.
func (a *analyzer) callBuiltin(f *pg.FuncCall, name string, b builtin, sc *scope) (value, error) {
	if err := a.callClauses(f, b.kind, sc); err != nil {
		return value{}, err
	}
	vs, err := a.args(f.GetArgs(), sc)
	if err != nil {
		return value{}, err
	}
	if n := len(vs); n < b.min || n > b.max || f.GetAggStar() && name != "count" {
		return value{}, a.errAt("query-unknown-function", f.GetLocation(), "%s is not called with %d arguments in query files", name, n)
	}
	for i, v := range vs {
		if len(b.args) > 0 {
			t := b.args[min(i, len(b.args)-1)]
			if t != nil {
				v = a.give(v, t(), false)
			}
		}
		v = settle(v)
		if v.typ == nil {
			return value{}, a.errAt("query-param-untyped", f.GetLocation(), "argument %d of %s is untyped; cast it", i+1, name)
		}
		vs[i] = v
	}
	t := b.result(a, vs)
	if t == nil {
		return value{}, a.errAt("query-unsupported", f.GetLocation(), "%s is not supported on this argument type in query files", name)
	}
	if b.volatile {
		a.writes = true
	}
	v := value{typ: t, nullable: true, name: name}
	switch {
	case name == "count":
		v.nullable = false
	case b.kind == aggregateFunc && (name == "min" || name == "max" || name == "sum"):
		// Over a non-empty group of non-null inputs (guide §5.4). Without
		// GROUP BY, or in a window or with FILTER, the input may be empty.
		if sc.grouped && f.GetOver() == nil && f.GetAggFilter() == nil && nonnull(vs...) {
			v.nullable, v.annotated = false, anyAnnotated(vs...)
		}
	case b.proves:
		v = strict(t, vs...)
		v.name = name
	}
	return v, nil
}

// callClauses types an aggregate's ORDER BY and FILTER, and a window's
// PARTITION BY and ORDER BY. A window function needs OVER.
func (a *analyzer) callClauses(f *pg.FuncCall, kind funcKind, sc *scope) error {
	if kind == windowFunc && f.GetOver() == nil {
		return a.errAt("query-unsupported", f.GetLocation(), "window function %s needs OVER", nameOf(f))
	}
	if kind == scalarFunc && (f.GetOver() != nil || f.GetAggFilter() != nil || len(f.GetAggOrder()) > 0 || f.GetAggDistinct()) {
		return a.errAt("query-unsupported", f.GetLocation(), "%s is not an aggregate or window function", nameOf(f))
	}
	var exprs []*pg.Node
	for _, n := range f.GetAggOrder() {
		exprs = append(exprs, n.GetSortBy().GetNode())
	}
	if w := f.GetOver(); w != nil {
		if w.GetRefname() != "" || w.GetName() != "" || w.GetStartOffset() != nil || w.GetEndOffset() != nil {
			return a.errAt("query-unsupported", f.GetLocation(), "named windows and frame offsets are not supported in query files")
		}
		exprs = append(exprs, w.GetPartitionClause()...)
		for _, n := range w.GetOrderClause() {
			exprs = append(exprs, n.GetSortBy().GetNode())
		}
	}
	for _, n := range exprs {
		if _, err := a.expr(n, sc); err != nil {
			return err
		}
	}
	return a.condition(f.GetAggFilter(), sc)
}

func nameOf(f *pg.FuncCall) string {
	names, _ := nameList(f.GetFuncname())
	return joinDots(names)
}

// callRoutine types a call of a function of the catalog. Its result is
// nullable unless the catalog says otherwise; STRICT proves nothing (§6.5).
func (a *analyzer) callRoutine(f *pg.FuncCall, namespace, name string, sc *scope) (value, error) {
	full := name
	if namespace != "" {
		full = namespace + "." + name
	}
	rs := a.ix.routines(namespace, name)
	if len(rs) != 1 {
		return value{}, a.errAt("query-unknown-function", f.GetLocation(), "function %s is not a built-in function query files support, nor one routine of the catalog", full)
	}
	if err := a.callClauses(f, scalarFunc, sc); err != nil {
		return value{}, err
	}
	vs, err := a.args(f.GetArgs(), sc)
	if err != nil {
		return value{}, err
	}
	var match []*irv1.Overload
	for _, o := range rs[0].obj.GetOverloads() {
		if o.GetKind() == irv1.RoutineKind_ROUTINE_KIND_FUNCTION && fits(o, vs) {
			match = append(match, o)
		}
	}
	if len(match) != 1 {
		return value{}, a.errAt("query-unknown-function", f.GetLocation(), "%d overloads of %s take these %d arguments; cast the arguments to pick one", len(match), full, len(vs))
	}
	o := match[0]
	a.catalogs[rs[0].catalog] = true
	ins := inputs(o)
	for i, v := range vs {
		if _, err := a.expect(v, ins[i].GetType(), true); err != nil {
			return value{}, err
		}
	}
	ret := o.GetReturns().GetScalar()
	if ret == nil {
		return value{}, a.errAt("query-unsupported", f.GetLocation(), "function %s does not return one value; set-returning functions are not supported in query files", full)
	}
	if o.GetVolatility() == irv1.Volatility_VOLATILITY_VOLATILE {
		a.writes = true
	}
	return value{typ: ret.GetType(), nullable: ret.GetNullable(), annotated: !ret.GetNullable(), name: name}, nil
}

// inputs returns an overload's input arguments.
func inputs(o *irv1.Overload) []*irv1.Argument {
	var out []*irv1.Argument
	for _, arg := range o.GetArguments() {
		if arg.GetMode() != irv1.ArgMode_ARG_MODE_OUT {
			out = append(out, arg)
		}
	}
	return out
}

// fits reports whether o takes args: their number, with defaults, and the
// type of each typed argument, which may be a narrower number, as Postgres
// casts int4 to int8 implicitly.
func fits(o *irv1.Overload, args []value) bool {
	ins := inputs(o)
	required := 0
	for _, arg := range ins {
		if !arg.GetHasDefault() {
			required++
		}
	}
	if len(args) < required || len(args) > len(ins) {
		return false
	}
	for i, v := range args {
		t := ins[i].GetType()
		if v.typ == nil || v.literal || v.param != 0 || sameType(v.typ, t) {
			continue
		}
		if r := rank(v.typ); r == 0 || r > rank(t) {
			return false
		}
	}
	return true
}
