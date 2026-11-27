package constraints

import (
	"fmt"
	"slices"
	"strconv"
	"unicode/utf8"
)

// This file is a parser for the portable regex subset of §10.2. It is
// hand-written over code points so that it accepts exactly the subset, and
// nothing a particular regex engine happens to accept beyond it:
//
//   - literals, and \ before one of ^ $ \ . * + ? ( ) [ ] { } | / for the
//     character itself (and \- inside a bracket class);
//   - . (any code point, newline included, as in Postgres ~);
//   - bracket classes with ranges and negation;
//   - ^ and $ for the start and end of the whole string;
//   - greedy quantifiers * + ? {m} {m,} {m,n}, with counts up to 255;
//   - alternation and groups;
//   - \d \w \s, ASCII only.
//
// Everything else is rejected, including spellings that some engines read
// as literals and others reject or read differently: an unescaped { } or ]
// outside a bracket class, a ] first in a bracket class, and a - inside one
// that is neither first, last nor a range.

// maxRepeat is the largest repetition count, the limit of Postgres
// (RE_DUP_MAX) and POSIX.
const maxRepeat = 255

// PatternError reports a pattern outside the portable subset of §10.2.
type PatternError struct {
	// Construct is a stable kebab-case name of the offending construct, such
	// as "backreference" or "lazy-quantifier".
	Construct string
	// Pos is the construct's 0-based offset in code points.
	Pos int
	// Text is the construct as written.
	Text string
	// Reason says what is wrong and how to write it in the subset.
	Reason string
}

func (e *PatternError) Error() string {
	return fmt.Sprintf("pattern: %s %q at code point %d is not in the portable subset (§10.2): %s",
		e.Construct, e.Text, e.Pos, e.Reason)
}

// A code point range, inclusive.
type rng struct{ lo, hi rune }

type nodeKind uint8

const (
	// A code point in one of ranges: a literal, ., a class or a class escape.
	nodeSet nodeKind = iota
	nodeBegin
	nodeEnd
	nodeConcat
	nodeAlt
	nodeRepeat
)

type node struct {
	kind   nodeKind
	ranges []rng // nodeSet: sorted, disjoint, not adjacent
	subs   []*node
	// nodeRepeat: max < 0 is unbounded.
	min, max int
}

var (
	anyRanges   = []rng{{0, utf8.MaxRune}}
	digitRanges = []rng{{'0', '9'}}
	wordRanges  = []rng{{'0', '9'}, {'A', 'Z'}, {'_', '_'}, {'a', 'z'}}
	// The ASCII white space of Postgres's [[:space:]] in the C locale: tab,
	// newline, vertical tab, form feed, carriage return and space.
	spaceRanges = []rng{{'\t', '\r'}, {' ', ' '}}
)

type parser struct {
	src []rune
	pos int
}

// parse parses p into a syntax tree, or returns a *PatternError.
func parse(p string) (*node, error) {
	if !utf8.ValidString(p) {
		i := 0
		for ; i < len(p); i++ {
			if r, size := utf8.DecodeRuneInString(p[i:]); r == utf8.RuneError && size == 1 {
				break
			}
		}
		return nil, &PatternError{"invalid-utf8", utf8.RuneCountInString(p[:i]), p[i : i+1], "a pattern is text; encode it as UTF-8"}
	}
	ps := &parser{src: []rune(p)}
	n, err := ps.alternation()
	if err == nil && ps.pos < len(ps.src) {
		// alternation stops only at the end or at a ) it did not open.
		err = ps.fail("unmatched-paren", ps.pos, 1, "this ) closes no group; write \\) for a literal parenthesis")
	}
	return n, err
}

func (p *parser) fail(construct string, pos, n int, reason string) error {
	end := min(pos+n, len(p.src))
	return &PatternError{construct, pos, string(p.src[pos:end]), reason}
}

func (p *parser) peek(off int) rune {
	if i := p.pos + off; i < len(p.src) {
		return p.src[i]
	}
	return -1
}

func (p *parser) alternation() (*node, error) {
	var alts []*node
	for {
		s, err := p.sequence()
		if err != nil {
			return nil, err
		}
		alts = append(alts, s)
		if p.peek(0) != '|' {
			break
		}
		p.pos++
	}
	if len(alts) == 1 {
		return alts[0], nil
	}
	return &node{kind: nodeAlt, subs: alts}, nil
}

func (p *parser) sequence() (*node, error) {
	seq := &node{kind: nodeConcat}
	for p.pos < len(p.src) && p.src[p.pos] != '|' && p.src[p.pos] != ')' {
		a, err := p.atom()
		if err == nil {
			a, err = p.quantified(a)
		}
		if err != nil {
			return nil, err
		}
		seq.subs = append(seq.subs, a)
	}
	return seq, nil
}

