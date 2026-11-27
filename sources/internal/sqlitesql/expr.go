package sqlitesql

import (
	"slices"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"github.com/thec1oud/dql/querysources/sqlite/grammar"
)

// Expr is an expression of a check, in the forms the recognizer reads;
// anything else is Other.
type Expr interface{ expr() }

// Col is a column reference, qualified by Table or not.
type Col struct{ Table, Name string }

// Lit is a literal: a string's value unquoted, a number's text, or NULL,
// TRUE, FALSE or a CURRENT_ keyword as written.
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
	LitBool
	LitOther
)

// Call is a function call, its name lowercased.
type Call struct {
	Name string
	Args []Expr
}

// Bin is a binary operation, its operator uppercased: = <> < <= > >= AND
// OR, or another as written.
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

// Not is NOT x.
type Not struct{ X Expr }

// Neg is a unary minus or plus.
type Neg struct {
	Op string
	X  Expr
}

// Other is any other expression, as written.
type Other struct{ Text string }

func (Col) expr()     {}
func (Lit) expr()     {}
func (Call) expr()    {}
func (Bin) expr()     {}
func (Between) expr() {}
func (In) expr()      {}
func (Not) expr()     {}
func (Neg) expr()     {}
func (Other) expr()   {}

// expr converts an expression's tree.
func (p *parsed) expr(e grammar.IExprContext) Expr {
	return p.node(e.Expr_or())
}

// node converts one level of the layered expression grammar.
func (p *parsed) node(t antlr.ParserRuleContext) Expr {
	switch n := t.(type) {
	case *grammar.Expr_orContext:
		return p.chain(n)
	case *grammar.Expr_andContext:
		return p.chain(n)
	case *grammar.Expr_notContext:
		x := p.node(n.Expr_binary())
		for range n.AllNOT_() {
			x = Not{X: x}
		}
		return x
	case *grammar.Expr_binaryContext:
		return p.binary(n)
	case *grammar.Expr_comparisonContext, *grammar.Expr_bitwiseContext, *grammar.Expr_additionContext,
		*grammar.Expr_multiplicationContext, *grammar.Expr_stringContext:
		return p.chain(n)
	case *grammar.Expr_collateContext:
		if n.COLLATE_(0) != nil {
			return Other{Text: p.text(n)}
		}
		return p.node(n.Expr_unary())
	case *grammar.Expr_unaryContext:
		x := p.node(n.Expr_base())
		kids := n.GetChildren()
		for i := len(kids) - 2; i >= 0; i-- {
			x = Neg{Op: kids[i].(antlr.TerminalNode).GetText(), X: x}
		}
		return x
	}
	return p.base(t.(*grammar.Expr_baseContext))
}

// chain folds a level of operands joined by operators, left to right.
func (p *parsed) chain(t antlr.ParserRuleContext) Expr {
	kids := t.GetChildren()
	x := p.node(kids[0].(antlr.ParserRuleContext))
	for i := 1; i+1 < len(kids); i += 2 {
		// Equality and IS are expr_binary's, so no operator here has
		// another spelling to normalize.
		op := strings.ToUpper(kids[i].(antlr.TerminalNode).GetText())
		x = Bin{Op: op, L: x, R: p.node(kids[i+1].(antlr.ParserRuleContext))}
	}
	return x
}

// binary reads expr_binary: an operand and one postfix form after it.
// Anything longer, or with a subquery or a table, is Other.
func (p *parsed) binary(n *grammar.Expr_binaryContext) Expr {
	kids := n.GetChildren()
	x := p.node(kids[0].(antlr.ParserRuleContext))
	if len(kids) == 1 {
		return x
	}
	var ops []string
	var operands []Expr
	other := false
	for _, k := range kids[1:] {
		switch k := k.(type) {
		case antlr.TerminalNode:
			ops = append(ops, strings.ToUpper(k.GetText()))
		case *grammar.Expr_comparisonContext:
			operands = append(operands, p.node(k))
		default:
			other = true
		}
	}
	head := strings.Join(ops, " ")
	null := Lit{Kind: LitNull, Text: "NULL"}
	list := func(not string) string {
		return strings.TrimSpace(not + " IN ( " + strings.Repeat(", ", max(len(operands)-1, 0)) + ")")
	}
	switch {
	case other:
	case len(operands) == 1 && (head == "=" || head == "=="):
		return Bin{Op: "=", L: x, R: operands[0]}
	case len(operands) == 1 && (head == "<>" || head == "!="):
		return Bin{Op: "<>", L: x, R: operands[0]}
	case len(operands) == 1 && (head == "IS" || head == "IS NOT"):
		return Bin{Op: head, L: x, R: operands[0]}
	case len(operands) == 2 && (head == "BETWEEN AND" || head == "NOT BETWEEN AND"):
		return Between{X: x, Lo: operands[0], Hi: operands[1], Not: head != "BETWEEN AND"}
	case len(operands) > 0 && (head == list("") || head == list("NOT")):
		return In{X: x, Items: operands, Not: strings.HasPrefix(head, "NOT")}
	case head == "ISNULL":
		return Bin{Op: "IS", L: x, R: null}
	case head == "NOTNULL" || head == "NOT NULL":
		return Bin{Op: "IS NOT", L: x, R: null}
	}
	return Other{Text: p.text(n)}
}

// base reads an operand.
func (p *parsed) base(n *grammar.Expr_baseContext) Expr {
	switch {
	case n.Literal_value() != nil:
		return literal(n.Literal_value())
	case n.Column_name_excluding_string() != nil && n.DOT(0) == nil:
		return Col{Name: Unquote(n.Column_name_excluding_string().GetText())}
	case n.Column_name() != nil && n.Table_name() != nil && n.Schema_name() == nil:
		return Col{Table: Unquote(n.Table_name().GetText()), Name: Unquote(n.Column_name().GetText())}
	case n.Expr_recursive() != nil:
		r := n.Expr_recursive()
		switch {
		case r.Function_name() != nil && r.DISTINCT_() == nil && r.STAR() == nil && r.Order_clause() == nil &&
			r.Filter_clause() == nil && r.Over_clause() == nil && r.Percentile_clause() == nil:
			c := Call{Name: strings.ToLower(Unquote(r.Function_name().GetText()))}
			for _, a := range r.AllExpr() {
				c.Args = append(c.Args, p.expr(a))
			}
			return c
		case r.CAST_() == nil && r.CASE_() == nil && r.Function_name() == nil && len(r.AllExpr()) == 1:
			return p.expr(r.Expr(0))
		}
	}
	return Other{Text: p.text(n)}
}

// literal reads a literal value.
func literal(l grammar.ILiteral_valueContext) Lit {
	text := l.GetText()
	switch {
	case l.STRING_LITERAL() != nil:
		return Lit{Kind: LitString, Text: Unquote(text)}
	case l.NUMERIC_LITERAL() != nil:
		return Lit{Kind: LitNumber, Text: text}
	case l.NULL_() != nil:
		return Lit{Kind: LitNull, Text: "NULL"}
	case l.TRUE_() != nil || l.FALSE_() != nil:
		return Lit{Kind: LitBool, Text: strings.ToUpper(text)}
	}
	return Lit{Kind: LitOther, Text: text}
}

// References lists the unqualified columns an expression reads, each once,
// in order of appearance.
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
		case Not:
			walk(e.X)
		case Neg:
			walk(e.X)
		}
	}
	walk(e)
	return out
}

func sortStrings(s []string) { slices.Sort(s) }
