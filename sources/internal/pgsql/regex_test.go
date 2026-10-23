package pgsql

import "testing"

func TestPortableRegex(t *testing.T) {
	cases := []struct {
		re   string
		want bool
	}{
		// §10.4: slug_text.
		{`^[a-z0-9]+(-[a-z0-9]+)*$`, true},
		{`abc`, true},
		{`a|b`, true},
		{`(a|b)c`, true},
		{`((a))`, true},
		{`a.c`, true},
		{`x?y+z*`, true},
		{`a{2}`, true},
		{`a{2,}`, true},
		{`a{2,5}`, true},
		{`a{255}`, true},
		{`[^a-z]`, true},
		{`[a-]`, true},
		{`[-a]`, true},
		{`[\]]`, true},
		{`[a\-z]`, true},
		{`[^-]`, true},
		{`\.\$`, true},
		{`é+ü`, true},
		{`^$`, true},
		{`a,b-c`, true},

		{`\d`, false},
		{`\w`, false},
		{`\s`, false},
		{`\n`, false},
		{`\1`, false},
		{`\ `, false},
		{`a\`, false},
		{`a*?`, false},
		{`a+?`, false},
		{`a??`, false},
		{`a{2}?`, false},
		{`a**`, false},
		{`a{2}{3}`, false},
		{`(?:a)`, false},
		{`(?=a)`, false},
		{`(?i)a`, false},
		{`()`, false},
		{`a|`, false},
		{`|a`, false},
		{`a||b`, false},
		{`(a`, false},
		{`a)`, false},
		{`[]`, false},
		{`[^]`, false},
		{`[]a]`, false},
		{`[[:alpha:]]`, false},
		{`[a`, false},
		{`[a-`, false},
		{`[z-a]`, false},
		{`[a-b-c]`, false},
		{`[\d]`, false},
		{`a{`, false},
		{`a{x}`, false},
		{`a{,5}`, false},
		{`a{5,3}`, false},
		{`a{256}`, false},
		{`a{1,256}`, false},
		{`a{2`, false},
		{`*a`, false},
		{`{2}`, false},
		{`^*`, false},
		{`$+`, false},
		{`]`, false},
		{`}`, false},
		{"\xff", false},
	}
	for _, c := range cases {
		if got := portableRegex(c.re); got != c.want {
			t.Errorf("%q: got %v, want %v", c.re, got, c.want)
		}
	}
}
