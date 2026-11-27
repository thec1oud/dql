package sqlserver

import (
	"strconv"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"github.com/thec1oud/dql/querysources/sqlserver/grammar"
)

const onlyDML = "only SELECT, INSERT, UPDATE and DELETE statements are supported in query files"

// batch reads a batch's statements; a GO, a batch-level statement such as
// CREATE PROCEDURE, and EXECUTE are unsupported ones.
func (lw *lowerer) batch(b grammar.IBatchContext) []stmt {
	var out []stmt
	for _, k := range b.GetChildren() {
		switch k := k.(type) {
		case grammar.ISql_clausesContext:
			switch {
			case k.Dml_clause() != nil:
				out = append(out, lw.dml(k.Dml_clause()))
			case k.SEMI() != nil && k.GetChildCount() == 1:
			default:
				out = append(out, &unsupported{pos: lw.pos(k), msg: onlyDML})
			}
		case antlr.ParserRuleContext:
			out = append(out, &unsupported{pos: lw.pos(k), msg: onlyDML})
		}
	}
	return out
}

func (lw *lowerer) dml(d grammar.IDml_clauseContext) stmt {
	switch {
	case d.Select_statement_standalone() != nil:
		s := d.Select_statement_standalone()
		with := lw.with(s.With_expression())
		sel := lw.selectStmt(s.Select_statement())
		if sel, ok := sel.(*selectStmt); ok {
			sel.with = with
		}
		return sel
	case d.Insert_statement() != nil:
		return lw.insert(d.Insert_statement())
	case d.Update_statement() != nil:
		return lw.update(d.Update_statement())
	case d.Delete_statement() != nil:
		return lw.delete(d.Delete_statement())
	}
	return &unsupported{pos: lw.pos(d), msg: "MERGE is not supported in query files yet; write the INSERT and UPDATE it would run"}
}

func (lw *lowerer) with(w grammar.IWith_expressionContext) []*cte {
	if w == nil {
		return nil
	}
	var out []*cte
	for _, c := range w.AllCommon_table_expression() {
		x := &cte{pos: lw.pos(c), name: ident(c.GetExpression_name())}
		if cols := c.GetColumns(); cols != nil {
			for _, id := range cols.AllId_() {
				x.cols = append(x.cols, ident(id))
			}
		}
		sel := lw.selectStmt(c.GetCte_query())
		if s, ok := sel.(*selectStmt); ok {
			x.sel = s
		} else {
			x.sel = &selectStmt{pos: x.pos, cores: []*selectCore{{pos: x.pos, from: sel.(*unsupported)}}}
		}
		out = append(out, x)
	}
	return out
}

// selectStmt reads a SELECT: a query expression, its ORDER BY, OFFSET and
// FETCH. FOR XML and FOR JSON are unsupported; OPTION's hints do not change
// what the query returns.
func (lw *lowerer) selectStmt(s grammar.ISelect_statementContext) stmt {
	if f := s.For_clause(); f != nil {
		return &unsupported{pos: lw.pos(f), msg: "FOR XML and FOR JSON are not supported in query files"}
	}
	q := s.Query_expression()
	if q.Query_specification() == nil {
		return &unsupported{pos: lw.pos(q), msg: "a parenthesized query expression is not supported in query files; write its SELECT without parentheses"}
	}
	out := &selectStmt{pos: lw.pos(s), cores: []*selectCore{lw.core(q.Query_specification())}}
	order := q.Select_order_by_clause()
	for _, u := range q.AllSql_union() {
		if u.Query_specification() == nil {
			return &unsupported{pos: lw.pos(u), msg: "a parenthesized query expression is not supported in query files; write its SELECT without parentheses"}
		}
		var words []string
		for _, k := range u.GetChildren() {
			if t, ok := k.(antlr.TerminalNode); ok {
				words = append(words, strings.ToUpper(t.GetText()))
			}
		}
		out.ops = append(out.ops, strings.Join(words, " "))
		out.cores = append(out.cores, lw.core(u.Query_specification()))
	}
	if o := s.Select_order_by_clause(); o != nil {
		order = o
	}
	if order != nil {
		out.order = lw.order(order.Order_by_clause())
		if e := order.GetOffset_exp(); e != nil {
			out.offset = lw.expr(e)
		}
		if e := order.GetFetch_exp(); e != nil {
			out.fetch = lw.expr(e)
		}
	}
	return out
}

