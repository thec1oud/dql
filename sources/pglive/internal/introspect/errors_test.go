package introspect

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var errInjected = errors.New("injected")

// faultyConn fails the begin when failAt is 0, and otherwise the failAt-th
// statement of the transaction.
type faultyConn struct {
	conn   *pgx.Conn
	failAt int
}

func (c faultyConn) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	if c.failAt == 0 {
		return nil, errInjected
	}
	tx, err := c.conn.BeginTx(ctx, opts)
	return &faultyTx{Tx: tx, failAt: c.failAt}, err
}

type faultyTx struct {
	pgx.Tx
	n, failAt int
}

func (t *faultyTx) fail() bool {
	t.n++
	return t.n == t.failAt
}

func (t *faultyTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if t.fail() {
		return pgconn.CommandTag{}, errInjected
	}
	return t.Tx.Exec(ctx, sql, args...)
}

func (t *faultyTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if t.fail() {
		return nil, errInjected
	}
	return t.Tx.Query(ctx, sql, args...)
}

// Every statement's failure is reported, until none is left to fail.
func TestEveryFailureIsReported(t *testing.T) {
	for failAt := 0; ; failAt++ {
		s, err := Read(context.Background(), faultyConn{conn: shopDB, failAt: failAt}, []string{"app"})
		if err == nil {
			if failAt < 2 || s.Relations == nil {
				t.Fatalf("Read succeeded with failure %d injected", failAt)
			}
			return
		}
		if !errors.Is(err, errInjected) {
			t.Fatalf("failure %d: %v", failAt, err)
		}
	}
}

func TestQueryErrorIsReported(t *testing.T) {
	tx, err := shopDB.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := collect[Database](context.Background(), tx, `SELECT json_build_object('x', 1 / 0)`); err == nil {
		t.Fatal("division by zero was not reported")
	}
}

func TestDecodeErrorIsReported(t *testing.T) {
	tx, err := shopDB.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	_, err = collect[Database](context.Background(), tx, `SELECT json_build_object('encoding', 1)`)
	if err == nil || err.Error() != "decoding introspect.Database: json: cannot unmarshal number into Go struct field Database.encoding of type string" {
		t.Fatalf("err = %v", err)
	}
}
