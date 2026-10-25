package dql

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type hookKey struct{}

// recorder records the events of its hooks as lines, and marks the
// context its start hooks return so that end hooks can check they get it.
type recorder struct {
	mu    sync.Mutex
	lines []string
}

func (r *recorder) add(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, fmt.Sprintf(format, args...))
}

// take returns the lines since the last take.
func (r *recorder) take() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := strings.Join(r.lines, "\n")
	r.lines = nil
	return out
}

func marked(ctx context.Context) string {
	m, _ := ctx.Value(hookKey{}).(string)
	return m
}

func (r *recorder) hooks(params bool) Hooks {
	return Hooks{
		Params: params,
		QueryStart: func(ctx context.Context, q QueryEvent) context.Context {
			r.add("start %s %s %s/%s %s attempt %d sql %q params %v", q.Query, q.Kind, q.Catalog, q.Source, q.Consistency, q.Attempt, q.SQL, q.Params)
			return context.WithValue(ctx, hookKey{}, marked(ctx)+"q")
		},
		QueryEnd: func(ctx context.Context, q QueryEvent) {
			r.add("end %s replica %v rows %d class %s err %v params %v ctx %s", q.Query, q.Replica, q.Rows, q.Class, q.Err != nil, q.Params, marked(ctx))
		},
		TxBegin: func(ctx context.Context, t TxEvent) context.Context {
			r.add("begin %s %q read-only %v savepoint %v attempt %d", t.Propagation, t.Isolation, t.ReadOnly, t.Savepoint, t.Attempt)
			return context.WithValue(ctx, hookKey{}, marked(ctx)+"t")
		},
		TxEnd: func(ctx context.Context, t TxEvent) {
			r.add("end tx %s savepoint %v attempt %d err %v ctx %s", t.Outcome, t.Savepoint, t.Attempt, t.Err, marked(ctx))
		},
		TxRetry: func(ctx context.Context, t TxEvent) {
			r.add("retry attempt %d delay %v class %s", t.Attempt, t.Delay, ClassOf(t.Err))
		},
		TxLeaked: func(ctx context.Context, t TxEvent) {
			r.add("leaked %s read-only %v", t.Propagation, t.ReadOnly)
		},
		PoolWait: func(ctx context.Context, p PoolWaitEvent) {
			r.add("wait %s replica %v", p.Source, p.Replica)
		},
		Rotation: func(ev RotationEvent) {
			r.add("rotation %s/%d in %v lag %v", ev.Source, ev.Replica, ev.In, ev.Lag)
		},
	}
}

func TestQueryHooks(t *testing.T) {
	ctx := context.Background()
	rec, plain := &recorder{}, &recorder{}
	d := &fakeDriver{}
	e := newExec(t, d, WithHooks(rec.hooks(true)), WithHooks(Hooks{QueryEnd: plain.hooks(false).QueryEnd}))

	s := stmt("SELECT $1, $2")
	s.Kind, s.Args, s.Sensitive = "select", []any{"a@b.c", int64(7)}, []bool{true}
	if _, err := Fetch(ctx, e, s); err != nil {
		t.Fatal(err)
	}
	want := `start q select shop/main primary attempt 1 sql "SELECT $1, $2" params [[redacted] 7]
wait main replica false
end q replica false rows 1 class  err false params [[redacted] 7] ctx q`
	if got := rec.take(); got != want {
		t.Errorf("events\n got %s\nwant %s", got, want)
	}
	// Hooks that do not ask for parameters get none.
	if got := plain.take(); got != "end q replica false rows 1 class  err false params [] ctx q" {
		t.Error(got)
	}

	// An error's class; a call that opts out; a write's level.
	if _, err := Exec(ctx, e, stmt("FAIL 23505")); err == nil {
		t.Fatal("no error")
	}
	s.NoLog = true
	Fetch(ctx, e, s)
	if got := rec.take(); !strings.HasSuffix(got, "end q replica false rows 0 class UniqueViolation err true params [] ctx q\nwait main replica false") ||
		!strings.Contains(got, "start q  shop/main primary") {
		t.Errorf("events %s", got)
	}

	// A read transaction's level.
	e.Read(ctx, func(ctx context.Context, tx *ReadTx) error {
		_, err := Fetch(ctx, tx, stmt("SELECT"))
		return err
	}, WithConsistency(Any))
	if got := rec.take(); !strings.Contains(got, "start q  shop/main any attempt 1") {
		t.Error(got)
	}

	// A statement on an unknown catalog names no source.
	if _, err := Fetch(ctx, e, Statement{Query: "q", Catalog: "nowhere"}); err == nil {
		t.Fatal("no error")
	}
	if got := rec.take(); !strings.HasPrefix(got, "start q  nowhere/ primary") {
		t.Error(got)
	}
}

