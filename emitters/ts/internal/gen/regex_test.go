package gen

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
)

// The translation of each construct of the portable subset (§10.2).
var regexCases = []struct {
	pattern, js string
}{
	{`^[a-z]+$`, `/^[a-z]+$/u`},
	{`a{2}b{2,}c{2,3}`, `/a{2}b{2,}c{2,3}/u`},
	{`(ab)+`, `/(?:ab)+/u`},
	{`(?:ab)+`, `/(?:ab)+/u`},
	{`(a|bc)*d`, `/(?:a|bc)*d/u`},
	{`x(?:ab|cd)`, `/x(?:ab|cd)/u`},
	{`x|yz`, `/x|yz/u`},
	{`a|`, `/a|(?:)/u`},
	{`a*?b+?`, `/a*?b+?/u`},
	{`.`, `/[^\n]/u`},
	{`(?s).`, `/[^]/u`},
	{`[^a]`, `/[^a]/u`},
	{`\s\S`, `/[\u{9}\u{a}\u{c}\u{d} ][^\u{9}\u{a}\u{c}\u{d} ]/u`},
	{`\d\w`, `/[0-9][0-9A-Z_a-z]/u`},
	{`\bfoo\B`, `/\bfoo\B/u`},
	{`[\]\-^/]`, `/[\-\/\]\^]/u`},
	{`\.\/\$`, `/\.\/\$/u`},
	{"\t\u00e9 \u00a0\u2028\u0085", `/\u{9}é \u{a0}\u{2028}\u{85}/u`},
	{`()`, `/(?:(?:))/u`},
	{`[^\x00-\x{10FFFF}]`, `/[]/u`},
	{`[ab]c[a-c]`, `/[ab]c[a-c]/u`},
	{`ab?`, `/ab?/u`},
}

func TestJSRegex(t *testing.T) {
	for _, c := range regexCases {
		got, err := jsRegex(c.pattern)
		if err != nil || got != c.js {
			t.Errorf("jsRegex(%q) = %s, %v; want %s", c.pattern, got, err, c.js)
		}
	}
	for _, bad := range []string{`(?i)a`, `(?i)[a]`, `(?m)^a$`, `(`, `x(?i:a)*`, `(?i:a)|b`, `(?i:a)(b)`, `((?i)a)`, `x(?:(?i)ab|cd)`, `(?i:ab)|cd`, `(?:(?i:a)b)*`} {
		if got, err := jsRegex(bad); err == nil {
			t.Errorf("jsRegex(%q) = %s, want an error", bad, got)
		}
	}
}

// Each translation matches exactly the strings Go's regexp matches, under
// Bun, so a value passes a TypeScript validator exactly when it passes a Go
// one.
func TestJSRegexMatches(t *testing.T) {
	inputs := []string{"", "a", "aa", "ab", "abab", "abc", "bcd", "ad", "xab", "xcd", "x", "yz", "aabbcc", "aabbbccc", "\n", "é", " ", "\t",
		"foo", "foox", " foo", "]", "-", "^", "/", "./$", "\t\u00e9 \u00a0\u2028\u0085", "z", "Z", "_", "9", "c", "ac", "a\nb"}
	type row struct {
		Regex  string `json:"regex"`
		Inputs []string
		Want   []bool
	}
	var rows []row
	for _, c := range regexCases {
		re := regexp.MustCompile(c.pattern)
		js, err := jsRegex(c.pattern)
		if err != nil {
			t.Fatal(err)
		}
		r := row{Regex: js, Inputs: inputs}
		for _, in := range inputs {
			r.Want = append(r.Want, re.MatchString(in))
		}
		rows = append(rows, r)
	}
	data, _ := json.Marshal(rows)
	dir := t.TempDir()
	script := `const rows = JSON.parse(await Bun.file(process.argv[2]).text());
let bad = 0;
for (const r of rows) {
  const re = eval(r.regex);
  r.Inputs.forEach((s, i) => {
    if (re.test(s) !== r.Want[i]) { console.log(r.regex, JSON.stringify(s), r.Want[i]); bad++; }
  });
}
process.exit(bad === 0 ? 0 : 1);
`
	if err := os.WriteFile(filepath.Join(dir, "rows.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "check.ts"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatalf("bun is needed to run the translations (it builds runtime/ts): %v", err)
	}
	if out, err := exec.Command(bun, filepath.Join(dir, "check.ts"), filepath.Join(dir, "rows.json")).CombinedOutput(); err != nil {
		t.Fatalf("translations disagree with Go: %v\n%s", err, out)
	}
	if !slices.ContainsFunc(rows, func(r row) bool { return slices.Contains(r.Want, true) && slices.Contains(r.Want, false) }) {
		t.Fatal("no case tells matches apart")
	}
}
