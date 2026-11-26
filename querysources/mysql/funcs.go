package mysql

import (
	"slices"

	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/opcode"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// builtin is a built-in function a query file may call.
type builtin struct {
	// The argument types an untyped argument takes; the last repeats.
	// Nil leaves untyped arguments untyped.
	args     []func() *irv1.Type
	min, max int
	// The result type from the arguments, which are typed; nil is an
	// error the caller reports.
	result func(a *analyzer, args []value) *irv1.Type
	// How the result's nullability follows from the arguments'.
	null nullRule
	// For temporalNull, how many leading arguments are dates or times.
	dates int
	// The function reads more than its arguments, such as RAND() or
	// LAST_INSERT_ID(), so the query runs on the primary (§22).
	volatile bool
}

// A nullRule is when a function's result is NULL.
type nullRule int

const (
	// The result may be NULL whatever the arguments.
	maybeNull nullRule = iota
	// NULL only for a NULL argument (guide §5.4).
	strictNull
	// Never NULL, as JSON_OBJECT and NOW().
	neverNull
	// NULL for a NULL argument, or for text that is no date or time: so
	// strict only when the leading dates arguments have a date or time
	// type.
	temporalNull
	// NULL when the first argument is, as CONCAT_WS's separator.
	firstNull
)

func returns(t func() *irv1.Type) func(*analyzer, []value) *irv1.Type {
	return func(*analyzer, []value) *irv1.Type { return t() }
}

// firstArg returns the first argument's type, as MIN and LAG do.
func firstArg(_ *analyzer, args []value) *irv1.Type { return bare(args[0].typ) }

// numericArg returns the first argument's type when it is a number.
func numericArg(a *analyzer, args []value) *irv1.Type {
	if b := a.base(args[0].typ); rank(b) > 0 {
		return bare(b)
	}
	return nil
}

// rounding is the type of ROUND and TRUNCATE: the argument's, in BIGINT for
// an integer.
func rounding(a *analyzer, args []value) *irv1.Type {
	return arithmeticType(opcode.Plus, a.base(args[0].typ), a.base(args[0].typ))
}

// wholeNumber is the type of CEIL and FLOOR on an integer or a float;
// MySQL picks the type of a DECIMAL's by its precision, so that needs a
// cast.
func wholeNumber(a *analyzer, args []value) *irv1.Type {
	b := a.base(args[0].typ)
	if rank(b) == 5 {
		return nil
	}
	return rounding(a, args)
}

// dateShift is the type of DATE_ADD and DATE_SUB: a DATE shifted by days or
// longer stays a DATE; any other date or time is a DATETIME.
func dateShift(a *analyzer, args []value) *irv1.Type {
	switch a.base(args[0].typ).GetKind().(type) {
	case *irv1.Type_Date:
		if wholeDays[a.unit] {
			return tDate()
		}
		return tLocalDatetime()
	case *irv1.Type_LocalDatetime, *irv1.Type_Instant:
		return tLocalDatetime()
	}
	return nil
}

var wholeDays = map[ast.TimeUnitType]bool{ast.TimeUnitDay: true, ast.TimeUnitWeek: true, ast.TimeUnitMonth: true,
	ast.TimeUnitQuarter: true, ast.TimeUnitYear: true, ast.TimeUnitYearMonth: true}

// sumType is SUM's result type: DECIMAL for exact numbers, DOUBLE for the
// others.
func sumType(a *analyzer, args []value) *irv1.Type {
	switch r := rank(a.base(args[0].typ)); {
	case r == 0:
		return nil
	case r >= 6:
		return tFloat64()
	}
	return tDecimal()
}

var (
	text      = []func() *irv1.Type{tText}
	textInt   = []func() *irv1.Type{tText, tInt64}
	temporal  = []func() *irv1.Type{tLocalDatetime}
	jsonPaths = []func() *irv1.Type{tJSON, tText}
)

var builtins = func() map[string]builtin {
	m := map[string]builtin{}
	for _, n := range []string{"lower", "lcase", "upper", "ucase", "ltrim", "rtrim", "reverse", "md5", "sha1", "sha", "hex", "quote", "soundex"} {
		m[n] = builtin{args: text, min: 1, max: 1, result: returns(tText), null: strictNull}
	}
	// TRIM(BOTH 'x' FROM s) takes the text to remove too.
	m["trim"] = builtin{args: text, min: 1, max: 2, result: returns(tText), null: strictNull}
	m["any_value"] = builtin{min: 1, max: 1, result: firstArg, null: strictNull}
	m["concat"] = builtin{args: text, min: 1, max: 100, result: returns(tText), null: strictNull}
	m["concat_ws"] = builtin{args: text, min: 2, max: 100, result: returns(tText), null: firstNull}
	m["replace"] = builtin{args: text, min: 3, max: 3, result: returns(tText), null: strictNull}
	m["regexp_replace"] = builtin{args: []func() *irv1.Type{tText, tText, tText, tInt64, tInt64, tText}, min: 3, max: 6, result: returns(tText), null: strictNull}
	for _, n := range []string{"substring", "substr", "mid"} {
		m[n] = builtin{args: textInt, min: 2, max: 3, result: returns(tText), null: strictNull}
	}
	for _, n := range []string{"left", "right", "repeat"} {
		m[n] = builtin{args: textInt, min: 2, max: 2, result: returns(tText), null: strictNull}
	}
	for _, n := range []string{"lpad", "rpad"} {
		m[n] = builtin{args: []func() *irv1.Type{tText, tInt64, tText}, min: 3, max: 3, result: returns(tText), null: strictNull}
	}
	m["substring_index"] = builtin{args: []func() *irv1.Type{tText, tText, tInt64}, min: 3, max: 3, result: returns(tText), null: strictNull}
	for _, n := range []string{"length", "char_length", "character_length", "octet_length", "bit_length"} {
		m[n] = builtin{args: text, min: 1, max: 1, result: returns(tInt64), null: strictNull}
	}
	m["locate"] = builtin{args: textInt, min: 2, max: 3, result: returns(tInt64), null: strictNull}
	m["instr"] = builtin{args: text, min: 2, max: 2, result: returns(tInt64), null: strictNull}
	m["abs"] = builtin{min: 1, max: 1, result: numericArg, null: strictNull}
	m["sign"] = builtin{min: 1, max: 1, result: returns(tInt64), null: strictNull}
	m["round"] = builtin{args: []func() *irv1.Type{nil, tInt64}, min: 1, max: 2, result: rounding, null: strictNull}
	m["truncate"] = builtin{args: []func() *irv1.Type{nil, tInt64}, min: 2, max: 2, result: rounding, null: strictNull}
	for _, n := range []string{"ceil", "ceiling", "floor"} {
		m[n] = builtin{min: 1, max: 1, result: wholeNumber, null: strictNull}
	}
	for _, n := range []string{"pow", "power"} {
		m[n] = builtin{args: []func() *irv1.Type{tFloat64}, min: 2, max: 2, result: returns(tFloat64), null: strictNull}
	}
	m["exp"] = builtin{args: []func() *irv1.Type{tFloat64}, min: 1, max: 1, result: returns(tFloat64), null: strictNull}
	// NULL outside their domains, such as SQRT(-1).
	for _, n := range []string{"sqrt", "ln", "log", "log2", "log10"} {
		m[n] = builtin{args: []func() *irv1.Type{tFloat64}, min: 1, max: 2, result: returns(tFloat64)}
	}
	m["rand"] = builtin{args: []func() *irv1.Type{tInt64}, max: 1, result: returns(tFloat64), null: neverNull, volatile: true}
	m["uuid"] = builtin{result: returns(tText), null: neverNull, volatile: true}
	m["last_insert_id"] = builtin{result: returns(tUint64), null: neverNull, volatile: true}

	// DATE '2026-01-01', TIME '10:00' and TIMESTAMP '2026-01-01 10:00'.
	m[ast.DateLiteral] = builtin{args: text, min: 1, max: 1, result: returns(tDate), null: neverNull}
	m[ast.TimeLiteral] = builtin{args: text, min: 1, max: 1, result: returns(tInterval), null: neverNull}
	m[ast.TimestampLiteral] = builtin{args: text, min: 1, max: 1, result: returns(tLocalDatetime), null: neverNull}
	for _, n := range []string{"now", "current_timestamp", "localtime", "localtimestamp", "utc_timestamp"} {
		m[n] = builtin{args: []func() *irv1.Type{tInt64}, max: 1, result: returns(tLocalDatetime), null: neverNull}
	}
	m["sysdate"] = builtin{args: []func() *irv1.Type{tInt64}, max: 1, result: returns(tLocalDatetime), null: neverNull, volatile: true}
	for _, n := range []string{"curdate", "current_date", "utc_date"} {
		m[n] = builtin{result: returns(tDate), null: neverNull}
	}
	for _, n := range []string{"curtime", "current_time", "utc_time"} {
		m[n] = builtin{args: []func() *irv1.Type{tInt64}, max: 1, result: returns(tInterval), null: neverNull}
	}
	for _, n := range []string{"date_add", "date_sub", "adddate", "subdate"} {
		m[n] = builtin{args: []func() *irv1.Type{tLocalDatetime, tInt64}, min: 2, max: 2, result: dateShift, null: temporalNull, dates: 1}
	}
	m["date"] = builtin{args: temporal, min: 1, max: 1, result: returns(tDate), null: temporalNull, dates: 1}
	for _, n := range []string{"year", "month", "day", "dayofmonth", "dayofweek", "dayofyear", "hour", "minute", "second", "quarter", "week", "weekday", "extract"} {
		m[n] = builtin{args: temporal, min: 1, max: 2, result: returns(tInt64), null: temporalNull, dates: 1}
	}
	m["datediff"] = builtin{args: temporal, min: 2, max: 2, result: returns(tInt64), null: temporalNull, dates: 2}
	m["timestampdiff"] = builtin{args: temporal, min: 2, max: 2, result: returns(tInt64), null: temporalNull, dates: 2}
	m["date_format"] = builtin{args: []func() *irv1.Type{tLocalDatetime, tText}, min: 2, max: 2, result: returns(tText), null: temporalNull, dates: 1}

	m["json_extract"] = builtin{args: jsonPaths, min: 2, max: 100, result: returns(tJSON)}
	m["json_unquote"] = builtin{args: text, min: 1, max: 1, result: returns(tText), null: strictNull}
	for _, n := range []string{"json_object", "json_array"} {
		m[n] = builtin{max: 100, result: returns(tJSON), null: neverNull}
	}
	for _, n := range []string{"json_set", "json_insert", "json_replace", "json_remove", "json_merge_patch", "json_merge_preserve"} {
		m[n] = builtin{args: jsonPaths, min: 2, max: 100, result: returns(tJSON), null: strictNull}
	}
	m["json_length"] = builtin{args: jsonPaths, min: 1, max: 2, result: returns(tInt64)}
	m["json_contains"] = builtin{args: []func() *irv1.Type{tJSON, tJSON, tText}, min: 2, max: 3, result: returns(tBool)}
	m["json_valid"] = builtin{min: 1, max: 1, result: returns(tBool), null: strictNull}
	m["json_type"] = builtin{args: []func() *irv1.Type{tJSON}, min: 1, max: 1, result: returns(tText), null: strictNull}
	m["json_keys"] = builtin{args: jsonPaths, min: 1, max: 2, result: returns(tJSON)}
	m["json_quote"] = builtin{args: text, min: 1, max: 1, result: returns(tJSON), null: strictNull}
	return m
}()

// aggregates are the aggregate functions, which are NULL over no rows,
// except COUNT.
var aggregates = map[string]builtin{
	"count":          {max: 100, result: returns(tInt64), null: neverNull},
	"min":            {min: 1, max: 1, result: firstArg},
	"max":            {min: 1, max: 1, result: firstArg},
	"sum":            {min: 1, max: 1, result: sumType},
	"avg":            {min: 1, max: 1, result: sumType},
	"group_concat":   {args: text, min: 1, max: 100, result: returns(tText)},
	"json_arrayagg":  {min: 1, max: 1, result: returns(tJSON)},
	"json_objectagg": {args: []func() *irv1.Type{tText}, min: 2, max: 2, result: returns(tJSON)},
	"std":            {min: 1, max: 1, result: returns(tFloat64)},
	"stddev":         {min: 1, max: 1, result: returns(tFloat64)},
	"stddev_pop":     {min: 1, max: 1, result: returns(tFloat64)},
	"stddev_samp":    {min: 1, max: 1, result: returns(tFloat64)},
	"variance":       {min: 1, max: 1, result: returns(tFloat64)},
	"var_pop":        {min: 1, max: 1, result: returns(tFloat64)},
	"var_samp":       {min: 1, max: 1, result: returns(tFloat64)},
}

// windows are the window functions; an aggregate is one too with OVER.
var windows = map[string]builtin{
	"row_number":   {result: returns(tInt64), null: neverNull},
	"rank":         {result: returns(tInt64), null: neverNull},
	"dense_rank":   {result: returns(tInt64), null: neverNull},
	"percent_rank": {result: returns(tFloat64), null: neverNull},
	"cume_dist":    {result: returns(tFloat64), null: neverNull},
	"ntile":        {args: []func() *irv1.Type{tInt64}, min: 1, max: 1, result: returns(tInt64), null: neverNull},
	"lag":          {args: []func() *irv1.Type{nil, tInt64, nil}, min: 1, max: 3, result: firstArg},
	"lead":         {args: []func() *irv1.Type{nil, tInt64, nil}, min: 1, max: 3, result: firstArg},
	"first_value":  {min: 1, max: 1, result: firstArg},
	"last_value":   {min: 1, max: 1, result: firstArg},
	"nth_value":    {args: []func() *irv1.Type{nil, tInt64}, min: 2, max: 2, result: firstArg},
}

// call types a function call: a built-in, a special form such as COALESCE,
// or a routine of the catalog.
func (a *analyzer) call(f *ast.FuncCallExpr, sc *scope) (value, error) {
	name, off := f.FnName.L, f.OriginTextPosition()
	if f.Schema.O != "" {
		return value{}, a.errAt("query-unsupported", off, "%s.%s names a database; call functions without it, as the connection picks the database", f.Schema.O, f.FnName.O)
	}
	switch name {
	case "coalesce", "ifnull":
		vs, t, err := a.common(f.Args, sc, off)
		if err != nil {
			return value{}, err
		}
		v := value{typ: t, nullable: true}
		for _, x := range vs {
			if !x.nullable {
				v.nullable, v.annotated = false, x.annotated
				break
			}
		}
		return v, nil
	case "nullif":
		vs, t, err := a.common(f.Args, sc, off)
		if err != nil {
			return value{}, err
		}
		return value{typ: bare(vs[0].typ), nullable: t != nil}, nil
	case "if":
		cond, err := a.expr(f.Args[0], sc)
		if err != nil {
			return value{}, err
		}
		if _, err := a.expect(cond, tBool(), true); err != nil {
			return value{}, err
		}
		vs, t, err := a.common(f.Args[1:], sc, off)
		if err != nil {
			return value{}, err
		}
		return strict(t, vs...), nil
	case "greatest", "least":
		// Unlike Postgres's, MySQL's are NULL when any argument is.
		vs, t, err := a.common(f.Args, sc, off)
		if err != nil {
			return value{}, err
		}
		return strict(t, vs...), nil
	}
	if b, ok := builtins[name]; ok {
		v, _, err := a.callBuiltin(name, b, f.Args, sc, off)
		return v, err
	}
	return a.callRoutine(f, sc)
}

// args types a call's arguments. A time unit, as in DATE_ADD(d, INTERVAL 1
// DAY), or a TRIM direction is no value; the last unit is kept in a.unit.
func (a *analyzer) args(list []ast.ExprNode, sc *scope) ([]value, error) {
	var vs []value
	for _, n := range list {
		switch x := n.(type) {
		case *ast.TimeUnitExpr:
			a.unit = x.Unit
			continue
		case *ast.TrimDirectionExpr:
			continue
		}
		v, err := a.expr(n, sc)
		if err != nil {
			return nil, err
		}
		vs = append(vs, v)
	}
	return vs, nil
}

// callBuiltin types a call of a built-in function, an aggregate or a window
// function.
func (a *analyzer) callBuiltin(name string, b builtin, list []ast.ExprNode, sc *scope, off int) (value, []value, error) {
	a.unit = ast.TimeUnitDay
	vs, err := a.args(list, sc)
	if err != nil {
		return value{}, nil, err
	}
	if n := len(vs); n < b.min || n > b.max {
		return value{}, nil, a.errAt("query-unknown-function", off, "%s is not called with %d arguments in query files", name, n)
	}
	for i, v := range vs {
		if len(b.args) > 0 {
			if t := b.args[min(i, len(b.args)-1)]; t != nil {
				v = a.give(v, t(), false)
			}
		}
		v = settle(v)
		if v.typ == nil {
			return value{}, nil, a.errAt("query-param-untyped", off, "argument %d of %s is untyped; cast it", i+1, name)
		}
		vs[i] = v
	}
	t := b.result(a, vs)
	if t == nil {
		return value{}, nil, a.errAt("query-unsupported", off, "%s is not supported on this argument type in query files; cast its argument", name)
	}
	if b.volatile {
		a.writes = true
	}
	v := value{typ: t, nullable: true}
	switch b.null {
	case neverNull:
		v.nullable = false
	case strictNull:
		v = strict(t, vs...)
	case firstNull:
		v = strict(t, vs[0])
	case temporalNull:
		if !slices.ContainsFunc(vs[:b.dates], func(x value) bool { return !isTemporal(a.base(x.typ)) }) {
			v = strict(t, vs...)
		}
	}
	return v, vs, nil
}

func isTemporal(t *irv1.Type) bool {
	switch t.GetKind().(type) {
	case *irv1.Type_Date, *irv1.Type_LocalDatetime, *irv1.Type_Instant:
		return true
	}
	return false
}

// aggregate types an aggregate function. MIN, MAX and SUM are non-null
// over a non-empty group of non-null inputs (guide §5.4); without GROUP BY
// the input may be empty.
func (a *analyzer) aggregate(f *ast.AggregateFuncExpr, sc *scope) (value, error) {
	name, off := f.F, f.OriginTextPosition()
	b, ok := aggregates[name]
	if !ok {
		return value{}, a.errAt("query-unknown-function", off, "aggregate %s is not supported in query files", name)
	}
	if f.Order != nil {
		if err := a.byItems(f.Order.Items, sc); err != nil {
			return value{}, err
		}
	}
	args := f.Args
	if name == "group_concat" {
		// The parser gives the separator as the last argument.
		args = args[:len(args)-1]
	}
	v, vs, err := a.callBuiltin(name, b, args, sc, off)
	if err != nil {
		return value{}, err
	}
	if sc.grouped && (name == "min" || name == "max" || name == "sum") && nonnull(vs...) {
		v.nullable, v.annotated = false, anyAnnotated(vs...)
	}
	return v, nil
}

// window types a window function, with its PARTITION BY and ORDER BY.
func (a *analyzer) window(f *ast.WindowFuncExpr, sc *scope) (value, error) {
	off := f.OriginTextPosition()
	if f.Spec.Name.O != "" || f.Spec.Ref.O != "" {
		return value{}, a.errAt("query-unsupported", off, "named windows are not supported in query files")
	}
	if f.Spec.PartitionBy != nil {
		if err := a.byItems(f.Spec.PartitionBy.Items, sc); err != nil {
			return value{}, err
		}
	}
	if f.Spec.OrderBy != nil {
		if err := a.byItems(f.Spec.OrderBy.Items, sc); err != nil {
			return value{}, err
		}
	}
	b, ok := windows[f.Name]
	if !ok {
		if b, ok = aggregates[f.Name]; !ok {
			return value{}, a.errAt("query-unknown-function", off, "window function %s is not supported in query files", f.Name)
		}
	}
	v, _, err := a.callBuiltin(f.Name, b, f.Args, sc, off)
	return v, err
}

// callRoutine types a call of a function of the catalog. Its result is
// nullable unless the catalog says otherwise (§6.5).
func (a *analyzer) callRoutine(f *ast.FuncCallExpr, sc *scope) (value, error) {
	name, off := f.FnName.L, f.OriginTextPosition()
	rs := a.ix.routines(name)
	if len(rs) != 1 {
		return value{}, a.errAt("query-unknown-function", off, "function %s is not a built-in function query files support, nor one routine of the catalog", f.FnName.O)
	}
	vs, err := a.args(f.Args, sc)
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
		return value{}, a.errAt("query-unknown-function", off, "function %s does not take these %d arguments", f.FnName.O, len(vs))
	}
	o := match[0]
	a.catalogs[rs[0].catalog] = true
	for i, v := range vs {
		if _, err := a.expect(v, o.GetArguments()[i].GetType(), true); err != nil {
			return value{}, err
		}
	}
	ret := o.GetReturns().GetScalar()
	if o.GetVolatility() == irv1.Volatility_VOLATILITY_VOLATILE {
		a.writes = true
	}
	return value{typ: ret.GetType(), nullable: ret.GetNullable(), annotated: !ret.GetNullable()}, nil
}

// fits reports whether o takes args: their number, and the type of each
// typed argument, which may be a narrower number, as MySQL converts.
func fits(o *irv1.Overload, args []value) bool {
	ins := o.GetArguments()
	if len(args) != len(ins) {
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
