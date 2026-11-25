package gen

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// TypeName turns words into a TypeScript type name (§4.2, §15.2): each word
// with its first letter upper-cased, with no initialisms, as the record's
// TS column shows. [display name] is DisplayName, [user id] UserId,
// [http server log] HttpServerLog. A name that would not start with a letter,
// such as one whose first word starts with a digit, is prefixed with X.
func TypeName(words []string) string {
	var b strings.Builder
	for _, w := range words {
		b.WriteString(capitalize(w))
	}
	s := b.String()
	if r, _ := utf8.DecodeRuneInString(s); !unicode.IsLetter(r) {
		s = "X" + s
	}
	return s
}

// MemberName turns words into a TypeScript property or function name: the
// first word as it is, the rest capitalized. [display name] is displayName,
// [user id] userId, [v2 items] v2Items. A name that would not start with a
// letter is prefixed with x. Property names may be reserved words, so
// MemberName does not escape them; see LocalName.
func MemberName(words []string) string {
	var b strings.Builder
	for i, w := range words {
		if i == 0 {
			b.WriteString(w)
		} else {
			b.WriteString(capitalize(w))
		}
	}
	s := b.String()
	if r, _ := utf8.DecodeRuneInString(s); !unicode.IsLetter(r) {
		s = "x" + s
	}
	return s
}

// LocalName is MemberName for a parameter or local binding, where a reserved
// word is escaped with a trailing underscore (class_, default_).
func LocalName(words []string) string {
	s := MemberName(words)
	if reservedWords[s] {
		s += "_"
	}
	return s
}

// reservedWords are the words a binding may not take in an ES module, where
// strict mode applies, together with TypeScript's own.
var reservedWords = map[string]bool{
	"arguments": true, "await": true, "break": true, "case": true, "catch": true, "class": true,
	"const": true, "continue": true, "debugger": true, "default": true, "delete": true, "do": true,
	"else": true, "enum": true, "eval": true, "export": true, "extends": true, "false": true,
	"finally": true, "for": true, "function": true, "if": true, "implements": true, "import": true,
	"in": true, "instanceof": true, "interface": true, "let": true, "new": true, "null": true,
	"package": true, "private": true, "protected": true, "public": true, "return": true,
	"static": true, "super": true, "switch": true, "this": true, "throw": true, "true": true,
	"try": true, "typeof": true, "undefined": true, "var": true, "void": true, "while": true,
	"with": true, "yield": true,
}

// capitalize spells one word with its first letter upper-cased. Case
// mapping is Unicode's, which does not depend on the locale (§3.2).
func capitalize(w string) string {
	r, n := utf8.DecodeRuneInString(w)
	if n == 0 {
		return ""
	}
	return string(unicode.ToUpper(r)) + w[n:]
}
