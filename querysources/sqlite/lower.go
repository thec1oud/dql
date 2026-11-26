package sqlite

import (
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"github.com/thec1oud/dql/querysources/sqlite/grammar"
)

// stmt reads a statement.
func (lw *lowerer) stmt(s grammar.ISql_stmtContext) stmt {
	switch {
	case s.EXPLAIN_() != nil:
	case s.Select_stmt() != nil:
		return lw.selectStmt(s.Select_stmt())
	case s.Insert_stmt() != nil:
		return lw.insert(s.Insert_stmt())
	case s.Update_stmt() != nil:
		return lw.update(s.Update_stmt())
	case s.Delete_stmt() != nil:
		return lw.delete(s.Delete_stmt())
	}
	return &unsupported{pos: lw.pos(s), msg: "only SELECT, INSERT, REPLACE, UPDATE and DELETE statements are supported in query files"}
}

func (lw *lowerer) with(w grammar.IWith_clauseContext) *withClause {
	if w == nil {
		return nil
	}
	out := &withClause{pos: lw.pos(w), recursive: w.RECURSIVE_() != nil}
	for _, c := range w.AllCommon_table_expression() {
		n := c.Cte_table_name()
		x := &cte{pos: lw.pos(c), name: ident(n.Table_name()), sel: lw.selectStmt(c.Select_stmt())}
		for _, col := range n.AllColumn_name() {
			x.cols = append(x.cols, ident(col))
		}
		out.ctes = append(out.ctes, x)
	}
	return out
}

func (lw *lowerer) selectStmt(s grammar.ISelect_stmtContext) *selectStmt {
	out := &selectStmt{pos: lw.pos(s), with: lw.with(s.With_clause())}
	for _, c := range s.AllSelect_core() {
		out.cores = append(out.cores, lw.core(c))
	}
	for _, op := range s.AllCompound_operator() {
		var words []string
		for _, k := range op.GetChildren() {
			words = append(words, strings.ToUpper(k.(antlr.TerminalNode).GetText()))
		}
		out.ops = append(out.ops, strings.Join(words, " "))
	}
	out.order = lw.order(s.Order_clause())
	if l := s.Limit_clause(); l != nil {
		out.limit = lw.expr(l.Expr(0))
		if l.Expr(1) != nil {
			out.offset = lw.expr(l.Expr(1))
		}
	}
	return out
}

func (lw *lowerer) order(o grammar.IOrder_clauseContext) []expr {
	if o == nil {
		return nil
	}
	var out []expr
	for _, t := range o.AllOrdering_term() {
		out = append(out, lw.expr(t.Expr()))
	}
	return out
}

func (lw *lowerer) exprs(es []grammar.IExprContext) []expr {
	var out []expr
	for _, e := range es {
		out = append(out, lw.expr(e))
	}
	return out
}

// optional reads an expression that may be absent.
func (lw *lowerer) optional(e grammar.IExprContext) expr {
	if e == nil {
		return nil
	}
	return lw.expr(e)
}

func (lw *lowerer) core(c grammar.ISelect_coreContext) *selectCore {
	out := &selectCore{pos: lw.pos(c)}
	if v := c.Values_clause(); v != nil {
		for _, row := range v.AllValue_row() {
			out.values = append(out.values, lw.exprs(row.AllExpr()))
		}
		return out
	}
	for _, rc := range c.AllResult_column() {
		out.cols = append(out.cols, lw.resultCol(rc))
	}
	if j := c.Join_clause(); j != nil {
		out.from = lw.joinClause(j)
	}
	out.where = lw.optional(c.GetWhere_expr())
	out.group = lw.exprs(c.GetGroup_by_expr())
	out.having = lw.optional(c.GetHaving_expr())
	out.windows = c.WINDOW_() != nil
	return out
}

func (lw *lowerer) resultCol(rc grammar.IResult_columnContext) *resultCol {
	out := &resultCol{pos: lw.pos(rc), star: rc.STAR() != nil, table: ident(rc.Table_name())}
	if rc.Expr() != nil {
		out.e, out.alias = lw.expr(rc.Expr()), ident(rc.Column_alias())
	}
	return out
}

