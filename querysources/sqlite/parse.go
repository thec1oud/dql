package sqlite

import (
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"github.com/thec1oud/dql/querysources/sqlite/grammar"
)

// syntaxError is where the parser first failed: a byte offset of the
// statement, len(text) at its end.
type syntaxError struct{ off int }

func (e *syntaxError) Error() string { return "syntax error" }

// listener keeps the token of the first syntax error. Only the parser
// reports them: the lexer makes any character it cannot read a token of
// its own, UNEXPECTED_CHAR, which no rule takes.
type listener struct {
	*antlr.DefaultErrorListener
	token  antlr.Token
	failed bool
}

func (l *listener) SyntaxError(_ antlr.Recognizer, sym any, _, _ int, _ string, _ antlr.RecognitionException) {
	if !l.failed {
		l.failed, l.token = true, sym.(antlr.Token)
	}
}

// lowerer reads a tree into the analyzer's forms.
type lowerer struct {
	// The byte offset of each character of the statement.
	bytes []int
}

// parse parses text and returns its statements. A first pass predicts
// with SLL, which is fast and right for almost every statement; one it
// cannot parse is parsed again with full LL, whose errors are the
// grammar's.
func parse(text string) ([]stmt, error) {
	tree, l := parseWith(text, antlr.PredictionModeSLL)
	if l.failed {
		tree, l = parseWith(text, antlr.PredictionModeLL)
	}
	lw := &lowerer{}
	for i := range text {
		lw.bytes = append(lw.bytes, i)
	}
	lw.bytes = append(lw.bytes, len(text))
	if l.failed {
		return nil, &syntaxError{off: lw.errorOffset(text, l)}
	}
	var out []stmt
	for _, s := range tree.Sql_stmt_list().AllSql_stmt() {
		out = append(out, lw.stmt(s))
	}
	return out, nil
}

func parseWith(text string, mode int) (grammar.IParseContext, *listener) {
	l := &listener{DefaultErrorListener: antlr.NewDefaultErrorListener()}
	lx := grammar.NewSQLiteLexer(antlr.NewInputStream(text))
	lx.RemoveErrorListeners()
	lx.AddErrorListener(l)
	p := grammar.NewSQLiteParser(antlr.NewCommonTokenStream(lx, antlr.TokenDefaultChannel))
	p.RemoveErrorListeners()
	p.AddErrorListener(l)
	p.GetInterpreter().SetPredictionMode(mode)
	return p.Parse(), l
}

// errorOffset returns the byte offset of a syntax error's token.
func (lw *lowerer) errorOffset(text string, l *listener) int {
	if l.token.GetTokenType() == antlr.TokenEOF {
		return len(text)
	}
	return lw.bytes[l.token.GetStart()]
}

// off returns where ctx starts, as a byte offset.
func (lw *lowerer) off(ctx antlr.ParserRuleContext) int { return lw.bytes[ctx.GetStart().GetStart()] }

func (lw *lowerer) pos(ctx antlr.ParserRuleContext) pos { return pos{off: lw.off(ctx)} }

// unquote returns an identifier's stored name: "a""b", `a`, [a] and 'a'
// lose their quotes, as SQLite reads them.
func unquote(s string) string {
	switch q := s[0]; q {
	case '"', '`', '\'':
		return strings.ReplaceAll(s[1:len(s)-1], string(q)+string(q), string(q))
	case '[':
		return s[1 : len(s)-1]
	}
	return s
}

func ident(ctx antlr.ParserRuleContext) string {
	if ctx == nil {
		return ""
	}
	return unquote(ctx.GetText())
}
