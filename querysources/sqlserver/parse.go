package sqlserver

import (
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"github.com/thec1oud/dql/querysources/sqlserver/grammar"
)

// syntaxError is where the parser first failed: a byte offset of the
// statement, len(text) at its end.
type syntaxError struct{ off int }

func (e *syntaxError) Error() string { return "syntax error" }

// listener keeps where the first syntax error is: the character index of
// the parser's offending token, or of the token the lexer could not read,
// and whether it is the end of the statement.
type listener struct {
	*antlr.DefaultErrorListener
	failed bool
	at     int
	eof    bool
}

func (l *listener) SyntaxError(r antlr.Recognizer, sym any, _, _ int, _ string, _ antlr.RecognitionException) {
	if l.failed {
		return
	}
	l.failed = true
	if t, ok := sym.(antlr.Token); ok {
		l.at, l.eof = t.GetStart(), t.GetTokenType() == antlr.TokenEOF
		return
	}
	// The lexer passes no token.
	l.at = r.(*antlr.BaseLexer).TokenStartCharIndex
}

// lowerer reads a tree into the analyzer's forms.
type lowerer struct {
	// The byte offset of each character of the statement.
	bytes []int
	// The placeholders' offsets in the statement, which a LOCAL_ID at one
	// is.
	r *rewritten
}

// parse parses text and returns its statements. A first pass predicts
// with SLL, which is fast and right for almost every statement; one it
// cannot parse is parsed again with full LL, whose errors are the
// grammar's. A query file leaves out the statement's closing semicolon,
// which MERGE must have, so a statement that ends too early is parsed
// once more with one.
func parse(r *rewritten) ([]stmt, error) {
	text := r.text
	tree, l := parseWith(text, antlr.PredictionModeSLL)
	if l.failed {
		tree, l = parseWith(text, antlr.PredictionModeLL)
	}
	if l.failed && l.eof {
		if t, sl := parseWith(text+";", antlr.PredictionModeLL); !sl.failed {
			tree, l = t, sl
		}
	}
	lw := &lowerer{r: r}
	for i := range text {
		lw.bytes = append(lw.bytes, i)
	}
	lw.bytes = append(lw.bytes, len(text))
	if l.failed {
		return nil, &syntaxError{off: lw.errorOffset(text, l)}
	}
	var out []stmt
	for _, b := range tree.AllBatch() {
		out = append(out, lw.batch(b)...)
	}
	return out, nil
}

func parseWith(text string, mode int) (grammar.ITsql_fileContext, *listener) {
	l := &listener{DefaultErrorListener: antlr.NewDefaultErrorListener()}
	lx := grammar.NewTSqlLexer(antlr.NewInputStream(text))
	lx.RemoveErrorListeners()
	lx.AddErrorListener(l)
	p := grammar.NewTSqlParser(antlr.NewCommonTokenStream(lx, antlr.TokenDefaultChannel))
	p.RemoveErrorListeners()
	p.AddErrorListener(l)
	p.GetInterpreter().SetPredictionMode(mode)
	return p.Tsql_file(), l
}

// errorOffset returns the byte offset of a syntax error.
func (lw *lowerer) errorOffset(text string, l *listener) int {
	if l.eof {
		return len(text)
	}
	return lw.bytes[l.at]
}

// off returns where ctx starts, as a byte offset.
func (lw *lowerer) off(ctx antlr.ParserRuleContext) int { return lw.bytes[ctx.GetStart().GetStart()] }

func (lw *lowerer) pos(ctx antlr.ParserRuleContext) pos { return pos{off: lw.off(ctx)} }

func (lw *lowerer) tokenPos(t antlr.TerminalNode) pos {
	return pos{off: lw.bytes[t.GetSymbol().GetStart()]}
}

// unquote returns an identifier's stored name: "a""b" and [a]]b] lose
// their quotes, as SQL Server reads them.
func unquote(s string) string {
	switch s[0] {
	case '"':
		return strings.ReplaceAll(s[1:len(s)-1], `""`, `"`)
	case '[':
		return strings.ReplaceAll(s[1:len(s)-1], "]]", "]")
	}
	return s
}

// ident returns the stored name of an identifier, or "" for none.
func ident(ctx grammar.IId_Context) string {
	if ctx == nil {
		return ""
	}
	return unquote(ctx.GetText())
}

// unquoteString returns a string literal's value: 'it”s' and N'x' lose
// their quotes and prefix.
func unquoteString(s string) string {
	if s[0] == 'N' || s[0] == 'n' {
		s = s[1:]
	}
	return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
}
