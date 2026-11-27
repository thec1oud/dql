// Package dqlsqlserver is the dql executor's driver for SQL Server 2022 and
// newer, on go-mssqldb (design record §22). It turns the values the driver
// decodes into the driver-neutral form of dql.Row, and dql's host types
// into values the driver encodes, so generated code holds no driver types.
//
//	exec, err := dql.New(ctx, dqlsqlserver.New(), dialect, cfg)
//
// The DSN is a go-mssqldb URL, sqlserver://user:password@host:1433?database=shop.
// Every new connection, and every connection the pool resets for reuse,
// runs the dialect's session setup. go-mssqldb sends each parameterized
// statement through sp_executesql, so a statement is always prepared on the
// server; the simple mode changes nothing.
package dqlsqlserver

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net"
	"strings"

	mssql "github.com/microsoft/go-mssqldb"
	"github.com/microsoft/go-mssqldb/msdsn"

	"github.com/thec1oud/dql/runtime/go/dql"
)

// Driver is a dql.Driver on go-mssqldb.
type Driver struct{}

// New returns the driver.
func New() *Driver { return &Driver{} }

// Open returns a pool for c.DSN. c.Setup runs as one batch on each new
// connection and again each time the pool resets one for reuse, which sets
// the session back to the server's defaults.
func (d *Driver) Open(_ context.Context, c dql.PoolConfig) (dql.Pool, error) {
	if _, err := msdsn.Parse(c.DSN); err != nil {
		return nil, err
	}
	// The DSN parsed, so the connector does too.
	connector, _ := mssql.NewConnector(c.DSN)
	connector.SessionInitSQL = strings.Join(c.Setup, "; ")
	db := sql.OpenDB(connector)
	if c.MaxConns > 0 {
		db.SetMaxOpenConns(c.MaxConns)
		db.SetMaxIdleConns(c.MaxConns)
	}
	return &pool{db: db}, nil
}

// Native returns the fields of an mssql.Error: its number as the code, and
// the names its message carries (native).
func (d *Driver) Native(err error) (dql.NativeError, bool) {
	var e mssql.Error
	if !errors.As(err, &e) {
		return dql.NativeError{}, false
	}
	return native(e.Number, e.Message), true
}

// Broken reports whether err is a failure to connect, a network error, or
// a use of a broken or closed connection.
func (d *Driver) Broken(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) ||
		errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed)
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
	c  *sql.Conn
	tx bool
	// isolated is set while a transaction runs at a level of its own.
	isolated bool
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
	// The rows are open, so their column types are known.
	types, _ := r.ColumnTypes()
	names := make([]string, len(types))
	for i, t := range types {
		names[i] = t.DatabaseTypeName()
	}
	return &rows{r: r, types: names}, nil
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
	// The driver's result counts the rows and never fails to.
	n, _ := res.RowsAffected()
	return n, nil
}

// Begin starts a transaction. SQL Server has no read-only transaction, so
// ReadOnly changes nothing. An isolation level holds for the session, so
// the transaction's end sets it back to the default, READ COMMITTED.
func (c *conn) Begin(ctx context.Context, o dql.TxOptions) error {
	if o.Isolation != "" {
		if err := c.exec(ctx, "SET TRANSACTION ISOLATION LEVEL "+strings.ToUpper(string(o.Isolation))); err != nil {
			return err
		}
		c.isolated = true
	}
	if err := c.exec(ctx, "BEGIN TRANSACTION"); err != nil {
		return err
	}
	c.tx = true
	return nil
}

func (c *conn) Commit(ctx context.Context) error { return c.end(ctx, "COMMIT TRANSACTION") }

// Rollback rolls the transaction back. When the server has already ended
// it, as it ends a transaction an error dooms or a broken connection, the
// rollback succeeds.
func (c *conn) Rollback(ctx context.Context) error {
	err := c.end(ctx, "IF @@TRANCOUNT > 0 ROLLBACK TRANSACTION")
	if err != nil && (&Driver{}).Broken(err) {
		return nil
	}
	return err
}

// end ends the transaction, then sets the isolation level back. When the
// statement fails the transaction may still be open, so the connection is
// discarded on release.
func (c *conn) end(ctx context.Context, q string) error {
	if err := c.exec(ctx, q); err != nil {
		return err
	}
	c.tx = false
	if c.isolated {
		c.isolated = false
		return c.exec(ctx, "SET TRANSACTION ISOLATION LEVEL READ COMMITTED")
	}
	return nil
}

func (c *conn) Savepoint(ctx context.Context, name string) error {
	return c.exec(ctx, "SAVE TRANSACTION "+quote(name))
}

func (c *conn) RollbackTo(ctx context.Context, name string) error {
	return c.exec(ctx, "ROLLBACK TRANSACTION "+quote(name))
}

// ReleaseSavepoint does nothing: a SQL Server savepoint lasts until its
// transaction ends.
func (c *conn) ReleaseSavepoint(context.Context, string) error { return nil }

// quote quotes an identifier.
func quote(name string) string {
	return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
}

// exec runs q. go-mssqldb sends a statement even when its context is
// done, and the statement then runs, fails, or breaks the connection, by
// how soon the driver sees the context; a done context fails it unsent.
func (c *conn) exec(ctx context.Context, q string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := c.c.ExecContext(ctx, q)
	return err
}

// Release returns the connection to its pool, or discards it when it is
// left in a transaction.
func (c *conn) Release() {
	if c.tx {
		c.c.Raw(func(any) error { return driver.ErrBadConn })
	}
	c.c.Close()
}

// decodeValue is decode, replaced by tests.
var decodeValue = decode

type rows struct {
	r     *sql.Rows
	types []string
	row   dql.Row
	err   error
}

// Next reads and converts the next row; a value that does not convert
// stops the rows with its error.
func (r *rows) Next() bool {
	if r.err != nil || !r.r.Next() {
		return false
	}
	vals := make([]any, len(r.types))
	ptrs := make([]any, len(vals))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	// Scanning into an any never fails.
	_ = r.r.Scan(ptrs...)
	r.row = make(dql.Row, len(vals))
	for i, v := range vals {
		x, err := decodeValue(r.types[i], v)
		if err != nil {
			r.err = err
			r.r.Close()
			return false
		}
		r.row[i] = x
	}
	return true
}

func (r *rows) Row() dql.Row { return r.row }

func (r *rows) Err() error {
	if r.err != nil {
		return r.err
	}
	return r.r.Err()
}

func (r *rows) Close() { r.r.Close() }
