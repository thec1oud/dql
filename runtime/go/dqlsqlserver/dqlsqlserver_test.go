package dqlsqlserver

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	mssql "github.com/microsoft/go-mssqldb"

	"github.com/thec1oud/dql/runtime/go/civil"
	"github.com/thec1oud/dql/runtime/go/dec"
	"github.com/thec1oud/dql/runtime/go/dql"
)

// The tests run against the SQL Server 2022 server DQL_TEST_SQLSERVER_URL
// names, in a fresh database each; they fail rather than skip without one,
// since they keep the package at full coverage.
const urlEnv = "DQL_TEST_SQLSERVER_URL"

// setup is the sqlserver dialect's session setup.
var setup = []string{"SET ANSI_NULLS ON", "SET QUOTED_IDENTIFIER ON", "SET ARITHABORT ON"}

var schema = []string{
	`CREATE SCHEMA app`,
	`CREATE TABLE app.users (
		id bigint IDENTITY PRIMARY KEY,
		email nvarchar(80) NOT NULL CONSTRAINT users_email UNIQUE,
		name nvarchar(80) NOT NULL DEFAULT '',
		age int,
		CONSTRAINT users_age CHECK (age >= 0)
	)`,
	`CREATE UNIQUE INDEX users_name ON app.users (name) WHERE name <> ''`,
	`CREATE TABLE app.posts (
		id bigint IDENTITY PRIMARY KEY,
		author bigint NOT NULL CONSTRAINT posts_author FOREIGN KEY REFERENCES app.users (id)
	)`,
	`CREATE TABLE app.[all] (
		u8 tinyint, i16 smallint, i32 int, i64 bigint, b bit,
		f32 real, f64 float, d decimal(10,3), m money,
		c nchar(3), v nvarchar(10), t nvarchar(max), x xml,
		bin varbinary(4), id uniqueidentifier,
		dt date, tm time(7), ts datetime2(7), old datetime, tz datetimeoffset(7)
	)`,
}

// database creates a fresh database with the test schema and returns its
// DSN; the database is dropped when the test ends.
func database(t *testing.T) string {
	t.Helper()
	base := os.Getenv(urlEnv)
	if base == "" {
		t.Fatal(urlEnv + " is not set; these tests need a SQL Server 2022 server, such as sqlserver://sa:password@localhost:1433")
	}
	admin, err := sql.Open("sqlserver", base)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("dqlsqlserver_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin.Exec("ALTER DATABASE " + name + " SET SINGLE_USER WITH ROLLBACK IMMEDIATE")
		admin.Exec("DROP DATABASE " + name)
		admin.Close()
	})
	u, _ := url.Parse(base)
	q := u.Query()
	q.Set("database", name)
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlserver", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, s := range schema {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	return u.String()
}

func open(t *testing.T, dsn string) (dql.Pool, dql.Conn) {
	t.Helper()
	p, err := New().Open(context.Background(), dql.PoolConfig{DSN: dsn, MaxConns: 1, Setup: setup, Mode: dql.ModePrepare})
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
	_, c := open(t, database(t))
	ts := time.Date(2024, 5, 6, 7, 8, 9, 123456700, time.UTC)
	tz := ts.In(time.FixedZone("", 5400))
	id := uuid.MustParse("6f9619ff-8b86-d011-b42d-00c04fc964ff")
	age := int64(3)
	var none *int64
	exec(t, c, "INSERT INTO app.[all] VALUES (@p1, @p2, @p3, @p4, @p5, @p6, @p7, @p8, @p9, @p10, @p11, @p12, @p13, @p14, @p15, @p16, @p17, @p18, @p19, @p20)",
		int64(200), &age, none, int64(math.MaxInt64), true,
		float32(1.5), 2.25, dec.MustParse("12.345"), dec.MustParse("-1.5"),
		"abc", mood("happy"), "text", "<a/>",
		[]byte{1, 2}, id,
		civil.DateOf(ts), civil.TimeOf(ts), civil.DateTimeOf(ts), civil.DateTimeOf(ts.Truncate(time.Second)), civil.OffsetDateTimeOf(tz))
	got := query(t, c, "SELECT * FROM app.[all]")
	want := dql.Row{
		int64(200), int64(3), nil, int64(math.MaxInt64), true,
		1.5, 2.25, dec.MustParse("12.345"), dec.MustParse("-1.5000"),
		"abc", "happy", "text", "<a/>",
		[]byte{1, 2}, id,
		civil.DateOf(ts), civil.TimeOf(ts), civil.DateTimeOf(ts), civil.DateTimeOf(ts.Truncate(time.Second)), civil.OffsetDateTimeOf(tz),
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Errorf("\n got %#v\nwant %#v", got, want)
	}
	// A JSON value reads back as JSON text.
	if got := query(t, c, "SELECT JSON_QUERY(@p1)", json.RawMessage(`{"a":1}`)); fmt.Sprint(got) != `[[{"a":1}]]` {
		t.Errorf("json %v", got)
	}
}

