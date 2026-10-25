package dql

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var pg = Dialect{
	Name:  "postgres",
	Setup: []string{"SET TIME ZONE 'UTC'"},
	Errors: ErrorMap{
		Codes: map[string]Class{
			"23505": ClassUniqueViolation, "23503": ClassForeignKeyViolation, "23514": ClassCheckViolation,
			"23502": ClassNotNullViolation, "23P01": ClassExclusionViolation, "40001": ClassSerializationFailure,
			"40P01": ClassDeadlock, "55P03": ClassLockTimeout, "57014": ClassTimeout, "08006": ClassConnectionError,
		},
		Violations: map[Reported]Constraint{
			{Catalog: "shop", Schema: "app", Object: "users", Class: ClassUniqueViolation, Name: "users_tenant_email_key"}: {Name: "shop.app.users.users_tenant_email_key", Kind: ConstraintUnique},
			{Catalog: "shop", Schema: "app", Object: "users", Class: ClassCheckViolation, Name: "users_check"}:             {Name: "shop.app.users.users_check", Kind: ConstraintCheck, Sensitive: true},
		},
	},
}

// rows answers every statement with these rows, or the statement's error.
func rows(rs ...Row) func(string, []any) ([]Row, int64, error) {
	return func(sql string, _ []any) ([]Row, int64, error) {
		if strings.HasPrefix(sql, "FAIL ") {
			return nil, 0, &fakeNative{NativeError{Code: strings.TrimPrefix(sql, "FAIL ")}}
		}
		return rs, int64(len(rs)), nil
	}
}