// returning reads a RETURNING clause, whose items the grammar lists flat.
func (lw *lowerer) returning(r grammar.IReturning_clauseContext) []*resultCol {
	if r == nil {
		return nil
	}
	var out []*resultCol
	for _, k := range r.GetChildren()[1:] {
		switch k := k.(type) {
		case grammar.IExprContext:
			out = append(out, &resultCol{pos: lw.pos(k), e: lw.expr(k)})
		case grammar.IColumn_aliasContext:
			out[len(out)-1].alias = ident(k)
		case antlr.TerminalNode:
			if k.GetSymbol().GetTokenType() == grammar.SQLiteParserSTAR {
				out = append(out, &resultCol{pos: pos{off: lw.bytes[k.GetSymbol().GetStart()]}, star: true})
			}
		}
	}
	return out
}

// joinClause reads a FROM clause's items and joins, left to right.
func (lw *lowerer) joinClause(j grammar.IJoin_clauseContext) from {
	var acc from
	var cur *join
	for _, k := range j.GetChildren() {
		switch k := k.(type) {
		case grammar.ITable_or_subqueryContext:
			item := lw.tableOrSubquery(k)
			if cur == nil {
				acc = item
				continue
			}
			cur.right, acc = item, cur
		case grammar.IJoin_operatorContext:
			cur = &join{pos: lw.pos(k), left: acc, kind: "INNER", natural: k.NATURAL_() != nil}
			for _, t := range []antlr.TerminalNode{k.LEFT_(), k.RIGHT_(), k.FULL_(), k.CROSS_()} {
				if t != nil {
					cur.kind = strings.ToUpper(t.GetText())
				}
			}
		case grammar.IJoin_constraintContext:
			cur.on = lw.optional(k.Expr())
			for _, c := range k.AllColumn_name() {
				cur.using = append(cur.using, name{pos: lw.pos(c), text: ident(c)})
			}
		}
	}
	return acc
}

func (lw *lowerer) tableOrSubquery(t grammar.ITable_or_subqueryContext) from {
	switch {
	case t.Table_name() != nil:
		alias := ident(t.Table_alias())
		if alias == "" {
			alias = ident(t.Table_alias_excluding_joins())
		}
		return &tableRef{pos: lw.pos(t), schema: ident(t.Schema_name()), name: ident(t.Table_name()), alias: alias}
	case t.Table_function_name() != nil:
		return &unsupported{pos: lw.pos(t), msg: "table-valued functions are not supported in query files"}
	case t.Join_clause() != nil:
		return lw.joinClause(t.Join_clause())
	}
	return &subqueryRef{pos: lw.pos(t), sel: lw.selectStmt(t.Select_stmt()), alias: ident(t.Table_alias())}
}

func (lw *lowerer) qualified(q grammar.IQualified_table_nameContext) tableRef {
	return tableRef{pos: lw.pos(q), schema: ident(q.Schema_name()), name: ident(q.Table_name()), alias: ident(q.Alias())}
}

// sets reads the assignments of an UPDATE or an upsert's DO UPDATE, from
// the children after SET up to the first that is no assignment.
func (lw *lowerer) sets(kids []antlr.Tree) ([]assign, int) {
	var out []assign
	var cols []name
	for i, k := range kids {
		switch k := k.(type) {
		case grammar.IColumn_nameContext:
			cols = []name{{pos: lw.pos(k), text: ident(k)}}
		case grammar.IColumn_name_listContext:
			cols = nil
			for _, c := range k.AllColumn_name() {
				cols = append(cols, name{pos: lw.pos(c), text: ident(c)})
			}
		case grammar.IExprContext:
			out = append(out, assign{cols: cols, e: lw.expr(k)})
		case antlr.TerminalNode:
			if tt := k.GetSymbol().GetTokenType(); tt != grammar.SQLiteParserASSIGN && tt != grammar.SQLiteParserCOMMA {
				return out, i
			}
		}
	}
	return out, len(kids)
}

