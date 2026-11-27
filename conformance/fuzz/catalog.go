// Package fuzz generates the cases of the differential fuzzer (guide §6.4):
// random catalogs with data, which every dialect then holds, so that the
// same queries can run on each and their results be compared.
//
// A catalog stays within the portable subset: integers, text of bounded
// length, and, on the dialects that hold them, booleans and exact
// decimals; primary keys, unique keys, foreign keys, and the portable
// checks (non-empty text, minimum lengths, membership and integer ranges).
// Text compares by code point on every dialect, and its data is ASCII
// letters, digits and inner spaces, so lengths count the same in every
// unit and no dialect pads or folds it. Its rows hold every key, check and
// foreign key, and each table only references the tables before it.
//
// A catalog depends only on its seed, so a seed replays it.
package fuzz

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
)

// Kind is a column's type.
type Kind int

const (
	Integer Kind = iota
	Text
	Bool
	Decimal
)

// Column is a column of a table.
type Column struct {
	Name string
	Kind Kind
	// Max is a Text column's length; P and S are a Decimal column's
	// precision and scale.
	Max  int
	P, S int
	Null bool
}

// CheckKind is the kind of a portable check.
type CheckKind int

const (
	// NotEmpty holds a text column to a value other than ''.
	NotEmpty CheckKind = iota
	// MinLength holds a text column to at least N characters.
	MinLength
	// Membership holds a text column to one of Labels.
	Membership
	// Range holds an integer column to Lo through Hi.
	Range
)

// Check is a portable check on one column.
type Check struct {
	Kind   CheckKind
	Column string
	N      int
	Labels []string
	Lo, Hi int64
}

// ForeignKey references the primary key of another table.
type ForeignKey struct {
	Column string
	Table  string
}

// Table is a table with its rows, each a value per column, in order: an
// int64, a string, a bool, a DecimalValue, or nil for NULL.
type Table struct {
	Name    string
	Columns []Column
	Key     []string
	Uniques []string
	Foreign []ForeignKey
	Checks  []Check
	Rows    [][]any
}

// DecimalValue is an exact decimal, in its plain decimal notation.
type DecimalValue string

// Catalog is a generated catalog: its tables, each referencing only those
// before it.
type Catalog struct {
	Seed   uint64
	Tables []*Table
}

// Generate returns the catalog of seed.
func Generate(seed uint64) *Catalog {
	g := &gen{r: rand.New(rand.NewPCG(seed, 0x64716c)), c: &Catalog{Seed: seed}}
	// Half the catalogs keep to the kinds every dialect holds.
	g.kinds = kinds
	if g.chance(2) {
		g.kinds = kinds[:len(kinds)-2]
	}
	for i := range 1 + g.r.IntN(6) {
		g.c.Tables = append(g.c.Tables, g.table(fmt.Sprintf("t%d", i+1)))
	}
	return g.c
}

// Features are the features the catalog uses that some dialect lacks.
func (c *Catalog) Features() []string {
	var fs []string
	for _, t := range c.Tables {
		for _, col := range t.Columns {
			switch col.Kind {
			case Bool:
				fs = append(fs, "booleans")
			case Decimal:
				fs = append(fs, "decimals")
			}
		}
	}
	slices.Sort(fs)
	return slices.Compact(fs)
}

// Table returns the table named name, or nil.
func (c *Catalog) Table(name string) *Table {
	for _, t := range c.Tables {
		if t.Name == name {
			return t
		}
	}
	return nil
}

// Column returns the column named name, or nil.
func (t *Table) Column(name string) *Column {
	for i := range t.Columns {
		if t.Columns[i].Name == name {
			return &t.Columns[i]
		}
	}
	return nil
}

// gen generates a catalog from its random source.
type gen struct {
	r     *rand.Rand
	c     *Catalog
	kinds []Kind
}

// chance reports true with probability 1/n.
func (g *gen) chance(n int) bool { return g.r.IntN(n) == 0 }

// parents are the tables before the one being generated that a foreign
// key can reference: those keyed by a single column.
func (g *gen) parents() []*Table {
	var ps []*Table
	for _, t := range g.c.Tables {
		if len(t.Key) == 1 {
			ps = append(ps, t)
		}
	}
	return ps
}

// table generates a table named name, and its rows: a link table between
// two earlier tables, keyed by its references, or else an entity keyed by
// id.
func (g *gen) table(name string) *Table {
	if ps := g.parents(); len(ps) > 0 && g.chance(4) {
		return g.link(name, ps[g.r.IntN(len(ps))], ps[g.r.IntN(len(ps))])
	}
	t := &Table{Name: name, Key: []string{"id"}, Columns: []Column{{Name: "id", Kind: Integer}}}
	for i := range 1 + g.r.IntN(5) {
		g.column(t, fmt.Sprintf("c%d", i+1))
	}
	for _, p := range g.parents() {
		if g.chance(3) {
			col := p.Name + "_id"
			t.Columns = append(t.Columns, Column{Name: col, Kind: Integer, Null: g.chance(2)})
			t.Foreign = append(t.Foreign, ForeignKey{Column: col, Table: p.Name})
		}
	}
	g.rows(t)
	return t
}

// link generates a table linking a and b, which may be one table.
func (g *gen) link(name string, a, b *Table) *Table {
	ca, cb := a.Name+"_id", b.Name+"_id"
	if a == b {
		cb += "_2"
	}
	t := &Table{
		Name:    name,
		Key:     []string{ca, cb},
		Columns: []Column{{Name: ca, Kind: Integer}, {Name: cb, Kind: Integer}},
		Foreign: []ForeignKey{{Column: ca, Table: a.Name}, {Column: cb, Table: b.Name}},
	}
	if g.chance(2) {
		g.column(t, "c1")
	}
	g.rows(t)
	return t
}

