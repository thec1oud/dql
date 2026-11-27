package dql

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

// Propagation is how a new transaction relates to the one its context
// carries (§22.3).
type Propagation int

// The propagation modes.
const (
	// Join reuses the enclosing transaction, or begins one. The default.
	Join Propagation = iota
	// Independent begins a separate transaction on its own connection.
	Independent
	// Nested begins a savepoint in the enclosing transaction, or a new
	// transaction.
	Nested
	// Mandatory joins the enclosing transaction, and fails without one.
	Mandatory
	// Never begins a new transaction, and fails inside one.
	Never
)

// TxOption configures one transaction.
type TxOption func(*txConfig)

type txConfig struct {
	propagation Propagation
	isolation   Isolation
	retry       *RetryPolicy
	consistency Consistency
}

// WithPropagation sets how the transaction relates to an enclosing one.
func WithPropagation(p Propagation) TxOption { return func(c *txConfig) { c.propagation = p } }

// WithIsolation sets the isolation level of a transaction it begins.
func WithIsolation(i Isolation) TxOption { return func(c *txConfig) { c.isolation = i } }

// WithConsistency sets how fresh a read transaction it begins must be,
// which decides whether it may run on a replica (§22.5).
func WithConsistency(c Consistency) TxOption { return func(t *txConfig) { t.consistency = c } }

// WithRetry overrides the executor's retry policy for one closure (§22.4).
func WithRetry(p RetryPolicy) TxOption { return func(c *txConfig) { c.retry = &p } }

// root is a transaction that owns its connections: one per source it has
// used, each begun on first use, so a transaction spans the sources its
// statements reach.
type root struct {
	exec        *Executor
	opts        TxOptions
	consistency Consistency
	// Records the positions of the root's commits.
	session *Session
	conns   map[*sourcePool]Conn
	order   []*sourcePool
	// The sources whose connection is a replica's.
	replicas    map[*sourcePool]bool
	independent int // how deep in independent transactions
	done        bool
	// For hooks: how the root began, its closure's run, when it began, and
	// the context its begin hooks returned.
	propagation Propagation
	attempt     int
	started     time.Time
	hctx        context.Context
}

// frame is one level of a transaction: its root, or a savepoint in it.
type frame struct {
	root      *root
	parent    *frame
	savepoint string
	// The sources the savepoint was made on.
	saved map[*sourcePool]bool
	// The context the frame's closure runs in, from the begin hooks.
	hctx context.Context
}

type frameKey struct{}

func frameOf(ctx context.Context) *frame {
	f, _ := ctx.Value(frameKey{}).(*frame)
	return f
}

func (f *frame) with(ctx context.Context) context.Context {
	return context.WithValue(ctx, frameKey{}, f)
}

// conn returns the frame's connection to s, and whether it is a
// replica's, beginning the root's transaction on s and making the frame's
// savepoints there on first use.
func (f *frame) conn(ctx context.Context, s *sourcePool, q string) (Conn, bool, error) {
	r := f.root
	if r.done {
		return nil, false, ErrDone
	}
	c := r.conns[s]
	if c == nil {
		var err error
		var replica bool
		if r.opts.ReadOnly {
			c, replica, err = r.exec.route(ctx, s, r.consistency, r.independent > 0, q)
		} else {
			c, err = r.exec.conn(ctx, s, r.independent > 0, q)
		}
		if err != nil {
			return nil, false, err
		}
		if replica {
			r.replicas[s] = true
		}
		if err := c.Begin(ctx, r.opts); err != nil {
			c.Release()
			return nil, false, r.exec.translate(err, "", q, s)
		}
		r.conns[s] = c
		r.order = append(r.order, s)
	}
	// Savepoints are made outermost first.
	var chain []*frame
	for g := f; g.savepoint != ""; g = g.parent {
		chain = append([]*frame{g}, chain...)
	}
	for _, g := range chain {
		if !g.saved[s] {
			if err := c.Savepoint(ctx, g.savepoint); err != nil {
				return nil, false, r.exec.translate(err, "", q, s)
			}
			g.saved[s] = true
		}
	}
	return c, r.replicas[s], nil
}

