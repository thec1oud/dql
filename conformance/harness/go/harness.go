// Package harness runs the conformance scenarios (guide §6.3) against the Go
// emitter's generated code. A step's op names a store and a method, as
// users.insert or users.upsert_set_by_tenant_id_and_email, and the harness
// resolves it by reflection: words match Go names without their underscores
// and case. Inputs decode as JSON into the method's shape types, results
// encode back to JSON, and both are compared under the same spelling. A
// nested change is an object of one key, its kind, holding the change's key,
// draft or partial, as author: { link: { id: 1 } }, or for an update by key
// both, as posts: [{ update: { key: { id: 1 }, input: { title: x } } }]; the
// schema lists the functions that build them. A step of op select reads a
// shape through the query builder (§18.2): all its rows, a stream of them, or
// their count. A scenario of replica runs on a source with a streaming
// replica, and its control steps start a session, pause or resume the
// replica's replay, or sleep (§22.5). A step's call may run under a
// deadline, be canceled, or have its connection terminated while the server
// sleeps in an injected delay, so that each infrastructure error is the
// server's or the driver's own (§23.1). An operation step runs its steps as
// one operation, in which a pair-form transaction left open is rolled back
// and reported (§22.3). A step of op sql runs its dialect's statements on
// the database, as a scenario that puts a value the schema's assertions
// rule out in place does (§13). A step of op convert calls a generated
// conversion, a method of a shape's value or a function, and checks its
// result, or that it is not generated (§14.9).
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
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/thec1oud/dql/runtime/go/dql"
)

// Scenario is one scenario file: steps run in order against a fresh database
// of the named schema, on a source with a replica when Replica is set. On a
// dialect whose schema lacks a feature the scenario requires, it is skipped
// and listed (decided 2026-11-26).
type Scenario struct {
	Name     string   `yaml:"scenario"`
	Schema   string   `yaml:"schema"`
	Replica  bool     `yaml:"replica"`
	Requires []string `yaml:"requires"`
	// MaxConns bounds the primary's pool; 0 is the schema's default.
	MaxConns int    `yaml:"max_conns"`
	Steps    []Step `yaml:"steps"`
	// File is where the scenario was read from.
	File string `yaml:"-"`
}

// Step is one operation and what it must produce.
type Step struct {
	Op string `yaml:"op"`
	// Scope reaches the store through the handle's WithScope.
	Scope map[string]any `yaml:"scope"`
	// Key, Input, Bind and Set fill the method's parameters in order: a Key
	// shape takes Key, and the other parameters take Input, then each of
	// Bind, a custom write shape's bound fields in its order (§14.8), then
	// Set.
	Key   any   `yaml:"key"`
	Input any   `yaml:"input"`
	Bind  []any `yaml:"bind"`
	Set   any   `yaml:"set"`
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
	// no error ends the run with that error. Op retry runs Steps the same
	// way under Retry(policy).Do, outside a transaction, with Retry's
	// attempts and On, the classes it reruns after (§22.4).
	Steps       []Step  `yaml:"steps"`
	Propagation string  `yaml:"propagation"`
	Isolation   string  `yaml:"isolation"`
	Retry       *Retry  `yaml:"retry"`
	Inject      *Inject `yaml:"inject"`
	Rollback    bool    `yaml:"rollback"`

	// A select step, op select, reads Shape through the store From of the
	// handle Scope reaches, keeping the rows every Filter holds for, in
	// Order, at most Limit of them. Read
	// is all (the default), one, the only row, iter, which streams the rows
	// and, with Stop, ends iteration after that many, or count.
	From   string     `yaml:"from"`
	Shape  string     `yaml:"shape"`
	Filter []Filter   `yaml:"filter"`
	Order  []OrderKey `yaml:"order"`
	Limit  int64      `yaml:"limit"`
	Read   string     `yaml:"read"`
	Stop   int        `yaml:"stop"`
	// Deleted is with to keep soft-deleted rows too, or only to keep only
	// them (§21.3).
	Deleted string `yaml:"deleted"`
	// Distinct reads each distinct row once.
	Distinct bool `yaml:"distinct"`
	// Consistency is the read's level: primary, any, bounded, with Bound,
	// or read_your_writes, with Wait and OrFail. Bound and Wait are Go
	// durations, as 500ms.
	Consistency string `yaml:"consistency"`
	Bound       string `yaml:"bound"`
	Wait        string `yaml:"wait"`
	OrFail      bool   `yaml:"or_fail"`

	// Duration is how long a sleep step sleeps.
	Duration string `yaml:"duration"`

	// Deadline and Cancel end a call's context after that long, as a
	// deadline or a cancellation, and Terminate ends its connections after
	// that long. Each is a Go duration.
	Deadline  string `yaml:"deadline"`
	Cancel    string `yaml:"cancel"`
	Terminate string `yaml:"terminate"`

	// A begin step, op begin_write or begin_read, begins a pair-form
	// transaction, runs Steps on it and ends it as End says: commit,
	// rollback, or, when empty, not at all, leaving it open.
	End string `yaml:"end"`

	// SQL holds a sql step's statements by dialect, and a raw step's: it
	// runs them through the handle's Raw on Source, as Exec when Exec is
	// set, whose value is the rows affected, and otherwise as Query, whose
	// value is the rows. Args are its parameters (§17, §22.1).
	SQL    map[string]string `yaml:"sql"`
	Source string            `yaml:"source"`
	Exec   bool              `yaml:"exec"`

	// A convert step calls Call: with From, the method of that shape's
	// value decoded from Input, and without it, the schema's function. Args
	// fill the rest of its parameters in order. Its expect takes generated:
	// false for a conversion that must not exist, and a fallible one's error.
	Call string `yaml:"call"`
	Args []any  `yaml:"args"`
}

