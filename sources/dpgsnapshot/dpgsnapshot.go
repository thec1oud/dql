// Package dpgsnapshot is the dpg-snapshot source plugin: it reads one
// database's catalog from a snapshot file committed by dpg and produces the
// catalog layer of a bundle (design record §2.3, §2.4). It reads dpg's
// current snapshot format and refuses every other version. It runs
// in-process through the plugin.Plugin interface, and as the executable
// dql-plugin-dpg-snapshot (ADR 0002).
//
// A snapshot holds less than the live catalog; the catalog's provenance names
// the limits (fidelity.DPGSnapshot).
package dpgsnapshot

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/dpgsnapshot/internal/format"
	"github.com/thec1oud/dql/sources/dpgsnapshot/internal/mapping"
)

// Name is the plugin's name in dql.yaml.
const Name = mapping.Source

// Settings are a dql.yaml sources entry for dpg-snapshot (§1.3).
type Settings struct {
	// Snapshot is the path of the snapshot file.
	Snapshot string
	// Catalog names the catalog the database becomes. Empty means the
	// snapshot's database (§5.1).
	Catalog string
	// Namespaces are the schemas read. None means the default namespace.
	Namespaces []string
	// DefaultNamespace is the namespace whose objects get no prefix (§4.3);
	// empty means public.
	DefaultNamespace string
}

// load and build are variables so tests can make them fail.
var (
	load  = format.Load
	build = mapping.Map
)

// Plugin reads the snapshot its settings name on every invocation.
type Plugin struct {
	settings Settings
}

// New returns a dpg-snapshot plugin for one sources entry.
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

// Invoke reads the snapshot and returns a bundle holding its catalog, with
// the IR version of the bundle it was handed.
func (p *Plugin) Invoke(_ context.Context, in *irv1.Invoke) (*irv1.InvokeResult, error) {
	s := p.settings
	if s.Snapshot == "" {
		return nil, errors.New("dpg-snapshot: no snapshot file; set snapshot: on the sources entry")
	}
	snap, err := load(s.Snapshot)
	if err != nil {
		return nil, fmt.Errorf("dpg-snapshot: %w", err)
	}
	defaultNS := cmp.Or(s.DefaultNamespace, "public")
	namespaces := s.Namespaces
	if len(namespaces) == 0 {
		namespaces = []string{defaultNS}
	}
	c, diags, err := build(snap, mapping.Options{Catalog: s.Catalog, DefaultNamespace: defaultNS, Namespaces: namespaces})
	if err != nil {
		return nil, fmt.Errorf("dpg-snapshot: %s: %w", s.Snapshot, err)
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