// after returns the children of ctx after its first token of type tt.
func after(ctx antlr.Tree, tt int) []antlr.Tree {
	kids := ctx.(antlr.ParserRuleContext).GetChildren()
	for i, k := range kids {
		if t, ok := k.(antlr.TerminalNode); ok && t.GetSymbol().GetTokenType() == tt {
			return kids[i+1:]
		}
	}
	return nil
}

func (lw *lowerer) insert(s grammar.IInsert_stmtContext) *insertStmt {
	out := &insertStmt{pos: lw.pos(s), with: lw.with(s.With_clause()),
		table:         tableRef{pos: lw.pos(s.Table_name()), schema: ident(s.Schema_name()), name: ident(s.Table_name()), alias: ident(s.Table_alias())},
		defaultValues: s.DEFAULT_() != nil, returning: lw.returning(s.Returning_clause())}
	for _, c := range s.AllColumn_name() {
		out.cols = append(out.cols, name{pos: lw.pos(c), text: ident(c)})
	}
	if s.Select_stmt() != nil {
		out.sel = lw.selectStmt(s.Select_stmt())
	}
	for _, u := range s.AllUpsert_clause() {
		out.upserts = append(out.upserts, lw.upsert(u))
	}
	return out
}

func (lw *lowerer) upsert(u grammar.IUpsert_clauseContext) *upsert {
	out := &upsert{pos: lw.pos(u), nothing: u.NOTHING_() != nil}
	for _, c := range u.AllIndexed_column() {
		out.target = append(out.target, lw.expr(c.Expr()))
	}
	// The target's WHERE comes before DO, the update's after its SET.
	if w := after(u, grammar.SQLiteParserWHERE_); w != nil && u.DO_().GetSymbol().GetStart() > w[0].(grammar.IExprContext).GetStart().GetStart() {
		out.targetWhere = lw.expr(w[0].(grammar.IExprContext))
	}
	if kids := after(u, grammar.SQLiteParserSET_); kids != nil {
		sets, n := lw.sets(kids)
		out.sets = sets
		if n < len(kids) {
			out.where = lw.expr(kids[n+1].(grammar.IExprContext))
		}
	}
	return out
}

func (lw *lowerer) update(s grammar.IUpdate_stmtContext) *updateStmt {
	out := &updateStmt{pos: lw.pos(s), with: lw.with(s.With_clause()), table: lw.qualified(s.Qualified_table_name()),
		returning: lw.returning(s.Returning_clause()), limited: s.Order_clause() != nil || s.Limit_clause() != nil}
	out.sets, _ = lw.sets(after(s, grammar.SQLiteParserSET_))
	if j := s.Join_clause(); j != nil {
		out.from = lw.joinClause(j)
	}
	if w := after(s, grammar.SQLiteParserWHERE_); w != nil {
		out.where = lw.expr(w[0].(grammar.IExprContext))
	}
	return out
}

func (lw *lowerer) delete(s grammar.IDelete_stmtContext) *deleteStmt {
	return &deleteStmt{pos: lw.pos(s), with: lw.with(s.With_clause()), table: lw.qualified(s.Qualified_table_name()),
		where: lw.optional(s.Expr()), returning: lw.returning(s.Returning_clause()), limited: s.Order_clause() != nil || s.Limit_clause() != nil}
}

// expr reads an expression from the grammar's layers of precedence.
func (lw *lowerer) expr(e grammar.IExprContext) expr { return lw.node(e.Expr_or()) }

func (lw *lowerer) node(t antlr.Tree) expr {
	switch n := t.(type) {
	case *grammar.Expr_notContext:
		x := lw.node(n.Expr_binary())
		for range n.AllNOT_() {
			x = &unary{pos: lw.pos(n), op: "NOT", x: x}
		}
		return x
	case *grammar.Expr_binaryContext:
		return lw.binary(n)
	case *grammar.Expr_collateContext:
		x := lw.node(n.Expr_unary())
		for range n.AllCOLLATE_() {
			x = &collate{pos: lw.pos(n), x: x}
		}
		return x
	case *grammar.Expr_unaryContext:
		kids := n.GetChildren()
		x := lw.node(n.Expr_base())
		for i := len(kids) - 2; i >= 0; i-- {
			op := kids[i].(antlr.TerminalNode).GetSymbol()
			x = &unary{pos: pos{off: lw.bytes[op.GetStart()]}, op: op.GetText(), x: x}
		}
		return x
	case *grammar.Expr_baseContext:
		return lw.base(n)
	}
	return lw.chain(t.(antlr.ParserRuleContext))
}

