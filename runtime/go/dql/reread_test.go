package dql

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
)

// A MySQL-like table: no RETURNING, so a write's row is read back by key.
var rereadTable = &Fragments{Placeholder: Question, Templates: map[string]string{
	"insert.users":   "INSERT INTO `users` ({cols}) VALUES {rows}",
	"update.users":   "UPDATE `users` SET {sets}{where}",
	"select.user":    "SELECT `users`.`id` FROM `users`",
	"name.users.id":  "`id`",
	"name.users.a":   "`a`",
	"set.users.a":    "`a` = {0}",
	"values.row":     "({0})",
	"value.default":  "DEFAULT",
	"clause.where":   " WHERE {0}",
	"op.eq":          "{0} = {1}",
	"col.users.id":   "`users`.`id`",
	"clause.limit":   " LIMIT {0}",
	"clause.offset":  " OFFSET {0}",
	"value.no_limit": "18446744073709551615",
}}

func rereadInsert() *Query {
	return &Query{
		Name: "user.insert", Catalog: "shop", Kind: "insert", Writes: true,
		Plan: []Node{Use{Fragment: "insert.users", Holes: map[string]Node{
			"cols": List{Items: []Node{Use{Fragment: "name.users.id"}, Use{Fragment: "name.users.a"}}, Separator: ", "},
			"rows": Use{Fragment: "values.row", Holes: map[string]Node{"0": List{Items: []Node{Param("id"), Param("a")}, Separator: ", "}}},
		}}},
		Reread:    []Node{Use{Fragment: "select.user"}, Use{Fragment: "clause.where", Holes: map[string]Node{"0": eq("col.users.id", Param("id"))}}},
		Generated: "id",
	}
}

// A write that returns its row runs, then reads the row back by key in the
// same transaction, the key filled from the last insert ID where the call
// left it out (§20.1).
func TestReread(t *testing.T) {
	ctx := context.Background()
	d := &fakeDriver{insertID: func(string) int64 { return 41 }}
	var affected int64 = 1
	d.handle = func(sql string, args []any) ([]Row, int64, error) {
		if strings.HasPrefix(sql, "SELECT") {
			return []Row{{args[0]}}, 0, nil
		}
		return nil, affected, nil
	}
	e := newExec(t, d)
	s, err := rereadInsert().Statement(rereadTable, Args{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := OneOf(ctx, e, s, decodeInt); err != nil || got != 41 {
		t.Fatalf("insert: %d %v", got, err)
	}
	want := "begin; exec INSERT INTO `users` (`id`, `a`) VALUES (DEFAULT, ?) [1]; " +
		"query SELECT `users`.`id` FROM `users` WHERE `users`.`id` = ? [41]; commit; put back"
	if got := regexp.MustCompile(`pg#\d+ `).ReplaceAllString(d.calls(), ""); !strings.Contains(got, want) {
		t.Errorf("calls\n got %s\nwant %s", got, want)
	}
	// A key the call gives is read by its own value.
	s, _ = rereadInsert().Statement(rereadTable, Args{"id": int64(9), "a": 1})
	if got, err := OneOf(ctx, e, s, decodeInt); err != nil || got != 9 {
		t.Errorf("given key: %d %v", got, err)
	}
	// A write that affects nothing returns no row: NotFound for One, absent
	// for Optional, Missing for a versioned write.
	affected = 0
	d.calls()
	if _, err := OneOf(ctx, e, s, decodeInt); !errors.As(err, new(*NotFound)) {
		t.Errorf("no row: %v", err)
	}
	if got := d.calls(); strings.Contains(got, "SELECT") {
		t.Errorf("read after no write: %s", got)
	}
	if v, err := OptionalOf(ctx, e, s, decodeInt); err != nil || v.IsSome() {
		t.Errorf("optional: %v %v", v, err)
	}
	exists := Statement{Query: "user.find", Catalog: "shop", SQL: "SELECT 1"}
	d.handle = func(sql string, args []any) ([]Row, int64, error) {
		if sql == "SELECT 1" {
			return []Row{{int64(1)}}, 0, nil
		}
		return nil, 0, nil
	}
	if _, err := VersionedOne(ctx, e, s, exists, decodeInt); !errors.As(err, new(*ConcurrentModification)) {
		t.Errorf("versioned: %v", err)
	}
	// The write's and the read's errors come back.
	d.handle = func(string, []any) ([]Row, int64, error) { return nil, 0, errors.New("boom") }
	if _, err := OneOf(ctx, e, s, decodeInt); !errors.As(err, new(*DatabaseError)) {
		t.Errorf("write fails: %v", err)
	}
	d.handle = func(sql string, args []any) ([]Row, int64, error) {
		if strings.HasPrefix(sql, "SELECT") {
			return nil, 0, errors.New("read boom")
		}
		return nil, 1, nil
	}
	if _, err := OneOf(ctx, e, s, decodeInt); !errors.As(err, new(*DatabaseError)) || !strings.Contains(d.calls(), "rollback") {
		t.Errorf("read fails: %v", err)
	}
	// A read that does not assemble fails the write's transaction.
	bad := rereadInsert()
	bad.Reread = []Node{Use{Fragment: "select.nothing"}}
	s, _ = bad.Statement(rereadTable, Args{"a": 1})
	if _, err := OneOf(ctx, e, s, decodeInt); err == nil || !strings.Contains(err.Error(), "no select.nothing in query user.insert") {
		t.Errorf("bad read: %v", err)
	}
	// Only a writer writes.
	err = e.Read(ctx, func(ctx context.Context, tx *ReadTx) error {
		_, err := OneOf(ctx, tx, s, decodeInt)
		return err
	})
	if !errors.Is(err, ErrReadOnly) {
		t.Errorf("read transaction: %v", err)
	}
}

// Without RETURNING, InsertMany inserts and reads back one row at a time,
// in one transaction.
func TestInsertManyReread(t *testing.T) {
	ctx := context.Background()
	next := int64(0)
	d := &fakeDriver{insertID: func(sql string) int64 { next++; return next }}
	d.handle = func(sql string, args []any) ([]Row, int64, error) {
		if strings.HasPrefix(sql, "SELECT") {
			return []Row{{args[0]}}, 0, nil
		}
		return nil, 1, nil
	}
	e := newExec(t, d)
	got, err := InsertMany(ctx, e, rereadTable, rereadInsert(), []Args{{"a": 1}, {"a": 2}, {"id": int64(7), "a": 3}}, decodeInt)
	if err != nil || len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 7 {
		t.Fatalf("%v %v", got, err)
	}
	if c := d.calls(); strings.Count(c, "begin") != 1 || strings.Count(c, "INSERT") != 3 || strings.Count(c, "SELECT") != 3 {
		t.Errorf("calls %s", c)
	}
	bad := rereadInsert()
	bad.Plan = []Node{Use{Fragment: "insert.nothing"}}
	if _, err := InsertMany(ctx, e, rereadTable, bad, []Args{{"a": 1}}, decodeInt); err == nil {
		t.Error("an insert that does not assemble")
	}
	d.handle = func(string, []any) ([]Row, int64, error) { return nil, 0, errors.New("boom") }
	if _, err := InsertMany(ctx, e, rereadTable, rereadInsert(), []Args{{"a": 1}}, decodeInt); err == nil {
		t.Error("a failing insert")
	}
}
