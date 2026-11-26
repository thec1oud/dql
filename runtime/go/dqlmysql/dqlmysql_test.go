package dqlmysql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/thec1oud/dql/runtime/go/civil"
	"github.com/thec1oud/dql/runtime/go/dec"
	"github.com/thec1oud/dql/runtime/go/dql"
)

// The tests run against the MySQL 8.4 server DQL_TEST_MYSQL_URL names, in
// a fresh database each; they fail rather than skip without one, since
// they keep the package at full coverage.
const urlEnv = "DQL_TEST_MYSQL_URL"

// setup is the MySQL dialect's session setup.
var setup = []string{
	"SET time_zone = '+00:00'",
	"SET SESSION sql_mode = CONCAT_WS(',', @@SESSION.sql_mode, 'STRICT_ALL_TABLES')",
}

const schema = `
CREATE TABLE users (
	id bigint AUTO_INCREMENT PRIMARY KEY,
	email varchar(80) NOT NULL UNIQUE,
	name varchar(80) NOT NULL DEFAULT '',
	age int,
	CONSTRAINT users_age CHECK (age >= 0)
);
CREATE TABLE posts (
	id bigint AUTO_INCREMENT PRIMARY KEY,
	author bigint NOT NULL,
	CONSTRAINT posts_author FOREIGN KEY (author) REFERENCES users (id)
);
CREATE TABLE ` + "`all`" + ` (
	i8 tinyint, u8 tinyint unsigned, i16 smallint, u32 int unsigned, u64 bigint unsigned,
	f32 float, f64 double, d decimal(10,3),
	c char(3), v varchar(10), t text, e enum('sad','happy'), s set('a','b'),
	b varbinary(4), bl blob, j json, bt bit(10), y year,
	dt date, ts datetime(6), tz timestamp(6) NULL, tm time(6)
);
`

// database creates a fresh database with the test schema and returns its
// DSN; the database is dropped when the test ends.
func database(t *testing.T) string {
	t.Helper()
	base := os.Getenv(urlEnv)
	if base == "" {
		t.Fatal(urlEnv + " is not set; these tests need a MySQL 8.4 server, such as root:root@tcp(localhost:3306)/")
	}
	admin, err := sql.Open("mysql", base)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("dqlmysql_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin.Exec("DROP DATABASE " + name)
		admin.Close()
	})
	cfg, _ := mysql.ParseDSN(base)
	cfg.DBName = name
	cfg.MultiStatements = true
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	cfg.MultiStatements = false
	return cfg.FormatDSN()
}

func open(t *testing.T, dsn string, mode dql.StatementMode) (dql.Pool, dql.Conn) {
	t.Helper()
	p, err := New().Open(context.Background(), dql.PoolConfig{DSN: dsn, MaxConns: 1, Setup: setup, Mode: mode})
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

type mood string

func TestValues(t *testing.T) {
	dsn := database(t)
	for _, mode := range []dql.StatementMode{dql.ModePrepare, dql.ModeSimple} {
		_, c := open(t, dsn, mode)
		exec(t, c, "DELETE FROM `all`")
		bits, _ := dql.ParseBits("1000000001")
		ts := time.Date(2024, 5, 6, 7, 8, 9, 123456000, time.UTC)
		age := int64(3)
		var none *int64
		exec(t, c, "INSERT INTO `all` VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			int64(-5), uint64(200), &age, none, uint64(1<<63+1),
			float32(1.5), 2.25, dec.MustParse("12.345"),
			"abc", "v", "text", mood("happy"), "a,b",
			[]byte{1, 2}, []byte("blob"), json.RawMessage(`{"a": 1}`), bits, "2024",
			civil.DateOf(ts), civil.DateTimeOf(ts), civil.OffsetDateTimeOf(ts.In(time.FixedZone("", 3600))),
			dql.Interval{Days: 1, Microseconds: -1_500_000})
		got := query(t, c, "SELECT * FROM `all`")
		want := dql.Row{
			int64(-5), int64(200), int64(3), nil, uint64(1<<63 + 1),
			1.5, 2.25, dec.MustParse("12.345"),
			"abc", "v", "text", "happy", "a,b",
			[]byte{1, 2}, []byte("blob"), json.RawMessage(`{"a": 1}`), mustBits(t, "0000001000000001"), "2024",
			civil.DateOf(ts), civil.DateTimeOf(ts), ts,
			dql.Interval{Microseconds: 86_400_000_000 - 1_500_000},
		}
		if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
			t.Errorf("mode %v:\n got %#v\nwant %#v", mode, got, want)
		}
		// A TIME holds a duration past a day, and a time of day.
		exec(t, c, "DELETE FROM `all`")
		exec(t, c, "INSERT INTO `all` (tm, ts) VALUES (?, ?), (?, NULL)",
			dql.Interval{Microseconds: -(838*3600 + 59*60 + 59) * 1_000_000}, civil.DateTime{Date: civil.Date{Year: 2024, Month: 1, Day: 2}},
			civil.Time{Hour: 1, Minute: 2, Second: 3})
		r := query(t, c, "SELECT tm, ts FROM `all` ORDER BY tm")
		if want := []dql.Row{{dql.Interval{Microseconds: -(838*3600 + 59*60 + 59) * 1_000_000}, civil.DateTime{Date: civil.Date{Year: 2024, Month: 1, Day: 2}}}, {dql.Interval{Microseconds: 3723_000_000}, nil}}; !reflect.DeepEqual(r, want) {
			t.Errorf("mode %v: times %#v", mode, r)
		}
		c.Release()
	}
}

