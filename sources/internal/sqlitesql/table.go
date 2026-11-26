package sqlitesql

import (
	"fmt"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"github.com/thec1oud/dql/querysources/sqlite/grammar"
)

// Table is what a CREATE TABLE statement says that SQLite's pragmas do not:
// constraint names, checks, collations, generation expressions, options and
// comments.
//
// A comment belongs to the definition it sits in, to the one whose line it
// ends (past its comma), or to the definition after it when it stands on
// lines of its own. A comment that ends the line of the opening
// parenthesis is the table's.
type Table struct {
	Comment      string
	Strict       bool
	WithoutRowid bool
	Columns      []Column
	// Constraints in order of declaration, a column's after the columns
	// before it and a table's after every column.
	Constraints []Constraint
}

// Column is a column definition.
type Column struct {
	Name string
	// Type is the declared type as written, its words single-spaced, or
	// empty.
	Type      string
	Comment   string
	Collation string
	// Generation is a generated column's expression as written; Stored
	// is set for a STORED one.
	Generation string
	Stored     bool
}

// Kinds of constraint.
const (
	PrimaryKey = "PRIMARY KEY"
	Unique     = "UNIQUE"
	Check      = "CHECK"
	ForeignKey = "FOREIGN KEY"
)

// Constraint is a constraint, on a column or on the table.
type Constraint struct {
	Kind string
	// Name is CONSTRAINT's name, or empty.
	Name    string
	Comment string
	// Columns are a key's columns, or a column constraint's column. An
	// expression element of a key is left out, and Expressions holds it.
	Columns     []string
	Expressions []string
	// Expr is a check's expression as written, and Parsed its tree.
	Expr   string
	Parsed Expr
	// RefTable and RefColumns are a foreign key's target. RefColumns is
	// empty when the key references the target's primary key.
	RefTable   string
	RefColumns []string
}

// ParseTable reads a CREATE TABLE statement as sqlite_schema stores it.
func ParseTable(sql string) (*Table, error) {
	p, err := parse(sql)
	if err != nil {
		return nil, err
	}
	ct := p.stmt.Create_table_stmt()
	if ct == nil || ct.OPEN_PAR() == nil {
		return nil, fmt.Errorf("%q is not a CREATE TABLE statement with columns", sql)
	}
	t := &Table{}
	open := ct.OPEN_PAR().GetSymbol().GetTokenIndex()
	from := p.trailingEnd(open)
	t.Comment = p.comments(open+1, from)
	// span gives a definition's comments and moves past them.
	span := func(def antlr.ParserRuleContext) string {
		to := p.trailingEnd(def.GetStop().GetTokenIndex())
		c := p.comments(from+1, to)
		from = to
		return c
	}
	if o := ct.Table_options(); o != nil {
		t.Strict = len(o.AllSTRICT_()) > 0
		t.WithoutRowid = len(o.AllWITHOUT_()) > 0
	}
	for _, cd := range ct.AllColumn_def() {
		col := Column{Name: Unquote(cd.Column_name().GetText()), Comment: span(cd)}
		if tn := cd.Type_name(); tn != nil {
			col.Type = strings.Join(strings.Fields(p.text(tn)), " ")
		}
		for _, cc := range cd.AllColumn_constraint() {
			c := Constraint{Columns: []string{col.Name}}
			if n := cc.Name(); n != nil {
				c.Name = Unquote(n.GetText())
			}
			switch {
			case cc.PRIMARY_() != nil:
				c.Kind = PrimaryKey
			case cc.UNIQUE_() != nil:
				c.Kind = Unique
			case cc.CHECK_() != nil:
				c.Kind = Check
				c.Expr = p.text(cc.Expr())
				c.Parsed = p.expr(cc.Expr())
			case cc.Foreign_key_clause() != nil:
				c.Kind = ForeignKey
				c.RefTable, c.RefColumns = reference(cc.Foreign_key_clause())
			case cc.COLLATE_() != nil:
				col.Collation = Unquote(cc.Collation_name().GetText())
				continue
			case cc.AS_() != nil:
				col.Generation = p.text(cc.Expr())
				col.Stored = cc.STORED_() != nil
				continue
			default:
				// NOT NULL, NULL, DEFAULT: the pragmas report them.
				continue
			}
			t.Constraints = append(t.Constraints, c)
		}
		t.Columns = append(t.Columns, col)
	}
	for _, tc := range ct.AllTable_constraint() {
		c := Constraint{Comment: span(tc)}
		if n := tc.Name(); n != nil {
			c.Name = Unquote(n.GetText())
		}
		switch {
		case tc.CHECK_() != nil:
			c.Kind = Check
			c.Expr = p.text(tc.Expr())
			c.Parsed = p.expr(tc.Expr())
			c.Columns = References(c.Parsed)
		case tc.FOREIGN_() != nil:
			c.Kind = ForeignKey
			for _, n := range tc.AllColumn_name() {
				c.Columns = append(c.Columns, Unquote(n.GetText()))
			}
			c.RefTable, c.RefColumns = reference(tc.Foreign_key_clause())
		default:
			c.Kind = Unique
			if tc.PRIMARY_() != nil {
				c.Kind = PrimaryKey
			}
			for _, ic := range tc.AllIndexed_column() {
				if col, ok := p.expr(ic.Expr()).(Col); ok && col.Table == "" {
					c.Columns = append(c.Columns, col.Name)
				} else {
					c.Expressions = append(c.Expressions, p.text(ic.Expr()))
				}
			}
		}
		t.Constraints = append(t.Constraints, c)
	}
	return t, nil
}