// chain folds a layer of operands and operators, left to right: OR, AND,
// comparisons, bitwise operators, arithmetic, || and the JSON arrows.
func (lw *lowerer) chain(ctx antlr.ParserRuleContext) expr {
	kids := ctx.GetChildren()
	x := lw.node(kids[0])
	for i := 1; i+1 < len(kids); i += 2 {
		x = &binary{pos: lw.pos(ctx), op: strings.ToUpper(kids[i].(antlr.TerminalNode).GetText()), l: x, r: lw.node(kids[i+1])}
	}
	return x
}

func tokenType(t antlr.Tree) int {
	if n, ok := t.(antlr.TerminalNode); ok {
		return n.GetSymbol().GetTokenType()
	}
	return -1
}

// binary reads expr_binary: an operand and the postfix forms after it,
// left to right.
func (lw *lowerer) binary(n *grammar.Expr_binaryContext) expr {
	kids := n.GetChildren()
	x := lw.node(kids[0])
	p := lw.pos(n)
	for i := 1; i < len(kids); {
		switch tokenType(kids[i]) {
		case grammar.SQLiteParserASSIGN, grammar.SQLiteParserEQ:
			x, i = &binary{pos: p, op: "=", l: x, r: lw.node(kids[i+1])}, i+2
		case grammar.SQLiteParserNOT_EQ1, grammar.SQLiteParserNOT_EQ2:
			x, i = &binary{pos: p, op: "<>", l: x, r: lw.node(kids[i+1])}, i+2
		case grammar.SQLiteParserIS_:
			for i++; tokenType(kids[i]) != -1; i++ {
			}
			x, i = &isExpr{pos: p, l: x, r: lw.node(kids[i])}, i+1
		case grammar.SQLiteParserNOT_:
			// NOT before BETWEEN, IN, LIKE and the rest leaves their type.
			if i++; tokenType(kids[i]) == grammar.SQLiteParserNULL_ {
				x, i = &nullTest{pos: p, x: x}, i+1
			}
		case grammar.SQLiteParserISNULL_, grammar.SQLiteParserNOTNULL_:
			x, i = &nullTest{pos: p, x: x}, i+1
		case grammar.SQLiteParserBETWEEN_:
			x, i = &between{pos: p, x: x, lo: lw.node(kids[i+1]), hi: lw.node(kids[i+3])}, i+4
		case grammar.SQLiteParserLIKE_, grammar.SQLiteParserGLOB_:
			l := &likeExpr{pos: p, x: x, pattern: lw.node(kids[i+1])}
			if i += 2; i < len(kids) && tokenType(kids[i]) == grammar.SQLiteParserESCAPE_ {
				l.escape, i = lw.node(kids[i+1]), i+2
			}
			x = l
		case grammar.SQLiteParserREGEXP_, grammar.SQLiteParserMATCH_:
			return &unsupported{pos: p, msg: "REGEXP and MATCH call functions SQLite does not define; use LIKE or GLOB"}
		default: // IN
			if tokenType(kids[i+1]) != grammar.SQLiteParserOPEN_PAR {
				return &unsupported{pos: p, msg: "IN takes a parenthesized list or subquery in query files"}
			}
			in := &inExpr{pos: p, x: x}
			for i += 2; tokenType(kids[i]) != grammar.SQLiteParserCLOSE_PAR; i++ {
				switch k := kids[i].(type) {
				case grammar.ISelect_stmtContext:
					in.sel = lw.selectStmt(k)
				case *grammar.Expr_comparisonContext:
					in.list = append(in.list, lw.node(k))
				}
			}
			x, i = in, i+1
		}
	}
	return x
}

