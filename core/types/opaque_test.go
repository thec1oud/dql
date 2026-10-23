package types

import (
	"slices"
	"strings"
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// withCitext adds §8.3's nickname citext column to users, plus places that
// reach citext and geometry indirectly.
func withOpaque() *irv1.Bundle {
	b := shop()
	app := func(o string) *irv1.QualifiedName { return qn("shop", "app", o) }
	n := ns(b)
	n.Tables = append(n.Tables, &irv1.Table{Name: id("users"), Columns: []*irv1.Column{
		col("nickname", tOpaque("public", "citext")),
		col("aliases", tArray(tOpaque("public", "citext"), 1)),
		col("handle", tDomain(app("handle"))),
		col("area", tRange(tOpaque("public", "geometry"))),
		col("both", tWeak(tArray(tOpaque("public", "citext"), 1))),
	}})
	n.Domains = append(n.Domains,
		&irv1.Domain{Name: id("handle"), Base: tDomain(app("inner_handle"))},
		&irv1.Domain{Name: id("inner_handle"), Base: tOpaque("public", "citext")},
		// A loop must not hang the walk.
		&irv1.Domain{Name: id("loop"), Base: tDomain(app("loop"))},
	)
	return b
}

func TestCheckOpaqueUnmapped(t *testing.T) {
	ds := CheckOpaque(withOpaque(), nil, FallbackNone)
	want := []string{
		"type-opaque-unmapped shop.app.handle",
		"type-opaque-unmapped shop.app.inner_handle",
		"type-opaque-unmapped shop.app.users.aliases",
		"type-opaque-unmapped shop.app.users.area",
		"type-opaque-unmapped shop.app.users.both",
		"type-opaque-unmapped shop.app.users.handle",
		"type-opaque-unmapped shop.app.users.nickname",
	}
	if got := codes(ds); !slices.Equal(got, want) {
		t.Fatalf("codes = %q, want %q", got, want)
	}
	for _, d := range ds {
		if d.GetSeverity() != irv1.Severity_SEVERITY_ERROR {
			t.Errorf("%s: want an error", d.GetCode())
		}
	}
	msg := ds[len(ds)-1].GetMessage()
	for _, s := range []string{"column shop.app.users.nickname", "public.citext", "opaque-fallback"} {
		if !strings.Contains(msg, s) {
			t.Errorf("message %q lacks %q", msg, s)
		}
	}
}

func TestCheckOpaqueMapped(t *testing.T) {
	mapped := []*irv1.QualifiedName{{Namespace: "public", Object: "citext"}, {Namespace: "public", Object: "geometry"}}
	if ds := CheckOpaque(withOpaque(), mapped, FallbackNone); len(ds) != 0 {
		t.Fatalf("codes = %q, want none", codes(ds))
	}
	// Matching is on namespace and object: another namespace's citext is not mapped.
	other := []*irv1.QualifiedName{{Namespace: "ext", Object: "citext"}, {Namespace: "public", Object: "geometry"}}
	if got := len(CheckOpaque(withOpaque(), other, FallbackNone)); got != 6 {
		t.Errorf("%d diagnostics, want 6", got)
	}
}

func TestCheckOpaqueFallback(t *testing.T) {
	for _, fb := range []string{FallbackString, FallbackBytes} {
		ds := CheckOpaque(withOpaque(), []*irv1.QualifiedName{{Namespace: "public", Object: "citext"}}, fb)
		if got, want := codes(ds), []string{"type-opaque-fallback shop.app.users.area"}; !slices.Equal(got, want) {
			t.Fatalf("%s: codes = %q, want %q", fb, got, want)
		}
		if ds[0].GetSeverity() != irv1.Severity_SEVERITY_WARNING || !strings.Contains(ds[0].GetMessage(), "falls back to "+fb) {
			t.Errorf("%s: %v", fb, ds[0])
		}
	}
	// An invalid fallback counts as unset.
	if ds := CheckOpaque(withOpaque(), nil, "json"); ds[0].GetCode() != CodeOpaqueUnmapped {
		t.Errorf("invalid fallback: %q", codes(ds))
	}
}

func TestCheckOpaqueShopIsClean(t *testing.T) {
	if ds := CheckOpaque(shop(), nil, FallbackNone); len(ds) != 0 {
		t.Fatalf("codes = %q", codes(ds))
	}
}
