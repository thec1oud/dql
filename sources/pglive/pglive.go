// Package pglive is the pg-live source plugin: it reads a live Postgres
// database and produces the catalog layer of a bundle (design record §2.3).
// It runs in-process through the plugin.Plugin interface, and as the
// executable dql-plugin-pg-live (ADR 0002).
package pglive

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/pglive/internal/introspect"
	"github.com/thec1oud/dql/sources/pglive/internal/mapping"
)

// Name is the plugin's name in dql.yaml.
const Name = mapping.Source

// Settings are a dql.yaml sources entry for pg-live (§1.3).
type Settings struct {
	// Catalog names the catalog the database becomes.
	Catalog string
	// Connection is a Postgres connection string, or env:NAME to read one
	// from the environment variable NAME.
	Connection string
	// Namespaces are the schemas read. None means the default namespace.
	Namespaces []string
	// DefaultNamespace is the namespace whose objects get no prefix (§4.3);
	// empty means public.
	DefaultNamespace string
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

// New returns a pg-live plugin for one sources entry.
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

// Invoke reads the database and returns a bundle holding its catalog, with
// the IR version of the bundle it was handed.
func (p *Plugin) Invoke(ctx context.Context, in *irv1.Invoke) (*irv1.InvokeResult, error) {
	s := p.settings
	if s.Catalog == "" {
		return nil, errors.New("pg-live: no catalog name; set catalog: on the sources entry")
	}
	conn, err := connection(s.Connection)
	if err != nil {
		return nil, err
	}
	db, err := pgx.Connect(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("pg-live: connecting to catalog %s: %w", s.Catalog, err)
	}
	defer db.Close(ctx)
	defaultNS := cmp.Or(s.DefaultNamespace, "public")
	namespaces := s.Namespaces
	if len(namespaces) == 0 {
		namespaces = []string{defaultNS}
	}
	snap, err := read(ctx, db, namespaces)
	if err != nil {
		return nil, fmt.Errorf("pg-live: reading catalog %s: %w", s.Catalog, err)
	}
	c, diags, err := build(snap, mapping.Options{Catalog: s.Catalog, DefaultNamespace: defaultNS})
	if err != nil {
		return nil, fmt.Errorf("pg-live: catalog %s: %w", s.Catalog, err)
	}
	return &irv1.InvokeResult{
		Bundle: &irv1.Bundle{
			Version:  in.GetBundle().GetVersion(),
			Dialect:  irv1.Dialect_DIALECT_POSTGRES,
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
		return "", fmt.Errorf("pg-live: connection names %s, which is not set", setting)
	}
	return v, nil
}