func mustBits(t *testing.T, s string) dql.Bits {
	b, err := dql.ParseBits(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestLists(t *testing.T) {
	dsn := database(t)
	for _, mode := range []dql.StatementMode{dql.ModePrepare, dql.ModeSimple} {
		_, c := open(t, dsn, mode)
		exec(t, c, "DELETE FROM users")
		exec(t, c, "INSERT INTO users (email, name) VALUES ('a', 'x?'), ('b', 'y'), ('c', 'z')")
		got := query(t, c, "SELECT email /* ? */ FROM users WHERE email IN (?) AND name <> 'it''s ?' AND name <> \"\\\" ?\" # ?\n AND id > ? ORDER BY email", []string{"a", "c"}, int64(0))
		if !reflect.DeepEqual(got, []dql.Row{{"a"}, {"c"}}) {
			t.Errorf("mode %v: list %v", mode, got)
		}
		if got := query(t, c, "SELECT email FROM users WHERE email IN (?)", []string{}); len(got) != 0 {
			t.Errorf("mode %v: empty list %v", mode, got)
		}
		if got := query(t, c, "SELECT email FROM users WHERE email NOT IN (?) -- ?\n ORDER BY email", []mood{"b"}); !reflect.DeepEqual(got, []dql.Row{{"a"}, {"c"}}) {
			t.Errorf("mode %v: enum list %v", mode, got)
		}
		c.Release()
	}
}

func TestStatement(t *testing.T) {
	for _, tc := range []struct {
		q    string
		args []any
		want string
		vals []any
	}{
		{"a ? b", []any{int64(1)}, "a ? b", []any{int64(1)}},
		{"a IN (?)", []any{[]int64{1, 2}}, "a IN (?, ?)", []any{int64(1), int64(2)}},
		{"a IN (?) ?", []any{[]int64{}}, "a IN (NULL) ?", nil},
		{"'?' `?` \"?\" ? -- ?", []any{[]int64{1}}, "'?' `?` \"?\" ? -- ?", []any{int64(1)}},
		{"/* ? ? IN (?)", []any{[]int64{1}}, "/* ? ? IN (?)", nil},
		{"# ? ? IN (?)", []any{[]int64{1}}, "# ? ? IN (?)", nil},
		{"'a\\'?' ``?`` ?", []any{[]int64{1}}, "'a\\'?' ``?`` ?", []any{int64(1)}},
		{"x--? ?", []any{[]int64{1}}, "x--? ?", []any{int64(1)}},
		{"'?", []any{[]int64{1}}, "'?", nil},
	} {
		q, vals, err := statement(tc.q, tc.args)
		if err != nil || q != tc.want || !reflect.DeepEqual(vals, tc.vals) {
			t.Errorf("%q: %q %v %v", tc.q, q, vals, err)
		}
	}
	big := mustBits(t, strings.Repeat("1", 65))
	for _, args := range [][]any{{dql.Interval{Months: 1}}, {[]any{big}}} {
		if _, _, err := statement("?", args); err == nil {
			t.Errorf("%v encoded", args)
		}
	}
	if _, _, err := statement("?", []any{dql.Interval{Months: 1}}); !errors.Is(err, ErrMonths) {
		t.Error(err)
	}
}

func TestEncode(t *testing.T) {
	ts := time.Date(2024, 5, 6, 7, 8, 9, 0, time.FixedZone("", 3600))
	d := dec.MustParse("1.50")
	for _, tc := range []struct{ in, want any }{
		{civil.OffsetTime{Time: civil.Time{Hour: 1}, Offset: 3600}, "01:00:00+01:00"},
		{civil.OffsetDateTimeOf(ts), ts.UTC()},
		{&d, "1.50"},
		{dql.Interval{Microseconds: 1}, "0:00:00.000001"},
		{mustBits(t, ""), uint64(0)},
		{mustBits(t, "101"), uint64(5)},
	} {
		got, err := encode(tc.in)
		if g, ok := got.(time.Time); ok {
			got = g.UTC()
		}
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%v: %#v %v", tc.in, got, err)
		}
	}
}

func TestDecode(t *testing.T) {
	for _, tc := range []struct {
		typ  string
		in   any
		want any
	}{
		{"YEAR", int64(2024), "2024"},
		{"GEOMETRY", []byte{1}, []byte{1}},
		{"VECTOR", []byte{1}, []byte{1}},
		{"TIME", []byte("12:00:00"), dql.Interval{Microseconds: 43_200_000_000}},
		{"BOOL", true, true},
	} {
		if got, err := decode(tc.typ, tc.in); err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s %v: %#v %v", tc.typ, tc.in, got, err)
		}
	}
	for _, tc := range []struct{ typ, in string }{
		{"UNSIGNED BIGINT", "x"}, {"DECIMAL", "x"}, {"TIME", "1:2"}, {"TIME", "1:2:x"}, {"TIME", "1:-2:3"}, {"TIME", "1:2:3.1234567"},
	} {
		if _, err := decode(tc.typ, []byte(tc.in)); err == nil {
			t.Errorf("%s %q decoded", tc.typ, tc.in)
		}
	}
}

