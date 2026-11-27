package sqlserver

import (
	"slices"

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
	result func(args []value) *irv1.Type
	// How the result's nullability follows from the arguments'.
	null nullRule
	// The function reads more than its arguments, such as NEWID() or
	// SCOPE_IDENTITY(), so the query runs on the primary (§22).
	volatile bool
	// The function takes a date part first, such as DATEADD(day, ...).
	part bool
}

// A nullRule is when a function's result is NULL.
type nullRule int

const (
	// The result may be NULL whatever the arguments.
	maybeNull nullRule = iota
	// NULL only for a NULL argument (guide §5.4).
	strictNull
	// Never NULL, as COUNT() and GETDATE().
	neverNull
	// NULL when the first argument is, as JSON_MODIFY's document.
	firstNull
)

func returns(t func() *irv1.Type) func([]value) *irv1.Type {
	return func([]value) *irv1.Type { return t() }
}

// firstArg returns the first argument's type, as MIN() and DATEADD() do.
func firstArg(args []value) *irv1.Type { return bare(args[0].typ) }

// lastArg returns the last argument's type: DATEADD's date.
func lastArg(args []value) *irv1.Type { return bare(args[len(args)-1].typ) }

// numericArg returns the first argument's type when it is a number:
// ROUND(), CEILING() and SIGN() keep the type, but an integer narrower
// than int widens to int, as ABS() does.
func numericArg(args []value) *irv1.Type {
	switch r := rank(args[0].typ); {
	case r == 0:
		return nil
	case r < 4:
		return tInt32()
	}
	return bare(args[0].typ)
}

// sumType is SUM()'s and AVG()'s result type: int for the integers
// narrower than bigint, bigint, decimal, and float for real and float.
func sumType(args []value) *irv1.Type {
	switch r := rank(args[0].typ); {
	case r == 0:
		return nil
	case r < 5:
		return tInt32()
	case r == 5:
		return tInt64()
	case r == rankDecimal:
		return tDecimal()
	}
	return tFloat64()
}

// lengthType is the result of LEN(), CHARINDEX() and PATINDEX(): bigint
// for a (max) argument, int otherwise.
func lengthType(args []value) *irv1.Type {
	if slices.ContainsFunc(args, func(v value) bool { return !v.soft && isMax(v.typ) }) {
		return tInt64()
	}
	return tInt32()
}

// textArg is the text a string function returns: text, unless it is
// given none, as REPLICATE of a number.
func textArg(args []value) *irv1.Type {
	if !isText(args[0].typ) {
		return nil
	}
	return tText()
}

var (
	text    = []func() *irv1.Type{tText}
	textInt = []func() *irv1.Type{tText, tInt32}
	ints    = []func() *irv1.Type{tInt32}
	floats  = []func() *irv1.Type{tFloat64}
	// A date function's arguments after its date part.
	dateInt = []func() *irv1.Type{tInt32, func() *irv1.Type { return tLocalDatetime(7) }}
	dates   = []func() *irv1.Type{func() *irv1.Type { return tLocalDatetime(7) }}
)

