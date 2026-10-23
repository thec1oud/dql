package naming

import (
	"slices"
	"testing"
)

// The host-name table of §4.3, at the word level. The Go names add emitter
// casing and the shape suffix: [user] becomes UserFull. shop.app.users carries
// the running example's name annotation, so its base is [user] (§4.4).
func TestEntityWords(t *testing.T) {
	shop, billing := Words("shop"), Words("billing")
	user := []string{"user"}
	cases := []struct {
		name string
		base []string
		p    Placement
		want []string
	}{
		{"shop.app.users, default namespace app, shop only", user,
			Placement{CatalogWords: shop, NamespaceWords: Words("app"), DefaultNamespace: true},
			[]string{"user"}}, // UserFull
		{"shop.audit.users, shop only", Words("users"),
			Placement{CatalogWords: shop, NamespaceWords: Words("audit")},
			[]string{"audit", "users"}}, // AuditUsersFull
		{"shop.app.users, shop default catalog, billing", user,
			Placement{CatalogWords: shop, NamespaceWords: Words("app"), MultiCatalog: true, DefaultCatalog: true, DefaultNamespace: true},
			[]string{"user"}}, // UserFull
		{"billing.public.invoices, shop default catalog", Words("invoices"),
			Placement{CatalogWords: billing, NamespaceWords: Words("public"), MultiCatalog: true, DefaultNamespace: true},
			[]string{"billing", "invoices"}}, // BillingInvoicesFull
		{"billing.public.invoices, no default catalog", Words("invoices"),
			Placement{CatalogWords: billing, NamespaceWords: Words("public"), MultiCatalog: true, DefaultNamespace: true},
			[]string{"billing", "invoices"}}, // BillingInvoicesFull
		{"shop.app.users, no default catalog", user,
			Placement{CatalogWords: shop, NamespaceWords: Words("app"), MultiCatalog: true, DefaultNamespace: true},
			[]string{"shop", "user"}}, // ShopUserFull

		// Catalog before namespace (§4.3).
		{"both prefixes", Words("users"),
			Placement{CatalogWords: billing, NamespaceWords: Words("audit"), MultiCatalog: true},
			[]string{"billing", "audit", "users"}},
		// A single-catalog bundle never takes a catalog prefix.
		{"one catalog, not marked default", Words("users"),
			Placement{CatalogWords: shop, DefaultNamespace: true},
			[]string{"users"}},
		{"multi-word prefixes", Words("OrderLines"),
			Placement{CatalogWords: Words("ShopEU"), NamespaceWords: Words("sales_v2"), MultiCatalog: true},
			[]string{"shop", "eu", "sales", "v2", "order", "lines"}},
	}
	for _, c := range cases {
		if got := EntityWords(c.base, c.p); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestEntityWordsDoesNotAliasBase(t *testing.T) {
	base := make([]string, 1, 4)
	base[0] = "users"
	got := EntityWords(base, Placement{NamespaceWords: []string{"audit"}})
	got[0] = "changed"
	if base[0] != "users" {
		t.Fatal("result shares its array with base")
	}
	got = EntityWords(base, Placement{DefaultNamespace: true})
	got[0] = "changed"
	if base[0] != "users" {
		t.Fatal("result shares its array with base")
	}
}
