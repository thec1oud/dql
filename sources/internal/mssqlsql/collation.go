package mssqlsql

import (
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Collation returns a text column's comparison facts (§8.9), read from the
// collation's name, which SQL Server builds from fixed parts: _BIN and
// _BIN2 compare code points, _CS and _CI are case sensitive or not, and _AS
// and _AI accent sensitive or not. Equality under every collation ignores
// trailing spaces, so padSpace is always true and no collation is exact
// (§10.2). Only a binary collation is deterministic; none is certified for
// client-side comparison.
func Collation(name string) *irv1.Collation {
	t, f := irv1.Tristate_TRISTATE_TRUE, irv1.Tristate_TRISTATE_FALSE
	c := &irv1.Collation{Name: name, PadSpace: t, Deterministic: f}
	has := map[string]bool{}
	for _, p := range strings.Split(strings.ToUpper(name), "_") {
		has[p] = true
	}
	switch {
	case has["BIN"] || has["BIN2"]:
		c.CaseSensitive, c.AccentSensitive, c.Deterministic = t, t, t
	case has["CS"] || has["CI"]:
		c.CaseSensitive, c.AccentSensitive = f, f
		if has["CS"] {
			c.CaseSensitive = t
		}
		if has["AS"] {
			c.AccentSensitive = t
		}
	}
	return c
}
