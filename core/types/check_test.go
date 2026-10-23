package types

import (
	"slices"
	"strings"
	"testing"

	"github.com/thec1oud/dql/core/bundle"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/testkit/determinism"
)

func TestCheckShopIsClean(t *testing.T) {
	if ds := Check(shop()); len(ds) != 0 {
		t.Fatalf("Check(shop) = %q", codes(ds))
	}
}

func TestCheck(t *testing.T) {
	app := func(o string) *irv1.QualifiedName { return qn("shop", "app", o) }
	cases := []struct {
		name   string
		mutate func(b *irv1.Bundle)
		want   []string
		in     string // a substring of the first message
	}{
		{"column without type", func(b *irv1.Bundle) { ns(b).Tables[0].Columns[0].Type = nil },
			[]string{"type-missing shop.app.posts.id"}, "column shop.app.posts.id has no logical type"},
		{"type without kind", func(b *irv1.Bundle) { ns(b).Domains[0].Base = &irv1.Type{} },
			[]string{"type-missing shop.app.slug_text"}, "domain shop.app.slug_text"},
		{"empty named reference", func(b *irv1.Bundle) {
			ns(b).Tables[0].Columns[2].Type = &irv1.Type{Kind: &irv1.Type_Named{Named: &irv1.NamedRef{}}}
		}, []string{"type-reference-empty shop.app.posts.status"}, "names no enum"},
		{"unresolved enum", func(b *irv1.Bundle) { ns(b).Tables[0].Columns[2].Type = tEnum(app("order_status")) },
			[]string{"type-reference-unresolved shop.app.posts.status"}, "references enum shop.app.order_status"},
		{"unresolved domain", func(b *irv1.Bundle) { ns(b).Tables[1].Columns[0].Type = tDomain(app("nope")) },
			[]string{"type-reference-unresolved shop.app.tags.name"}, "references domain shop.app.nope"},
		{"unresolved composite", func(b *irv1.Bundle) { ns(b).Tables[0].Columns[3].Type = tComposite(app("nope")) },
			[]string{"type-reference-unresolved shop.app.posts.price"}, "references composite type shop.app.nope"},
		{"reference in another catalog", func(b *irv1.Bundle) { ns(b).Tables[0].Columns[2].Type = tEnum(qn("billing", "app", "post_status")) },
			[]string{"type-reference-unresolved shop.app.posts.status"}, "billing.app.post_status"},
		{"array without element", func(b *irv1.Bundle) { ns(b).Tables[0].Columns[4].Type = tArray(nil, 1) },
			[]string{"type-array-element-missing shop.app.posts.labels"}, "array without an element type"},
		{"array without dimensions", func(b *irv1.Bundle) { ns(b).Tables[0].Columns[4].Type = tArray(tText, 0) },
			[]string{"type-array-dimensions shop.app.posts.labels"}, "0 dimensions"},
		{"array with neither", func(b *irv1.Bundle) { ns(b).Tables[0].Columns[4].Type = tArray(nil, 0) },
			[]string{"type-array-dimensions shop.app.posts.labels", "type-array-element-missing shop.app.posts.labels"}, ""},
		{"bad element", func(b *irv1.Bundle) { ns(b).Tables[0].Columns[4].Type = tArray(tArray(tText, 0), 1) },
			[]string{"type-array-dimensions shop.app.posts.labels"}, "the element type of column"},
		{"range without element", func(b *irv1.Bundle) { ns(b).Sequences[0].Type = tRange(nil) },
			[]string{"type-range-element-missing shop.app.posts_id_seq"}, "sequence shop.app.posts_id_seq is a range"},
		{"multirange with bad element", func(b *irv1.Bundle) { ns(b).Sequences[0].Type = tMultirange(&irv1.Type{}) },
			[]string{"type-missing shop.app.posts_id_seq"}, "the element type of sequence"},
		{"good range", func(b *irv1.Bundle) { ns(b).Sequences[0].Type = tRange(tInt64) }, nil, ""},
		{"weak without affinity", func(b *irv1.Bundle) { ns(b).Composites[0].Attributes[0].Type = tWeak(nil) },
			[]string{"type-weak-affinity-missing shop.app.money_amount.currency", "type-weak-column shop.app.money_amount.currency"}, "attribute shop.app.money_amount.currency"},
		{"weak with bad affinity", func(b *irv1.Bundle) { ns(b).Routines[0].Overloads[0].Arguments[0].Type = tWeak(&irv1.Type{}) },
			[]string{"type-missing shop.app.post_score.p_id"}, "the affinity of argument 1 of shop.app.post_score(int64)"},
		{"weak column warns", func(b *irv1.Bundle) { ns(b).Tables[0].Columns[0].Type = tWeak(tInt64) },
			[]string{"type-weak-column shop.app.posts.id"}, "is weak"},
		{"weak result column warns", func(b *irv1.Bundle) {
			ns(b).Routines[0].Overloads[2].Returns.GetTable().Columns[0].Type = tWeak(tInt64)
		}, []string{"type-weak-column shop.app.post_score.score"}, "result column score of shop.app.post_score()"},
		{"weak argument does not warn", func(b *irv1.Bundle) { ns(b).Routines[0].Overloads[1].Arguments[1].Type = tWeak(tInt32) }, nil, ""},
		{"opaque without native", func(b *irv1.Bundle) {
			ns(b).Routines[0].Overloads[0].Returns.GetScalar().Type = &irv1.Type{Kind: &irv1.Type_Opaque{Opaque: &irv1.Opaque{}}}
		}, []string{"type-opaque-native-missing shop.app.post_score"}, "the result of shop.app.post_score(int64)"},
		{"opaque set result", func(b *irv1.Bundle) {
			ns(b).Routines[0].Overloads[1].Returns.GetSetOf().Type = &irv1.Type{Kind: &irv1.Type_Opaque{Opaque: &irv1.Opaque{}}, Native: &irv1.NativeType{Raw: "x"}}
		}, []string{"type-opaque-native-missing shop.app.post_score"}, "the result of shop.app.post_score(int64, int32)"},
		{"opaque with native", func(b *irv1.Bundle) { ns(b).Tables[0].Columns[0].Type = tOpaque("public", "citext") }, nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := shop()
			c.mutate(b)
			ds := Check(b)
			if got := codes(ds); !slices.Equal(got, c.want) {
				t.Fatalf("codes = %q, want %q", got, c.want)
			}
			if c.in != "" && !strings.Contains(ds[0].GetMessage(), c.in) {
				t.Errorf("message %q does not contain %q", ds[0].GetMessage(), c.in)
			}
			for _, d := range ds {
				if d.GetCode() == CodeWeakColumn {
					if d.GetSeverity() != irv1.Severity_SEVERITY_WARNING {
						t.Errorf("%s must be a warning", d.GetCode())
					}
				} else if d.GetSeverity() != irv1.Severity_SEVERITY_ERROR {
					t.Errorf("%s must be an error", d.GetCode())
				}
			}
		})
	}
}

