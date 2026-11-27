package sqlite

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
	// error the caller reports. A nil result leaves the value untyped: one
	// whose storage class only the data decides, as json_extract's.
	result func(args []value) *irv1.Type
	// The result's storage class depends on the data; it needs a cast.
	dynamic bool
	// How the result's nullability follows from the arguments'.
	null nullRule
	// The function reads more than its arguments, such as random() or
	// last_insert_rowid(), so the query runs on the primary (§22).
	volatile bool
}

// A nullRule is when a function's result is NULL.
type nullRule int

const (
	// The result may be NULL whatever the arguments.
	maybeNull nullRule = iota
	// NULL only for a NULL argument (guide §5.4).
	strictNull
	// Never NULL, as count() and typeof().
	neverNull
	// NULL when the first argument is, as concat_ws's separator.
	firstNull
	// Never NULL when called without arguments, as date() for today, and
	// otherwise maybe: text that is no date or time gives NULL.
	nowNull
)

func returns(t func() *irv1.Type) func([]value) *irv1.Type {
	return func([]value) *irv1.Type { return t() }
}

// firstArg returns the first argument's type, as min() and lag() do.
func firstArg(args []value) *irv1.Type { return bare(args[0].typ) }

// numericArg returns the first argument's type when it is a number: abs(),
// ceil() and floor() keep an integer an integer and a real a real.
func numericArg(args []value) *irv1.Type {
	if rank(args[0].typ) == 0 {
		return nil
	}
	return bare(logical(args[0].typ))
}

// sumType is sum()'s result type: INTEGER for integers, REAL for the rest.
func sumType(args []value) *irv1.Type {
	switch rank(args[0].typ) {
	case 0:
		return nil
	case 1:
		return tInt64()
	}
	return tFloat64()
}

var (
	text     = []func() *irv1.Type{tText}
	textInt  = []func() *irv1.Type{tText, tInt64}
	floats   = []func() *irv1.Type{tFloat64}
	jsonText = []func() *irv1.Type{tJSON, tText}
)

