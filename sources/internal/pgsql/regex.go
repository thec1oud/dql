package pgsql

import (
	"strconv"
	"unicode/utf8"
)

// portableRegex reports whether a Postgres regular expression (an ARE, as the
// ~ operator reads it) lies inside the portable subset of §10.2 with the same
// meaning, so it can be emitted unchanged. Nothing is translated.
//
// The check is conservative and rejects whatever it is unsure of; core/constraints
// holds the authoritative validator of the subset. Beyond the subset's own
// exclusions it rejects:
//   - every letter or digit escape, \d, \w and \s included: Postgres matches
//     them by the database's locale or ICU (non-ASCII digits, letters and
//     spaces), while the subset's are ASCII-only;
//   - (? in any form, which covers lookaround, non-capturing groups and flags;
//   - empty groups, empty alternatives and empty or nested brackets, and a ]
//     or } outside the construct it closes, which hosts read differently;
//   - quantifiers on anchors, stacked quantifiers and bounds above 255
//     (Postgres's limit).
func portableRegex(re string) bool {
	if !utf8.ValidString(re) {
		return false
	}
	p := &reParser{src: []rune(re)}
	return p.alternation() && p.pos == len(p.src)
}

type reParser struct {
	src []rune
	pos int
}

func (p *reParser) peek() rune {
	if p.pos < len(p.src) {
		return p.src[p.pos]
	}
	return -1
}

// alternation reads branch ('|' branch)*, every branch non-empty.
func (p *reParser) alternation() bool {
	for {
		if !p.branch() {
			return false
		}
		if p.peek() != '|' {
			return true
		}
		p.pos++
	}
}

// branch reads one or more pieces, each an atom and an optional quantifier.
func (p *reParser) branch() bool {
	n := 0
	for {
		switch p.peek() {
		case -1, '|', ')':
			return n > 0
		}
		quantifiable, ok := p.atom()
		if !ok {
			return false
		}
		if p.quantifier() {
			if !quantifiable {
				return false
			}
			// A second quantifier is a lazy one (*?) or a stacked one (a**).
			if isQuantStart(p.peek()) {
				return false
			}
		} else if p.peek() == '{' {
			return false
		}
		n++
	}
}

func isQuantStart(r rune) bool {
	return r == '*' || r == '+' || r == '?' || r == '{'
}

// atom reads one atom and says whether a quantifier may follow it.
func (p *reParser) atom() (quantifiable, ok bool) {
	r := p.peek()
	p.pos++
	switch r {
	case '^', '$':
		return false, true
	case '.':
		return true, true
	case '(':
		if p.peek() == '?' || !p.alternation() || p.peek() != ')' {
			return false, false
		}
		p.pos++
		return true, true
	case '[':
		return true, p.bracket()
	case '\\':
		_, ok := p.escape()
		return true, ok
	case ')', ']', '}', '*', '+', '?', '{':
		return false, false
	}
	return true, true
}

// escape reads the character after a backslash, which must be ASCII
// punctuation: Postgres reads \ before any other character as an escape with
// its own meaning.
func (p *reParser) escape() (rune, bool) {
	r := p.peek()
	p.pos++
	ok := r > ' ' && r < 0x7f &&
		!(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
	return r, ok
}

// bracket reads a bracket class after its [: an optional ^, then literals and
// ranges, then ].
func (p *reParser) bracket() bool {
	if p.peek() == '^' {
		p.pos++
	}
	if p.peek() == ']' {
		return false
	}
	first := true
	for p.peek() != ']' {
		if p.peek() == '-' && !first && p.next() != ']' {
			return false
		}
		lo, ok := p.classChar()
		if !ok {
			return false
		}
		if p.peek() == '-' && p.next() != ']' {
			p.pos++
			hi, ok := p.classChar()
			if !ok || hi < lo {
				return false
			}
		}
		first = false
	}
	p.pos++
	return true
}

func (p *reParser) next() rune {
	if p.pos+1 < len(p.src) {
		return p.src[p.pos+1]
	}
	return -1
}

// classChar reads one character of a bracket class. A [ would start a POSIX
// class, collating element or equivalence class, or nest a class in some hosts.
func (p *reParser) classChar() (rune, bool) {
	r := p.peek()
	p.pos++
	switch r {
	case -1, '[':
		return 0, false
	case '\\':
		return p.escape()
	}
	return r, true
}

// quantifier reads an optional quantifier and says whether there was one.
// A { that does not start a valid bound leaves the position on it.
func (p *reParser) quantifier() bool {
	switch p.peek() {
	case '*', '+', '?':
		p.pos++
		return true
	case '{':
		start := p.pos
		p.pos++
		m, ok := p.number()
		if ok && p.peek() == ',' {
			p.pos++
			if p.peek() != '}' {
				var n int
				n, ok = p.number()
				ok = ok && m <= n
			}
		}
		if ok && p.peek() == '}' {
			p.pos++
			return true
		}
		p.pos = start
	}
	return false
}

// number reads a repetition bound of at most 255.
func (p *reParser) number() (int, bool) {
	start := p.pos
	for p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
		p.pos++
	}
	n, err := strconv.Atoi(string(p.src[start:p.pos]))
	return n, err == nil && n <= 255
}
