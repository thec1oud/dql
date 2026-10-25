package dql

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestIter(t *testing.T) {
	ctx := context.Background()
	rec := &recorder{}
	d := &fakeDriver{handle: rows(Row{int64(1)}, Row{int64(2)}, Row{int64(3)})}
	e := newExec(t, d, WithHooks(rec.hooks(false)))
	var got []any
	for r, err := range Iter(ctx, e, stmt("SELECT")) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, r[0])
	}
	if fmt.Sprint(got) != "[1 2 3]" {
		t.Error(got)
	}
	expect(t, d, "pg#2 query SELECT []; pg#2 put back")
	if ev := rec.take(); !strings.Contains(ev, "end q replica false rows 3") {
		t.Error(ev)
	}

	// Stopping early closes the rows and releases the connection.
	for range Iter(ctx, e, stmt("SELECT")) {
		break
	}
	expect(t, d, "pg#3 query SELECT []; pg#3 put back")
	if ev := rec.take(); !strings.Contains(ev, "end q replica false rows 1") {
		t.Error(ev)
	}

	// Errors end the iteration: the statement's, the rows', and no source.
	for _, s := range []Statement{stmt("FAIL 23505"), {Query: "q", Catalog: "nowhere"}} {
		n := 0
		for r, err := range Iter(ctx, e, s) {
			if n++; r != nil || err == nil {
				t.Error(r, err)
			}
		}
		if n != 1 {
			t.Error(n)
		}
	}
	d.fail = map[string]error{"rows": errBroken}
	var last error
	for _, err := range Iter(ctx, e, stmt("SELECT")) {
		last = err
	}
	if ClassOf(last) != ClassConnectionError {
		t.Error(last)
	}
}

// bulkExec returns an executor whose dialect binds at most maxParams
// parameters per statement.
func bulkExec(t *testing.T, d *fakeDriver, maxParams int) *Executor {
	t.Helper()
	dialect := pg
	dialect.MaxParams = maxParams
	if d.handle == nil {
		d.handle = func(sql string, _ []any) ([]Row, int64, error) {
			if strings.Contains(sql, "FAIL") {
				return rows()(sql[strings.Index(sql, "FAIL"):], nil)
			}
			return []Row{{sql}}, 2, nil
		}
	}
	e, err := New(context.Background(), d, dialect, Config{
		Sources:  map[string]SourceConfig{"main": {Primary: ConnConfig{DSN: "pg", MaxConns: 3}}},
		Catalogs: map[string]string{"shop": "main"},
	}, withRand(func() float64 { return 0 }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	d.calls()
	return e
}

func insert(rows int, fail string) Bulk {
	return Bulk{Rows: rows, ParamsPerRow: 2, Statement: func(from, to int) Statement {
		s := stmt(fmt.Sprintf("INSERT %d-%d", from, to))
		if fmt.Sprintf("%d-%d", from, to) == fail {
			s.SQL += " FAIL 23505"
		}
		return s
	}}
}

func TestBulk(t *testing.T) {
	ctx := context.Background()
	d := &fakeDriver{}
	e := bulkExec(t, d, 4)

	// Five rows of two parameters split into three statements in one
	// transaction.
	n, err := ExecBulk(ctx, e, insert(5, ""))
	if err != nil || n != 6 {
		t.Fatal(n, err)
	}
	expect(t, d, "pg#2 begin; pg#2 exec INSERT 0-2 []; pg#2 exec INSERT 2-4 []; pg#2 exec INSERT 4-5 []; pg#2 commit; pg#2 put back")

	// One statement needs no transaction; no rows run nothing.
	if n, err := ExecBulk(ctx, e, insert(2, "")); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	expect(t, d, "pg#3 exec INSERT 0-2 []; pg#3 put back")
	if rs, err := FetchBulk(ctx, e, insert(0, "")); err != nil || rs == nil || len(rs) != 0 {
		t.Fatal(rs, err)
	}
	expect(t, d, "")

	// A transaction is joined, from its handle or its context.
	e.Write(ctx, func(ctx context.Context, tx *WriteTx) error {
		FetchBulk(ctx, tx, insert(3, ""))
		ExecBulk(ctx, e, insert(3, ""))
		return nil
	})
	expect(t, d, "pg#4 begin; pg#4 query INSERT 0-2 []; pg#4 query INSERT 2-3 []; pg#4 exec INSERT 0-2 []; pg#4 exec INSERT 2-3 []; pg#4 commit; pg#4 put back")

	// A failing chunk rolls back every chunk.
	if rs, err := FetchBulk(ctx, e, insert(5, "2-4")); rs != nil || ClassOf(err) != ClassUniqueViolation {
		t.Fatal(rs, err)
	}
	expect(t, d, "pg#5 begin; pg#5 query INSERT 0-2 []; pg#5 query INSERT 2-4 FAIL 23505 []; pg#5 rollback; pg#5 put back")

	// A rerun starts the count again.
	runs := 0
	d.handle = func(sql string, _ []any) ([]Row, int64, error) {
		if sql == "INSERT 2-3" {
			if runs++; runs == 1 {
				return rows()("FAIL 40001", nil)
			}
		}
		return []Row{{sql}}, 1, nil
	}
	if rs, err := FetchBulk(ctx, e, insert(3, "")); err != nil || len(rs) != 2 {
		t.Fatal(rs, err)
	}
	if n, err := ExecBulk(ctx, e, insert(3, "")); err != nil || n != 2 {
		t.Fatal(n, err)
	}
}

func TestBulkChunks(t *testing.T) {
	for _, c := range []struct {
		b         Bulk
		maxParams int
		want      int
	}{
		{Bulk{Rows: 10, ParamsPerRow: 3}, 0, 10},
		{Bulk{Rows: 0, ParamsPerRow: 3}, 0, 1},
		{Bulk{Rows: 10, ParamsPerRow: 0}, 100, 10},
		{Bulk{Rows: 10, ParamsPerRow: 3}, 65535, 21845},
		{Bulk{Rows: 10, ParamsPerRow: 50}, 20, 1},
	} {
		if got := c.b.chunk(c.maxParams); got != c.want {
			t.Errorf("%+v in %d: %d, want %d", c.b, c.maxParams, got, c.want)
		}
	}
}
