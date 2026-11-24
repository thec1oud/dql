// Package harness runs the conformance scenarios (guide §6.3) against the Go
// emitter's generated code. A step's op names a store and a method, as
// users.insert or users.upsert_set_by_tenant_id_and_email, and the harness
// resolves it by reflection: words match Go names without their underscores
// and case. Inputs decode as JSON into the method's shape types, results
// encode back to JSON, and both are compared under the same spelling. A
// nested change is an object of one key, its kind, holding the change's key,
// draft or partial, as author: { link: { id: 1 } }, or for an update by key
// both, as posts: [{ update: { key: { id: 1 }, input: { title: x } } }]; the
// schema lists the functions that build them.
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
	"time"

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

	// A transaction step, op write or read, runs Steps in a transaction, on
	// its context (§22.3). Propagation (join, independent, nested,
	// mandatory, never), Isolation and Retry configure it, Inject makes the
	// server fail its next writes, and Rollback ends each run with an error,
	// so that it rolls back. Its expect takes error, and attempts, how many
	// times its closure ran. A step in it whose call fails where it expects
	// no error ends the run with that error.
	Steps       []Step  `yaml:"steps"`
	Propagation string  `yaml:"propagation"`
	Isolation   string  `yaml:"isolation"`
	Retry       *Retry  `yaml:"retry"`
	Inject      *Inject `yaml:"inject"`
	Rollback    bool    `yaml:"rollback"`
}

// Retry is a transaction's retry policy: how many runs, without backoff.
type Retry struct {
	Attempts int `yaml:"attempts"`
}

// Inject is a failure of a retryable class that the server raises for the
// transaction's next Times writes.
type Inject struct {
	Class string `yaml:"class"`
	Times int    `yaml:"times"`
}

// Assign is one expression on a column: set to now or default, or add or
// subtract a value.
type Assign struct {
	Column string `yaml:"column"`
	Set    string `yaml:"set"`
	Add    any    `yaml:"add"`
	Sub    any    `yaml:"sub"`
}

// Schema is a schema the scenarios run against: its DDL files, relative to
// the repository root, how to open its generated handle and reach its
// tables' column handles, the functions that build its nested changes (each
// change type's constructors, as shopdb.PostAuthorLink, and opt.Some for
// each optional nest's type, as opt.Some[shopdb.PostAuthorChange]), and how
// to make the database at dsn fail its next times writes with class.
type Schema struct {
	DDL     []string
	Open    func(ctx context.Context, dsn string) (db any, close func(), err error)
	Tables  map[string]any
	Changes []any
	Inject  func(ctx context.Context, dsn, class string, times int) error
}

