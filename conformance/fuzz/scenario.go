package fuzz

import (
	"fmt"
	"math/rand/v2"
	"slices"

	harness "github.com/thec1oud/dql/conformance/harness/go"
)

// Scenario returns the queries and writes of the catalog's seed, as a
// conformance scenario over the generated code of its catalog (guide §6.4):
// builder reads with filters, ordering and paging, counts, and inserts,
// upserts, patches and deletes. Every read orders its rows by the catalog's
// key last, so its rows come in one order on every dialect.
//
// The writes hold every key, check and foreign key, but for those the
// scenario makes fail on purpose: a repeated key, a missing reference, a
// delete of a referenced row, or a read of a missing key, which expect
// their error. The scenario ends by reading every table. It requires the
// features its catalog uses.
func (c *Catalog) Scenario() harness.Scenario {
	g := &gen{r: rand.New(rand.NewPCG(c.Seed, 0x7363)), c: c.clone()}
	sc := harness.Scenario{Name: fmt.Sprintf("fuzz-%d", c.Seed), Requires: c.Features()}
	for range 10 + g.r.IntN(31) {
		t := g.c.Tables[g.r.IntN(len(g.c.Tables))]
		steps := []func(*Table) (harness.Step, bool){g.read, g.read, g.read, g.insert, g.insert, g.upsert, g.patch, g.remove, g.fail}
		if st, ok := steps[g.r.IntN(len(steps))](t); ok {
			sc.Steps = append(sc.Steps, st)
		}
	}
	for _, t := range g.c.Tables {
		sc.Steps = append(sc.Steps, harness.Step{Op: "select", From: t.Name, Shape: t.Name + "_full", Order: keyOrder(t)})
	}
	return sc
}

// clone copies the catalog, so that a scenario's writes change the copy's
// rows.
func (c *Catalog) clone() *Catalog {
	out := &Catalog{Seed: c.Seed}
	for _, t := range c.Tables {
		u := *t
		u.Rows = nil
		for _, r := range t.Rows {
			u.Rows = append(u.Rows, slices.Clone(r))
		}
		out.Tables = append(out.Tables, &u)
	}
	return out
}

// keyOrder orders by t's key.
func keyOrder(t *Table) []harness.OrderKey {
	var o []harness.OrderKey
	for _, k := range t.Key {
		o = append(o, harness.OrderKey{Column: k})
	}
	return o
}

// operators are the filters on each kind of column every dialect offers;
// is_null and is_not_null are added on a nullable column.
var operators = map[Kind][]string{
	Integer: {"eq", "ne", "lt", "le", "gt", "ge", "between", "in", "is_distinct_from"},
	Text:    {"eq", "ne", "lt", "le", "gt", "ge", "between", "in", "is_distinct_from", "like", "ilike", "starts_with", "contains"},
	Bool:    {"eq", "ne", "is_distinct_from"},
	Decimal: {"eq", "ne", "lt", "le", "gt", "ge", "between", "in"},
}

// read is a select of t: all its rows or their count, through up to three
// filters, in a random order ended by the key, and maybe a page of it.
func (g *gen) read(t *Table) (harness.Step, bool) {
	st := harness.Step{Op: "select", From: t.Name, Shape: t.Name + "_full"}
	for range g.r.IntN(4) {
		st.Filter = append(st.Filter, g.filter(t, t.Columns[g.r.IntN(len(t.Columns))]))
	}
	if g.chance(4) {
		st.Read = "count"
		return st, true
	}
	for range g.r.IntN(3) {
		st.Order = append(st.Order, harness.OrderKey{Column: t.Columns[g.r.IntN(len(t.Columns))].Name, Desc: g.chance(2)})
	}
	st.Order = append(st.Order, keyOrder(t)...)
	if g.chance(3) {
		st.Limit = 1 + g.r.Int64N(5)
		if g.chance(2) {
			st.Offset = g.r.Int64N(5)
		}
	}
	return st, true
}

// filter is a predicate on c, on a value from a row of t when it has one,
// so that some rows match.
func (g *gen) filter(t *Table, c Column) harness.Filter {
	ops := operators[c.Kind]
	if c.Null {
		ops = append(ops[:len(ops):len(ops)], "is_null", "is_not_null")
	}
	f := harness.Filter{Column: c.Name, Op: ops[g.r.IntN(len(ops))]}
	value := func() any {
		if len(t.Rows) > 0 && !g.chance(4) {
			if v := t.pick(t.Rows[g.r.IntN(len(t.Rows))], []string{c.Name})[0]; v != nil {
				return v
			}
		}
		return g.value(t, c)
	}
	switch f.Op {
	case "is_null", "is_not_null":
	case "between":
		f.Value = []any{value(), value()}
	case "in":
		vs := []any{}
		for range 1 + g.r.IntN(4) {
			vs = append(vs, value())
		}
		f.Value = vs
	case "like", "ilike":
		f.Value = g.pattern(value().(string))
	case "starts_with", "contains":
		s := value().(string)
		i := g.r.IntN(len(s) + 1)
		f.Value = s[i : i+g.r.IntN(len(s)-i+1)]
	default:
		f.Value = value()
	}
	return f
}

