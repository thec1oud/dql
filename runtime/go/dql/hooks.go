package dql

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"
)

// Hooks observe what queries, transactions, pools and replicas do (§24).
// Every field is optional. A start hook returns the context the work runs
// in, such as one carrying a tracing span, which its end hook receives.
//
// Events name queries rather than carry values: a query's SQL is
// assembled from fragments and holds none, and its parameters are left
// out unless Params is set, and then a sensitive one is Redacted.
type Hooks struct {
	// Params includes parameter values in query events.
	Params bool

	QueryStart func(context.Context, QueryEvent) context.Context
	QueryEnd   func(context.Context, QueryEvent)
	// TxBegin and TxEnd see each transaction a closure or pair begins, and
	// each savepoint of a Nested closure; a joined closure begins none.
	TxBegin func(context.Context, TxEvent) context.Context
	TxEnd   func(context.Context, TxEvent)
	// TxRetry sees a closure's root transaction about to rerun.
	TxRetry func(context.Context, TxEvent)
	// TxLeaked sees a pair-form transaction still open when its operation
	// ended, which the executor then rolls back (§22.3).
	TxLeaked func(context.Context, TxEvent)
	PoolWait func(context.Context, PoolWaitEvent)
	Rotation func(RotationEvent)
}

// WithHooks adds hooks; each event reaches every set of hooks in the order
// they were added.
func WithHooks(h Hooks) Option {
	return func(e *Executor) { e.hooks = append(e.hooks, h) }
}

// QueryEvent is a query's start or end (§24).
type QueryEvent struct {
	Query string
	// Such as select, insert or get, as generated code names it.
	Kind    string
	Catalog string
	Source  string
	// The query ran on a replica. Set at the end.
	Replica     bool
	Consistency Consistency
	// The run of its root transaction's closure, from 1.
	Attempt int
	SQL     string
	// The arguments, when the hooks ask for them.
	Params []any
	// Set at the end.
	Duration time.Duration
	Rows     int64
	Err      error
	Class    Class
}

// Redacted stands in an event for the value of a sensitive parameter.
var Redacted = redacted{}

type redacted struct{}

func (redacted) String() string { return "[redacted]" }

// TxOutcome is how a transaction ended.
type TxOutcome string

// The outcomes.
const (
	Committed  TxOutcome = "commit"
	RolledBack TxOutcome = "rollback"
)

// TxEvent is a transaction's begin, end, retry or leak (§24).
type TxEvent struct {
	Propagation Propagation
	Isolation   Isolation
	ReadOnly    bool
	// The transaction is a savepoint of a Nested closure.
	Savepoint bool
	Attempt   int
	// Set at the end, the leak and the retry.
	Duration time.Duration
	Outcome  TxOutcome
	// The error that ended the transaction or attempt.
	Err error
	// Set at a retry: the backoff before the next attempt.
	Delay time.Duration
}

// PoolWaitEvent is the time a statement or transaction waited for a
// connection (§24).
type PoolWaitEvent struct {
	Source  string
	Replica bool
	Wait    time.Duration
}

// RotationEvent is a replica entering or leaving rotation (§24).
type RotationEvent struct {
	Source string
	// The replica's position in the source's configuration, from 0; a DSN
	// may hold a password, so events do not carry it.
	Replica int
	Lag     time.Duration
	In      bool
}

// String names a consistency level: primary, any, bounded(2s) or
// read-your-writes, with any wait or fail.
func (c Consistency) String() string {
	s := [...]string{"unset", "primary", "any", "bounded", "read-your-writes"}[c.level]
	if c.level == bounded {
		s += "(" + c.bound.String() + ")"
	}
	if c.wait > 0 {
		s += ".wait(" + c.wait.String() + ")"
	}
	if c.orFail {
		s += ".or-fail"
	}
	return s
}

// String names a propagation mode.
func (p Propagation) String() string {
	if p < Join || p > Never {
		return "Propagation(" + strconv.Itoa(int(p)) + ")"
	}
	return [...]string{"join", "independent", "nested", "mandatory", "never"}[p]
}

// queryRun is one query between its start and end events.
type queryRun struct {
	e     *Executor
	ctx   context.Context
	ev    QueryEvent
	start time.Time
	off   bool
	// The redacted arguments, for hooks that ask for them.
	params []any
}