// inList is how the dialect compares a column with a list (§25.3).
func inList(col, typ string) string {
	return col + " IN (SELECT [v] FROM OPENJSON(@p1) WITH ([v] " + typ + " '$'))"
}

func TestLists(t *testing.T) {
	_, c := open(t, database(t))
	exec(t, c, "INSERT INTO app.users (email, name) VALUES (N'a', N'x'), (N'B', N'y'), (N'c', N'z')")
	text := "nvarchar(80) COLLATE SQL_Latin1_General_CP1_CI_AS"
	if got := query(t, c, "SELECT email FROM app.users WHERE "+inList("email", text)+" ORDER BY email", []string{"a", "b"}); !reflect.DeepEqual(got, []dql.Row{{"a"}, {"B"}}) {
		t.Errorf("list under the collation %v", got)
	}
	if got := query(t, c, "SELECT email FROM app.users WHERE "+inList("email", text), []string{}); len(got) != 0 {
		t.Errorf("empty list %v", got)
	}
	if got := query(t, c, "SELECT id FROM app.users WHERE "+inList("id", "bigint")+" ORDER BY id", []int64{3, 1, 9}); !reflect.DeepEqual(got, []dql.Row{{int64(1)}, {int64(3)}}) {
		t.Errorf("integers %v", got)
	}
	if got := query(t, c, "SELECT email FROM app.users WHERE "+inList("email", text), []mood{"c"}); !reflect.DeepEqual(got, []dql.Row{{"c"}}) {
		t.Errorf("enum list %v", got)
	}
	// Each type's items read as that type.
	ts := time.Date(2024, 5, 6, 7, 8, 9, 123456700, time.UTC)
	id := uuid.MustParse("6f9619ff-8b86-d011-b42d-00c04fc964ff")
	for _, tc := range []struct {
		typ  string
		item any
		want any
	}{
		{"uniqueidentifier", id, id},
		{"date", civil.DateOf(ts), civil.DateOf(ts)},
		{"time(7)", civil.TimeOf(ts), civil.TimeOf(ts)},
		{"datetime2(7)", civil.DateTimeOf(ts), civil.DateTimeOf(ts)},
		{"datetimeoffset(7)", civil.OffsetDateTimeOf(ts.In(time.FixedZone("", -3600))), civil.OffsetDateTimeOf(ts.In(time.FixedZone("", -3600)))},
		{"datetimeoffset(7)", ts, civil.OffsetDateTimeOf(ts)},
		{"decimal(10,3)", dec.MustParse("1.5"), dec.MustParse("1.500")},
	} {
		got := query(t, c, "SELECT [v] FROM OPENJSON(@p1) WITH ([v] "+tc.typ+" '$')", []any{tc.item})
		if len(got) != 1 || !reflect.DeepEqual(got[0][0], tc.want) {
			t.Errorf("%s: %#v", tc.typ, got)
		}
	}
}

