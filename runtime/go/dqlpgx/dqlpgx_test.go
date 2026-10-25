package dqlpgx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/thec1oud/dql/runtime/go/civil"
	"github.com/thec1oud/dql/runtime/go/dec"
	"github.com/thec1oud/dql/runtime/go/dql"
)

// The tests run against the Postgres 16 server DQL_TEST_POSTGRES_URL names,
// in a fresh database each; they fail rather than skip without one, since
// they keep the package at full coverage.
const urlEnv = "DQL_TEST_POSTGRES_URL"

const schema = `
CREATE TYPE mood AS ENUM ('sad', 'happy');
CREATE TYPE point2 AS (x int4, tags text[]);
CREATE TYPE pair AS (p point2, m mood, j json);
CREATE TYPE floatrange AS RANGE (subtype = float8);
CREATE TYPE priced AS (amount money);
CREATE TABLE item (
	id int8 PRIMARY KEY,
	name text NOT NULL CONSTRAINT name_set CHECK (name <> ''),
	mood mood,
	moods mood[]
);
CREATE UNIQUE INDEX item_name ON item (name);
`

// database creates a fresh database with the test schema and returns its
// URL; the database is dropped when the test ends.
func database(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	base := os.Getenv(urlEnv)
	if base == "" {
		t.Fatal(urlEnv + " is not set; these tests need a Postgres 16 server, such as postgres://postgres@localhost:5432/postgres")
	}
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("dqlpgx_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name+" TEMPLATE template0 ENCODING 'UTF8' LOCALE 'C'"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(ctx)
	})
	u, _ := url.Parse(base)
	u.Path = "/" + name
	c, err := pgx.Connect(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	if _, err := c.Exec(ctx, schema); err != nil {
		t.Fatal(err)
	}
	return u.String()
}

var dialect = dql.Dialect{
	Name:  "postgres",
	Setup: []string{"SET TIME ZONE 'UTC'"},
	Errors: dql.ErrorMap{
		Codes: map[string]dql.Class{
			"23505": dql.ClassUniqueViolation, "23514": dql.ClassCheckViolation,
			"40001": dql.ClassSerializationFailure, "57P01": dql.ClassConnectionError,
		},
		Violations: map[dql.Reported]dql.Constraint{
			{Catalog: "shop", Schema: "public", Object: "item", Class: dql.ClassUniqueViolation, Name: "item_name"}: {Name: "shop.item.name"},
		},
	},
}

