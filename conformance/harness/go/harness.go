// Package harness runs the conformance scenarios (guide §6.3) against the Go
// emitter's generated code. A step's op names a store and a method, as
// users.insert or users.upsert_set_by_tenant_id_and_email, and the harness
// resolves it by reflection: words match Go names without their underscores
// and case. Inputs decode as JSON into the method's shape types, results
// encode back to JSON, and both are compared under the same spelling.
package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thec1oud/dql/runtime/go/dql"
)

// Scenario is one scenario file: steps run in order against a fresh database
// of the named schema.
type Scenario struct {
	Name   string `yaml:"scenario"`
	Schema string `yaml:"schema"`
	Steps  []Step `yaml:"steps"`
	// File is where the scenario was read from.
	File string `yaml:"-"`
}

// Step is one operation and what it must produce.
type Step struct {
	Op string `yaml:"op"`
	// Scope reaches the store through the handle's WithScope.
	Scope map[string]any `yaml:"scope"`
	// Key, Input and Set fill the method's parameters in order: a Key shape
	// takes Key, the first other shape Input and the second Set.
	Key   any `yaml:"key"`
	Input any `yaml:"input"`
	Set   any `yaml:"set"`
	// Where is "all" for a filtered write over every row.
	Where string `yaml:"where"`
	// Assign holds the operation's expressions (§20.2).
	Assign []Assign `yaml:"assign"`
	// Save keeps the step's result for later steps' $name.field.
	Save   string         `yaml:"save"`
	Expect map[string]any `yaml:"expect"`
}

// Assign is one expression on a column: set to now or default, or add or
// subtract a value.
type Assign struct {
	Column string `yaml:"column"`
	Set    string `yaml:"set"`
	Add    any    `yaml:"add"`
	Sub    any    `yaml:"sub"`
}

// Schema is a schema the scenarios run against: its DDL, relative to the
// repository root, and how to open its generated handle and reach its
// tables' column handles.
type Schema struct {
	DDL    string
	Open   func(ctx context.Context, dsn string) (db any, close func(), err error)
	Tables map[string]any
}

// Load reads every scenario file in dir, sorted by file name.
func Load(dir string) ([]Scenario, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	var out []Scenario
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var s Scenario
		if err := yaml.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		s.File = f
		out = append(out, s)
	}
	return out, nil
}

// Run runs a scenario's steps on db, a generated handle, and returns each
// way the outcomes differ from the expectations.
func Run(ctx context.Context, sc Scenario, db any, tables map[string]any) []string {
	r := &runner{ctx: ctx, db: reflect.ValueOf(db), tables: tables, saved: map[string]any{}}
	var problems []string
	for i, st := range sc.Steps {
		for _, p := range r.step(st) {
			problems = append(problems, fmt.Sprintf("%s step %d (%s): %s", sc.Name, i+1, st.Op, p))
		}
	}
	return problems
}

type runner struct {
	ctx    context.Context
	db     reflect.Value
	tables map[string]any
	saved  map[string]any
}

// norm spells a name for matching: lower case, without underscores.
func norm(s string) string { return strings.ToLower(strings.ReplaceAll(s, "_", "")) }

// member returns v's method or field whose name norm matches name.
func member(v reflect.Value, name string) (reflect.Value, bool) {
	if !v.IsValid() {
		return reflect.Value{}, false
	}
	t := v.Type()
	for i := range t.NumMethod() {
		if norm(t.Method(i).Name) == norm(name) {
			return v.Method(i), true
		}
	}
	if v.Kind() == reflect.Struct {
		for i := range t.NumField() {
			if t.Field(i).IsExported() && norm(t.Field(i).Name) == norm(name) {
				return v.Field(i), true
			}
		}
	}
	return reflect.Value{}, false
}

