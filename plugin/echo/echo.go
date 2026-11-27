// Package echo is a plugin that returns the bundle it receives. It exercises
// the plugin protocol in-process and as an executable.
package echo

import (
	"context"
	"errors"
	"fmt"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Plugin echoes bundles. Its handshake is set at construction.
type Plugin struct {
	hs *irv1.PluginHandshake
}

// New returns an echo plugin of the given kind, reading IR from min up to
// maxMajor, with the given features.
func New(kind irv1.PluginKind, min string, maxMajor uint32, features ...string) *Plugin {
	return &Plugin{hs: &irv1.PluginHandshake{
		Plugin:     &irv1.PluginInfo{Name: "echo", Kind: kind, Version: "1.0.0"},
		IrVersions: &irv1.IrVersionRange{Min: min, MaxMajor: maxMajor},
		Features:   features,
	}}
}

func (p *Plugin) Handshake() *irv1.PluginHandshake { return p.hs }

// Invoke returns the bundle unchanged, with a warning that echoes the
// settings when there are any. It fails when there is no bundle.
func (p *Plugin) Invoke(_ context.Context, in *irv1.Invoke) (*irv1.InvokeResult, error) {
	if in.GetBundle() == nil {
		return nil, errors.New("echo: no bundle to echo")
	}
	res := &irv1.InvokeResult{Bundle: in.GetBundle()}
	if in.GetSettings() != nil {
		res.Diagnostics = []*irv1.Diagnostic{{Severity: irv1.Severity_SEVERITY_WARNING, Code: "echo-settings",
			Message: fmt.Sprint(in.GetSettings().AsMap())}}
	}
	return res, nil
}
