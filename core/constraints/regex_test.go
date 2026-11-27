package constraints

import (
	"errors"
	"strings"
	"testing"
)

func TestValidatePatternAccepts(t *testing.T) {
	for _, p := range []string{
		"",
		"abc",
		".",
		"^a$",
		"^[a-z0-9]+(-[a-z0-9]+)*$", // §10.4
		"[^a]",
		"[-a]",
		"[a-]",
		"[^-]",
		`[\]\-\\\[\^]`,
		`[\d\w\s]`,
		`[\d-]`,
		"[[]",
		"[a^]",
		"[{}()|*.$+?]",
		"[--/]",
		"[!--]",
		"a|b|",
		"()",
		"(a|)",
		"(a*)*",
		"()*",
		"a{0}",
		"a{2,}",
		"a{0,255}",
		"a{255}",
		`\.\*\+\?\(\)\[\]\{\}\|\^\$\/\\`,
		`\d\w\s`,
		"日本語",
		"# not a comment, and spaces",
		"(^a|b$)",
		"a^b",
	} {
		if err := ValidatePattern(p); err != nil {
			t.Errorf("ValidatePattern(%q) = %v, want accepted", p, err)
		}
	}
}

func TestValidatePatternRejects(t *testing.T) {
	cases := []struct {
		pattern   string
		construct string
		pos       int
		text      string
	}{
		{`(a)\1`, "backreference", 3, `\1`},
		{`(a)\9`, "backreference", 3, `\9`},
		{`\k<n>`, "backreference", 0, `\k`},
		{"a(?=b)", "lookahead", 1, "(?="},
		{"a(?!b)", "lookahead", 1, "(?!"},
		{"(?<=a)b", "lookbehind", 0, "(?<="},
		{"(?<!a)b", "lookbehind", 0, "(?<!"},
		{"(?<n>a)", "named-group", 0, "(?<"},
		{"(?P<n>a)", "named-group", 0, "(?P"},
		{"(?:a)", "non-capturing-group", 0, "(?:"},
		{"(?#c)", "comment", 0, "(?#"},
		{"(?i)abc", "flags", 0, "(?i"},
		{"(?", "flags", 0, "(?"},
		{"a*?", "lazy-quantifier", 1, "*?"},
		{"a+?", "lazy-quantifier", 1, "+?"},
		{"a??", "lazy-quantifier", 1, "??"},
		{"a{2,3}?", "lazy-quantifier", 1, "{2,3}?"},
		{"a*+", "possessive-quantifier", 1, "*+"},
		{"a**", "nested-quantifier", 1, "**"},
		{"a{2}{3}", "nested-quantifier", 1, "{2}{"},
		{`\p{L}`, "unicode-class", 0, `\p`},
		{`\P{L}`, "unicode-class", 0, `\P`},
		{"[[:alpha:]]", "posix-class", 1, "[:"},
		{"[[=a=]]", "posix-class", 1, "[="},
		{"[[.a.]]", "posix-class", 1, "[."},
		{`\D`, "negated-class-escape", 0, `\D`},
		{`\W`, "negated-class-escape", 0, `\W`},
		{`[\S]`, "negated-class-escape", 1, `\S`},
		{`\b`, "assertion-escape", 0, `\b`},
		{`a\A`, "assertion-escape", 1, `\A`},
		{`\m`, "assertion-escape", 0, `\m`},
		{`\n`, "escape", 0, `\n`},
		{`\x41`, "escape", 0, `\x`},
		{"\\u00e9", "escape", 0, `\u`},
		{`\0`, "escape", 0, `\0`},
		{`a\-b`, "escape", 1, `\-`},
		{`\é`, "escape", 0, `\é`},
		{`[\t]`, "escape", 1, `\t`},
		{`abc\`, "trailing-backslash", 3, `\`},
		{"(ab", "unclosed-group", 0, "("},
		{"((a)", "unclosed-group", 0, "("},
		{"a)b", "unmatched-paren", 1, ")"},
		{"[ab", "unclosed-bracket", 0, "["},
		{"x[a-", "unclosed-bracket", 1, "["},
		{`[\d`, "unclosed-bracket", 0, "["},
		{"[]a]", "bracket-leading-close", 1, "]"},
		{"[^]a]", "bracket-leading-close", 2, "]"},
		{"[]", "bracket-leading-close", 1, "]"},
		{"[z-a]", "bracket-range", 1, "z-a"},
		{`[a-\d]`, "bracket-range", 1, `a-\d`},
		{`[a-\q]`, "escape", 3, `\q`},
		{"[a-[:x:]]", "posix-class", 3, "[:"},
		{"[a-z-0]", "bracket-hyphen", 4, "-"},
		{`[\d-z]`, "bracket-hyphen", 3, "-"},
		{"*a", "missing-operand", 0, "*"},
		{"+", "missing-operand", 0, "+"},
		{"?", "missing-operand", 0, "?"},
		{"{2}", "missing-operand", 0, "{"},
		{"a|*b", "missing-operand", 2, "*"},
		{"(*a)", "missing-operand", 1, "*"},
		{"^*", "missing-operand", 1, "*"},
		{"a$+", "missing-operand", 2, "+"},
		{"^{2}", "missing-operand", 1, "{2}"},
		{"{", "literal-brace", 0, "{"},
		{"a{", "literal-brace", 1, "{"},
		{"a{,3}", "literal-brace", 1, "{"},
		{"a}", "literal-brace", 1, "}"},
		{"]", "literal-bracket", 0, "]"},
		{"a{2", "bound", 1, "{2"},
		{"a{2,3", "bound", 1, "{2,3"},
		{"a{2x}", "bound", 1, "{2"},
		{"a{3,2}", "bound", 1, "{3,2}"},
		{"a{256}", "bound", 1, "{256}"},
		{"a{1,256}", "bound", 1, "{1,256}"},
		{"a{256,300}", "bound", 1, "{256,300}"},
		{"a{99999999999999999999}", "bound", 1, "{99999999999999999999}"},
		{"ab\xff", "invalid-utf8", 2, "\xff"},
		{"é(?=x)", "lookahead", 1, "(?="}, // positions count code points, not bytes
		{"((a{255}){255}){255}", "too-large", 0, "((a{255}){255}){255}"},
	}
	for _, c := range cases {
		err := ValidatePattern(c.pattern)
		var pe *PatternError
		if !errors.As(err, &pe) {
			t.Errorf("ValidatePattern(%q) = %v, want a *PatternError", c.pattern, err)
			continue
		}
		if pe.Construct != c.construct || pe.Pos != c.pos || pe.Text != c.text {
			t.Errorf("ValidatePattern(%q) = %s at %d %q, want %s at %d %q",
				c.pattern, pe.Construct, pe.Pos, pe.Text, c.construct, c.pos, c.text)
		}
		if pe.Reason == "" || !strings.Contains(err.Error(), c.construct) || !strings.Contains(err.Error(), "§10.2") {
			t.Errorf("ValidatePattern(%q): message %q", c.pattern, err)
		}
	}
}

func TestNormalizeAndComplement(t *testing.T) {
	got := normalize([]rng{{'m', 'p'}, {'a', 'c'}, {'d', 'e'}, {'b', 'b'}, {'o', 'z'}})
	want := []rng{{'a', 'e'}, {'m', 'z'}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("normalize = %v, want %v", got, want)
	}
	if c := complement([]rng{{0, 0x10FFFF}}); len(c) != 0 {
		t.Errorf("complement(everything) = %v", c)
	}
	if c := complement([]rng{{0, 'a'}}); len(c) != 1 || c[0] != (rng{'b', 0x10FFFF}) {
		t.Errorf("complement = %v", c)
	}
}
