package dql

import "context"

// Driver is what the executor needs from a database driver: pools of
// connections, and the database's own fields of an error. An adapter
// package wraps one driver library, such as dqlpgx for pgx, so this package
// holds no driver and no dialect knowledge: SQL comes from the fragment
// table and query files (§16.2), and error codes from the generated error
// map (§23). A fake driver implements the same interfaces (§26.3).
type Driver interface {
	// Open returns a pool for one server. It does not connect.
	Open(ctx context.Context, c PoolConfig) (Pool, error)
	// Native returns the database's own fields of err, and false when the
	// database did not send err.
	Native(err error) (NativeError, bool)
	// Broken reports whether err means a connection failed or broke, as
	// opposed to a statement failing.
	Broken(err error) bool
}

// PoolConfig configures one pool.
type PoolConfig struct {
	// The data source name, with any env: reference resolved.
	DSN      string
	MaxConns int
	// Statements every new connection runs, in order, before its first use
	// (§22.7).
	Setup []string
}

// Pool is a pool of connections to one server.
type Pool interface {
	// Acquire returns a connection, waiting until ctx ends for one to free
	// up. It connects when the pool has room.
	Acquire(ctx context.Context) (Conn, error)
	Close()
}

// Conn is one connection, used by one goroutine at a time.
type Conn interface {
	// Query runs a statement that returns rows.
	Query(ctx context.Context, sql string, args []any) (Rows, error)
	// Exec runs a statement and returns how many rows it affected.
	Exec(ctx context.Context, sql string, args []any) (int64, error)
	Begin(ctx context.Context, opts TxOptions) error
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
	Savepoint(ctx context.Context, name string) error
	RollbackTo(ctx context.Context, name string) error
	ReleaseSavepoint(ctx context.Context, name string) error
	// Release returns the connection to its pool. A broken connection is
	// discarded.
	Release()
}

// Rows iterates over a query's rows, each in the driver-neutral form of
// Row.
type Rows interface {
	Next() bool
	Row() Row
	Err() error
	Close()
}

// TxOptions are how a transaction begins.
type TxOptions struct {
	ReadOnly  bool
	Isolation Isolation
}

// Isolation is a transaction isolation level. The empty level is the
// database's default.
type Isolation string

// The isolation levels.
const (
	ReadCommitted  Isolation = "read committed"
	RepeatableRead Isolation = "repeatable read"
	Serializable   Isolation = "serializable"
)

// NativeError is the database's own description of an error. Message and
// Detail may quote values (§23.3).
type NativeError struct {
	// Such as a SQLSTATE.
	Code    string
	Message string
	Detail  string
	// The names the error carries, where the database sends them: the
	// schema and table or data type, and the constraint or column.
	Schema     string
	Table      string
	DataType   string
	Constraint string
	Column     string
}
