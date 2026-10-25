package dql

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// fakeDriver is a scripted driver: every call is logged, a statement's
// result comes from handle, and an operation fails when fail names it.
type fakeDriver struct {
	mu      sync.Mutex
	log     []string
	openErr error
	pools   []*fakePool
	// handle answers a statement: its rows, rows affected, or an error.
	handle func(sql string, args []any) ([]Row, int64, error)
	// fail makes an operation fail: begin, commit, rollback, savepoint,
	// rollback to, release, acquire, or rows (after the rows are read).
	fail map[string]error
}

type fakeNative struct{ n NativeError }

func (e *fakeNative) Error() string { return "native " + e.n.Code }

var errBroken = errors.New("broken pipe")

func (d *fakeDriver) record(format string, args ...any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.log = append(d.log, fmt.Sprintf(format, args...))
}

func (d *fakeDriver) failure(op string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.fail[op]
}

// calls returns the log since the last call, joined.
func (d *fakeDriver) calls() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := strings.Join(d.log, "; ")
	d.log = nil
	return out
}

func (d *fakeDriver) Open(_ context.Context, c PoolConfig) (Pool, error) {
	if d.openErr != nil {
		return nil, d.openErr
	}
	p := &fakePool{d: d, cfg: c, free: make(chan struct{}, c.MaxConns)}
	for range c.MaxConns {
		p.free <- struct{}{}
	}
	d.mu.Lock()
	d.pools = append(d.pools, p)
	d.mu.Unlock()
	d.record("open %s max %d setup %q", c.DSN, c.MaxConns, c.Setup)
	return p, nil
}

func (d *fakeDriver) Native(err error) (NativeError, bool) {
	var n *fakeNative
	if errors.As(err, &n) {
		return n.n, true
	}
	return NativeError{}, false
}

func (d *fakeDriver) Broken(err error) bool { return errors.Is(err, errBroken) }

type fakePool struct {
	d      *fakeDriver
	cfg    PoolConfig
	free   chan struct{}
	n      int
	closed bool
}

func (p *fakePool) Acquire(ctx context.Context) (Conn, error) {
	if err := p.d.failure("acquire"); err != nil {
		return nil, err
	}
	select {
	case <-p.free:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	p.d.mu.Lock()
	p.n++
	id := fmt.Sprintf("%s#%d", p.cfg.DSN, p.n)
	p.d.mu.Unlock()
	return &fakeConn{p: p, id: id}, nil
}

func (p *fakePool) Close() {
	p.closed = true
	p.d.record("close %s", p.cfg.DSN)
}

type fakeConn struct {
	p  *fakePool
	id string
}

func (c *fakeConn) op(name string, detail ...string) error {
	c.p.d.record("%s %s", c.id, strings.TrimSpace(name+" "+strings.Join(detail, " ")))
	return c.p.d.failure(name)
}

func (c *fakeConn) Query(_ context.Context, sql string, args []any) (Rows, error) {
	c.p.d.record("%s query %s %v", c.id, sql, args)
	rows, _, err := c.p.d.handle(sql, args)
	if err != nil {
		return nil, err
	}
	return &fakeRows{rows: rows, err: c.p.d.failure("rows")}, nil
}

func (c *fakeConn) Exec(_ context.Context, sql string, args []any) (int64, error) {
	c.p.d.record("%s exec %s %v", c.id, sql, args)
	_, n, err := c.p.d.handle(sql, args)
	return n, err
}

func (c *fakeConn) Begin(_ context.Context, o TxOptions) error {
	var mode []string
	if o.ReadOnly {
		mode = append(mode, "read only")
	}
	if o.Isolation != "" {
		mode = append(mode, string(o.Isolation))
	}
	return c.op("begin", mode...)
}
func (c *fakeConn) Commit(context.Context) error                { return c.op("commit") }
func (c *fakeConn) Rollback(context.Context) error              { return c.op("rollback") }
func (c *fakeConn) Savepoint(_ context.Context, n string) error { return c.op("savepoint", n) }
func (c *fakeConn) RollbackTo(_ context.Context, n string) error {
	return c.op("rollback to", n)
}
func (c *fakeConn) ReleaseSavepoint(_ context.Context, n string) error {
	return c.op("release", n)
}
func (c *fakeConn) Release() {
	c.p.d.record("%s put back", c.id)
	c.p.free <- struct{}{}
}

type fakeRows struct {
	rows []Row
	i    int
	err  error
	done bool
}

func (r *fakeRows) Next() bool {
	if r.i >= len(r.rows) {
		return false
	}
	r.i++
	return true
}
func (r *fakeRows) Row() Row { return r.rows[r.i-1] }
func (r *fakeRows) Err() error {
	return r.err
}
func (r *fakeRows) Close() { r.done = true }
