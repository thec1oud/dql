package sqlite

// The statement a query holds, read from the grammar's tree into the forms
// the analyzer types. Every node keeps where it is written: its byte
// offset in the rewritten statement.

// stmt is a SELECT, INSERT, UPDATE or DELETE.
type stmt interface{ at() int }

// expr is an expression.
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
	with  *withClause
	cores []*selectCore
	// The operators between the cores: UNION, UNION ALL, INTERSECT or
	// EXCEPT.
	ops           []string
	order         []expr
	limit, offset expr
}

type withClause struct {
	pos
	recursive bool
	ctes      []*cte
}

type cte struct {
	pos
	name string
	cols []string
	sel  *selectStmt
}

// selectCore is one SELECT of a compound, or a VALUES list.
type selectCore struct {
	pos
	values [][]expr
	cols   []*resultCol
	from   from
	where  expr
	group  []expr
	having expr
	// The SELECT defines named windows.
	windows bool
}

// resultCol is an item of a SELECT list or a RETURNING clause: *, t.* or
// an expression.
type resultCol struct {
	pos
	star  bool
	table string
	e     expr
	alias string
}

type tableRef struct {
	pos
	schema, name, alias string
}

type subqueryRef struct {
	pos
	sel   *selectStmt
	alias string
}

type join struct {
	pos
	left, right from
	// LEFT, RIGHT, FULL, CROSS or INNER, which a comma is too.
	kind    string
	natural bool
	on      expr
	using   []name
}

// name is a name and where it is written.
type name struct {
	pos
	text string
}

type assign struct {
	cols []name
	e    expr
}

type upsert struct {
	pos
	target      []expr
	targetWhere expr
	nothing     bool
	sets        []assign
	where       expr
}

type insertStmt struct {
	pos
	with          *withClause
	table         tableRef
	cols          []name
	sel           *selectStmt
	defaultValues bool
	upserts       []*upsert
	returning     []*resultCol
}

type updateStmt struct {
	pos
	with      *withClause
	table     tableRef
	sets      []assign
	from      from
	where     expr
	returning []*resultCol
	limited   bool
}

type deleteStmt struct {
	pos
	with      *withClause
	table     tableRef
	where     expr
	returning []*resultCol
	limited   bool
}

type colRef struct {
	pos
	schema, table, name string
}

// placeholder is a ? the rewrite put in place of a parameter.
type placeholder struct{ pos }

type litKind int

const (
	litNull litKind = iota
	litInteger
	litReal
	litString
	litBlob
	litBool
	litTime
	litDate
	litTimestamp
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

// binary is a binary operator, = and <> in one spelling each.
type binary struct {
	pos
	op   string
	l, r expr
}

// isExpr is IS, IS NOT, IS DISTINCT FROM or IS NOT DISTINCT FROM.
type isExpr struct {
	pos
	l, r expr
}

// nullTest is ISNULL, NOTNULL or NOT NULL.
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

// likeExpr is LIKE and GLOB; REGEXP and MATCH are unsupported.
type likeExpr struct {
	pos
	x, pattern, escape expr
}

type collate struct {
	pos
	x expr
}

type call struct {
	pos
	name     string
	distinct bool
	star     bool
	args     []expr
	order    []expr
	filter   expr
	over     *window
}

type window struct {
	pos
	named     bool
	partition []expr
	order     []expr
}

type cast struct {
	pos
	x   expr
	typ string
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
