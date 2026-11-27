package shopdb_test

// This file is hand-written (§26.5): it measures the runtime's overhead per
// call of generated code against the fake driver, so the numbers count dql's
// own work (building statements, binding, validating, decoding) and not a
// database's.

import (
	"context"
	"testing"

	"github.com/thec1oud/dql/emitters/go/testdata/shopdb"
	"github.com/thec1oud/dql/runtime/go/dql"
	"github.com/thec1oud/dql/runtime/go/dqlfake"
)

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