// pattern makes a LIKE pattern from s: some characters become _ and some
// runs %.
func (g *gen) pattern(s string) string {
	var p []byte
	for i := 0; i < len(s); i++ {
		switch g.r.IntN(6) {
		case 0:
			p = append(p, '_')
		case 1:
			p = append(p, '%')
			i += g.r.IntN(3)
		default:
			p = append(p, s[i])
		}
	}
	return string(p)
}

// draft is the input of a write of row: each column's value, NULL
// included.
func draft(t *Table, row []any) map[string]any {
	m := map[string]any{}
	for i, c := range t.Columns {
		m[c.Name] = row[i]
	}
	return m
}

// key is row's key.
func key(t *Table, row []any) map[string]any {
	m := map[string]any{}
	for _, k := range t.Key {
		m[k] = t.pick(row, []string{k})[0]
	}
	return m
}

// insert inserts a new row, or inserts it unless it conflicts.
func (g *gen) insert(t *Table) (harness.Step, bool) {
	row, ok := g.fresh(t)
	if !ok {
		return harness.Step{}, false
	}
	t.Rows = append(t.Rows, row)
	op := ".insert"
	if g.chance(4) {
		op = ".insert_or_ignore"
	}
	return harness.Step{Op: t.Name + op, Input: draft(t, row)}, true
}

// upsert writes new values to a row by its key, or inserts a new row.
func (g *gen) upsert(t *Table) (harness.Step, bool) {
	if len(t.Rows) == 0 || g.chance(4) {
		st, ok := g.insert(t)
		st.Op = t.Name + ".upsert"
		return st, ok
	}
	i := g.r.IntN(len(t.Rows))
	row, ok := g.fresh(t)
	if !ok {
		return harness.Step{}, false
	}
	for _, k := range t.Key {
		j := slices.IndexFunc(t.Columns, func(c Column) bool { return c.Name == k })
		row[j] = t.Rows[i][j]
	}
	// Its unique values repeat no row's, its own included.
	t.Rows[i] = row
	return harness.Step{Op: t.Name + ".upsert", Input: draft(t, row)}, true
}

// patch sets some columns of a row other than its key.
func (g *gen) patch(t *Table) (harness.Step, bool) {
	if len(t.Rows) == 0 {
		return harness.Step{}, false
	}
	i := g.r.IntN(len(t.Rows))
	values, ok := g.fresh(t)
	if !ok {
		return harness.Step{}, false
	}
	row, set := slices.Clone(t.Rows[i]), map[string]any{}
	for j, c := range t.Columns {
		if !slices.Contains(t.Key, c.Name) && g.chance(2) {
			row[j], set[c.Name] = values[j], values[j]
		}
	}
	if len(set) == 0 {
		return harness.Step{}, false
	}
	st := harness.Step{Op: t.Name + ".patch", Key: key(t, t.Rows[i]), Input: set}
	t.Rows[i] = row
	return st, true
}

// remove deletes a row, which fails while another row references it.
func (g *gen) remove(t *Table) (harness.Step, bool) {
	if len(t.Rows) == 0 {
		return harness.Step{}, false
	}
	i := g.r.IntN(len(t.Rows))
	st := harness.Step{Op: t.Name + ".delete", Key: key(t, t.Rows[i])}
	if g.referenced(t, t.Rows[i]) {
		st.Expect = failure("ForeignKeyViolation")
		return st, true
	}
	t.Rows = slices.Delete(t.Rows, i, i+1)
	return st, true
}

// referenced reports whether a row of another table references row.
func (g *gen) referenced(t *Table, row []any) bool {
	for _, u := range g.c.Tables {
		for _, f := range u.Foreign {
			if f.Table == t.Name && slices.ContainsFunc(u.Rows, func(r []any) bool { return u.pick(r, []string{f.Column})[0] == row[0] }) {
				return true
			}
		}
	}
	return false
}

// failure expects an error of class.
func failure(class string) map[string]any {
	return map[string]any{"error": map[string]any{"class": class}}
}

// fail is a write or read that fails or does nothing: an insert of a key
// that is there, or one ignored; an insert referencing a row that is not;
// or a get of a key that is not.
func (g *gen) fail(t *Table) (harness.Step, bool) {
	row, ok := g.fresh(t)
	if !ok {
		return harness.Step{}, false
	}
	switch {
	case len(t.Rows) > 0 && g.chance(2):
		copy(row, t.Rows[g.r.IntN(len(t.Rows))][:len(t.Key)])
		if g.chance(2) {
			return harness.Step{Op: t.Name + ".insert_or_ignore", Input: draft(t, row)}, true
		}
		return harness.Step{Op: t.Name + ".insert", Input: draft(t, row), Expect: failure("UniqueViolation")}, true
	case len(t.Foreign) > 0 && g.chance(2):
		f := t.Foreign[g.r.IntN(len(t.Foreign))]
		row[slices.IndexFunc(t.Columns, func(c Column) bool { return c.Name == f.Column })] = g.c.Table(f.Table).maxID() + 1000
		return harness.Step{Op: t.Name + ".insert", Input: draft(t, row), Expect: failure("ForeignKeyViolation")}, true
	}
	for i := range t.Key {
		row[i] = int64(-1 - i)
	}
	return harness.Step{Op: t.Name + ".get", Key: key(t, row), Expect: failure("NotFound")}, true
}
