// Package sqlite is the sqlite dialect plugin: it publishes the capability
// profile of SQLite 3.38 and newer: RETURNING needs 3.35, and the JSON
// operator -> 3.38 (design record §2.3, §9.2). It runs in-process through the plugin.Plugin
// interface, and as the executable dql-plugin-sqlite (ADR 0002).
//
// How an operator renders as SQL stays private to the plugin (§9.2): it
// reaches the runtime only through the fragment table (§16.2, §18.4). The
// profile also carries the session setup and the error mapping (§22.7, §23).
package sqlite

import (
	"context"
	"fmt"
	"maps"
	"slices"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Name is the plugin's name.
const Name = "sqlite"

// Version is the plugin's version.
const Version = "1.0.0"

// Plugin publishes the SQLite capability profile on every invocation.
type Plugin struct{}

// New returns the sqlite dialect plugin.
func New() *Plugin { return &Plugin{} }

// Handshake declares a dialect plugin reading IR 1.x.
func (*Plugin) Handshake() *irv1.PluginHandshake {
	return &irv1.PluginHandshake{
		Plugin:     &irv1.PluginInfo{Name: Name, Kind: irv1.PluginKind_PLUGIN_KIND_DIALECT, Version: Version},
		IrVersions: &irv1.IrVersionRange{Min: "1.0.0", MaxMajor: 1},
	}
}

// Invoke returns a bundle holding the capability profile, with how SQLite
// reports a violation of each rule of the bundle's catalogs, and the
// fragment table of its catalogs and shapes. It refuses a bundle of
// another dialect.
func (*Plugin) Invoke(_ context.Context, in *irv1.Invoke) (*irv1.InvokeResult, error) {
	if d := in.GetBundle().GetDialect(); d != irv1.Dialect_DIALECT_SQLITE {
		return nil, fmt.Errorf("sqlite: the bundle's catalogs are %s, not SQLite", d)
	}
	p := Profile()
	p.Violations = Violations(in.GetBundle().GetCatalogs())
	return &irv1.InvokeResult{Bundle: &irv1.Bundle{Capabilities: p, Queries: &irv1.QueryLayer{Fragments: Fragments(in.GetBundle(), p)}}}, nil
}

// Profile returns SQLite's capability profile.
//
// The pattern operators need a deterministic collation, so that they mean
// what they mean on Postgres. LIKE ignores collations, and the session
// makes it case sensitive. There are no arrays, ranges or regular
// expressions. in and any take one list placeholder, a JSON array, which
// json_each expands (§25.3). Division and modulo by zero yield NULL rather
// than an error, so both are approximate, and lower() folds ASCII only, so
// ilike is too. JSON paths are not SQL/JSON paths, so json_path is left
// out, and SQLite has no JSON containment.
//
// Every connection enforces foreign keys and case-sensitive LIKE (§22.7).
// SQLite's extended result codes map into the taxonomy (§23.1). A
// statement binds at most 32,766 parameters (§25.2). There are no
// replicas, so every read goes to the database. A write's rows come back
// through RETURNING (§20.1). Statements are always prepared (§26.4).
func Profile() *irv1.CapabilityProfile {
	deterministic := &irv1.OperatorCondition{Collation: &irv1.Collation{Deterministic: irv1.Tristate_TRISTATE_TRUE}}
	var ops []*irv1.OperatorSupport
	for _, name := range slices.Sorted(maps.Keys(operatorTemplates)) {
		s := &irv1.OperatorSupport{Name: name, Fidelity: operatorFidelity(name)}
		if textOperators[name] {
			s.When = deterministic
		}
		ops = append(ops, s)
	}
	var codes []*irv1.ErrorCode
	for _, code := range slices.Sorted(maps.Keys(errorCodes)) {
		codes = append(codes, &irv1.ErrorCode{Code: code, Kind: errorCodes[code]})
	}
	return &irv1.CapabilityProfile{
		Dialect: irv1.Dialect_DIALECT_SQLITE, Operators: ops, SessionSetup: slices.Clone(sessionSetup), ErrorCodes: codes,
		MaxParams:         32766,
		ConsistencyLevels: []irv1.ConsistencyLevel{irv1.ConsistencyLevel_CONSISTENCY_PRIMARY},
		StatementModes:    []irv1.StatementMode{irv1.StatementMode_STATEMENT_MODE_PREPARE},
		ReturnedRows:      irv1.ReturnedRows_RETURNED_ROWS_CLAUSE,
	}
}

// operatorFidelity is an operator's fidelity: exact unless listed.
func operatorFidelity(name string) irv1.Fidelity {
	switch name {
	case "div", "mod", "ilike":
		return irv1.Fidelity_FIDELITY_APPROXIMATE
	case "json_get":
		return irv1.Fidelity_FIDELITY_EMULATED
	}
	return irv1.Fidelity_FIDELITY_EXACT
}

// textOperators are the pattern-matching operators, which compare text as
// it is stored.
var textOperators = map[string]bool{"contains": true, "ilike": true, "like": true, "starts_with": true}
