package dql

import (
	"context"
	"fmt"
	"maps"
	"testing"
)

type userE struct{}

var builderTable = &Fragments{Placeholder: DollarNumbered, Templates: map[string]string{
	"select.user":      `SELECT u."id" FROM "app"."users" u`,
	"select.distinct":  `SELECT DISTINCT {cols} FROM {from}`,
	"from.users":       `"app"."users" u`,
	"col.users.id":     `u."id"`,
	"col.users.email":  `u."email"`,
	"op.eq":            `{0} = {1}`,
	"op.gt":            `{0} > {1}`,
	"bool.and":         `({0} AND {1})`,
	"bool.or":          `({0} OR {1})`,
	"bool.not":         `NOT ({0})`,
	"op.is_null":       `{0} IS NULL`,
	"value.true":       `TRUE`,
	"col.users.gone":   `u."gone"`,
	"clause.where":     ` WHERE {0}`,
	"clause.order":     ` ORDER BY {0}`,
	"clause.limit":     ` LIMIT {0}`,
	"clause.offset":    ` OFFSET {0}`,
	"order.asc":        `{0} ASC`,
	"order.desc":       `{0} DESC`,
	"lock.update":      ` FOR UPDATE`,
	"lock.share":       ` FOR SHARE`,
	"lock.skip_locked": ` SKIP LOCKED`,
	"lock.nowait":      ` NOWAIT`,
	"count.users":      `SELECT count(*) FROM "app"."users" u{where}`,
	"exists.users":     `SELECT EXISTS (SELECT FROM "app"."users" u{where})`,
}}

var userShape = Shape[int64, userE]{
	Name: "user.select", Catalog: "shop", Select: "select.user", From: "from.users",
	Columns: []string{"col.users.id"}, Count: "count.users", Exists: "exists.users",
	CountName: "user.count", ExistsName: "user.exists", Decode: decodeInt,
}

// noLimitTable is builderTable on a dialect, such as MySQL, that has no
// OFFSET without a LIMIT.
func noLimitTable() *Fragments {
	t := &Fragments{Placeholder: builderTable.Placeholder, Templates: maps.Clone(builderTable.Templates)}
	t.Templates["value.no_limit"] = "18446744073709551615"
	return t
}

// pageTable is a dialect, such as SQL Server, whose page is one clause that
// needs an ORDER BY and whose locks are table hints.
var pageTable = &Fragments{Placeholder: AtNumbered, Templates: map[string]string{
	"select.user":      `SELECT u.[id] FROM [app].[users] AS u{locks}`,
	"select.distinct":  `SELECT DISTINCT {cols} FROM {from}{locks}`,
	"from.users":       `[app].[users] AS u`,
	"col.users.id":     `u.[id]`,
	"clause.order":     ` ORDER BY {0}`,
	"clause.offset":    ` OFFSET {0} ROWS`,
	"clause.page":      ` OFFSET {offset} ROWS FETCH NEXT {limit} ROWS ONLY`,
	"order.asc":        `{0} ASC`,
	"order.none":       ` ORDER BY (SELECT NULL)`,
	"value.zero":       `0`,
	"lock.hints":       ` WITH ({0})`,
	"lock.update":      `UPDLOCK, ROWLOCK`,
	"lock.skip_locked": `READPAST`,
}}

func colEq(col string, v any) Pred[userE] {
	return NewPred[userE](Use{Fragment: "op.eq", Holes: map[string]Node{"0": Use{Fragment: col}, "1": Arg{Value: v}}})
}