func (lw *lowerer) order(o grammar.IOrder_by_clauseContext) []expr {
	if o == nil {
		return nil
	}
	var out []expr
	for _, e := range o.AllOrder_by_expression() {
		out = append(out, lw.expr(e.Expression()))
	}
	return out
}

func (lw *lowerer) exprs(l grammar.IExpression_list_Context) []expr {
	if l == nil {
		return nil
	}
	var out []expr
	for _, e := range l.AllExpression() {
		out = append(out, lw.expr(e))
	}
	return out
}

// top reads TOP (n) or TOP n; TOP (n) PERCENT sets percent.
func (lw *lowerer) top(t grammar.ITop_clauseContext) (expr, bool) {
	if t == nil {
		return nil, false
	}
	if p := t.Top_percent(); p != nil {
		if e := p.GetTopper_expression(); e != nil {
			return lw.expr(e), true
		}
		return &literal{pos: lw.pos(p), kind: litFloat, text: p.GetPercent_constant().GetText()}, true
	}
	c := t.Top_count()
	if e := c.GetTopcount_expression(); e != nil {
		return lw.expr(e), false
	}
	return &literal{pos: lw.pos(c), kind: litInteger, text: c.GetText()}, false
}

func (lw *lowerer) core(q grammar.IQuery_specificationContext) *selectCore {
	out := &selectCore{pos: lw.pos(q)}
	if q.INTO() != nil {
		out.from = &unsupported{pos: lw.pos(q.GetInto()), msg: "SELECT INTO is not supported in query files"}
		return out
	}
	if q.GROUPING() != nil || q.GetGroupByAll() != nil {
		out.from = &unsupported{pos: lw.pos(q), msg: "GROUP BY ALL and GROUPING SETS are not supported in query files"}
		return out
	}
	out.top, out.percent = lw.top(q.Top_clause())
	for _, el := range q.Select_list().AllSelect_list_elem() {
		out.cols = append(out.cols, lw.selectElem(el))
	}
	if f := q.GetFrom(); f != nil {
		out.from = lw.tableSources(f)
	}
	if w := q.GetWhere(); w != nil {
		out.where = lw.cond(w)
	}
	for _, g := range q.AllGroup_by_item() {
		out.group = append(out.group, lw.expr(g.Expression()))
	}
	if h := q.GetHaving(); h != nil {
		out.having = lw.cond(h)
	}
	return out
}

// selectElem reads an item of a SELECT list: *, t.*, expr [AS] alias, or
// alias = expr.
func (lw *lowerer) selectElem(el grammar.ISelect_list_elemContext) *resultCol {
	switch {
	case el.Asterisk() != nil:
		return lw.asterisk(el.Asterisk())
	case el.Expression_elem() != nil:
		e := el.Expression_elem()
		if a := e.GetLeftAlias(); a != nil {
			return &resultCol{pos: lw.pos(e), e: lw.expr(e.GetLeftAssignment()), alias: columnAlias(a)}
		}
		out := &resultCol{pos: lw.pos(e), e: lw.expr(e.GetExpressionAs())}
		if a := e.As_column_alias(); a != nil {
			out.alias = columnAlias(a.Column_alias())
		}
		return out
	}
	if u := el.Udt_elem(); u != nil {
		// schema.function(@p1, 'x') reads as a method of a CLR column.
		// The static form, type::method(), does not parse.
		out := &resultCol{pos: lw.pos(u)}
		if a := u.As_column_alias(); a != nil {
			out.alias = columnAlias(a.Column_alias())
		}
		c := &call{pos: lw.pos(u), schema: ident(u.GetUdt_column_name()), name: ident(u.GetNon_static_attr())}
		for _, v := range u.Udt_method_arguments().AllExecute_var_string() {
			c.args = append(c.args, lw.varString(v))
		}
		out.e = c
		return out
	}
	return &resultCol{pos: lw.pos(el), e: &unsupported{pos: lw.pos(el), msg: "assigning to a variable is not supported in query files"}}
}

