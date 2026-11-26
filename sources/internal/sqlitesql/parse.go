// Package sqlitesql reads the SQL text SQLite keeps in sqlite_schema, with
// the ANTLR grammar of querysources/sqlite: the parts of a table's
// definition its pragmas do not report (checks, constraint names, column
// collations and the comments that carry annotations), the tables a view
// reads, and declared types and defaults as the IR models them.
package sqlitesql

import (
	"fmt"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"github.com/thec1oud/dql/querysources/sqlite/grammar"
)

// parsed is one statement's parse: its tree, and its tokens, comments
// included, for reading comments by position.
type parsed struct {
	tokens *antlr.CommonTokenStream
	input  antlr.CharStream
	stmt   grammar.ISql_stmtContext
}

// errors collects syntax errors.
type errors struct {
	*antlr.DefaultErrorListener
	msgs []string
}

func (e *errors) SyntaxError(_ antlr.Recognizer, _ any, line, column int, msg string, _ antlr.RecognitionException) {
	e.msgs = append(e.msgs, fmt.Sprintf("%d:%d: %s", line, column, msg))
}

// parse parses text as one statement.
func parse(text string) (*parsed, error) {
	in := antlr.NewInputStream(text)
	lx := grammar.NewSQLiteLexer(in)
	errs := &errors{DefaultErrorListener: antlr.NewDefaultErrorListener()}
	lx.RemoveErrorListeners()
	lx.AddErrorListener(errs)
	ts := antlr.NewCommonTokenStream(lx, antlr.TokenDefaultChannel)
	p := grammar.NewSQLiteParser(ts)
	p.RemoveErrorListeners()
	p.AddErrorListener(errs)
	p.GetInterpreter().SetPredictionMode(antlr.PredictionModeSLL)
	tree := p.Parse()
	if len(errs.msgs) > 0 {
		return nil, fmt.Errorf("%q does not parse: %s", text, strings.Join(errs.msgs, "; "))
	}
	stmts := tree.Sql_stmt_list().AllSql_stmt()
	if len(stmts) != 1 {
		return nil, fmt.Errorf("%q is not one statement", text)
	}
	ts.Fill()
	return &parsed{tokens: ts, input: in, stmt: stmts[0]}, nil
}

// text returns the source text of ctx, as written.
func (p *parsed) text(ctx antlr.ParserRuleContext) string {
	return p.input.GetTextFromInterval(antlr.NewInterval(ctx.GetStart().GetStart(), ctx.GetStop().GetStop()))
}

// comments returns the comments among tokens from through to, inclusive,
// each without its markers and trimmed, joined by newlines.
func (p *parsed) comments(from, to int) string {
	var out []string
	for _, t := range p.tokens.GetAllTokens()[from : to+1] {
		var body string
		switch t.GetTokenType() {
		case grammar.SQLiteLexerSINGLE_LINE_COMMENT:
			body = strings.TrimPrefix(t.GetText(), "--")
		case grammar.SQLiteLexerMULTILINE_COMMENT:
			body = strings.TrimSuffix(strings.TrimPrefix(t.GetText(), "/*"), "*/")
		default:
			continue
		}
		if body = strings.TrimSpace(body); body != "" {
			out = append(out, body)
		}
	}
	return strings.Join(out, "\n")
}

// trailingEnd returns the index of the last token that belongs with token
// i, a definition's last: a comma after it, and the comments on the line
// it, or that comma, ends.
func (p *parsed) trailingEnd(i int) int {
	all := p.tokens.GetAllTokens()
	line, end, comma := all[i].GetLine(), i, false
	for j := i + 1; all[j].GetTokenType() != antlr.TokenEOF; j++ {
		t := all[j]
		if t.GetChannel() == antlr.TokenDefaultChannel {
			if comma || t.GetTokenType() != grammar.SQLiteLexerCOMMA {
				break
			}
			comma, end, line = true, j, t.GetLine()
			continue
		}
		if t.GetLine() > line {
			break
		}
		end = j
		if t.GetTokenType() == grammar.SQLiteLexerSINGLE_LINE_COMMENT {
			break
		}
	}
	return end
}

// Unquote returns an identifier's stored name: "a""b", `a`, [a] and 'a'
// lose their quotes, as SQLite reads them.
func Unquote(s string) string {
	if len(s) < 2 {
		return s
	}
	switch q := s[0]; q {
	case '"', '`', '\'':
		if s[len(s)-1] == q {
			return strings.ReplaceAll(s[1:len(s)-1], string(q)+string(q), string(q))
		}
	case '[':
		if s[len(s)-1] == ']' {
			return s[1 : len(s)-1]
		}
	}
	return s
}