var builtins = func() map[string]builtin {
	m := map[string]builtin{}
	for _, n := range []string{"lower", "upper", "ltrim", "rtrim", "reverse", "soundex"} {
		m[n] = builtin{args: text, min: 1, max: 1, result: returns(tText), null: strictNull}
	}
	m["trim"] = builtin{args: text, min: 1, max: 2, result: returns(tText), null: strictNull}
	for _, n := range []string{"replace", "translate"} {
		m[n] = builtin{args: text, min: 3, max: 3, result: returns(tText), null: strictNull}
	}
	m["substring"] = builtin{args: textInt, min: 3, max: 3, result: returns(tText), null: strictNull}
	for _, n := range []string{"left", "right"} {
		m[n] = builtin{args: textInt, min: 2, max: 2, result: returns(tText), null: strictNull}
	}
	m["replicate"] = builtin{args: textInt, min: 2, max: 2, result: textArg, null: strictNull}
	m["stuff"] = builtin{args: []func() *irv1.Type{tText, tInt32, tInt32, tText}, min: 4, max: 4, result: returns(tText)}
	m["concat"] = builtin{args: text, min: 2, max: 254, result: returns(tText), null: neverNull}
	m["concat_ws"] = builtin{args: text, min: 3, max: 254, result: returns(tText), null: neverNull}
	m["format"] = builtin{args: []func() *irv1.Type{nil, tText}, min: 2, max: 3, result: returns(tText)}
	m["quotename"] = builtin{args: text, min: 1, max: 2, result: returns(tText)}
	m["space"] = builtin{args: ints, min: 1, max: 1, result: returns(tText), null: strictNull}
	for _, n := range []string{"char", "nchar"} {
		m[n] = builtin{args: ints, min: 1, max: 1, result: returns(tText)}
	}
	for _, n := range []string{"ascii", "unicode"} {
		m[n] = builtin{args: text, min: 1, max: 1, result: returns(tInt32), null: strictNull}
	}
	m["difference"] = builtin{args: text, min: 2, max: 2, result: returns(tInt32), null: strictNull}
	m["len"] = builtin{args: text, min: 1, max: 1, result: lengthType, null: strictNull}
	m["datalength"] = builtin{min: 1, max: 1, result: lengthType, null: strictNull}
	m["charindex"] = builtin{args: []func() *irv1.Type{tText, tText, tInt64}, min: 2, max: 3, result: lengthType, null: strictNull}
	m["patindex"] = builtin{args: text, min: 2, max: 2, result: lengthType, null: strictNull}

	m["abs"] = builtin{min: 1, max: 1, result: numericArg, null: strictNull}
	for _, n := range []string{"ceiling", "floor", "sign"} {
		m[n] = builtin{min: 1, max: 1, result: numericArg, null: strictNull}
	}
	m["round"] = builtin{args: []func() *irv1.Type{tFloat64, tInt32, tInt32}, min: 2, max: 3, result: numericArg, null: strictNull}
	m["power"] = builtin{args: floats, min: 2, max: 2, result: numericArg, null: strictNull}
	for _, n := range []string{"degrees", "radians"} {
		m[n] = builtin{args: floats, min: 1, max: 1, result: numericArg, null: strictNull}
	}
	// Outside their domains, such as SQRT(-1), they fail rather than give
	// NULL.
	for _, n := range []string{"sqrt", "square", "exp", "log10", "sin", "cos", "tan", "cot", "asin", "acos", "atan"} {
		m[n] = builtin{args: floats, min: 1, max: 1, result: returns(tFloat64), null: strictNull}
	}
	m["log"] = builtin{args: floats, min: 1, max: 2, result: returns(tFloat64), null: strictNull}
	m["atn2"] = builtin{args: floats, min: 2, max: 2, result: returns(tFloat64), null: strictNull}
	m["pi"] = builtin{result: returns(tFloat64), null: neverNull}
	m["rand"] = builtin{args: ints, max: 1, result: returns(tFloat64), null: neverNull, volatile: true}
	m["newid"] = builtin{result: returns(tUUID), null: neverNull, volatile: true}
	m["scope_identity"] = builtin{result: returns(tDecimal), volatile: true}
	for _, n := range []string{"checksum", "binary_checksum"} {
		m[n] = builtin{max: 100, result: returns(tInt32), null: neverNull}
	}

	// The current time.
	for _, n := range []string{"getdate", "getutcdate", "current_timestamp"} {
		m[n] = builtin{result: func([]value) *irv1.Type { return tLocalDatetime(3) }, null: neverNull}
	}
	for _, n := range []string{"sysdatetime", "sysutcdatetime"} {
		m[n] = builtin{result: func([]value) *irv1.Type { return tLocalDatetime(7) }, null: neverNull}
	}
	m["sysdatetimeoffset"] = builtin{result: func([]value) *irv1.Type { return tOffsetDatetime(7) }, null: neverNull}
	m["dateadd"] = builtin{args: dateInt, min: 2, max: 2, result: lastArg, null: strictNull, part: true}
	m["datediff"] = builtin{args: dates, min: 2, max: 2, result: returns(tInt32), null: strictNull, part: true}
	m["datediff_big"] = builtin{args: dates, min: 2, max: 2, result: returns(tInt64), null: strictNull, part: true}
	m["datepart"] = builtin{args: dates, min: 1, max: 1, result: returns(tInt32), null: strictNull, part: true}
	m["datename"] = builtin{args: dates, min: 1, max: 1, result: returns(tText), null: strictNull, part: true}
	m["datetrunc"] = builtin{args: dates, min: 1, max: 1, result: firstArg, null: strictNull, part: true}
	for _, n := range []string{"year", "month", "day"} {
		m[n] = builtin{args: dates, min: 1, max: 1, result: returns(tInt32), null: strictNull}
	}
	m["eomonth"] = builtin{args: []func() *irv1.Type{tDate, tInt32}, min: 1, max: 2, result: returns(tDate), null: strictNull}
	m["datefromparts"] = builtin{args: ints, min: 3, max: 3, result: returns(tDate), null: strictNull}
	m["switchoffset"] = builtin{args: []func() *irv1.Type{func() *irv1.Type { return tOffsetDatetime(7) }, tText}, min: 2, max: 2, result: firstArg, null: strictNull}
	m["todatetimeoffset"] = builtin{args: []func() *irv1.Type{func() *irv1.Type { return tLocalDatetime(7) }, tText}, min: 2, max: 2,
		result: func([]value) *irv1.Type { return tOffsetDatetime(7) }, null: strictNull}
	m["isdate"] = builtin{args: text, min: 1, max: 1, result: returns(tInt32), null: neverNull}

	m["json_value"] = builtin{args: text, min: 2, max: 2, result: returns(tText)}
	m["json_query"] = builtin{args: text, min: 1, max: 2, result: returns(tJSON)}
	m["json_modify"] = builtin{args: text, min: 3, max: 3, result: returns(tJSON), null: firstNull}
	m["isjson"] = builtin{args: text, min: 1, max: 2, result: returns(tBool), null: strictNull}
	m["json_path_exists"] = builtin{args: text, min: 2, max: 2, result: returns(tBool), null: strictNull}
	return m
}()