func TestCheckSortsAndIsDeterministic(t *testing.T) {
	gen := func() ([]byte, error) {
		b := shop()
		ns(b).Tables[0].Columns[0].Type = tWeak(tInt64)
		ns(b).Tables[0].Columns[4].Type = tArray(nil, 0)
		ns(b).Domains[0].Base = nil
		return bundle.MarshalJSON(&irv1.Bundle{Diagnostics: Check(b)})
	}
	if err := determinism.Repeat(10, gen); err != nil {
		t.Fatal(err)
	}
	b := shop()
	ns(b).Tables[0].Columns[0].Type = tWeak(tInt64)
	ns(b).Domains[0].Base = nil
	want := []string{"type-missing shop.app.slug_text", "type-weak-column shop.app.posts.id"}
	if got := codes(Check(b)); !slices.Equal(got, want) {
		t.Errorf("codes = %q, want %q (errors first)", got, want)
	}
}

func TestUnderlying(t *testing.T) {
	b := shop()
	app := func(o string) *irv1.QualifiedName { return qn("shop", "app", o) }
	ns(b).Domains = append(ns(b).Domains,
		&irv1.Domain{Name: id("short_slug"), Base: tDomain(app("slug_text"))},
		&irv1.Domain{Name: id("loop_a"), Base: tDomain(app("loop_b"))},
		&irv1.Domain{Name: id("loop_b"), Base: tDomain(app("loop_a"))},
	)
	ix := NewIndex(b)
	if got := ix.Underlying(tDomain(app("short_slug"))); got != tText {
		t.Errorf("Underlying(short_slug) = %v, want text", got)
	}
	if got := ix.Underlying(tInt64); got != tInt64 {
		t.Errorf("Underlying(int64) = %v", got)
	}
	if got := ix.Underlying(tDomain(app("loop_a"))); got != nil {
		t.Errorf("Underlying(loop) = %v, want nil", got)
	}
	if got := ix.Underlying(tDomain(app("missing"))); got != nil {
		t.Errorf("Underlying(missing) = %v, want nil", got)
	}
	if ix.Enum(app("post_status")) == nil || ix.Composite(app("money_amount")) == nil || ix.Domain(app("slug_text")) == nil {
		t.Error("index lookups failed")
	}
}
