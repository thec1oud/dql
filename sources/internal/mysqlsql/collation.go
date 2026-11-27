package mysqlsql

import (
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Collation returns a text column's comparison facts (§8.9), read from the
// collation's name, which MySQL and MariaDB build from fixed suffixes: _bin
// compares the encoded bytes, _cs and _ci are case sensitive or not, _as and
// _ai accent sensitive or not, and a legacy _ci collation without _ai or _as
// ignores accents. padSpace is the collation's pad attribute, which the
// server reports.
//
// Only a binary collation is deterministic, equal strings having equal
// bytes, and only it is certified for client-side comparison, with no
// folding (§8.9).
func Collation(name string, padSpace bool) *irv1.Collation {
	c := &irv1.Collation{Name: name, PadSpace: tristate(padSpace), Deterministic: irv1.Tristate_TRISTATE_FALSE}
	parts := strings.Split(strings.ToLower(name), "_")
	has := func(s string) bool {
		for _, p := range parts[1:] {
			if p == s {
				return true
			}
		}
		return false
	}
	switch {
	case has("bin") || name == "binary":
		c.CaseSensitive, c.AccentSensitive = irv1.Tristate_TRISTATE_TRUE, irv1.Tristate_TRISTATE_TRUE
		c.Deterministic, c.ClientFold = irv1.Tristate_TRISTATE_TRUE, irv1.ClientFold_CLIENT_FOLD_NONE
	case has("cs"):
		c.CaseSensitive = irv1.Tristate_TRISTATE_TRUE
		c.AccentSensitive = tristate(!has("ai"))
	case has("ci"):
		c.CaseSensitive = irv1.Tristate_TRISTATE_FALSE
		c.AccentSensitive = tristate(has("as"))
	}
	return c
}

func tristate(b bool) irv1.Tristate {
	if b {
		return irv1.Tristate_TRISTATE_TRUE
	}
	return irv1.Tristate_TRISTATE_FALSE
}

// Exact reports whether comparisons under c are exact: case and accent
// sensitive, without padding and deterministic, so that equality in SQL is
// equality of the strings.
func Exact(c *irv1.Collation) bool {
	return c.GetDeterministic() == irv1.Tristate_TRISTATE_TRUE && c.GetPadSpace() == irv1.Tristate_TRISTATE_FALSE
}
