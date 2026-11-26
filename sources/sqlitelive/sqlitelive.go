// Package sqlitelive is the sqlite-live source plugin: it reads a SQLite
// database file and produces the catalog layer of a bundle (design record
// §2.3). It runs in-process through the plugin.Plugin interface, and as the
// executable dql-plugin-sqlite-live (ADR 0002).
package sqlitelive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/sqlitelive/internal/introspect"
	"github.com/thec1oud/dql/sources/sqlitelive/internal/mapping"
)

// Name is the plugin's name in dql.yaml.
const Name = mapping.Source

// Settings are a dql.yaml sources entry for sqlite-live (§1.3).
type Settings struct {
	// Catalog names the catalog the database becomes.
	Catalog string
	// Connection is the database file's path, a file: URI, or env:NAME to
	// read either from the environment variable NAME.
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

// New returns a sqlite-live plugin for one sources entry.
func New(s Settings) *Plugin { return &Plugin{settings: s} }

// Handshake declares a source plugin reading IR 1.x. Columns of tables
// that are not STRICT are weak (§8.10).
func (p *Plugin) Handshake() *irv1.PluginHandshake {
	return &irv1.PluginHandshake{
		Plugin:     &irv1.PluginInfo{Name: Name, Kind: irv1.PluginKind_PLUGIN_KIND_SOURCE, Version: mapping.Version},
		IrVersions: &irv1.IrVersionRange{Min: "1.0.0", MaxMajor: 1},
		Features:   []string{"weak-types"},
	}
}

// Invoke opens the database read-only, reads it in one transaction and
// returns a bundle holding its catalog, with the IR version of the bundle
// it was handed. A file that does not exist is an error, never created.
func (p *Plugin) Invoke(ctx context.Context, in *irv1.Invoke) (*irv1.InvokeResult, error) {
	s := p.settings
	if s.Catalog == "" {
		return nil, errors.New("sqlite-live: no catalog name; set catalog: on the sources entry")
	}
	conn, err := connection(s.Connection)
	if err != nil {
		return nil, err
	}
	if conn == "" {
		return nil, fmt.Errorf("sqlite-live: catalog %s names no database file; set connection: to its path", s.Catalog)
	}
	db, _ := sql.Open("sqlite", dsn(conn)) // Open only checks the driver's name.
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err == nil {
		// A deferred transaction takes its snapshot at its first read; this
		// one takes it now, and fails here if the file cannot be opened.
		_, err = tx.ExecContext(ctx, "SELECT 1 FROM sqlite_schema LIMIT 1")
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite-live: opening catalog %s: %w", s.Catalog, err)
	}
	defer tx.Rollback() //nolint:errcheck // read-only; nothing to keep
	snap, err := read(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("sqlite-live: reading catalog %s: %w", s.Catalog, err)
	}
	c, diags, err := build(snap, mapping.Options{Catalog: s.Catalog})
	if err != nil {
		return nil, fmt.Errorf("sqlite-live: catalog %s: %w", s.Catalog, err)
	}
	return &irv1.InvokeResult{
		Bundle: &irv1.Bundle{
			Version:  in.GetBundle().GetVersion(),
			Dialect:  irv1.Dialect_DIALECT_SQLITE,
			Catalogs: []*irv1.Catalog{c},
		},
		Diagnostics: diags,
	}, nil
}

// dsn makes a connection setting a read-only file: URI.
func dsn(conn string) string {
	if !strings.HasPrefix(conn, "file:") {
		conn = "file:" + strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(conn)
	}
	sep := "?"
	if strings.Contains(conn, "?") {
		sep = "&"
	}
	return conn + sep + "mode=ro"
}

// connection resolves a connection setting: env:NAME reads the environment.
func connection(setting string) (string, error) {
	name, ok := strings.CutPrefix(setting, "env:")
	if !ok {
		return setting, nil
	}
	v := os.Getenv(name)
	if v == "" {
		return "", fmt.Errorf("sqlite-live: connection names %s, which is not set", setting)
	}
	return v, nil
}
