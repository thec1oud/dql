package sqlite

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// queryError is a problem with one query: a diagnostic code, a message and
// where in the parsed statement it is, or -1.
type queryError struct {
	code string
	off  int
	msg  string
}

func (e *queryError) Error() string { return e.msg }

func (a *analyzer) errAt(code string, off int, format string, args ...any) error {
	return &queryError{code: code, off: off, msg: fmt.Sprintf(format, args...)}
}

// analyzer types one statement against the catalog (§17.2).
type analyzer struct {
	ix *index
	r  *rewritten
	// The parameters, as rewrite found them.
	params []*param
	// The list placeholders, by their position among the placeholders.
	lists map[int]bool
	// The catalogs of the tables the statement uses.
	catalogs map[string]bool
	// The tables the statement reads or writes, by qualified name.
	tables map[string]*irv1.QualifiedName
	// Set by a statement that writes or a call of a volatile function
	// (§17.2, §22).
	writes bool
}

// param is a parameter and the type the statement gives it. A type from a
// weak context, such as a function argument, gives way to one from a strong
// context: a column, a cast or an insert target.
type param struct {
	raw    rawParam
	typ    *irv1.Type
	strong bool
}

// value is what an expression evaluates to.
type value struct {
	// Nil for a value whose type its context decides: a parameter not yet
	// typed, a string literal, NULL, or a value whose storage class only
	// the row decides, such as ->>'s.
	typ      *irv1.Type
	nullable bool
	// Non-null only because a nonnull annotation says so; decoding
	// enforces it (§13).
	annotated bool
	// The column the value reads unchanged, and its catalog column.
	origin *irv1.QualifiedName
	col    *irv1.Column
	// The parameter of a bare placeholder, by its index in params, plus 1.
	param int
	// The placeholder's position, for a list.
	use int
	// An untyped string literal: text unless its context says otherwise.
	literal bool
	// An integer literal, which ORDER BY and GROUP BY read as a result
	// column's position.
	positional bool
	position   int
	// The name of the column the value reads, or the parameter's.
	name string
	// Where the value is written.
	off int
}

// column is a column of a relation or of a result.
type column struct {
	// Empty for a result column that needs AS.
	name string
	v    value
	// Where a result column is written.
	off int
}

// rel is a relation of a FROM clause: a table or view, a CTE or a subquery.
type rel struct {
	// The alias, or the table's or CTE's name.
	name string
	cols []column
	// Set when an outer join can fill the relation with NULLs.
	nullable bool
	// Columns that a USING join merged; an unqualified reference reaches
	// them through the join's own relation.
	hidden map[string]bool
}

// scope is one level of name resolution: a SELECT's FROM items, the CTEs
// it defines, and the scope of the query it is nested in.
type scope struct {
	rels   []*rel
	ctes   map[string]*rel
	parent *scope
	// The query has GROUP BY (§17.2, guide §5.4).
	grouped bool
	// The result columns, which ORDER BY, GROUP BY and HAVING may name.
	outputs []column
}

func (sc *scope) cte(name string) (*rel, bool) {
	for s := sc; s != nil; s = s.parent {
		if r, ok := s.ctes[strings.ToLower(name)]; ok {
			return r, true
		}
	}
	return nil, false
}

// statement types a statement, nested in parent's scope or, for the
// query's own statement, nil, and returns its result columns: nil for a
// write without RETURNING, which returns no rows.
func (a *analyzer) statement(n stmt, parent *scope) ([]column, error) {
	switch s := n.(type) {
	case *selectStmt:
		return a.selectStmt(s, parent)
	case *insertStmt:
		return a.insert(s, parent)
	case *updateStmt:
		return a.update(s, parent)
	case *deleteStmt:
		return a.delete(s, parent)
	}
	u := n.(*unsupported)
	return nil, a.errAt("query-unsupported", u.off, "%s", u.msg)
}

