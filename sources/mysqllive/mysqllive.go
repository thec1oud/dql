// Package mysqllive is the mysql-live source plugin: it reads a live MySQL or
// MariaDB database and produces the catalog layer of a bundle (design record
// §2.3). It runs in-process through the plugin.Plugin interface, and as the
// executable dql-plugin-mysql-live (ADR 0002).
package mysqllive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/go-sql-driver/mysql"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/mysqllive/internal/introspect"
	"github.com/thec1oud/dql/sources/mysqllive/internal/mapping"
)

// Name is the plugin's name in dql.yaml.
const Name = mapping.Source

// Settings are a dql.yaml sources entry for mysql-live (§1.3).
type Settings struct {
	// Catalog names the catalog the database becomes.
	Catalog string
	// Connection is a go-sql-driver/mysql DSN naming the database, such as
	// user:password@tcp(localhost:3306)/shop, or env:NAME to read one from
	// the environment variable NAME.
	Connection string
}

// read and build are variables so tests can make them fail.
var (
	read  = introspect.Read
	build = mapping.Map
)

// Plugin reads the database its settings name on every invocation.
type Plugin struct {
	settings Settings
}

// New returns a mysql-live plugin for one sources entry.
func New(s Settings) *Plugin { return &Plugin{settings: s} }

// Handshake declares a source plugin reading IR 1.x. Columns of types the
// core cannot classify are opaque (§8.3).
func (p *Plugin) Handshake() *irv1.PluginHandshake {
	return &irv1.PluginHandshake{
		Plugin:     &irv1.PluginInfo{Name: Name, Kind: irv1.PluginKind_PLUGIN_KIND_SOURCE, Version: mapping.Version},
		IrVersions: &irv1.IrVersionRange{Min: "1.0.0", MaxMajor: 1},
		Features:   []string{"opaque-types"},
	}
}

// Invoke reads the database in one read-only transaction and returns a
// bundle holding its catalog, with the IR version of the bundle it was
// handed.
func (p *Plugin) Invoke(ctx context.Context, in *irv1.Invoke) (*irv1.InvokeResult, error) {
	s := p.settings
	if s.Catalog == "" {
		return nil, errors.New("mysql-live: no catalog name; set catalog: on the sources entry")
	}
	dsn, err := connection(s.Connection)
	if err != nil {
		return nil, err
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("mysql-live: the connection of catalog %s: %w", s.Catalog, err)
	}
	if cfg.DBName == "" {
		return nil, fmt.Errorf("mysql-live: the connection of catalog %s names no database; end it with /<database>", s.Catalog)
	}
	// ParseDSN has checked everything NewConnector checks.
	connector, _ := mysql.NewConnector(cfg)
	db := sql.OpenDB(connector)
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, fmt.Errorf("mysql-live: connecting to catalog %s: %w", s.Catalog, err)
	}
	defer tx.Rollback() //nolint:errcheck // read-only; nothing to keep
	snap, err := read(ctx, tx, cfg.DBName)
	if err != nil {
		return nil, fmt.Errorf("mysql-live: reading catalog %s: %w", s.Catalog, err)
	}
	c, diags, err := build(snap, mapping.Options{Catalog: s.Catalog, Database: cfg.DBName})
	if err != nil {
		return nil, fmt.Errorf("mysql-live: catalog %s: %w", s.Catalog, err)
	}
	return &irv1.InvokeResult{
		Bundle: &irv1.Bundle{
			Version:  in.GetBundle().GetVersion(),
			Dialect:  irv1.Dialect_DIALECT_MYSQL,
			Catalogs: []*irv1.Catalog{c},
		},
		Diagnostics: diags,
	}, nil
}

// connection resolves a connection setting: env:NAME reads the environment.
func connection(setting string) (string, error) {
	name, ok := strings.CutPrefix(setting, "env:")
	if !ok {
		return setting, nil
	}
	v := os.Getenv(name)
	if v == "" {
		return "", fmt.Errorf("mysql-live: connection names %s, which is not set", setting)
	}
	return v, nil
}
