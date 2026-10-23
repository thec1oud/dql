package pgsql

import (
	"testing"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func TestCollationFacts(t *testing.T) {
	u, f, tr := irv1.Tristate_TRISTATE_UNSPECIFIED, irv1.Tristate_TRISTATE_FALSE, irv1.Tristate_TRISTATE_TRUE
	none := irv1.ClientFold_CLIENT_FOLD_NONE
	icu := func(locale string) CollationDef { return CollationDef{Name: "s.x", ICU: true, ICULocale: locale} }
	for _, c := range []struct {
		def      CollationDef
		ns, base string
		want     *irv1.Collation
	}{
		{CollationDef{Name: "default", Deterministic: true}, "pg_catalog", "text",
			&irv1.Collation{Name: "default", CaseSensitive: tr, AccentSensitive: tr, PadSpace: f, Deterministic: tr, ClientFold: none}},
		{CollationDef{Name: "default", Deterministic: true}, "pg_catalog", "bpchar",
			&irv1.Collation{Name: "default", CaseSensitive: tr, AccentSensitive: tr, PadSpace: tr, Deterministic: tr}},
		{CollationDef{Name: "default", Deterministic: true}, "ext", "citext", &irv1.Collation{Name: "default"}},
		{icu("und-u-ks-level1"), "pg_catalog", "text", &irv1.Collation{Name: "s.x", CaseSensitive: f, AccentSensitive: f, Deterministic: f}},
		{icu("und-u-ks-level2"), "pg_catalog", "text", &irv1.Collation{Name: "s.x", CaseSensitive: f, AccentSensitive: tr, Deterministic: f}},
		{icu("und-u-ks-level2-kc"), "pg_catalog", "text", &irv1.Collation{Name: "s.x", CaseSensitive: u, AccentSensitive: tr, Deterministic: f}},
		{icu("und-u-kc-false-ks-level2"), "pg_catalog", "text", &irv1.Collation{Name: "s.x", CaseSensitive: f, AccentSensitive: tr, Deterministic: f}},
		{icu("und-u-ks-level3"), "pg_catalog", "text", &irv1.Collation{Name: "s.x", Deterministic: f}},
		{icu("und-u-ks"), "pg_catalog", "text", &irv1.Collation{Name: "s.x", Deterministic: f}},
		{CollationDef{Name: "s.x"}, "pg_catalog", "bpchar", &irv1.Collation{Name: "s.x", PadSpace: tr, Deterministic: f}},
	} {
		if got := CollationFacts(c.def, c.ns, c.base); !proto.Equal(got, c.want) {
			t.Errorf("%+v on %s.%s: %v, want %v", c.def, c.ns, c.base, got, c.want)
		}
	}
}
