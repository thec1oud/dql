package dqlsqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"modernc.org/sqlite"

	"github.com/thec1oud/dql/runtime/go/civil"
	"github.com/thec1oud/dql/runtime/go/dec"
	"github.com/thec1oud/dql/runtime/go/dql"
	"github.com/thec1oud/dql/runtime/go/storage"
	"github.com/thec1oud/dql/runtime/go/weak"
)

// setup is the SQLite dialect's session setup.
var setup = []string{"PRAGMA foreign_keys = ON", "PRAGMA case_sensitive_like = ON"}

var schema = []string{
	`CREATE TABLE users (
		id INTEGER PRIMARY KEY,
		email TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL DEFAULT '',
		age INTEGER,
		CONSTRAINT users_age CHECK (age >= 0),
		CHECK (length(name) < 10)
	) STRICT`,
	`CREATE UNIQUE INDEX users_lower_name ON users (lower(name))`,
	`CREATE TABLE pairs (a INTEGER, b INTEGER, UNIQUE (a, b))`,
	`CREATE TABLE posts (
		id INTEGER PRIMARY KEY,
		author INTEGER NOT NULL REFERENCES users (id)
	) STRICT`,
	`CREATE TABLE loose (x, d DATETIME, n NUMERIC)`,
}

// database creates a database file with the test schema and returns its
// DSN.
func database(t *testing.T) string {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "test.db") + "?_pragma=busy_timeout(2000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, s := range schema {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	return dsn
}

func open(t *testing.T, dsn string) (dql.Pool, dql.Conn) {
	t.Helper()
	p, err := New().Open(context.Background(), dql.PoolConfig{DSN: dsn, MaxConns: 1, Setup: setup})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	c, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return p, c
}

