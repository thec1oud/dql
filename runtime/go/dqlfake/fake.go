// Package dqlfake is a recording fake for tests of code that runs dql
// queries (design record §26.3). It is a dql.Driver that answers each
// statement with rows set up by query name, and parameters when given,
// and records every call, so builder chains need no mocking method by
// method:
//
//	f := dqlfake.New()
//	f.On("users.get.by_primary", id).Return(dql.Row{id, "a@b.c"})
//	exec := f.Executor(t, dialect, []string{"shop"})
//	// The code under test runs its queries on exec.
//
// A call no expectation matches fails with an *Unexpected error and is
// listed by Unexpected. Integration tests against real databases remain
// the main guarantee.
package dqlfake

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	"github.com/thec1oud/dql/runtime/go/dql"
)

// Fake is a recording fake driver. It is safe for concurrent use.
type Fake struct {
	mu         sync.Mutex
	exps       []*Expectation
	calls      []Call
	unexpected []Call
}

// New returns a fake with no expectations.
func New() *Fake { return &Fake{} }

// Expectation answers the calls of one query, with given parameters or
// any.
type Expectation struct {
	f     *Fake
	query string
	args  []any
	any   bool
	rows  []dql.Row
	n     int64
	err   error
	calls int
}

// On expects calls of query with exactly args, compared deeply. Later
// expectations take precedence over earlier ones.
func (f *Fake) On(query string, args ...any) *Expectation {
	return f.add(&Expectation{f: f, query: query, args: args})
}

// OnAny expects calls of query with any parameters.
func (f *Fake) OnAny(query string) *Expectation {
	return f.add(&Expectation{f: f, query: query, any: true})
}

func (f *Fake) add(x *Expectation) *Expectation {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exps = append(f.exps, x)
	return x
}

// Return answers with rows, and as many rows affected.
func (x *Expectation) Return(rows ...dql.Row) *Expectation {
	x.rows, x.n = rows, int64(len(rows))
	return x
}

// Affected answers a statement that returns no rows with n rows affected.
func (x *Expectation) Affected(n int64) *Expectation {
	x.n = n
	return x
}

// Fail answers with err. An error of the dql taxonomy reaches the caller
// as it is, such as a *dql.UniqueViolation.
func (x *Expectation) Fail(err error) *Expectation {
	x.err = err
	return x
}

// Calls returns how many calls the expectation answered.
func (x *Expectation) Calls() int {
	x.f.mu.Lock()
	defer x.f.mu.Unlock()
	return x.calls
}

// Call is one statement the fake ran.
type Call struct {
	Query string
	SQL   string
	Args  []any
	// The statement ran in a transaction.
	InTx bool
}

// Unexpected is the error of a call no expectation matched.
type Unexpected struct{ Call Call }

func (e *Unexpected) Error() string {
	return fmt.Sprintf("dqlfake: unexpected call of %s with %v", e.Call.Query, e.Call.Args)
}

// Class makes the error reach the caller as it is.
func (e *Unexpected) Class() dql.Class { return dql.ClassDatabaseError }

// Calls returns every statement the fake ran, in order.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

// Unexpected returns the calls no expectation matched.
func (f *Fake) Unexpected() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.unexpected...)
}

// answer records a call and finds its expectation.
func (f *Fake) answer(c Call) (*Expectation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	for i := len(f.exps) - 1; i >= 0; i-- {
		x := f.exps[i]
		if x.query == c.Query && (x.any || reflect.DeepEqual(x.args, c.Args) || len(x.args) == 0 && len(c.Args) == 0) {
			x.calls++
			return x, x.err
		}
	}
	f.unexpected = append(f.unexpected, c)
	return nil, &Unexpected{Call: c}
}

// TB is the part of testing.TB the fake uses.
type TB interface {
	Errorf(format string, args ...any)
	Cleanup(func())
}

// Executor returns an executor on the fake for the given catalogs, in the
// dialect generated code was generated for, with options such as hooks.
// The test fails if a call went unexpected.
func (f *Fake) Executor(t TB, dialect dql.Dialect, catalogs []string, opts ...dql.Option) *dql.Executor {
	cats := map[string]string{}
	for _, c := range catalogs {
		cats[c] = "fake"
	}
	// New cannot fail: every catalog names the one source, whose pool
	// always connects.
	e, _ := dql.New(context.Background(), f, dialect, dql.Config{
		Sources:  map[string]dql.SourceConfig{"fake": {Primary: dql.ConnConfig{DSN: "fake"}}},
		Catalogs: cats,
	}, opts...)
	t.Cleanup(func() {
		e.Close()
		for _, c := range f.Unexpected() {
			t.Errorf("dqlfake: unexpected call of %s with %v", c.Query, c.Args)
		}
	})
	return e
}

// Open returns a pool of fake connections.
func (f *Fake) Open(context.Context, dql.PoolConfig) (dql.Pool, error) { return pool{f}, nil }

// Native reports that the fake sends no database errors.
func (f *Fake) Native(error) (dql.NativeError, bool) { return dql.NativeError{}, false }

// Broken reports that no fake connection breaks.
func (f *Fake) Broken(error) bool { return false }

type pool struct{ f *Fake }

func (p pool) Acquire(context.Context) (dql.Conn, error) { return &conn{f: p.f}, nil }
func (p pool) Close()                                    {}

type conn struct {
	f    *Fake
	inTx bool
}

func (c *conn) call(ctx context.Context, sql string, args []any) (*Expectation, error) {
	return c.f.answer(Call{Query: dql.QueryName(ctx), SQL: sql, Args: args, InTx: c.inTx})
}

func (c *conn) Query(ctx context.Context, sql string, args []any) (dql.Rows, error) {
	x, err := c.call(ctx, sql, args)
	if err != nil {
		return nil, err
	}
	return &rows{rows: x.rows}, nil
}

func (c *conn) Exec(ctx context.Context, sql string, args []any) (int64, error) {
	x, err := c.call(ctx, sql, args)
	if err != nil {
		return 0, err
	}
	return x.n, nil
}

func (c *conn) Begin(context.Context, dql.TxOptions) error { c.inTx = true; return nil }
func (c *conn) Commit(context.Context) error               { c.inTx = false; return nil }
func (c *conn) Rollback(context.Context) error             { c.inTx = false; return nil }
func (c *conn) Savepoint(context.Context, string) error    { return nil }
func (c *conn) RollbackTo(context.Context, string) error   { return nil }
func (c *conn) ReleaseSavepoint(context.Context, string) error {
	return nil
}
func (c *conn) Release() {}

type rows struct {
	rows []dql.Row
	i    int
}

func (r *rows) Next() bool {
	if r.i >= len(r.rows) {
		return false
	}
	r.i++
	return true
}

func (r *rows) Row() dql.Row { return r.rows[r.i-1] }
func (r *rows) Err() error   { return nil }
func (r *rows) Close()       {}
