package mssqlsql

import (
	"slices"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"github.com/thec1oud/dql/querysources/sqlserver/grammar"
)

// Expr is an expression of a check, in the forms the recognizer reads;
// anything else is Other.
type Expr interface{ expr() }

// Col is an unqualified column reference.
type Col struct{ Name string }

// Lit is a literal: a string's value unquoted, a number's text, or NULL.
type Lit struct {
	Kind LitKind
	Text string
}

// LitKind tells literals apart.
type LitKind int

// Kinds of literal.
const (
	LitString LitKind = iota + 1
	LitNumber
	LitNull
)

// Call is a function call, its name lowercased.
type Call struct {
	Name string
	Args []Expr
}

// Bin is a binary operation, its operator uppercased: = <> < <= > >= AND
// OR, or an arithmetic one as written.
type Bin struct {
	Op   string
	L, R Expr
}

// Between is x [NOT] BETWEEN lo AND hi.
type Between struct {
	X, Lo, Hi Expr
	Not       bool
}

// In is x [NOT] IN (items).
type In struct {
	X     Expr
	Items []Expr
	Not   bool
}

// Is is x IS [NOT] NULL.
type Is struct {
	X   Expr
	Not bool
}

// Not is NOT x.
type Not struct{ X Expr }

// Neg is a unary minus or plus.
type Neg struct {
	Op string
	X  Expr
}

// Other is any other expression, as written, with the unqualified columns
// it reads.
type Other struct {
	Text string
	Cols []string
}

func (Col) expr()     {}
func (Lit) expr()     {}
func (Call) expr()    {}
func (Bin) expr()     {}
func (Between) expr() {}
func (In) expr()      {}
func (Is) expr()      {}
func (Not) expr()     {}
func (Neg) expr()     {}
func (Other) expr()   {}

// condition converts a search condition.
func (p *parsed) condition(n grammar.ISearch_conditionContext) Expr {
	var x Expr
	switch {
	case n.AND() != nil:
		return Bin{Op: "AND", L: p.condition(n.Search_condition(0)), R: p.condition(n.Search_condition(1))}
	case n.OR() != nil:
		return Bin{Op: "OR", L: p.condition(n.Search_condition(0)), R: p.condition(n.Search_condition(1))}
	case n.Predicate() != nil:
		x = p.predicate(n.Predicate())
	default:
		x = p.condition(n.Search_condition(0))
	}
	for range n.AllNOT() {
		x = Not{X: x}
	}
	return x
}

// comparisons spell each comparison operator one way; !> and !< are <=
// and >=.
var comparisons = map[string]string{"=": "=", "<>": "<>", "!=": "<>", "<": "<", "<=": "<=", ">": ">", ">=": ">=", "!>": "<=", "!<": ">="}

// predicate converts a predicate. A subquery, LIKE or a full-text
// predicate is Other.
func (p *parsed) predicate(n grammar.IPredicateContext) Expr {
	not := len(n.AllNOT()) == 1
	switch {
	case n.Comparison_operator() != nil && n.LR_BRACKET() == nil:
		return Bin{Op: comparisons[n.Comparison_operator().GetText()], L: p.expr(n.Expression(0)), R: p.expr(n.Expression(1))}
	case n.BETWEEN() != nil && len(n.AllNOT()) < 2:
		return Between{X: p.expr(n.Expression(0)), Lo: p.expr(n.Expression(1)), Hi: p.expr(n.Expression(2)), Not: not}
	case n.IN() != nil && n.Expression_list_() != nil && len(n.AllNOT()) < 2:
		in := In{X: p.expr(n.Expression(0)), Not: not}
		for _, e := range n.Expression_list_().AllExpression() {
			in.Items = append(in.Items, p.expr(e))
		}
		return in
	case n.IS() != nil:
		return Is{X: p.expr(n.Expression(0)), Not: n.Null_notnull().NOT() != nil}
	}
	return p.other(n)
}

