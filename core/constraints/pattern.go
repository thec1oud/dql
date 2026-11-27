package constraints

import (
	"fmt"
	"unicode/utf8"
)

// maxProgram bounds the size of a compiled pattern. Counted repetition is
// expanded, so nested bounds such as ((a{255}){255}){255} would otherwise
// compile to millions of instructions; Postgres likewise refuses such a
// pattern as too complex.
const maxProgram = 100_000

// Pattern is a compiled pattern of the portable subset (§10.2).
//
// It matches with the semantics of §10.2: an unanchored search, as Postgres ~
// does, over Unicode code points; ^ and $ match only at the start and end of
// the whole string; . matches any code point, newline included; \d, \w and
// \s match ASCII only; matching is case-sensitive. A string that is not valid
// UTF-8 is read as Go reads it, each invalid byte as U+FFFD.
//
// Matching simulates the pattern's automaton (a Pike VM), so it runs in time
// linear in the input for a given pattern and never backtracks.
type Pattern struct {
	source string
	prog   []inst
}

type opcode uint8

const (
	opSet   opcode = iota // consume a code point in ranges
	opBegin               // assert the start of the string
	opEnd                 // assert the end of the string
	opSplit               // continue at x and at y
	opJmp                 // continue at x
	opMatch
)

type inst struct {
	op     opcode
	ranges []rng
	x, y   int
}

// ValidatePattern reports whether p is in the portable subset of §10.2. The
// error, when not nil, is a *PatternError naming the offending construct and
// its position.
func ValidatePattern(p string) error {
	_, err := CompilePattern(p)
	return err
}

// CompilePattern validates p (see ValidatePattern) and compiles it.
func CompilePattern(p string) (*Pattern, error) {
	n, err := parse(p)
	if err != nil {
		return nil, err
	}
	c := &compiler{}
	c.emit(n)
	c.add(inst{op: opMatch})
	if len(c.prog) > maxProgram {
		return nil, &PatternError{"too-large", 0, p,
			fmt.Sprintf("the pattern expands to more than %d instructions; use smaller repetition counts", maxProgram)}
	}
	return &Pattern{source: p, prog: c.prog}, nil
}

// String returns the pattern's source.
func (p *Pattern) String() string { return p.source }

type compiler struct{ prog []inst }

func (c *compiler) add(i inst) int {
	c.prog = append(c.prog, i)
	return len(c.prog) - 1
}

func (c *compiler) emit(n *node) {
	if len(c.prog) > maxProgram {
		return // CompilePattern reports it
	}
	switch n.kind {
	case nodeSet:
		c.add(inst{op: opSet, ranges: n.ranges})
	case nodeBegin:
		c.add(inst{op: opBegin})
	case nodeEnd:
		c.add(inst{op: opEnd})
	case nodeConcat:
		for _, s := range n.subs {
			c.emit(s)
		}
	case nodeAlt:
		var jumps []int
		for i, s := range n.subs {
			if i == len(n.subs)-1 {
				c.emit(s)
				break
			}
			split := c.add(inst{op: opSplit})
			c.prog[split].x = len(c.prog)
			c.emit(s)
			jumps = append(jumps, c.add(inst{op: opJmp}))
			c.prog[split].y = len(c.prog)
		}
		for _, j := range jumps {
			c.prog[j].x = len(c.prog)
		}
	case nodeRepeat:
		sub := n.subs[0]
		for range n.min {
			c.emit(sub)
		}
		if n.max < 0 {
			// L: split L+1, out; sub; jmp L; out:
			split := c.add(inst{op: opSplit})
			c.prog[split].x = len(c.prog)
			c.emit(sub)
			c.add(inst{op: opJmp, x: split})
			c.prog[split].y = len(c.prog)
			return
		}
		// Each optional copy: split next, out; sub.
		var splits []int
		for range n.max - n.min {
			split := c.add(inst{op: opSplit})
			c.prog[split].x = len(c.prog)
			splits = append(splits, split)
			c.emit(sub)
		}
		for _, s := range splits {
			c.prog[s].y = len(c.prog)
		}
	}
}

// MatchString reports whether s contains a match of p anywhere.
func (p *Pattern) MatchString(s string) bool {
	cur, next := newThreads(len(p.prog)), newThreads(len(p.prog))
	pos := 0
	for {
		atEnd := pos == len(s)
		// The search is unanchored: a match may start at every position.
		p.follow(cur, 0, pos == 0, atEnd)
		for _, pc := range cur.dense {
			if p.prog[pc].op == opMatch {
				return true
			}
		}
		if atEnd {
			return false
		}
		r, size := utf8.DecodeRuneInString(s[pos:])
		pos += size
		next.clear()
		for _, pc := range cur.dense {
			if i := p.prog[pc]; i.op == opSet && contains(i.ranges, r) {
				p.follow(next, pc+1, false, pos == len(s))
			}
		}
		cur, next = next, cur
	}
}

// follow adds pc to t together with every instruction reachable from it
// without consuming input, at a position that is or is not the start and the
// end of the string.
func (p *Pattern) follow(t *threads, pc int, atStart, atEnd bool) {
	if t.has(pc) {
		return
	}
	t.insert(pc)
	switch i := p.prog[pc]; i.op {
	case opJmp:
		p.follow(t, i.x, atStart, atEnd)
	case opSplit:
		p.follow(t, i.x, atStart, atEnd)
		p.follow(t, i.y, atStart, atEnd)
	case opBegin:
		if atStart {
			p.follow(t, pc+1, atStart, atEnd)
		}
	case opEnd:
		if atEnd {
			p.follow(t, pc+1, atStart, atEnd)
		}
	}
}

func contains(rs []rng, r rune) bool {
	for _, x := range rs {
		if r < x.lo {
			return false
		}
		if r <= x.hi {
			return true
		}
	}
	return false
}

// threads is a sparse set of program counters, in insertion order.
type threads struct {
	dense  []int
	sparse []int
}

func newThreads(n int) *threads {
	return &threads{dense: make([]int, 0, n), sparse: make([]int, n)}
}

func (t *threads) has(pc int) bool {
	i := t.sparse[pc]
	return i < len(t.dense) && t.dense[i] == pc
}

func (t *threads) insert(pc int) {
	t.sparse[pc] = len(t.dense)
	t.dense = append(t.dense, pc)
}

func (t *threads) clear() { t.dense = t.dense[:0] }
