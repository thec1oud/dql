// Package mssqlsql reads the SQL text SQL Server keeps in its sys views:
// column types, defaults and the expressions of check constraints, with
// the ANTLR grammar of querysources/sqlserver, into their IR forms (design
// record §8, §10, §11.3).
package mssqlsql

import (
	"fmt"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"github.com/thec1oud/dql/querysources/sqlserver/grammar"
)

// errors collects syntax errors.
type errors struct {
	*antlr.DefaultErrorListener
	msgs []string
}

func (e *errors) SyntaxError(_ antlr.Recognizer, _ any, line, column int, msg string, _ antlr.RecognitionException) {
	e.msgs = append(e.msgs, fmt.Sprintf("%d:%d: %s", line, column, msg))
}

// parsed is one condition's parse.
type parsed struct {
	input antlr.CharStream
}

// ParseCondition parses a search condition, such as a check constraint's
// definition, into an Expr.
func ParseCondition(text string) (Expr, error) {
	in := antlr.NewInputStream(text)
	lx := grammar.NewTSqlLexer(in)
	errs := &errors{DefaultErrorListener: antlr.NewDefaultErrorListener()}
	lx.RemoveErrorListeners()
	lx.AddErrorListener(errs)
	ts := antlr.NewCommonTokenStream(lx, antlr.TokenDefaultChannel)
	p := grammar.NewTSqlParser(ts)
	p.RemoveErrorListeners()
	p.AddErrorListener(errs)
	tree := p.Search_condition()
	if len(errs.msgs) == 0 && ts.LA(1) != antlr.TokenEOF {
		errs.msgs = append(errs.msgs, "text follows the condition")
	}
	if len(errs.msgs) > 0 {
		return nil, fmt.Errorf("%q does not parse: %s", text, strings.Join(errs.msgs, "; "))
	}
	return (&parsed{input: in}).condition(tree), nil
}

// text returns the source text of ctx, as written.
func (p *parsed) text(ctx antlr.ParserRuleContext) string {
	return p.input.GetTextFromInterval(antlr.NewInterval(ctx.GetStart().GetStart(), ctx.GetStop().GetStop()))
}

// Unquote returns an identifier's stored name: [a]]b] and "a""b" lose their
// quotes, as SQL Server reads them.
func Unquote(s string) string {
	if len(s) >= 2 {
		switch {
		case s[0] == '[' && s[len(s)-1] == ']':
			return strings.ReplaceAll(s[1:len(s)-1], "]]", "]")
		case s[0] == '"' && s[len(s)-1] == '"':
			return strings.ReplaceAll(s[1:len(s)-1], `""`, `"`)
		}
	}
	return s
}

// unquoteString returns a string literal's value: 'a”b' and N'a”b' are a'b.
func unquoteString(s string) string {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "N"), "n")
	return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
}