// varString reads an argument the grammar reads as EXECUTE's: placeholders
// and strings joined by +.
func (lw *lowerer) varString(v grammar.IExecute_var_stringContext) expr {
	var out expr
	for _, k := range v.GetChildren() {
		var term expr
		switch k := k.(type) {
		case grammar.IExecute_var_stringContext:
			term = lw.varString(k)
		case antlr.TerminalNode:
			at := lw.tokenPos(k)
			switch k.GetSymbol().GetTokenType() {
			case grammar.TSqlLexerLOCAL_ID:
				term = &placeholder{pos: at}
				if lw.r.use(at.off) < 0 {
					term = &unsupported{pos: at, msg: k.GetText() + " is not supported in query files"}
				}
			case grammar.TSqlLexerSTRING:
				term = &literal{pos: at, kind: litString, text: unquoteString(k.GetText())}
			case grammar.TSqlLexerPLUS:
				continue
			default:
				return &unsupported{pos: at, msg: "OUTPUT arguments are not supported in query files"}
			}
		}
		if out == nil {
			out = term
		} else {
			out = &binary{pos: lw.pos(v), op: "+", l: out, r: term}
		}
	}
	return out
}

func (lw *lowerer) asterisk(a grammar.IAsteriskContext) *resultCol {
	out := &resultCol{pos: lw.pos(a), star: true}
	// The parser reads inserted.* and deleted.* as a table's star, which
	// only OUTPUT has.
	if t := a.Table_name(); t != nil {
		out.table = tableName(t.GetSchema(), t.GetTable())
	}
	return out
}

// tableName joins a qualified name's schema and name with a dot, for a
// qualifier that names a relation.
func tableName(schema, name grammar.IId_Context) string {
	if schema != nil {
		return ident(schema) + "." + ident(name)
	}
	return ident(name)
}

func columnAlias(a grammar.IColumn_aliasContext) string {
	if s := a.STRING(); s != nil {
		return unquoteString(s.GetText())
	}
	return ident(a.Id_())
}

// tableSources reads a FROM clause: its items, joined by commas, are cross
// joins.
func (lw *lowerer) tableSources(t grammar.ITable_sourcesContext) from {
	srcs := t.AllTable_source()
	if n := t.Non_ansi_join(); n != nil {
		srcs = n.AllTable_source()
	}
	var out from
	for _, s := range srcs {
		f := lw.tableSource(s)
		if out == nil {
			out = f
			continue
		}
		out = &join{pos: lw.pos(s), left: out, right: f, kind: "INNER"}
	}
	return out
}

// tableSource reads an item and its joins.
func (lw *lowerer) tableSource(s grammar.ITable_sourceContext) from {
	out := lw.tableItem(s.Table_source_item())
	for _, j := range s.AllJoin_part() {
		switch {
		case j.Join_on() != nil:
			on := j.Join_on()
			kind := "INNER"
			if t := on.GetJoin_type(); t != nil {
				kind = strings.ToUpper(t.GetText())
			}
			out = &join{pos: lw.pos(j), left: out, right: lw.tableSource(on.GetSource()), kind: kind, on: lw.cond(on.GetCond())}
		case j.Cross_join() != nil:
			out = &join{pos: lw.pos(j), left: out, right: lw.tableItem(j.Cross_join().Table_source_item()), kind: "CROSS"}
		case j.Apply_() != nil:
			return &unsupported{pos: lw.pos(j), msg: "APPLY is not supported in query files yet; join a subquery"}
		default:
			return &unsupported{pos: lw.pos(j), msg: "PIVOT and UNPIVOT are not supported in query files"}
		}
	}
	return out
}

func tableAlias(a grammar.IAs_table_aliasContext) string {
	if a == nil {
		return ""
	}
	return ident(a.Table_alias().Id_())
}

// tableItem reads a table or view, which may take table hints, or a
// subquery; a function, OPENJSON or a variable is unsupported.
func (lw *lowerer) tableItem(t grammar.ITable_source_itemContext) from {
	switch {
	case t.Full_table_name() != nil:
		ref := lw.tableRef(t.Full_table_name())
		ref.alias = tableAlias(t.As_table_alias())
		if t.Deprecated_table_hint() != nil || t.Sybase_legacy_hints() != nil {
			return &unsupported{pos: lw.pos(t), msg: "table hints are written WITH (...) in query files"}
		}
		return ref
	case t.Derived_table() != nil:
		d := t.Derived_table()
		if len(d.AllSubquery()) != 1 || d.LR_BRACKET() != nil {
			return &unsupported{pos: lw.pos(d), msg: "a VALUES list or a UNION of subqueries in parentheses is not supported in FROM in query files; write one SELECT"}
		}
		sel := lw.selectStmt(d.Subquery(0).Select_statement())
		s, ok := sel.(*selectStmt)
		if !ok {
			return sel.(*unsupported)
		}
		out := &subqueryRef{pos: lw.pos(t), sel: s, alias: tableAlias(t.As_table_alias())}
		if l := t.Column_alias_list(); l != nil {
			for _, a := range l.AllColumn_alias() {
				out.cols = append(out.cols, columnAlias(a))
			}
		}
		return out
	case t.Table_source() != nil:
		return lw.tableSource(t.Table_source())
	}
	return &unsupported{pos: lw.pos(t), msg: "only tables, views and subqueries are supported in FROM in query files"}
}