// aggregates are the aggregate functions, which are NULL over no rows,
// except COUNT() and COUNT_BIG().
var aggregates = map[string]builtin{
	"count":      {min: 0, max: 1, result: returns(tInt32), null: neverNull},
	"count_big":  {min: 0, max: 1, result: returns(tInt64), null: neverNull},
	"min":        {min: 1, max: 1, result: firstArg},
	"max":        {min: 1, max: 1, result: firstArg},
	"sum":        {min: 1, max: 1, result: sumType},
	"avg":        {min: 1, max: 1, result: sumType},
	"stdev":      {args: floats, min: 1, max: 1, result: returns(tFloat64)},
	"stdevp":     {args: floats, min: 1, max: 1, result: returns(tFloat64)},
	"var":        {args: floats, min: 1, max: 1, result: returns(tFloat64)},
	"varp":       {args: floats, min: 1, max: 1, result: returns(tFloat64)},
	"string_agg": {args: text, min: 2, max: 2, result: returns(tText)},
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
}

// call types a function call: a special form such as COALESCE(), a
// built-in function, an aggregate, a window function, or a function of the
// catalog.
func (a *analyzer) call(c *call, sc *scope) (value, error) {
	if c.schema != "" {
		return a.callRoutine(c, sc)
	}
	if c.over != nil {
		return a.window(c, sc)
	}
	if _, ok := aggregates[c.name]; ok {
		return a.aggregate(c, sc)
	}
	switch c.name {
	case "coalesce", "greatest", "least":
		if len(c.args) < 2 {
			return value{}, a.errAt("query-unknown-function", c.off, "%s takes two or more arguments", c.name)
		}
		vs, t, err := a.common(c.args, sc, c.off)
		if err != nil {
			return value{}, err
		}
		// Each is NULL only when every argument is.
		v := value{typ: t, nullable: true}
		for _, x := range vs {
			if !x.nullable {
				v.nullable, v.annotated = false, x.annotated
				break
			}
		}
		return v, nil
	case "isnull":
		// ISNULL() has its first argument's type.
		vs, err := a.args(c.args, sc)
		if err != nil {
			return value{}, err
		}
		l, r, err := a.unify(vs[0], vs[1], true)
		if err != nil {
			return value{}, err
		}
		l = settle(l)
		if l.typ == nil {
			return value{}, a.errAt("query-param-untyped", c.off, "the first argument of isnull is untyped; cast it")
		}
		v := value{typ: bare(l.typ), nullable: l.nullable && r.nullable}
		switch {
		case !l.nullable:
			v.annotated = l.annotated
		case !r.nullable:
			v.annotated = r.annotated
		}
		return v, nil
	case "nullif":
		vs, _, err := a.common(c.args, sc, c.off)
		if err != nil {
			return value{}, err
		}
		return value{typ: bare(vs[0].typ), nullable: true}, nil
	case "iif":
		cond, err := a.expr(c.args[0], sc)
		if err != nil {
			return value{}, err
		}
		if _, err := a.expect(cond, tBool(), true); err != nil {
			return value{}, err
		}
		vs, t, err := a.common(c.args[1:], sc, c.off)
		if err != nil {
			return value{}, err
		}
		return value{typ: t, nullable: !nonnull(vs...), annotated: anyAnnotated(vs...)}, nil
	}
	b, ok := builtins[c.name]
	if !ok {
		return value{}, a.errAt("query-unknown-function", c.off, "function %s is not a built-in function query files support; a function of the catalog is named with its schema, as in app.%s", c.name, c.name)
	}
	if b.part != (c.part != "") {
		return value{}, a.errAt("query-unknown-function", c.off, "%s is not called that way in query files", c.name)
	}
	v, _, err := a.callBuiltin(c.name, b, c.args, sc, c.off)
	return v, err
}

