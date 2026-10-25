package dql

import (
	"context"
	"fmt"
	"sync/atomic"
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
}

// WithPropagation sets how the transaction relates to an enclosing one.
func WithPropagation(p Propagation) TxOption { return func(c *txConfig) { c.propagation = p } }

// WithIsolation sets the isolation level of a transaction it begins.
func WithIsolation(i Isolation) TxOption { return func(c *txConfig) { c.isolation = i } }

// WithRetry overrides the executor's retry policy for one closure (§22.4).
func WithRetry(p RetryPolicy) TxOption { return func(c *txConfig) { c.retry = &p } }

// root is a transaction that owns its connections: one per source it has
// used, each begun on first use, so a transaction spans the sources its
// statements reach.
type root struct {
	exec        *Executor
	opts        TxOptions
	conns       map[*sourcePool]Conn
	order       []*sourcePool
	independent int // how deep in independent transactions
	done        bool
}

// frame is one level of a transaction: its root, or a savepoint in it.
type frame struct {
	root      *root
	parent    *frame
	savepoint string
	// The sources the savepoint was made on.
	saved map[*sourcePool]bool
}

type frameKey struct{}

func frameOf(ctx context.Context) *frame {
	f, _ := ctx.Value(frameKey{}).(*frame)
	return f
}

func (f *frame) with(ctx context.Context) context.Context {
	return context.WithValue(ctx, frameKey{}, f)
}

// conn returns the frame's connection to s, beginning the root's
// transaction on s and making the frame's savepoints there on first use.
func (f *frame) conn(ctx context.Context, s *sourcePool, q string) (Conn, error) {
	r := f.root
	if r.done {
		return nil, ErrDone
	}
	c := r.conns[s]
	if c == nil {
		var err error
		if c, err = r.exec.conn(ctx, s, r.independent > 0, q); err != nil {
			return nil, err
		}
		if err := c.Begin(ctx, r.opts); err != nil {
			c.Release()
			return nil, r.exec.translate(err, "", q, s)
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
				return nil, r.exec.translate(err, "", q, s)
			}
			g.saved[s] = true
		}
	}
	return c, nil
}

// end commits or rolls back every connection of the root and releases
// them. Commit stops at the first failure and rolls back the rest.
func (r *root) end(ctx context.Context, commit bool) error {
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
			err = c.Commit(ctx)
		} else {
			err = c.Rollback(ctx)
		}
		if err != nil && first == nil {
			first = r.exec.translate(err, "", "", s)
		}
		c.Release()
	}
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
	return t.f.root.end(ctx, true)
}

// Rollback rolls back a transaction of the pair form. After Commit it does
// nothing, so it can be deferred.
func (t *txHandle) Rollback(ctx context.Context) error {
	if !t.owns {
		return nil
	}
	return t.f.root.end(ctx, false)
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
	return &WriteTx{txHandle{f: f, owns: owns}}, nil
}

// BeginRead begins a read transaction of the pair form.
func (e *Executor) BeginRead(ctx context.Context, opts ...TxOption) (*ReadTx, error) {
	f, owns, err := e.begin(ctx, true, opts)
	if err != nil {
		return nil, err
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
		return e.newRoot(readOnly, c, depth), true, nil
	}
	if enclosing == nil {
		return e.newRoot(readOnly, c, 0), true, nil
	}
	if !readOnly && enclosing.root.opts.ReadOnly {
		return nil, false, ErrReadOnly
	}
	return enclosing, false, nil
}

func (e *Executor) newRoot(readOnly bool, c txConfig, independent int) *frame {
	return &frame{root: &root{exec: e, opts: TxOptions{ReadOnly: readOnly, Isolation: c.isolation},
		conns: map[*sourcePool]Conn{}, independent: independent}}
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
		f := &frame{root: enclosing.root, parent: enclosing, savepoint: fmt.Sprintf("dql_%d", savepoints.Add(1)), saved: map[*sourcePool]bool{}}
		err := call(ctx, f, fn)
		if end := f.endSavepoint(ctx, err == nil); err == nil {
			err = end
		}
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
			err = f.root.end(ctx, true)
		} else {
			f.root.end(ctx, false)
		}
		if err == nil || !retryable(err) || attempt >= policy.Attempts || !wait(ctx, policy.backoff(attempt, e.rand)) {
			if err != nil && attempt > 1 {
				err = &Retried{Attempts: attempt, Err: err}
			}
			return err
		}
		f = e.newRoot(readOnly, c, f.root.independent)
	}
}

// call runs fn in f, rolling back f's root and re-panicking on a panic.
func call(ctx context.Context, f *frame, fn func(context.Context, *frame) error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			f.root.end(ctx, false)
			panic(p)
		}
	}()
	return fn(f.with(ctx), f)
}
