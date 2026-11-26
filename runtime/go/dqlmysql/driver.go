// Package dqlmysql is the dql executor's driver for MySQL and MariaDB, on
// go-sql-driver/mysql (design record §22). It turns the values the driver
// decodes into the driver-neutral form of dql.Row, and dql's host types into
// values the driver encodes, so generated code holds no driver types.
//
//	exec, err := dql.New(ctx, dqlmysql.New(), dialect, cfg)
//
// Every connection reports the rows an UPDATE matched rather than those it
// changed, which is what the generated code counts, reads time values in
// UTC, the session time zone the dialect sets, and reports the ID of the
// last row an insert numbered, by which the runtime re-reads a written row
// (§20.1).
package dqlmysql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/thec1oud/dql/runtime/go/dql"
)

// Driver is a dql.Driver on go-sql-driver/mysql.
type Driver struct{}

// New returns the driver.
func New() *Driver { return &Driver{} }

// newConnector is mysql.NewConnector, replaced by tests.
var newConnector = mysql.NewConnector

// Open returns a pool for c.DSN, written in the driver's DSN syntax. Each
// new connection runs c.Setup. In simple mode the driver writes values
// into the statement text instead of preparing it (§26.4).
func (d *Driver) Open(_ context.Context, c dql.PoolConfig) (dql.Pool, error) {
	cfg, err := mysql.ParseDSN(c.DSN)
	if err != nil {
		return nil, err
	}
	cfg.ClientFoundRows = true
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.InterpolateParams = c.Mode == dql.ModeSimple
	connector, err := newConnector(cfg)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(&setupConnector{Connector: connector, setup: c.Setup})
	if c.MaxConns > 0 {
		db.SetMaxOpenConns(c.MaxConns)
		db.SetMaxIdleConns(c.MaxConns)
	}
	return &pool{db: db}, nil
}

// setupConnector runs the setup statements on each new connection.
type setupConnector struct {
	driver.Connector
	setup []string
}

func (s *setupConnector) Connect(ctx context.Context) (driver.Conn, error) {
	c, err := s.Connector.Connect(ctx)
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

// Native returns the fields of a *mysql.MySQLError: its number as the code,
// and the names its message carries (native).
func (d *Driver) Native(err error) (dql.NativeError, bool) {
	var e *mysql.MySQLError
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
		errors.Is(err, driver.ErrBadConn) || errors.Is(err, mysql.ErrInvalidConn) ||
		errors.Is(err, sql.ErrConnDone) ||
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
}

func (c *conn) Query(ctx context.Context, q string, args []any, _ []string) (dql.Rows, error) {
	q, vals, err := statement(q, args)
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

func (c *conn) Exec(ctx context.Context, q string, args []any, types []string) (int64, error) {
	n, _, err := c.ExecID(ctx, q, args, types)
	return n, err
}

// ExecID runs a statement and returns how many rows it matched and the
// last insert ID.
func (c *conn) ExecID(ctx context.Context, q string, args []any, _ []string) (int64, int64, error) {
	q, vals, err := statement(q, args)
	if err != nil {
		return 0, 0, err
	}
	res, err := c.c.ExecContext(ctx, q, vals...)
	if err != nil {
		return 0, 0, err
	}
	// The driver's result has both numbers and never fails to give them.
	n, _ := res.RowsAffected()
	id, _ := res.LastInsertId()
	return n, id, nil
}

// Begin starts a transaction. An isolation level is set for the next
// transaction only, so it holds for this one.
func (c *conn) Begin(ctx context.Context, o dql.TxOptions) error {
	if o.Isolation != "" {
		if err := c.exec(ctx, "SET TRANSACTION ISOLATION LEVEL "+strings.ToUpper(string(o.Isolation))); err != nil {
			return err
		}
	}
	q := "START TRANSACTION"
	if o.ReadOnly {
		q += " READ ONLY"
	}
	if err := c.exec(ctx, q); err != nil {
		return err
	}
	c.tx = true
	return nil
}

func (c *conn) Commit(ctx context.Context) error   { return c.end(ctx, "COMMIT") }
func (c *conn) Rollback(ctx context.Context) error { return c.end(ctx, "ROLLBACK") }

// end ends the transaction. When the statement fails the transaction may
// still be open, so the connection is discarded on release.
func (c *conn) end(ctx context.Context, q string) error {
	if err := c.exec(ctx, q); err != nil {
		return err
	}
	c.tx = false
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
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

func (c *conn) exec(ctx context.Context, q string) error {
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
