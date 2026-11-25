package dql

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// §17, §22.1: the raw escape hatch runs SQL text on a source picked by name,
// on its primary or in the transaction there, reported as query raw.
func TestRaw(t *testing.T) {
	ctx := context.Background()
	d := &fakeDriver{handle: rows(Row{int64(7)})}
	rec := &recorder{}
	e := newExec(t, d, WithHooks(rec.hooks(false)))

	got, err := RawWrite(e, "main").Query(ctx, "SELECT 7")
	if err != nil || fmt.Sprint(got) != "[[7]]" {
		t.Fatal(got, err)
	}
	if n, err := RawWrite(e, "main").Exec(ctx, "UPDATE t SET a = $1", 1); n != 1 || err != nil {
		t.Fatal(n, err)
	}
	if got, err := Raw(e, "main").Query(ctx, "SELECT 7"); err != nil || len(got) != 1 {
		t.Fatal(got, err)
	}
	expect(t, d, "pg#2 query SELECT 7 []; pg#2 put back; pg#3 exec UPDATE t SET a = $1 [1]; pg#3 put back; pg#4 query SELECT 7 []; pg#4 put back")
	if lines := rec.take(); !strings.HasPrefix(lines, `start raw raw /main primary attempt 1 sql "SELECT 7" params []`) {
		t.Errorf("hooks:\n%s", lines)
	}

	// It joins the transaction the context carries, on that source.
	err = e.Write(ctx, func(ctx context.Context, tx *WriteTx) error {
		if _, err := Exec(ctx, tx, stmt("INSERT")); err != nil {
			return err
		}
		_, err := RawWrite(e, "main").Exec(ctx, "DELETE FROM t")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	expect(t, d, "pg#5 begin; pg#5 exec INSERT []; pg#5 exec DELETE FROM t []; pg#5 commit; pg#5 put back")

	// A read transaction's raw reader reads in it, and has no Exec.
	err = e.Read(ctx, func(ctx context.Context, tx *ReadTx) error {
		_, err := Raw(tx, "main").Query(ctx, "SELECT 1")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	// An unknown source, and a failure the dialect maps.
	if _, err := Raw(e, "nope").Query(ctx, "SELECT 1"); !errors.Is(err, ErrUnknownSource) || err.Error() != "dql: no source is named: nope" {
		t.Error(err)
	}
	if _, err := RawWrite(e, "main").Exec(ctx, "FAIL 23505"); ClassOf(err) != ClassUniqueViolation || !strings.Contains(err.Error(), "in raw") {
		t.Error(err)
	}
	if _, err := RawWrite(e, "main").Query(ctx, "FAIL 23505"); ClassOf(err) != ClassUniqueViolation {
		t.Error(err)
	}
}
