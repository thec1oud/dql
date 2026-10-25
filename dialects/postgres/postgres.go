// Package postgres is the postgres dialect plugin: it publishes the
// capability profile of Postgres 15 and newer (design record §2.3, §9.2).
// It runs in-process through the plugin.Plugin interface, and as the
// executable dql-plugin-postgres (ADR 0002).
//
// How an operator renders as SQL stays private to the plugin (§9.2); the
// fragment table that carries it arrives with the query layer (§16.2).
package postgres

import (
	"context"
	"fmt"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Name is the plugin's name.
const Name = "postgres"

// Version is the plugin's version.
const Version = "1.0.0"

// Plugin publishes the Postgres capability profile on every invocation.
type Plugin struct{}

// New returns the postgres dialect plugin.
func New() *Plugin { return &Plugin{} }

// Handshake declares a dialect plugin reading IR 1.x.
func (*Plugin) Handshake() *irv1.PluginHandshake {
	return &irv1.PluginHandshake{
		Plugin:     &irv1.PluginInfo{Name: Name, Kind: irv1.PluginKind_PLUGIN_KIND_DIALECT, Version: Version},
		IrVersions: &irv1.IrVersionRange{Min: "1.0.0", MaxMajor: 1},
	}
}

// Invoke returns a bundle holding the capability profile. It refuses a
// bundle of another dialect.
func (*Plugin) Invoke(_ context.Context, in *irv1.Invoke) (*irv1.InvokeResult, error) {
	if d := in.GetBundle().GetDialect(); d != irv1.Dialect_DIALECT_POSTGRES {
		return nil, fmt.Errorf("postgres: the bundle's catalogs are %s, not Postgres", d)
	}
	return &irv1.InvokeResult{Bundle: &irv1.Bundle{Capabilities: Profile()}}, nil
}

// Profile returns the capability profile of Postgres 15 and newer. Every
// operator of the catalog is exact (§9.1, §9.2). The text operators need a
// deterministic collation: before Postgres 18, LIKE, ILIKE, starts_with and
// regular expressions reject a nondeterministic one, so a column with it
// does not get them. No reference may cross catalogs (§5.1).
func Profile() *irv1.CapabilityProfile {
	exact := irv1.Fidelity_FIDELITY_EXACT
	deterministic := &irv1.OperatorCondition{Collation: &irv1.Collation{Deterministic: irv1.Tristate_TRISTATE_TRUE}}
	var ops []*irv1.OperatorSupport
	for _, name := range []string{
		"add", "any", "array_contains", "array_overlaps", "between", "contains", "div", "eq", "ge", "gt",
		"ilike", "in", "is_distinct_from", "is_not_null", "is_null", "json_contains", "json_get", "json_path",
		"le", "like", "lt", "mod", "mul", "ne", "neg", "range_contains", "range_overlaps", "regex_match",
		"starts_with", "sub",
	} {
		s := &irv1.OperatorSupport{Name: name, Fidelity: exact}
		if textOperators[name] {
			s.When = deterministic
		}
		ops = append(ops, s)
	}
	return &irv1.CapabilityProfile{Dialect: irv1.Dialect_DIALECT_POSTGRES, Operators: ops}
}

// textOperators are the pattern-matching operators Postgres before 18 runs
// only under a deterministic collation.
var textOperators = map[string]bool{"contains": true, "ilike": true, "like": true, "regex_match": true, "starts_with": true}