// Target is what a scenario runs on: a schema's handle, open on the
// database at DSN.
type Target struct {
	Schema Schema
	DB     any
	DSN    string
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

// Run runs a scenario's steps on t and returns each way the outcomes differ
// from the expectations.
func Run(ctx context.Context, sc Scenario, t Target) []string {
	s := t.Schema
	r := &runner{ctx: ctx, db: reflect.ValueOf(t.DB), tables: s.Tables, saved: map[string]any{},
		inject:  func(class string, times int) error { return s.Inject(ctx, t.DSN, class, times) },
		changes: map[reflect.Type]map[string]reflect.Value{}, some: map[reflect.Type]reflect.Value{},
		nested: map[reflect.Type]bool{}}
	for _, f := range s.Changes {
		r.index(reflect.ValueOf(f))
	}
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
	inject func(class string, times int) error
	// changes holds each change type's constructors by kind, some the opt.Some
	// of each optional type, and nested whether a type holds either.
	changes map[reflect.Type]map[string]reflect.Value
	some    map[reflect.Type]reflect.Value
	nested  map[reflect.Type]bool
}

// changer is what every nested change type implements.
type changer interface{ Change() dql.Change }

// index files f, a change constructor or an opt.Some, under its result type,
// learning a constructor's kind by calling it.
func (r *runner) index(f reflect.Value) {
	t := f.Type()
	var zeros []reflect.Value
	for i := range t.NumIn() {
		zeros = append(zeros, reflect.Zero(t.In(i)))
	}
	out := t.Out(0)
	res := f.Call(zeros)[0].Interface()
	c, ok := res.(changer)
	if !ok {
		r.some[out] = f
		return
	}
	if r.changes[out] == nil {
		r.changes[out] = map[string]reflect.Value{}
	}
	r.changes[out][string(c.Change())] = f
}

// nests reports whether a value of type t holds a nested change.
func (r *runner) nests(t reflect.Type) bool {
	if n, ok := r.nested[t]; ok {
		return n
	}
	r.nested[t] = false
	n := r.changes[t] != nil || r.some[t].IsValid()
	switch t.Kind() {
	case reflect.Slice:
		n = n || r.nests(t.Elem())
	case reflect.Struct:
		for i := range t.NumField() {
			n = n || t.Field(i).IsExported() && r.nests(t.Field(i).Type)
		}
	}
	r.nested[t] = n
	return n
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
	ps, _ := r.run(st)
	return ps
}

// run runs one step and returns its problems, and the error of a call that
// failed where the step expects none.
func (r *runner) run(st Step) ([]string, error) {
	storeName, method, ok := strings.Cut(st.Op, ".")
	if !ok {
		return r.transact(st)
	}
	result, err, problems := r.call(st, storeName, method)
	if problems != nil {
		return problems, nil
	}
	if _, ok := st.Expect["error"]; err != nil && !ok {
		return []string{"unexpected error: " + err.Error()}, err
	}
	if err == nil && st.Save != "" {
		r.saved[st.Save] = result
	}
	return r.compare(st, result, err), nil
}

// call calls the step's method and returns its result, normalized, or its
// error, or the problems that kept it from being called.
func (r *runner) call(st Step, storeName, method string) (any, error, []string) {
	h := r.db
	if st.Scope != nil {
		with, ok := member(h, "WithScope")
		if !ok {
			return nil, nil, []string{"the handle has no WithScope"}
		}
		scope, err := r.decode(st.Scope, with.Type().In(0))
		if err != nil {
			return nil, nil, []string{"scope: " + err.Error()}
		}
		h = with.Call([]reflect.Value{scope})[0]
	}
	store, ok := member(h, storeName)
	if !ok {
		return nil, nil, []string{"no store " + storeName}
	}
	m, ok := member(store, method)
	if !ok {
		return nil, nil, []string{"no method " + method + " on " + storeName}
	}
	args, err := r.args(st, storeName, m.Type())
	if err != nil {
		return nil, nil, []string{err.Error()}
	}
	out := m.Call(args)
	if e, _ := out[len(out)-1].Interface().(error); e != nil {
		return nil, e, nil
	}
	var result any
	if len(out) == 2 {
		// The generated shapes always encode.
		raw, _ := json.Marshal(out[0].Interface())
		json.Unmarshal(raw, &result)
		result = normalize(result)
	}
	return result, nil, nil
}

// errRollback ends a run of a transaction step's closure that rolls back.
var errRollback = errors.New("conformance: rolled back")

var propagations = map[string]dql.Propagation{
	"join": dql.Join, "independent": dql.Independent, "nested": dql.Nested,
	"mandatory": dql.Mandatory, "never": dql.Never,
}

// transact runs a transaction step: its steps in the handle's Write or Read
// closure, on the closure's context. Like run, it returns its problems and
// an error the step does not expect.
func (r *runner) transact(st Step) ([]string, error) {
	ps, err := r.transaction(st)
	if _, ok := st.Expect["error"]; err != nil && !ok {
		return []string{"unexpected error: " + err.Error()}, err
	}
	return ps, nil
}

func (r *runner) transaction(st Step) ([]string, error) {
	m, ok := member(r.db, st.Op)
	if !ok {
		return []string{"no transaction " + st.Op}, nil
	}
	var opts []reflect.Value
	if st.Propagation != "" {
		p, ok := propagations[st.Propagation]
		if !ok {
			return []string{"no propagation " + st.Propagation}, nil
		}
		opts = append(opts, reflect.ValueOf(dql.WithPropagation(p)))
	}
	if st.Isolation != "" {
		opts = append(opts, reflect.ValueOf(dql.WithIsolation(dql.Isolation(st.Isolation))))
	}
	if st.Retry != nil {
		opts = append(opts, reflect.ValueOf(dql.WithRetry(dql.RetryPolicy{Attempts: st.Retry.Attempts, Initial: time.Microsecond, Max: time.Microsecond})))
	}
	if st.Inject != nil {
		if err := r.inject(st.Inject.Class, st.Inject.Times); err != nil {
			return []string{"inject: " + err.Error()}, nil
		}
		defer r.inject("", 0)
	}
	runs := 0
	var inner []string
	fn := reflect.MakeFunc(m.Type().In(1), func(in []reflect.Value) []reflect.Value {
		runs++
		inner = nil
		outer := r.ctx
		r.ctx = in[0].Interface().(context.Context)
		defer func() { r.ctx = outer }()
		var err error
		for i, s := range st.Steps {
			ps, e := r.run(s)
			if e != nil {
				err = e
				break
			}
			for _, p := range ps {
				inner = append(inner, fmt.Sprintf("step %d (%s): %s", i+1, s.Op, p))
			}
		}
		if err == nil && st.Rollback {
			err = errRollback
		}
		out := reflect.New(reflect.TypeFor[error]()).Elem()
		if err != nil {
			out.Set(reflect.ValueOf(err))
		}
		return []reflect.Value{out}
	})
	out := m.Call(append([]reflect.Value{reflect.ValueOf(r.ctx), fn}, opts...))
	err, _ := out[0].Interface().(error)
	if errors.Is(err, errRollback) {
		err = nil
	}
	problems := inner
	expect := maps.Clone(st.Expect)
	if w, ok := expect["attempts"]; ok {
		if normalize(w) != float64(runs) {
			problems = append(problems, fmt.Sprintf("attempts: want %v, got %d", w, runs))
		}
		delete(expect, "attempts")
	}
	return append(problems, r.compare(Step{Expect: expect}, nil, err)...), err
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
	return r.build(normalize(v), t)
}

// build makes a value of type t from v: nested changes through the schema's
// functions, the rest as JSON.
func (r *runner) build(v any, t reflect.Type) (reflect.Value, error) {
	if some, ok := r.some[t]; ok {
		x, err := r.build(v, some.Type().In(0))
		if err != nil {
			return reflect.Value{}, err
		}
		return some.Call([]reflect.Value{x})[0], nil
	}
	if kinds, ok := r.changes[t]; ok {
		m, _ := v.(map[string]any)
		if len(m) != 1 {
			return reflect.Value{}, fmt.Errorf("a %s is an object of one of %s", t.Name(), strings.Join(slices.Sorted(maps.Keys(kinds)), ", "))
		}
		for kind, arg := range m {
			f, ok := kinds[kind]
			if !ok {
				return reflect.Value{}, fmt.Errorf("a %s cannot %s", t.Name(), kind)
			}
			args := []any{arg}
			if f.Type().NumIn() == 2 {
				m, _ := arg.(map[string]any)
				args = []any{m["key"], m["input"]}
			}
			var xs []reflect.Value
			for i, a := range args {
				x, err := r.build(a, f.Type().In(i))
				if err != nil {
					return reflect.Value{}, fmt.Errorf("%s: %w", kind, err)
				}
				xs = append(xs, x)
			}
			return f.Call(xs)[0], nil
		}
	}
	if !r.nests(t) {
		raw, _ := json.Marshal(v)
		out := reflect.New(t)
		if err := json.Unmarshal(raw, out.Interface()); err != nil {
			return reflect.Value{}, err
		}
		return out.Elem(), nil
	}
	if t.Kind() == reflect.Slice {
		xs, ok := v.([]any)
		if !ok {
			return reflect.Value{}, fmt.Errorf("want a list for %s, got %v", t, v)
		}
		out := reflect.MakeSlice(t, len(xs), len(xs))
		for i, x := range xs {
			e, err := r.build(x, t.Elem())
			if err != nil {
				return reflect.Value{}, fmt.Errorf("%d: %w", i, err)
			}
			out.Index(i).Set(e)
		}
		return out, nil
	}
	// The struct embeds its flat shape, which takes the whole object, and
	// holds its nests as fields.
	m, ok := v.(map[string]any)
	if !ok {
		return reflect.Value{}, fmt.Errorf("want an object for %s, got %v", t.Name(), v)
	}
	out := reflect.New(t).Elem()
	for i := range t.NumField() {
		f := t.Field(i)
		x, present := m[norm(f.Name)]
		if f.Anonymous {
			x, present = m, true
		}
		if !present {
			continue
		}
		e, err := r.build(x, f.Type)
		if err != nil {
			return reflect.Value{}, fmt.Errorf("%s: %w", f.Name, err)
		}
		out.Field(i).Set(e)
	}
	return out, nil
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
// none (no row), value, and error (class, constraint, kind, field, and
// misuse, for the runtime's errors of misuse).
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
		if !present {
			problems = append(problems, fmt.Sprintf("%sno field %s", at, k))
			continue
		}
		problems = append(problems, r.match(at+k, wv, gv)...)
	}
	return problems
}