func (p *parser) atom() (*node, error) {
	start := p.pos
	c := p.src[p.pos]
	switch c {
	case '(':
		if p.peek(1) == '?' {
			return nil, p.extension()
		}
		p.pos++
		inner, err := p.alternation()
		if err != nil {
			return nil, err
		}
		if p.pos >= len(p.src) {
			return nil, p.fail("unclosed-group", start, 1, "this ( is never closed; add the missing )")
		}
		p.pos++
		return inner, nil
	case '[':
		return p.class()
	case '.':
		p.pos++
		return &node{kind: nodeSet, ranges: anyRanges}, nil
	case '^':
		p.pos++
		return &node{kind: nodeBegin}, nil
	case '$':
		p.pos++
		return &node{kind: nodeEnd}, nil
	case '\\':
		rs, _, err := p.escape(false)
		return &node{kind: nodeSet, ranges: rs}, err
	case '*', '+', '?':
		return nil, p.fail("missing-operand", start, 1, "the quantifier repeats nothing; write \\"+string(c)+" for the character itself")
	case '{':
		if isDigit(p.peek(1)) {
			return nil, p.fail("missing-operand", start, 1, "the bound repeats nothing; write \\{ for a literal brace")
		}
		return nil, p.fail("literal-brace", start, 1, "a bare { is read differently by different engines; write \\{")
	case '}':
		return nil, p.fail("literal-brace", start, 1, "a bare } is read differently by different engines; write \\}")
	case ']':
		return nil, p.fail("literal-bracket", start, 1, "a bare ] is read differently by different engines; write \\]")
	}
	p.pos++
	return &node{kind: nodeSet, ranges: []rng{{c, c}}}, nil
}

// extension rejects a group that starts with (?: flags, non-capturing, named
// and comment groups, and lookaround.
func (p *parser) extension() error {
	start := p.pos
	switch p.peek(2) {
	case ':':
		return p.fail("non-capturing-group", start, 3, "write a plain group (...)")
	case '=', '!':
		return p.fail("lookahead", start, 3, "lookaround is not portable; leave the constraint opaque")
	case '<':
		if c := p.peek(3); c == '=' || c == '!' {
			return p.fail("lookbehind", start, 4, "lookaround is not portable; leave the constraint opaque")
		}
		return p.fail("named-group", start, 3, "write a plain group (...)")
	case 'P':
		return p.fail("named-group", start, 3, "write a plain group (...)")
	case '#':
		return p.fail("comment", start, 3, "remove the comment")
	}
	return p.fail("flags", start, 3, "flags are not portable; matching is always case-sensitive, so spell out cases with classes such as [Aa]")
}

// escape parses a backslash escape: a class escape (\d \w \s) or an escaped
// metacharacter. class reports a class escape.
func (p *parser) escape(inClass bool) (rs []rng, class bool, err error) {
	start := p.pos
	p.pos++
	if p.pos >= len(p.src) {
		return nil, false, p.fail("trailing-backslash", start, 1, "a pattern cannot end with \\; write \\\\ for a backslash")
	}
	c := p.src[p.pos]
	p.pos++
	switch c {
	case 'd':
		return digitRanges, true, nil
	case 'w':
		return wordRanges, true, nil
	case 's':
		return spaceRanges, true, nil
	case 'D', 'W', 'S':
		return nil, false, p.fail("negated-class-escape", start, 2, "write a negated bracket class, such as [^0-9] for \\D")
	case '1', '2', '3', '4', '5', '6', '7', '8', '9', 'k':
		return nil, false, p.fail("backreference", start, 2, "backreferences are not portable; leave the constraint opaque")
	case 'p', 'P':
		return nil, false, p.fail("unicode-class", start, 2, "Unicode classes are not portable; write the ranges as a bracket class")
	case 'b', 'B', 'A', 'Z', 'z', 'G', 'm', 'M', 'y', 'Y':
		return nil, false, p.fail("assertion-escape", start, 2, "only ^ and $ assert positions; leave the constraint opaque")
	case '^', '$', '\\', '.', '*', '+', '?', '(', ')', '[', ']', '{', '}', '|', '/':
		return []rng{{c, c}}, false, nil
	case '-':
		if inClass {
			return []rng{{c, c}}, false, nil
		}
	}
	return nil, false, p.fail("escape", start, 2, "this escape is not portable; write the character itself, or a bracket class")
}

func (p *parser) class() (*node, error) {
	start := p.pos
	p.pos++
	negate := p.peek(0) == '^'
	if negate {
		p.pos++
	}
	if p.peek(0) == ']' {
		return nil, p.fail("bracket-leading-close", p.pos, 1, "engines disagree on a ] first in a bracket class; write \\]")
	}
	var rs []rng
	first := true
	for {
		if p.pos >= len(p.src) {
			return nil, p.fail("unclosed-bracket", start, 1, "this [ is never closed; add the missing ]")
		}
		if p.src[p.pos] == ']' {
			p.pos++
			break
		}
		itemStart := p.pos
		lo, class, err := p.classAtom(first)
		if err != nil {
			return nil, err
		}
		first = false
		if class || p.peek(0) != '-' || p.peek(1) == ']' {
			rs = append(rs, lo...)
			continue
		}
		p.pos++ // the range's -
		if p.pos >= len(p.src) {
			return nil, p.fail("unclosed-bracket", start, 1, "this [ is never closed; add the missing ]")
		}
		hi, hiClass, err := p.classAtom(false)
		switch {
		case err != nil:
			return nil, err
		case hiClass:
			return nil, p.fail("bracket-range", itemStart, p.pos-itemStart, "a class escape cannot end a range")
		case hi[0].lo < lo[0].lo:
			return nil, p.fail("bracket-range", itemStart, p.pos-itemStart, "the range is reversed; write the lower end first")
		}
		rs = append(rs, rng{lo[0].lo, hi[0].lo})
	}
	rs = normalize(rs)
	if negate {
		rs = complement(rs)
	}
	return &node{kind: nodeSet, ranges: rs}, nil
}