// Filter is a predicate of a select step: the column's operator Op, as eq
// or in, on Value. Column may name a relation, whose Op is any or none,
// with no Value.
type Filter struct {
	Column string `yaml:"column"`
	Op     string `yaml:"op"`
	Value  any    `yaml:"value"`
}

// OrderKey is a key of a select step's order: the column, ascending unless
// Desc.
type OrderKey struct {
	Column string `yaml:"column"`
	Desc   bool   `yaml:"desc"`
}

// Retry is a transaction's retry policy: how many runs, without backoff.
type Retry struct {
	Attempts int `yaml:"attempts"`
	// On is the error classes an op retry step reruns after (§22.4).
	On []string `yaml:"on"`
}

// Inject is what the server does on the step's next Times writes: sleep for
// Delay, a Go duration, or else raise a failure of the retryable Class.
type Inject struct {
	Class string `yaml:"class"`
	Times int    `yaml:"times"`
	Delay string `yaml:"delay"`
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
// to make the database at dsn fail or stall its next writes, and to end
// every other connection to it. Types holds a zero value of each of its
// shape types, and Funcs its generated functions that convert, as
// MergeUserByKey, by their names. Dialect names the dialect of its DDL and of
// sql steps, and Exec runs statements on the database at dsn. Shapes
// holds its read shapes, and Selects the Select that reads each, by the
// shape's name, instantiated for it, as
// shopdb.Select[shopdb.TenantFull, shopdb.TenantsTable]. Open opens the
// handle and its executor on the database at dsn, with replicas of it at the
// replicas' DSNs and at most maxConns connections to it, 0 meaning a default,
// sending statements in mode, and Replay pauses or resumes the replay of the
// replica at dsn. URLEnv names the environment variable that holds the
// URL of its server, on which Database creates a database from the DDL
// files; Modes are the statement modes its dialect offers (§26.4), and
// Features what it holds that a scenario may require.
type Schema struct {
	DDL      []string
	Dialect  string
	URLEnv   string
	Database func(ctx context.Context, base string, ddl ...string) (dsn string, drop func(), err error)
	Modes    []dql.StatementMode
	Features []string
	// Fill gives a Draft field the dialect requires a value for when a
	// step leaves it out, by type and normalized field name: on MySQL, a
	// key filled by an expression default (STANDING-RULES, 2026-11-26).
	Fill      map[string]map[string]func() any
	Exec      func(ctx context.Context, dsn, sql string) error
	Open      func(ctx context.Context, dsn string, replicas []string, maxConns int, mode dql.StatementMode, opts ...dql.Option) (db any, exec *dql.Executor, err error)
	Replay    func(ctx context.Context, dsn string, pause bool) error
	Tables    map[string]any
	Changes   []any
	Inject    func(ctx context.Context, dsn string, in Inject) error
	Terminate func(ctx context.Context, dsn string) error
	Shapes    any
	Selects   map[string]any
	Types     []any
	Funcs     map[string]any
}

// Target is what a scenario runs on: a schema's handle and its executor,
// open on the database at DSN and its replica at ReplicaDSN, if any, and the
// recorder of the executor's hook events. Record, if set, receives each
// step's outcome.
type Target struct {
	Schema     Schema
	DB         any
	Exec       *dql.Executor
	DSN        string
	ReplicaDSN string
	Events     *Recorder
	Record     func(Outcome)
}

// Outcome is what one step produced, which the Go and TypeScript harnesses
// both write so that CI can compare them (guide §6.3): the step, as its
// index and those of the steps that hold it, from 1, joined by dots, its
// result, normalized, or its error's fields that expectations match, and
// the hook events it fired.
type Outcome struct {
	Step   string         `json:"step"`
	Op     string         `json:"op"`
	Result any            `json:"result"`
	Error  map[string]any `json:"error,omitempty"`
	Events []any          `json:"events"`
}

// Missing returns the features sc requires that s lacks.
func Missing(sc Scenario, s Schema) []string {
	var out []string
	for _, f := range sc.Requires {
		if !slices.Contains(s.Features, f) {
			out = append(out, f)
		}
	}
	return out
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
	r := &runner{ctx: ctx, db: reflect.ValueOf(t.DB), tables: s.Tables, saved: map[string]any{}, events: t.Events,
		inject:    func(in Inject) error { return s.Inject(ctx, t.DSN, in) },
		terminate: func() error { return s.Terminate(ctx, t.DSN) },
		exec:      func(sql string) error { return s.Exec(ctx, t.DSN, sql) },
		dialect:   s.Dialect,
		types:     map[string]reflect.Type{}, funcs: map[string]reflect.Value{},
		operation: func(ctx context.Context) (context.Context, func()) { return t.Exec.Operation(ctx) },
		replay:    func(pause bool) error { return s.Replay(ctx, t.ReplicaDSN, pause) },
		base:      ctx,
		changes:   map[reflect.Type]map[string]reflect.Value{}, some: map[reflect.Type]reflect.Value{},
		nested: map[reflect.Type]bool{}, shapes: reflect.ValueOf(s.Shapes), selects: map[string]reflect.Value{},
		record: t.Record, fill: s.Fill}
	for name, f := range s.Selects {
		r.selects[norm(name)] = reflect.ValueOf(f)
	}
	for _, v := range s.Types {
		t := reflect.TypeOf(v)
		r.types[norm(t.Name())] = t
	}
	for name, f := range s.Funcs {
		r.funcs[norm(name)] = reflect.ValueOf(f)
	}
	for _, f := range s.Changes {
		r.index(reflect.ValueOf(f))
	}
	var problems []string
	for i, st := range sc.Steps {
		r.path = []int{i + 1}
		for _, p := range r.step(st) {
			problems = append(problems, fmt.Sprintf("%s step %d (%s): %s", sc.Name, i+1, st.Op, p))
		}
	}
	if r.paused {
		if err := r.replay(false); err != nil {
			problems = append(problems, fmt.Sprintf("%s: resume_replay: %s", sc.Name, err))
		}
	}
	return problems
}

type runner struct {
	fill      map[string]map[string]func() any
	ctx       context.Context
	db        reflect.Value
	tables    map[string]any
	saved     map[string]any
	inject    func(Inject) error
	terminate func() error
	// exec runs a sql step's statements, those of dialect.
	exec    func(sql string) error
	dialect string
	// types holds the shape types, and funcs the conversion functions, by
	// their names under norm.
	types map[string]reflect.Type
	funcs map[string]reflect.Value
	// operation begins an operation on ctx and returns the function that
	// ends it.
	operation func(ctx context.Context) (context.Context, func())
	// replay pauses or resumes the replica's replay, paused says whether it
	// is paused, and base is the context a session step starts from.
	replay func(pause bool) error
	paused bool
	base   context.Context
	// events records the hooks, and fired holds what the step being
	// compared fired.
	events *Recorder
	fired  []any
	// changes holds each change type's constructors by kind, some the opt.Some
	// of each optional type, and nested whether a type holds either.
	changes map[reflect.Type]map[string]reflect.Value
	some    map[reflect.Type]reflect.Value
	nested  map[reflect.Type]bool
	// shapes holds the schema's read shapes, and selects their Selects.
	shapes  reflect.Value
	selects map[string]reflect.Value
	// record receives each step's outcome, and path is the step's place.
	record func(Outcome)
	path   []int
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

// control runs a control step, reporting false for any other op.
func (r *runner) control(st Step) ([]string, bool) {
	switch st.Op {
	case "session":
		r.ctx = dql.WithSession(r.base)
	case "pause_replay", "resume_replay":
		pause := st.Op == "pause_replay"
		if err := r.replay(pause); err != nil {
			return []string{err.Error()}, true
		}
		r.paused = pause
	case "sql":
		sql, ok := st.SQL[r.dialect]
		if !ok {
			return []string{"no sql for " + r.dialect}, true
		}
		if err := r.exec(sql); err != nil {
			return []string{err.Error()}, true
		}
	case "sleep":
		d, err := time.ParseDuration(st.Duration)
		if err != nil {
			return []string{err.Error()}, true
		}
		time.Sleep(d)
	default:
		return nil, false
	}
	return nil, true
}

// run runs one step and returns its problems, and the error of a call that
// failed where the step expects none.
func (r *runner) run(st Step) ([]string, error) {
	if ps, ok := r.control(st); ok {
		return ps, nil
	}
	switch st.Op {
	case "operation":
		return r.operate(st), nil
	case "begin_write", "begin_read":
		return r.begin(st), nil
	}
	if st.Op == "convert" {
		return r.convert(st), nil
	}
	if st.Op == "raw" {
		return r.raw(st)
	}
	storeName, method, ok := strings.Cut(st.Op, ".")
	if !ok && st.Op != "select" {
		return r.transact(st)
	}
	finish, problems := r.prepare(st)
	if problems != nil {
		return problems, nil
	}
	mark := r.events.mark()
	var result any
	var err error
	if ok {
		result, err, problems = r.call(st, storeName, method)
	} else {
		result, err, problems = r.read(st)
	}
	r.fired = r.events.since(mark)
	problems = append(problems, finish()...)
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

// prepare sets up a step's call: the injected delay or failure, and the
// deadline, cancellation and termination, which act on r.ctx. The function
// it returns undoes them once the call returns, and reports what failed.
func (r *runner) prepare(st Step) (func() []string, []string) {
	outer := r.ctx
	undo := []func() []string{func() []string {
		r.ctx = outer
		return nil
	}}
	finish := func() []string {
		var problems []string
		for i := len(undo) - 1; i >= 0; i-- {
			problems = append(problems, undo[i]()...)
		}
		return problems
	}
	if st.Inject != nil {
		if err := r.inject(*st.Inject); err != nil {
			return nil, []string{"inject: " + err.Error()}
		}
		undo = append(undo, func() []string {
			r.inject(Inject{})
			return nil
		})
	}
	for _, t := range []struct {
		name, after string
		start       func(d time.Duration) func() []string
	}{
		{"deadline", st.Deadline, func(d time.Duration) func() []string {
			ctx, cancel := context.WithTimeout(r.ctx, d)
			r.ctx = ctx
			return func() []string { cancel(); return nil }
		}},
		{"cancel", st.Cancel, func(d time.Duration) func() []string {
			ctx, cancel := context.WithCancel(r.ctx)
			r.ctx = ctx
			stop := time.AfterFunc(d, cancel)
			return func() []string { stop.Stop(); cancel(); return nil }
		}},
		{"terminate", st.Terminate, func(d time.Duration) func() []string {
			done := make(chan error, 1)
			go func() {
				time.Sleep(d)
				done <- r.terminate()
			}()
			return func() []string {
				if err := <-done; err != nil {
					return []string{"terminate: " + err.Error()}
				}
				return nil
			}
		}},
	} {
		if t.after == "" {
			continue
		}
		d, err := time.ParseDuration(t.after)
		if err != nil {
			finish()
			return nil, []string{fmt.Sprintf("%s: %s", t.name, err)}
		}
		undo = append(undo, t.start(d))
	}
	return finish, nil
}

// operate runs an operation step: its steps in one operation, which rolls
// back and reports each pair-form transaction left open when it ends. Its
// expect takes events, all that the steps and the operation's end fired.
func (r *runner) operate(st Step) []string {
	mark := r.events.mark()
	outer := r.ctx
	ctx, end := r.operation(r.ctx)
	r.ctx = ctx
	problems := r.steps(st.Steps)
	r.ctx = outer
	end()
	r.fired = r.events.since(mark)
	return append(problems, r.compare(Step{Op: st.Op, Expect: st.Expect}, nil, nil)...)
}

// steps runs steps in order and returns their problems, each prefixed with
// its step.
func (r *runner) steps(steps []Step) []string {
	var problems []string
	path := r.path
	for i, s := range steps {
		r.path = append(slices.Clip(path), i+1)
		ps, _ := r.run(s)
		for _, p := range ps {
			problems = append(problems, fmt.Sprintf("step %d (%s): %s", i+1, s.Op, p))
		}
	}
	r.path = path
	return problems
}

// ends are a begin step's ends by the method that ends the transaction.
var ends = map[string]string{"commit": "Commit", "rollback": "Rollback", "": ""}

// begin runs a begin step: it begins a pair-form transaction, runs its steps
// on the transaction's stores and context, and then commits it, rolls it
// back or leaves it open. Its expect takes error, of the beginning or the
// end, and events.
func (r *runner) begin(st Step) []string {
	end, ok := ends[st.End]
	if !ok {
		return []string{"no end " + st.End}
	}
	m, ok := member(r.db, map[string]string{"begin_write": "BeginWrite", "begin_read": "BeginRead"}[st.Op])
	if !ok {
		return []string{"no transaction " + st.Op}
	}
	mark := r.events.mark()
	out := m.Call([]reflect.Value{reflect.ValueOf(r.ctx)})
	err, _ := out[1].Interface().(error)
	var problems []string
	if err == nil {
		tx := out[0]
		outerDB, outerCtx := r.db, r.ctx
		r.db = tx
		r.ctx = tx.MethodByName("Context").Call([]reflect.Value{reflect.ValueOf(r.ctx)})[0].Interface().(context.Context)
		problems = r.steps(st.Steps)
		r.db, r.ctx = outerDB, outerCtx
		if end != "" {
			err, _ = tx.MethodByName(end).Call([]reflect.Value{reflect.ValueOf(r.ctx)})[0].Interface().(error)
		}
	}
	r.fired = r.events.since(mark)
	return append(problems, r.compare(Step{Op: st.Op, Expect: st.Expect}, nil, err)...)
}

// convert runs a convert step.
// raw runs a raw step through the handle's escape hatch (§17, §22.1).
func (r *runner) raw(st Step) ([]string, error) {
	sql, ok := st.SQL[r.dialect]
	if !ok {
		return []string{"no sql for " + r.dialect}, nil
	}
	h := r.db.MethodByName("Raw").Call([]reflect.Value{reflect.ValueOf(st.Source)})[0]
	method := "Query"
	if st.Exec {
		method = "Exec"
	}
	args := []reflect.Value{reflect.ValueOf(r.ctx), reflect.ValueOf(sql)}
	for _, a := range st.Args {
		v, err := r.resolve(a)
		if err != nil {
			return []string{"args: " + err.Error()}, nil
		}
		args = append(args, reflect.ValueOf(v))
	}
	mark := r.events.mark()
	out := h.MethodByName(method).Call(args)
	r.fired = r.events.since(mark)
	err, _ := out[1].Interface().(error)
	if _, ok := st.Expect["error"]; err != nil && !ok {
		return []string{"unexpected error: " + err.Error()}, err
	}
	result := out[0].Interface()
	if n, ok := result.(int64); ok {
		result = float64(n)
	} else {
		rows := []any{}
		for _, row := range result.([]dql.Row) {
			rows = append(rows, normalize([]any(row)))
		}
		result = rows
	}
	return r.compare(st, result, err), nil
}

func (r *runner) convert(st Step) []string {
	var f reflect.Value
	var args []reflect.Value
	if st.From != "" {
		t, ok := r.types[norm(st.From)]
		if !ok {
			return []string{"no shape type " + st.From}
		}
		v, err := r.decode(st.Input, t)
		if err != nil {
			return []string{"input: " + err.Error()}
		}
		f, _ = member(v, st.Call)
	} else {
		f = r.funcs[norm(st.Call)]
	}
	expect := maps.Clone(st.Expect)
	generated, _ := expect["generated"].(bool)
	if _, ok := expect["generated"]; ok && !generated {
		if f.IsValid() {
			return []string{st.Call + " is generated"}
		}
		return nil
	}
	delete(expect, "generated")
	if !f.IsValid() {
		return []string{"no conversion " + st.Call}
	}
	t := f.Type()
	if len(st.Args) != t.NumIn() {
		return []string{fmt.Sprintf("%s takes %d arguments, got %d", st.Call, t.NumIn(), len(st.Args))}
	}
	for i, a := range st.Args {
		x, err := r.decode(a, t.In(i))
		if err != nil {
			return []string{fmt.Sprintf("argument %d: %s", i+1, err)}
		}
		args = append(args, x)
	}
	var results []any
	var err error
	for _, o := range f.Call(args) {
		if e, ok := o.Interface().(error); ok || o.Type() == reflect.TypeFor[error]() {
			err = e
			continue
		}
		results = append(results, encode(o.Interface()))
	}
	var result any = results
	if len(results) == 1 {
		result = results[0]
	}
	if err == nil && st.Save != "" {
		r.saved[st.Save] = result
	}
	r.fired = nil
	return r.compare(Step{Op: st.Op, Expect: expect}, result, err)
}

// call calls the step's method and returns its result, normalized, or its
// error, or the problems that kept it from being called.
func (r *runner) call(st Step, storeName, method string) (any, error, []string) {
	h, problems := r.handle(st)
	if problems != nil {
		return nil, nil, problems
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
	if len(out) == 2 {
		return encode(out[0].Interface()), nil, nil
	}
	return nil, nil, nil
}

// handle is the handle a step runs on: through WithScope, given its scope.
func (r *runner) handle(st Step) (reflect.Value, []string) {
	if st.Scope == nil {
		return r.db, nil
	}
	with, ok := member(r.db, "WithScope")
	if !ok {
		return reflect.Value{}, []string{"the handle has no WithScope"}
	}
	scope, err := r.decode(st.Scope, with.Type().In(0))
	if err != nil {
		return reflect.Value{}, []string{"scope: " + err.Error()}
	}
	return with.Call([]reflect.Value{scope})[0], nil
}

// encode turns a result into its JSON form, normalized.
func encode(v any) any {
	var out any
	// The generated shapes always encode.
	raw, _ := json.Marshal(v)
	json.Unmarshal(raw, &out)
	return normalize(out)
}

// read runs a select step and returns its rows, or their count, or its
// error, or the problems that kept it from running.
func (r *runner) read(st Step) (any, error, []string) {
	sel, ok := r.selects[norm(st.Shape)]
	if !ok {
		return nil, nil, []string{"no shape " + st.Shape}
	}
	h, problems := r.handle(st)
	if problems != nil {
		return nil, nil, problems
	}
	store, ok := member(h, st.From)
	if !ok {
		return nil, nil, []string{"no store " + st.From}
	}
	if !store.Type().Implements(sel.Type().In(0)) {
		return nil, nil, []string{fmt.Sprintf("shape %s is not read through %s", st.Shape, st.From)}
	}
	shape, _ := member(r.shapes, st.Shape)
	b := sel.Call([]reflect.Value{store, shape})[0]
	table := reflect.ValueOf(r.tables[st.From])
	for _, f := range st.Filter {
		col, ok := member(table, f.Column)
		if !ok {
			return nil, nil, []string{fmt.Sprintf("no column %s of %s", f.Column, st.From)}
		}
		op, ok := member(col, f.Op)
		if !ok {
			return nil, nil, []string{fmt.Sprintf("column %s has no %s", f.Column, f.Op)}
		}
		v, err := generate(f.Value)
		if err == nil {
			var x reflect.Value
			if x, err = r.decode(v, op.Type().In(0)); err == nil {
				call := op.Call
				if op.Type().IsVariadic() {
					call = op.CallSlice
				}
				b = b.MethodByName("Where").Call(call([]reflect.Value{x}))[0]
				continue
			}
		}
		return nil, nil, []string{fmt.Sprintf("filter %s: %s", f.Column, err)}
	}
	for _, k := range st.Order {
		col, ok := member(table, k.Column)
		if !ok {
			return nil, nil, []string{fmt.Sprintf("no column %s of %s", k.Column, st.From)}
		}
		dir := "Asc"
		if k.Desc {
			dir = "Desc"
		}
		b = b.MethodByName("OrderBy").Call(col.MethodByName(dir).Call(nil))[0]
	}
	switch st.Deleted {
	case "with", "only":
		m := b.MethodByName(map[string]string{"with": "WithDeleted", "only": "OnlyDeleted"}[st.Deleted])
		if !m.IsValid() {
			return nil, nil, []string{"the schema has no soft delete"}
		}
		b = m.Call(nil)[0]
	case "":
	default:
		return nil, nil, []string{"no deleted " + st.Deleted}
	}
	if st.Distinct {
		b = b.MethodByName("Distinct").Call(nil)[0]
	}
	if st.Limit > 0 {
		b = b.MethodByName("Limit").Call([]reflect.Value{reflect.ValueOf(st.Limit)})[0]
	}
	if st.Consistency != "" {
		c, err := consistency(st)
		if err != nil {
			return nil, nil, []string{err.Error()}
		}
		b = b.MethodByName("Consistency").Call([]reflect.Value{reflect.ValueOf(c)})[0]
	}
	ctx := reflect.ValueOf(r.ctx)
	switch st.Read {
	case "", "all", "count", "one":
		m := map[string]string{"": "All", "all": "All", "count": "Count", "one": "One"}[st.Read]
		out := b.MethodByName(m).Call([]reflect.Value{ctx})
		if e, _ := out[1].Interface().(error); e != nil {
			return nil, e, nil
		}
		return encode(out[0].Interface()), nil, nil
	case "iter":
		seq := b.MethodByName("Iter").Call([]reflect.Value{ctx})[0]
		rows := []any{}
		var err error
		yield := reflect.MakeFunc(seq.Type().In(0), func(in []reflect.Value) []reflect.Value {
			err, _ = in[1].Interface().(error)
			if err == nil {
				rows = append(rows, in[0].Interface())
			}
			return []reflect.Value{reflect.ValueOf(err == nil && len(rows) != st.Stop)}
		})
		seq.Call([]reflect.Value{yield})
		if err != nil {
			return nil, err, nil
		}
		return encode(rows), nil, nil
	}
	return nil, nil, []string{"no read " + st.Read}
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
	mark := r.events.mark()
	h, problems := r.handle(st)
	if problems != nil {
		return problems, nil
	}
	m, ok := member(h, st.Op)
	if !ok && st.Op != "retry" {
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
		if err := r.inject(*st.Inject); err != nil {
			return []string{"inject: " + err.Error()}, nil
		}
		defer r.inject(Inject{})
	}
	runs := 0
	var inner []string
	body := func(ctx context.Context) error {
		runs++
		inner = nil
		outer := r.ctx
		r.ctx = ctx
		path := r.path
		defer func() { r.ctx, r.path = outer, path }()
		var err error
		for i, s := range st.Steps {
			r.path = append(slices.Clip(path), i+1)
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
		return err
	}
	var err error
	if st.Op == "retry" {
		// Retry(policy).Do reruns the steps after the classes On names
		// (§22.4), outside any transaction.
		p := dql.RetryPolicy{Initial: time.Microsecond, Max: time.Microsecond}
		if st.Retry != nil {
			p.Attempts = st.Retry.Attempts
			for _, c := range st.Retry.On {
				p.On = append(p.On, dql.Class(c))
			}
		}
		err = dql.Retry(p).Do(r.ctx, body)
	} else {
		fn := reflect.MakeFunc(m.Type().In(1), func(in []reflect.Value) []reflect.Value {
			out := reflect.New(reflect.TypeFor[error]()).Elem()
			if st.Scope != nil {
				// A scoped handle's transaction runs its steps, so they
				// show the scope it inherits (§22.3).
				db := r.db
				r.db = in[1]
				defer func() { r.db = db }()
			}
			if err := body(in[0].Interface().(context.Context)); err != nil {
				out.Set(reflect.ValueOf(err))
			}
			return []reflect.Value{out}
		})
		out := m.Call(append([]reflect.Value{reflect.ValueOf(r.ctx), fn}, opts...))
		err, _ = out[0].Interface().(error)
	}
	if errors.Is(err, errRollback) {
		err = nil
	}
	problems = inner
	expect := maps.Clone(st.Expect)
	if w, ok := expect["attempts"]; ok {
		if normalize(w) != float64(runs) {
			problems = append(problems, fmt.Sprintf("attempts: want %v, got %d", w, runs))
		}
		delete(expect, "attempts")
	}
	r.fired = r.events.since(mark)
	return append(problems, r.compare(Step{Op: st.Op, Expect: expect}, nil, err)...), err
}

// args builds the method's arguments after its context.
func (r *runner) args(st Step, storeName string, t reflect.Type) ([]reflect.Value, error) {
	args := []reflect.Value{reflect.ValueOf(r.ctx)}
	input, err := generate(st.Input)
	if err != nil {
		return nil, err
	}
	payloads := append(append([]any{input}, st.Bind...), st.Set)
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

// consistency builds a select step's consistency level.
func consistency(st Step) (dql.Consistency, error) {
	var bound, wait time.Duration
	var err error
	for _, d := range []struct {
		name string
		text string
		to   *time.Duration
	}{{"bound", st.Bound, &bound}, {"wait", st.Wait, &wait}} {
		if d.text != "" {
			if *d.to, err = time.ParseDuration(d.text); err != nil {
				return dql.Consistency{}, fmt.Errorf("%s: %w", d.name, err)
			}
		}
	}
	switch st.Consistency {
	case "primary":
		return dql.Primary, nil
	case "any":
		return dql.Any, nil
	case "bounded":
		return dql.Bounded(bound), nil
	case "read_your_writes":
		c := dql.ReadYourWrites.Wait(wait)
		if st.OrFail {
			c = c.OrFail()
		}
		return c, nil
	}
	return dql.Consistency{}, fmt.Errorf("no consistency %s", st.Consistency)
}

// generate expands an input of the form { generate: { count: n, row: r } }
// into n copies of r, with each "$i" in r's strings replaced by the copy's
// number, from 1; value in place of row names a list's item. Any other input
// is returned as it is.
func generate(input any) (any, error) {
	m, _ := input.(map[string]any)
	g, ok := m["generate"].(map[string]any)
	if !ok || len(m) != 1 {
		return input, nil
	}
	n, ok := g["count"].(int)
	if !ok {
		return nil, fmt.Errorf("generate: count %v is not a number", g["count"])
	}
	item, ok := g["value"]
	if !ok {
		item = g["row"]
	}
	rows := make([]any, n)
	for i := range rows {
		rows[i] = numbered(item, strconv.Itoa(i+1))
	}
	return rows, nil
}

// numbered returns v with "$i" replaced by i in its strings.
func numbered(v any, i string) any {
	switch x := v.(type) {
	case string:
		return strings.ReplaceAll(x, "$i", i)
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = numbered(e, i)
		}
		return out
	}
	return v
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
	if xs, ok := v.([]any); ok && t.Kind() == reflect.Slice && r.fill[t.Elem().Name()] != nil {
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
	if fills := r.fill[t.Name()]; fills != nil {
		if m, ok := v.(map[string]any); ok {
			m = maps.Clone(m)
			for field, f := range fills {
				if _, present := m[field]; !present {
					m[field] = f()
				}
			}
			v = m
		}
	}
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

// resolve replaces each "$name.field" string with that saved field, and
// each "$name" with the whole saved result.
func (r *runner) resolve(v any) (any, error) {
	switch x := v.(type) {
	case string:
		ref, ok := strings.CutPrefix(x, "$")
		if !ok {
			return x, nil
		}
		name, field, dotted := strings.Cut(ref, ".")
		if v, ok := r.saved[name]; ok && !dotted {
			return v, nil
		}
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
// none (no row), value, results (a convert step's several results, each
// matched as a field is), count (how many rows), statements (how many queries
// the step ran), events (the hook events it fired, in order), and error
// (class, constraint, kind, field, misuse, for the runtime's errors of
// misuse, a decode error's shape, reason and assertion, and hides, a value
// the error's text must not show). It records the step's outcome.
func (r *runner) compare(st Step, result any, err error) []string {
	if r.record != nil {
		o := Outcome{Step: joinPath(r.path), Op: st.Op, Events: r.fired}
		if err != nil {
			o.Error = errorFields(err)
		} else {
			o.Result = result
		}
		r.record(o)
	}
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
		case "results":
			ws, _ := w.([]any)
			gs, _ := result.([]any)
			if len(ws) != len(gs) {
				problems = append(problems, fmt.Sprintf("want %d results, got %d", len(ws), len(gs)))
				continue
			}
			for i := range ws {
				problems = append(problems, r.match(fmt.Sprintf("result %d", i+1), ws[i], gs[i])...)
			}
		case "none":
			if (result == nil) != (w == true) {
				problems = append(problems, fmt.Sprintf("want none %v, got %v", w, result))
			}
		case "value":
			if !reflect.DeepEqual(w, result) {
				problems = append(problems, fmt.Sprintf("want value %v, got %v", w, result))
			}
		case "count":
			if gs, _ := result.([]any); w != float64(len(gs)) {
				problems = append(problems, fmt.Sprintf("want count %v, got %d", w, len(gs)))
			}
		case "statements":
			n := 0
			for _, ev := range r.fired {
				if _, ok := ev.(map[string]any)["query"]; ok {
					n++
				}
			}
			if w != float64(n) {
				problems = append(problems, fmt.Sprintf("want %v statements, got %d", w, n))
			}
		case "events":
			problems = append(problems, r.compareEvents(w)...)
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

// compareEvents compares the events the step fired with want, in order, each on
// the fields want names.
func (r *runner) compareEvents(want any) []string {
	ws, _ := want.([]any)
	if len(ws) != len(r.fired) {
		return []string{fmt.Sprintf("want %d events, got %d: %v", len(ws), len(r.fired), r.fired)}
	}
	var problems []string
	for i := range ws {
		problems = append(problems, r.compareRow(fmt.Sprintf("event %d ", i+1), ws[i], r.fired[i])...)
	}
	return problems
}

// misuses are the runtime's errors of misuse, which have no class, by the
// name an error expectation's misuse gives them.
var misuses = map[string]error{
	"NoTransaction": dql.ErrNoTransaction, "InTransaction": dql.ErrInTransaction,
	"ReadOnly": dql.ErrReadOnly, "IndependentDepth": dql.ErrIndependentDepth,
	"UnknownSource": dql.ErrUnknownSource,
}

// errorFields are the fields of an error that expectations match: its
// class, the misuse, the violated constraint, the first validation issue's
// kind and field, and a decode error's shape, field, reason and assertion.
func errorFields(err error) map[string]any {
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
	var de *dql.DecodeError
	if errors.As(err, &de) {
		got["shape"], got["field"], got["reason"], got["assertion"] = de.Shape, de.Field, string(de.Reason), de.Assertion
	}
	return got
}

// joinPath spells a step's place, as 3.1.
func joinPath(path []int) string {
	parts := make([]string, len(path))
	for i, n := range path {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ".")
}

func (r *runner) compareError(want any, err error) []string {
	got := errorFields(err)
	w, ok := want.(map[string]any)
	if !ok {
		return []string{fmt.Sprintf("error expectation %v is not a map", want)}
	}
	var problems []string
	for _, k := range slices.Sorted(maps.Keys(w)) {
		if k == "hides" {
			if h := fmt.Sprint(w[k]); strings.Contains(err.Error(), h) {
				problems = append(problems, fmt.Sprintf("error shows %s: %v", h, err))
			}
			continue
		}
		if g, ok := got[k]; !ok || g != w[k] {
			problems = append(problems, fmt.Sprintf("error %s: want %v, got %v (%v)", k, w[k], g, err))
		}
	}
	return problems
}