// with adds a WITH clause's CTEs to a new scope over parent.
func (a *analyzer) with(w *withClause, parent *scope) (*scope, error) {
	sc := &scope{parent: parent, ctes: map[string]*rel{}}
	if w == nil {
		return sc, nil
	}
	if w.recursive {
		return nil, a.errAt("query-unsupported", w.off, "WITH RECURSIVE is not supported in query files yet")
	}
	for _, c := range w.ctes {
		cols, err := a.selectStmt(c.sel, sc)
		if err != nil {
			return nil, err
		}
		if len(c.cols) > 0 {
			if len(c.cols) != len(cols) {
				return nil, a.errAt("query-unsupported", c.off, "CTE %s names %d columns but returns %d", c.name, len(c.cols), len(cols))
			}
			cols = slices.Clone(cols)
			for i, nm := range c.cols {
				cols[i].name = nm
			}
		}
		sc.ctes[strings.ToLower(c.name)] = &rel{name: c.name, cols: cols}
	}
	return sc, nil
}

// selectStmt types a SELECT, or a compound of them: the first names the
// columns, a column is nullable when any side's is, and a parameter or
// literal takes its type from another side.
func (a *analyzer) selectStmt(s *selectStmt, parent *scope) ([]column, error) {
	sc, err := a.with(s.with, parent)
	if err != nil {
		return nil, err
	}
	out, inner, err := a.core(s.cores[0], sc)
	if err != nil {
		return nil, err
	}
	for _, c := range s.cores[1:] {
		cols, _, err := a.core(c, sc)
		if err != nil {
			return nil, err
		}
		if len(cols) != len(out) {
			return nil, a.errAt("query-unsupported", c.off, "the queries of a compound SELECT return %d and %d columns", len(out), len(cols))
		}
		out = slices.Clone(out)
		for j := range out {
			lv, rv, err := a.unify(out[j].v, cols[j].v, true)
			if err != nil {
				return nil, err
			}
			v := value{typ: lv.typ, nullable: lv.nullable || rv.nullable, annotated: lv.annotated || rv.annotated, literal: lv.literal && rv.literal}
			if lv.origin != nil && rv.origin != nil && lv.origin.String() == rv.origin.String() {
				v.origin = lv.origin
			}
			out[j] = column{name: out[j].name, v: v, off: out[j].off}
		}
	}
	// ORDER BY of a compound names its result columns only.
	if len(s.cores) > 1 {
		inner = &scope{parent: sc, outputs: out}
	}
	return out, a.order(s.order, s.limit, s.offset, inner)
}

// core types one SELECT of a statement, in a scope of its own, which it
// returns for the statement's ORDER BY.
func (a *analyzer) core(c *selectCore, parent *scope) ([]column, *scope, error) {
	switch {
	case c.values != nil:
		return nil, nil, a.errAt("query-unsupported", c.off, "VALUES lists are supported only in INSERT in query files; write SELECT")
	case c.windows:
		return nil, nil, a.errAt("query-unsupported", c.off, "named windows are not supported in query files")
	}
	sc := &scope{parent: parent}
	if c.from != nil {
		if err := a.from(c.from, sc); err != nil {
			return nil, nil, err
		}
	}
	if err := a.condition(c.where, sc); err != nil {
		return nil, nil, err
	}
	sc.grouped = len(c.group) > 0
	cols, err := a.targets(c.cols, sc)
	if err != nil {
		return nil, nil, err
	}
	sc.outputs = cols
	if err := a.byItems(c.group, sc); err != nil {
		return nil, nil, err
	}
	if c.having != nil {
		v, err := a.outputExpr(c.having, sc)
		if err != nil {
			return nil, nil, err
		}
		if _, err := a.expect(v, tBool(), true); err != nil {
			return nil, nil, err
		}
	}
	return cols, sc, nil
}

// order types ORDER BY, LIMIT and OFFSET.
func (a *analyzer) order(by []expr, limit, offset expr, sc *scope) error {
	if err := a.byItems(by, sc); err != nil {
		return err
	}
	for _, n := range []expr{limit, offset} {
		if n == nil {
			continue
		}
		v, err := a.expr(n, sc)
		if err != nil {
			return err
		}
		if _, err := a.expect(v, tInt64(), true); err != nil {
			return err
		}
	}
	return nil
}