// tableRef reads a table's name. A server or database qualifier is kept as
// a schema the analyzer refuses.
func (lw *lowerer) tableRef(n grammar.IFull_table_nameContext) *tableRef {
	out := &tableRef{pos: lw.pos(n), schema: ident(n.GetSchema()), name: ident(n.GetTable())}
	if n.GetDatabase() != nil || n.GetServer() != nil || n.GetLinkedServer() != nil {
		out.schema = "\x00" + n.GetText()
	}
	return out
}

// ddlTable reads the table a write names; a table variable is
// unsupported.
func (lw *lowerer) ddlTable(d grammar.IDdl_objectContext) (tableRef, *unsupported) {
	if d.Full_table_name() == nil {
		return tableRef{}, &unsupported{pos: lw.pos(d), msg: "a write's target is a table or view in query files"}
	}
	return *lw.tableRef(d.Full_table_name()), nil
}

// output reads an OUTPUT clause; OUTPUT INTO returns no rows, so it is
// unsupported.
func (lw *lowerer) output(o grammar.IOutput_clauseContext) ([]*resultCol, *unsupported) {
	if o == nil {
		return nil, nil
	}
	if o.INTO() != nil {
		return nil, &unsupported{pos: lw.pos(o), msg: "OUTPUT INTO is not supported in query files; leave INTO out to return the rows"}
	}
	out := []*resultCol{}
	for _, el := range o.AllOutput_dml_list_elem() {
		var c *resultCol
		if a := el.Asterisk(); a != nil {
			c = lw.asterisk(a)
		} else {
			c = &resultCol{pos: lw.pos(el), e: lw.expr(el.Expression())}
		}
		if a := el.As_column_alias(); a != nil {
			c.alias = columnAlias(a.Column_alias())
		}
		out = append(out, c)
	}
	return out, nil
}

// writeTop reads TOP (n) of a write; PERCENT is unsupported. TOP n
// without parentheses does not parse in INSERT and UPDATE, and DELETE
// reports it itself.
func (lw *lowerer) writeTop(top antlr.TerminalNode, e grammar.IExpressionContext, percent antlr.TerminalNode) (expr, *unsupported) {
	switch {
	case top == nil:
		return nil, nil
	case percent != nil:
		return nil, &unsupported{pos: lw.tokenPos(top), msg: "TOP (n) PERCENT is not supported in a write in query files"}
	}
	return lw.expr(e), nil
}

func (lw *lowerer) insert(s grammar.IInsert_statementContext) stmt {
	out := &insertStmt{pos: lw.pos(s), with: lw.with(s.With_expression())}
	var u *unsupported
	if out.top, u = lw.writeTop(s.TOP(), s.Expression(), s.PERCENT()); u != nil {
		return u
	}
	if s.Rowset_function_limited() != nil {
		return &unsupported{pos: lw.pos(s.Rowset_function_limited()), msg: "a write's target is a table or view in query files"}
	}
	if out.table, u = lw.ddlTable(s.Ddl_object()); u != nil {
		return u
	}
	if l := s.Insert_column_name_list(); l != nil {
		for _, c := range l.AllInsert_column_id() {
			ids := c.AllId_()
			last := ids[len(ids)-1]
			out.cols = append(out.cols, name{pos: lw.pos(last), text: ident(last)})
		}
	}
	if out.output, u = lw.output(s.Output_clause()); u != nil {
		return u
	}
	v := s.Insert_statement_value()
	switch {
	case v.Table_value_constructor() != nil:
		for _, row := range v.Table_value_constructor().AllExpression_list_() {
			out.values = append(out.values, lw.exprs(row))
		}
	case v.Derived_table() != nil:
		d := v.Derived_table()
		if len(d.AllSubquery()) != 1 || d.LR_BRACKET() != nil {
			return &unsupported{pos: lw.pos(d), msg: "an INSERT's rows are one VALUES list or one SELECT in query files"}
		}
		sel := lw.selectStmt(d.Subquery(0).Select_statement())
		ss, ok := sel.(*selectStmt)
		if !ok {
			return sel
		}
		out.sel = ss
	case v.Execute_statement() != nil:
		return &unsupported{pos: lw.pos(v), msg: "INSERT ... EXECUTE is not supported in query files"}
	}
	return out
}