func TestExecID(t *testing.T) {
	_, c := open(t, database(t), dql.ModePrepare)
	ctx := context.Background()
	ic := c.(dql.InsertIDConn)
	n, id, err := ic.ExecID(ctx, "INSERT INTO users (email) VALUES (?)", []any{"a"}, nil)
	if err != nil || n != 1 || id != 1 {
		t.Fatalf("insert %d %d %v", n, id, err)
	}
	// Found rows: an update that changes nothing still counts its row.
	if n := exec(t, c, "UPDATE users SET name = name WHERE id = ?", int64(1)); n != 1 {
		t.Errorf("matched %d", n)
	}
	if _, _, err := ic.ExecID(ctx, "INSERT INTO nope VALUES (1)", nil, nil); err == nil {
		t.Error("no error")
	}
	if _, _, err := ic.ExecID(ctx, "?", []any{dql.Interval{Months: 1}}, nil); err == nil {
		t.Error("encoded")
	}
	if _, err := c.Query(ctx, "SELECT * FROM nope", nil, nil); err == nil {
		t.Error("no error")
	}
	if _, err := c.Query(ctx, "?", []any{dql.Interval{Months: 1}}, nil); err == nil {
		t.Error("encoded")
	}
}

func TestDecodeFailure(t *testing.T) {
	_, c := open(t, database(t), dql.ModePrepare)
	defer func(d func(string, any) (any, error)) { decodeValue = d }(decodeValue)
	decodeValue = func(string, any) (any, error) { return nil, errors.New("bad") }
	r, err := c.Query(context.Background(), "SELECT 1", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	first := r.Next()
	if first || r.Next() || r.Err() == nil {
		t.Error("decoded")
	}
	r.Close()
}

func connID(t *testing.T, c dql.Conn) int64 {
	t.Helper()
	id, err := dql.Int[int64](query(t, c, "SELECT CONNECTION_ID()")[0][0])
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestTransactions(t *testing.T) {
	p, c := open(t, database(t), dql.ModePrepare)
	ctx := context.Background()
	for _, err := range []error{
		c.Begin(ctx, dql.TxOptions{Isolation: dql.Serializable}),
		c.Savepoint(ctx, "s`1"),
		exec1(c, "INSERT INTO users (email) VALUES ('a')"),
		c.RollbackTo(ctx, "s`1"),
		c.ReleaseSavepoint(ctx, "s`1"),
		exec1(c, "INSERT INTO users (email) VALUES ('b')"),
		c.Commit(ctx),
		c.Begin(ctx, dql.TxOptions{ReadOnly: true}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := query(t, c, "SELECT email, @@transaction_isolation FROM users"); !reflect.DeepEqual(got, []dql.Row{{"b", "REPEATABLE-READ"}}) {
		t.Errorf("rows %v", got)
	}
	if err := exec1(c, "INSERT INTO users (email) VALUES ('c')"); err == nil {
		t.Error("read only wrote")
	}
	if err := c.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	// A connection released in a transaction is discarded.
	id := connID(t, c)
	c.Release()
	c, _ = p.Acquire(ctx)
	if connID(t, c) != id {
		t.Error("a connection was discarded outside a transaction")
	}
	c.Begin(ctx, dql.TxOptions{})
	c.Release()
	c, _ = p.Acquire(ctx)
	if connID(t, c) == id {
		t.Error("a connection in a transaction was kept")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if c.Begin(ctx, dql.TxOptions{Isolation: "bogus"}) == nil || c.Begin(canceled, dql.TxOptions{}) == nil || c.Commit(canceled) == nil {
		t.Error("no error")
	}
	c.Release()
}

func exec1(c dql.Conn, q string) error {
	_, err := c.Exec(context.Background(), q, nil, nil)
	return err
}

func TestErrors(t *testing.T) {
	_, c := open(t, database(t), dql.ModePrepare)
	d := New()
	exec(t, c, "INSERT INTO users (email) VALUES ('a')")
	for q, want := range map[string]dql.NativeError{
		"INSERT INTO users (email) VALUES ('a')":          {Code: "1062", Table: "users", Constraint: "email", Message: "Duplicate entry 'a' for key 'users.email'", Detail: "Duplicate entry 'a' for key 'users.email'"},
		"INSERT INTO posts (author) VALUES (9)":           {Code: "1452", Table: "posts", Constraint: "posts_author"},
		"INSERT INTO users (email, age) VALUES ('b', -1)": {Code: "3819", Constraint: "users_age", Message: "Check constraint 'users_age' is violated."},
		"INSERT INTO users (email) VALUES (NULL)":         {Code: "1048", Column: "email", Message: "Column 'email' cannot be null"},
		"INSERT INTO users (name) VALUES ('x')":           {Code: "1364", Column: "email", Message: "Field 'email' doesn't have a default value"},
	} {
		err := exec1(c, q)
		n, ok := d.Native(err)
		if want.Message == "" {
			n.Message = ""
		}
		if !ok || n != want {
			t.Errorf("%s: %#v", q, n)
		}
		if d.Broken(err) {
			t.Errorf("%s: broken", q)
		}
	}
	if _, ok := d.Native(errors.New("x")); ok {
		t.Error("native")
	}
}

func TestNative(t *testing.T) {
	for _, tc := range []struct {
		number  uint16
		message string
		want    dql.NativeError
	}{
		{1062, "Duplicate entry 'a' for key 'email'", dql.NativeError{Constraint: "email"}},
		{1062, "Duplicate entry", dql.NativeError{}},
		{1451, "Cannot delete or update a parent row: a foreign key constraint fails (`d``b`.`po``sts`, CONSTRAINT `f``k` FOREIGN KEY (`a`) REFERENCES `u` (`id`))", dql.NativeError{Table: "po`sts", Constraint: "f`k"}},
		{1451, "Cannot delete", dql.NativeError{}},
		{3819, "nope", dql.NativeError{}},
		{4025, "CONSTRAINT `c` failed for `db`.`t`", dql.NativeError{Table: "t", Constraint: "c"}},
		{4025, "nope", dql.NativeError{}},
		{1263, "Column set to default value; NULL supplied to NOT NULL column 'it''s' at row 1", dql.NativeError{Column: "it's"}},
		{1048, "nope", dql.NativeError{}},
	} {
		got := native(tc.number, tc.message)
		got.Code, got.Message, got.Detail = "", "", ""
		if got != tc.want {
			t.Errorf("%d %s: %#v", tc.number, tc.message, got)
		}
	}
}

func TestBroken(t *testing.T) {
	d := New()
	for _, err := range []error{&net.OpError{Op: "dial", Err: errors.New("x")}, driver.ErrBadConn, mysql.ErrInvalidConn, sql.ErrConnDone, io.EOF, io.ErrUnexpectedEOF, net.ErrClosed} {
		if !d.Broken(fmt.Errorf("w: %w", err)) {
			t.Errorf("%v not broken", err)
		}
	}
	if d.Broken(errors.New("x")) {
		t.Error("broken")
	}
}

func TestOpen(t *testing.T) {
	ctx := context.Background()
	if _, err := New().Open(ctx, dql.PoolConfig{DSN: "bad dsn"}); err == nil {
		t.Error("bad DSN opened")
	}
	defer func(f func(*mysql.Config) (driver.Connector, error)) { newConnector = f }(newConnector)
	newConnector = func(*mysql.Config) (driver.Connector, error) { return nil, errors.New("no") }
	if _, err := New().Open(ctx, dql.PoolConfig{DSN: "root@tcp(localhost:1)/"}); err == nil {
		t.Error("opened")
	}
}

func TestAcquire(t *testing.T) {
	ctx := context.Background()
	p, err := New().Open(ctx, dql.PoolConfig{DSN: "root@tcp(127.0.0.1:1)/?timeout=1s"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err := p.Acquire(ctx); err == nil || !New().Broken(err) {
		t.Errorf("connected: %v", err)
	}
	p, err = New().Open(ctx, dql.PoolConfig{DSN: database(t), Setup: []string{"SET nope = 1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err := p.Acquire(ctx); err == nil {
		t.Error("setup did not fail")
	}
}
