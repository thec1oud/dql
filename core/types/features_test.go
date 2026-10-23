package types

import (
	"slices"
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func TestRequiredFeatures(t *testing.T) {
	billing := func(o string) *irv1.QualifiedName { return qn("billing", "", o) }
	cases := []struct {
		name   string
		mutate func(b *irv1.Bundle)
		want   []string
	}{
		// §1.3 as written has no opaque, weak or cross-catalog use.
		{"shop", func(*irv1.Bundle) {}, nil},
		{"opaque column", func(b *irv1.Bundle) {
			ns(b).Tables[0].Columns = append(ns(b).Tables[0].Columns, col("nickname", tOpaque("public", "citext")))
		}, []string{"opaque-types"}},
		{"opaque nested in a routine result", func(b *irv1.Bundle) {
			ns(b).Routines[0].Overloads[0].Returns.GetScalar().Type = tArray(tOpaque("public", "citext"), 1)
		}, []string{"opaque-types"}},
		{"opaque in a multirange", func(b *irv1.Bundle) { ns(b).Sequences[0].Type = tMultirange(tOpaque("public", "geometry")) }, []string{"opaque-types"}},
		{"weak and opaque", func(b *irv1.Bundle) {
			ns(b).Tables[0].Columns[0].Type = tWeak(tInt64)
			ns(b).Domains[0].Base = tOpaque("public", "citext")
		}, []string{"opaque-types", "weak-types"}},
		{"relation flagged", func(b *irv1.Bundle) { ns(b).Tables[0].Relations[0].CrossCatalog = true }, []string{"cross-catalog"}},
		{"relation to another catalog", func(b *irv1.Bundle) { ns(b).Tables[0].Relations[0].To.Table = billing("invoices") }, []string{"cross-catalog"}},
		{"relation through another catalog", func(b *irv1.Bundle) { ns(b).Tables[0].Relations[0].Through = billing("links") }, []string{"cross-catalog"}},
		{"foreign key", func(b *irv1.Bundle) { ns(b).Tables[0].ForeignKeys[0].References.Table = billing("users") }, []string{"cross-catalog"}},
		{"view dependency", func(b *irv1.Bundle) {
			ns(b).Tables[2].DependsOn = append(ns(b).Tables[2].DependsOn, billing("invoices"))
		}, []string{"cross-catalog"}},
		{"named type", func(b *irv1.Bundle) { ns(b).Tables[0].Columns[2].Type = tEnum(billing("status")) }, []string{"cross-catalog"}},
		{"all three", func(b *irv1.Bundle) {
			ns(b).Tables[0].Columns[2].Type = tArray(tEnum(billing("status")), 1)
			ns(b).Tables[0].Columns[0].Type = tWeak(tInt64)
			ns(b).Tables[0].Columns[1].Type = tOpaque("public", "citext")
		}, []string{"cross-catalog", "opaque-types", "weak-types"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := shop()
			c.mutate(b)
			if got := RequiredFeatures(b); !slices.Equal(got, c.want) {
				t.Errorf("RequiredFeatures = %q, want %q", got, c.want)
			}
		})
	}
}