func executor(t *testing.T, dsn string, d *Driver) *dql.Executor {
	t.Helper()
	e, err := dql.New(context.Background(), d, dialect, dql.Config{
		Sources:  map[string]dql.SourceConfig{"main": {Primary: dql.ConnConfig{DSN: dsn, MaxConns: 4}}},
		Catalogs: map[string]string{"shop": "main"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return e
}

func stmt(sql string, args ...any) dql.Statement {
	return dql.Statement{Query: "q", Catalog: "shop", SQL: sql, Args: args}
}

func TestExecutor(t *testing.T) {
	ctx := context.Background()
	e := executor(t, database(t), New("mood", "point2", "pair"))
	insert := func(ctx context.Context, h dql.Writer, id int64, name string) error {
		s := stmt("INSERT INTO item (id, name, mood, moods) VALUES ($1, $2, $3, $4)", id, name, "happy", []string{"sad", "happy"})
		s.Writes = true
		_, err := dql.Exec(ctx, h, s)
		return err
	}
	if err := insert(ctx, e, 1, "one"); err != nil {
		t.Fatal(err)
	}
	row, err := dql.One(ctx, e, stmt("SELECT id, name, mood, moods FROM item WHERE id = $1", int64(1)))
	if err != nil || !reflect.DeepEqual(row, dql.Row{int64(1), "one", "happy", []any{"sad", "happy"}}) {
		t.Fatalf("row %#v, %v", row, err)
	}

	// A violation translates through the error map.
	var uv *dql.UniqueViolation
	if err := insert(ctx, e, 2, "one"); !errors.As(err, &uv) || uv.Constraint.Name != "shop.item.name" {
		t.Fatalf("unique: %v", err)
	}
	var cv *dql.CheckViolation
	if err := insert(ctx, e, 2, ""); !errors.As(err, &cv) || cv.Constraint.Name != "shop.public.item.name_set" {
		t.Fatalf("check: %v", err)
	}

	// A write transaction commits; a nested one rolls back to its savepoint.
	err = e.Write(ctx, func(ctx context.Context, tx *dql.WriteTx) error {
		if err := insert(ctx, tx, 2, "two"); err != nil {
			return err
		}
		nested := e.Write(ctx, func(ctx context.Context, _ *dql.WriteTx) error {
			if err := insert(ctx, e, 3, "three"); err != nil {
				return err
			}
			return errors.New("undo three")
		}, dql.WithPropagation(dql.Nested))
		if nested == nil {
			t.Error("nested: no error")
		}
		return nil
	}, dql.WithIsolation(dql.Serializable))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := dql.Fetch(ctx, e, stmt("SELECT id FROM item ORDER BY id"))
	if err != nil || !reflect.DeepEqual(rows, []dql.Row{{int64(1)}, {int64(2)}}) {
		t.Fatalf("rows %v, %v", rows, err)
	}

	// A read transaction is read only.
	err = e.Read(ctx, func(ctx context.Context, tx *dql.ReadTx) error {
		_, err := dql.Fetch(ctx, tx, stmt("INSERT INTO item (id, name) VALUES (9, 'nine') RETURNING id"))
		return err
	})
	var de *dql.DatabaseError
	if !errors.As(err, &de) || de.Code != "25006" {
		t.Fatalf("read only: %v", err)
	}

	// A failed statement aborts the transaction, so its commit rolls back.
	tx, err := e.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	insert(ctx, tx, 4, "four")
	if err := insert(ctx, tx, 5, "one"); err == nil {
		t.Fatal("no unique violation")
	}
	if err := tx.Commit(ctx); !errors.As(err, &de) || de.Message != ErrCommitRolledBack.Error() {
		t.Fatalf("commit: %v", err)
	}
	if n, _ := dql.Fetch(ctx, e, stmt("SELECT 1 FROM item WHERE id = 4")); len(n) != 0 {
		t.Fatal("4 committed")
	}
}

func TestSetup(t *testing.T) {
	ctx := context.Background()
	dsn := database(t)
	e := executor(t, dsn, New())
	row, err := dql.One(ctx, e, stmt("SELECT current_setting('TimeZone')"))
	if err != nil || row[0] != "UTC" {
		t.Fatalf("%v %v", row, err)
	}

	d := New()
	if _, err := d.Open(ctx, dql.PoolConfig{DSN: "postgres://%zz"}); err == nil {
		t.Error("bad DSN opened")
	}
	if _, err := d.Open(ctx, dql.PoolConfig{DSN: dsn, MaxConns: 1 << 31}); err == nil {
		t.Error("bad pool config opened")
	}
	p, err := d.Open(ctx, dql.PoolConfig{DSN: dsn, Setup: []string{"SET nonsense"}})
	if err != nil {
		t.Fatal(err)
	}
	if c, err := p.Acquire(ctx); err == nil {
		c.Release()
		t.Error("bad setup acquired")
	}
	p.Close()
	// pgx has no type for money, so it cannot load a composite type of it.
	for _, bad := range []string{"no_such_type", "priced"} {
		p, err = New(bad).Open(ctx, dql.PoolConfig{DSN: dsn})
		if err != nil {
			t.Fatal(err)
		}
		if c, err := p.Acquire(ctx); err == nil {
			c.Release()
			t.Errorf("%s acquired", bad)
		}
		p.Close()
	}

	if got := arrayName("app.status"); got != "app._status" {
		t.Error(got)
	}
	if got := arrayName("status"); got != "_status" {
		t.Error(got)
	}
}

func TestErrors(t *testing.T) {
	ctx := context.Background()
	dsn := database(t)
	d := New()
	p, err := d.Open(ctx, dql.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	c, err := p.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Exec(ctx, "INSERT INTO item (id, name) VALUES (1, NULL)", nil)
	n, ok := d.Native(err)
	if !ok || n.Code != "23502" || n.Schema != "public" || n.Table != "item" || n.Column != "name" || d.Broken(err) {
		t.Fatalf("%+v %v", n, err)
	}
	if _, ok := d.Native(errors.New("x")); ok {
		t.Error("native plain error")
	}

	// A terminated backend reports 57P01, and then the connection is broken.
	var pid int64
	rows, _ := c.Query(ctx, "SELECT pg_backend_pid()", nil)
	for rows.Next() {
		pid = rows.Row()[0].(int64)
	}
	rows.Close()
	admin, _ := pgx.Connect(ctx, dsn)
	defer admin.Close(ctx)
	admin.Exec(ctx, "SELECT pg_terminate_backend($1)", pid)
	_, err = c.Exec(ctx, "SELECT 1", nil)
	if n, _ := d.Native(err); n.Code != "57P01" && !d.Broken(err) {
		t.Fatalf("terminated: %v", err)
	}
	_, err = c.Exec(ctx, "SELECT 1", nil)
	if !d.Broken(err) {
		t.Fatalf("closed: %v", err)
	}
	c.Release()

	refused, err := d.Open(ctx, dql.PoolConfig{DSN: "postgres://localhost:1/x?connect_timeout=1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := refused.Acquire(ctx); !d.Broken(err) {
		t.Fatalf("refused: %v", err)
	}
	for _, err := range []error{io.EOF, io.ErrUnexpectedEOF, net.ErrClosed, &net.OpError{Op: "read", Err: errors.New("reset")}} {
		if !d.Broken(fmt.Errorf("x: %w", err)) {
			t.Errorf("not broken: %v", err)
		}
	}
	if d.Broken(errors.New("x")) {
		t.Error("plain error broken")
	}

	// Each statement of a connection fails once it is closed.
	c, _ = p.Acquire(ctx)
	defer c.Release()
	c.(*conn).c.Conn().Close(ctx)
	for _, f := range []func() error{
		func() error { _, err := c.Query(ctx, "SELECT 1", nil); return err },
		func() error { return c.Begin(ctx, dql.TxOptions{}) },
		func() error { return c.Commit(ctx) },
		func() error { return c.Savepoint(ctx, "s") },
	} {
		if err := f(); !d.Broken(err) {
			t.Errorf("closed conn: %v", err)
		}
	}
}

func TestStatements(t *testing.T) {
	ctx := context.Background()
	d := New()
	p, err := d.Open(ctx, dql.PoolConfig{DSN: database(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	c, _ := p.Acquire(ctx)
	defer c.Release()
	run := func(sql string) {
		t.Helper()
		if _, err := c.Exec(ctx, sql, nil); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	count := func() int64 {
		rows, err := c.Query(ctx, "SELECT count(*) FROM item", nil)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		rows.Next()
		return rows.Row()[0].(int64)
	}
	if err := c.Begin(ctx, dql.TxOptions{Isolation: dql.RepeatableRead}); err != nil {
		t.Fatal(err)
	}
	run("INSERT INTO item (id, name) VALUES (1, 'a')")
	if err := c.Savepoint(ctx, "a b"); err != nil {
		t.Fatal(err)
	}
	run("INSERT INTO item (id, name) VALUES (2, 'b')")
	if err := c.RollbackTo(ctx, "a b"); err != nil {
		t.Fatal(err)
	}
	if err := c.ReleaseSavepoint(ctx, "a b"); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 1 {
		t.Fatal(n)
	}
	if err := c.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 0 {
		t.Fatal(n)
	}
	n, err := c.Exec(ctx, "INSERT INTO item (id, name) VALUES ($1, $2), ($3, $4)", []any{int64(1), "a", int64(2), "b"})
	if err != nil || n != 2 {
		t.Fatal(n, err)
	}
	if _, err := c.Query(ctx, "SELECT nonsense", nil); err == nil {
		t.Error("bad query ran")
	}

	// A value that does not decode stops the rows with its error.
	r, err := c.Query(ctx, "SELECT 1::int4 AS n UNION ALL SELECT 2", nil)
	if err != nil {
		t.Fatal(err)
	}
	m := pgtype.NewMap()
	m.RegisterType(&pgtype.Type{Name: "int4", OID: pgtype.Int4OID, Codec: pgtype.BoolCodec{}})
	r.(*rows).m = m
	if r.Next() || r.Next() || r.Err() == nil || !strings.Contains(r.Err().Error(), "column 0 (n)") {
		t.Fatalf("decode error: %v", r.Err())
	}
	r.Close()
}

// TestValues round-trips each host type through a parameter and back.
func TestValues(t *testing.T) {
	ctx := context.Background()
	d := New("mood", "point2", "pair", "floatrange", "floatmultirange")
	p, err := d.Open(ctx, dql.PoolConfig{DSN: database(t), Setup: []string{"SET TIME ZONE 'UTC'"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	c, err := p.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Release()

	instant := time.Date(2024, 2, 29, 12, 30, 45, 123456000, time.UTC)
	addr := netip.MustParseAddr("10.0.0.1")
	prefix := netip.MustParsePrefix("10.0.0.0/8")
	mac, _ := net.ParseMAC("08:00:2b:01:02:03")
	bits, _ := dql.ParseBits("101")
	id := uuid.MustParse("0b8d2a3e-1c5f-4d6e-9a7b-8c9d0e1f2a3b")
	date := civil.Date{Year: 2024, Month: 2, Day: 29}
	local := civil.DateTime{Date: date, Time: civil.Time{Hour: 12, Minute: 30, Second: 45, Nanosecond: 123456000}}
	offTime := civil.OffsetTime{Time: civil.Time{Hour: 12, Minute: 30}, Offset: 5*3600 + 1800}
	type mood string
	cases := []struct {
		typ  string
		arg  any
		want any
	}{
		{"int2", int16(7), int64(7)},
		{"int4", int32(7), int64(7)},
		{"int8", int64(7), int64(7)},
		{"oid", uint32(7), int64(7)},
		{"float4", float32(1.5), float64(1.5)},
		{"float8", 1.5, 1.5},
		{"bool", true, true},
		{"text", "x", "x"},
		{"bytea", []byte("x"), []byte("x")},
		{"numeric", dec.MustParse("12.50"), dec.MustParse("12.50")},
		{"numeric", dec.Decimal{}, dec.MustParse("0")},
		{"money", dec.MustParse("-1234.50"), dec.MustParse("-1234.50")},
		{"date", date, date},
		{"timestamp", local, local},
		{"timestamptz", instant, instant},
		{"timestamptz", civil.OffsetDateTimeOf(instant.In(time.FixedZone("", 3600))), instant},
		{"time", local.Time, local.Time},
		{"timetz", offTime, offTime},
		{"timetz", civil.OffsetTime{Time: civil.Time{Hour: 1}, Offset: -7200}, civil.OffsetTime{Time: civil.Time{Hour: 1}, Offset: -7200}},
		{"interval", dql.Interval{Months: 1, Days: -2, Microseconds: 3}, dql.Interval{Months: 1, Days: -2, Microseconds: 3}},
		{"uuid", id, id},
		{"json", json.RawMessage(`{"a": 1}`), json.RawMessage(`{"a": 1}`)},
		{"jsonb", json.RawMessage(`{"a":1}`), json.RawMessage(`{"a": 1}`)},
		{"inet", addr, addr},
		{"inet", prefix, prefix},
		{"cidr", prefix, prefix},
		{"macaddr", mac, mac},
		{"bit(3)", bits, bits},
		{"varbit", bits, bits},
		{"xml", "<a/>", "<a/>"},
		{"mood", mood("sad"), "sad"},
		{"int4", (*int32)(nil), nil},
		{"int4", dql.Ptr(int32(3)), int64(3)},
		{"int4[]", []int32(nil), nil},
		{"int4[]", []int32{1, 2}, []any{int64(1), int64(2)}},
		{"int8[]", [][]int64{{1, 2}, {3, 4}}, []any{[]any{int64(1), int64(2)}, []any{int64(3), int64(4)}}},
		{"date[]", []civil.Date{date}, []any{date}},
		{"mood[]", []mood{"happy"}, []any{"happy"}},
		{"json[]", []json.RawMessage{json.RawMessage(`{"a":1}`)}, []any{json.RawMessage(`{"a":1}`)}},
		{"int4range", dql.NewRange(dql.InclusiveBound(int32(1)), dql.ExclusiveBound(int32(5))), dql.NewRange(dql.InclusiveBound(int32(1)), dql.ExclusiveBound(int32(5)))},
		{"int4range", dql.EmptyRange[int32](), dql.EmptyRange[int32]()},
		{"int8range", dql.NewRange(dql.UnboundedBound[int64](), dql.ExclusiveBound(int64(5))), dql.NewRange(dql.UnboundedBound[int64](), dql.ExclusiveBound(int64(5)))},
		{"numrange", dql.NewRange(dql.InclusiveBound(dec.MustParse("1.5")), dql.UnboundedBound[dec.Decimal]()), dql.NewRange(dql.InclusiveBound(dec.MustParse("1.5")), dql.UnboundedBound[dec.Decimal]())},
		{"daterange", dql.NewRange(dql.InclusiveBound(date), dql.ExclusiveBound(date.AddDays(1))), dql.NewRange(dql.InclusiveBound(date), dql.ExclusiveBound(date.AddDays(1)))},
		{"tsrange", dql.NewRange(dql.InclusiveBound(local), dql.InclusiveBound(local)), dql.NewRange(dql.InclusiveBound(local), dql.InclusiveBound(local))},
		{"tstzrange", dql.NewRange(dql.InclusiveBound(instant), dql.UnboundedBound[time.Time]()), dql.NewRange(dql.InclusiveBound(instant), dql.UnboundedBound[time.Time]())},
		{"int4multirange", []dql.Range[int32]{dql.NewRange(dql.InclusiveBound(int32(1)), dql.ExclusiveBound(int32(2)))}, []dql.Range[int32]{dql.NewRange(dql.InclusiveBound(int32(1)), dql.ExclusiveBound(int32(2)))}},
		{"int8multirange", nil, nil},
		{"int8multirange", []dql.Range[int64]{dql.NewRange(dql.InclusiveBound(int64(1)), dql.ExclusiveBound(int64(2)))}, []dql.Range[int64]{dql.NewRange(dql.InclusiveBound(int64(1)), dql.ExclusiveBound(int64(2)))}},
		{"nummultirange", []dql.Range[dec.Decimal]{dql.NewRange(dql.InclusiveBound(dec.MustParse("1")), dql.UnboundedBound[dec.Decimal]())}, []dql.Range[dec.Decimal]{dql.NewRange(dql.InclusiveBound(dec.MustParse("1")), dql.UnboundedBound[dec.Decimal]())}},
		{"tsmultirange", []dql.Range[civil.DateTime]{dql.NewRange(dql.InclusiveBound(local), dql.UnboundedBound[civil.DateTime]())}, []dql.Range[civil.DateTime]{dql.NewRange(dql.InclusiveBound(local), dql.UnboundedBound[civil.DateTime]())}},
		{"tstzmultirange", []dql.Range[time.Time]{dql.NewRange(dql.UnboundedBound[time.Time](), dql.ExclusiveBound(instant))}, []dql.Range[time.Time]{dql.NewRange(dql.UnboundedBound[time.Time](), dql.ExclusiveBound(instant))}},
		{"point2", dql.Row{int32(1), []string{"a"}}, dql.Row{int64(1), []any{"a"}}},
		{"pair", dql.Row{dql.Row{int32(1), nil}, mood("sad"), json.RawMessage(`[1]`)}, dql.Row{dql.Row{int64(1), nil}, "sad", json.RawMessage(`[1]`)}},
	}
	for _, tc := range cases {
		rows, err := c.Query(ctx, "SELECT $1::"+tc.typ, []any{tc.arg})
		if err != nil {
			t.Errorf("%s %v: %v", tc.typ, tc.arg, err)
			continue
		}
		var got any
		for rows.Next() {
			got = rows.Row()[0]
		}
		if err := rows.Err(); err != nil {
			t.Errorf("%s %v: %v", tc.typ, tc.arg, err)
		}
		rows.Close()
		if !equal(got, tc.want) {
			t.Errorf("%s: got %#v, want %#v", tc.typ, got, tc.want)
		}
	}

	// Values dql has no host type for stay as pgx decodes them.
	for sql, want := range map[string]any{
		"SELECT 'NaN'::numeric":                            pgtype.Numeric{NaN: true, Valid: true},
		"SELECT 'infinity'::date":                          pgtype.Infinity,
		"SELECT '24:00'::time":                             pgtype.Time{Microseconds: usPerDay, Valid: true},
		"SELECT '10.0.0.0/8'::inet":                        netip.MustParsePrefix("10.0.0.0/8"),
		"SELECT '[2024-01-01,infinity)'::daterange":        nil,
		"SELECT '{[2024-01-01,infinity)}'::datemultirange": nil,
		"SELECT '[1,2)'::floatrange":                       nil,
		"SELECT '{[1,2)}'::floatmultirange":                nil,
		"SELECT '(1,2)'::point":                            pgtype.Point{P: pgtype.Vec2{X: 1, Y: 2}, Valid: true},
	} {
		rows, err := c.Query(ctx, sql, nil)
		if err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		rows.Next()
		got := rows.Row()[0]
		rows.Close()
		if want == nil {
			if !strings.HasPrefix(reflect.TypeOf(got).String(), "pgtype.") {
				t.Errorf("%s: got %#v", sql, got)
			}
		} else if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %#v, want %#v", sql, got, want)
		}
	}
}

func equal(a, b any) bool {
	switch x := a.(type) {
	case dec.Decimal:
		y, ok := b.(dec.Decimal)
		return ok && x.Equal(y)
	case dql.Bits:
		y, ok := b.(dql.Bits)
		return ok && x.Equal(y)
	case dql.Range[dec.Decimal]:
		y, ok := b.(dql.Range[dec.Decimal])
		return ok && x.Lower.Kind == y.Lower.Kind && x.Lower.Value.Equal(y.Lower.Value) && x.Upper.Kind == y.Upper.Kind
	}
	return reflect.DeepEqual(a, b)
}

// TestDecode covers what the server does not send in these tests: binary
// values of unknown types and binary jsonb, and values convert receives
// only from a malformed or unusual decode.
func TestDecode(t *testing.T) {
	m := pgtype.NewMap()
	if v, err := decode(m, 99999, pgtype.BinaryFormatCode, []byte{1}); err != nil || !reflect.DeepEqual(v, []byte{1}) {
		t.Errorf("unknown binary: %v %v", v, err)
	}
	if v, err := decode(m, pgtype.JSONBOID, pgtype.BinaryFormatCode, []byte("\x01{}")); err != nil || string(v.(json.RawMessage)) != "{}" {
		t.Errorf("binary jsonb: %v %v", v, err)
	}
	if v, err := decode(m, pgtype.Int4OID, pgtype.BinaryFormatCode, nil); err != nil || v != nil {
		t.Errorf("null: %v %v", v, err)
	}
	if _, err := decode(m, pgtype.Int4OID, pgtype.BinaryFormatCode, []byte{1}); err == nil {
		t.Error("short int4 decoded")
	}
	if _, err := decode(m, pgtype.Int4ArrayOID, pgtype.BinaryFormatCode, []byte{1}); err == nil {
		t.Error("short int4[] decoded")
	}
	f := func() {}
	if v := convert(m, pgtype.JSONOID, f); reflect.ValueOf(v).Pointer() != reflect.ValueOf(f).Pointer() {
		t.Errorf("unmarshalable json: %v", v)
	}
	odd := pgtype.Bits{Bytes: []byte{1}, Len: 9, Valid: true}
	if v := convert(m, pgtype.VarbitOID, odd); !reflect.DeepEqual(v, odd) {
		t.Errorf("bad bits: %v", v)
	}
	if v := convert(m, oidMoney, "€1"); v != "€1" {
		t.Errorf("money: %v", v)
	}
	if v := convert(m, oidTimeTZ, "noon"); v != "noon" {
		t.Errorf("timetz: %v", v)
	}
	if v := convert(m, pgtype.BitOID, []byte{1}); !reflect.DeepEqual(v, []byte{1}) {
		t.Errorf("bytes: %v", v)
	}
	if v := encode(struct{ A int }{1}); !reflect.DeepEqual(v, struct{ A int }{1}) {
		t.Errorf("struct: %v", v)
	}
	if !isRange(reflect.TypeFor[dql.Range[string]]()) || isRange(reflect.TypeFor[dql.Bound[string]]()) || isRange(reflect.TypeFor[pgconn.PgError]()) {
		t.Error("isRange")
	}
}
