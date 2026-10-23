// Package config loads dql.yaml, the project configuration read by the core
// and handed to source and emitter plugins (design record §1.3).
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"

	"gopkg.in/yaml.v3"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Config is dql.yaml.
type Config struct {
	Sources []Source `yaml:"sources"`
	// Annotations by qualified name, such as shop.app.users: { name: user } (§7.1).
	Annotations map[string]map[string]any `yaml:"annotations"`
	Emit        []Emit                    `yaml:"emit"`
	// Custom shapes (§14.8) and declared conversions (§14.9), read in M2.
	Shapes      map[string]any `yaml:"shapes"`
	Conversions any            `yaml:"conversions"`
	// Project-wide settings (§7.2).
	DefaultCatalog string `yaml:"default-catalog"`
	OpaqueFallback string `yaml:"opaque-fallback"`
}

// Source is one source plugin invocation.
type Source struct {
	Plugin           string   `yaml:"plugin"`
	Path             string   `yaml:"path"`
	Catalog          string   `yaml:"catalog"`
	Connection       string   `yaml:"connection"`
	Namespaces       []string `yaml:"namespaces"`
	DefaultNamespace string   `yaml:"default-namespace"`
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
	return Parse(b)
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