func (lw *lowerer) update(s grammar.IUpdate_statementContext) stmt {
	out := &updateStmt{pos: lw.pos(s), with: lw.with(s.With_expression())}
	var u *unsupported
	if out.top, u = lw.writeTop(s.TOP(), s.Expression(), s.PERCENT()); u != nil {
		return u
	}
	if s.Rowset_function_limited() != nil {
		return &unsupported{pos: lw.pos(s.Rowset_function_limited()), msg: "a write's target is a table or view in query files"}
	}
	if out.table, u = lw.ddlTable(s.Ddl_object()); u != nil {
		return u
	}
	for _, el := range s.AllUpdate_elem() {
		if el.LOCAL_ID() != nil || el.Full_column_name() == nil {
			return &unsupported{pos: lw.pos(el), msg: "an UPDATE sets columns in query files, not variables or a method's result"}
		}
		c := el.Full_column_name()
		a := assign{col: name{pos: lw.pos(c), text: ident(c.GetColumn_name())}, e: lw.expr(el.Expression())}
		if op := el.Assignment_operator(); op != nil {
			a.op = op.GetText()
		}
		out.sets = append(out.sets, a)
	}
	if out.output, u = lw.output(s.Output_clause()); u != nil {
		return u
	}
	if f := s.Table_sources(); f != nil {
		out.from = lw.tableSources(f)
	}
	if s.CURRENT() != nil {
		return &unsupported{pos: lw.tokenPos(s.CURRENT()), msg: "cursors are not supported in query files"}
	}
	if w := s.Search_condition(); w != nil {
		out.where = lw.cond(w)
	}
	return out
}

func (lw *lowerer) delete(s grammar.IDelete_statementContext) stmt {
	out := &deleteStmt{pos: lw.pos(s), with: lw.with(s.With_expression())}
	var u *unsupported
	if s.TOP() != nil && s.DECIMAL() != nil {
		return &unsupported{pos: lw.tokenPos(s.TOP()), msg: "write TOP (n) with parentheses in a write"}
	}
	if out.top, u = lw.writeTop(s.TOP(), s.Expression(), s.PERCENT()); u != nil {
		return u
	}
	f := s.Delete_statement_from()
	if f.Ddl_object() == nil {
		return &unsupported{pos: lw.pos(f), msg: "a write's target is a table or view in query files"}
	}
	if out.table, u = lw.ddlTable(f.Ddl_object()); u != nil {
		return u
	}
	if out.output, u = lw.output(s.Output_clause()); u != nil {
		return u
	}
	if t := s.Table_sources(); t != nil {
		out.from = lw.tableSources(t)
	}
	if s.CURRENT() != nil {
		return &unsupported{pos: lw.tokenPos(s.CURRENT()), msg: "cursors are not supported in query files"}
	}
	if w := s.Search_condition(); w != nil {
		out.where = lw.cond(w)
	}
	return out
}

// cond reads a search condition.
func (lw *lowerer) cond(c grammar.ISearch_conditionContext) expr {
	var x expr
	switch {
	case c.AND() != nil:
		return &binary{pos: lw.pos(c), op: "AND", l: lw.cond(c.Search_condition(0)), r: lw.cond(c.Search_condition(1))}
	case c.OR() != nil:
		return &binary{pos: lw.pos(c), op: "OR", l: lw.cond(c.Search_condition(0)), r: lw.cond(c.Search_condition(1))}
	case c.Predicate() != nil:
		x = lw.predicate(c.Predicate())
	default:
		x = lw.cond(c.Search_condition(0))
	}
	for range c.AllNOT() {
		x = &unary{pos: lw.pos(c), op: "NOT", x: x}
	}
	return x
}

// comparisons spell each comparison operator one way; !> and !< are <=
// and >=.
var comparisons = map[string]string{"=": "=", "<>": "<>", "!=": "<>", "<": "<", "<=": "<=", ">": ">", ">=": ">=", "!>": "<=", "!<": ">="}