func (a *analyzer) byItems(items []expr, sc *scope) error {
	for _, it := range items {
		if _, err := a.outputExpr(it, sc); err != nil {
			return err
		}
	}
	return nil
}

// outputExpr types an ORDER BY, GROUP BY or HAVING item, which may also
// name a result column or, as an integer, give its position.
func (a *analyzer) outputExpr(n expr, sc *scope) (value, error) {
	v, err := a.expr(n, sc)
	if err != nil || !v.positional {
		return v, err
	}
	if v.position < 1 || v.position > len(sc.outputs) {
		return value{}, a.errAt("query-unsupported", v.off, "position %d is not a result column", v.position)
	}
	return sc.outputs[v.position-1].v, nil
}

// condition types a WHERE or ON clause, which must be boolean.
func (a *analyzer) condition(n expr, sc *scope) error {
	if n == nil {
		return nil
	}
	v, err := a.expr(n, sc)
	if err != nil {
		return err
	}
	_, err = a.expect(v, tBool(), true)
	return err
}

// targets types a SELECT list or a RETURNING clause.
func (a *analyzer) targets(items []*resultCol, sc *scope) ([]column, error) {
	out := []column{}
	for _, it := range items {
		if it.star {
			cols, err := a.star(it, sc)
			if err != nil {
				return nil, err
			}
			out = append(out, cols...)
			continue
		}
		v, err := a.expr(it.e, sc)
		if err != nil {
			return nil, err
		}
		name := it.alias
		if name == "" && v.param == 0 {
			name = v.name
		}
		v.positional = false
		out = append(out, column{name: name, v: v, off: it.off})
	}
	return out, nil
}

// star expands * or rel.* into the columns of the FROM clause.
func (a *analyzer) star(it *resultCol, sc *scope) ([]column, error) {
	if it.table == "" {
		if len(sc.rels) == 0 {
			return nil, a.errAt("query-unsupported", it.off, "SELECT * needs a FROM clause")
		}
		var out []column
		for _, r := range sc.rels {
			for _, c := range r.columns() {
				if !r.hidden[strings.ToLower(c.name)] {
					out = append(out, c)
				}
			}
		}
		return out, nil
	}
	r, err := a.findRel("", it.table, sc, it.off)
	if err != nil {
		return nil, err
	}
	return r.columns(), nil
}

// columns returns r's columns as a reference reads them: nullable when an
// outer join can null the relation.
func (r *rel) columns() []column {
	out := make([]column, 0, len(r.cols))
	for _, c := range r.cols {
		if r.nullable {
			c.v.nullable = true
		}
		out = append(out, c)
	}
	return out
}

// findRel finds the relation a qualified reference names, searching
// outward from sc. A schema qualifier is an error: the catalog is the main
// schema of the database the connection opens.
func (a *analyzer) findRel(schema, name string, sc *scope, off int) (*rel, error) {
	if schema != "" {
		return nil, a.errAt("query-unsupported", off, "%s.%s names a schema; name tables and columns without it, as the connection picks the database", schema, name)
	}
	for s := sc; s != nil; s = s.parent {
		var match []*rel
		for _, r := range s.rels {
			if strings.EqualFold(r.name, name) {
				match = append(match, r)
			}
		}
		if len(match) > 1 {
			return nil, a.errAt("query-ambiguous-column", off, "%s names more than one relation of the FROM clause; give them different aliases", name)
		}
		if len(match) == 1 {
			return match[0], nil
		}
	}
	return nil, a.errAt("query-unknown-column", off, "%s is not a relation of the FROM clause", name)
}