var builtins = func() map[string]builtin {
	m := map[string]builtin{}
	for _, n := range []string{"lower", "upper", "ltrim", "rtrim", "trim"} {
		m[n] = builtin{args: text, min: 1, max: 2, result: returns(tText), null: strictNull}
	}
	m["replace"] = builtin{args: text, min: 3, max: 3, result: returns(tText), null: strictNull}
	for _, n := range []string{"substr", "substring"} {
		m[n] = builtin{args: textInt, min: 2, max: 3, result: returns(tText), null: strictNull}
	}
	m["concat"] = builtin{args: text, min: 1, max: 100, result: returns(tText), null: neverNull}
	m["concat_ws"] = builtin{args: text, min: 2, max: 100, result: returns(tText), null: firstNull}
	for _, n := range []string{"printf", "format"} {
		m[n] = builtin{args: text, min: 1, max: 100, result: returns(tText), null: firstNull}
	}
	for _, n := range []string{"quote", "typeof"} {
		m[n] = builtin{min: 1, max: 1, result: returns(tText), null: neverNull}
	}
	m["hex"] = builtin{min: 1, max: 1, result: returns(tText), null: neverNull}
	m["unhex"] = builtin{args: text, min: 1, max: 2, result: returns(tBytes)}
	m["char"] = builtin{args: []func() *irv1.Type{tInt64}, max: 100, result: returns(tText), null: neverNull}
	m["unicode"] = builtin{args: text, min: 1, max: 1, result: returns(tInt64), null: strictNull}
	for _, n := range []string{"length", "octet_length"} {
		m[n] = builtin{args: text, min: 1, max: 1, result: returns(tInt64), null: strictNull}
	}
	m["instr"] = builtin{args: text, min: 2, max: 2, result: returns(tInt64), null: strictNull}
	m["abs"] = builtin{min: 1, max: 1, result: numericArg, null: strictNull}
	for _, n := range []string{"ceil", "ceiling", "floor", "trunc"} {
		m[n] = builtin{min: 1, max: 1, result: numericArg, null: strictNull}
	}
	m["round"] = builtin{args: []func() *irv1.Type{tFloat64, tInt64}, min: 1, max: 2, result: returns(tFloat64), null: strictNull}
	m["sign"] = builtin{args: floats, min: 1, max: 1, result: returns(tInt64), null: strictNull}
	// NULL outside their domains, such as sqrt(-1).
	for _, n := range []string{"sqrt", "ln", "log", "log2", "log10", "exp", "pow", "power", "mod", "acos", "asin", "atan", "atan2", "cos", "sin", "tan", "degrees", "radians"} {
		m[n] = builtin{args: floats, min: 1, max: 2, result: returns(tFloat64)}
	}
	m["pi"] = builtin{result: returns(tFloat64), null: neverNull}
	m["random"] = builtin{result: returns(tInt64), null: neverNull, volatile: true}
	m["randomblob"] = builtin{args: []func() *irv1.Type{tInt64}, min: 1, max: 1, result: returns(tBytes), null: neverNull, volatile: true}
	m["zeroblob"] = builtin{args: []func() *irv1.Type{tInt64}, min: 1, max: 1, result: returns(tBytes), null: strictNull}
	for _, n := range []string{"last_insert_rowid", "changes", "total_changes"} {
		m[n] = builtin{result: returns(tInt64), null: neverNull, volatile: true}
	}
	for _, n := range []string{"likely", "unlikely"} {
		m[n] = builtin{min: 1, max: 1, result: firstArg, null: strictNull}
	}
	m["likelihood"] = builtin{args: []func() *irv1.Type{nil, tFloat64}, min: 2, max: 2, result: firstArg, null: firstNull}

	// The date and time functions read text, and give NULL for text that
	// is no date or time; without arguments they read the current time.
	m["date"] = builtin{args: text, max: 100, result: returns(tDate), null: nowNull}
	m["time"] = builtin{args: text, max: 100, result: returns(tLocalTime), null: nowNull}
	m["datetime"] = builtin{args: text, max: 100, result: returns(tLocalDatetime), null: nowNull}
	m["julianday"] = builtin{args: text, max: 100, result: returns(tFloat64), null: nowNull}
	m["unixepoch"] = builtin{args: text, max: 100, result: returns(tInt64), null: nowNull}
	m["strftime"] = builtin{args: text, min: 1, max: 100, result: returns(tText)}
	m["timediff"] = builtin{args: text, min: 2, max: 2, result: returns(tText)}

	m["json"] = builtin{args: text, min: 1, max: 1, result: returns(tJSON), null: strictNull}
	m["json_extract"] = builtin{args: jsonText, min: 2, max: 100, dynamic: true}
	for _, n := range []string{"json_object", "json_array"} {
		m[n] = builtin{max: 100, result: returns(tJSON), null: neverNull}
	}
	m["json_quote"] = builtin{min: 1, max: 1, result: returns(tJSON), null: neverNull}
	for _, n := range []string{"json_set", "json_insert", "json_replace", "json_remove", "json_patch"} {
		m[n] = builtin{args: jsonText, min: 1, max: 100, result: returns(tJSON), null: firstNull}
	}
	m["json_array_length"] = builtin{args: jsonText, min: 1, max: 2, result: returns(tInt64)}
	m["json_type"] = builtin{args: jsonText, min: 1, max: 2, result: returns(tText)}
	m["json_valid"] = builtin{args: text, min: 1, max: 2, result: returns(tBool), null: strictNull}
	return m
}()

