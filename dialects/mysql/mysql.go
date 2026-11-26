// Package mysql is the mysql dialect plugin: it publishes the capability
// profile of MySQL 8.4 and MariaDB 11.4 and newer (design record §2.3,
// §9.2). Both speak the same protocol and most of the same SQL, so MariaDB
// is a profile of this plugin, chosen by the flavor mysql-live records on
// the catalog. It runs in-process through the plugin.Plugin interface, and
// as the executable dql-plugin-mysql (ADR 0002).
//
// How an operator renders as SQL stays private to the plugin (§9.2): it
// reaches the runtime only through the fragment table (§16.2, §18.4). The
// profile also carries the session setup and the error mapping (§22.7, §23).
package mysql

import (
	"context"
	"fmt"
	"maps"
	"slices"

	mysqlextv1 "github.com/thec1oud/dql/proto/dql/ext/mysql/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Name is the plugin's name.
const Name = "mysql"

// Version is the plugin's version.
const Version = "1.0.0"

// Flavor is the server family a profile serves.
type Flavor int

// The flavors.
const (
	MySQL Flavor = iota
	MariaDB
)

// Plugin publishes the MySQL or MariaDB capability profile on every
// invocation.
type Plugin struct{}

// New returns the mysql dialect plugin.
func New() *Plugin { return &Plugin{} }

// Handshake declares a dialect plugin reading IR 1.x.
func (*Plugin) Handshake() *irv1.PluginHandshake {
	return &irv1.PluginHandshake{
		Plugin:     &irv1.PluginInfo{Name: Name, Kind: irv1.PluginKind_PLUGIN_KIND_DIALECT, Version: Version},
		IrVersions: &irv1.IrVersionRange{Min: "1.0.0", MaxMajor: 1},
	}
}

// Invoke returns a bundle holding the capability profile, with how the server reports a violation of each rule of the
// bundle's catalogs, and the fragment table of its catalogs and shapes in
// the catalogs' flavor. It refuses a bundle of another dialect, and catalogs of both flavors.
func (*Plugin) Invoke(_ context.Context, in *irv1.Invoke) (*irv1.InvokeResult, error) {
	if d := in.GetBundle().GetDialect(); d != irv1.Dialect_DIALECT_MYSQL {
		return nil, fmt.Errorf("mysql: the bundle's catalogs are %s, not MySQL", d)
	}
	flavor, err := flavorOf(in.GetBundle().GetCatalogs())
	if err != nil {
		return nil, err
	}
	p := Profile()
	p.Violations = Violations(in.GetBundle().GetCatalogs())
	return &irv1.InvokeResult{Bundle: &irv1.Bundle{Capabilities: p, Queries: &irv1.QueryLayer{Fragments: Fragments(in.GetBundle(), p, flavor)}}}, nil
}

// flavorOf reads the flavor mysql-live records on each catalog; a catalog
// without one is MySQL's.
func flavorOf(cats []*irv1.Catalog) (Flavor, error) {
	seen := map[Flavor]bool{}
	for _, c := range cats {
		f := MySQL
		for _, e := range c.GetExtensions() {
			var db mysqlextv1.Database
			if e.UnmarshalTo(&db) == nil && db.GetFlavor() == "mariadb" {
				f = MariaDB
			}
		}
		seen[f] = true
	}
	if len(seen) > 1 {
		return 0, fmt.Errorf("mysql: the bundle mixes MySQL and MariaDB catalogs; generate each flavor with its own dql.yaml")
	}
	if seen[MariaDB] {
		return MariaDB, nil
	}
	return MySQL, nil
}

// Profile returns the capability profile, which MySQL and MariaDB share.
//
// The text operators need a deterministic collation, so that they mean the
// same as on Postgres: MySQL compares text by its collation, which is case
// and accent insensitive by default. There are no arrays or ranges. in and
// any take one list placeholder, which the driver adapter expands into one
// placeholder per item (§9.2). Division, whose result on integers is a
// decimal rather than a truncated integer, and regular expressions, which
// are ICU's rather than POSIX's, are approximate. JSON paths are not
// SQL/JSON paths, so json_path is left out.
//
// Every connection runs in UTC in strict mode (§22.7), and the server's
// error numbers map into the taxonomy (§23.1). A statement binds at most
// 65,535 parameters (§25.2). Replicas serve reads at any staleness. A
// write's rows come back by re-reading them (§20.1). go-sql-driver/mysql
// prepares statements on the server, or interpolates values client-side
// in its simple mode (§26.4).
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
		Dialect: irv1.Dialect_DIALECT_MYSQL, Operators: ops, SessionSetup: slices.Clone(sessionSetup), ErrorCodes: codes,
		MaxParams:         65535,
		ConsistencyLevels: []irv1.ConsistencyLevel{irv1.ConsistencyLevel_CONSISTENCY_PRIMARY, irv1.ConsistencyLevel_CONSISTENCY_ANY},
		StatementModes:    []irv1.StatementMode{irv1.StatementMode_STATEMENT_MODE_PREPARE, irv1.StatementMode_STATEMENT_MODE_SIMPLE},
		ReturnedRows:      irv1.ReturnedRows_RETURNED_ROWS_REREAD,
	}
}

// operatorFidelity is an operator's fidelity: exact unless listed.
func operatorFidelity(name string) irv1.Fidelity {
	switch name {
	case "div", "regex_match":
		return irv1.Fidelity_FIDELITY_APPROXIMATE
	case "ilike", "json_contains", "json_get":
		return irv1.Fidelity_FIDELITY_EMULATED
	}
	return irv1.Fidelity_FIDELITY_EXACT
}

// textOperators are the pattern-matching operators, which compare under
// the column's collation.
var textOperators = map[string]bool{"contains": true, "ilike": true, "like": true, "regex_match": true, "starts_with": true}