// match compares one field: "present" means not null, an object a subset of
// the fields of a nested row, and a list of objects as many nested rows.
func (r *runner) match(path string, want, got any) []string {
	w, isRow := want.(map[string]any)
	ws, _ := want.([]any)
	gs, _ := got.([]any)
	var isRows bool
	if len(ws) > 0 {
		_, isRows = ws[0].(map[string]any)
	}
	switch {
	case want == "present":
		if got == nil {
			return []string{path + " is null"}
		}
	case isRow:
		return r.compareRow(path+".", w, got)
	case isRows:
		if len(ws) != len(gs) {
			return []string{fmt.Sprintf("%s: want %d rows, got %d", path, len(ws), len(gs))}
		}
		var problems []string
		for i := range ws {
			problems = append(problems, r.compareRow(fmt.Sprintf("%s.%d.", path, i), ws[i], gs[i])...)
		}
		return problems
	case !reflect.DeepEqual(want, got):
		return []string{fmt.Sprintf("%s: want %v, got %v", path, want, got)}
	}
	return nil
}

// misuses are the runtime's errors of misuse, which have no class, by the
// name an error expectation's misuse gives them.
var misuses = map[string]error{
	"NoTransaction": dql.ErrNoTransaction, "InTransaction": dql.ErrInTransaction,
	"ReadOnly": dql.ErrReadOnly, "IndependentDepth": dql.ErrIndependentDepth,
}

func (r *runner) compareError(want any, err error) []string {
	got := map[string]any{"class": string(dql.ClassOf(err))}
	for name, m := range misuses {
		if errors.Is(err, m) {
			got["misuse"] = name
		}
	}
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
