// Package dqlsqlite is the dql executor's driver for SQLite, on
// modernc.org/sqlite, which needs no C compiler (design record §22). It
// turns the values the driver decodes into the driver-neutral form of
// dql.Row, and dql's host types into values the driver encodes, so
// generated code holds no driver types.
//
//	exec, err := dql.New(ctx, dqlsqlite.New(), dialect, cfg)
//
// The DSN is modernc.org/sqlite's: a file name or a file: URI, whose query
// may set pragmas, such as file:shop.db?_pragma=busy_timeout(5000).
//
// SQLite's transactions are serializable whatever the isolation level
// asked for. A transaction that may write begins IMMEDIATE, taking the
// write lock at once, so that it never fails to upgrade a read snapshot
// part way; a read-only one begins DEFERRED and refuses writes with
// query_only.
package dqlsqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"

	"modernc.org/sqlite"

	"github.com/thec1oud/dql/runtime/go/dql"
)

// Driver is a dql.Driver on modernc.org/sqlite.
type Driver struct{}

// New returns the driver.
func New() *Driver { return &Driver{} }

// Open returns a pool for c.DSN. Each new connection runs c.Setup. SQLite
// prepares every statement, so the statement mode changes nothing (§26.4).
func (d *Driver) Open(_ context.Context, c dql.PoolConfig) (dql.Pool, error) {
	db := sql.OpenDB(&connector{dsn: c.DSN, setup: c.Setup})
	if c.MaxConns > 0 {
		db.SetMaxOpenConns(c.MaxConns)
		db.SetMaxIdleConns(c.MaxConns)
	}
	return &pool{db: db}, nil
}

// connector opens connections with modernc.org/sqlite and runs the setup
// statements on each.
type connector struct {
	dsn   string
	setup []string
}

func (s *connector) Connect(ctx context.Context) (driver.Conn, error) {
	c, err := s.Driver().Open(s.dsn)
	if err != nil {
		return nil, err
	}
	for _, q := range s.setup {
		if _, err := c.(driver.ExecerContext).ExecContext(ctx, q, nil); err != nil {
			c.Close()
			return nil, err
		}
	}
	return c, nil
}

func (s *connector) Driver() driver.Driver { return &sqlite.Driver{} }

// Native returns the fields of a *sqlite.Error: its extended result code as
// the code, and the names its message carries (native).
func (d *Driver) Native(err error) (dql.NativeError, bool) {
	var e *sqlite.Error
	if !errors.As(err, &e) {
		return dql.NativeError{}, false
	}
	return native(e.Code(), e.Error()), true
}

// Broken reports whether err is the use of a broken or closed connection.
// SQLite runs in the process, so nothing else breaks one.
func (d *Driver) Broken(err error) bool {
	return errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone)
}

type pool struct {
	db *sql.DB
}

func (p *pool) Acquire(ctx context.Context) (dql.Conn, error) {
	c, err := p.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	return &conn{c: c}, nil
}

func (p *pool) Close() { p.db.Close() }

// conn is one connection. It runs transaction control as statements, so
// it knows when a transaction is open: a connection released in one is
// discarded.
type conn struct {
	c        *sql.Conn
	tx       bool
	readOnly bool
}

func (c *conn) Query(ctx context.Context, q string, args []any, _ []string) (dql.Rows, error) {
	vals, err := arguments(args)
	if err != nil {
		return nil, err
	}
	r, err := c.c.QueryContext(ctx, q, vals...)
	if err != nil {
		return nil, err
	}
	// The rows are open, so their columns are known.
	cols, _ := r.Columns()
	return &rows{r: r, n: len(cols)}, nil
}

func (c *conn) Exec(ctx context.Context, q string, args []any, _ []string) (int64, error) {
	vals, err := arguments(args)
	if err != nil {
		return 0, err
	}
	res, err := c.c.ExecContext(ctx, q, vals...)
	if err != nil {
		return 0, err
	}
	// The driver's result always has the count.
	n, _ := res.RowsAffected()
	return n, nil
}

// Begin starts a transaction: IMMEDIATE when it may write, and DEFERRED
// under query_only when it only reads.
func (c *conn) Begin(ctx context.Context, o dql.TxOptions) error {
	if o.ReadOnly {
		if err := c.exec(ctx, "PRAGMA query_only = ON"); err != nil {
			return err
		}
		c.readOnly = true
		if err := c.exec(ctx, "BEGIN DEFERRED"); err != nil {
			c.writable(ctx)
			return err
		}
	} else if err := c.exec(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	c.tx = true
	return nil
}

func (c *conn) Commit(ctx context.Context) error { return c.end(ctx, "COMMIT") }

// Rollback rolls the transaction back. SQLite rolls a transaction back on
// its own after some errors, such as a full disk; the transaction has then
// ended, so the rollback succeeds.
func (c *conn) Rollback(ctx context.Context) error {
	err := c.end(ctx, "ROLLBACK")
	if err != nil && strings.Contains(err.Error(), "no transaction is active") {
		c.tx = false
		c.writable(ctx)
		return nil
	}
	return err
}

// end ends the transaction, and lets a read-only one's connection write
// again. When the statement fails the transaction may still be open, so
// the connection is discarded on release.
func (c *conn) end(ctx context.Context, q string) error {
	if err := c.exec(ctx, q); err != nil {
		return err
	}
	c.tx = false
	return c.writable(ctx)
}

// writable turns query_only off after a read-only transaction. When it
// fails the connection is left read-only, so it is discarded on release.
func (c *conn) writable(ctx context.Context) error {
	if !c.readOnly {
		return nil
	}
	if err := c.exec(ctx, "PRAGMA query_only = OFF"); err != nil {
		return err
	}
	c.readOnly = false
	return nil
}

func (c *conn) Savepoint(ctx context.Context, name string) error {
	return c.exec(ctx, "SAVEPOINT "+quote(name))
}

func (c *conn) RollbackTo(ctx context.Context, name string) error {
	return c.exec(ctx, "ROLLBACK TO SAVEPOINT "+quote(name))
}

func (c *conn) ReleaseSavepoint(ctx context.Context, name string) error {
	return c.exec(ctx, "RELEASE SAVEPOINT "+quote(name))
}

// quote quotes an identifier.
func quote(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func (c *conn) exec(ctx context.Context, q string) error {
	_, err := c.c.ExecContext(ctx, q)
	return err
}

// Release returns the connection to its pool, or discards it when it is
// left in a transaction or read-only.
func (c *conn) Release() {
	if c.tx || c.readOnly {
		c.c.Raw(func(any) error { return driver.ErrBadConn })
	}
	c.c.Close()
}

type rows struct {
	r   *sql.Rows
	n   int
	row dql.Row
}

// Next reads and converts the next row.
func (r *rows) Next() bool {
	if !r.r.Next() {
		return false
	}
	vals := make([]any, r.n)
	ptrs := make([]any, len(vals))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	// Scanning into an any never fails.
	_ = r.r.Scan(ptrs...)
	r.row = make(dql.Row, len(vals))
	for i, v := range vals {
		r.row[i] = decode(v)
	}
	return true
}

func (r *rows) Row() dql.Row { return r.row }

func (r *rows) Err() error { return r.r.Err() }

func (r *rows) Close() { r.r.Close() }
