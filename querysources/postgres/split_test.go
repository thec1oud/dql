package postgres

import (
	"fmt"
	"strings"
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// blocks describes what split makes of text: each block, then each
// diagnostic's message.
func blocks(text string) string {
	bs, ds := split(&irv1.QueryFile{Path: "q.sql", Text: text})
	var out []string
	for _, b := range bs {
		s := fmt.Sprintf("%s %s %s@%d %q", b.name, b.cardinality, b.location, b.line, b.body)
		if b.returns != "" {
			s += " returns=" + b.returns
		}
		if b.nonnull != nil {
			s += " nonnull=" + strings.Join(b.nonnull, ",")
		}
		if c := b.consistency; c != nil {
			s += " consistency=" + c.GetLevel().String()
			if c.GetMaxStaleness() != nil {
				s += "/" + c.GetMaxStaleness().AsDuration().String()
			}
		}
		if b.unscoped {
			s += " unscoped"
		}
		out = append(out, s)
	}
	for _, d := range ds {
		out = append(out, d.GetCode()+": "+d.GetMessage())
	}
	return strings.Join(out, "\n")
}

func TestSplit(t *testing.T) {
	for _, c := range []struct{ text, want string }{
		{"-- name: A :one\nSELECT 1;\n", `A CARDINALITY_ONE q.sql:1@2 "SELECT 1"`},
		{"-- leading comment\n\n-- name: A :many\n-- a doc comment\n-- returns: user.full\n-- nonnull: a, b\n-- consistency: bounded-staleness 5s\n-- unscoped\n\n  SELECT 1\n  FROM t ;\r\n-- name: B :exec\n-- consistency: primary\nDELETE FROM t",
			"A CARDINALITY_MANY q.sql:3@10 \"SELECT 1\\n  FROM t\" returns=user.full nonnull=a,b consistency=CONSISTENCY_BOUNDED_STALENESS/5s unscoped\n" +
				`B CARDINALITY_EXEC q.sql:12@14 "DELETE FROM t" consistency=CONSISTENCY_PRIMARY`},
		{"-- name: A :optional\n-- consistency: any\nSELECT 1\n-- name: B :count\n-- consistency: read-your-writes\nSELECT 2\n-- name: C :execrows\nSELECT 3",
			"A CARDINALITY_OPTIONAL q.sql:1@3 \"SELECT 1\" consistency=CONSISTENCY_ANY\n" +
				"B CARDINALITY_COUNT q.sql:4@6 \"SELECT 2\" consistency=CONSISTENCY_READ_YOUR_WRITES\n" +
				`C CARDINALITY_EXEC_ROWS q.sql:7@8 "SELECT 3"`},
		{"SELECT 0;\nSELECT 0;\n-- name: A :one\nSELECT 1",
			"A CARDINALITY_ONE q.sql:3@4 \"SELECT 1\"\n" +
				"query-unnamed: q.sql:1: a statement before the first -- name: line; start each query with -- name: <Name> :<cardinality>"},
		{"-- name: 1A :one\nSELECT 1", `query-header: q.sql:1: "1A" is not a query name; write -- name: <Name> :<cardinality>`},
		{"-- name: A :first\nSELECT 1", `query-header: q.sql:1: query A has cardinality ":first"; use :one, :optional, :many, :count, :exec or :execrows`},
		{"-- name: A :one\n-- returns: user.full\n", "query-header: q.sql:1: query A has no statement"},
		{"-- name: A :one\n-- unscoped\n-- unscoped\nSELECT 1", "query-header: q.sql:3: query A: -- unscoped is given twice"},
		{"-- name: A :one\n-- returns: a\n-- returns: b\nSELECT 1", "query-header: q.sql:3: query A: -- returns: is given twice"},
		{"-- name: A :one\n-- returns: a b\nSELECT 1", `query-header: q.sql:2: query A: -- returns: "a b" is not a shape name, such as user.full`},
		{"-- name: A :one\n-- nonnull: a\n-- nonnull: b\nSELECT 1", "query-header: q.sql:3: query A: -- nonnull: is given twice"},
		{"-- name: A :one\n-- nonnull: a,\nSELECT 1", `query-header: q.sql:2: query A: -- nonnull: "" is not a column name; list result columns separated by commas`},
		{"-- name: A :one\n-- consistency: any\n-- consistency: any\nSELECT 1", "query-header: q.sql:3: query A: -- consistency: is given twice"},
		{"-- name: A :one\n-- consistency: often\nSELECT 1", `query-header: q.sql:2: query A: -- consistency: "often" is not a level; use primary, any, read-your-writes or bounded-staleness <duration>`},
		{"-- name: A :one\n-- consistency: bounded-staleness soon\nSELECT 1", `query-header: q.sql:2: query A: -- consistency: bounded-staleness takes a positive duration, such as 5s, not "soon"`},
		{"-- name: A :one\n-- consistency: bounded-staleness -1s\nSELECT 1", `query-header: q.sql:2: query A: -- consistency: bounded-staleness takes a positive duration, such as 5s, not "-1s"`},
		{"-- name: A :one\n-- consistency: bounded-stalenessX\nSELECT 1", `query-header: q.sql:2: query A: -- consistency: "bounded-stalenessX" is not a level; use primary, any, read-your-writes or bounded-staleness <duration>`},
	} {
		if got := blocks(c.text); got != c.want {
			t.Errorf("split(%q)\n got %s\nwant %s", c.text, got, c.want)
		}
	}
}
