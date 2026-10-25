// Package postgres is the postgres dialect plugin: it publishes the
// capability profile of Postgres 15 and newer (design record §2.3, §9.2).
// It runs in-process through the plugin.Plugin interface, and as the
// executable dql-plugin-postgres (ADR 0002).
//
// How an operator renders as SQL stays private to the plugin (§9.2): it
// reaches the runtime only through the fragment table (§16.2, §18.4). The
// profile also carries the session setup and the error mapping (§22.7, §23).
package postgres

import (
	"context"
	"fmt"
	"maps"
	"slices"

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

// Invoke returns a bundle holding the capability profile, with how
// Postgres reports a violation of each rule of the bundle's catalogs, and
// the fragment table of its catalogs and shapes. It refuses a bundle of
// another dialect.
func (*Plugin) Invoke(_ context.Context, in *irv1.Invoke) (*irv1.InvokeResult, error) {
	if d := in.GetBundle().GetDialect(); d != irv1.Dialect_DIALECT_POSTGRES {
		return nil, fmt.Errorf("postgres: the bundle's catalogs are %s, not Postgres", d)
	}
	p := Profile()
	p.Violations = Violations(in.GetBundle().GetCatalogs())
	return &irv1.InvokeResult{Bundle: &irv1.Bundle{Capabilities: p, Queries: &irv1.QueryLayer{Fragments: Fragments(in.GetBundle(), p)}}}, nil
}

// Profile returns the capability profile of Postgres 15 and newer. Every
// operator of the catalog is exact (§9.1, §9.2). The text operators need a
// deterministic collation: before Postgres 18, LIKE, ILIKE, starts_with and
// regular expressions reject a nondeterministic one, so a column with it
// does not get them. An array column gets no any or in, whose list would
// be an array of arrays. No reference may cross catalogs (§5.1). Every connection runs in UTC (§22.7), and
// SQLSTATEs map into the error taxonomy (§23.1). A statement binds at most
// 65,535 parameters (§25.2); replicas serve every consistency level through
// WAL positions (§22.5); pgx sends statements in all three modes (§26.4).
func Profile() *irv1.CapabilityProfile {
	exact := irv1.Fidelity_FIDELITY_EXACT
	deterministic := &irv1.OperatorCondition{Collation: &irv1.Collation{Deterministic: irv1.Tristate_TRISTATE_TRUE}}
	scalar := &irv1.OperatorCondition{Scalar: true}
	var ops []*irv1.OperatorSupport
	for _, name := range []string{
		"add", "any", "array_contains", "array_overlaps", "between", "contains", "div", "eq", "ge", "gt",
		"ilike", "in", "is_distinct_from", "is_not_null", "is_null", "json_contains", "json_get", "json_path",
		"le", "like", "lt", "mod", "mul", "ne", "neg", "range_contains", "range_overlaps", "regex_match",
		"starts_with", "sub",
	} {
		s := &irv1.OperatorSupport{Name: name, Fidelity: exact}
		switch {
		case textOperators[name]:
			s.When = deterministic
		case listOperators[name]:
			s.When = scalar
		}
		ops = append(ops, s)
	}
	var codes []*irv1.ErrorCode
	for _, code := range slices.Sorted(maps.Keys(errorCodes)) {
		codes = append(codes, &irv1.ErrorCode{Code: code, Kind: errorCodes[code]})
	}
	return &irv1.CapabilityProfile{
		Dialect: irv1.Dialect_DIALECT_POSTGRES, Operators: ops, SessionSetup: slices.Clone(sessionSetup), ErrorCodes: codes,
		MaxParams: 65535,
		ConsistencyLevels: []irv1.ConsistencyLevel{
			irv1.ConsistencyLevel_CONSISTENCY_PRIMARY, irv1.ConsistencyLevel_CONSISTENCY_ANY,
			irv1.ConsistencyLevel_CONSISTENCY_BOUNDED_STALENESS, irv1.ConsistencyLevel_CONSISTENCY_READ_YOUR_WRITES,
		},
		Replication: &irv1.ReplicationQueries{Position: position, Lag: lag, Replayed: replayed},
		StatementModes: []irv1.StatementMode{
			irv1.StatementMode_STATEMENT_MODE_PREPARE, irv1.StatementMode_STATEMENT_MODE_DESCRIBE, irv1.StatementMode_STATEMENT_MODE_SIMPLE,
		},
	}
}

// Replica positions are WAL positions (§22.5). A commit's position is the
// primary's current WAL position once it returns. A replica's lag is the
// age of the last transaction it replayed, or none when it has replayed all
// it received, so an idle primary does not make its replicas look behind.
const (
	position = "SELECT pg_current_wal_lsn()::text"
	lag      = "SELECT (CASE WHEN NOT pg_is_in_recovery() THEN NULL " +
		"WHEN pg_last_wal_receive_lsn() = pg_last_wal_replay_lsn() THEN 0 " +
		"ELSE extract(epoch FROM now() - pg_last_xact_replay_timestamp()) END)::float8"
	replayed = "SELECT pg_last_wal_replay_lsn() >= ALL (SELECT p::pg_lsn FROM unnest($1::text[]) p)"
)

// listOperators take a list of the column's values, which for an array
// column would be an array of arrays; Postgres has none.
var listOperators = map[string]bool{"any": true, "in": true}

// textOperators are the pattern-matching operators Postgres before 18 runs
// only under a deterministic collation.
var textOperators = map[string]bool{"contains": true, "ilike": true, "like": true, "regex_match": true, "starts_with": true}