// findColumn finds an unqualified column, searching outward from sc;
// SQLite's column names ignore case. Once a SELECT's result columns are
// typed, GROUP BY, HAVING and ORDER BY may name them too, by their alias,
// when no column of the FROM clause has the name. An ambiguous name is
// found, with its relation nil.
func (a *analyzer) findColumn(name string, sc *scope) (*rel, column, bool) {
	for s := sc; s != nil; s = s.parent {
		var (
			match []*rel
			col   column
		)
		for _, r := range s.rels {
			for _, c := range r.cols {
				if strings.EqualFold(c.name, name) && !r.hidden[strings.ToLower(c.name)] {
					match = append(match, r)
					col = c
				}
			}
		}
		if len(match) == 0 {
			for _, c := range s.outputs {
				if strings.EqualFold(c.name, name) {
					match = append(match, &rel{})
					col = c
				}
			}
		}
		if len(match) == 1 {
			return match[0], col, true
		}
		if len(match) > 1 {
			return nil, column{}, true
		}
	}
	return nil, column{}, false
}

// tableRel returns the relation a table name names: a CTE in scope, or a
// table or view of the catalog.
func (a *analyzer) tableRel(t *tableRef, sc *scope) (*rel, error) {
	if t.schema != "" {
		return nil, a.errAt("query-unsupported", t.off, "%s.%s names a schema; name tables without it, as the connection picks the database", t.schema, t.name)
	}
	r := &rel{name: t.name}
	if t.alias != "" {
		r.name = t.alias
	}
	if c, ok := sc.cte(t.name); ok {
		r.cols = c.cols
		return r, nil
	}
	ts := a.ix.tables(t.name)
	switch {
	case len(ts) == 0:
		return nil, a.errAt("query-unknown-table", t.off, "%s is not a table or view of the catalog", t.name)
	case len(ts) > 1:
		return nil, a.errAt("query-ambiguous-table", t.off, "%s names a table in more than one catalog", t.name)
	}
	f := ts[0]
	a.catalogs[f.catalog] = true
	q := columnName(f, "")
	a.tables[qkey(q.GetCatalog(), q.GetNamespace(), q.GetObject())] = q
	for _, c := range f.table.GetColumns() {
		r.cols = append(r.cols, column{name: c.GetName().GetStored(), v: value{
			typ:       c.GetType(),
			nullable:  c.GetNullable(),
			annotated: c.GetNullabilitySource() == irv1.NullabilitySource_NULLABILITY_SOURCE_ANNOTATION,
			origin:    columnName(f, c.GetName().GetStored()),
			col:       c,
			name:      c.GetName().GetStored(),
		}})
	}
	return r, nil
}

// from adds the relations of a FROM clause, or of an UPDATE's, to sc.
func (a *analyzer) from(f from, sc *scope) error {
	rels, err := a.fromItem(f, sc)
	if err != nil {
		return err
	}
	sc.rels = append(sc.rels, rels...)
	return nil
}

// fromItem types one FROM item and returns its relations: a table, a
// subquery, which does not see the FROM clause it is in, or a join.
func (a *analyzer) fromItem(f from, sc *scope) ([]*rel, error) {
	switch x := f.(type) {
	case *tableRef:
		r, err := a.tableRel(x, sc)
		if err != nil {
			return nil, err
		}
		return []*rel{r}, nil
	case *subqueryRef:
		cols, err := a.selectStmt(x.sel, &scope{parent: sc.parent})
		if err != nil {
			return nil, err
		}
		return []*rel{{name: x.alias, cols: cols}}, nil
	case *join:
		return a.join(x, sc)
	}
	u := f.(*unsupported)
	return nil, a.errAt("query-unsupported", u.off, "%s", u.msg)
}

// join types a join. An outer join makes the relations on its nullable
// side nullable (§17.2, guide §5.4).
func (a *analyzer) join(j *join, sc *scope) ([]*rel, error) {
	if j.natural {
		return nil, a.errAt("query-unsupported", j.off, "NATURAL joins are not supported in query files; join with ON or USING")
	}
	l, err := a.fromItem(j.left, sc)
	if err != nil {
		return nil, err
	}
	r, err := a.fromItem(j.right, sc)
	if err != nil {
		return nil, err
	}
	switch j.kind {
	case "LEFT":
		nullable(r)
	case "RIGHT":
		nullable(l)
	case "FULL":
		nullable(l)
		nullable(r)
	}
	rels := append(slices.Clone(l), r...)
	if len(j.using) > 0 {
		merged, err := a.using(j.using, l, r, j.kind)
		if err != nil {
			return nil, err
		}
		rels = append([]*rel{merged}, rels...)
	}
	if j.on != nil {
		on := &scope{rels: append(slices.Clone(sc.rels), rels...), parent: sc.parent}
		if err := a.condition(j.on, on); err != nil {
			return nil, err
		}
	}
	return rels, nil
}