func (lw *lowerer) predicate(p grammar.IPredicateContext) expr {
	at := lw.pos(p)
	switch {
	case p.EXISTS() != nil:
		return lw.subqueryOf(p.Subquery(), func(s *selectStmt) expr { return &exists{pos: at, sel: s} })
	case p.Comparison_operator() != nil && p.Subquery() != nil:
		return lw.subqueryOf(p.Subquery(), func(s *selectStmt) expr { return &quantified{pos: at, x: lw.expr(p.Expression(0)), sel: s} })
	case p.Comparison_operator() != nil:
		return &binary{pos: at, op: comparisons[p.Comparison_operator().GetText()], l: lw.expr(p.Expression(0)), r: lw.expr(p.Expression(1))}
	case p.BETWEEN() != nil:
		return &between{pos: at, x: lw.expr(p.Expression(0)), lo: lw.expr(p.Expression(1)), hi: lw.expr(p.Expression(2))}
	case p.IN() != nil && p.Subquery() != nil:
		return lw.subqueryOf(p.Subquery(), func(s *selectStmt) expr { return &inExpr{pos: at, x: lw.expr(p.Expression(0)), sel: s} })
	case p.IN() != nil:
		return &inExpr{pos: at, x: lw.expr(p.Expression(0)), list: lw.exprs(p.Expression_list_())}
	case p.LIKE() != nil:
		out := &likeExpr{pos: at, x: lw.expr(p.Expression(0)), pattern: lw.expr(p.Expression(1))}
		if p.ESCAPE() != nil {
			out.escape = lw.expr(p.Expression(2))
		}
		return out
	case p.IS() != nil:
		return &nullTest{pos: at, x: lw.expr(p.Expression(0))}
	}
	return &unsupported{pos: at, msg: "full-text predicates and *= joins are not supported in query files"}
}

// subqueryOf reads a subquery into the expression make builds of it.
func (lw *lowerer) subqueryOf(q grammar.ISubqueryContext, make func(*selectStmt) expr) expr {
	sel := lw.selectStmt(q.Select_statement())
	if s, ok := sel.(*selectStmt); ok {
		return make(s)
	}
	return sel.(*unsupported)
}

// expr reads an expression.
func (lw *lowerer) expr(e grammar.IExpressionContext) expr {
	at := lw.pos(e)
	switch {
	case e.Primitive_expression() != nil:
		return lw.primitive(e.Primitive_expression())
	case e.Function_call() != nil:
		return lw.call(e.Function_call())
	case e.COLLATE() != nil:
		return &collate{pos: at, x: lw.expr(e.Expression(0))}
	case e.Case_expression() != nil:
		return lw.caseExpr(e.Case_expression())
	case e.Full_column_name() != nil:
		return lw.column(e.Full_column_name())
	case e.Bracket_expression() != nil:
		b := e.Bracket_expression()
		if b.Expression() != nil {
			return lw.expr(b.Expression())
		}
		return lw.subqueryOf(b.Subquery(), func(s *selectStmt) expr { return &subquery{pos: at, sel: s} })
	case e.Unary_operator_expression() != nil:
		u := e.Unary_operator_expression()
		op := "~"
		if u.GetOp() != nil {
			op = u.GetOp().GetText()
		}
		return &unary{pos: at, op: op, x: lw.expr(u.Expression())}
	case e.GetOp() != nil:
		return &binary{pos: at, op: e.GetOp().GetText(), l: lw.expr(e.Expression(0)), r: lw.expr(e.Expression(1))}
	case e.Time_zone() != nil:
		return &unsupported{pos: at, msg: "AT TIME ZONE is not supported in query files; use SWITCHOFFSET or TODATETIMEOFFSET"}
	}
	return &unsupported{pos: at, msg: "this expression is not supported in query files"}
}

func (lw *lowerer) column(c grammar.IFull_column_nameContext) expr {
	at := lw.pos(c)
	if c.DOLLAR() != nil {
		return &unsupported{pos: at, msg: "$IDENTITY and $ROWGUID are not supported in query files; name the column"}
	}
	out := &colRef{pos: at, name: ident(c.GetColumn_name())}
	switch {
	case c.INSERTED() != nil:
		out.table = "inserted"
	case c.DELETED() != nil:
		out.table = "deleted"
	case c.Full_table_name() != nil:
		t := lw.tableRef(c.Full_table_name())
		out.schema, out.table = t.schema, t.name
	}
	return out
}