func query(t *testing.T, c dql.Conn, q string, args ...any) []dql.Row {
	t.Helper()
	r, err := c.Query(context.Background(), q, args, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var out []dql.Row
	for r.Next() {
		out = append(out, r.Row())
	}
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func exec(t *testing.T, c dql.Conn, q string, args ...any) int64 {
	t.Helper()
	n, err := c.Exec(context.Background(), q, args, nil)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

type status string

func TestValues(t *testing.T) {
	_, c := open(t, database(t))
	at := time.Date(2024, 5, 6, 7, 8, 9, 500, time.FixedZone("x", 3600))
	day := civil.Date{Year: 2024, Month: 5, Day: 6}
	five := int64(5)
	var none *int64
	got := query(t, c, `SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?`,
		true, false, json.RawMessage(`{"a":1}`), dec.MustParse("1.50"), day,
		civil.DateTime{Date: day, Time: civil.Time{Hour: 1, Minute: 2, Second: 3}}, civil.Time{Hour: 4},
		civil.OffsetTime{Time: civil.Time{Hour: 4}, Offset: 3600}, civil.OffsetDateTime{DateTime: civil.DateTime{Date: day}, Offset: -3600},
		at, storage.Integer(1), storage.Real(1.5), storage.Text("t"), storage.Blob("b"),
		weak.Of[int64](7), weak.Raw[int64](storage.Text("seven")), &five, none, status("ok"), []byte{1}, 2.5)
	want := dql.Row{int64(1), int64(0), `{"a":1}`, "1.50", "2024-05-06",
		"2024-05-06T01:02:03", "04:00:00",
		civil.OffsetTime{Time: civil.Time{Hour: 4}, Offset: 3600}.String(), civil.OffsetDateTime{DateTime: civil.DateTime{Date: day}, Offset: -3600}.String(),
		"2024-05-06T06:08:09.0000005Z", int64(1), 1.5, "t", []byte("b"),
		int64(7), "seven", int64(5), nil, "ok", []byte{1}, 2.5}
	if !reflect.DeepEqual(got[0], want) {
		t.Errorf("got  %#v\nwant %#v", got[0], want)
	}
}

func TestLists(t *testing.T) {
	_, c := open(t, database(t))
	got := query(t, c, `SELECT value FROM json_each(?) ORDER BY key`,
		[]any{int64(1), "a", true, json.RawMessage(`[1]`), dec.MustParse("2.5"), nil})
	var vals []any
	for _, r := range got {
		vals = append(vals, r[0])
	}
	if fmt.Sprint(vals) != "[1 a 1 [1] 2.5 <nil>]" {
		t.Errorf("%#v", vals)
	}
	if _, err := c.Query(context.Background(), `SELECT ?`, []any{[]any{make(chan int)}}, nil); err == nil {
		t.Error("a list of what JSON cannot write was taken")
	}
	if _, err := c.Exec(context.Background(), `SELECT ?`, []any{[]any{make(chan int)}}, nil); err == nil {
		t.Error("a list of what JSON cannot write was taken")
	}
}

func TestDecodeTime(t *testing.T) {
	_, c := open(t, database(t))
	exec(t, c, `INSERT INTO loose (x, d) VALUES (1, '2024-05-06 07:08:09.5'), (2, '2024-05-06T07:08:09+02:00'), (3, 'not a time')`)
	var got []any
	for _, r := range query(t, c, `SELECT d FROM loose ORDER BY x`) {
		got = append(got, r[0])
	}
	if fmt.Sprint(got) != "[2024-05-06 07:08:09.5 2024-05-06 07:08:09+02:00 not a time]" {
		t.Errorf("%q", got)
	}
}

func TestTransactions(t *testing.T) {
	ctx := context.Background()
	dsn := database(t)
	p, c := open(t, dsn)
	if err := c.Begin(ctx, dql.TxOptions{}); err != nil {
		t.Fatal(err)
	}
	exec(t, c, `INSERT INTO users (email) VALUES ('a@x')`)
	for _, step := range []func(context.Context, string) error{c.Savepoint, c.RollbackTo, c.ReleaseSavepoint} {
		if err := step(ctx, `s"1`); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if n := query(t, c, `SELECT count(*) FROM users`)[0][0]; n != int64(1) {
		t.Error(n)
	}

	// A read-only transaction refuses writes, and the connection writes
	// again after it.
	if err := c.Begin(ctx, dql.TxOptions{ReadOnly: true, Isolation: dql.ReadCommitted}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Exec(ctx, `INSERT INTO users (email) VALUES ('b@x')`, nil, nil); err == nil {
		t.Error("a read-only transaction wrote")
	}
	if err := c.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	exec(t, c, `INSERT INTO users (email, name) VALUES ('b@x', 'b')`)

	// A rollback after SQLite ended the transaction succeeds.
	if err := c.Begin(ctx, dql.TxOptions{}); err != nil {
		t.Fatal(err)
	}
	exec(t, c, `ROLLBACK`)
	if err := c.Rollback(ctx); err != nil {
		t.Error(err)
	}
	// A commit with none open fails, and the connection is discarded.
	if err := c.Begin(ctx, dql.TxOptions{}); err != nil {
		t.Fatal(err)
	}
	exec(t, c, `ROLLBACK`)
	if err := c.Commit(ctx); err == nil {
		t.Error("committed")
	}
	if err := c.Rollback(ctx); err != nil {
		t.Error(err)
	}
	c.Release()

	// A second connection's write transaction waits for the first's write
	// lock and times out.
	c1, _ := p.Acquire(ctx)
	if err := c1.Begin(ctx, dql.TxOptions{}); err != nil {
		t.Fatal(err)
	}
	p2, err := New().Open(ctx, dql.PoolConfig{DSN: strings.Replace(dsn, "2000", "10", 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer p2.Close()
	c2, _ := p2.Acquire(ctx)
	err = c2.Begin(ctx, dql.TxOptions{})
	if n, ok := New().Native(err); !ok || n.Code != "5" {
		t.Errorf("%v %v", n, err)
	}
	// A read-only transaction that cannot begin leaves the connection
	// writable: an open one cannot begin another.
	if err := c1.Begin(ctx, dql.TxOptions{ReadOnly: true}); err == nil {
		t.Error("began within a transaction")
	}
	c1.Release() // discarded in its transaction
	c2.Release()
}

func TestReleaseReadOnly(t *testing.T) {
	ctx := context.Background()
	p, c := open(t, database(t))
	if err := c.Begin(ctx, dql.TxOptions{ReadOnly: true}); err != nil {
		t.Fatal(err)
	}
	c.(*conn).tx = false // as though it ended but query_only stayed on
	c.Release()
	c, _ = p.Acquire(ctx)
	exec(t, c, `INSERT INTO users (email) VALUES ('a@x')`)
	c.Release()

	// When query_only cannot be turned off, the end fails.
	c, _ = p.Acquire(ctx)
	if err := c.Begin(ctx, dql.TxOptions{ReadOnly: true}); err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := c.(*conn).writable(cctx); err == nil {
		t.Error("turned query_only off with a canceled context")
	}
	c.(*conn).tx, c.(*conn).readOnly = false, false
	if err := c.Begin(cctx, dql.TxOptions{ReadOnly: true}); err == nil {
		t.Error("began with a canceled context")
	}
	c.Release()
}

func TestErrors(t *testing.T) {
	ctx := context.Background()
	_, c := open(t, database(t))
	exec(t, c, `INSERT INTO users (id, email) VALUES (1, 'a@x')`)
	exec(t, c, `INSERT INTO pairs VALUES (1, 2)`)
	d := New()
	for _, x := range []struct {
		q    string
		want dql.NativeError
	}{
		{`INSERT INTO users (email, name) VALUES ('a@x', 'q')`, dql.NativeError{Code: "2067", Table: "users", Constraint: "email"}},
		{`INSERT INTO users (email) VALUES ('e@x')`, dql.NativeError{Code: "2067", Constraint: "users_lower_name"}},
		{`INSERT INTO users (id, email) VALUES (1, 'c@x')`, dql.NativeError{Code: "1555", Table: "users", Constraint: "id"}},
		{`INSERT INTO pairs VALUES (1, 2)`, dql.NativeError{Code: "2067", Table: "pairs", Constraint: "a, b"}},
		{`INSERT INTO users (email, name) VALUES (NULL, 'n')`, dql.NativeError{Code: "1299", Table: "users", Column: "email"}},
		{`INSERT INTO users (email, name, age) VALUES ('d@x', 'm', -1)`, dql.NativeError{Code: "275", Constraint: "users_age"}},
		{`INSERT INTO users (email, name) VALUES ('d@x', 'a long name')`, dql.NativeError{Code: "275", Constraint: "length(name) < 10"}},
		{`INSERT INTO posts (author) VALUES (9)`, dql.NativeError{Code: "787"}},
		{`SELECT nope`, dql.NativeError{Code: "1"}},
	} {
		_, err := c.Exec(ctx, x.q, nil, nil)
		n, ok := d.Native(err)
		n.Message = ""
		if !ok || n != x.want {
			t.Errorf("%s: %+v, want %+v (%v)", x.q, n, x.want, err)
		}
	}
	if _, ok := d.Native(errors.New("x")); ok {
		t.Error("a foreign error is native")
	}
	// Messages that do not have the form keep only their code.
	for _, code := range []int{1555, 1299, 275} {
		if n := native(code, "other"); n != (dql.NativeError{Code: fmt.Sprint(code), Message: "other"}) {
			t.Error(n)
		}
	}
	if n, _ := d.Native(fmt.Errorf("w: %w", &sqlite.Error{})); n.Code != "0" {
		t.Error(n)
	}
}

func TestBroken(t *testing.T) {
	d := New()
	if !d.Broken(driver.ErrBadConn) || !d.Broken(fmt.Errorf("x: %w", sql.ErrConnDone)) || d.Broken(errors.New("x")) {
		t.Error("Broken")
	}
}

func TestOpen(t *testing.T) {
	ctx := context.Background()
	// The setup runs on each connection, and a failing one fails it.
	p, err := New().Open(ctx, dql.PoolConfig{DSN: database(t), Setup: []string{"NOT SQL"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err := p.Acquire(ctx); err == nil {
		t.Error("acquired despite a failing setup")
	}
	// A database that cannot be opened.
	p2, _ := New().Open(ctx, dql.PoolConfig{DSN: "file:" + filepath.Join(t.TempDir(), "no", "such.db") + "?mode=ro"})
	defer p2.Close()
	if _, err := p2.Acquire(ctx); err == nil {
		t.Error("opened a missing database")
	}
}

func TestQueryErrors(t *testing.T) {
	ctx := context.Background()
	_, c := open(t, database(t))
	if _, err := c.Query(ctx, `SELECT nope`, nil, nil); err == nil {
		t.Error("queried")
	}
	if _, err := c.Exec(ctx, `SELECT nope`, nil, nil); err == nil {
		t.Error("ran")
	}
	if n := exec(t, c, `INSERT INTO users (email, name) VALUES (?, 'a'), (?, 'b')`, "a@x", "b@x"); n != 2 {
		t.Error(n)
	}
	if _, err := c.Begin(ctx, dql.TxOptions{}), c.Begin(ctx, dql.TxOptions{}); err == nil {
		t.Error("began twice")
	}
}
