// Package goemit is the go emitter plugin: it turns a bundle into one Go
// package for the bundle's dialect (design record §2.3, §2.4, §15). It runs
// in-process through the plugin.Plugin interface, and as the executable
// dql-plugin-go (ADR 0002).
package goemit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/thec1oud/dql/emitters/go/internal/gen"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Name is the plugin's name in dql.yaml.
const Name = "go"

// Version is the plugin's version.
const Version = "1.0.0"

// Features are the optional IR capabilities the emitter implements (§3.3).
var Features = []string{"cross-catalog", "opaque-types", "weak-types"}

// Plugin generates the package its emit entry describes on every invocation.
type Plugin struct {
	dir   string
	flags Settings
}

// New returns a go emitter that resolves a relative out against dir, the
// directory of dql.yaml; an empty dir means the working directory. Settings
// in flags, as the executable takes them, replace the entry's.
func New(dir string, flags Settings) *Plugin { return &Plugin{dir: dir, flags: flags} }

// Handshake declares an emitter reading IR 1.x.
func (p *Plugin) Handshake() *irv1.PluginHandshake {
	return &irv1.PluginHandshake{
		Plugin:     &irv1.PluginInfo{Name: Name, Kind: irv1.PluginKind_PLUGIN_KIND_EMITTER, Version: Version},
		IrVersions: &irv1.IrVersionRange{Min: "1.0.0", MaxMajor: 1},
		Features:   slices.Clone(Features),
	}
}

// Invoke generates the package and writes it to out. With an error
// diagnostic it writes nothing and returns the diagnostics.
func (p *Plugin) Invoke(_ context.Context, in *irv1.Invoke) (*irv1.InvokeResult, error) {
	s, err := ParseSettings(in.GetSettings())
	if err != nil {
		return nil, err
	}
	s = s.with(p.flags)
	if err := s.check(in.GetBundle()); err != nil {
		return nil, err
	}
	files, diags := gen.Generate(in.GetBundle(), gen.Options{Package: s.Package, Types: s.Types})
	if files != nil {
		out := s.Out
		if !filepath.IsAbs(out) {
			out = filepath.Join(p.dir, out)
		}
		if err := write(out, files); err != nil {
			return nil, fmt.Errorf("go: %w", err)
		}
	}
	return &irv1.InvokeResult{Diagnostics: diags}, nil
}

// remove is a variable so tests can make it fail.
var remove = os.Remove

// write puts files in dir and removes the generated files of an earlier run
// that files no longer hold. Every other file is hand-written and stays
// (§26.5).
func write(dir string, files []gen.File) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	stale, err := filepath.Glob(filepath.Join(dir, "*"+gen.Suffix))
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, f := range files {
		keep[f.Name] = true
	}
	for _, path := range stale {
		if keep[filepath.Base(path)] {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.HasPrefix(b, []byte(gen.Header+"\n")) {
			if err := remove(path); err != nil {
				return err
			}
		}
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.Name), f.Content, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// dialects are the dialect spellings of dql.yaml (§2.4).
var dialects = map[string]irv1.Dialect{
	"postgres":  irv1.Dialect_DIALECT_POSTGRES,
	"mysql":     irv1.Dialect_DIALECT_MYSQL,
	"sqlite":    irv1.Dialect_DIALECT_SQLITE,
	"sqlserver": irv1.Dialect_DIALECT_SQLSERVER,
}

// check fails settings that do not fit the bundle: one package per target
// dialect, so the entry's dialect is the bundle's (§2.4).
func (s Settings) check(b *irv1.Bundle) error {
	var errs []error
	if !token.IsIdentifier(s.Package) || s.Package == "_" {
		errs = append(errs, fmt.Errorf("package is %q; set package: to the generated package's name", s.Package))
	}
	if s.Out == "" {
		errs = append(errs, errors.New("no out; set out: to the directory the package is written to"))
	}
	for _, native := range slices.Sorted(maps.Keys(s.Types)) {
		if !strings.Contains(native, ".") {
			errs = append(errs, fmt.Errorf("types.%s must name a native type as namespace.type", native))
		}
		if spec := s.Types[native]; !validType(spec) {
			errs = append(errs, fmt.Errorf("types.%s.go is %q, which is neither a Go type nor import/path.Name", native, spec))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("go: emit entry: %w", err)
	}
	if d := dialects[s.Dialect]; d != b.GetDialect() {
		return fmt.Errorf("go: the emit entry targets dialect %s, and the bundle's catalogs are %s; one package targets one dialect (§2.4)",
			s.Dialect, strings.ToLower(strings.TrimPrefix(b.GetDialect().String(), "DIALECT_")))
	}
	return nil
}
