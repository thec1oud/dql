// Package config loads dql.yaml, the project configuration read by the core
// and handed to source and emitter plugins (design record §1.3).
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"

	"google.golang.org/protobuf/types/known/structpb"
	"gopkg.in/yaml.v3"

	"github.com/thec1oud/dql/core/shapes"
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
	// Custom shapes by name (§14.8).
	Shapes map[string]Shape `yaml:"shapes"`
	// Conversions to generate beyond the presets' and lineages' (§14.9).
	Conversions []Conversion `yaml:"conversions"`
	// Project-wide settings (§7.2).
	DefaultCatalog string `yaml:"default-catalog"`
	OpaqueFallback string `yaml:"opaque-fallback"`

	// file is the name of the file Load read, for the locations of config
	// annotations (§7.6), and dir its directory.
	file, dir string
}

// Shape is a custom shape (§14.8): from names the shape it derives from in
// the dotted spelling of shape ids, such as user.full or user_summary, and
// each operator names fields of that shape.
type Shape struct {
	From        string   `yaml:"from"`
	Pick        []string `yaml:"pick"`
	Omit        []string `yaml:"omit"`
	Require     []string `yaml:"require"`
	Optionalize []string `yaml:"optionalize"`
	Nullify     []string `yaml:"nullify"`
	Nonnull     []string `yaml:"nonnull"`
	// Nest maps a relation to the shape it takes.
	Nest map[string]string `yaml:"nest"`
	Bind []string          `yaml:"bind"`
}

// Conversion is a declared conversion between two shapes, written in the
// dotted spelling of shape ids (§14.9). Each entry is a mapping, so that keys
// can be added to it.
type Conversion struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

// CustomShapes returns the shapes: entries for the shape layer, sorted by
// name.
func (c *Config) CustomShapes() []shapes.Custom {
	var out []shapes.Custom
	for _, name := range slices.Sorted(maps.Keys(c.Shapes)) {
		s := c.Shapes[name]
		out = append(out, shapes.Custom{
			Name: name, From: s.From,
			Pick: s.Pick, Omit: s.Omit,
			Require: s.Require, Optionalize: s.Optionalize,
			Nullify: s.Nullify, Nonnull: s.Nonnull,
			Bind: s.Bind, Nest: s.Nest,
		})
	}
	return out
}

// Source is one source plugin invocation. Which settings apply depends on
// the plugin: pg-live reads a database through connection, and dpg-snapshot
// reads a snapshot file. A shipped plugin's entry holds only the keys below;
// any other plugin's may hold keys of its own, which the plugin validates.
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
	// Settings is the whole entry as written, for the plugin request.
	Settings *structpb.Struct `yaml:"-"`
}

func (s *Source) UnmarshalYAML(n *yaml.Node) error {
	type entry Source
	var p entry
	_, strict := shipped[entryPlugin(n)]
	settings, err := decodeEntry(n, &p, "config.Source", strict)
	*s = Source(p)
	s.Settings = settings
	return err
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

// Emit is one emitter run, which targets exactly one dialect (§2.4). A
// shipped emitter's entry holds only the keys below; any other emitter's may
// hold keys of its own, which its plugin validates (ADR 0003).
type Emit struct {
	Plugin  string                    `yaml:"plugin"`
	Path    string                    `yaml:"path"`
	Dialect string                    `yaml:"dialect"`
	Package string                    `yaml:"package"`
	Out     string                    `yaml:"out"`
	Types   map[string]map[string]any `yaml:"types"`
	// Settings is the whole entry as written, for the plugin request.
	Settings *structpb.Struct `yaml:"-"`
}

func (e *Emit) UnmarshalYAML(n *yaml.Node) error {
	type entry Emit
	var p entry
	settings, err := decodeEntry(n, &p, "config.Emit", shippedEmitters[entryPlugin(n)])
	*e = Emit(p)
	e.Settings = settings
	return err
}

// shippedEmitters are the emitter plugins that ship with dql.
var shippedEmitters = map[string]bool{"go": true}

// entryPlugin is the plugin a sources or emit entry names, or "".
func entryPlugin(n *yaml.Node) string {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == "plugin" {
			return n.Content[i+1].Value
		}
	}
	return ""
}