// kinds weighs the column kinds; those some dialect lacks come last.
var kinds = []Kind{Integer, Integer, Integer, Text, Text, Text, Text, Bool, Decimal}

// column adds a column named name to t, with its checks, and maybe makes it
// a unique key.
func (g *gen) column(t *Table, name string) {
	c := Column{Name: name, Kind: g.kinds[g.r.IntN(len(g.kinds))], Null: g.chance(3)}
	switch c.Kind {
	case Integer:
		if g.chance(4) {
			lo := g.r.Int64N(2001) - 1000
			t.Checks = append(t.Checks, Check{Kind: Range, Column: name, Lo: lo, Hi: lo + g.r.Int64N(1001)})
		}
	case Text:
		c.Max = 1 + g.r.IntN(40)
		switch g.r.IntN(6) {
		case 0:
			t.Checks = append(t.Checks, Check{Kind: NotEmpty, Column: name})
		case 1:
			t.Checks = append(t.Checks, Check{Kind: MinLength, Column: name, N: 1 + g.r.IntN(c.Max)})
		case 2:
			labels := []string{}
			for range 2 + g.r.IntN(3) {
				if l := g.text(1, min(c.Max, 8)); !slices.Contains(labels, l) {
					labels = append(labels, l)
				}
			}
			t.Checks = append(t.Checks, Check{Kind: Membership, Column: name, Labels: labels})
		}
	case Decimal:
		c.P = 1 + g.r.IntN(18)
		c.S = g.r.IntN(min(c.P, 4) + 1)
	}
	// A unique key is never nullable, as dialects disagree on whether
	// NULLs repeat in it, and never boolean or a membership, which hold
	// too few values.
	if c.Kind != Bool && !slices.ContainsFunc(t.Checks, func(k Check) bool { return k.Column == name && k.Kind == Membership }) && g.chance(5) {
		c.Null = false
		t.Uniques = append(t.Uniques, name)
	}
	t.Columns = append(t.Columns, c)
}

// alphabet is what text data is made of; a space never starts or ends a
// value.
const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// text returns a value of lo through hi characters.
func (g *gen) text(lo, hi int) string {
	n := lo + g.r.IntN(hi-lo+1)
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[g.r.IntN(len(alphabet))]
		if i > 0 && i < n-1 && g.chance(8) {
			b[i] = ' '
		}
	}
	return string(b)
}

// rows generates up to 20 rows of t that hold its keys, checks and foreign
// keys. A row whose key or unique value repeats is left out, as is one
// that must reference a table with no rows.
func (g *gen) rows(t *Table) {
	seen := map[string]bool{}
	id := int64(0)
	for range g.r.IntN(21) {
		row := make([]any, len(t.Columns))
		ok := true
		for i, c := range t.Columns {
			if fk := slices.IndexFunc(t.Foreign, func(f ForeignKey) bool { return f.Column == c.Name }); fk >= 0 {
				p := g.c.Table(t.Foreign[fk].Table)
				if c.Null && g.chance(5) || len(p.Rows) == 0 {
					ok = ok && c.Null
					continue
				}
				row[i] = p.Rows[g.r.IntN(len(p.Rows))][0]
				continue
			}
			if c.Name == "id" {
				id += 1 + g.r.Int64N(3)
				row[i] = id
				continue
			}
			if c.Null && g.chance(5) {
				continue
			}
			row[i] = g.value(t, c)
		}
		if !ok || g.repeats(t, seen, row) {
			continue
		}
		t.Rows = append(t.Rows, row)
	}
}

// repeats reports whether row repeats a key or unique value of the rows
// before it, and records its own.
func (g *gen) repeats(t *Table, seen map[string]bool, row []any) bool {
	keys := []string{"key " + fmt.Sprint(t.pick(row, t.Key)...)}
	for _, u := range t.Uniques {
		keys = append(keys, u+" "+fmt.Sprint(t.pick(row, []string{u})...))
	}
	for _, k := range keys {
		if seen[k] {
			return true
		}
	}
	for _, k := range keys {
		seen[k] = true
	}
	return false
}

// pick returns row's values of the named columns.
func (t *Table) pick(row []any, names []string) []any {
	var vs []any
	for _, n := range names {
		vs = append(vs, row[slices.IndexFunc(t.Columns, func(c Column) bool { return c.Name == n })])
	}
	return vs
}

// value returns a value of c that holds t's checks on it.
func (g *gen) value(t *Table, c Column) any {
	switch c.Kind {
	case Integer:
		for _, k := range t.Checks {
			if k.Column == c.Name {
				return k.Lo + g.r.Int64N(k.Hi-k.Lo+1)
			}
		}
		return g.r.Int64N(2_000_001) - 1_000_000
	case Text:
		lo := 0
		for _, k := range t.Checks {
			if k.Column != c.Name {
				continue
			}
			switch k.Kind {
			case Membership:
				return k.Labels[g.r.IntN(len(k.Labels))]
			case NotEmpty:
				lo = 1
			case MinLength:
				lo = k.N
			}
		}
		return g.text(lo, c.Max)
	case Bool:
		return g.chance(2)
	}
	whole, frac := g.r.Int64N(pow10(c.P-c.S)), g.r.Int64N(pow10(c.S))
	var b strings.Builder
	// Zero is never negative, so no two values of a unique key are equal.
	if (whole != 0 || frac != 0) && g.chance(2) {
		b.WriteByte('-')
	}
	fmt.Fprint(&b, whole)
	if c.S > 0 {
		fmt.Fprintf(&b, ".%0*d", c.S, frac)
	}
	return DecimalValue(b.String())
}

// pow10 returns 10 to the n.
func pow10(n int) int64 {
	p := int64(1)
	for range n {
		p *= 10
	}
	return p
}
