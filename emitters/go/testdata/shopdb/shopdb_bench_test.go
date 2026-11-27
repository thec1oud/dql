package shopdb_test

// This file is hand-written (§26.5): it measures the runtime's overhead per
// call of generated code against the fake driver, so the numbers count dql's
// own work (building statements, binding, validating, decoding) and not a
// database's.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/thec1oud/dql/emitters/go/testdata/shopdb"
	"github.com/thec1oud/dql/runtime/go/dql"
	"github.com/thec1oud/dql/runtime/go/dqlfake"
)

// TestBudgets holds the runtime to its performance budgets: at most 10µs
// per call, and a fixed number of allocations per call. It runs only with
// DQL_BUDGETS set, as the budgets CI job does: times taken under coverage
// or alongside other tests mean nothing.
func TestBudgets(t *testing.T) {
	if os.Getenv("DQL_BUDGETS") == "" {
		t.Skip("DQL_BUDGETS is not set")
	}
	const budget = 10 * time.Microsecond
	for _, c := range []struct {
		name   string
		bench  func(*testing.B)
		allocs int64
	}{
		{"Find", BenchmarkFind, 20},
		{"Count", BenchmarkCount, 14},
		{"Insert", BenchmarkInsert, 22},
	} {
		r := testing.Benchmark(c.bench)
		per := time.Duration(r.NsPerOp())
		switch {
		case r.N == 0:
			t.Errorf("%s did not run", c.name)
		case per > budget:
			t.Errorf("%s took %v per call, over its budget of %v", c.name, per, budget)
		case r.AllocsPerOp() > c.allocs:
			t.Errorf("%s made %d allocations per call, over its budget of %d", c.name, r.AllocsPerOp(), c.allocs)
		default:
			t.Logf("%s took %v and %d allocations per call", c.name, per, r.AllocsPerOp())
		}
	}
}

// fake returns a generated DB on a fake driver answering every call of
// query with row.
func fake(b *testing.B, query string, row dql.Row) shopdb.DB {
	b.Helper()
	f := dqlfake.New()
	f.OnAny(query).Return(row)
	return shopdb.New(f.Executor(b, shopdb.Dialect, []string{"shop"}))
}

// BenchmarkFind reads one row by its primary key.
func BenchmarkFind(b *testing.B) {
	ctx := context.Background()
	db := fake(b, "tag.find.by_primary", dql.Row{int64(1), "go"})
	b.ReportAllocs()
	for b.Loop() {
		if _, err := db.Tags.Find(ctx, shopdb.TagKey{ID: 1}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCount reads one aggregate.
func BenchmarkCount(b *testing.B) {
	ctx := context.Background()
	db := fake(b, "tag.count", dql.Row{int64(5)})
	b.ReportAllocs()
	for b.Loop() {
		if _, err := db.Tags.Count(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkInsert validates, binds and writes one row, returning it.
func BenchmarkInsert(b *testing.B) {
	ctx := context.Background()
	db := fake(b, "tag.insert", dql.Row{int64(1), "go"})
	b.ReportAllocs()
	for b.Loop() {
		if _, err := db.Tags.Insert(ctx, shopdb.TagDraft{Name: "go"}); err != nil {
			b.Fatal(err)
		}
	}
}