// aggregates are the aggregate functions, which are NULL over no rows,
// except count(), total() and the JSON ones.
var aggregates = map[string]builtin{
	"count":             {max: 1, result: returns(tInt64), null: neverNull},
	"min":               {min: 1, max: 1, result: firstArg},
	"max":               {min: 1, max: 1, result: firstArg},
	"sum":               {min: 1, max: 1, result: sumType},
	"total":             {min: 1, max: 1, result: returns(tFloat64), null: neverNull},
	"avg":               {min: 1, max: 1, result: returns(tFloat64)},
	"group_concat":      {args: text, min: 1, max: 2, result: returns(tText)},
	"string_agg":        {args: text, min: 2, max: 2, result: returns(tText)},
	"json_group_array":  {min: 1, max: 1, result: returns(tJSON), null: neverNull},
	"json_group_object": {args: []func() *irv1.Type{tText}, min: 2, max: 2, result: returns(tJSON), null: neverNull},
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

// call types a function call: a special form such as coalesce(), a
// built-in function, an aggregate or a window function. SQLite has no
// functions of its own catalog.
func (a *analyzer) call(c *call, sc *scope) (value, error) {
	if c.over != nil {
		return a.window(c, sc)
	}
	_, aggregate := aggregates[c.name]
	if (c.name == "min" || c.name == "max") && len(c.args) > 1 {
		// min() and max() of more than one argument compare them: NULL when
		// any is.
		aggregate = false
	}
	if aggregate {
		return a.aggregate(c, sc)
	}
	if c.distinct || c.star || c.order != nil || c.filter != nil {
		return value{}, a.errAt("query-unsupported", c.off, "%s is no aggregate, so takes no DISTINCT, *, ORDER BY or FILTER", c.name)
	}
	switch c.name {
	case "coalesce", "ifnull":
		vs, t, err := a.common(c.args, sc, c.off)
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
		vs, _, err := a.common(c.args, sc, c.off)
		if err != nil {
			return value{}, err
		}
		return value{typ: bare(vs[0].typ), nullable: true}, nil
	case "iif", "if":
		if len(c.args) != 3 {
			return value{}, a.errAt("query-unknown-function", c.off, "%s is called with a condition and two values in query files", c.name)
		}
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
		return strict(t, vs...), nil
	case "min", "max":
		vs, t, err := a.common(c.args, sc, c.off)
		if err != nil {
			return value{}, err
		}
		return strict(t, vs...), nil
	}
	b, ok := builtins[c.name]
	if !ok {
		return value{}, a.errAt("query-unknown-function", c.off, "function %s is not a built-in function query files support", c.name)
	}
	v, _, err := a.callBuiltin(c.name, b, c.args, sc, c.off)
	return v, err
}

// callBuiltin types a call of a built-in function, an aggregate or a window
// function.
func (a *analyzer) callBuiltin(name string, b builtin, list []expr, sc *scope, off int) (value, []value, error) {
	if n := len(list); n < b.min || n > b.max {
		return value{}, nil, a.errAt("query-unknown-function", off, "%s is not called with %d arguments in query files", name, n)
	}
	vs := make([]value, len(list))
	for i, n := range list {
		v, err := a.expr(n, sc)
		if err != nil {
			return value{}, nil, err
		}
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
	if b.dynamic {
		return value{nullable: true, off: off}, vs, nil
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
	case nowNull:
		v.nullable = len(vs) > 0
	}
	return v, vs, nil
}

// aggregate types an aggregate function, with its DISTINCT, ORDER BY and
// FILTER. min(), max() and sum() are non-null over a non-empty group of
// non-null inputs (guide §5.4); without GROUP BY the input may be empty.
func (a *analyzer) aggregate(c *call, sc *scope) (value, error) {
	if err := a.byItems(c.order, sc); err != nil {
		return value{}, err
	}
	if err := a.condition(c.filter, sc); err != nil {
		return value{}, err
	}
	if c.star && c.name != "count" {
		return value{}, a.errAt("query-unsupported", c.off, "only count takes *")
	}
	v, vs, err := a.callBuiltin(c.name, aggregates[c.name], c.args, sc, c.off)
	if err != nil {
		return value{}, err
	}
	if sc.grouped && c.filter == nil && slices.Contains([]string{"min", "max", "sum"}, c.name) && nonnull(vs...) {
		v.nullable, v.annotated = false, anyAnnotated(vs...)
	}
	return v, nil
}

// window types a window function, with its PARTITION BY and ORDER BY.
func (a *analyzer) window(c *call, sc *scope) (value, error) {
	w := c.over
	if w.named {
		return value{}, a.errAt("query-unsupported", w.off, "named windows are not supported in query files")
	}
	if err := a.byItems(w.partition, sc); err != nil {
		return value{}, err
	}
	if err := a.byItems(w.order, sc); err != nil {
		return value{}, err
	}
	if err := a.condition(c.filter, sc); err != nil {
		return value{}, err
	}
	b, ok := windows[c.name]
	if !ok {
		if b, ok = aggregates[c.name]; !ok {
			return value{}, a.errAt("query-unknown-function", c.off, "window function %s is not supported in query files", c.name)
		}
	}
	v, _, err := a.callBuiltin(c.name, b, c.args, sc, c.off)
	return v, err
}