func TestBuilderStatements(t *testing.T) {
	scope := From{Scope: []Node{colEq("col.users.id", int64(9)).Node()}}
	live := Use{Fragment: "op.is_null", Holes: map[string]Node{"0": Use{Fragment: "col.users.gone"}}}
	soft := From{Scope: scope.Scope, Visible: live}
	b := NewBuilder(builderTable, From{}, userShape)
	asc := NewOrder[userE](Use{Fragment: "order.asc", Holes: map[string]Node{"0": Use{Fragment: "col.users.id"}}})
	desc := NewOrder[userE](Use{Fragment: "order.desc", Holes: map[string]Node{"0": Use{Fragment: "col.users.email"}}})
	gt := NewPred[userE](Use{Fragment: "op.gt", Holes: map[string]Node{"0": Use{Fragment: "col.users.id"}, "1": Arg{Value: int64(1)}}})
	where := b.Where(Or(colEq("col.users.email", "a"), colEq("col.users.email", "b"), Not(gt)))
	for _, c := range []struct {
		name string
		b    Builder[int64, userE]
		agg  string
		want string
		args string
	}{
		{name: "plain", b: b, want: `SELECT u."id" FROM "app"."users" u`, args: "[]"},
		// A dialect without OFFSET alone limits to every row.
		{name: "offset alone", b: NewBuilder(noLimitTable(), From{}, userShape).Offset(5),
			want: `SELECT u."id" FROM "app"."users" u LIMIT 18446744073709551615 OFFSET $1`, args: "[5]"},
		{name: "offset with limit", b: NewBuilder(noLimitTable(), From{}, userShape).Limit(2).Offset(5),
			want: `SELECT u."id" FROM "app"."users" u LIMIT $1 OFFSET $2`, args: "[2 5]"},
		{name: "offset alone where it can be", b: b.Offset(5), want: `SELECT u."id" FROM "app"."users" u OFFSET $1`, args: "[5]"},
		{name: "where", b: where,
			want: `SELECT u."id" FROM "app"."users" u WHERE ((u."email" = $1 OR u."email" = $2) OR NOT (u."id" > $3))`, args: "[a b 1]"},
		{name: "scope and calls", b: NewBuilder(builderTable, scope, userShape).Where(And(gt, gt)).Where(gt),
			want: `SELECT u."id" FROM "app"."users" u WHERE ((u."id" = $1 AND (u."id" > $2 AND u."id" > $3)) AND u."id" > $4)`, args: "[9 1 1 1]"},
		{name: "soft delete", b: NewBuilder(builderTable, soft, userShape).Where(gt),
			want: `SELECT u."id" FROM "app"."users" u WHERE ((u."id" = $1 AND u."gone" IS NULL) AND u."id" > $2)`, args: "[9 1]"},
		{name: "with deleted", b: NewBuilder(builderTable, soft, userShape).OnlyDeleted().WithDeleted(),
			want: `SELECT u."id" FROM "app"."users" u WHERE u."id" = $1`, args: "[9]"},
		{name: "only deleted", b: NewBuilder(builderTable, soft, userShape).OnlyDeleted(), agg: "count",
			want: `SELECT count(*) FROM "app"."users" u WHERE (u."id" = $1 AND NOT (u."gone" IS NULL))`, args: "[9]"},
		{name: "only deleted without soft delete", b: b.OnlyDeleted(),
			want: `SELECT u."id" FROM "app"."users" u WHERE NOT (TRUE)`, args: "[]"},
		{name: "with deleted without soft delete", b: b.WithDeleted(), want: `SELECT u."id" FROM "app"."users" u`, args: "[]"},
		{name: "page", b: b.OrderBy(asc).OrderBy(desc).Limit(10).Offset(20),
			want: `SELECT u."id" FROM "app"."users" u ORDER BY u."id" ASC, u."email" DESC LIMIT $1 OFFSET $2`, args: "[10 20]"},
		{name: "distinct", b: b.Distinct(), want: `SELECT DISTINCT u."id" FROM "app"."users" u`, args: "[]"},
		{name: "locks", b: b.ForUpdate().SkipLocked(), want: `SELECT u."id" FROM "app"."users" u FOR UPDATE SKIP LOCKED`, args: "[]"},
		{name: "share", b: b.ForShare().NoWait(), want: `SELECT u."id" FROM "app"."users" u FOR SHARE NOWAIT`, args: "[]"},
		// A page is one clause, ordered by nothing when the call orders
		// nothing; locks are table hints on the from item.
		{name: "hinted page", b: NewBuilder(pageTable, From{}, userShape).Limit(10).Offset(20),
			want: `SELECT u.[id] FROM [app].[users] AS u ORDER BY (SELECT NULL) OFFSET @p1 ROWS FETCH NEXT @p2 ROWS ONLY`, args: "[20 10]"},
		{name: "hinted limit", b: NewBuilder(pageTable, From{}, userShape).OrderBy(asc).Limit(10),
			want: `SELECT u.[id] FROM [app].[users] AS u ORDER BY u.[id] ASC OFFSET 0 ROWS FETCH NEXT @p1 ROWS ONLY`, args: "[10]"},
		{name: "hinted offset", b: NewBuilder(pageTable, From{}, userShape).Offset(5),
			want: `SELECT u.[id] FROM [app].[users] AS u ORDER BY (SELECT NULL) OFFSET @p1 ROWS`, args: "[5]"},
		{name: "hinted plain", b: NewBuilder(pageTable, From{}, userShape), want: `SELECT u.[id] FROM [app].[users] AS u`, args: "[]"},
		{name: "hinted locks", b: NewBuilder(pageTable, From{}, userShape).ForUpdate().SkipLocked(),
			want: `SELECT u.[id] FROM [app].[users] AS u WITH (UPDLOCK, ROWLOCK, READPAST)`, args: "[]"},
		{name: "hinted distinct", b: NewBuilder(pageTable, From{}, userShape).Distinct().ForUpdate(),
			want: `SELECT DISTINCT u.[id] FROM [app].[users] AS u WITH (UPDLOCK, ROWLOCK)`, args: "[]"},
		{name: "count", b: where.OrderBy(asc).Limit(1), agg: "count",
			want: `SELECT count(*) FROM "app"."users" u WHERE ((u."email" = $1 OR u."email" = $2) OR NOT (u."id" > $3))`, args: "[a b 1]"},
		{name: "exists", b: b, agg: "exists", want: `SELECT EXISTS (SELECT FROM "app"."users" u)`, args: "[]"},
	} {
		var s Statement
		var err error
		switch c.agg {
		case "count":
			s, err = c.b.aggregate(userShape.CountName, "count", userShape.Count)
		case "exists":
			s, err = c.b.aggregate(userShape.ExistsName, "exists", userShape.Exists)
		default:
			s, err = c.b.rows()
		}
		if err != nil || s.SQL != c.want || fmt.Sprint(s.Args) != c.args {
			t.Errorf("%s: %v\n got %s %v\nwant %s %s", c.name, err, s.SQL, s.Args, c.want, c.args)
		}
	}

	// A builder is a value: a call leaves the builder it was made from as
	// it was.
	if s, _ := b.rows(); s.SQL != `SELECT u."id" FROM "app"."users" u` {
		t.Error(s.SQL)
	}

	// Locks write; NoLog and Consistency carry to the statement.
	s, _ := b.ForUpdate().NoLog().Consistency(Primary).rows()
	if !s.Writes || !s.NoLog || s.Consistency != Primary || s.Query != "user.select" || s.Catalog != "shop" {
		t.Errorf("%+v", s)
	}
	if s, _ := b.aggregate("user.count", "count", "count.users"); s.Writes || s.NoLog || s.Query != "user.count" {
		t.Errorf("%+v", s)
	}
}

