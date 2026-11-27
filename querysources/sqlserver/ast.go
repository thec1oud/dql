package sqlserver

// The statement a query holds, read from the grammar's tree into the forms
// the analyzer types. Every node keeps where it is written: its byte
// offset in the rewritten statement.

// stmt is a SELECT, INSERT, UPDATE or DELETE.
type stmt interface{ at() int }

// expr is an expression, or a search condition: T-SQL keeps the two
// apart, and the analyzer types a condition as bool.
type expr interface{ at() int }

// from is an item of a FROM clause: a table, a subquery or a join.
type from interface{ at() int }

type pos struct{ off int }

func (p pos) at() int { return p.off }

// unsupported is a form query files do not support, with what to say.
type unsupported struct {
	pos
	msg string
}

type selectStmt struct {
	pos
	with  []*cte
	cores []*selectCore
	// The operators between the cores: UNION, UNION ALL, INTERSECT or
	// EXCEPT.
	ops   []string
	order []expr
	// OFFSET n ROWS and FETCH NEXT n ROWS ONLY.
	offset, fetch expr
}

type cte struct {
	pos
	name string
	cols []string
	sel  *selectStmt
}

// selectCore is one SELECT of a compound.
type selectCore struct {
	pos
	// TOP (n), and whether it is TOP (n) PERCENT.
	top     expr
	percent bool
	cols    []*resultCol
	from    from
	where   expr
	group   []expr
	having  expr
}

// resultCol is an item of a SELECT list or an OUTPUT clause: *, t.* or an
// expression.
type resultCol struct {
	pos
	star  bool
	table string
	e     expr
	alias string
}

// tableRef names a table or view, or a CTE: schema is empty for a CTE's
// name, which the analyzer also tries first.
type tableRef struct {
	pos
	schema, name, alias string
}

type subqueryRef struct {
	pos
	sel   *selectStmt
	alias string
	cols  []string
}

type join struct {
	pos
	left, right from
	// LEFT, RIGHT, FULL, CROSS or INNER, which a comma is too.
	kind string
	on   expr
}

// name is a name and where it is written.
type name struct {
	pos
	text string
}

type assign struct {
	col name
	// The compound operator, such as +=, or empty for =.
	op string
	e  expr
}

type insertStmt struct {
	pos
	with  []*cte
	top   expr
	table tableRef
	cols  []name
	// The rows: VALUES lists, or a SELECT; neither for DEFAULT VALUES.
	values [][]expr
	sel    *selectStmt
	output []*resultCol
}

type updateStmt struct {
	pos
	with   []*cte
	top    expr
	table  tableRef
	sets   []assign
	output []*resultCol
	from   from
	where  expr
}

type deleteStmt struct {
	pos
	with   []*cte
	top    expr
	table  tableRef
	output []*resultCol
	from   from
	where  expr
}

// colRef is a column reference: schema.table.name, table.name or name.
type colRef struct {
	pos
	schema, table, name string
}

// placeholder is an @pN the rewrite put in place of a parameter.
type placeholder struct{ pos }

// defaultExpr is DEFAULT, as a value an INSERT or UPDATE writes.
type defaultExpr struct{ pos }

type litKind int

const (
	litNull litKind = iota
	// An integer, such as 42.
	litInteger
	// A number with a point, such as 1.5, which T-SQL reads as numeric.
	litNumeric
	// A number with an exponent, such as 1e3: float.
	litFloat
	// A money constant, such as $1.50.
	litMoney
	litString
	litBinary
)

type literal struct {
	pos
	kind litKind
	text string
}

// unary is -, +, ~ or NOT.
type unary struct {
	pos
	op string
	x  expr
}

// binary is a binary operator: AND, OR, a comparison, spelled one way
// each, or an arithmetic or bitwise one.
type binary struct {
	pos
	op   string
	l, r expr
}

// nullTest is IS NULL or IS NOT NULL.
type nullTest struct {
	pos
	x expr
}

type between struct {
	pos
	x, lo, hi expr
}

type inExpr struct {
	pos
	x    expr
	list []expr
	sel  *selectStmt
}

// quantified is x op ALL, SOME or ANY (subquery).
type quantified struct {
	pos
	x   expr
	sel *selectStmt
}

type likeExpr struct {
	pos
	x, pattern, escape expr
}

type collate struct {
	pos
	x expr
}

// call is a function call: a built-in one, named in lowercase, or a
// function of the catalog, schema.name.
type call struct {
	pos
	schema, name string
	// The date part a date function takes first, such as day.
	part     string
	distinct bool
	star     bool
	args     []expr
	// STRING_AGG's WITHIN GROUP (ORDER BY ...).
	order []expr
	over  *window
}

type window struct {
	pos
	partition []expr
	order     []expr
}

type cast struct {
	pos
	x   expr
	typ dataType
	// TRY_CAST and TRY_CONVERT, NULL for a value that does not convert.
	try bool
}

// dataType is a type a cast names: its name in lowercase and its
// arguments, -1 for max.
type dataType struct {
	name string
	args []int
}

type caseExpr struct {
	pos
	subject      expr
	whens, thens []expr
	els          expr
}

type subquery struct {
	pos
	sel *selectStmt
}

type exists struct {
	pos
	sel *selectStmt
}