// reference reads a foreign key's target.
func reference(fk grammar.IForeign_key_clauseContext) (string, []string) {
	var cols []string
	for _, n := range fk.AllColumn_name() {
		cols = append(cols, Unquote(n.GetText()))
	}
	return Unquote(fk.Foreign_table().GetText()), cols
}

// ViewReads returns the tables and views a CREATE VIEW statement reads,
// each once, sorted, leaving out the names its common table expressions
// define.
func ViewReads(sql string) ([]string, error) {
	p, err := parse(sql)
	if err != nil {
		return nil, err
	}
	cv := p.stmt.Create_view_stmt()
	if cv == nil {
		return nil, fmt.Errorf("%q is not a CREATE VIEW statement", sql)
	}
	ctes := map[string]bool{}
	var names []string
	var walk func(t antlr.Tree)
	walk = func(t antlr.Tree) {
		switch n := t.(type) {
		case *grammar.Common_table_expressionContext:
			ctes[strings.ToLower(Unquote(n.Cte_table_name().Table_name().GetText()))] = true
		case *grammar.Table_or_subqueryContext:
			if n.Table_name() != nil {
				names = append(names, Unquote(n.Table_name().GetText()))
			}
		}
		for _, c := range t.GetChildren() {
			walk(c)
		}
	}
	walk(cv.Select_stmt())
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		if !ctes[strings.ToLower(n)] && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sortStrings(out)
	return out, nil
}

// Index is what a CREATE INDEX statement says that PRAGMA index_xinfo does
// not: its expression elements' text and its predicate.
type Index struct {
	// Expressions holds each element's expression as written, empty for a
	// plain column, in key order.
	Expressions []string
	// Where is a partial index's predicate as written.
	Where string
	// NullColumns are the columns the predicate requires to be NULL, by a
	// top-level conjunct c IS NULL; NotTrueColumns those it requires to be
	// false or NULL, by NOT c, c = FALSE, c IS FALSE or c IS NOT TRUE
	// (§21.3).
	NullColumns    []string
	NotTrueColumns []string
}

// ParseIndex reads a CREATE INDEX statement as sqlite_schema stores it.
func ParseIndex(sql string) (*Index, error) {
	p, err := parse(sql)
	if err != nil {
		return nil, err
	}
	ci := p.stmt.Create_index_stmt()
	if ci == nil {
		return nil, fmt.Errorf("%q is not a CREATE INDEX statement", sql)
	}
	x := &Index{}
	for _, ic := range ci.AllIndexed_column() {
		if c, ok := p.expr(ic.Expr()).(Col); ok && c.Table == "" {
			x.Expressions = append(x.Expressions, "")
		} else {
			x.Expressions = append(x.Expressions, p.text(ic.Expr()))
		}
	}
	if ci.WHERE_() != nil {
		x.Where = p.text(ci.Expr())
		var conjuncts func(e Expr)
		conjuncts = func(e Expr) {
			switch e := e.(type) {
			case Bin:
				switch {
				case e.Op == "AND":
					conjuncts(e.L)
					conjuncts(e.R)
				case e.Op == "IS" && isLit(e.R, LitNull, "NULL"):
					x.NullColumns = appendCol(x.NullColumns, e.L)
				case (e.Op == "=" || e.Op == "IS") && isLit(e.R, LitBool, "FALSE"), e.Op == "IS NOT" && isLit(e.R, LitBool, "TRUE"):
					x.NotTrueColumns = appendCol(x.NotTrueColumns, e.L)
				}
			case Not:
				x.NotTrueColumns = appendCol(x.NotTrueColumns, e.X)
			}
		}
		conjuncts(p.expr(ci.Expr()))
	}
	return x, nil
}

func isLit(e Expr, kind LitKind, text string) bool {
	l, ok := e.(Lit)
	return ok && l.Kind == kind && l.Text == text
}

func appendCol(cols []string, e Expr) []string {
	if c, ok := e.(Col); ok && c.Table == "" {
		return append(cols, c.Name)
	}
	return cols
}