// end commits or rolls back every connection of the root and releases
// them. Commit stops at the first failure and rolls back the rest. cause is
// why a rollback happens, for the end hooks.
func (r *root) end(ctx context.Context, commit bool, cause error) error {
	if r.done {
		return nil
	}
	r.done = true
	ctx = context.WithoutCancel(ctx)
	var first error
	for _, s := range r.order {
		c := r.conns[s]
		var err error
		if commit && first == nil {
			if err = c.Commit(ctx); err == nil && !r.opts.ReadOnly {
				r.exec.recordWrite(ctx, r.session, s, c)
			}
		} else {
			err = c.Rollback(ctx)
		}
		if err != nil && first == nil {
			first = r.exec.translate(err, "", "", s)
		}
		c.Release()
	}
	ev := r.event()
	ev.Duration, ev.Outcome, ev.Err = time.Since(r.started), RolledBack, cause
	if commit && first == nil {
		ev.Outcome = Committed
	} else if first != nil {
		ev.Err = first
	}
	r.exec.txEvent(r.hctx, txEnd, ev)
	return first
}

// endSavepoint releases a savepoint, or rolls back to it, on every source
// it was made on.
func (f *frame) endSavepoint(ctx context.Context, commit bool) error {
	ctx = context.WithoutCancel(ctx)
	for _, s := range f.root.order {
		if !f.saved[s] {
			continue
		}
		c := f.root.conns[s]
		var err error
		if !commit {
			err = c.RollbackTo(ctx, f.savepoint)
		}
		if err == nil {
			err = c.ReleaseSavepoint(ctx, f.savepoint)
		}
		if err != nil {
			return f.root.exec.translate(err, "", "", s)
		}
	}
	return nil
}

// WriteTx is a write transaction: a handle that reads and writes (§22.2).
type WriteTx struct{ txHandle }

// ReadTx is a read transaction: a handle that only reads (§22.2).
type ReadTx struct{ txHandle }

type txHandle struct {
	f *frame
	// Set on a handle of the pair form, which ends its own root.
	owns bool
}

// Commit commits a transaction of the pair form, or releases its
// savepoint. A joined transaction's Commit does nothing: its root commits.
func (t *txHandle) Commit(ctx context.Context) error {
	switch {
	case !t.owns:
		return nil
	case t.f.root.done:
		return ErrDone
	}
	return t.f.root.end(ctx, true, nil)
}

// Rollback rolls back a transaction of the pair form. After Commit it does
// nothing, so it can be deferred.
func (t *txHandle) Rollback(ctx context.Context) error {
	if !t.owns {
		return nil
	}
	return t.f.root.end(ctx, false, nil)
}

// Write runs fn in a write transaction (§22.3). A transaction fn's context
// carries is joined by default; one it begins commits when fn returns nil
// and rolls back when fn returns an error or panics, and reruns after a
// serialization failure or deadlock, so fn must be safe to rerun.
func (e *Executor) Write(ctx context.Context, fn func(context.Context, *WriteTx) error, opts ...TxOption) error {
	return e.run(ctx, false, func(ctx context.Context, f *frame) error { return fn(ctx, &WriteTx{txHandle{f: f}}) }, opts)
}

// Read runs fn in a read transaction, as Write does. A read transaction
// joins an enclosing write transaction and sees its writes.
func (e *Executor) Read(ctx context.Context, fn func(context.Context, *ReadTx) error, opts ...TxOption) error {
	return e.run(ctx, true, func(ctx context.Context, f *frame) error { return fn(ctx, &ReadTx{txHandle{f: f}}) }, opts)
}

// BeginWrite begins a write transaction of the pair form, which the caller
// commits or rolls back and which never retries (§22.3). Its handle is
// bound to it; a context it should reach takes Context.
func (e *Executor) BeginWrite(ctx context.Context, opts ...TxOption) (*WriteTx, error) {
	f, owns, err := e.begin(ctx, false, opts)
	if err != nil {
		return nil, err
	}
	if owns {
		track(ctx, f.root)
	}
	return &WriteTx{txHandle{f: f, owns: owns}}, nil
}

// BeginRead begins a read transaction of the pair form.
func (e *Executor) BeginRead(ctx context.Context, opts ...TxOption) (*ReadTx, error) {
	f, owns, err := e.begin(ctx, true, opts)
	if err != nil {
		return nil, err
	}
	if owns {
		track(ctx, f.root)
	}
	return &ReadTx{txHandle{f: f, owns: owns}}, nil
}

// Context returns ctx carrying the transaction, so generated code called
// with the executor and ctx joins it.
func (t *txHandle) Context(ctx context.Context) context.Context { return t.f.with(ctx) }