func TestTxHooks(t *testing.T) {
	ctx := context.Background()
	rec := &recorder{}
	d := &fakeDriver{}
	e := newExec(t, d, WithHooks(rec.hooks(false)))
	query := func(ctx context.Context, h Reader) {
		t.Helper()
		if _, err := Fetch(ctx, h, stmt("SELECT")); err != nil {
			t.Fatal(err)
		}
	}

	// A closure's root, with a nested savepoint and a joined closure,
	// which begins nothing; the statements run in the hooks' contexts.
	e.Write(ctx, func(ctx context.Context, tx *WriteTx) error {
		query(ctx, tx)
		e.Write(ctx, func(ctx context.Context, tx *WriteTx) error {
			query(ctx, tx)
			return errors.New("undo")
		}, WithPropagation(Nested))
		return e.Write(ctx, func(ctx context.Context, tx *WriteTx) error { return nil })
	}, WithIsolation(Serializable))
	want := `begin join "serializable" read-only false savepoint false attempt 1
start q  shop/main primary attempt 1 sql "SELECT" params []
wait main replica false
end q replica false rows 1 class  err false params [] ctx tq
begin nested "serializable" read-only false savepoint true attempt 1
start q  shop/main primary attempt 1 sql "SELECT" params []
end q replica false rows 1 class  err false params [] ctx ttq
end tx rollback savepoint true attempt 1 err undo ctx tt
end tx commit savepoint false attempt 1 err <nil> ctx t`
	got := rec.take()
	if got != want {
		t.Errorf("events\n got %s\nwant %s", got, want)
	}

	// Each attempt of a rerun, the retry between them, and the attempt
	// number of the statements in each.
	runs := 0
	e.Write(ctx, func(ctx context.Context, tx *WriteTx) error {
		if runs++; runs == 1 {
			_, err := Exec(ctx, tx, stmt("FAIL 40001"))
			return err
		}
		query(ctx, tx)
		return nil
	})
	got = rec.take()
	for _, line := range []string{
		`begin join "" read-only false savepoint false attempt 1`,
		"end tx rollback savepoint false attempt 1 err dql: SerializationFailure in q (postgres 40001) ctx t",
		"retry attempt 1 delay 0s class SerializationFailure",
		`begin join "" read-only false savepoint false attempt 2`,
		"start q  shop/main primary attempt 2",
		"end tx commit savepoint false attempt 2 err <nil> ctx t",
	} {
		if !strings.Contains(got, line) {
			t.Errorf("no %q in\n%s", line, got)
		}
	}

	// A panic rolls back with its cause; a pair commits.
	func() {
		defer func() { recover() }()
		e.Read(ctx, func(ctx context.Context, tx *ReadTx) error {
			query(ctx, tx)
			panic("boom")
		})
	}()
	tx, _ := e.BeginWrite(ctx, WithPropagation(Independent))
	query(ctx, tx)
	tx.Commit(ctx)
	got = rec.take()
	for _, line := range []string{
		"end tx rollback savepoint false attempt 1 err " + errPanicked.Error(),
		`begin independent "" read-only false savepoint false attempt 1`,
		"end tx commit savepoint false attempt 1 err <nil>",
	} {
		if !strings.Contains(got, line) {
			t.Errorf("no %q in\n%s", line, got)
		}
	}

	// A failed commit ends with its error.
	d.fail = map[string]error{"commit": errBroken}
	e.Write(ctx, func(ctx context.Context, tx *WriteTx) error { query(ctx, tx); return nil })
	d.fail = nil
	if got := rec.take(); !strings.Contains(got, "end tx rollback savepoint false attempt 1 err dql: ConnectionError") {
		t.Errorf("failed commit: %s", got)
	}
}