func nullable(rels []*rel) {
	for _, r := range rels {
		r.nullable = true
	}
}

// using merges the USING columns of a join into a relation of their own,
// which unqualified references reach: the left side's column, the right
// side's for a right join, and for a full join either side's, NULL only
// when both are.
func (a *analyzer) using(names []name, l, r []*rel, kind string) (*rel, error) {
	merged := &rel{}
	for _, n := range names {
		lr, lc, lok := a.findColumn(n.text, &scope{rels: l})
		rr, rc, rok := a.findColumn(n.text, &scope{rels: r})
		if !lok || !rok || lr == nil || rr == nil {
			return nil, a.errAt("query-unknown-column", n.off, "USING column %s is not a column of exactly one relation on each side of the join", n.text)
		}
		v := read(lr, lc)
		switch kind {
		case "RIGHT":
			v = read(rr, rc)
		case "FULL":
			v.nullable = lc.v.nullable && rc.v.nullable
			v.annotated = !v.nullable && (lc.v.annotated || rc.v.annotated)
		}
		merged.cols = append(merged.cols, column{name: lc.name, v: v})
		for _, x := range []*rel{lr, rr} {
			if x.hidden == nil {
				x.hidden = map[string]bool{}
			}
			x.hidden[strings.ToLower(n.text)] = true
		}
	}
	return merged, nil
}

// targetColumn finds the column of a write's table that n names; a
// generated column cannot be written.
func (a *analyzer) targetColumn(target *rel, n name) (column, error) {
	for _, c := range target.cols {
		if strings.EqualFold(c.name, n.text) {
			if c.v.col.GetGeneration() != nil {
				return column{}, a.errAt("query-unsupported", n.off, "%s is a generated column, which a write cannot set", c.name)
			}
			return c, nil
		}
	}
	return column{}, a.errAt("query-unknown-column", n.off, "%s is not a column of %s", n.text, target.name)
}

// insert types an INSERT or REPLACE: its values take the types of their
// columns. An upsert's DO UPDATE reads the target's row, and the proposed
// one as excluded.
func (a *analyzer) insert(s *insertStmt, parent *scope) ([]column, error) {
	a.writes = true
	sc, err := a.with(s.with, parent)
	if err != nil {
		return nil, err
	}
	target, err := a.tableRel(&s.table, sc)
	if err != nil {
		return nil, err
	}
	var cols []column
	for _, c := range target.cols {
		if c.v.col.GetGeneration() == nil {
			cols = append(cols, c)
		}
	}
	if len(s.cols) > 0 {
		cols = nil
		for _, n := range s.cols {
			c, err := a.targetColumn(target, n)
			if err != nil {
				return nil, err
			}
			cols = append(cols, c)
		}
	}
	if err := a.rows(s.sel, cols, sc); err != nil {
		return nil, err
	}
	excluded := &rel{name: "excluded", cols: target.cols}
	for _, u := range s.upserts {
		on := &scope{rels: []*rel{target}, parent: sc}
		for _, t := range u.target {
			if _, err := a.expr(t, on); err != nil {
				return nil, err
			}
		}
		if err := a.condition(u.targetWhere, on); err != nil {
			return nil, err
		}
		set := &scope{rels: []*rel{target, excluded}, parent: sc}
		if err := a.set(u.sets, set, target); err != nil {
			return nil, err
		}
		if err := a.condition(u.where, set); err != nil {
			return nil, err
		}
	}
	return a.returning(s.returning, target, sc)
}

