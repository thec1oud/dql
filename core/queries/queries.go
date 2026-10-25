// Package queries checks the query layer against the catalogs and the shape
// layer (design record §16–21). A query-source plugin types each query of a
// query file; the core then resolves its -- returns: shape, checks that it
// filters on the scope of every scoped table it reads, that it selects no
// write-only column, and that a :count query returns one count.
//
// Plan adds the builder's queries: each generated operation as a plan of the
// dialect's fragment table (§16.2, §18).
package queries

import (
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/thec1oud/dql/core/diagnostics"
	"github.com/thec1oud/dql/core/shapes"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

const (
	keyBound     = "core.bound"
	keyWriteOnly = "core.write-only"
)

// Check checks the query-file queries of b's query layer, resolves each
// -- returns: into its shape, and returns the diagnostics, sorted. A query
// whose returns fails keeps its anonymous result. The builder's queries
// are right by construction.
func Check(b *irv1.Bundle) []*irv1.Diagnostic {
	c := &checker{b: b, tables: map[string]*irv1.Table{}, columns: map[string]*irv1.Column{}}
	for _, cat := range b.GetCatalogs() {
		for _, ns := range cat.GetNamespaces() {
			for _, t := range ns.GetTables() {
				q := &irv1.QualifiedName{Catalog: cat.GetName().GetStored(), Namespace: ns.GetName().GetStored(), Object: t.GetName().GetStored()}
				c.tables[diagnostics.Name(q)] = t
				for _, col := range t.GetColumns() {
					q.Member = col.GetName().GetStored()
					c.columns[diagnostics.Name(q)] = col
				}
			}
		}
	}
	for _, q := range b.GetQueries().GetQueries() {
		if q.GetSource() == irv1.QuerySource_QUERY_SOURCE_BUILDER {
			continue
		}
		c.writeOnly(q)
		c.scope(q)
		c.count(q)
		if q.GetReturns() != "" {
			c.returns(q)
		}
	}
	diagnostics.Sort(c.ds)
	return c.ds
}

type checker struct {
	b *irv1.Bundle
	// The catalogs' tables and their columns, by qualified name.
	tables  map[string]*irv1.Table
	columns map[string]*irv1.Column
	ds      []*irv1.Diagnostic
}

func (c *checker) errorf(q *irv1.Query, code string, target *irv1.QualifiedName, format string, args ...any) {
	d := diagnostics.Errorf(code, target, format, args...)
	d.Query = q.GetName().GetStored()
	c.ds = append(c.ds, d)
}

func has(col *irv1.Column, key string) bool {
	return slices.ContainsFunc(col.GetAnnotations(), func(a *irv1.Annotation) bool { return a.GetKey() == key })
}

// writeOnly reports a result column that reads a write-only column (§7,
// §17.2).
func (c *checker) writeOnly(q *irv1.Query) {
	for _, f := range q.GetResult().GetAnonymous().GetFields() {
		if o := f.GetOrigin(); o != nil && has(c.columns[diagnostics.Name(o)], keyWriteOnly) {
			c.errorf(q, "query-write-only", o, "query %s selects %s, which is write-only; leave it out of the result", q.GetName().GetStored(), diagnostics.Name(o))
		}
	}
}

// scope reports a scoped table the query does not filter on the scope of,
// unless it is -- unscoped, and a scope column no table of the query is
// scoped by (§21.2).
func (c *checker) scope(q *irv1.Query) {
	used := q.GetScope().GetColumns()
	bound := map[string]bool{}
	for _, name := range q.GetTables() {
		var missing []string
		for _, col := range c.tables[diagnostics.Name(name)].GetColumns() {
			if !has(col, keyBound) {
				continue
			}
			bound[col.GetName().GetStored()] = true
			if !slices.Contains(used, col.GetName().GetStored()) {
				missing = append(missing, col.GetName().GetStored())
			}
		}
		if len(missing) > 0 && !q.GetScope().GetUnscoped() {
			c.errorf(q, "query-unscoped", name, "query reads %s, which is scoped by %s, without filtering on %s; filter on %s or mark the query -- unscoped",
				diagnostics.Name(name), strings.Join(missing, " and "), scopeParams(missing), pronoun(missing))
		}
	}
	for _, u := range used {
		if !bound[u] {
			c.errorf(q, "query-scope-unknown", nil, "query %s filters on @scope.%s, but no table it reads is scoped by %s; use a bound column", q.GetName().GetStored(), u, u)
		}
	}
}

func scopeParams(cols []string) string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = "@scope." + c
	}
	return strings.Join(out, " and ")
}