func TestList(t *testing.T) {
	ts := time.Date(2024, 5, 6, 7, 8, 9, 500000000, time.FixedZone("", 3600))
	for _, tc := range []struct {
		in   any
		want string
	}{
		{[]int64{1, 2}, "[1,2]"},
		{[]string{}, "[]"},
		{[]*int64{nil}, "[null]"},
		{[]any{dec.MustParse("1.50"), civil.Date{Year: 2024, Month: 1, Day: 2}, ts, json.RawMessage(`{"a":1}`)},
			`["1.50","2024-01-02","2024-05-06T07:08:09.5+01:00",{"a":1}]`},
	} {
		got, err := arguments([]any{tc.in})
		if err != nil || got[0] != tc.want {
			t.Errorf("%v: %v %v", tc.in, got, err)
		}
	}
	for _, args := range [][]any{{dql.Interval{Months: 1}}, {[]any{dql.Interval{}}}, {[]float64{math.NaN()}}} {
		if _, err := arguments(args); err == nil {
			t.Errorf("%v encoded", args)
		}
	}
	if got, _ := arguments([]any{[]byte("x"), json.RawMessage("1")}); !reflect.DeepEqual(got, []any{[]byte("x"), "1"}) {
		t.Errorf("bytes and JSON are no lists: %v", got)
	}
}

func TestEncode(t *testing.T) {
	d := dec.MustParse("1.50")
	for _, in := range []any{dql.Interval{}, dql.Bits{}, civil.OffsetTime{}} {
		if _, err := encode(in); !errors.Is(err, ErrNoType) {
			t.Errorf("%v: %v", in, err)
		}
	}
	if got, err := encode(&d); err != nil || got != "1.50" {
		t.Errorf("%#v %v", got, err)
	}
	if got, _ := encode(uuid.Nil); got != (mssql.UniqueIdentifier{}) {
		t.Errorf("%#v", got)
	}
}

func TestDecode(t *testing.T) {
	for _, tc := range []struct {
		typ  string
		in   any
		want any
	}{
		{"JSON", `{"a":1}`, json.RawMessage(`{"a":1}`)},
		{"JSON", []byte(`{"a":1}`), json.RawMessage(`{"a":1}`)},
		{"NVARCHAR", "x", "x"},
		{"SMALLDATETIME", time.Date(2024, 1, 2, 3, 4, 0, 0, time.UTC), civil.DateTime{Date: civil.Date{Year: 2024, Month: 1, Day: 2}, Time: civil.Time{Hour: 3, Minute: 4}}},
	} {
		if got, err := decode(tc.typ, tc.in); err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s %v: %#v %v", tc.typ, tc.in, got, err)
		}
	}
	for _, tc := range []struct{ typ, in string }{{"DECIMAL", "x"}, {"UNIQUEIDENTIFIER", "x"}} {
		if _, err := decode(tc.typ, []byte(tc.in)); err == nil {
			t.Errorf("%s %q decoded", tc.typ, tc.in)
		}
	}
}

// A write that outputs its rows into a table variable returns the rows the
// batch then selects, on a table with a trigger too.
func TestOutputBatch(t *testing.T) {
	_, c := open(t, database(t))
	exec(t, c, `CREATE TRIGGER app.users_touch ON app.users AFTER UPDATE AS BEGIN SET NOCOUNT ON; UPDATE u SET age = 1 FROM app.users u JOIN inserted i ON u.id = i.id; END`)
	got := query(t, c, "DECLARE @dql_out TABLE (id bigint, email nvarchar(80)); INSERT INTO app.users (email) OUTPUT INSERTED.id, INSERTED.email INTO @dql_out VALUES (@p1), (@p2); SELECT id, email FROM @dql_out ORDER BY id;", "a", "b")
	if !reflect.DeepEqual(got, []dql.Row{{int64(1), "a"}, {int64(2), "b"}}) {
		t.Errorf("insert %v", got)
	}
	if n := exec(t, c, "DECLARE @dql_out TABLE (id bigint); UPDATE app.users SET name = name OUTPUT INSERTED.id INTO @dql_out WHERE email = @p1;", "a"); n != 1 {
		t.Errorf("an update that changes nothing matched %d", n)
	}
}

func TestQueryFailures(t *testing.T) {
	_, c := open(t, database(t))
	ctx := context.Background()
	if _, err := c.Exec(ctx, "INSERT INTO nope VALUES (1)", nil, nil); err == nil {
		t.Error("no error")
	}
	if _, err := c.Exec(ctx, "@p1", []any{dql.Interval{}}, nil); err == nil {
		t.Error("encoded")
	}
	if _, err := c.Query(ctx, "SELECT * FROM nope", nil, nil); err == nil {
		t.Error("no error")
	}
	if _, err := c.Query(ctx, "@p1", []any{dql.Interval{}}, nil); err == nil {
		t.Error("encoded")
	}
}

