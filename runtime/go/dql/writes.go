package dql

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
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
//
// On a dialect without RETURNING the rows are read back in the same
// transaction (§20.1). Where every row leaves an auto-increment key to the
// database and the table has reread.from, each statement's rows are read
// back at once: a multi-row insert that sets no key takes consecutive
// values, a step apart, from its last insert ID on. Otherwise each row is
// inserted and read back on its own, so that its key comes back as its
// own last insert ID.
func InsertMany[T any](ctx context.Context, h Writer, t *Fragments, q *Query, rows []Args, decode func(Row) (T, error)) ([]T, error) {
	if q.Reread != nil && batched(t, q, rows) {
		return insertBatched(ctx, h, t, q, rows, decode)
	}
	if q.Reread != nil {
		out := make([]T, 0, len(rows))
		err := Atomic(ctx, h, func(ctx context.Context, w Writer) error {
			out = out[:0] // a rerun starts over
			for _, r := range rows {
				s, err := q.Statement(t, r)
				if err != nil {
					return err
				}
				v, err := OneOf(ctx, w, s, decode)
				if err != nil {
					return err
				}
				out = append(out, v)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		return out, nil
	}
	var fetched []Row
	var err error
	if gs := groups(t, rows); len(gs) == 1 {
		fetched, err = FetchBulk(ctx, h, manyRows(t, q, gs[0]))
	} else {
		err = Atomic(ctx, h, func(ctx context.Context, w Writer) error {
			fetched = fetched[:0] // a rerun starts over
			for _, group := range gs {
				got, err := FetchBulk(ctx, w, manyRows(t, q, group))
				if err != nil {
					return err
				}
				fetched = append(fetched, got...)
			}
			return nil
		})
	}
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

// groups splits rows into runs that one statement can write. Where the
// dialect has value.default, a row's unset column takes DEFAULT, so every
// row writes in one; where it has none, as on SQLite, a statement leaves
// unset columns out, so each run of rows setting the same columns is
// written apart (§25.2).
func groups(t *Fragments, rows []Args) [][]Args {
	if _, ok := t.Templates["value.default"]; ok || len(rows) == 0 {
		return [][]Args{rows}
	}
	var out [][]Args
	from := 0
	for i := 1; i <= len(rows); i++ {
		if i == len(rows) || !sameKeys(rows[i], rows[from]) {
			out = append(out, rows[from:i])
			from = i
		}
	}
	return out
}

func sameKeys(a, b Args) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

// omitUnset leaves the columns args does not set out of an insert's column
// list, VALUES rows and new-row values ({news}), for a dialect without value.default: the column
// then takes its default. A multi-row insert's rows set the same columns,
// as groups splits them so, so the first row's say which.
func omitUnset(plan []Node, args Args) []Node {
	out := slices.Clone(plan)
	for i, node := range plan {
		u, ok := node.(Use)
		cols, isList := u.Holes["cols"].(List)
		rows, hasRows := u.Holes["rows"]
		if !ok || !isList || !hasRows {
			continue
		}
		first := rows
		if l, ok := rows.(List); ok {
			first = l.Items[0]
		}
		var keep []bool
		for _, it := range first.(Use).Holes["0"].(List).Items {
			p, isParam := it.(Param)
			_, set := args[string(p)]
			keep = append(keep, !isParam || set)
		}
		holes := maps.Clone(u.Holes)
		holes["cols"] = kept(cols, keep)
		if news, ok := u.Holes["news"].(List); ok {
			// A MERGE's insert branch names the new row's value of each
			// column the statement writes.
			holes["news"] = kept(news, keep)
		}
		if l, ok := rows.(List); ok {
			items := make([]Node, len(l.Items))
			for j, r := range l.Items {
				items[j] = keptRow(r.(Use), keep)
			}
			holes["rows"] = List{Items: items, Separator: l.Separator}
		} else {
			holes["rows"] = keptRow(rows.(Use), keep)
		}
		out[i] = Use{Fragment: u.Fragment, Holes: holes}
	}
	return out
}

func kept(l List, keep []bool) List {
	out := List{Separator: l.Separator}
	for i, it := range l.Items {
		if keep[i] {
			out.Items = append(out.Items, it)
		}
	}
	return out
}

func keptRow(row Use, keep []bool) Use {
	return Use{Fragment: row.Fragment, Holes: map[string]Node{"0": kept(row.Holes["0"].(List), keep)}}
}

// manyRows is the bulk write of rows with q, each statement repeating q's
// VALUES row for its rows.
func manyRows(t *Fragments, q *Query, rows []Args) Bulk {
	per := 0
	for _, node := range q.Plan {
		per += placeholders(node, false)
	}
	return Bulk{Rows: len(rows), ParamsPerRow: per, Statement: func(from, to int) (Statement, error) {
		many := *q
		many.Reread = nil
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
}

// batched reports whether InsertMany can read rows back by statement: q's
// key is an auto-increment every row leaves out, its read filters on it,
// and the table can read from a last insert ID on.
func batched(t *Fragments, q *Query, rows []Args) bool {
	if _, ok := t.Templates["reread.from"]; !ok || q.Generated == "" {
		return false
	}
	if _, ok := readFrom(q.Reread, q.Generated, 0, 0, ""); !ok {
		return false
	}
	for _, r := range rows {
		if _, set := r[q.Generated]; set {
			return false
		}
	}
	return true
}

// insertBatched runs InsertMany's statements, and after each reads back
// its rows by their keys, in one transaction.
func insertBatched[T any](ctx context.Context, h Writer, t *Fragments, q *Query, rows []Args, decode func(Row) (T, error)) ([]T, error) {
	b := manyRows(t, q, rows)
	var out []T
	err := Atomic(ctx, h, func(ctx context.Context, w Writer) error {
		out = make([]T, 0, len(rows))
		e, _ := w.bind()
		size := b.chunk(e.dialect.MaxParams)
		for from := 0; from < len(rows); from += size {
			to := min(from+size, len(rows))
			s, err := b.Statement(from, to)
			if err != nil {
				return err
			}
			n, id, err := execID(ctx, w, s)
			if err != nil {
				return err
			}
			plan, _ := readFrom(q.Reread, q.Generated, id, int64(to-from), q.Types[q.Generated])
			a, err := t.Assemble(plan, rows[from], q.Sensitive, q.Types)
			if err != nil {
				return fmt.Errorf("%w in query %s", err, q.Name)
			}
			read := Statement{
				Query: q.Name, Catalog: q.Catalog, SQL: a.SQL, Args: a.Args, Writes: true,
				Kind: "select", Sensitive: a.Sensitive, Types: a.Types,
			}
			got, err := Fetch(ctx, w, read)
			if err != nil {
				return err
			}
			if int64(len(got)) != n || n != int64(to-from) {
				return fmt.Errorf("dql: query %s inserted %d rows and read back %d of %d", q.Name, n, len(got), to-from)
			}
			for _, r := range got {
				v, err := decode(r)
				if err != nil {
					return err
				}
				out = append(out, v)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// readFrom returns plan, a read by key, reading instead the n rows whose
// key gen holds the last insert ID id or a later value of its sequence, in
// key order: its comparison of gen with its parameter becomes reread.from,
// and an order and a limit follow. ok is false when plan has no such
// comparison.
func readFrom(plan []Node, gen string, id, n int64, typ string) ([]Node, bool) {
	out := make([]Node, 0, len(plan)+2)
	var col Node
	for _, node := range plan {
		node, c := fromID(node, gen, id, typ)
		if c != nil {
			col = c
		}
		out = append(out, node)
	}
	if col == nil {
		return nil, false
	}
	out = append(out,
		Use{Fragment: "clause.order", Holes: map[string]Node{"0": Use{Fragment: "order.asc", Holes: map[string]Node{"0": col}}}},
		Use{Fragment: "clause.limit", Holes: map[string]Node{"0": Arg{Value: n}}},
	)
	return out, true
}

// fromID replaces the comparison of a column with gen under node by
// reread.from, and returns the column.
func fromID(node Node, gen string, id int64, typ string) (Node, Node) {
	u, ok := node.(Use)
	if !ok {
		return node, nil
	}
	if p, ok := u.Holes["1"].(Param); ok && u.Fragment == "op.eq" && string(p) == gen {
		col := u.Holes["0"]
		return Use{Fragment: "reread.from", Holes: map[string]Node{"0": col, "1": Arg{Value: id, Type: typ}, "2": Arg{Value: id, Type: typ}}}, col
	}
	var found Node
	holes := make(map[string]Node, len(u.Holes))
	for k, h := range u.Holes {
		h, c := fromID(h, gen, id, typ)
		if c != nil {
			found = c
		}
		holes[k] = h
	}
	return Use{Fragment: u.Fragment, Holes: holes}, found
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