// decodeEntry decodes a plugin entry into v, a pointer to a struct of the
// keys the core reads, and returns the whole entry as the plugin's settings.
// When strict, a key v lacks is an error, reported as for the rest of
// dql.yaml with v's type as name.
func decodeEntry(n *yaml.Node, v any, name string, strict bool) (*structpb.Struct, error) {
	if err := n.Decode(v); err != nil {
		return nil, err
	}
	var errs []string
	if strict {
		known := map[string]bool{}
		t := reflect.TypeOf(v).Elem()
		for i := range t.NumField() {
			known[t.Field(i).Tag.Get("yaml")] = true
		}
		for i := 0; i < len(n.Content); i += 2 {
			if k := n.Content[i]; !known[k.Value] {
				errs = append(errs, fmt.Sprintf("line %d: field %s not found in type %s", k.Line, k.Value, name))
			}
		}
	}
	if k := textual(n, map[*yaml.Node]bool{}); k != nil {
		errs = append(errs, fmt.Sprintf("line %d: a key in a plugin's settings must be a scalar", k.Line))
	}
	if len(errs) > 0 {
		return nil, &yaml.TypeError{Errors: errs}
	}
	var m map[string]any
	n.Decode(&m)                         // cannot fail: v decoded the same mapping
	settings, _ := structpb.NewStruct(m) // cannot fail: textual leaves only JSON values
	return settings, nil
}

// textual retags n's mapping keys, timestamps and binary values as strings,
// so the plugin receives them as written and every mapping decodes with
// string keys. It returns the first key that is not a scalar, or nil.
func textual(n *yaml.Node, seen map[*yaml.Node]bool) *yaml.Node {
	if seen[n] {
		return nil
	}
	seen[n] = true
	switch n.Kind {
	case yaml.AliasNode:
		return textual(n.Alias, seen)
	case yaml.ScalarNode:
		if t := n.ShortTag(); t == "!!timestamp" || t == "!!binary" {
			n.Tag = "!!str"
		}
	case yaml.MappingNode:
		for i := 0; i < len(n.Content); i += 2 {
			switch k := n.Content[i]; {
			case k.Kind != yaml.ScalarNode:
				return k
			case k.ShortTag() != "!!merge":
				k.Tag = "!!str"
			}
		}
	}
	for _, c := range n.Content {
		if k := textual(c, seen); k != nil {
			return k
		}
	}
	return nil
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
	// cannot fail: path was just read, so the working directory exists
	c.dir, _ = filepath.Abs(filepath.Dir(path))
	c.file = filepath.Base(path)
	return c, nil
}

// Dir is the absolute path of the directory holding the file Load read,
// where plugin executables run so that relative paths in their settings
// resolve as dql.yaml means them. It is "" for parsed content.
func (c *Config) Dir() string { return c.dir }

// Resolve returns a path written in dql.yaml, such as a snapshot's, as an
// absolute path: relative paths in dql.yaml are relative to its directory.
// For parsed content they stay relative.
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
		if shippedEmitters[e.Plugin] {
			for _, f := range [][2]string{{"package", e.Package}, {"out", e.Out}} {
				if f[1] == "" {
					add("config-setting-missing", "emit[%d] (%s) has no %s; set %s:", i, e.Plugin, f[0], f[0])
				}
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(c.Shapes)) {
		if c.Shapes[name].From == "" {
			add("config-setting-missing", "shapes.%s has no from; set from: to the shape it derives from, such as user.full", name)
		}
	}
	for i, v := range c.Conversions {
		if v.From == "" {
			add("config-setting-missing", "conversions[%d] has no from; set from: to a shape, such as user.full", i)
		}
		if v.To == "" {
			add("config-setting-missing", "conversions[%d] has no to; set to: to a shape, such as user_summary", i)
		}
	}
	if c.OpaqueFallback != "" && c.OpaqueFallback != "string" && c.OpaqueFallback != "bytes" {
		add("config-opaque-fallback-invalid", "opaque-fallback is %q; use string or bytes", c.OpaqueFallback)
	}
	return ds
}
