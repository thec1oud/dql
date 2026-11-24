package dql

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func decodeInt(r Row) (int64, error) {
	d := NewDecoder("n", r, 1)
	n := Field(d, 0, "n", "", Int[int64])
	return n, d.Err()
}

func TestTerminals(t *testing.T) {
	ctx := context.Background()
	d := &fakeDriver{}
	e := newExec(t, d)
	answer := func(rs ...Row) { d.handle = rows(rs...) }

	answer(Row{int64(7)})
	if v, err := OneOf(ctx, e, stmt("SELECT"), decodeInt); v != 7 || err != nil {
		t.Error(v, err)
	}
	if v, err := OptionalOf(ctx, e, stmt("SELECT"), decodeInt); fmt.Sprint(v) != "Some(7)" || err != nil {
		t.Error(v, err)
	}
	if v, err := AllOf(ctx, e, stmt("SELECT"), decodeInt); fmt.Sprint(v) != "[7]" || err != nil {
		t.Error(v, err)
	}
	if n, err := Count(ctx, e, stmt("SELECT")); n != 7 || err != nil {
		t.Error(n, err)
	}
	answer(Row{true})
	if ok, err := Exists(ctx, e, stmt("SELECT")); !ok || err != nil {
		t.Error(ok, err)
	}

	// No rows: NotFound, absent, empty.
	answer()
	if _, err := OneOf(ctx, e, stmt("SELECT"), decodeInt); ClassOf(err) != ClassNotFound {
		t.Error(err)
	}
	if v, err := OptionalOf(ctx, e, stmt("SELECT"), decodeInt); v.IsSome() || err != nil {
		t.Error(v, err)
	}
	if v, err := AllOf(ctx, e, stmt("SELECT"), decodeInt); v == nil || len(v) != 0 || err != nil {
		t.Error(v, err)
	}

	// Rows that do not decode.
	answer(Row{"x"})
	for _, err := range []error{
		second(OneOf(ctx, e, stmt("SELECT"), decodeInt)),
		second(OptionalOf(ctx, e, stmt("SELECT"), decodeInt)),
		second(AllOf(ctx, e, stmt("SELECT"), decodeInt)),
		second(Count(ctx, e, stmt("SELECT"))),
		second(Exists(ctx, e, stmt("SELECT"))),
	} {
		if ClassOf(err) != ClassDecodeError {
			t.Error(err)
		}
	}

	// Failing statements.
	for _, err := range []error{
		second(OptionalOf(ctx, e, stmt("FAIL 23505"), decodeInt)),
		second(AllOf(ctx, e, stmt("FAIL 23505"), decodeInt)),
	} {
		if ClassOf(err) != ClassUniqueViolation {
			t.Error(err)
		}
	}
}

func second[T any](_ T, err error) error { return err }

func TestIterOf(t *testing.T) {
	ctx := context.Background()
	d := &fakeDriver{handle: rows(Row{int64(1)}, Row{int64(2)}, Row{"x"})}
	e := newExec(t, d)
	var got []int64
	var last error
	for v, err := range IterOf(ctx, e, stmt("SELECT"), decodeInt) {
		if err != nil {
			last = err
			continue
		}
		got = append(got, v)
	}
	if fmt.Sprint(got) != "[1 2]" || ClassOf(last) != ClassDecodeError {
		t.Error(got, last)
	}
	for range IterOf(ctx, e, stmt("SELECT"), decodeInt) {
		break
	}
	n := 0
	for _, err := range IterOf(ctx, e, stmt("FAIL 23505"), decodeInt) {
		if n++; ClassOf(err) != ClassUniqueViolation {
			t.Error(err)
		}
	}
	if n != 1 {
		t.Error(n)
	}
}

func TestAtomicAndMissing(t *testing.T) {
	ctx := context.Background()
	d := &fakeDriver{}
	e := newExec(t, d)
	d.handle = rows(Row{int64(1)})
	err := Atomic(ctx, e, func(ctx context.Context, w Writer) error {
		return Missing(ctx, w, "user.patch", stmt("SELECT"))
	})
	var cm *ConcurrentModification
	if !errors.As(err, &cm) || cm.Query != "user.patch" || cm.Dialect != "postgres" {
		t.Error(err)
	}
	expect(t, d, "pg#2 begin; pg#2 query SELECT []; pg#2 rollback; pg#2 put back")

	// A write transaction's handle is joined.
	d.handle = rows()
	e.Write(ctx, func(ctx context.Context, tx *WriteTx) error {
		err := Atomic(context.Background(), tx, func(ctx context.Context, w Writer) error {
			return Missing(ctx, w, "user.patch", stmt("SELECT"))
		})
		if ClassOf(err) != ClassNotFound {
			t.Error(err)
		}
		return nil
	})
	expect(t, d, "pg#3 begin; pg#3 query SELECT []; pg#3 commit; pg#3 put back")
	if err := Missing(ctx, e, "q", stmt("FAIL 23505")); ClassOf(err) != ClassUniqueViolation {
		t.Error(err)
	}
}

func TestVersioned(t *testing.T) {
	ctx := context.Background()
	d := &fakeDriver{}
	e := newExec(t, d)
	// The write matches: its row decodes; a row that does not decode fails.
	d.handle = rows(Row{int64(3)})
	if v, err := VersionedOne(ctx, e, stmt("UPDATE"), stmt("SELECT"), decodeInt); v != 3 || err != nil {
		t.Error(v, err)
	}
	expect(t, d, "pg#2 begin; pg#2 query UPDATE []; pg#2 commit; pg#2 put back")
	d.handle = rows(Row{"x"})
	if _, err := VersionedOne(ctx, e, stmt("UPDATE"), stmt("SELECT"), decodeInt); ClassOf(err) != ClassDecodeError {
		t.Error(err)
	}
	if err := VersionedExec(ctx, e, stmt("DELETE"), stmt("SELECT")); err != nil {
		t.Error(err)
	}
	// No row matches, and the key exists.
	d.handle = func(sql string, _ []any) ([]Row, int64, error) {
		if sql == "SELECT" {
			return []Row{{int64(1)}}, 1, nil
		}
		return rows()(sql, nil)
	}
	d.calls()
	if _, err := VersionedOne(ctx, e, Statement{Query: "user.patch", Catalog: "shop", SQL: "UPDATE"}, stmt("SELECT"), decodeInt); ClassOf(err) != ClassConcurrentModification {
		t.Error(err)
	}
	expect(t, d, "pg#5 begin; pg#5 query UPDATE []; pg#5 query SELECT []; pg#5 rollback; pg#5 put back")
	if err := VersionedExec(ctx, e, stmt("DELETE"), stmt("SELECT")); ClassOf(err) != ClassConcurrentModification {
		t.Error(err)
	}
	// The write fails.
	if _, err := VersionedOne(ctx, e, stmt("FAIL 23505"), stmt("SELECT"), decodeInt); ClassOf(err) != ClassUniqueViolation {
		t.Error(err)
	}
	if err := VersionedExec(ctx, e, stmt("FAIL 23505"), stmt("SELECT")); ClassOf(err) != ClassUniqueViolation {
		t.Error(err)
	}
}
