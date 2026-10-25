package gen

import "testing"

// §4.2's table, plus initialisms, digits and escaping.
func TestExported(t *testing.T) {
	for _, c := range []struct {
		words []string
		want  string
	}{
		{[]string{"display", "name"}, "DisplayName"},
		{[]string{"user", "id"}, "UserID"},
		{[]string{"http", "server", "log"}, "HTTPServerLog"},
		{[]string{"v2", "items"}, "V2Items"},
		{[]string{"order", "lines"}, "OrderLines"},
		{[]string{"post", "score", "int64"}, "PostScoreInt64"},
		{[]string{"uuid"}, "UUID"},
		{[]string{"2fa", "codes"}, "X2faCodes"},
		{[]string{"日本"}, "X日本"},
		{[]string{"élan"}, "Élan"},
		{nil, "X"},
	} {
		if got := Exported(c.words); got != c.want {
			t.Errorf("Exported(%q) = %s, want %s", c.words, got, c.want)
		}
	}
}

func TestUnexported(t *testing.T) {
	for _, c := range []struct {
		words []string
		want  string
	}{
		{[]string{"password", "hash"}, "passwordHash"},
		{[]string{"user", "id"}, "userID"},
		{[]string{"id"}, "id"},
		{[]string{"type"}, "type_"},
		{[]string{"string"}, "string_"},
		{[]string{"2fa"}, "x2fa"},
		{nil, "x"},
	} {
		if got := Unexported(c.words); got != c.want {
			t.Errorf("Unexported(%q) = %s, want %s", c.words, got, c.want)
		}
	}
}

func TestReceiver(t *testing.T) {
	for _, c := range []struct {
		words []string
		want  string
	}{
		{[]string{"user", "full"}, "f"},
		{[]string{"post", "status"}, "s"},
		{[]string{"v", "2b"}, "b"},
		{[]string{"x", "42"}, "v"},
		{nil, "v"},
	} {
		if got := receiver(c.words); got != c.want {
			t.Errorf("receiver(%q) = %s, want %s", c.words, got, c.want)
		}
	}
}
