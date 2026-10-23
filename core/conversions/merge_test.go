package conversions

import (
	"fmt"
	"strings"
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func collated(c *irv1.Column, coll *irv1.Collation) *irv1.Column {
	c.Collation = coll
	return c
}

var (
	yes, no = irv1.Tristate_TRISTATE_TRUE, irv1.Tristate_TRISTATE_FALSE
	// A deterministic collation, as pg-live states it.
	exactCollation = &irv1.Collation{Name: "default", CaseSensitive: yes, AccentSensitive: yes, PadSpace: no, Deterministic: yes, ClientFold: irv1.ClientFold_CLIENT_FOLD_NONE}
)

// Merge by key for every keyed table (§8.9): users.id is an integer and
// compares exactly; countries.code is CHAR(2) under utf8mb4_0900_ai_ci, which
// ignores case and accents, so the merge is fallible; a certified fold makes
// handles total.
func TestMerges(t *testing.T) {
	ai := &irv1.Collation{Name: "utf8mb4_0900_ai_ci", CaseSensitive: no, AccentSensitive: no, PadSpace: no, Deterministic: yes, ClientFold: irv1.ClientFold_CLIENT_FOLD_NONE}
	citext := &irv1.Collation{Name: "citext", CaseSensitive: no, AccentSensitive: yes, PadSpace: no, Deterministic: yes, ClientFold: irv1.ClientFold_CLIENT_FOLD_ASCII_LOWER}
	char2 := &irv1.Type{Kind: &irv1.Type_Char{Char: &irv1.Char{Length: 2}}}
	decimal := &irv1.Type{Kind: &irv1.Type_Decimal{Decimal: &irv1.Decimal{}}}
	view := table("published_posts", []string{"published", "post"}, nil, col("id", i64Type))
	view.Kind = irv1.TableKind_RELATION_KIND_VIEW
	view.LogicalKey = &irv1.LogicalKey{Columns: []string{"id"}}
	logical := table("events", []string{"event"}, nil, col("seq", i64Type))
	logical.LogicalKey = &irv1.LogicalKey{Columns: []string{"seq"}}
	tables := []*irv1.Table{
		users(),
		table("countries", []string{"country"}, []string{"code"}, collated(col("code", char2), ai)),
		table("handles", []string{"handle"}, []string{"handle"}, collated(col("handle", &irv1.Type{Kind: &irv1.Type_Opaque{Opaque: &irv1.Opaque{}}}), citext)),
		table("tags", []string{"tag"}, []string{"name"}, col("name", domainType("slug_text"))),
		table("slugs", []string{"slug"}, []string{"slug"}, collated(col("slug", domainType("slug_text")), &irv1.Collation{Name: "und-x-icu"})),
		table("prices", []string{"price"}, []string{"amount", "status"}, col("amount", decimal), col("status", enumType("post_status"))),
		table("notes", []string{"note"}, []string{"body"}, col("body", textType)),
		table("loops", []string{"loop"}, []string{"a", "b", "c"}, col("a", domainType("ping")), col("b", domainType("ghost")), col("c", &irv1.Type{})),
		table("logs", []string{"log"}, nil, col("line", textType)),
		view,
		logical,
	}
	domains := []*irv1.Domain{
		{Name: ident("slug_text"), Base: domainType("inner_text"), Collation: exactCollation},
		{Name: ident("inner_text"), Base: textType},
		{Name: ident("ping"), Base: domainType("pong")},
		{Name: ident("pong"), Base: domainType("ping")},
	}
	b := bundleOf(tables, domains)
	if ds := Derive(b, nil); len(ds) != 0 {
		t.Fatalf("unexpected diagnostics: %v", ds)
	}
	var got []string
	for _, m := range b.GetShapes().GetMerges() {
		var keys []string
		for _, k := range m.GetKey() {
			s := k.GetColumn() + ":" + strings.TrimPrefix(k.GetComparison().String(), "COMPARISON_")
			if k.GetFold() != irv1.ClientFold_CLIENT_FOLD_UNSPECIFIED {
				s += "/" + strings.TrimPrefix(k.GetFold().String(), "CLIENT_FOLD_")
			}
			keys = append(keys, s)
		}
		got = append(got, fmt.Sprintf("%s %s %s", m.GetEntity().GetObject(), strings.TrimPrefix(m.GetKind().String(), "CONVERSION_"), strings.Join(keys, " ")))
	}
	assertLines(t, got, []string{
		"countries FALLIBLE code:INEXACT",
		"events TOTAL seq:EXACT",
		"handles TOTAL handle:FOLDED/ASCII_LOWER",
		"loops FALLIBLE a:INEXACT b:INEXACT c:INEXACT",
		"notes FALLIBLE body:INEXACT",
		"prices FALLIBLE amount:INEXACT status:EXACT",
		"slugs FALLIBLE slug:INEXACT",
		"tags TOTAL name:EXACT",
		"users TOTAL id:EXACT",
	})
}
