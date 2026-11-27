package constraints

import (
	"strings"
	"testing"
)

// The expectations are what Postgres's ~ returns for the same pattern and
// text (§10.2: an unanchored search over code points, ^ and $ for the whole
// string, . matching newline), except that \d \w \s are ASCII only, as the
// subset defines them.
func TestPatternMatch(t *testing.T) {
	cases := []struct {
		pattern, s string
		want       bool
	}{
		// The slug_text domain of §1.3 and §10.4.
		{"^[a-z0-9]+(-[a-z0-9]+)*$", "hello", true},
		{"^[a-z0-9]+(-[a-z0-9]+)*$", "hello-world", true},
		{"^[a-z0-9]+(-[a-z0-9]+)*$", "a-b-c", true},
		{"^[a-z0-9]+(-[a-z0-9]+)*$", "abc123", true},
		{"^[a-z0-9]+(-[a-z0-9]+)*$", "hello--world", false},
		{"^[a-z0-9]+(-[a-z0-9]+)*$", "-hello", false},
		{"^[a-z0-9]+(-[a-z0-9]+)*$", "hello-", false},
		{"^[a-z0-9]+(-[a-z0-9]+)*$", "Hello", false},
		{"^[a-z0-9]+(-[a-z0-9]+)*$", "", false},
		{"^[a-z0-9]+(-[a-z0-9]+)*$", "héllo", false},
		{"^[a-z0-9]+(-[a-z0-9]+)*$", "a b", false},
		{"^[a-z0-9]+(-[a-z0-9]+)*$", "ok\n", false},

		// Unanchored search; anchors are written explicitly.
		{"b", "abc", true},
		{"^b", "abc", false},
		{"c$", "abc", true},
		{"b$", "abc", false},
		{"[0-9]+", "order 66 shipped", true},
		{"x", "abc", false},
		{"", "", true},
		{"", "anything", true},
		{"^$", "", true},
		{"^$", "a", false},
		{"x*", "", true},

		// ^ and $ are the whole string: no multiline, and $ does not match
		// before a trailing newline.
		{"^b", "a\nb", false},
		{"a$", "a\n", false},
		{"a$", "ba", true},
		{"a^b", "ab", false},
		{"(^a|b$)", "xab", true},

		// . is any code point, newline included.
		{"a.b", "a\nb", true},
		{"^.$", "é", true},
		{"^.$", "日本", false},
		{"^.{2}$", "日本", true},
		{"^...$", "a😀b", true},

		// ASCII-only class escapes.
		{`^\d+$`, "0123456789", true},
		{`\d`, "٣", false},
		{`\w`, "é", false},
		{`^\w+$`, "a_Z9", true},
		{`\w`, "-", false},
		{`\s`, "\v", true},
		{`^\s+$`, " \t\n\f\r", true},
		{`\s`, "\u00a0", false},
		{`\s`, "\u2003", false},
		{`[\d]`, "7", true},

		// Bracket classes.
		{"^[^a]$", "é", true},
		{"^[^a]$", "a", false},
		{`[^\d]`, "5", false},
		{`^[\-a]+$`, "-a-", true},
		{"^[-a]$", "-", true},
		{"^[a-]$", "-", true},
		{"^[a-cx-z]+$", "abzyx", true},
		{"^[a-cx-z]+$", "abd", false},
		{`[\]]`, "]", true},
		{"^[日-本]$", "日", true},
		{"[[]", "[", true},

		// Case-sensitive.
		{"a", "A", false},

		// Quantifiers.
		{"^a{2,3}$", "a", false},
		{"^a{2,3}$", "aa", true},
		{"^a{2,3}$", "aaa", true},
		{"^a{2,3}$", "aaaa", false},
		{"^a{2,}$", "aaaaa", true},
		{"^a{2,}$", "a", false},
		{"^a{2}$", "aa", true},
		{"^a{0}$", "", true},
		{"^a{0}$", "a", false},
		{"^(ab)+$", "abab", true},
		{"^(ab)+$", "aba", false},
		{"^a?$", "", true},
		{"^a?$", "aa", false},
		{"^(a*)*$", "aaa", true},
		{"^(a*)*$", "b", false},
		{"(a*)*", "b", true},
		{"^()*$", "", true},
		{"^(a|b)*c$", "ababc", true},
		{"^[a-z]{255}$", strings.Repeat("a", 255), true},
		{"^[a-z]{255}$", strings.Repeat("a", 254), false},

		// Alternation and groups.
		{"^(cat|dog)$", "dog", true},
		{"^(cat|dog)$", "cow", false},
		{"^(cat|dog|)$", "", true},
		{"a|", "zzz", true},
		{"^(a|ab)(c|bcd)$", "abcd", true},

		// Escaped metacharacters are literals.
		{`^\.\*\+\?\(\)\[\]\{\}\|\^\$\/\\$`, `.*+?()[]{}|^$/\`, true},
		{`^a\.b$`, "axb", false},

		// Invalid UTF-8 text reads each bad byte as U+FFFD.
		{"^.$", "\xff", true},
		{"^..$", "\xff\xfe", true},
	}
	for _, c := range cases {
		p, err := CompilePattern(c.pattern)
		if err != nil {
			t.Fatalf("CompilePattern(%q): %v", c.pattern, err)
		}
		if got := p.MatchString(c.s); got != c.want {
			t.Errorf("%q ~ %q = %v, want %v", c.s, c.pattern, got, c.want)
		}
	}
}

func TestCompilePatternRejects(t *testing.T) {
	if p, err := CompilePattern("a(?=b)"); err == nil || p != nil {
		t.Errorf("CompilePattern accepted lookahead: %v", p)
	}
}

func TestPatternString(t *testing.T) {
	p, _ := CompilePattern("^[a-z]+$")
	if p.String() != "^[a-z]+$" {
		t.Errorf("String() = %q", p.String())
	}
}

// A pattern that backtracking engines take exponential time on runs in
// linear time.
func TestPatternIsLinear(t *testing.T) {
	p, _ := CompilePattern("^(a|a)*(a*)*b$")
	if p.MatchString(strings.Repeat("a", 5000)) {
		t.Error("matched without a b")
	}
}
