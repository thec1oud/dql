package dql

import (
	"fmt"
	"strings"
	"testing"
)

var table = &Fragments{Placeholder: DollarNumbered, Templates: map[string]string{
	"insert.users":       `INSERT INTO "app"."users" AS u ({cols}) VALUES {rows}{returning}`,
	"update.users":       `UPDATE "app"."users" AS u SET {sets}{where}{returning}`,
	"name.users.email":   `"email"`,
	"name.users.name":    `"name"`,
	"set.users.email":    `"email" = {0}`,
	"set.users.name":     `"name" = {0}`,
	"values.row":         `({0})`,
	"value.default":      `DEFAULT`,
	"clause.where":       ` WHERE {0}`,
	"op.eq":              `{0} = {1}`,
	"col.users.id":       `u."id"`,
	"returning.user":     ` RETURNING u."id"`,
	"op.json":            `{0} @> '{"a": 1}' AND '{}' <> {1} AND '{x' = '{X}'`,
	"select.user":        `SELECT u."id" FROM "app"."users" u`,
	"clause.limit":       ` LIMIT {0}`,
	"wrap.default_hole":  `x{missing}y`,
	"set.users.computed": `"n" = {0}`,
}}

func eq(col string, v Node) Use {
	return Use{Fragment: "op.eq", Holes: map[string]Node{"0": Use{Fragment: col}, "1": v}}
}

func TestAssemble(t *testing.T) {
	insert := []Node{Use{Fragment: "insert.users", Holes: map[string]Node{
		"cols":      List{Items: []Node{Use{Fragment: "name.users.email"}, Use{Fragment: "name.users.name"}}, Separator: ", "},
		"rows":      Use{Fragment: "values.row", Holes: map[string]Node{"0": List{Items: []Node{Param("email"), Param("name")}, Separator: ", "}}},
		"returning": Use{Fragment: "returning.user"},
	}}}
	update := []Node{Use{Fragment: "update.users", Holes: map[string]Node{
		"sets": List{Items: []Node{
			Use{Fragment: "set.users.email", Holes: map[string]Node{"0": Param("email")}},
			Use{Fragment: "set.users.name", Holes: map[string]Node{"0": Param("name")}},
			Use{Fragment: "set.users.computed", Holes: map[string]Node{"0": Use{Fragment: "op.eq", Holes: map[string]Node{"0": Param("name"), "1": Param("email")}}}},
		}, Separator: ", "},
		"where": Use{Fragment: "clause.where", Holes: map[string]Node{"0": eq("col.users.id", Param("key.id"))}},
	}}}
	for _, c := range []struct {
		name  string
		t     *Fragments
		plan  []Node
		args  Args
		want  string
		vals  string
		sens  string
		error string
	}{
		{name: "insert", t: table, plan: insert, args: Args{"email": "a@b.c", "name": "A"},
			want: `INSERT INTO "app"."users" AS u ("email", "name") VALUES ($1, $2) RETURNING u."id"`, vals: "[a@b.c A]", sens: "[true false]"},
		{name: "insert default", t: table, plan: insert, args: Args{"name": "A"},
			want: `INSERT INTO "app"."users" AS u ("email", "name") VALUES (DEFAULT, $1) RETURNING u."id"`, vals: "[A]", sens: "[false]"},
		{name: "update", t: table, plan: update, args: Args{"email": "a@b.c", "name": nil, "key.id": 7},
			want: `UPDATE "app"."users" AS u SET "email" = $1, "name" = $2, "n" = $3 = $4 WHERE u."id" = $5`, vals: "[a@b.c <nil> <nil> a@b.c 7]", sens: "[true false false true false]"},
		{name: "update drops", t: table, plan: update, args: Args{"name": "A", "key.id": 7},
			want: `UPDATE "app"."users" AS u SET "name" = $1 WHERE u."id" = $2`, vals: "[A 7]", sens: "[false false]"},
		{name: "question", t: &Fragments{Placeholder: Question, Templates: table.Templates}, plan: update, args: Args{"email": "e", "key.id": 7},
			want: `UPDATE "app"."users" AS u SET "email" = ? WHERE u."id" = ?`, vals: "[e 7]", sens: "[true false]"},
		{name: "at", t: &Fragments{Placeholder: AtNumbered, Templates: table.Templates}, plan: []Node{Use{Fragment: "select.user"}, Use{Fragment: "clause.limit", Holes: map[string]Node{"0": Arg{Value: 20, Sensitive: true}}}},
			want: `SELECT u."id" FROM "app"."users" u LIMIT @p1`, vals: "[20]", sens: "[true]"},
		{name: "braces", t: table, plan: []Node{Use{Fragment: "op.json", Holes: map[string]Node{"0": Arg{Value: 1}, "1": Arg{Value: 2}}}},
			want: `$1 @> '{"a": 1}' AND '{}' <> $2 AND '{x' = '{X}'`, vals: "[1 2]", sens: "[false false]"},
		{name: "unfilled hole", t: table, plan: []Node{Use{Fragment: "wrap.default_hole"}}, want: "xy", vals: "[]", sens: "[]"},
		{name: "absent key", t: table, plan: update, args: Args{"name": "A"}, error: "dql: parameter key.id is left out: absent"},
		{name: "no fragment", t: table, plan: []Node{Use{Fragment: "nope"}}, error: "dql: the fragment table has no nope"},
		{name: "no fragment in list", t: table, plan: []Node{List{Items: []Node{Use{Fragment: "nope"}}}}, error: "dql: the fragment table has no nope"},
		{name: "no default", t: &Fragments{Templates: map[string]string{}}, plan: []Node{List{Items: []Node{Param("x")}}}, error: "dql: the fragment table has no value.default"},
	} {
		a, err := c.t.Assemble(c.plan, c.args, map[string]bool{"email": true})
		if c.error != "" {
			if err == nil || err.Error() != c.error {
				t.Errorf("%s: %v", c.name, err)
			}
			continue
		}
		if err != nil || a.SQL != c.want || fmt.Sprint(a.Args) != c.vals || fmt.Sprint(a.Sensitive) != c.sens {
			t.Errorf("%s:\n%s\n%v %v %v", c.name, a.SQL, a.Args, a.Sensitive, err)
		}
	}
}

func TestQueryStatement(t *testing.T) {
	q := &Query{
		Name: "user.get", Catalog: "shop", Kind: "select", AtMostOne: true, Consistency: Any, Sensitive: map[string]bool{"key.id": true},
		Plan: []Node{Use{Fragment: "select.user"}, Use{Fragment: "clause.where", Holes: map[string]Node{"0": eq("col.users.id", Param("key.id"))}}},
	}
	s, err := q.Statement(table, Args{"key.id": 7})
	if err != nil || s.SQL != `SELECT u."id" FROM "app"."users" u WHERE u."id" = $1` || fmt.Sprint(s.Args, s.Sensitive) != "[7] [true]" ||
		s.Query != "user.get" || s.Catalog != "shop" || s.Kind != "select" || !s.AtMostOne || s.Consistency != Any || s.Writes {
		t.Errorf("%+v %v", s, err)
	}
	if _, err := q.Statement(table, nil); err == nil || !strings.HasSuffix(err.Error(), "left out: absent in query user.get") {
		t.Error(err)
	}
}