func newExec(t *testing.T, d *fakeDriver, opts ...Option) *Executor {
	t.Helper()
	if d.handle == nil {
		d.handle = rows(Row{int64(1)})
	}
	e, err := New(context.Background(), d, pg, Config{
		Sources:  map[string]SourceConfig{"main": {Primary: ConnConfig{DSN: "pg", MaxConns: 3}}},
		Catalogs: map[string]string{"shop": "main"},
	}, append([]Option{withRand(func() float64 { return 0 })}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	d.calls()
	savepoints.Store(0)
	return e
}

func stmt(sql string) Statement { return Statement{Query: "q", Catalog: "shop", SQL: sql} }

func expect(t *testing.T, d *fakeDriver, want string) {
	t.Helper()
	if got := d.calls(); got != want {
		t.Errorf("calls\n got %s\nwant %s", got, want)
	}
}

func TestNew(t *testing.T) {
	ctx := context.Background()
	d := &fakeDriver{}
	t.Setenv("SHOP_URL", "pg://shop")
	e, err := New(ctx, d, pg, Config{
		Sources: map[string]SourceConfig{
			"main":      {Primary: ConnConfig{DSN: "env:SHOP_URL", MaxConns: 2}},
			"analytics": {Primary: ConnConfig{DSN: "pg://wh", MaxConns: 1}},
		},
		Catalogs: map[string]string{"shop": "main"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Sources open in name order, each connecting once (§22.1).
	expect(t, d, `open pg://wh max 1 setup ["SET TIME ZONE 'UTC'"]; pg://wh#1 put back; open pg://shop max 2 setup ["SET TIME ZONE 'UTC'"]; pg://shop#1 put back`)
	if e.retry != DefaultRetry {
		t.Errorf("retry %v", e.retry)
	}
	e.Close()
	expect(t, d, "close pg://wh; close pg://shop")

	fail := func(d *fakeDriver, c Config) string {
		t.Helper()
		_, err := New(ctx, d, pg, c)
		if err == nil {
			t.Fatal("no error")
		}
		return err.Error()
	}
	one := func(dsn string) Config {
		return Config{Sources: map[string]SourceConfig{"main": {Primary: ConnConfig{DSN: dsn, MaxConns: 1}}}}
	}
	if got := fail(&fakeDriver{}, one("env:NOPE_NOT_SET")); got != "dql: source main: environment variable NOPE_NOT_SET is not set" {
		t.Error(got)
	}
	if got := fail(&fakeDriver{openErr: errors.New("bad dsn")}, one("x")); got != "dql: ConnectionError on source main (postgres): bad dsn" {
		t.Error(got)
	}
	d = &fakeDriver{fail: map[string]error{"acquire": errors.New("refused")}}
	if got := fail(d, one("x")); got != "dql: ConnectionError on source main (postgres): refused" || !d.pools[0].closed {
		t.Error(got)
	}
	c := one("x")
	c.Catalogs = map[string]string{"shop": "other"}
	if got := fail(&fakeDriver{}, c); got != "dql: catalog shop names source other, which is not configured" {
		t.Error(got)
	}
}

func TestStatements(t *testing.T) {
	ctx := context.Background()
	d := &fakeDriver{}
	e := newExec(t, d)

	rs, err := Fetch(ctx, e, Statement{Query: "q", Catalog: "shop", SQL: "SELECT", Args: []any{7}})
	if err != nil || len(rs) != 1 {
		t.Fatal(rs, err)
	}
	expect(t, d, "pg#2 query SELECT [7]; pg#2 put back")

	d.handle = rows()
	if rs, _ := Fetch(ctx, e, stmt("SELECT")); rs == nil || len(rs) != 0 {
		t.Errorf("no rows is %v, want empty and non-nil", rs)
	}
	if _, err := One(ctx, e, stmt("SELECT")); ClassOf(err) != ClassNotFound || err.Error() != "dql: NotFound in q (postgres)" {
		t.Errorf("one of none: %v", err)
	}
	if r, ok, err := Optional(ctx, e, stmt("SELECT")); r != nil || ok || err != nil {
		t.Errorf("optional of none: %v %v %v", r, ok, err)
	}
	d.handle = rows(Row{1}, Row{2})
	if _, err := One(ctx, e, stmt("SELECT")); ClassOf(err) != ClassTooManyRows {
		t.Errorf("one of two: %v", err)
	}
	// A proven statement reads one row and stops.
	s := stmt("SELECT")
	s.AtMostOne = true
	if r, err := One(ctx, e, s); err != nil || r[0] != 1 {
		t.Errorf("at most one: %v %v", r, err)
	}
	if r, ok, err := Optional(ctx, e, stmt("SELECT")); err == nil || ok || r != nil {
		t.Errorf("optional of two: %v", err)
	}
	d.calls()

	if n, err := Exec(ctx, e, stmt("UPDATE")); n != 2 || err != nil {
		t.Errorf("exec %d %v", n, err)
	}
	expect(t, d, "pg#9 exec UPDATE []; pg#9 put back")
	if _, err := Exec(ctx, e, stmt("FAIL 23505")); ClassOf(err) != ClassUniqueViolation {
		t.Errorf("exec error %v", err)
	}
	if _, err := Fetch(ctx, e, stmt("FAIL 40001")); ClassOf(err) != ClassSerializationFailure {
		t.Errorf("query error %v", err)
	}
	if _, _, err := Optional(ctx, e, stmt("FAIL 40001")); ClassOf(err) != ClassSerializationFailure {
		t.Errorf("optional error %v", err)
	}
	d.fail = map[string]error{"rows": errBroken}
	if _, err := Fetch(ctx, e, stmt("SELECT")); ClassOf(err) != ClassConnectionError {
		t.Errorf("rows error %v", err)
	}
	d.fail = map[string]error{"acquire": errBroken}
	if _, err := Fetch(ctx, e, stmt("SELECT")); ClassOf(err) != ClassConnectionError {
		t.Errorf("acquire error %v", err)
	}
	if _, err := Exec(ctx, e, stmt("SELECT")); ClassOf(err) != ClassConnectionError {
		t.Errorf("exec acquire error %v", err)
	}
	d.fail = nil
	if _, err := Fetch(ctx, e, Statement{Catalog: "nope"}); err == nil || err.Error() != "dql: no source serves catalog nope" {
		t.Errorf("unknown catalog %v", err)
	}
}

func TestWrite(t *testing.T) {
	ctx := context.Background()
	d := &fakeDriver{}
	e := newExec(t, d)

	// A closure begins on first use, joins nested closures, and commits.
	err := e.Write(ctx, func(ctx context.Context, tx *WriteTx) error {
		if _, err := Exec(ctx, tx, stmt("INSERT")); err != nil {
			return err
		}
		return e.Read(ctx, func(ctx context.Context, r *ReadTx) error {
			_, err := Fetch(ctx, e, stmt("SELECT")) // the executor joins ctx's transaction
			return err
		})
	}, WithIsolation(Serializable))
	if err != nil {
		t.Fatal(err)
	}
	expect(t, d, "pg#2 begin serializable; pg#2 exec INSERT []; pg#2 query SELECT []; pg#2 commit; pg#2 put back")

	// An error rolls back, and a closure that never ran a statement has
	// nothing to end.
	boom := errors.New("boom")
	if err := e.Write(ctx, func(ctx context.Context, tx *WriteTx) error {
		Exec(ctx, tx, stmt("INSERT"))
		return boom
	}); err != boom {
		t.Errorf("error %v", err)
	}
	expect(t, d, "pg#3 begin; pg#3 exec INSERT []; pg#3 rollback; pg#3 put back")
	e.Write(ctx, func(context.Context, *WriteTx) error { return nil })
	expect(t, d, "")

	// A panic rolls back and panics on.
	func() {
		defer func() {
			if recover() != "oops" {
				t.Error("no panic")
			}
		}()
		e.Write(ctx, func(ctx context.Context, tx *WriteTx) error {
			Exec(ctx, tx, stmt("INSERT"))
			panic("oops")
		})
	}()
	expect(t, d, "pg#4 begin; pg#4 exec INSERT []; pg#4 rollback; pg#4 put back")

	// A read transaction runs read only, and refuses writes.
	err = e.Read(ctx, func(ctx context.Context, r *ReadTx) error {
		if _, err := Fetch(ctx, r, stmt("SELECT")); err != nil {
			return err
		}
		if _, err := Exec(ctx, e, stmt("UPDATE")); err != ErrReadOnly {
			t.Errorf("a write in a read transaction: %v", err)
		}
		if _, err := Fetch(ctx, r, Statement{Catalog: "shop", Writes: true}); err != ErrReadOnly {
			t.Errorf("a writing read in a read transaction: %v", err)
		}
		if err := e.Write(ctx, func(context.Context, *WriteTx) error { return nil }); err != ErrReadOnly {
			t.Errorf("joining a read transaction to write: %v", err)
		}
		if err := e.Write(ctx, func(context.Context, *WriteTx) error { return nil }, WithPropagation(Nested)); err != ErrReadOnly {
			t.Errorf("nesting a write in a read: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	expect(t, d, "pg#5 begin read only; pg#5 query SELECT []; pg#5 commit; pg#5 put back")

	// Begin, commit and statement failures.
	d.fail = map[string]error{"begin": errBroken}
	err = e.Write(ctx, func(ctx context.Context, tx *WriteTx) error {
		_, err := Exec(ctx, tx, stmt("INSERT"))
		return err
	})
	if ClassOf(err) != ClassConnectionError {
		t.Errorf("begin failure %v", err)
	}
	expect(t, d, "pg#6 begin; pg#6 put back")
	d.fail = map[string]error{"commit": &fakeNative{NativeError{Code: "40001"}}}
	err = e.Write(ctx, func(ctx context.Context, tx *WriteTx) error {
		_, err := Exec(ctx, tx, stmt("INSERT"))
		return err
	}, WithRetry(RetryPolicy{Attempts: 1}))
	if ClassOf(err) != ClassSerializationFailure {
		t.Errorf("commit failure %v", err)
	}
	expect(t, d, "pg#7 begin; pg#7 exec INSERT []; pg#7 commit; pg#7 put back")
	d.fail = map[string]error{"acquire": errBroken}
	err = e.Write(ctx, func(ctx context.Context, tx *WriteTx) error {
		_, err := Exec(ctx, tx, stmt("INSERT"))
		return err
	})
	if ClassOf(err) != ClassConnectionError {
		t.Errorf("acquire failure %v", err)
	}
	d.fail = nil
	err = e.Write(ctx, func(ctx context.Context, tx *WriteTx) error {
		_, err := Exec(ctx, tx, Statement{Catalog: "nope"})
		return err
	})
	if err == nil {
		t.Error("unknown catalog in a transaction")
	}
}

// A transaction spans the sources its statements reach, committing each
// in the order it began.
func TestWriteAcrossSources(t *testing.T) {
	ctx := context.Background()
	d := &fakeDriver{handle: rows()}
	e, err := New(ctx, d, pg, Config{
		Sources:  map[string]SourceConfig{"a": {Primary: ConnConfig{DSN: "a", MaxConns: 1}}, "b": {Primary: ConnConfig{DSN: "b", MaxConns: 1}}},
		Catalogs: map[string]string{"one": "a", "two": "b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	d.calls()
	d.fail = map[string]error{"commit": errBroken}
	err = e.Write(ctx, func(ctx context.Context, tx *WriteTx) error {
		Exec(ctx, tx, Statement{Catalog: "two", SQL: "X"})
		Exec(ctx, tx, Statement{Catalog: "one", SQL: "Y"})
		return nil
	})
	if ClassOf(err) != ClassConnectionError {
		t.Errorf("commit failure %v", err)
	}
	// The first commit fails, so the second source rolls back.
	expect(t, d, "b#2 begin; b#2 exec X []; a#2 begin; a#2 exec Y []; b#2 commit; b#2 put back; a#2 rollback; a#2 put back")
}

func TestPropagation(t *testing.T) {
	ctx := context.Background()
	d := &fakeDriver{}
	e := newExec(t, d)
	ok := func(context.Context, *WriteTx) error { return nil }
	if err := e.Write(ctx, ok, WithPropagation(Mandatory)); err != ErrNoTransaction {
		t.Errorf("mandatory: %v", err)
	}
	insert := func(ctx context.Context, tx *WriteTx) error {
		_, err := Exec(ctx, tx, stmt("INSERT"))
		return err
	}
	err := e.Write(ctx, func(ctx context.Context, tx *WriteTx) error {
		insert(ctx, tx)
		if err := e.Write(ctx, ok, WithPropagation(Never)); err != ErrInTransaction {
			t.Errorf("never: %v", err)
		}
		if err := e.Write(ctx, insert, WithPropagation(Mandatory)); err != nil {
			t.Errorf("mandatory: %v", err)
		}
		// A savepoint is made on first use and released on success.
		if err := e.Write(ctx, func(ctx context.Context, tx *WriteTx) error {
			if err := insert(ctx, tx); err != nil {
				return err
			}
			// A savepoint inside a savepoint.
			return e.Write(ctx, insert, WithPropagation(Nested))
		}, WithPropagation(Nested)); err != nil {
			t.Errorf("nested: %v", err)
		}
		// A failing savepoint rolls back to itself.
		boom := errors.New("boom")
		if err := e.Write(ctx, func(ctx context.Context, tx *WriteTx) error {
			insert(ctx, tx)
			return boom
		}, WithPropagation(Nested)); err != boom {
			t.Errorf("nested failure: %v", err)
		}
		// A savepoint that ran nothing has nothing to end.
		e.Write(ctx, ok, WithPropagation(Nested))
		// An independent transaction has its own connection.
		return e.Write(ctx, insert, WithPropagation(Independent))
	})
	if err != nil {
		t.Fatal(err)
	}
	expect(t, d, "pg#2 begin; pg#2 exec INSERT []; pg#2 exec INSERT []; "+
		"pg#2 savepoint dql_1; pg#2 exec INSERT []; pg#2 savepoint dql_2; pg#2 exec INSERT []; pg#2 release dql_2; pg#2 release dql_1; "+
		"pg#2 savepoint dql_3; pg#2 exec INSERT []; pg#2 rollback to dql_3; pg#2 release dql_3; "+
		"pg#3 begin; pg#3 exec INSERT []; pg#3 commit; pg#3 put back; pg#2 commit; pg#2 put back")

	// Nested without an enclosing transaction begins one.
	e.Write(ctx, insert, WithPropagation(Nested))
	expect(t, d, "pg#4 begin; pg#4 exec INSERT []; pg#4 commit; pg#4 put back")

	// Savepoint failures.
	for _, op := range []string{"savepoint", "release", "rollback to"} {
		d.fail = map[string]error{op: errBroken}
		err := e.Write(ctx, func(ctx context.Context, tx *WriteTx) error {
			Exec(ctx, tx, stmt("INSERT"))
			return e.Write(ctx, func(ctx context.Context, tx *WriteTx) error {
				if err := insert(ctx, tx); err != nil {
					return err
				}
				return errors.New("fail")
			}, WithPropagation(Nested))
		})
		if err == nil {
			t.Errorf("%s failure: no error", op)
		}
	}
}

func TestIndependent(t *testing.T) {
	ctx := context.Background()
	d := &fakeDriver{}
	e := newExec(t, d, WithIndependentDepth(1), WithIndependentTimeout(10*time.Millisecond))
	insert := func(ctx context.Context, tx *WriteTx) error {
		_, err := Exec(ctx, tx, stmt("INSERT"))
		return err
	}
	// Too deep.
	err := e.Write(ctx, func(ctx context.Context, tx *WriteTx) error {
		return e.Write(ctx, insert, WithPropagation(Independent))
	}, WithPropagation(Independent))
	if err != ErrIndependentDepth {
		t.Errorf("depth: %v", err)
	}
	// With every connection taken, an independent transaction fails fast.
	held := []Conn{}
	for range 3 {
		c, _ := d.pools[0].Acquire(ctx)
		held = append(held, c)
	}
	err = e.Write(ctx, insert, WithPropagation(Independent))
	if ClassOf(err) != ClassPoolExhausted || err.Error() != "dql: PoolExhausted on source main in q (postgres)" {
		t.Errorf("exhausted: %v", err)
	}
	// Its own context ending is not exhaustion.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := e.Write(cctx, insert, WithPropagation(Independent)); ClassOf(err) != ClassCanceled {
		t.Errorf("canceled: %v", err)
	}
	for _, c := range held {
		c.Release()
	}
}

// Connections reserved for independent transactions are not given to
// anything else (§22.6).
func TestIndependentReserve(t *testing.T) {
	ctx := context.Background()
	d := &fakeDriver{handle: rows()}
	e, err := New(ctx, d, pg, Config{
		Sources:  map[string]SourceConfig{"main": {Primary: ConnConfig{DSN: "pg", MaxConns: 2}, IndependentReserve: 1}},
		Catalogs: map[string]string{"shop": "main"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	err = e.Write(ctx, func(ctx context.Context, tx *WriteTx) error {
		Exec(ctx, tx, stmt("INSERT"))
		short, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
		defer cancel()
		// The one general connection is taken.
		if _, err := Fetch(short, e, Statement{Catalog: "shop"}); err != nil {
			t.Errorf("joined read: %v", err)
		}
		other, cancel2 := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel2()
		if _, err := Fetch(other, e, Statement{Catalog: "shop"}); ClassOf(err) != ClassTimeout {
			t.Errorf("general read: %v", err)
		}
		// The reserved one serves an independent transaction.
		return e.Write(ctx, func(ctx context.Context, tx *WriteTx) error {
			_, err := Exec(ctx, tx, stmt("INSERT"))
			return err
		}, WithPropagation(Independent))
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRetry(t *testing.T) {
	ctx := context.Background()
	d := &fakeDriver{}
	e := newExec(t, d)
	runs := 0
	failing := func(codes ...string) func(context.Context, *WriteTx) error {
		return func(ctx context.Context, tx *WriteTx) error {
			runs++
			code := codes[min(runs, len(codes))-1]
			if code == "" {
				return nil
			}
			_, err := Exec(ctx, tx, stmt("FAIL "+code))
			return err
		}
	}
	// A serialization failure, then a deadlock, then success.
	if err := e.Write(ctx, failing("40001", "40P01", "")); err != nil || runs != 3 {
		t.Errorf("%d runs: %v", runs, err)
	}
	// Out of attempts: the last error, with the count.
	runs = 0
	err := e.Write(ctx, failing("40001"))
	var r *Retried
	var sf *SerializationFailure
	if !errors.As(err, &r) || r.Attempts != 3 || ClassOf(err) != ClassSerializationFailure || !errors.As(err, &sf) ||
		err.Error() != "dql: SerializationFailure in q (postgres 40001) (after 3 attempts)" {
		t.Errorf("exhausted: %v", err)
	}
	// Other errors are not retried.
	runs = 0
	if err := e.Write(ctx, failing("23505")); ClassOf(err) != ClassUniqueViolation || runs != 1 {
		t.Errorf("unique: %d %v", runs, err)
	}
	// A joined closure returns its error to the root, which reruns.
	runs = 0
	inner := 0
	err = e.Write(ctx, func(ctx context.Context, tx *WriteTx) error {
		runs++
		return e.Write(ctx, func(ctx context.Context, tx *WriteTx) error {
			inner++
			_, err := Exec(ctx, tx, stmt("FAIL 40001"))
			return err
		})
	}, WithRetry(RetryPolicy{Attempts: 2, Initial: time.Microsecond, Max: time.Microsecond}))
	if runs != 2 || inner != 2 || ClassOf(err) != ClassSerializationFailure {
		t.Errorf("joined: %d %d %v", runs, inner, err)
	}
	// No rerun when the backoff would pass the deadline.
	runs = 0
	slow := newExec(t, &fakeDriver{}, withRand(func() float64 { return 0.99 }))
	dctx, cancel := context.WithTimeout(ctx, 5*time.Millisecond)
	defer cancel()
	err = slow.Write(dctx, failing("40001"), WithRetry(RetryPolicy{Attempts: 5, Initial: time.Second, Max: time.Second}))
	if runs != 1 || ClassOf(err) != ClassSerializationFailure {
		t.Errorf("deadline: %d %v", runs, err)
	}
}

func TestBackoff(t *testing.T) {
	p := RetryPolicy{Initial: 10 * time.Millisecond, Max: 200 * time.Millisecond}
	one := func() float64 { return 1 }
	for n, want := range map[int]time.Duration{1: 10 * time.Millisecond, 2: 20 * time.Millisecond, 5: 160 * time.Millisecond, 6: 200 * time.Millisecond, 70: 200 * time.Millisecond} {
		if got := p.backoff(n, one); got != want {
			t.Errorf("backoff(%d) = %v, want %v", n, got, want)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if wait(ctx, time.Hour) {
		t.Error("waited on a done context")
	}
	if !wait(context.Background(), time.Microsecond) {
		t.Error("did not wait")
	}
}

func TestPairForm(t *testing.T) {
	ctx := context.Background()
	d := &fakeDriver{}
	e := newExec(t, d)
	tx, err := e.BeginWrite(ctx, WithIsolation(RepeatableRead))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Exec(ctx, tx, stmt("INSERT")); err != nil {
		t.Fatal(err)
	}
	// Its context carries it to the executor's statements.
	if _, err := Fetch(tx.Context(ctx), e, stmt("SELECT")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Errorf("rollback after commit: %v", err)
	}
	if err := tx.Commit(ctx); err != ErrDone {
		t.Errorf("second commit: %v", err)
	}
	if _, err := Exec(ctx, tx, stmt("INSERT")); err != ErrDone {
		t.Errorf("use after commit: %v", err)
	}
	expect(t, d, "pg#2 begin repeatable read; pg#2 exec INSERT []; pg#2 query SELECT []; pg#2 commit; pg#2 put back")

	r, err := e.BeginRead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	Fetch(ctx, r, stmt("SELECT"))
	r.Rollback(ctx)
	expect(t, d, "pg#3 begin read only; pg#3 query SELECT []; pg#3 rollback; pg#3 put back")

	// A pair begun inside a transaction joins it, and leaves ending it to
	// the root.
	e.Write(ctx, func(ctx context.Context, outer *WriteTx) error {
		inner, err := e.BeginWrite(ctx)
		if err != nil {
			return err
		}
		Exec(ctx, inner, stmt("INSERT"))
		if inner.Commit(ctx) != nil || inner.Rollback(ctx) != nil {
			t.Error("a joined pair ended its root")
		}
		if _, err := e.BeginWrite(ctx, WithPropagation(Never)); err != ErrInTransaction {
			t.Errorf("never: %v", err)
		}
		if _, err := e.BeginRead(ctx, WithPropagation(Never)); err != ErrInTransaction {
			t.Errorf("never: %v", err)
		}
		return nil
	})
	expect(t, d, "pg#4 begin; pg#4 exec INSERT []; pg#4 commit; pg#4 put back")
}

func TestTranslate(t *testing.T) {
	d := &fakeDriver{}
	m := &pg.Errors
	tr := func(err error) error { return m.translate(d, "postgres", err, "shop", "q", "main") }
	native := func(n NativeError) error { return tr(&fakeNative{n}) }

	u := native(NativeError{Code: "23505", Schema: "app", Table: "users", Constraint: "users_tenant_email_key", Detail: "Key (email)=(a@x.com) already exists."})
	var uv *UniqueViolation
	if !errors.As(u, &uv) || !IsViolation(u, Constraint{Name: "shop.app.users.users_tenant_email_key"}) || uv.Detail == "" || uv.Code != "23505" {
		t.Errorf("unique %#v", u)
	}
	// A sensitive constraint's detail is dropped (§23.3).
	c := native(NativeError{Code: "23514", Schema: "app", Table: "users", Constraint: "users_check", Detail: "Failing row contains (secret)."})
	var cv *CheckViolation
	if !errors.As(c, &cv) || cv.Detail != "" {
		t.Errorf("check %#v", c)
	}
	// An unknown rule is named as reported, and redacted.
	f := native(NativeError{Code: "23503", Schema: "app", Table: "posts", Constraint: "posts_author_id_fkey", Detail: "Key (author_id)=(9)"})
	var fv *ForeignKeyViolation
	if !errors.As(f, &fv) || fv.Constraint.Name != "shop.app.posts.posts_author_id_fkey" || fv.Detail != "" {
		t.Errorf("foreign key %#v", f)
	}
	x := native(NativeError{Code: "23P01", Schema: "app", Table: "rooms", Constraint: "no_overlap"})
	if ClassOf(x) != ClassExclusionViolation {
		t.Errorf("exclusion %v", x)
	}
	// A domain's check names the domain.
	dc := native(NativeError{Code: "23514", Schema: "app", DataType: "email_address", Constraint: "email_address_check"})
	if !errors.As(dc, &cv) || cv.Constraint.Name != "shop.app.email_address.email_address_check" {
		t.Errorf("domain check %v", dc)
	}
	// Postgres names the column of a not-null violation.
	nn := native(NativeError{Code: "23502", Schema: "app", Table: "users", Column: "email"})
	var nv *NotNullViolation
	if !errors.As(nn, &nv) || nv.Column != "shop.app.users.email" || nv.Constraint.Name != "" || nn.Error() != "dql: NotNullViolation in shop.app.users.email in q (postgres 23502)" {
		t.Errorf("not null %v", nn)
	}
	named := native(NativeError{Code: "23502", Schema: "app", Table: "users", Column: "email", Constraint: "users_email_not_null"})
	if !errors.As(named, &nv) || nv.Constraint.Name != "shop.app.users.users_email_not_null" {
		t.Errorf("named not null %v", named)
	}
	for code, class := range map[string]Class{
		"40001": ClassSerializationFailure, "40P01": ClassDeadlock, "55P03": ClassLockTimeout,
		"57014": ClassTimeout, "08006": ClassConnectionError, "42601": ClassDatabaseError,
	} {
		if got := ClassOf(native(NativeError{Code: code})); got != class {
			t.Errorf("%s: %s, want %s", code, got, class)
		}
	}
	db := native(NativeError{Code: "42601", Message: "syntax error", Detail: "near x"})
	var de *DatabaseError
	if !errors.As(db, &de) || de.Message != "syntax error" || de.Detail != "near x" {
		t.Errorf("database error %#v", db)
	}
	if tr(nil) != nil {
		t.Error("nil")
	}
	if ClassOf(tr(context.Canceled)) != ClassCanceled || ClassOf(tr(context.DeadlineExceeded)) != ClassTimeout {
		t.Error("context errors")
	}
	if err := tr(errBroken); ClassOf(err) != ClassConnectionError || !errors.Is(err, errBroken) {
		t.Errorf("broken %v", err)
	}
	if err := tr(errors.New("cannot encode")); ClassOf(err) != ClassDatabaseError {
		t.Errorf("client error %v", err)
	}
	nf := &NotFound{}
	if tr(nf) != nf {
		t.Error("a taxonomy error is kept")
	}
}

// Only the executor and write transactions are Writers.
func TestHandles(t *testing.T) {
	var ws []Writer = []Writer{&Executor{}, &WriteTx{}}
	for _, w := range ws {
		w.writer()
	}
	var _ Reader = &ReadTx{}
}