// expr converts an expression.
func (p *parsed) expr(n grammar.IExpressionContext) Expr {
	switch {
	case n.Primitive_expression() != nil:
		return p.primitive(n.Primitive_expression())
	case n.Full_column_name() != nil:
		c := n.Full_column_name()
		if c.DOT() != nil || c.Id_() == nil {
			break
		}
		return Col{Name: Unquote(c.Id_().GetText())}
	case n.Bracket_expression() != nil:
		if b := n.Bracket_expression(); b.Expression() != nil {
			return p.expr(b.Expression())
		}
	case n.Unary_operator_expression() != nil:
		u := n.Unary_operator_expression()
		if u.BIT_NOT() == nil {
			return Neg{Op: u.GetOp().GetText(), X: p.expr(u.Expression())}
		}
	case n.Function_call() != nil:
		return p.call(n.Function_call())
	case len(n.AllExpression()) == 2 && n.COLLATE() == nil && n.DOT() == nil && n.Time_zone() == nil:
		return Bin{Op: n.GetOp().GetText(), L: p.expr(n.Expression(0)), R: p.expr(n.Expression(1))}
	}
	return p.other(n)
}

// primitive converts a constant or NULL; a variable or DEFAULT is Other.
func (p *parsed) primitive(n grammar.IPrimitive_expressionContext) Expr {
	if n.NULL_() != nil {
		return Lit{Kind: LitNull, Text: "NULL"}
	}
	if c := n.Primitive_constant(); c != nil {
		switch {
		case c.STRING() != nil:
			return Lit{Kind: LitString, Text: unquoteString(c.GetText())}
		case c.DECIMAL() != nil || c.REAL() != nil || c.FLOAT() != nil:
			if c.DOLLAR() == nil {
				return Lit{Kind: LitNumber, Text: c.GetText()}
			}
		}
	}
	return p.other(n)
}

// call converts a function call: a built-in's arguments are the
// expressions among its children, and another function's its argument
// list. A windowed or full-text call is Other.
func (p *parsed) call(n grammar.IFunction_callContext) Expr {
	switch f := n.(type) {
	case *grammar.BUILT_IN_FUNCContext:
		b := f.Built_in_functions()
		c := Call{Name: strings.ToLower(b.GetStart().GetText())}
		for _, k := range b.GetChildren() {
			if e, ok := k.(grammar.IExpressionContext); ok {
				c.Args = append(c.Args, p.expr(e))
			} else if l, ok := k.(grammar.IExpression_list_Context); ok {
				for _, e := range l.AllExpression() {
					c.Args = append(c.Args, p.expr(e))
				}
			}
		}
		return c
	case *grammar.SCALAR_FUNCTIONContext:
		c := Call{Name: strings.ToLower(dotted(f.Scalar_function_name()))}
		if l := f.Expression_list_(); l != nil {
			for _, e := range l.AllExpression() {
				c.Args = append(c.Args, p.expr(e))
			}
		}
		return c
	}
	return p.other(n.(antlr.ParserRuleContext))
}

// dotted returns a multipart name's parts, each unquoted, joined by dots.
func dotted(t antlr.Tree) string {
	if n, ok := t.(antlr.TerminalNode); ok {
		return Unquote(n.GetText())
	}
	var b strings.Builder
	for _, k := range t.GetChildren() {
		b.WriteString(dotted(k))
	}
	return b.String()
}

// other is an expression the recognizer does not read.
func (p *parsed) other(n antlr.ParserRuleContext) Other {
	o := Other{Text: p.text(n)}
	var walk func(t antlr.Tree)
	walk = func(t antlr.Tree) {
		if c, ok := t.(*grammar.Full_column_nameContext); ok && c.DOT() == nil && c.Id_() != nil {
			if name := Unquote(c.Id_().GetText()); !slices.Contains(o.Cols, name) {
				o.Cols = append(o.Cols, name)
			}
		}
		for _, k := range t.GetChildren() {
			walk(k)
		}
	}
	walk(n)
	return o
}

// References lists the columns an expression reads, each once, in order
// of appearance.
func References(e Expr) []string {
	var out []string
	var walk func(e Expr)
	walk = func(e Expr) {
		switch e := e.(type) {
		case Col:
			if !slices.Contains(out, e.Name) {
				out = append(out, e.Name)
			}
		case Call:
			for _, a := range e.Args {
				walk(a)
			}
		case Bin:
			walk(e.L)
			walk(e.R)
		case Between:
			walk(e.X)
			walk(e.Lo)
			walk(e.Hi)
		case In:
			walk(e.X)
			for _, i := range e.Items {
				walk(i)
			}
		case Is:
			walk(e.X)
		case Not:
			walk(e.X)
		case Neg:
			walk(e.X)
		case Other:
			for _, c := range e.Cols {
				walk(Col{Name: c})
			}
		}
	}
	walk(e)
	return out
}
