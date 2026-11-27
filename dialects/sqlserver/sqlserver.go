// Package sqlserver is the sqlserver dialect plugin: it publishes the
// capability profile of SQL Server 2022 and newer, which has IS DISTINCT
// FROM and OPENJSON (design record §2.3, §9.2). It runs in-process through
// the plugin.Plugin interface, and as the executable dql-plugin-sqlserver
// (ADR 0002).
//
// How an operator renders as SQL stays private to the plugin (§9.2): it
// reaches the runtime only through the fragment table (§16.2, §18.4). The
// profile also carries the session setup and the error mapping (§22.7, §23).
package sqlserver

import (
	"context"
	"fmt"
	"maps"
	"slices"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Name is the plugin's name.
const Name = "sqlserver"

// Version is the plugin's version.
const Version = "1.0.0"

// Plugin publishes the SQL Server capability profile on every invocation.
type Plugin struct{}

// New returns the sqlserver dialect plugin.
func New() *Plugin { return &Plugin{} }

// Handshake declares a dialect plugin reading IR 1.x.
func (*Plugin) Handshake() *irv1.PluginHandshake {
	return &irv1.PluginHandshake{
		Plugin:     &irv1.PluginInfo{Name: Name, Kind: irv1.PluginKind_PLUGIN_KIND_DIALECT, Version: Version},
		IrVersions: &irv1.IrVersionRange{Min: "1.0.0", MaxMajor: 1},
	}
}

// Invoke returns a bundle holding the capability profile, with how SQL
// Server reports a violation of each rule of the bundle's catalogs, and
// the fragment table of its catalogs and shapes. It refuses a bundle of
// another dialect.
func (*Plugin) Invoke(_ context.Context, in *irv1.Invoke) (*irv1.InvokeResult, error) {
	if d := in.GetBundle().GetDialect(); d != irv1.Dialect_DIALECT_SQLSERVER {
		return nil, fmt.Errorf("sqlserver: the bundle's catalogs are %s, not SQL Server", d)
	}
	p := Profile()
	p.Violations = Violations(in.GetBundle().GetCatalogs())
	return &irv1.InvokeResult{Bundle: &irv1.Bundle{Capabilities: p, Queries: &irv1.QueryLayer{Fragments: Fragments(in.GetBundle(), p)}}}, nil
}

// Profile returns SQL Server's capability profile.
//
// Every collation ignores trailing spaces, so the pattern operators need a
// binary one, the only kind that is deterministic, to mean what they mean
// on Postgres; LIKE then compares code units, as Postgres compares under C.
// lower() folds by the collation, so ilike is approximate. There are no
// arrays, ranges or regular expressions before SQL Server 2025, and json
// is text, so no JSON operators. in and any take one list placeholder, a
// JSON array, which OPENJSON expands (§25.3); on an expression that is no
// column its items are text, so they are approximate there.
//
// A statement binds at most 2,100 parameters (§25.2). Reads go to the
// primary: availability-group replicas are not routed yet, and
// read-your-writes always goes to the primary (decided 2026-11-26). A
// write's rows come back through OUTPUT (§20.1).
func Profile() *irv1.CapabilityProfile {
	binary := &irv1.OperatorCondition{Collation: &irv1.Collation{Deterministic: irv1.Tristate_TRISTATE_TRUE}}
	var ops []*irv1.OperatorSupport
	for _, name := range slices.Sorted(maps.Keys(operatorTemplates)) {
		s := &irv1.OperatorSupport{Name: name, Fidelity: operatorFidelity(name)}
		if textOperators[name] {
			s.When = binary
		}
		ops = append(ops, s)
	}
	var codes []*irv1.ErrorCode
	for _, code := range slices.Sorted(maps.Keys(errorCodes)) {
		codes = append(codes, &irv1.ErrorCode{Code: code, Kind: errorCodes[code]})
	}
	return &irv1.CapabilityProfile{
		Dialect: irv1.Dialect_DIALECT_SQLSERVER, Operators: ops, SessionSetup: slices.Clone(sessionSetup), ErrorCodes: codes,
		// A request binds at most 2100 parameters, and both drivers send a
		// statement through sp_executesql, whose text and declarations are
		// two of them. Inserts are a MERGE, whose source rows have no
		// limit, unlike an INSERT's VALUES at 1000.
		MaxParams:         2098,
		ConsistencyLevels: []irv1.ConsistencyLevel{irv1.ConsistencyLevel_CONSISTENCY_PRIMARY},
		StatementModes:    []irv1.StatementMode{irv1.StatementMode_STATEMENT_MODE_PREPARE},
		ReturnedRows:      irv1.ReturnedRows_RETURNED_ROWS_CLAUSE,
	}
}

// operatorFidelity is an operator's fidelity: exact unless listed.
func operatorFidelity(name string) irv1.Fidelity {
	switch name {
	case "ilike", "in", "any":
		return irv1.Fidelity_FIDELITY_APPROXIMATE
	}
	return irv1.Fidelity_FIDELITY_EXACT
}

// textOperators are the pattern-matching operators, which compare text as
// it is stored.
var textOperators = map[string]bool{"contains": true, "ilike": true, "like": true, "starts_with": true}