func (r *runner) step(st Step) []string {
	storeName, method, _ := strings.Cut(st.Op, ".")
	h := r.db
	if st.Scope != nil {
		with, ok := member(h, "WithScope")
		if !ok {
			return []string{"the handle has no WithScope"}
		}
		scope, err := r.decode(st.Scope, with.Type().In(0))
		if err != nil {
			return []string{"scope: " + err.Error()}
		}
		h = with.Call([]reflect.Value{scope})[0]
	}
	store, ok := member(h, storeName)
	if !ok {
		return []string{"no store " + storeName}
	}
	m, ok := member(store, method)
	if !ok {
		return []string{"no method " + method + " on " + storeName}
	}
	args, err := r.args(st, storeName, m.Type())
	if err != nil {
		return []string{err.Error()}
	}
	out := m.Call(args)
	if e, _ := out[len(out)-1].Interface().(error); e != nil {
		return r.compare(st, nil, e)
	}
	var result any
	if len(out) == 2 {
		// The generated shapes always encode.
		raw, _ := json.Marshal(out[0].Interface())
		json.Unmarshal(raw, &result)
		result = normalize(result)
	}
	if st.Save != "" {
		r.saved[st.Save] = result
	}
	return r.compare(st, result, nil)
}

// args builds the method's arguments after its context.
func (r *runner) args(st Step, storeName string, t reflect.Type) ([]reflect.Value, error) {
	args := []reflect.Value{reflect.ValueOf(r.ctx)}
	payloads := []any{st.Input, st.Set}
	for i := 1; i < t.NumIn(); i++ {
		p := t.In(i)
		switch {
		case t.IsVariadic() && i == t.NumIn()-1:
			as, err := r.assignments(st.Assign, storeName, p.Elem())
			if err != nil {
				return nil, err
			}
			args = append(args, as...)
		case strings.HasPrefix(p.Name(), "Pred["):
			if st.Where != "all" {
				return nil, fmt.Errorf("where %q: only all is supported", st.Where)
			}
			all, _ := member(reflect.ValueOf(r.tables[storeName]), "AllRows")
			if !all.IsValid() {
				return nil, fmt.Errorf("no table %s", storeName)
			}
			args = append(args, all.Call(nil)[0])
		case strings.HasSuffix(p.Name(), "Key"):
			v, err := r.decode(st.Key, p)
			if err != nil {
				return nil, fmt.Errorf("key: %w", err)
			}
			args = append(args, v)
		default:
			v, err := r.decode(payloads[0], p)
			if err != nil {
				return nil, fmt.Errorf("input: %w", err)
			}
			payloads = payloads[1:]
			args = append(args, v)
		}
	}
	return args, nil
}

// assignments builds the step's expressions on the store's column handles.
func (r *runner) assignments(as []Assign, storeName string, elem reflect.Type) ([]reflect.Value, error) {
	var out []reflect.Value
	for _, a := range as {
		col, ok := member(reflect.ValueOf(r.tables[storeName]), a.Column)
		if !ok {
			return nil, fmt.Errorf("no column %s of %s", a.Column, storeName)
		}
		var call string
		var arg any
		switch {
		case a.Set == "now":
			call, arg = "Set", dql.Now()
		case a.Set == "default":
			call, arg = "Set", dql.Default()
		case a.Add != nil:
			call, arg = "Add", a.Add
		case a.Sub != nil:
			call, arg = "Sub", a.Sub
		default:
			return nil, fmt.Errorf("assign %s: no expression", a.Column)
		}
		m, ok := member(col, call)
		if !ok {
			return nil, fmt.Errorf("column %s has no %s", a.Column, call)
		}
		v := reflect.ValueOf(arg)
		if call != "Set" {
			var err error
			if v, err = r.decode(arg, m.Type().In(0)); err != nil {
				return nil, fmt.Errorf("assign %s: %w", a.Column, err)
			}
		} else if !v.Type().AssignableTo(m.Type().In(0)) {
			return nil, fmt.Errorf("column %s cannot be set to %s", a.Column, a.Set)
		}
		out = append(out, m.Call([]reflect.Value{v})[0].Convert(elem))
	}
	return out, nil
}

// decode resolves v's references and decodes it into a value of type t.
func (r *runner) decode(v any, t reflect.Type) (reflect.Value, error) {
	v, err := r.resolve(v)
	if err != nil {
		return reflect.Value{}, err
	}
	raw, _ := json.Marshal(normalize(v))
	out := reflect.New(t)
	if err := json.Unmarshal(raw, out.Interface()); err != nil {
		return reflect.Value{}, err
	}
	return out.Elem(), nil
}

