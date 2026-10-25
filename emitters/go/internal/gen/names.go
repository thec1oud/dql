package gen

import (
	"go/token"
	"go/types"
	"strings"
	"unicode"
	"unicode/utf8"
)

// initialisms are words Go spells in upper case (§4.2: initialisms are an
// emitter rule), as Go's own naming conventions list them.
var initialisms = map[string]bool{
	"acl": true, "api": true, "ascii": true, "cpu": true, "css": true, "dns": true,
	"eof": true, "guid": true, "html": true, "http": true, "https": true, "id": true,
	"ip": true, "json": true, "lhs": true, "qps": true, "ram": true, "rhs": true,
	"rpc": true, "sla": true, "smtp": true, "sql": true, "ssh": true, "tcp": true,
	"tls": true, "ttl": true, "udp": true, "ui": true, "uid": true, "uuid": true,
	"uri": true, "url": true, "utf8": true, "vm": true, "xml": true, "xmpp": true,
	"xsrf": true, "xss": true,
}

// Exported turns words into an exported Go name (§4.2, §15.2): each word
// capitalized, an initialism in upper case. [display name] is DisplayName,
// [user id] UserID, [v2 items] V2Items. A name that would not start with an
// upper-case letter, such as one whose first word starts with a digit, is
// prefixed with X so that it stays an exported identifier.
func Exported(words []string) string {
	var b strings.Builder
	for _, w := range words {
		b.WriteString(capitalize(w))
	}
	s := b.String()
	if r, _ := utf8.DecodeRuneInString(s); !unicode.IsUpper(r) {
		s = "X" + s
	}
	return s
}

// Unexported turns words into an unexported Go name, for parameters and
// locals: the first word as it is, the rest as in Exported. [password hash]
// is passwordHash, [user id] userID, [id] id. A Go keyword or predeclared
// name is escaped with a trailing underscore (type_, string_), and a name
// that would not start with a letter is prefixed with x.
func Unexported(words []string) string {
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
	if token.IsKeyword(s) || types.Universe.Lookup(s) != nil {
		s += "_"
	}
	return s
}

// capitalize spells one word: an initialism in upper case, any other word
// with its first letter upper-cased. Case mapping is Unicode's, which does
// not depend on the locale (§3.2).
func capitalize(w string) string {
	if initialisms[w] {
		return strings.ToUpper(w)
	}
	r, n := utf8.DecodeRuneInString(w)
	return string(unicode.ToUpper(r)) + w[n:]
}

// receiver is the receiver name of a type's methods: the first letter of its
// last word, as in (f UserFull), (d UserDraft) and (s PostStatus).
func receiver(words []string) string {
	if len(words) == 0 {
		return "v"
	}
	return Unexported([]string{strings.ToLower(firstLetter(words[len(words)-1]))})
}

func firstLetter(w string) string {
	for _, r := range w {
		if unicode.IsLetter(r) {
			return string(r)
		}
	}
	return "v"
}