// begin starts the frame a transaction runs in, and reports whether the
// frame owns a new root. A pair-form Nested savepoint is not supported: it
// joins the enclosing transaction.
func (e *Executor) begin(ctx context.Context, readOnly bool, opts []TxOption) (*frame, bool, error) {
	var c txConfig
	for _, o := range opts {
		o(&c)
	}
	enclosing := frameOf(ctx)
	switch c.propagation {
	case Mandatory:
		if enclosing == nil {
			return nil, false, ErrNoTransaction
		}
	case Never:
		if enclosing != nil {
			return nil, false, ErrInTransaction
		}
	case Independent:
		depth := 1
		if enclosing != nil {
			depth = enclosing.root.independent + 1
		}
		if depth > e.independentDepth {
			return nil, false, ErrIndependentDepth
		}
		return e.newRoot(ctx, readOnly, c, depth, 1), true, nil
	}
	if enclosing == nil {
		return e.newRoot(ctx, readOnly, c, 0, 1), true, nil
	}
	if !readOnly && enclosing.root.opts.ReadOnly {
		return nil, false, ErrReadOnly
	}
	return enclosing, false, nil
}

// newRoot begins a root transaction, firing the begin hooks.
func (e *Executor) newRoot(ctx context.Context, readOnly bool, c txConfig, independent, attempt int) *frame {
	r := &root{exec: e, opts: TxOptions{ReadOnly: readOnly, Isolation: c.isolation},
		consistency: c.consistency, session: SessionFrom(ctx), conns: map[*sourcePool]Conn{}, replicas: map[*sourcePool]bool{}, independent: independent,
		propagation: c.propagation, attempt: attempt, started: time.Now()}
	r.hctx = e.txBegin(ctx, r.event())
	return &frame{root: r, hctx: r.hctx}
}

// event returns the root's transaction event.
func (r *root) event() TxEvent {
	return TxEvent{Propagation: r.propagation, Isolation: r.opts.Isolation, ReadOnly: r.opts.ReadOnly, Attempt: r.attempt}
}

// savepoints numbers savepoint names; a name need only be unique within
// its root, and the counter makes it unique everywhere.
var savepoints atomic.Int64

// run runs a closure transaction.
func (e *Executor) run(ctx context.Context, readOnly bool, fn func(context.Context, *frame) error, opts []TxOption) error {
	var c txConfig
	for _, o := range opts {
		o(&c)
	}
	enclosing := frameOf(ctx)
	if c.propagation == Nested && enclosing != nil {
		if !readOnly && enclosing.root.opts.ReadOnly {
			return ErrReadOnly
		}
		ev := enclosing.root.event()
		ev.Propagation, ev.ReadOnly, ev.Savepoint = Nested, readOnly, true
		f := &frame{root: enclosing.root, parent: enclosing, savepoint: fmt.Sprintf("dql_%d", savepoints.Add(1)),
			saved: map[*sourcePool]bool{}, hctx: e.txBegin(ctx, ev)}
		start := time.Now()
		err := call(ctx, f, fn)
		if end := f.endSavepoint(ctx, err == nil); err == nil {
			err = end
		}
		ev.Duration, ev.Outcome, ev.Err = time.Since(start), Committed, err
		if err != nil {
			ev.Outcome = RolledBack
		}
		e.txEvent(f.hctx, txEnd, ev)
		return err
	}
	f, owns, err := e.begin(ctx, readOnly, opts)
	if err != nil || !owns {
		if err != nil {
			return err
		}
		return fn(f.with(ctx), f)
	}
	policy := e.retry
	if c.retry != nil {
		policy = *c.retry
	}
	for attempt := 1; ; attempt++ {
		err = call(ctx, f, fn)
		if err == nil {
			err = f.root.end(ctx, true, nil)
		} else {
			f.root.end(ctx, false, err)
		}
		if err == nil || !retryable(err) || attempt >= policy.Attempts {
			return retried(err, attempt)
		}
		delay := policy.backoff(attempt, e.rand)
		if !fits(ctx, delay) {
			return retried(err, attempt)
		}
		ev := f.root.event()
		ev.Duration, ev.Err, ev.Delay = time.Since(f.root.started), err, delay
		e.txEvent(f.root.hctx, txRetry, ev)
		if !wait(ctx, delay) {
			return retried(err, attempt)
		}
		f = e.newRoot(ctx, readOnly, c, f.root.independent, attempt+1)
	}
}

// retried returns the error of a closure after attempt runs.
func retried(err error, attempt int) error {
	if err != nil && attempt > 1 {
		return &Retried{Attempts: attempt, Err: err}
	}
	return err
}

// errPanicked is the cause of a rollback after a panic, for the end hooks.
var errPanicked = errors.New("dql: the transaction's closure panicked")

// call runs fn in f, rolling back f's root and re-panicking on a panic.
func call(ctx context.Context, f *frame, fn func(context.Context, *frame) error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			f.root.end(ctx, false, errPanicked)
			panic(p)
		}
	}()
	return fn(f.with(f.hctx), f)
}