// resolve replaces each "$name.field" string with that saved field.
func (r *runner) resolve(v any) (any, error) {
	switch x := v.(type) {
	case string:
		ref, ok := strings.CutPrefix(x, "$")
		if !ok {
			return x, nil
		}
		name, field, _ := strings.Cut(ref, ".")
		row, ok := r.saved[name].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("no saved row %s", name)
		}
		val, ok := row[norm(field)]
		if !ok {
			return nil, fmt.Errorf("saved row %s has no %s", name, field)
		}
		return val, nil
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			var err error
			if out[k], err = r.resolve(e); err != nil {
				return nil, err
			}
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			var err error
			if out[i], err = r.resolve(e); err != nil {
				return nil, err
			}
		}
		return out, nil
	}
	return v, nil
}

// normalize respells map keys by norm and turns YAML's numbers into JSON's.
func normalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[norm(k)] = normalize(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = normalize(e)
		}
		return out
	case int:
		return float64(x)
	}
	return v
}

// compare checks an outcome against the step's expectations: row (a
// subset of the result's fields, where "present" means not null), rows,
// none (no row), value, and error (class, constraint, kind, field).
func (r *runner) compare(st Step, result any, err error) []string {
	want, ok := st.Expect["error"]
	if err != nil {
		if !ok {
			return []string{"unexpected error: " + err.Error()}
		}
		return r.compareError(normalize(want), err)
	}
	if ok {
		return []string{fmt.Sprintf("want error %v, got none", want)}
	}
	var problems []string
	for _, k := range slices.Sorted(maps.Keys(st.Expect)) {
		w := normalize(st.Expect[k])
		switch k {
		case "row":
			problems = append(problems, r.compareRow("", w, result)...)
		case "rows":
			ws, _ := w.([]any)
			gs, _ := result.([]any)
			if len(ws) != len(gs) {
				problems = append(problems, fmt.Sprintf("want %d rows, got %d", len(ws), len(gs)))
				continue
			}
			for i := range ws {
				problems = append(problems, r.compareRow(fmt.Sprintf("row %d ", i), ws[i], gs[i])...)
			}
		case "none":
			if (result == nil) != (w == true) {
				problems = append(problems, fmt.Sprintf("want none %v, got %v", w, result))
			}
		case "value":
			if !reflect.DeepEqual(w, result) {
				problems = append(problems, fmt.Sprintf("want value %v, got %v", w, result))
			}
		default:
			problems = append(problems, "unknown expectation "+k)
		}
	}
	return problems
}

func (r *runner) compareRow(at string, want, got any) []string {
	w, ok := want.(map[string]any)
	g, gok := got.(map[string]any)
	if !ok || !gok {
		return []string{fmt.Sprintf("%swant row %v, got %v", at, want, got)}
	}
	var problems []string
	for _, k := range slices.Sorted(maps.Keys(w)) {
		wv, err := r.resolve(w[k])
		if err != nil {
			problems = append(problems, at+err.Error())
			continue
		}
		gv, present := g[k]
		switch {
		case !present:
			problems = append(problems, fmt.Sprintf("%sno field %s", at, k))
		case wv == "present":
			if gv == nil {
				problems = append(problems, fmt.Sprintf("%s%s is null", at, k))
			}
		case !reflect.DeepEqual(wv, gv):
			problems = append(problems, fmt.Sprintf("%s%s: want %v, got %v", at, k, wv, gv))
		}
	}
	return problems
}

func (r *runner) compareError(want any, err error) []string {
	got := map[string]any{"class": string(dql.ClassOf(err))}
	var v interface{ Violated() dql.Constraint }
	if errors.As(err, &v) {
		name := v.Violated().Name
		got["constraint"] = name[strings.LastIndex(name, ".")+1:]
	}
	var vs dql.ValidationErrors
	if errors.As(err, &vs) {
		got["kind"], got["field"] = string(vs[0].Kind), vs[0].Field
	}
	w, ok := want.(map[string]any)
	if !ok {
		return []string{fmt.Sprintf("error expectation %v is not a map", want)}
	}
	var problems []string
	for _, k := range slices.Sorted(maps.Keys(w)) {
		if g, ok := got[k]; !ok || g != w[k] {
			problems = append(problems, fmt.Sprintf("error %s: want %v, got %v (%v)", k, w[k], g, err))
		}
	}
	return problems
}
