package dql

import (
	"errors"
	"testing"
)

var setTable = &Fragments{Placeholder: DollarNumbered, Templates: map[string]string{
	"update.users":  `UPDATE "app"."users" AS u SET {sets}`,
	"insert.users":  `INSERT INTO "app"."users" AS u ("a", "b") VALUES {rows}`,
	"set.users.a":   `"a" = {0}`,
	"set.users.b":   `"b" = {0}`,
	"col.users.b":   `u."b"`,
	"values.row":    `({0})`,
	"value.default": `DEFAULT`,
	"value.now":     `now()`,
	"op.add":        `({0} + {1})`,
}}

func TestAssign(t *testing.T) {
	update := &Query{Name: "user.patch", Plan: []Node{Use{Fragment: "update.users", Holes: map[string]Node{"sets": List{Items: []Node{
		Use{Fragment: "set.users.a", Holes: map[string]Node{"0": Param("a")}},
		Use{Fragment: "set.users.b", Holes: map[string]Node{"0": Param("b")}},
	}, Separator: ", "}}}}}
	insert := &Query{Name: "user.insert", Plan: []Node{Use{Fragment: "insert.users", Holes: map[string]Node{"rows": Use{Fragment: "values.row", Holes: map[string]Node{
		"0": List{Items: []Node{Param("a"), Param("b")}, Separator: ", "},
	}}}}}}
	for _, c := range []struct {
		q    *Query
		args Args
		as   []Update[userE]
		want string
	}{
		{update, Args{"a": 1}, []Update[userE]{Arith[userE]("op.add", "b", "col.users.b", Arg{Value: 2})}, `UPDATE "app"."users" AS u SET "a" = $1, "b" = (u."b" + $2)`},
		{update, Args{}, []Update[userE]{Set[userE]("a", Now()), Set[userE]("b", Default())}, `UPDATE "app"."users" AS u SET "a" = now(), "b" = DEFAULT`},
		{insert, Args{"b": 1}, Values([]Assignment[userE]{Set[userE]("a", Now())}), `INSERT INTO "app"."users" AS u ("a", "b") VALUES (now(), $1)`},
		{insert, Args{}, nil, `INSERT INTO "app"."users" AS u ("a", "b") VALUES (DEFAULT, DEFAULT)`},
	} {
		if err := Assign(c.q, "user.partial.flat", c.args, c.as); err != nil {
			t.Fatal(err)
		}
		if s, err := c.q.Statement(setTable, c.args); err != nil || s.SQL != c.want {
			t.Errorf("%v\n got %s\nwant %s", err, s.SQL, c.want)
		}
	}

	// A field the payload sets too is FieldSetTwice; one the query does not
	// write is an error.
	err := Assign(update, "user.partial.flat", Args{"a": 1}, []Update[userE]{Set[userE]("a", Now()), Set[userE]("b", Now())})
	var v ValidationErrors
	if !errors.As(err, &v) || len(v) != 1 || v[0].Shape != "user.partial.flat" || v[0].Field != "a" || v[0].Kind != KindFieldSetTwice {
		t.Errorf("twice: %v", err)
	}
	if err := Assign(update, "user.partial.flat", Args{}, []Update[userE]{Set[userE]("c", Now())}); err == nil || err.Error() != "dql: user.patch does not write c" {
		t.Errorf("unwritten: %v", err)
	}
}