func TestDecodeFailure(t *testing.T) {
	_, c := open(t, database(t))
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

// connID is the connection's own ID; a session ID is reused.
func connID(t *testing.T, c dql.Conn) string {
	t.Helper()
	return query(t, c, "SELECT CONVERT(nvarchar(36), connection_id) FROM sys.dm_exec_connections WHERE session_id = @@SPID")[0][0].(string)
}

// isolation is the session's isolation level, as sys.dm_exec_sessions
// numbers it: 2 is READ COMMITTED and 4 SERIALIZABLE.
func isolation(t *testing.T, c dql.Conn) int64 {
	t.Helper()
	n, err := dql.Int[int64](query(t, c, "SELECT CAST(transaction_isolation_level AS bigint) FROM sys.dm_exec_sessions WHERE session_id = @@SPID")[0][0])
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTransactions(t *testing.T) {
	p, c := open(t, database(t))
	ctx := context.Background()
	for _, err := range []error{
		c.Begin(ctx, dql.TxOptions{Isolation: dql.Serializable, ReadOnly: true}),
		c.Savepoint(ctx, "s]1"),
		exec1(c, "INSERT INTO app.users (email) VALUES ('a')"),
		c.RollbackTo(ctx, "s]1"),
		c.ReleaseSavepoint(ctx, "s]1"),
		exec1(c, "INSERT INTO app.users (email) VALUES ('b')"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if isolation(t, c) != 4 {
		t.Error("not serializable")
	}
	if err := c.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	// The level holds for the transaction alone.
	if got := query(t, c, "SELECT email FROM app.users"); !reflect.DeepEqual(got, []dql.Row{{"b"}}) || isolation(t, c) != 2 {
		t.Errorf("rows %v", got)
	}
	if err := c.Begin(ctx, dql.TxOptions{Isolation: dql.RepeatableRead}); err != nil {
		t.Fatal(err)
	}
	if err := c.Rollback(ctx); err != nil || isolation(t, c) != 2 {
		t.Fatal(err)
	}
	// A rollback after the server ended the transaction succeeds.
	if err := c.Rollback(ctx); err != nil {
		t.Error(err)
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
	if c.Begin(ctx, dql.TxOptions{Isolation: "bogus"}) == nil {
		t.Error("a bogus isolation level began")
	}
	// A done context fails each statement unsent, and the connection stays
	// good.
	id = connID(t, c)
	for i, err := range []error{c.Begin(canceled, dql.TxOptions{}), c.Commit(canceled), c.Rollback(canceled)} {
		if !errors.Is(err, context.Canceled) {
			t.Errorf("call %d: %v", i, err)
		}
	}
	if connID(t, c) != id {
		t.Error("a done context broke the connection")
	}
	c.Release()
}

// A rollback on a connection that broke succeeds: the server rolled the
// transaction back as the connection ended. The connection is discarded.
func TestRollbackBroken(t *testing.T) {
	p, c := open(t, database(t))
	ctx := context.Background()
	if err := c.Begin(ctx, dql.TxOptions{}); err != nil {
		t.Fatal(err)
	}
	id := connID(t, c)
	session, err := dql.Int[int64](query(t, c, "SELECT CAST(@@SPID AS bigint)")[0][0])
	if err != nil {
		t.Fatal(err)
	}
	admin, err := sql.Open("sqlserver", os.Getenv(urlEnv))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.Exec(fmt.Sprintf("KILL %d", session)); err != nil {
		t.Fatal(err)
	}
	if err := c.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	c.Release()
	c, _ = p.Acquire(ctx)
	defer c.Release()
	if connID(t, c) == id {
		t.Error("a broken connection was kept")
	}
}

func exec1(c dql.Conn, q string) error {
	_, err := c.Exec(context.Background(), q, nil, nil)
	return err
}

func TestErrors(t *testing.T) {
	_, c := open(t, database(t))
	d := New()
	exec(t, c, "INSERT INTO app.users (email, name) VALUES ('a', 'x')")
	dup := "Violation of UNIQUE KEY constraint 'users_email'. Cannot insert duplicate key in object 'app.users'. The duplicate key value is (a)."
	index := "Cannot insert duplicate key row in object 'app.users' with unique index 'users_name'. The duplicate key value is (x)."
	for q, want := range map[string]dql.NativeError{
		"INSERT INTO app.users (email) VALUES ('a')":              {Code: "2627", Schema: "app", Table: "users", Constraint: "users_email", Message: dup, Detail: dup},
		"INSERT INTO app.users (email, name) VALUES ('b', 'x')":   {Code: "2601", Schema: "app", Table: "users", Constraint: "users_name", Message: index, Detail: index},
		"INSERT INTO app.posts (author) VALUES (9)":               {Code: "547", Constraint: "posts_author"},
		"INSERT INTO app.users (email, age) VALUES ('c', -1)":     {Code: "547/CHECK", Schema: "app", Table: "users", Constraint: "users_age"},
		"INSERT INTO app.users (email) VALUES (NULL)":             {Code: "515", Schema: "app", Table: "users", Column: "email"},
		"INSERT INTO app.posts (author) SELECT id FROM app.users": {},
	} {
		err := exec1(c, q)
		if want.Code == "" {
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
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
	// A delete of a referenced row names the constraint alone.
	n, _ := d.Native(exec1(c, "DELETE FROM app.users"))
	if n.Code != "547" || n.Constraint != "posts_author" || n.Table != "" {
		t.Errorf("delete %#v", n)
	}
	if _, ok := d.Native(errors.New("x")); ok {
		t.Error("native")
	}
}

func TestNative(t *testing.T) {
	for _, tc := range []struct {
		number  int32
		message string
		want    dql.NativeError
	}{
		{2627, "Violation of PRIMARY KEY constraint 'it''s'. Cannot insert duplicate key in object 'odd'. The duplicate key value is (1).", dql.NativeError{Table: "odd", Constraint: "it's"}},
		{2627, "nope", dql.NativeError{}},
		{2601, "nope", dql.NativeError{}},
		{547, "nope", dql.NativeError{}},
		{515, "nope", dql.NativeError{}},
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
	for _, err := range []error{&net.OpError{Op: "dial", Err: errors.New("x")}, driver.ErrBadConn, sql.ErrConnDone, io.EOF, io.ErrUnexpectedEOF, net.ErrClosed} {
		if !d.Broken(fmt.Errorf("w: %w", err)) {
			t.Errorf("%v not broken", err)
		}
	}
	if d.Broken(errors.New("x")) {
		t.Error("broken")
	}
}

func TestOpen(t *testing.T) {
	if _, err := New().Open(context.Background(), dql.PoolConfig{DSN: "sqlserver://h?dial+timeout=x"}); err == nil {
		t.Error("bad DSN opened")
	}
}

// The setup runs on a new connection, and again on one the pool resets.
func TestAcquire(t *testing.T) {
	ctx := context.Background()
	p, err := New().Open(ctx, dql.PoolConfig{DSN: "sqlserver://sa:x@127.0.0.1:1?dial+timeout=1"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err := p.Acquire(ctx); err == nil || !New().Broken(err) {
		t.Errorf("connected: %v", err)
	}
	dsn := database(t)
	p, err = New().Open(ctx, dql.PoolConfig{DSN: dsn, Setup: []string{"SET nope ON"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err := p.Acquire(ctx); err == nil {
		t.Error("setup did not fail")
	}
	p, c := open(t, dsn)
	options := func() int64 {
		n, _ := dql.Int[int64](query(t, c, "SELECT CAST(@@OPTIONS AS bigint)")[0][0])
		return n
	}
	exec(t, c, "SET ARITHABORT OFF")
	c.Release()
	c, _ = p.Acquire(ctx)
	defer c.Release()
	if options()&64 == 0 {
		t.Error("a reset connection lost its setup")
	}
	if !strings.Contains(fmt.Sprint(setup), "ARITHABORT") {
		t.Fatal(setup)
	}
}
