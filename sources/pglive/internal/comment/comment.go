// Package comment finds annotation tokens in Postgres comments. pg-live reads
// @key and @key(value) tokens (design record §7.1), such as
// '@write-only @sensitive' or '@logical-key(id)'; the text around them is
// prose and is ignored here. The comment itself is kept whole as an extension
// (§7.5 step 2), so a token records where it was found instead of copying
// the text.
//
// The rules:
//
//   - An @ begins a token only at the start of the comment or after
//     whitespace, so an address such as a@b.com in prose is not a token.
//   - A key is an ASCII letter followed by ASCII letters, digits, - and _,
//     in segments joined by single dots (x.go.type). A dot ends the key unless
//     a letter follows it, so '@public.' at the end of a sentence is the key
//     public. The key ends at the first other character, which is prose.
//   - An @ not followed by a key, such as a lone @ or @@, is prose.
//   - A value is written in parentheses directly after the key, with no space
//     between. It is everything up to the matching parenthesis, kept exactly:
//     nested parentheses balance, and commas, spaces and quotes have no
//     meaning here. Splitting and trimming belong to whoever interprets the
//     key.
//   - A value whose parenthesis is never closed runs to the end of the
//     comment and is marked Unclosed, so the caller can reject it instead of
//     reading a different annotation than the one written.
//   - Keys keep their case, and repeated keys are all returned, in order.
package comment

import (
	"unicode"
	"unicode/utf8"
)

// Token is one annotation token.
type Token struct {
	Key string
	// HasValue is set when the key is followed by parentheses; Value is what
	// they enclose, which may be empty.
	HasValue bool
	Value    string
	// Unclosed is set when the value's opening parenthesis has no match.
	Unclosed bool
	// Start and End are the token's byte offsets in the comment, End
	// exclusive: text[Start:End] is the token as written.
	Start, End int
}

// ParseComment returns the tokens of text in the order they appear.
func ParseComment(text string) []Token {
	var tokens []Token
	for i := 0; i < len(text); i++ {
		if text[i] != '@' || !atBoundary(text, i) {
			continue
		}
		end := keyEnd(text, i+1)
		if end == i+1 {
			continue
		}
		tok := Token{Key: text[i+1 : end], Start: i, End: end}
		if end < len(text) && text[end] == '(' {
			tok.HasValue = true
			tok.Value, tok.End, tok.Unclosed = value(text, end+1)
		}
		tokens = append(tokens, tok)
		i = tok.End - 1
	}
	return tokens
}

// atBoundary reports whether position i starts the text or follows
// whitespace.
func atBoundary(text string, i int) bool {
	r, _ := utf8.DecodeLastRuneInString(text[:i])
	return i == 0 || unicode.IsSpace(r)
}

// keyEnd returns the end of the key starting at i, or i if none starts there.
func keyEnd(text string, i int) int {
	if i == len(text) || !isLetter(text[i]) {
		return i
	}
	j := i + 1
	for j < len(text) {
		c := text[j]
		switch {
		case isLetter(c) || isDigit(c) || c == '-' || c == '_':
			j++
		case c == '.' && j+1 < len(text) && isLetter(text[j+1]):
			j += 2
		default:
			return j
		}
	}
	return j
}

// value reads a value starting after its opening parenthesis at i. It
// returns the value, the end of the token and whether the parenthesis was
// left unclosed.
func value(text string, i int) (string, int, bool) {
	depth := 1
	for j := i; j < len(text); j++ {
		switch text[j] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return text[i:j], j + 1, false
			}
		}
	}
	return text[i:], len(text), true
}

func isLetter(c byte) bool { return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' }

func isDigit(c byte) bool { return '0' <= c && c <= '9' }