// rows types the rows an INSERT writes: a VALUES list, whose values take
// their columns' types, or a SELECT, whose columns must have them.
func (a *analyzer) rows(sel *selectStmt, cols []column, sc *scope) error {
	if sel == nil {
		return nil
	}
	if c := sel.cores[0]; len(sel.cores) == 1 && c.values != nil && sel.with == nil && sel.order == nil && sel.limit == nil {
		for _, row := range c.values {
			if len(row) != len(cols) {
				return a.errAt("query-unsupported", row[0].at(), "a VALUES row has %d values for %d columns", len(row), len(cols))
			}
			for i, n := range row {
				v, err := a.expr(n, sc)
				if err != nil {
					return err
				}
				if _, err := a.expect(v, cols[i].v.typ, true); err != nil {
					return err
				}
			}
		}
		return nil
	}
	got, err := a.selectStmt(sel, sc)
	if err != nil {
		return err
	}
	if len(got) != len(cols) {
		return a.errAt("query-unsupported", sel.off, "the SELECT returns %d columns for %d columns", len(got), len(cols))
	}
	for i := range got {
		if _, err := a.expect(got[i].v, cols[i].v.typ, true); err != nil {
			return err
		}
	}
	return nil
}

// set types the assignments of an UPDATE or an upsert's DO UPDATE.
func (a *analyzer) set(list []assign, sc *scope, target *rel) error {
	for _, as := range list {
		if len(as.cols) > 1 {
			return a.errAt("query-unsupported", as.cols[0].off, "assigning a list of columns is not supported in query files; assign each column")
		}
		c, err := a.targetColumn(target, as.cols[0])
		if err != nil {
			return err
		}
		v, err := a.expr(as.e, sc)
		if err != nil {
			return err
		}
		if _, err := a.expect(v, c.v.typ, true); err != nil {
			return err
		}
	}
	return nil
}

// returning types a write's RETURNING clause, which reads the row the
// write left, and returns its columns, or nil without one.
func (a *analyzer) returning(items []*resultCol, target *rel, sc *scope) ([]column, error) {
	if items == nil {
		return nil, nil
	}
	return a.targets(items, &scope{rels: []*rel{target}, parent: sc})
}

// limited refuses the ORDER BY and LIMIT of an UPDATE or DELETE, which
// need a build option SQLite leaves out by default.
func (a *analyzer) limited(limited bool, off int, verb string) error {
	if limited {
		return a.errAt("query-unsupported", off, "%s with ORDER BY or LIMIT needs a SQLite build option; leave them out", verb)
	}
	return nil
}

// update types an UPDATE, which may read other tables with FROM.
func (a *analyzer) update(s *updateStmt, parent *scope) ([]column, error) {
	a.writes = true
	if err := a.limited(s.limited, s.off, "UPDATE"); err != nil {
		return nil, err
	}
	sc, err := a.with(s.with, parent)
	if err != nil {
		return nil, err
	}
	target, err := a.tableRel(&s.table, sc)
	if err != nil {
		return nil, err
	}
	sc.rels = []*rel{target}
	if s.from != nil {
		if err := a.from(s.from, sc); err != nil {
			return nil, err
		}
	}
	if err := a.set(s.sets, sc, target); err != nil {
		return nil, err
	}
	if err := a.condition(s.where, sc); err != nil {
		return nil, err
	}
	return a.returning(s.returning, target, sc.parent)
}

// delete types a DELETE.
func (a *analyzer) delete(s *deleteStmt, parent *scope) ([]column, error) {
	a.writes = true
	if err := a.limited(s.limited, s.off, "DELETE"); err != nil {
		return nil, err
	}
	sc, err := a.with(s.with, parent)
	if err != nil {
		return nil, err
	}
	target, err := a.tableRel(&s.table, sc)
	if err != nil {
		return nil, err
	}
	sc.rels = []*rel{target}
	if err := a.condition(s.where, sc); err != nil {
		return nil, err
	}
	return a.returning(s.returning, target, sc.parent)
}

// position reads an integer literal's value, for ORDER BY 1.
func position(text string) int {
	n, _ := strconv.Atoi(text)
	return n
}