// primitive reads a constant, NULL, a placeholder or DEFAULT; any other
// variable is unsupported.
func (lw *lowerer) primitive(p grammar.IPrimitive_expressionContext) expr {
	at := lw.pos(p)
	switch {
	case p.DEFAULT() != nil:
		return &defaultExpr{pos: at}
	case p.NULL_() != nil:
		return &literal{pos: at, kind: litNull, text: "NULL"}
	case p.LOCAL_ID() != nil:
		if lw.r.use(at.off) >= 0 {
			return &placeholder{pos: at}
		}
		return &unsupported{pos: at, msg: p.GetText() + " is not supported in query files"}
	}
	c := p.Primitive_constant()
	l := &literal{pos: at, text: c.GetText()}
	switch {
	case c.STRING() != nil:
		l.kind, l.text = litString, unquoteString(c.GetText())
	case c.BINARY() != nil:
		l.kind = litBinary
	case c.DOLLAR() != nil:
		l.kind = litMoney
	case c.DECIMAL() != nil:
		l.kind = litInteger
	case c.FLOAT() != nil:
		l.kind = litNumeric
	case c.REAL() != nil:
		l.kind = litFloat
	default:
		return &unsupported{pos: at, msg: "? is not a parameter; name it with @name or sqlc.arg(name)"}
	}
	return l
}

func (lw *lowerer) caseExpr(c grammar.ICase_expressionContext) expr {
	out := &caseExpr{pos: lw.pos(c)}
	if s := c.GetCaseExpr(); s != nil {
		out.subject = lw.expr(s)
		for _, w := range c.AllSwitch_section() {
			out.whens = append(out.whens, lw.expr(w.Expression(0)))
			out.thens = append(out.thens, lw.expr(w.Expression(1)))
		}
	} else {
		for _, w := range c.AllSwitch_search_condition_section() {
			out.whens = append(out.whens, lw.cond(w.Search_condition()))
			out.thens = append(out.thens, lw.expr(w.Expression()))
		}
	}
	if e := c.GetElseExpr(); e != nil {
		out.els = lw.expr(e)
	}
	return out
}

func (lw *lowerer) over(o grammar.IOver_clauseContext) *window {
	if o == nil {
		return nil
	}
	return &window{pos: lw.pos(o), partition: lw.exprs(o.Expression_list_()), order: lw.order(o.Order_by_clause())}
}

// call reads a function call.
func (lw *lowerer) call(f grammar.IFunction_callContext) expr {
	at := lw.pos(f)
	switch f := f.(type) {
	case *grammar.RANKING_WINDOWED_FUNCContext:
		r := f.Ranking_windowed_function()
		out := &call{pos: at, name: strings.ToLower(r.GetStart().GetText()), over: lw.over(r.Over_clause())}
		if e := r.Expression(); e != nil {
			out.args = []expr{lw.expr(e)}
		}
		return out
	case *grammar.AGGREGATE_WINDOWED_FUNCContext:
		a := f.Aggregate_windowed_function()
		out := &call{pos: at, name: strings.ToLower(a.GetStart().GetText()), over: lw.over(a.Over_clause()), star: a.STAR() != nil}
		if d := a.All_distinct_expression(); d != nil {
			out.distinct = d.DISTINCT() != nil
			out.args = []expr{lw.expr(d.Expression())}
		} else if !out.star {
			return &unsupported{pos: at, msg: "function " + out.name + " is not supported in query files"}
		}
		return out
	case *grammar.ANALYTIC_WINDOWED_FUNCContext:
		a := f.Analytic_windowed_function()
		out := &call{pos: at, name: strings.ToLower(a.GetStart().GetText())}
		if a.WITHIN() != nil {
			return &unsupported{pos: at, msg: "function " + out.name + " is not supported in query files"}
		}
		for _, e := range a.AllExpression() {
			out.args = append(out.args, lw.expr(e))
		}
		if o := a.Over_clause(); o != nil {
			out.over = lw.over(o)
		} else {
			out.over = &window{pos: lw.tokenPos(a.OVER()), partition: lw.exprs(a.Expression_list_()), order: lw.order(a.Order_by_clause())}
		}
		return out
	case *grammar.BUILT_IN_FUNCContext:
		return lw.builtin(f.Built_in_functions())
	case *grammar.SCALAR_FUNCTIONContext:
		n := f.Scalar_function_name()
		out := &call{pos: at, args: lw.exprs(f.Expression_list_())}
		// LEFT, RIGHT, CHECKSUM and BINARY_CHECKSUM, which this form also
		// names, parse as built-in functions first.
		p := n.Func_proc_name_server_database_schema()
		if p.GetServer() != nil || p.Func_proc_name_database_schema().GetDatabase() != nil {
			return &unsupported{pos: at, msg: "a function of another database or server is not supported in query files"}
		}
		s := p.Func_proc_name_database_schema().Func_proc_name_schema()
		out.schema, out.name = ident(s.GetSchema()), ident(s.GetProcedure())
		if out.schema == "" {
			out.name = strings.ToLower(out.name)
		}
		return out
	}
	return &unsupported{pos: at, msg: "this function is not supported in query files"}
}

