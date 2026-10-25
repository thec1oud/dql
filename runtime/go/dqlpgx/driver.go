// Package dqlpgx is the dql executor's driver for Postgres, on pgx v5
// (design record §22). It turns the values pgx decodes into the
// driver-neutral form of dql.Row, and dql's host types into values pgx
// encodes, so generated code holds no pgx types.
//
//	exec, err := dql.New(ctx, dqlpgx.New("app.status", "app.address"), dialect, cfg)
//
// pgx decodes the built-in types itself. A user-defined composite type, and
// an array of a user-defined enum or composite type, decodes only once the
// driver registers it, so New takes their names; an enum on its own decodes
// as its label without registering.
package dqlpgx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thec1oud/dql/runtime/go/dql"
)

// Driver is a dql.Driver on pgx.
type Driver struct {
	types []string
}

// New returns a driver that registers the named user-defined types, each
// optionally schema-qualified, on every connection. A composite type's
// attribute types and an array's element type must come before it; the
// array type of each named type is registered with it.
func New(types ...string) *Driver {
	return &Driver{types: types}
}

// Open returns a pool for c.DSN. Each new connection runs c.Setup, then
// loads the driver's types.
func (d *Driver) Open(_ context.Context, c dql.PoolConfig) (dql.Pool, error) {
	cfg, err := pgxpool.ParseConfig(c.DSN)
	if err != nil {
		return nil, err
	}
	if c.MaxConns > 0 {
		cfg.MaxConns = int32(c.MaxConns)
	}
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		for _, s := range c.Setup {
			if _, err := conn.Exec(ctx, s); err != nil {
				return err
			}
		}
		return d.register(ctx, conn)
	}
	p, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		return nil, err
	}
	return &pool{p}, nil
}

// register loads the driver's types and their array types into conn's type
// map, one type at a time, since a type's dependencies must be registered
// before it loads.
func (d *Driver) register(ctx context.Context, conn *pgx.Conn) error {
	for _, t := range d.types {
		types, err := conn.LoadTypes(ctx, []string{t, arrayName(t)})
		if err != nil {
			return err
		}
		conn.TypeMap().RegisterTypes(types)
		if _, ok := conn.TypeMap().TypeForName(t); !ok {
			return fmt.Errorf("dqlpgx: no type %s", t)
		}
	}
	return nil
}

// arrayName returns the name of the array type of the named type: _name in
// the same schema.
func arrayName(t string) string {
	for i := len(t) - 1; i >= 0; i-- {
		if t[i] == '.' {
			return t[:i+1] + "_" + t[i+1:]
		}
	}
	return "_" + t
}

// Native returns the fields of a *pgconn.PgError.
func (d *Driver) Native(err error) (dql.NativeError, bool) {
	var e *pgconn.PgError
	if !errors.As(err, &e) {
		return dql.NativeError{}, false
	}
	return dql.NativeError{
		Code:       e.Code,
		Message:    e.Message,
		Detail:     e.Detail,
		Schema:     e.SchemaName,
		Table:      e.TableName,
		DataType:   e.DataTypeName,
		Constraint: e.ConstraintName,
		Column:     e.ColumnName,
	}, true
}

// Broken reports whether err is a failure to connect, a network error, or
// a use of a closed connection.
func (d *Driver) Broken(err error) bool {
	var connect *pgconn.ConnectError
	var netErr net.Error
	return errors.As(err, &connect) || errors.As(err, &netErr) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) ||
		pgconn.SafeToRetry(err)
}

type pool struct{ p *pgxpool.Pool }

func (p *pool) Acquire(ctx context.Context) (dql.Conn, error) {
	c, err := p.p.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	return &conn{c}, nil
}

func (p *pool) Close() { p.p.Close() }

type conn struct{ c *pgxpool.Conn }

func (c *conn) Query(ctx context.Context, sql string, args []any) (dql.Rows, error) {
	r, err := c.c.Query(ctx, sql, encodeArgs(args)...)
	if err != nil {
		return nil, err
	}
	return &rows{r: r, m: c.c.Conn().TypeMap()}, nil
}

func (c *conn) Exec(ctx context.Context, sql string, args []any) (int64, error) {
	tag, err := c.c.Exec(ctx, sql, encodeArgs(args)...)
	return tag.RowsAffected(), err
}

func (c *conn) Begin(ctx context.Context, o dql.TxOptions) error {
	sql := "BEGIN"
	if o.Isolation != "" {
		sql += " ISOLATION LEVEL " + string(o.Isolation)
	}
	if o.ReadOnly {
		sql += " READ ONLY"
	}
	return c.exec(ctx, sql)
}

// ErrCommitRolledBack is Commit's error when the database rolled the
// transaction back instead, because a statement in it failed.
var ErrCommitRolledBack = errors.New("dqlpgx: commit rolled back")

func (c *conn) Commit(ctx context.Context) error {
	tag, err := c.c.Exec(ctx, "COMMIT")
	if err == nil && tag.String() == "ROLLBACK" {
		err = ErrCommitRolledBack
	}
	return err
}

func (c *conn) Rollback(ctx context.Context) error { return c.exec(ctx, "ROLLBACK") }

func (c *conn) Savepoint(ctx context.Context, name string) error {
	return c.exec(ctx, "SAVEPOINT "+pgx.Identifier{name}.Sanitize())
}

func (c *conn) RollbackTo(ctx context.Context, name string) error {
	return c.exec(ctx, "ROLLBACK TO SAVEPOINT "+pgx.Identifier{name}.Sanitize())
}

func (c *conn) ReleaseSavepoint(ctx context.Context, name string) error {
	return c.exec(ctx, "RELEASE SAVEPOINT "+pgx.Identifier{name}.Sanitize())
}

func (c *conn) exec(ctx context.Context, sql string) error {
	_, err := c.c.Exec(ctx, sql)
	return err
}

// Release returns the connection to its pool, which discards it when it is
// closed or left in a transaction.
func (c *conn) Release() { c.c.Release() }

type rows struct {
	r   pgx.Rows
	m   *pgtype.Map
	row dql.Row
	err error
}

// Next reads and converts the next row; a value that does not decode stops
// the rows with its error.
func (r *rows) Next() bool {
	if r.err != nil || !r.r.Next() {
		return false
	}
	fields := r.r.FieldDescriptions()
	raw := r.r.RawValues()
	r.row = make(dql.Row, len(raw))
	for i, f := range fields {
		v, err := decode(r.m, f.DataTypeOID, f.Format, raw[i])
		if err != nil {
			r.err = fmt.Errorf("dqlpgx: column %d (%s): %w", i, f.Name, err)
			r.r.Close()
			return false
		}
		r.row[i] = v
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
