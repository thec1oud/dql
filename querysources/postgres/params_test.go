package postgres

import (
	"fmt"
	"strings"
	"testing"
)

func TestRewrite(t *testing.T) {
	for _, c := range []struct{ in, text, params string }{
		{"a = @x AND b = @y AND c = @x", "a = $1 AND b = $2 AND c = $1", "x:0 y:0"},
		{"@scope.tenant_id = t AND @scope.a = a AND @id = id AND @scope.a > 0", "$3 = t AND $2 = a AND $1 = id AND $2 > 0", "id:0 a:3 tenant_id:3"},
		{"sqlc.arg(a) sqlc.narg('b') sqlc.slice( c ) sqlc.arg (d)", "$1 $2 $3 $4", "a:0 b:1 c:2 d:0"},
		{"'@x' \"@y\" E'\\'@z' 'it''s @w' -- @c\n/* @d /* @e */ @f */ $$ @g $$ $t$ @h $t$ @i", "'@x' \"@y\" E'\\'@z' 'it''s @w' -- @c\n/* @d /* @e */ @f */ $$ @g $$ $t$ @h $t$ $1", "i:0"},
		{"a <@b AND c @@d AND e@>f AND x_sqlc.arg(y)", "a <@b AND c @@d AND e@>f AND x_sqlc.arg(y)", ""},
		{"a -- no newline @x", "a -- no newline @x", ""},
		{"a /* open @x", "a /* open @x", ""},
		{"a $t$ open @x", "a $t$ open @x", ""},
		{"a 'open @x", "a 'open @x", ""},
		{"$ @x", "$ $1", "x:0"},
		{"x$y @x", "x$y $1", "x:0"},
		{"sqlc", "sqlc", ""},
		{"@", "@", ""},
	} {
		r, err := rewrite(c.in)
		if err != nil {
			t.Errorf("rewrite(%q): %v", c.in, err)
			continue
		}
		var ps []string
		for _, p := range r.params {
			ps = append(ps, fmt.Sprintf("%s:%d", p.name, p.kind))
		}
		if r.text != c.text || strings.Join(ps, " ") != c.params {
			t.Errorf("rewrite(%q) = %q %q, want %q %q", c.in, r.text, strings.Join(ps, " "), c.text, c.params)
		}
	}
}

func TestRewriteErrors(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"a = @x AND b = sqlc.narg(x)", "parameter x is written both as sqlc.arg and as sqlc.narg; write it one way"},
		{"a = @scope.", "@scope. needs a column name, as in @scope.tenant_id"},
		{"a = $1", "positional parameter $1; name it with @name or sqlc.arg(name)"},
		{"sqlc.nope(x)", "sqlc.nope is not a parameter; use sqlc.arg, sqlc.narg or sqlc.slice"},
		{"sqlc.arg x", "sqlc.arg takes a parameter name in parentheses"},
		{"sqlc.arg(x", "sqlc.arg takes a parameter name in parentheses"},
		{"sqlc.", "sqlc. is not a parameter; use sqlc.arg, sqlc.narg or sqlc.slice"},
		{"sqlc.arg(1x)", `sqlc.arg(1x): "1x" is not a parameter name`},
	} {
		if _, err := rewrite(c.in); err == nil || err.Error() != c.want {
			t.Errorf("rewrite(%q) = %v, want %q", c.in, err, c.want)
		}
	}
}

func TestOriginal(t *testing.T) {
	r, err := rewrite("a = @email AND b = @id")
	if err != nil {
		t.Fatal(err)
	}
	// a = $1 AND b = $2: b is at 11 in the text and 15 in the statement.
	for _, c := range [][2]int{{-1, -1}, {0, 0}, {4, 4}, {6, 10}, {11, 15}, {15, 19}, {17, 22}} {
		if got := r.original(c[0]); got != c[1] {
			t.Errorf("original(%d) = %d, want %d", c[0], got, c[1])
		}
	}
}