func TestLeaks(t *testing.T) {
	rec := &recorder{}
	d := &fakeDriver{}
	e := newExec(t, d, WithHooks(rec.hooks(false)))
	ctx, end := e.Operation(context.Background())
	leaked, _ := e.BeginRead(ctx)
	Fetch(ctx, leaked, stmt("SELECT"))
	done, _ := e.BeginWrite(ctx)
	done.Rollback(ctx)
	joined, _ := e.BeginWrite(leaked.Context(ctx), WithPropagation(Mandatory))
	_ = joined
	untracked, _ := e.BeginWrite(context.Background())
	rec.take()
	d.calls()
	end()
	if got := rec.take(); got != "leaked join read-only true\nend tx rollback savepoint false attempt 1 err "+ErrLeaked.Error()+" ctx t" {
		t.Errorf("events %s", got)
	}
	expect(t, d, "pg#2 rollback; pg#2 put back")
	if !leaked.f.root.done || untracked.f.root.done {
		t.Error("wrong roots ended")
	}
	end()
	if rec.take() != "" {
		t.Error("ended twice")
	}
	// Without hooks, a leak still rolls back.
	e = newExec(t, &fakeDriver{})
	ctx, end = e.Operation(context.Background())
	tx, _ := e.BeginWrite(ctx)
	end()
	if !tx.f.root.done {
		t.Error("not rolled back")
	}
}

func TestRotationHooks(t *testing.T) {
	rec := &recorder{}
	c := &cluster{lag: map[string]any{"r1": 0.1, "r2": 5.0, "r3": nil}}
	d, e := newCluster(t, c, Config{}, WithHooks(rec.hooks(false)))
	if got := rec.take(); got != "rotation main/0 in true lag 100ms" {
		t.Errorf("startup %q", got)
	}
	c.lag["r2"] = 0.0
	e.poll(context.Background())
	d.fail = map[string]error{"acquire r1": errBroken, "acquire r2": errBroken}
	s := stmt("SELECT")
	s.Consistency = Bounded(time.Second)
	One(context.Background(), e, s)
	got := rec.take()
	for _, line := range []string{"rotation main/1 in true lag 0s", "rotation main/1 in false lag 0s", "wait main replica true", "rotation main/0 in false lag 100ms", "wait main replica false"} {
		if !strings.Contains(got, line) {
			t.Errorf("no %q in\n%s", line, got)
		}
	}
}

func TestNames(t *testing.T) {
	for c, want := range map[Consistency]string{
		{}: "unset", Primary: "primary", Any: "any", Bounded(2 * time.Second): "bounded(2s)",
		ReadYourWrites.Wait(time.Second).OrFail(): "read-your-writes.wait(1s).or-fail",
	} {
		if got := c.String(); got != want {
			t.Errorf("%s, want %s", got, want)
		}
	}
	if Never.String() != "never" || Propagation(9).String() != "Propagation(9)" || Propagation(-1).String() != "Propagation(-1)" {
		t.Error("propagation names")
	}
	if fmt.Sprint(Redacted) != "[redacted]" {
		t.Error("redacted")
	}
}

func TestRetryCanceled(t *testing.T) {
	e := newExec(t, &fakeDriver{}, withRand(func() float64 { return 0.99 }))
	ctx, cancel := context.WithCancel(context.Background())
	runs := 0
	err := e.Write(ctx, func(ctx context.Context, tx *WriteTx) error {
		runs++
		time.AfterFunc(10*time.Millisecond, cancel)
		_, err := Exec(ctx, tx, stmt("FAIL 40001"))
		return err
	}, WithRetry(RetryPolicy{Attempts: 3, Initial: time.Hour, Max: time.Hour}))
	if runs != 1 || ClassOf(err) != ClassSerializationFailure {
		t.Errorf("%d runs: %v", runs, err)
	}
}