func (lw *lowerer) base(n *grammar.Expr_baseContext) expr {
	p := lw.pos(n)
	switch {
	case n.Literal_value() != nil:
		return &literal{pos: p, kind: literalKind(n.Literal_value()), text: n.Literal_value().GetText()}
	case n.BIND_PARAMETER() != nil:
		return &placeholder{pos: p}
	case n.Column_name_excluding_string() != nil:
		return &colRef{pos: p, name: ident(n.Column_name_excluding_string())}
	case n.Column_name() != nil:
		return &colRef{pos: p, schema: ident(n.Schema_name()), table: ident(n.Table_name()), name: ident(n.Column_name())}
	case n.Select_stmt() != nil && n.EXISTS_() != nil:
		return &exists{pos: p, sel: lw.selectStmt(n.Select_stmt())}
	case n.Select_stmt() != nil:
		return &subquery{pos: p, sel: lw.selectStmt(n.Select_stmt())}
	case n.Raise_function() != nil:
		return &unsupported{pos: p, msg: "RAISE is supported only in triggers"}
	}
	return lw.recursive(n.Expr_recursive(), p)
}

func (lw *lowerer) recursive(r grammar.IExpr_recursiveContext, p pos) expr {
	switch {
	case r.Function_name() != nil:
		if r.Percentile_clause() != nil {
			return &unsupported{pos: p, msg: "WITHIN GROUP is not supported in query files"}
		}
		c := &call{pos: p, name: strings.ToLower(ident(r.Function_name())), distinct: r.DISTINCT_() != nil, star: r.STAR() != nil,
			args: lw.exprs(r.AllExpr()), order: lw.order(r.Order_clause())}
		if f := r.Filter_clause(); f != nil {
			c.filter = lw.expr(f.Expr())
		}
		if o := r.Over_clause(); o != nil {
			c.over = &window{pos: lw.pos(o), named: o.Window_name() != nil || o.Base_window_name() != nil,
				partition: lw.exprs(o.AllExpr()), order: lw.order(o.Order_clause())}
		}
		return c
	case r.CAST_() != nil:
		var words []string
		for _, w := range r.Type_name().AllName() {
			words = append(words, w.GetText())
		}
		return &cast{pos: p, x: lw.expr(r.Expr(0)), typ: strings.ToUpper(strings.Join(words, " "))}
	case r.CASE_() != nil:
		c := &caseExpr{pos: p}
		var last int
		for _, k := range r.GetChildren() {
			switch k := k.(type) {
			case antlr.TerminalNode:
				last = k.GetSymbol().GetTokenType()
			case grammar.IExprContext:
				switch last {
				case grammar.SQLiteParserCASE_:
					c.subject = lw.expr(k)
				case grammar.SQLiteParserWHEN_:
					c.whens = append(c.whens, lw.expr(k))
				case grammar.SQLiteParserTHEN_:
					c.thens = append(c.thens, lw.expr(k))
				default:
					c.els = lw.expr(k)
				}
			}
		}
		return c
	}
	if es := r.AllExpr(); len(es) == 1 {
		return lw.expr(es[0])
	}
	return &unsupported{pos: p, msg: "row values are not supported in query files"}
}

func literalKind(l grammar.ILiteral_valueContext) litKind {
	switch {
	case l.NUMERIC_LITERAL() != nil:
		t := strings.ToLower(l.GetText())
		if !strings.HasPrefix(t, "0x") && strings.ContainsAny(t, ".e") {
			return litReal
		}
		return litInteger
	case l.STRING_LITERAL() != nil:
		return litString
	case l.BLOB_LITERAL() != nil:
		return litBlob
	case l.TRUE_() != nil, l.FALSE_() != nil:
		return litBool
	case l.CURRENT_TIME_() != nil:
		return litTime
	case l.CURRENT_DATE_() != nil:
		return litDate
	case l.CURRENT_TIMESTAMP_() != nil:
		return litTimestamp
	}
	return litNull
}
