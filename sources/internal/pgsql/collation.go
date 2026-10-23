package pgsql

import (
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// CollationDef is what a source knows of a collation.
type CollationDef struct {
	// Name is the collation's name as the IR records it: unqualified for
	// pg_catalog's collations, such as "default" or "C", and namespace.name
	// for any other.
	Name string
	// Deterministic is set for a deterministic collation.
	Deterministic bool
	// ICU marks a collation of the ICU provider, whose comparison strength
	// ICULocale states.
	ICU       bool
	ICULocale string
}

// textTypes are the pg_catalog types whose comparison a collation decides.
var textTypes = map[string]bool{"text": true, "varchar": true, "bpchar": true, "name": true}

// CollationFacts turns the collation of a column, attribute or domain into
// its comparison facts (§8.9). base names the type compared: the column's
// type with domains resolved and, for an array, its element type. It states
// only what Postgres guarantees:
//
//   - A deterministic collation compares equal only strings that are equal
//     byte for byte, so it is case- and accent-sensitive and comparison is
//     exact, which certifies CLIENT_FOLD_NONE.
//   - A nondeterministic ICU collation of strength level1 ignores case and
//     accents, and of strength level2 ignores case only. Any other is unknown.
//   - char(n) ignores trailing spaces, so it pads, and no client fold matches.
//
// Facts are stated only for the text types; the comparison of any other
// collatable type, such as an extension's, is its type plugin's to declare
// (§8.3), so those facts stay unknown.
func CollationFacts(def CollationDef, baseNamespace, baseName string) *irv1.Collation {
	out := &irv1.Collation{Name: def.Name}
	if baseNamespace != "pg_catalog" || !textTypes[baseName] {
		return out
	}
	pads := baseName == "bpchar"
	if pads {
		out.PadSpace = irv1.Tristate_TRISTATE_TRUE
	}
	if def.Deterministic {
		out.CaseSensitive, out.AccentSensitive, out.Deterministic = tristate(true), tristate(true), tristate(true)
		if !pads {
			out.PadSpace, out.ClientFold = tristate(false), irv1.ClientFold_CLIENT_FOLD_NONE
		}
		return out
	}
	out.Deterministic = tristate(false)
	if def.ICU {
		out.CaseSensitive, out.AccentSensitive = icuStrength(def.ICULocale)
	}
	return out
}

func tristate(b bool) irv1.Tristate {
	if b {
		return irv1.Tristate_TRISTATE_TRUE
	}
	return irv1.Tristate_TRISTATE_FALSE
}

// icuStrength reads the comparison strength from an ICU locale's -u-
// extension: ks-level1 ignores case and accents, ks-level2 case only. The
// kc (case level) keyword makes case count again at those strengths, so
// with it case sensitivity is left unknown.
func icuStrength(locale string) (caseSensitive, accentSensitive irv1.Tristate) {
	tokens := strings.Split(strings.ToLower(locale), "-")
	caseLevel := false
	level := ""
	for i, t := range tokens {
		switch {
		case t == "kc":
			caseLevel = i+1 == len(tokens) || tokens[i+1] != "false"
		case t == "ks" && i+1 < len(tokens):
			level = tokens[i+1]
		}
	}
	switch level {
	case "level1":
		caseSensitive, accentSensitive = tristate(false), tristate(false)
	case "level2":
		caseSensitive, accentSensitive = tristate(false), tristate(true)
	default:
		return irv1.Tristate_TRISTATE_UNSPECIFIED, irv1.Tristate_TRISTATE_UNSPECIFIED
	}
	if caseLevel {
		caseSensitive = irv1.Tristate_TRISTATE_UNSPECIFIED
	}
	return caseSensitive, accentSensitive
}