func TestBuilderTerminals(t *testing.T) {
	ctx := context.Background()
	d := &fakeDriver{}
	e := newExec(t, d)
	b := NewBuilder(builderTable, From{H: e}, userShape)

	d.handle = rows(Row{int64(7)})
	if v, err := b.One(ctx); v != 7 || err != nil {
		t.Error(v, err)
	}
	if v, err := b.OptionalOne(ctx); fmt.Sprint(v) != "Some(7)" || err != nil {
		t.Error(v, err)
	}
	if v, err := b.All(ctx); fmt.Sprint(v) != "[7]" || err != nil {
		t.Error(v, err)
	}
	for v, err := range b.Iter(ctx) {
		if v != 7 || err != nil {
			t.Error(v, err)
		}
	}
	if n, err := b.Count(ctx); n != 7 || err != nil {
		t.Error(n, err)
	}
	d.handle = rows(Row{true})
	if ok, err := b.Exists(ctx); !ok || err != nil {
		t.Error(ok, err)
	}

	// A shape whose fragments the table lacks fails every terminal.
	broken := userShape
	broken.Select, broken.Count, broken.Exists = "select.missing", "count.missing", "exists.missing"
	bb := NewBuilder(builderTable, From{H: e}, broken)
	var errs []error
	errs = append(errs, second(bb.One(ctx)), second(bb.OptionalOne(ctx)), second(bb.All(ctx)), second(bb.Count(ctx)), second(bb.Exists(ctx)))
	n := 0
	for _, err := range bb.Iter(ctx) {
		n++
		errs = append(errs, err)
	}
	if n != 1 {
		t.Error("iter yielded", n)
	}
	for _, err := range errs {
		if err == nil {
			t.Error("no error")
		}
	}
}

type postE struct{}

func TestBuilderHelpers(t *testing.T) {
	table := &Fragments{Placeholder: DollarNumbered, Templates: map[string]string{
		"op.between":      `{0} BETWEEN {1} AND {2}`,
		"op.is_null":      `{0} IS NULL`,
		"col.users.id":    `u."id"`,
		"col.posts.title": `p."title"`,
		"op.eq":           `{0} = {1}`,
		"bool.and":        `{0} AND {1}`,
		"clause.and":      ` AND {0}`,
		"rel.users.posts": `EXISTS (SELECT 1 FROM "app"."posts" p WHERE p."author_id" = u."id"{and})`,
		"order.desc":      `{0} DESC`,
	}}
	title := Compare[postE]("op.eq", "col.posts.title", Arg{Value: "t"})
	for _, c := range []struct {
		n    Node
		want string
	}{
		{Compare[userE]("op.between", "col.users.id", Arg{Value: 1}, Arg{Value: 2}).Node(), `u."id" BETWEEN $1 AND $2`},
		{Compare[userE]("op.is_null", "col.users.id").Node(), `u."id" IS NULL`},
		{Related[userE, postE]("rel.users.posts").Node(), `EXISTS (SELECT 1 FROM "app"."posts" p WHERE p."author_id" = u."id")`},
		{Related[userE]("rel.users.posts", title, title).Node(), `EXISTS (SELECT 1 FROM "app"."posts" p WHERE p."author_id" = u."id" AND p."title" = $1 AND p."title" = $2)`},
		{Ordered[userE]("order.desc", "col.users.id").node, `u."id" DESC`},
	} {
		a, err := table.Assemble([]Node{c.n}, nil, nil, nil)
		if err != nil || a.SQL != c.want {
			t.Errorf("%v\n got %s\nwant %s", err, a.SQL, c.want)
		}
	}
}
