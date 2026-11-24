package dqlfake

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/thec1oud/dql/runtime/go/dql"
)

var dialect = dql.Dialect{Name: "postgres"}

func stmt(query string, args ...any) dql.Statement {
	return dql.Statement{Query: query, Catalog: "shop", SQL: "SQL of " + query, Args: args}
}

// tb records what a test would report.
type tb struct {
	errors   []string
	cleanups []func()
}

func (t *tb) Errorf(format string, args ...any) {
	t.errors = append(t.errors, fmt.Sprintf(format, args...))
}
func (t *tb) Cleanup(f func()) { t.cleanups = append(t.cleanups, f) }
func (t *tb) end() {
	for _, f := range t.cleanups {
		f()
	}
}

func TestFake(t *testing.T) {
	ctx := context.Background()
	f := New()
	get := f.On("users.get", int64(1)).Return(dql.Row{int64(1), "a@b.c"})
	f.On("users.get", int64(2)).Fail(&dql.NotFound{})
	f.OnAny("users.delete").Affected(3)
	f.On("users.count").Return(dql.Row{int64(5)})
	f.On("users.count").Return(dql.Row{int64(6)}) // later wins
	var t2 tb
	var queries []string
	e := f.Executor(&t2, dialect, []string{"shop"}, dql.WithHooks(dql.Hooks{QueryEnd: func(_ context.Context, q dql.QueryEvent) {
		queries = append(queries, q.Query)
	}}))

	if r, err := dql.One(ctx, e, stmt("users.get", int64(1))); err != nil || r[1] != "a@b.c" {
		t.Error(r, err)
	}
	if _, err := dql.One(ctx, e, stmt("users.get", int64(2))); dql.ClassOf(err) != dql.ClassNotFound {
		t.Error(err)
	}
	if r, err := dql.One(ctx, e, stmt("users.count")); err != nil || r[0] != int64(6) {
		t.Error(r, err)
	}
	err := e.Write(ctx, func(ctx context.Context, tx *dql.WriteTx) error {
		e.Write(ctx, func(ctx context.Context, tx *dql.WriteTx) error {
			dql.Exec(ctx, tx, stmt("users.delete"))
			return errors.New("undo")
		}, dql.WithPropagation(dql.Nested))
		e.Write(ctx, func(ctx context.Context, tx *dql.WriteTx) error {
			_, err := dql.Exec(ctx, tx, stmt("users.delete"))
			return err
		}, dql.WithPropagation(dql.Nested))
		if _, err := dql.Exec(ctx, tx, stmt("users.delete", "x")); err != nil {
			return err
		}
		return errors.New("roll back")
	})
	if err == nil {
		t.Fatal("no error")
	}
	if err := e.Write(ctx, func(ctx context.Context, tx *dql.WriteTx) error {
		n, err := dql.Exec(ctx, tx, stmt("users.delete"))
		if n != 3 {
			t.Error(n)
		}
		return err
	}); err != nil {
		t.Error(err)
	}
	if get.Calls() != 1 || len(queries) != 7 {
		t.Error(get.Calls(), queries)
	}

	calls := f.Calls()
	if len(calls) != 7 || !calls[5].InTx || calls[0].InTx || calls[0].SQL != "SQL of users.get" || calls[5].Args[0] != "x" {
		t.Errorf("%+v", calls)
	}

	// An unexpected call fails, and fails the test at cleanup.
	_, err = dql.Fetch(ctx, e, stmt("users.get", int64(3)))
	var u *Unexpected
	if !errors.As(err, &u) || u.Call.Query != "users.get" || !strings.Contains(err.Error(), "unexpected call of users.get with [3]") {
		t.Error(err)
	}
	if _, err := dql.Exec(ctx, e, stmt("users.patch")); err == nil {
		t.Error("no error")
	}
	if len(f.Unexpected()) != 2 {
		t.Error(f.Unexpected())
	}
	t2.end()
	if len(t2.errors) != 2 || t2.errors[1] != "dqlfake: unexpected call of users.patch with []" {
		t.Error(t2.errors)
	}

	// The fake sends no database errors and never breaks.
	if _, ok := f.Native(err); ok || f.Broken(err) {
		t.Error("native or broken")
	}
}
