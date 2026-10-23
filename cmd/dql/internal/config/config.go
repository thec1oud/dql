// Package config loads dql.yaml, the project configuration read by the core
// and handed to source and emitter plugins (design record §1.3).
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"gopkg.in/yaml.v3"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Config is dql.yaml.
type Config struct {
	Sources []Source `yaml:"sources"`
	// Annotations by qualified name, such as shop.app.users: { name: user }
	// (§7.1). Each entry is a mapping node, kept as written so a scalar keeps
	// its text; ConfigAnnotations turns them into IR annotations.
	Annotations map[string]yaml.Node `yaml:"annotations"`
	Emit        []Emit               `yaml:"emit"`
	// Custom shapes (§14.8) and declared conversions (§14.9), read in M2.
	Shapes      map[string]any `yaml:"shapes"`
	Conversions any            `yaml:"conversions"`
	// Project-wide settings (§7.2).
	DefaultCatalog string `yaml:"default-catalog"`
	OpaqueFallback string `yaml:"opaque-fallback"`

	// file is the name of the file Load read, for the locations of config
	// annotations (§7.6), and dir its directory.
	file, dir string
}

// Source is one source plugin invocation. Which settings apply depends on
// the plugin: pg-live reads a database through connection, and dpg-snapshot
// reads a snapshot file.
type Source struct {
	Plugin     string `yaml:"plugin"`
	Path       string `yaml:"path"`
	Catalog    string `yaml:"catalog"`
	Connection string `yaml:"connection"`
	// Snapshot is the path of a dpg snapshot file, relative to the directory
	// of dql.yaml.
	Snapshot         string   `yaml:"snapshot"`
	Namespaces       []string `yaml:"namespaces"`
	DefaultNamespace string   `yaml:"default-namespace"`
}

// shipped lists, for each shipped source plugin, the settings it needs and
// the ones it does not read; the others are optional.
var shipped = map[string]struct{ required, unused []string }{
	"pg-live":      {required: []string{"catalog", "connection"}, unused: []string{"snapshot"}},
	"dpg-snapshot": {required: []string{"snapshot"}, unused: []string{"connection"}},
}

// setting returns the value of the setting named as dql.yaml writes it.
func (s Source) setting(name string) string {
	switch name {
	case "catalog":
		return s.Catalog
	case "connection":
		return s.Connection
	default:
		return s.Snapshot
	}
}

// Emit is one emitter run, which targets exactly one dialect (§2.4).
type Emit struct {
	Plugin  string                    `yaml:"plugin"`
	Path    string                    `yaml:"path"`
	Dialect string                    `yaml:"dialect"`
	Package string                    `yaml:"package"`
	Out     string                    `yaml:"out"`
	Types   map[string]map[string]any `yaml:"types"`
}

// Dialects are the dialect names an emit entry may target.
var Dialects = []string{"mysql", "postgres", "sqlite", "sqlserver"}

// Load reads and decodes the file at path. Unknown keys are an error.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c, err := Parse(b)
	if err != nil {
		return nil, err
	}
	c.file, c.dir = filepath.Base(path), filepath.Dir(path)
	return c, nil
}

// Resolve returns a path written in dql.yaml, such as a snapshot's, as a
// path relative to the working directory: relative paths in dql.yaml are
// relative to its directory.
func (c *Config) Resolve(path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(c.dir, path)
}

// Parse decodes dql.yaml content. Unknown keys are an error.
func Parse(b []byte) (*Config, error) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	c := &Config{}
	if err := dec.Decode(c); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("dql.yaml: %w", err)
	}
	return c, nil
}

// Validate checks what can be checked without running plugins, in file order.
func (c *Config) Validate() []*irv1.Diagnostic {
	var ds []*irv1.Diagnostic
	add := func(code, format string, args ...any) {
		ds = append(ds, &irv1.Diagnostic{Severity: irv1.Severity_SEVERITY_ERROR, Code: code, Message: fmt.Sprintf(format, args...)})
	}
	if len(c.Sources) == 0 {
		add("config-no-sources", "dql.yaml lists no sources; add at least one under sources:")
	}
	for i, s := range c.Sources {
		if s.Plugin == "" {
			add("config-plugin-missing", "sources[%d] names no plugin; set plugin:", i)
		}
		rules := shipped[s.Plugin]
		for _, name := range rules.required {
			if s.setting(name) == "" {
				add("config-setting-missing", "sources[%d] (%s) has no %s; set %s:", i, s.Plugin, name, name)
			}
		}
		for _, name := range rules.unused {
			if s.setting(name) != "" {
				add("config-setting-unused", "sources[%d] (%s) sets %s, which %s does not read; remove it", i, s.Plugin, name, s.Plugin)
			}
		}
	}
	for i, e := range c.Emit {
		if e.Plugin == "" {
			add("config-plugin-missing", "emit[%d] names no plugin; set plugin:", i)
		}
		if !slices.Contains(Dialects, e.Dialect) {
			add("config-dialect-unknown", "emit[%d] targets dialect %q; use one of %v", i, e.Dialect, Dialects)
		}
	}
	if c.OpaqueFallback != "" && c.OpaqueFallback != "string" && c.OpaqueFallback != "bytes" {
		add("config-opaque-fallback-invalid", "opaque-fallback is %q; use string or bytes", c.OpaqueFallback)
	}
	return ds
}