// classAtom parses one element of a bracket class: a code point, an escaped
// code point or a class escape.
func (p *parser) classAtom(first bool) ([]rng, bool, error) {
	c := p.src[p.pos]
	switch c {
	case '[':
		if n := p.peek(1); n == ':' || n == '=' || n == '.' {
			return nil, false, p.fail("posix-class", p.pos, 2, "POSIX bracket classes are not portable; write the ranges, such as a-z for [:lower:] in ASCII")
		}
	case '\\':
		return p.escape(true)
	case '-':
		if !first && p.peek(1) != ']' {
			return nil, false, p.fail("bracket-hyphen", p.pos, 1, "a - inside a bracket class must be first, last or part of a range; write \\-")
		}
	}
	p.pos++
	return []rng{{c, c}}, false, nil
}

// quantified parses the quantifier after atom a, if any.
func (p *parser) quantified(a *node) (*node, error) {
	start := p.pos
	var lo, hi int
	switch p.peek(0) {
	case '*':
		lo, hi = 0, -1
	case '+':
		lo, hi = 1, -1
	case '?':
		lo, hi = 0, 1
	case '{':
		var err error
		if lo, hi, err = p.bound(); err != nil {
			return nil, err
		}
	default:
		return a, nil
	}
	if p.pos == start {
		p.pos++ // a one-character quantifier; bound moved past its own
	}
	if a.kind == nodeBegin || a.kind == nodeEnd {
		return nil, p.fail("missing-operand", start, p.pos-start, "an anchor cannot be repeated")
	}
	switch p.peek(0) {
	case '?':
		return nil, p.fail("lazy-quantifier", start, p.pos-start+1, "only greedy quantifiers are portable; drop the ?")
	case '+':
		return nil, p.fail("possessive-quantifier", start, p.pos-start+1, "only greedy quantifiers are portable; drop the +")
	case '*', '{':
		return nil, p.fail("nested-quantifier", start, p.pos-start+1, "a quantifier cannot follow a quantifier; group the operand first, as in (a*)*")
	}
	return &node{kind: nodeRepeat, subs: []*node{a}, min: lo, max: hi}, nil
}

// bound parses {m}, {m,} or {m,n} at p.pos and leaves p.pos after its }.
func (p *parser) bound() (lo, hi int, err error) {
	start := p.pos
	if !isDigit(p.peek(1)) {
		return 0, 0, p.fail("literal-brace", start, 1, "a bare { is read differently by different engines; write \\{")
	}
	p.pos++
	lo, ok := p.count()
	hi = lo
	if p.peek(0) == ',' {
		p.pos++
		hi = -1
		if isDigit(p.peek(0)) {
			var hiOK bool
			hi, hiOK = p.count()
			ok = ok && hiOK
		}
	}
	malformed := func(reason string) (int, int, error) {
		return 0, 0, p.fail("bound", start, p.pos-start, reason)
	}
	if p.peek(0) != '}' {
		return malformed("write a bound as {m}, {m,} or {m,n}")
	}
	p.pos++
	if !ok {
		return malformed("repetition counts are at most " + strconv.Itoa(maxRepeat))
	}
	if hi >= 0 && hi < lo {
		return malformed("the bound's minimum exceeds its maximum")
	}
	return lo, hi, nil
}

// count parses a run of digits; ok is false when the value exceeds maxRepeat.
func (p *parser) count() (n int, ok bool) {
	for isDigit(p.peek(0)) {
		n = min(n*10+int(p.src[p.pos]-'0'), maxRepeat+1)
		p.pos++
	}
	return n, n <= maxRepeat
}

func isDigit(c rune) bool { return c >= '0' && c <= '9' }

// normalize sorts ranges and merges overlapping and adjacent ones.
func normalize(rs []rng) []rng {
	rs = slices.Clone(rs)
	slices.SortFunc(rs, func(a, b rng) int { return int(a.lo - b.lo) })
	out := rs[:0]
	for _, r := range rs {
		if n := len(out); n > 0 && r.lo <= out[n-1].hi+1 {
			out[n-1].hi = max(out[n-1].hi, r.hi)
			continue
		}
		out = append(out, r)
	}
	return out
}

// complement returns the code points not in the normalized ranges rs.
func complement(rs []rng) []rng {
	var out []rng
	next := rune(0)
	for _, r := range rs {
		if r.lo > next {
			out = append(out, rng{next, r.lo - 1})
		}
		next = r.hi + 1
	}
	if next <= utf8.MaxRune {
		out = append(out, rng{next, utf8.MaxRune})
	}
	return out
}