// dateParts are the date functions whose first argument is a date part.
var dateParts = map[string]bool{"dateadd": true, "datediff": true, "datediff_big": true, "datename": true, "datepart": true, "datetrunc": true, "date_bucket": true}

// builtin reads a call of a function the grammar names: CAST, CONVERT and
// TRY_CAST take a type, IIF a condition, a date function a date part,
// TRIM the characters FROM, and STRING_AGG an order; for the rest, the
// arguments are the expressions among the call's children.
func (lw *lowerer) builtin(b grammar.IBuilt_in_functionsContext) expr {
	at := lw.pos(b)
	out := &call{pos: at, name: strings.ToLower(b.GetStart().GetText())}
	switch b := b.(type) {
	case *grammar.CASTContext:
		return &cast{pos: at, x: lw.expr(b.Expression()), typ: dataTypeOf(b.Data_type())}
	case *grammar.TRY_CASTContext:
		return &cast{pos: at, x: lw.expr(b.Expression()), typ: dataTypeOf(b.Data_type()), try: true}
	case *grammar.CONVERTContext:
		if b.GetStyle() != nil {
			return &unsupported{pos: at, msg: "CONVERT with a style is not supported in query files; use CAST, or FORMAT for text"}
		}
		return &cast{pos: at, x: lw.expr(b.GetConvert_expression()), typ: dataTypeOf(b.Data_type())}
	case *grammar.IIFContext:
		out.args = []expr{lw.cond(b.Search_condition()), lw.expr(b.GetLeft()), lw.expr(b.GetRight())}
		return out
	case *grammar.TRIMContext:
		if b.FROM() != nil {
			out.args = []expr{lw.expr(b.GetString_()), lw.expr(b.GetCharacters())}
		} else {
			out.args = []expr{lw.expr(b.GetString_())}
		}
		return out
	case *grammar.STRINGAGGContext:
		out.args = []expr{lw.expr(b.GetExpr()), lw.expr(b.GetSeparator())}
		out.order = lw.order(b.Order_by_clause())
		return out
	}
	if out.name == "next" {
		return &unsupported{pos: at, msg: "NEXT VALUE FOR is not supported in query files"}
	}
	for _, k := range b.GetChildren() {
		switch k := k.(type) {
		case grammar.IExpressionContext:
			out.args = append(out.args, lw.expr(k))
		case grammar.IExpression_list_Context:
			out.args = append(out.args, lw.exprs(k)...)
		case antlr.TerminalNode:
		case antlr.ParserRuleContext:
			if !dateParts[out.name] || out.part != "" {
				return &unsupported{pos: at, msg: "function " + out.name + " is not supported in query files"}
			}
			out.part = strings.ToLower(k.GetText())
		}
	}
	return out
}

// dataTypeOf reads a type a cast names.
func dataTypeOf(d grammar.IData_typeContext) dataType {
	if d.GetDouble_prec() != nil {
		return dataType{name: "float"}
	}
	var out dataType
	switch {
	case d.GetScaled() != nil:
		out.name, out.args = strings.ToLower(unquote(d.GetScaled().GetText())), []int{-1}
		return out
	case d.GetExt_type() != nil:
		out.name = strings.ToLower(ident(d.GetExt_type()))
	default:
		out.name = strings.ToLower(ident(d.GetUnscaled_type()))
	}
	for _, n := range d.AllDECIMAL() {
		v, _ := strconv.Atoi(n.GetText())
		out.args = append(out.args, v)
	}
	if d.IDENTITY() != nil {
		out.name = "identity"
	}
	return out
}