func pronoun(cols []string) string {
	if len(cols) == 1 {
		return "it"
	}
	return "them"
}

// count reports a :count query that does not return one integer column
// (§19).
func (c *checker) count(q *irv1.Query) {
	if q.GetCardinality() != irv1.Cardinality_CARDINALITY_COUNT {
		return
	}
	fs := q.GetResult().GetAnonymous().GetFields()
	if len(fs) == 1 {
		switch fs[0].GetType().GetKind().(type) {
		case *irv1.Type_Int16, *irv1.Type_Int32, *irv1.Type_Int64:
			return
		}
	}
	c.errorf(q, "query-count-shape", nil, "query %s is :count, so it returns one integer column, such as count(*)", q.GetName().GetStored())
}

// returns resolves q's -- returns: shape and checks the query's columns
// against it: every field appears with its type, non-null where the field
// is once -- nonnull: applies, and no column is extra (§17.2). The result
// becomes the shape, with the columns in the query's order.
func (c *checker) returns(q *irv1.Query) {
	name := q.GetName().GetStored()
	id := shapes.Ref(c.b.GetCatalogs(), c.b.GetShapes(), q.GetReturns())
	i := slices.IndexFunc(c.b.GetShapes().GetShapes(), func(s *irv1.Shape) bool { return proto.Equal(s.GetId(), id) })
	if i < 0 {
		c.errorf(q, "query-returns-unknown", nil, "query %s returns %s, which is not a shape; name a preset such as user.full or a custom shape", name, q.GetReturns())
		return
	}
	shape := c.b.GetShapes().GetShapes()[i]
	fields := q.GetResult().GetAnonymous().GetFields()
	byName := map[string]*irv1.ResultField{}
	for _, f := range fields {
		byName[f.GetName()] = f
	}
	before := len(c.ds)
	inShape := map[string]bool{}
	for _, sf := range shape.GetFields() {
		inShape[sf.GetColumn()] = true
		f := byName[sf.GetColumn()]
		switch {
		case f == nil:
			c.errorf(q, "query-returns-missing", nil, "query %s returns %s but selects no column %s; select it", name, q.GetReturns(), sf.GetColumn())
		case !sameType(f.GetType(), sf.GetType()):
			c.errorf(q, "query-returns-type", nil, "query %s returns %s, whose field %s has another type than the column the query selects; cast it", name, q.GetReturns(), sf.GetColumn())
		case f.GetNullable() && !sf.GetNullable():
			c.errorf(q, "query-returns-nullable", nil, "query %s returns %s, whose field %s is non-null, but the column may be null; prove it or narrow it with -- nonnull: %s", name, q.GetReturns(), sf.GetColumn(), sf.GetColumn())
		}
	}
	var columns []string
	for _, f := range fields {
		columns = append(columns, f.GetName())
		if !inShape[f.GetName()] {
			c.errorf(q, "query-returns-extra", nil, "query %s returns %s, which has no field %s; leave the column out", name, q.GetReturns(), f.GetName())
		}
	}
	if len(c.ds) == before {
		q.Result = &irv1.ResultShape{Kind: &irv1.ResultShape_Shape{Shape: shape.GetId()}, Columns: columns}
	}
}

// sameType reports whether a and b are the same logical type, whatever
// native types they carry.
func sameType(a, b *irv1.Type) bool {
	a, b = proto.Clone(a).(*irv1.Type), proto.Clone(b).(*irv1.Type)
	a.Native, b.Native = nil, nil
	return proto.Equal(a, b)
}
