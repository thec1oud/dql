package gen

import "testing"

// §4.2: the TS column of the word-splitting table, and the escapes.
func TestNames(t *testing.T) {
	for _, c := range []struct {
		words              []string
		typ, member, local string
	}{
		{[]string{"display", "name"}, "DisplayName", "displayName", "displayName"},
		{[]string{"user", "id"}, "UserId", "userId", "userId"},
		{[]string{"http", "server", "log"}, "HttpServerLog", "httpServerLog", "httpServerLog"},
		{[]string{"v2", "items"}, "V2Items", "v2Items", "v2Items"},
		{[]string{"2fa"}, "X2fa", "x2fa", "x2fa"},
		{[]string{"class"}, "Class", "class", "class_"},
		{[]string{"default"}, "Default", "default", "default_"},
		{[]string{"élan", "vital"}, "ÉlanVital", "élanVital", "élanVital"},
		{[]string{"a", ""}, "A", "a", "a"},
		{nil, "X", "x", "x"},
	} {
		if got := TypeName(c.words); got != c.typ {
			t.Errorf("TypeName(%q) = %q, want %q", c.words, got, c.typ)
		}
		if got := MemberName(c.words); got != c.member {
			t.Errorf("MemberName(%q) = %q, want %q", c.words, got, c.member)
		}
		if got := LocalName(c.words); got != c.local {
			t.Errorf("LocalName(%q) = %q, want %q", c.words, got, c.local)
		}
	}
}