// args types a call's arguments.
func (a *analyzer) args(list []expr, sc *scope) ([]value, error) {
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

// callBuiltin types a call of a built-in function, an aggregate or a window
// function.
func (a *analyzer) callBuiltin(name string, b builtin, list []expr, sc *scope, off int) (value, []value, error) {
	if n := len(list); n < b.min || n > b.max {
		return value{}, nil, a.errAt("query-unknown-function", off, "%s is not called with %d arguments in query files", name, n)
	}
	vs, err := a.args(list, sc)
	if err != nil {
		return value{}, nil, err
	}
	for i, v := range vs {
		v.soft = v.literal || v.param != 0
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
	if b.volatile {
		a.writes = true
	}
	t := b.result(vs)
	if t == nil {
		return value{}, nil, a.errAt("query-unsupported", off, "%s is not supported on this argument type in query files; cast its argument", name)
	}
	v := value{typ: t, nullable: true}
	switch b.null {
	case neverNull:
		v.nullable = false
	case strictNull:
		v = strict(t, vs...)
	case firstNull:
		v = strict(t, vs[0])
	}
	return v, vs, nil
}

// aggregate types an aggregate function, with its DISTINCT, and
// STRING_AGG's WITHIN GROUP order. MIN(), MAX() and SUM() are non-null over
// a non-empty group of non-null inputs (guide §5.4); without GROUP BY the
// input may be empty.
func (a *analyzer) aggregate(c *call, sc *scope) (value, error) {
	if err := a.byItems(c.order, sc, false); err != nil {
		return value{}, err
	}
	v, vs, err := a.callBuiltin(c.name, aggregates[c.name], c.args, sc, c.off)
	if err != nil {
		return value{}, err
	}
	if sc.grouped && slices.Contains([]string{"min", "max", "sum"}, c.name) && nonnull(vs...) {
		v.nullable, v.annotated = false, anyAnnotated(vs...)
	}
	return v, nil
}

// window types a window function, with its PARTITION BY and ORDER BY.
func (a *analyzer) window(c *call, sc *scope) (value, error) {
	w := c.over
	if err := a.byItems(w.partition, sc, false); err != nil {
		return value{}, err
	}
	if err := a.byItems(w.order, sc, false); err != nil {
		return value{}, err
	}
	b, ok := windows[c.name]
	if !ok {
		b = aggregates[c.name]
	}
	if c.star {
		c.args = nil
	}
	v, _, err := a.callBuiltin(c.name, b, c.args, sc, c.off)
	return v, err
}

// callRoutine types a call of a function of the catalog. Its result is
// nullable unless the catalog says otherwise (§6.5).
func (a *analyzer) callRoutine(c *call, sc *scope) (value, error) {
	rs := a.ix.routines(c.schema, c.name)
	if len(rs) != 1 {
		return value{}, a.errAt("query-unknown-function", c.off, "function %s.%s is not a function of the catalog", c.schema, c.name)
	}
	vs, err := a.args(c.args, sc)
	if err != nil {
		return value{}, err
	}
	var match []*irv1.Overload
	for _, o := range rs[0].obj.GetOverloads() {
		if o.GetKind() == irv1.RoutineKind_ROUTINE_KIND_FUNCTION && o.GetReturns().GetScalar() != nil && fits(o, vs) {
			match = append(match, o)
		}
	}
	if len(match) != 1 {
		return value{}, a.errAt("query-unknown-function", c.off, "function %s.%s does not take these %d arguments", c.schema, c.name, len(vs))
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
// typed argument, which may be a narrower number, as SQL Server converts.
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
