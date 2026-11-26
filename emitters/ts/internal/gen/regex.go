package gen

import (
	"errors"
	"fmt"
	"regexp/syntax"
	"strconv"
	"strings"
	"unicode"
)

// jsRegex translates a pattern of the portable subset (§10.2) into a
// JavaScript regular expression literal with the u flag, matching what Go's
// regexp matches, so that every emitter agrees: ^ and $ match at the ends of
// the string, . matches any code point but \n, and \d, \w and \s are
// ASCII. The pattern is parsed with Go's own parser and printed again, so
// the literal holds only what both engines read the same way.
func jsRegex(pattern string) (string, error) {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("/")
	if err := writeJS(&b, re); err != nil {
		return "", err
	}
	b.WriteString("/u")
	return b.String(), nil
}

var errFlags = errors.New("flags are not in the portable subset")

func writeJS(b *strings.Builder, re *syntax.Regexp) error {
	if re.Flags&syntax.FoldCase != 0 && (re.Op == syntax.OpLiteral || re.Op == syntax.OpCharClass) {
		return errFlags
	}
	switch re.Op {
	case syntax.OpEmptyMatch:
		b.WriteString("(?:)")
	case syntax.OpLiteral:
		for _, r := range re.Rune {
			b.WriteString(jsRune(r, false))
		}
	case syntax.OpCharClass:
		writeClass(b, re.Rune)
	case syntax.OpAnyCharNotNL:
		b.WriteString(`[^\n]`)
	case syntax.OpAnyChar:
		b.WriteString("[^]")
	case syntax.OpBeginText:
		b.WriteString("^")
	case syntax.OpEndText:
		b.WriteString("$")
	case syntax.OpWordBoundary:
		b.WriteString(`\b`)
	case syntax.OpNoWordBoundary:
		b.WriteString(`\B`)
	case syntax.OpCapture:
		b.WriteString("(?:")
		if err := writeJS(b, re.Sub[0]); err != nil {
			return err
		}
		b.WriteString(")")
	case syntax.OpStar, syntax.OpPlus, syntax.OpQuest, syntax.OpRepeat:
		if err := writeAtom(b, re.Sub[0]); err != nil {
			return err
		}
		switch re.Op {
		case syntax.OpStar:
			b.WriteString("*")
		case syntax.OpPlus:
			b.WriteString("+")
		case syntax.OpQuest:
			b.WriteString("?")
		default:
			switch {
			case re.Max == -1:
				fmt.Fprintf(b, "{%d,}", re.Min)
			case re.Min == re.Max:
				fmt.Fprintf(b, "{%d}", re.Min)
			default:
				fmt.Fprintf(b, "{%d,%d}", re.Min, re.Max)
			}
		}
		if re.Flags&syntax.NonGreedy != 0 {
			b.WriteString("?")
		}
	case syntax.OpConcat:
		for _, s := range re.Sub {
			if s.Op == syntax.OpAlternate {
				if err := writeGroup(b, s); err != nil {
					return err
				}
				continue
			}
			if err := writeJS(b, s); err != nil {
				return err
			}
		}
	case syntax.OpAlternate:
		for i, s := range re.Sub {
			if i > 0 {
				b.WriteString("|")
			}
			if err := writeJS(b, s); err != nil {
				return err
			}
		}
	default:
		// ^ and $ at line ends, which only a flag gives.
		return errFlags
	}
	return nil
}

// writeAtom writes re as the operand of a quantifier: grouped unless it is
// one character, a class or a group already.
func writeAtom(b *strings.Builder, re *syntax.Regexp) error {
	switch {
	case re.Op == syntax.OpLiteral && len(re.Rune) == 1, re.Op == syntax.OpCharClass, re.Op == syntax.OpAnyCharNotNL,
		re.Op == syntax.OpAnyChar, re.Op == syntax.OpCapture:
		return writeJS(b, re)
	}
	return writeGroup(b, re)
}

func writeGroup(b *strings.Builder, re *syntax.Regexp) error {
	b.WriteString("(?:")
	if err := writeJS(b, re); err != nil {
		return err
	}
	b.WriteString(")")
	return nil
}

// writeClass writes a class from its ranges, negated when that is shorter,
// as [^\n] is for every code point but \n.
func writeClass(b *strings.Builder, ranges []rune) {
	negate := len(ranges) > 0 && ranges[0] == 0 && ranges[len(ranges)-1] == unicode.MaxRune
	if negate {
		var inv []rune
		for i := 1; i+1 < len(ranges); i += 2 {
			inv = append(inv, ranges[i]+1, ranges[i+1]-1)
		}
		ranges = inv
		b.WriteString("[^")
	} else {
		b.WriteString("[")
	}
	for i := 0; i+1 < len(ranges); i += 2 {
		lo, hi := ranges[i], ranges[i+1]
		b.WriteString(jsRune(lo, true))
		if hi > lo {
			if hi > lo+1 {
				b.WriteString("-")
			}
			b.WriteString(jsRune(hi, true))
		}
	}
	b.WriteString("]")
}

// jsRune spells one code point in a regex literal with the u flag: syntax
// characters escaped, and control characters, line terminators and
// whitespace other than a space as \u{…}.
func jsRune(r rune, inClass bool) string {
	special := `^$\.*+?()[]{}|/`
	if inClass {
		special = `\]^-[/`
	}
	switch {
	case strings.ContainsRune(special, r):
		return `\` + string(r)
	case r < 0x20, r == 0x7f, r == 0x2028, r == 0x2029, r != ' ' && unicode.IsSpace(r), !unicode.IsPrint(r) && r != ' ':
		return `\u{` + strconv.FormatInt(int64(r), 16) + `}`
	}
	return string(r)
}
