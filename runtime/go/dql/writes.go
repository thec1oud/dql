package dql

import (
	"context"
	"errors"
	"strconv"
)

// errNoFilter is the error of a filtered write given the zero predicate.
var errNoFilter = errors.New("dql: a filtered write needs a filter; pass the table's AllRows() to write every row")

// AllRows is the filter that keeps every row, which a filtered write must be
// given explicitly (§20.1); generated tables' AllRows call it.
func AllRows[E any]() Pred[E] { return Pred[E]{all: true} }

// Filtered returns q with p added to the filter of its WHERE, which holds
// the scope's and the soft delete's (§20.1); generated filtered writes call
// it. AllRows leaves q as it is; the zero predicate is an error, so an
// empty filter never happens by accident.
func Filtered[E any](q *Query, p Pred[E]) (*Query, error) {
	if p.all {
		return q, nil
	}
	if p.node == nil {
		return nil, errNoFilter
	}
	out := *q
	out.Plan = append([]Node(nil), q.Plan...)
	u := out.Plan[0].(Use)
	holes := map[string]Node{}
	for k, v := range u.Holes {
		holes[k] = v
	}
	filter := p.node
	if w, ok := holes["where"].(Use); ok {
		filter = Use{Fragment: "bool.and", Holes: map[string]Node{"0": w.Holes["0"], "1": filter}}
	}
	holes["where"] = Use{Fragment: "clause.where", Holes: map[string]Node{"0": filter}}
	u.Holes = holes
	out.Plan[0] = u
	return &out, nil
}

// InsertMany inserts every row with q, whose plan writes one VALUES row
// from named parameters, and decodes the rows it returns, in order
// (§20.1, §25.2). It repeats the row for each, so the rows split into
// statements within the dialect's parameter limit, all in one
// transaction.
func InsertMany[T any](ctx context.Context, h Writer, t *Fragments, q *Query, rows []Args, decode func(Row) (T, error)) ([]T, error) {
	per := 0
	for _, node := range q.Plan {
		per += placeholders(node, false)
	}
	b := Bulk{Rows: len(rows), ParamsPerRow: per, Statement: func(from, to int) (Statement, error) {
		many := *q
		args := Args{}
		sensitive := map[string]bool{}
		many.Plan = repeatRows(q.Plan, to-from)
		for i, r := range rows[from:to] {
			for k, v := range r {
				args[k+"#"+strconv.Itoa(i)] = v
			}
			for k, v := range q.Sensitive {
				sensitive[k+"#"+strconv.Itoa(i)] = v
			}
		}
		many.Sensitive = sensitive
		return many.Statement(t, args)
	}}
	fetched, err := FetchBulk(ctx, h, b)
	if err != nil {
		return nil, err
	}
	out := make([]T, 0, len(fetched))
	for _, r := range fetched {
		v, err := decode(r)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// placeholders counts the placeholders of the values.row uses under node:
// its parameters and values.
func placeholders(node Node, inRow bool) int {
	n := 0
	switch v := node.(type) {
	case Use:
		for _, h := range v.Holes {
			n += placeholders(h, inRow || v.Fragment == "values.row")
		}
	case List:
		for _, it := range v.Items {
			n += placeholders(it, inRow)
		}
	case Param, Arg:
		if inRow {
			n = 1
		}
	}
	return n
}

// repeatRows returns plan with each values.row use repeated n times, its
// parameters numbered #0 to #n-1.
func repeatRows(plan []Node, n int) []Node {
	out := make([]Node, len(plan))
	for i, node := range plan {
		out[i] = repeat(node, n)
	}
	return out
}

func repeat(node Node, n int) Node {
	switch v := node.(type) {
	case Use:
		if v.Fragment == "values.row" {
			var rows []Node
			for i := range n {
				rows = append(rows, rename(v, "#"+strconv.Itoa(i)))
			}
			return List{Items: rows, Separator: ", "}
		}
		holes := map[string]Node{}
		for k, h := range v.Holes {
			holes[k] = repeat(h, n)
		}
		return Use{Fragment: v.Fragment, Holes: holes}
	case List:
		items := make([]Node, len(v.Items))
		for i, it := range v.Items {
			items[i] = repeat(it, n)
		}
		return List{Items: items, Separator: v.Separator}
	}
	return node
}

// rename appends suffix to every parameter's name under node.
func rename(node Node, suffix string) Node {
	switch v := node.(type) {
	case Use:
		holes := map[string]Node{}
		for k, h := range v.Holes {
			holes[k] = rename(h, suffix)
		}
		return Use{Fragment: v.Fragment, Holes: holes}
	case List:
		items := make([]Node, len(v.Items))
		for i, it := range v.Items {
			items[i] = rename(it, suffix)
		}
		return List{Items: items, Separator: v.Separator}
	case Param:
		return v + Param(suffix)
	}
	return node
}
