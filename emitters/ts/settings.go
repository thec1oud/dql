package tsemit

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/thec1oud/dql/emitters/ts/internal/gen"
)

// Settings are a dql.yaml emit entry for the ts emitter (§1.3, ADR 0003).
type Settings struct {
	// Dialect is the dialect the package targets: postgres, mysql, sqlite
	// or sqlserver (§2.4).
	Dialect string
	// Out is the directory the package is written to, relative to the
	// directory of dql.yaml.
	Out string
	// Types maps opaque native types, written namespace.type, to TypeScript
	// types: a built-in type such as string, or module#Name, a type the
	// module exports (§8.3).
	Types map[string]string
}

// keys are the keys an emit entry for the ts emitter may hold.
var keys = []string{"dialect", "out", "path", "plugin", "types"}

// ParseSettings reads and checks the emit entry the core passes with an
// invocation (ADR 0003): every key as written, plugin and path included.
//
//   - plugin: ts
//     dialect: postgres
//     out: src/shopdb
//     types:
//     public.citext: { ts: string }
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
			errs = append(errs, fmt.Errorf("unknown key %s; the ts emitter reads %s", k, strings.Join(keys, ", ")))
		}
	}
	str("plugin")
	str("path")
	s.Dialect, s.Out = str("dialect"), str("out")
	if v, ok := entry.GetFields()["types"]; ok {
		types, err := parseTypes(v)
		errs = append(errs, err)
		s.Types = types
	}
	if err := errors.Join(errs...); err != nil {
		return Settings{}, fmt.Errorf("ts: emit entry: %w", err)
	}
	return s, nil
}

// parseTypes reads types: a mapping from namespace.type to { ts: <type> }.
func parseTypes(v *structpb.Value) (map[string]string, error) {
	m := v.GetStructValue()
	if m == nil {
		return nil, errors.New("types must be a mapping from namespace.type to { ts: <type> }")
	}
	out := map[string]string{}
	var errs []error
	for _, native := range slices.Sorted(maps.Keys(m.GetFields())) {
		e := m.GetFields()[native].GetStructValue()
		spec, ok := e.GetFields()["ts"]
		if e == nil || !ok || len(e.GetFields()) != 1 {
			errs = append(errs, fmt.Errorf("types.%s must be { ts: <type> }", native))
			continue
		}
		out[native] = spec.GetStringValue()
	}
	return out, errors.Join(errs...)
}

// validType reports whether spec is a built-in type the types setting may
// name, or module#Name.
func validType(spec string) bool {
	_, _, ok := gen.SplitImported(spec)
	return ok || slices.Contains(gen.BuiltinTypes, spec)
}

// with returns s with every setting set in flags replaced.
func (s Settings) with(flags Settings) Settings {
	if flags.Dialect != "" {
		s.Dialect = flags.Dialect
	}
	if flags.Out != "" {
		s.Out = flags.Out
	}
	if len(flags.Types) > 0 {
		s.Types = flags.Types
	}
	return s
}