// startQuery fires the query start hooks, unless the statement opts out.
func (e *Executor) startQuery(ctx context.Context, f *frame, s *Statement) (context.Context, *queryRun) {
	q := &queryRun{e: e, start: time.Now(), off: s.NoLog || len(e.hooks) == 0}
	if q.off {
		return ctx, q
	}
	q.ev = QueryEvent{Query: s.Query, Kind: s.Kind, Catalog: s.Catalog, Consistency: s.Consistency, Attempt: 1, SQL: s.SQL}
	if src := e.catalogs[s.Catalog]; src != nil {
		q.ev.Source = src.name
	}
	if q.ev.Consistency.level == unset {
		q.ev.Consistency = e.consistency
	}
	if f != nil {
		q.ev.Attempt = f.root.attempt
		if f.root.opts.ReadOnly && f.root.consistency.level != unset {
			q.ev.Consistency = f.root.consistency
		}
	}
	if s.Writes || f != nil && !f.root.opts.ReadOnly {
		q.ev.Consistency = Primary
	}
	for _, h := range e.hooks {
		if h.Params {
			q.params = params(s)
		}
	}
	for _, h := range e.hooks {
		if h.QueryStart != nil {
			ctx = h.QueryStart(ctx, q.withParams(h))
		}
	}
	q.ctx = ctx
	return ctx, q
}

// params returns s's arguments with the sensitive ones redacted.
func params(s *Statement) []any {
	out := append([]any(nil), s.Args...)
	for i := range out {
		if i < len(s.Sensitive) && s.Sensitive[i] {
			out[i] = Redacted
		}
	}
	return out
}

// end fires the query end hooks.
func (q *queryRun) end(replica bool, rows int64, err error) {
	if q.off {
		return
	}
	q.ev.Replica, q.ev.Rows, q.ev.Err, q.ev.Class = replica, rows, err, ClassOf(err)
	q.ev.Duration = time.Since(q.start)
	for _, h := range q.e.hooks {
		if h.QueryEnd != nil {
			h.QueryEnd(q.ctx, q.withParams(h))
		}
	}
}

// withParams returns the event, with the parameters when h asks for them.
func (q *queryRun) withParams(h Hooks) QueryEvent {
	ev := q.ev
	if h.Params {
		ev.Params = q.params
	}
	return ev
}

// txBegin fires the transaction begin hooks.
func (e *Executor) txBegin(ctx context.Context, ev TxEvent) context.Context {
	for _, h := range e.hooks {
		if h.TxBegin != nil {
			ctx = h.TxBegin(ctx, ev)
		}
	}
	return ctx
}

// txEvent fires one kind of transaction hook.
func (e *Executor) txEvent(ctx context.Context, pick func(Hooks) func(context.Context, TxEvent), ev TxEvent) {
	for _, h := range e.hooks {
		if f := pick(h); f != nil {
			f(ctx, ev)
		}
	}
}

func txEnd(h Hooks) func(context.Context, TxEvent)    { return h.TxEnd }
func txRetry(h Hooks) func(context.Context, TxEvent)  { return h.TxRetry }
func txLeaked(h Hooks) func(context.Context, TxEvent) { return h.TxLeaked }

// acquire takes a connection from a pool, reporting the wait.
func (e *Executor) acquire(ctx context.Context, s *sourcePool, p Pool, replica bool) (Conn, error) {
	start := time.Now()
	c, err := p.Acquire(ctx)
	if len(e.hooks) > 0 {
		ev := PoolWaitEvent{Source: s.name, Replica: replica, Wait: time.Since(start)}
		for _, h := range e.hooks {
			if h.PoolWait != nil {
				h.PoolWait(ctx, ev)
			}
		}
	}
	return c, err
}

// rotate puts a replica in or out of rotation, reporting a change.
func (e *Executor) rotate(s *sourcePool, i int, in bool) {
	r := s.replicas[i]
	if r.in.Swap(in) == in {
		return
	}
	ev := RotationEvent{Source: s.name, Replica: i, Lag: time.Duration(r.lag.Load()), In: in}
	for _, h := range e.hooks {
		if h.Rotation != nil {
			h.Rotation(ev)
		}
	}
}

// ErrLeaked is the cause of the rollback of a pair-form transaction still
// open when its operation ended.
var ErrLeaked = errors.New("dql: a transaction was still open when its operation ended")

// operation is the pair-form root transactions begun under one Operation.
type operation struct {
	mu    sync.Mutex
	roots []*root
}

type operationKey struct{}

// Operation returns ctx scoped to one operation, such as a request, and the
// function that ends it: each pair-form transaction begun in it and still
// open is rolled back and reported to the TxLeaked hooks (§22.3).
func (e *Executor) Operation(ctx context.Context) (context.Context, func()) {
	op := &operation{}
	return context.WithValue(ctx, operationKey{}, op), func() {
		op.mu.Lock()
		roots := op.roots
		op.roots = nil
		op.mu.Unlock()
		for _, r := range roots {
			if r.done {
				continue
			}
			ev := r.event()
			ev.Duration = time.Since(r.started)
			e.txEvent(r.hctx, txLeaked, ev)
			r.end(ctx, false, ErrLeaked)
		}
	}
}

// track adds a pair-form root to the operation ctx carries, if any.
func track(ctx context.Context, r *root) {
	if op, _ := ctx.Value(operationKey{}).(*operation); op != nil {
		op.mu.Lock()
		op.roots = append(op.roots, r)
		op.mu.Unlock()
	}
}
