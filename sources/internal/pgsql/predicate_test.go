package pgsql

import (
	"fmt"
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func TestRecognizePredicate(t *testing.T) {
	for _, c := range []struct{ text, null, notTrue string }{
		// As Postgres prints them, and as written.
		{"(deleted_at IS NULL)", "[deleted_at]", "[]"},
		{"deleted_at is null", "[deleted_at]", "[]"},
		{"p.deleted_at IS NULL", "[deleted_at]", "[]"},
		{"(NOT deleted)", "[]", "[deleted]"},
		{"(deleted = false)", "[]", "[deleted]"},
		{"false = deleted", "[]", "[deleted]"},
		{"deleted = 'false'::boolean", "[]", "[deleted]"},
		{"(deleted IS FALSE)", "[]", "[deleted]"},
		{"(deleted IS NOT TRUE)", "[]", "[deleted]"},
		// Conjuncts at any depth of AND, deduplicated and sorted.
		{"((status = 'published'::text) AND (deleted_at IS NULL))", "[deleted_at]", "[]"},
		{"(b IS NULL AND (NOT x AND a IS NULL)) AND a IS NULL", "[a b]", "[x]"},
		// Forms that require nothing of a column.
		{"(status = 'published'::text)", "[]", "[]"},
		{"deleted_at IS NOT NULL", "[]", "[]"},
		{"deleted IS TRUE", "[]", "[]"},
		{"deleted = true", "[]", "[]"},
		{"deleted = 'true'", "[]", "[]"},
		{"deleted = other", "[]", "[]"},
		{"deleted <> false", "[]", "[]"},
		{"deleted IN (false)", "[]", "[]"},
		{"false = 1 + 1", "[]", "[]"},
		{"(1 + 1) IS NULL", "[]", "[]"},
		{"NOT (a AND b)", "[]", "[]"},
		{"a IS NULL OR b IS NULL", "[]", "[]"},
		{"x.* IS NULL", "[]", "[]"},
		{"deleted = 0", "[]", "[]"},
		// Text that is not one expression.
		{"a IS NULL); DROP TABLE t; (", "[]", "[]"},
		{"(((", "[]", "[]"},
	} {
		u := &irv1.Uniqueness{}
		RecognizePredicate(u, c.text)
		if got := fmt.Sprint(u.GetPredicateNullColumns()); got != c.null {
			t.Errorf("%q: null columns %s, want %s", c.text, got, c.null)
		}
		if got := fmt.Sprint(u.GetPredicateNotTrueColumns()); got != c.notTrue {
			t.Errorf("%q: not-true columns %s, want %s", c.text, got, c.notTrue)
		}
	}
}
