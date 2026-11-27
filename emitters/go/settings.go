package goemit

import (
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"maps"
	"slices"
	"strings"

	"google.golang.org/protobuf/types/known/structpb"
)

// Settings are a dql.yaml emit entry for the go emitter (§1.3, ADR 0003).
type Settings struct {
	// Dialect is the dialect the package targets: postgres, mysql, sqlite
	// or sqlserver (§2.4).
	Dialect string
	// Package is the generated package's name.
	Package string
	// Out is the directory the package is written to, relative to the
	// directory of dql.yaml.
	Out string
	// Types maps opaque native types, written namespace.type, to Go types:
	// a predeclared type such as string, or import/path.Name (§8.3).
	Types map[string]string
}

// keys are the keys an emit entry for the go emitter may hold.
var keys = []string{"dialect", "out", "package", "path", "plugin", "types"}

// ParseSettings reads and checks the emit entry the core passes with an
// invocation (ADR 0003): every key as written, plugin and path included.
//
//   - plugin: go
//     dialect: postgres
//     package: shopdb
//     out: internal/shopdb
//     types:
//     public.citext: { go: string }
func ParseSettings(entry *structpb.Struct) (Settings, error) {
	var s Settings
	var errs []error
	str := func(k string) string {
		v, ok := entry.GetFields()[k]
		if !ok {
			return ""
		}
		if _, ok := v.GetKind().(*structpb.Value_StringValue); !ok {
			errs = append(errs, fmt.Errorf("%s must be a string", k))
		}
		return v.GetStringValue()
	}
	for _, k := range slices.Sorted(maps.Keys(entry.GetFields())) {
		if !slices.Contains(keys, k) {
			errs = append(errs, fmt.Errorf("unknown key %s; the go emitter reads %s", k, strings.Join(keys, ", ")))
		}
	}
	str("plugin")
	str("path")
	s.Dialect, s.Package, s.Out = str("dialect"), str("package"), str("out")
	if v, ok := entry.GetFields()["types"]; ok {
		types, err := parseTypes(v)
		errs = append(errs, err)
		s.Types = types
	}
	if err := errors.Join(errs...); err != nil {
		return Settings{}, fmt.Errorf("go: emit entry: %w", err)
	}
	return s, nil
}

// parseTypes reads types: a mapping from namespace.type to { go: <type> }.
func parseTypes(v *structpb.Value) (map[string]string, error) {
	m := v.GetStructValue()
	if m == nil {
		return nil, errors.New("types must be a mapping from namespace.type to { go: <type> }")
	}
	out := map[string]string{}
	var errs []error
	for _, native := range slices.Sorted(maps.Keys(m.GetFields())) {
		e := m.GetFields()[native].GetStructValue()
		spec, ok := e.GetFields()["go"]
		if e == nil || !ok || len(e.GetFields()) != 1 {
			errs = append(errs, fmt.Errorf("types.%s must be { go: <type> }", native))
			continue
		}
		out[native] = spec.GetStringValue()
	}
	return out, errors.Join(errs...)
}

// validType reports whether spec is a Go type expression without package
// qualifiers, such as string or []byte, or import/path.Name.
func validType(spec string) bool {
	if strings.Contains(spec, "/") {
		i := strings.LastIndex(spec, ".")
		return i > strings.LastIndex(spec, "/") && token.IsIdentifier(spec[i+1:]) && token.IsExported(spec[i+1:])
	}
	_, err := parser.ParseExpr(spec)
	return err == nil && !strings.Contains(spec, ".")
}

// with returns s with every setting set in flags replaced.
func (s Settings) with(flags Settings) Settings {
	for _, f := range []struct {
		dst *string
		src string
	}{
		{&s.Dialect, flags.Dialect}, {&s.Package, flags.Package}, {&s.Out, flags.Out},
	} {
		if f.src != "" {
			*f.dst = f.src
		}
	}
	if len(flags.Types) > 0 {
		s.Types = flags.Types
	}
	return s
}
