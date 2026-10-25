package dql

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

var writeTable = &Fragments{Placeholder: DollarNumbered, Templates: map[string]string{
	"delete.users":  `DELETE FROM "app"."users" AS u{where}`,
	"insert.users":  `INSERT INTO "app"."users" AS u ({cols}) VALUES {rows} RETURNING u."id"`,
	"name.users.a":  `"a"`,
	"name.users.b":  `"b"`,
	"values.row":    `({0})`,
	"value.default": `DEFAULT`,
	"clause.where":  ` WHERE {0}`,
	"bool.and":      `{0} AND {1}`,
	"op.eq":         `{0} = {1}`,
	"col.users.id":  `u."id"`,
	"col.users.t":   `u."t"`,
}}

func TestFiltered(t *testing.T) {
	scoped := &Query{Name: "user.delete_where", Catalog: "shop", Plan: []Node{Use{Fragment: "delete.users", Holes: map[string]Node{
		"where": Use{Fragment: "clause.where", Holes: map[string]Node{"0": eq("col.users.t", Param("scope.t"))}},
	}}}}
	bare := &Query{Name: "user.delete_where", Catalog: "shop", Plan: []Node{Use{Fragment: "delete.users"}}}
	id := Compare[userE]("op.eq", "col.users.id", Arg{Value: 7})
	for _, c := range []struct {
		q    *Query
		p    Pred[userE]
		want string
	}{
		{scoped, id, `DELETE FROM "app"."users" AS u WHERE u."t" = $1 AND u."id" = $2`},
		{bare, id, `DELETE FROM "app"."users" AS u WHERE u."id" = $1`},
		{scoped, AllRows[userE](), `DELETE FROM "app"."users" AS u WHERE u."t" = $1`},
		{bare, AllRows[userE](), `DELETE FROM "app"."users" AS u`},
	} {
		q, err := Filtered(c.q, c.p)
		if err != nil {
			t.Fatal(err)
		}
		if s, err := q.Statement(writeTable, Args{"scope.t": 1}); err != nil || s.SQL != c.want {
			t.Errorf("%v\n got %s\nwant %s", err, s.SQL, c.want)
		}
	}
	// The query itself is left as it was.
	if _, ok := bare.Plan[0].(Use).Holes["where"]; ok {
		t.Error("bare query changed")
	}
	if _, err := Filtered(scoped, Pred[userE]{}); err != errNoFilter {
		t.Error(err)
	}
}

func TestInsertMany(t *testing.T) {
	ctx := context.Background()
	d := &fakeDriver{}
	var sqls []string
	var args [][]any
	d.handle = func(sql string, a []any) ([]Row, int64, error) {
		sqls, args = append(sqls, sql), append(args, a)
		// One row back for each VALUES row, numbered from 1.
		var out []Row
		for i := range strings.Count(sql, "), (") + 1 {
			out = append(out, Row{int64(i + 1)})
		}
		return out, int64(len(out)), nil
	}
	e := bulkExec(t, d, 6)
	q := &Query{Name: "user.insert", Catalog: "shop", Kind: "insert", Writes: true, Sensitive: map[string]bool{"b": true}, Plan: []Node{Use{Fragment: "insert.users", Holes: map[string]Node{
		"cols": List{Items: []Node{Use{Fragment: "name.users.a"}, Use{Fragment: "name.users.b"}}, Separator: ", "},
		"rows": Use{Fragment: "values.row", Holes: map[string]Node{"0": List{Items: []Node{Param("a"), Param("b"), Arg{Value: 0}}, Separator: ", "}}},
	}}}}
	drafts := []Args{{"a": 1, "b": 2}, {"a": 3}, {"a": 5, "b": 6}}
	got, err := InsertMany(ctx, e, writeTable, q, drafts, decodeInt)
	if err != nil {
		t.Fatal(err)
	}
	// Two rows of three placeholders fill the limit of six: two statements.
	if fmt.Sprint(sqls) != `[INSERT INTO "app"."users" AS u ("a", "b") VALUES ($1, $2, $3), ($4, DEFAULT, $5) RETURNING u."id" INSERT INTO "app"."users" AS u ("a", "b") VALUES ($1, $2, $3) RETURNING u."id"]` ||
		fmt.Sprint(args) != "[[1 2 0 3 0] [5 6 0]]" || fmt.Sprint(got) != "[1 2 1]" {
		t.Errorf("%q %v %v", sqls, args, got)
	}

	// No rows, a row that does not assemble and a row that does not decode.
	if got, err := InsertMany(ctx, e, writeTable, q, nil, decodeInt); err != nil || len(got) != 0 {
		t.Error(got, err)
	}
	q.Plan = append(q.Plan, Param("missing"))
	if _, err := InsertMany(ctx, e, writeTable, q, drafts, decodeInt); err == nil {
		t.Error("assembled")
	}
	q.Plan = q.Plan[:1]
	d.handle = rows(Row{"x"})
	if _, err := InsertMany(ctx, e, writeTable, q, drafts[:1], decodeInt); ClassOf(err) != ClassDecodeError {
		t.Error(err)
	}
}
