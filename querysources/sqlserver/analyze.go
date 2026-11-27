package sqlserver

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
	// The list placeholders, by their position among the placeholders,
	// and the type OPENJSON reads each one's items as.
	lists map[int]string
	// The catalogs of the objects the statement uses.
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
	// typed, a string literal or NULL.
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
	// An integer literal, which ORDER BY reads as a result column's
	// position.
	positional bool
	position   int
	// A number constant, which converts to the type it is compared with
	// or assigned to.
	constant bool
	// A function argument a parameter or literal became, whose length
	// the runtime decides.
	soft bool
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

// rel is a relation of a FROM clause: a table or view, a CTE or a
// subquery, or a write's inserted and deleted rows.
type rel struct {
	// The alias, or the table's or CTE's name; the table's schema when it
	// has no alias, which a qualified column may name.
	name, schema string
	cols         []column
	// Set when an outer join can fill the relation with NULLs.
	nullable bool
	// Only a qualified column reaches the relation's columns: inserted's
	// and deleted's.
	qualified bool
	// The relation is a table or view of the catalog.
	table bool
}

// scope is one level of name resolution: a SELECT's FROM items, the CTEs
// it defines, and the scope of the query it is nested in.
type scope struct {
	rels   []*rel
	ctes   map[string]*rel
	parent *scope
	// The query has GROUP BY (§17.2, guide §5.4).
	grouped bool
	// The result columns, which ORDER BY may name.
	outputs []column
}

// cte finds a CTE in scope; one being typed is there as nil.
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
// write without OUTPUT, which returns no rows.
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

// with adds a WITH clause's CTEs to a new scope over parent. A CTE that
// reads itself is recursive, which query files do not support yet.
func (a *analyzer) with(ctes []*cte, parent *scope) (*scope, error) {
	sc := &scope{parent: parent, ctes: map[string]*rel{}}
	for _, c := range ctes {
		key := strings.ToLower(c.name)
		sc.ctes[key] = nil
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
		sc.ctes[key] = &rel{name: c.name, cols: cols}
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
			vs, t, err := a.commonValues([]value{out[j].v, cols[j].v}, c.off)
			if err != nil {
				return nil, err
			}
			lv, rv := vs[0], vs[1]
			v := value{typ: t, nullable: lv.nullable || rv.nullable, annotated: lv.annotated || rv.annotated}
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
	if err := a.byItems(s.order, inner, true); err != nil {
		return nil, err
	}
	for _, n := range []expr{s.offset, s.fetch} {
		if err := a.count(n, inner, tInt64()); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// count types a TOP, OFFSET or FETCH count, of type t.
func (a *analyzer) count(n expr, sc *scope, t *irv1.Type) error {
	if n == nil {
		return nil
	}
	v, err := a.expr(n, sc)
	if err != nil {
		return err
	}
	_, err = a.expect(v, t, true)
	return err
}

// core types one SELECT of a statement, in a scope of its own, which it
// returns for the statement's ORDER BY.
func (a *analyzer) core(c *selectCore, parent *scope) ([]column, *scope, error) {
	sc := &scope{parent: parent}
	if c.from != nil {
		if err := a.from(c.from, sc); err != nil {
			return nil, nil, err
		}
	}
	top := tInt64()
	if c.percent {
		top = tFloat64()
	}
	if err := a.count(c.top, sc, top); err != nil {
		return nil, nil, err
	}
	if err := a.condition(c.where, sc); err != nil {
		return nil, nil, err
	}
	sc.grouped = len(c.group) > 0
	cols, err := a.targets(c.cols, sc)
	if err != nil {
		return nil, nil, err
	}
	if err := a.byItems(c.group, sc, false); err != nil {
		return nil, nil, err
	}
	if err := a.condition(c.having, sc); err != nil {
		return nil, nil, err
	}
	sc.outputs = cols
	return cols, sc, nil
}

// byItems types GROUP BY or ORDER BY items; an ORDER BY item may also name
// a result column or, as an integer, give its position.
func (a *analyzer) byItems(items []expr, sc *scope, order bool) error {
	for _, it := range items {
		v, err := a.expr(it, sc)
		if err != nil {
			return err
		}
		if order && v.positional && (v.position < 1 || v.position > len(sc.outputs)) {
			return a.errAt("query-unsupported", v.off, "position %d is not a result column", v.position)
		}
	}
	return nil
}

// condition types a WHERE, ON or HAVING clause, which must be boolean.
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

// targets types a SELECT list or an OUTPUT clause.
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
		var out []column
		for _, r := range sc.rels {
			if !r.qualified {
				out = append(out, r.columns()...)
			}
		}
		if len(out) == 0 {
			return nil, a.errAt("query-unsupported", it.off, "* needs a FROM clause")
		}
		return out, nil
	}
	schema, name, _ := strings.Cut(it.table, ".")
	if name == "" {
		schema, name = "", schema
	}
	r, err := a.findRel(schema, name, sc, it.off)
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
// outward from sc: by alias, or by a table's name, and its schema when the
// reference names one.
func (a *analyzer) findRel(schema, name string, sc *scope, off int) (*rel, error) {
	for s := sc; s != nil; s = s.parent {
		var match []*rel
		for _, r := range s.rels {
			if strings.EqualFold(r.name, name) && (schema == "" || strings.EqualFold(r.schema, schema)) {
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
	if schema != "" {
		name = schema + "." + name
	}
	return nil, a.errAt("query-unknown-column", off, "%s is not a relation of the FROM clause", name)
}

// findColumn finds an unqualified column, searching outward from sc. Once
// a SELECT's result columns are typed, ORDER BY may name them too, by
// their alias, when no column of the FROM clause has the name. An
// ambiguous name is found, with its relation nil.
func (a *analyzer) findColumn(name string, sc *scope) (*rel, column, bool) {
	for s := sc; s != nil; s = s.parent {
		var (
			match []*rel
			col   column
		)
		for _, r := range s.rels {
			if r.qualified {
				continue
			}
			for _, c := range r.cols {
				if strings.EqualFold(c.name, name) {
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
// table or view of the catalog, which a query names with its schema.
func (a *analyzer) tableRel(t *tableRef, sc *scope) (*rel, error) {
	r := &rel{name: t.name, schema: t.schema}
	if t.alias != "" {
		r.name, r.schema = t.alias, ""
	}
	if strings.HasPrefix(t.schema, "\x00") {
		return nil, a.errAt("query-unsupported", t.off, "%s names a table of another database or server, which query files do not support", t.schema[1:])
	}
	if t.schema == "" {
		c, ok := sc.cte(t.name)
		switch {
		case ok && c == nil:
			return nil, a.errAt("query-unsupported", t.off, "CTE %s reads itself; recursive CTEs are not supported in query files yet", t.name)
		case ok:
			r.cols = c.cols
			return r, nil
		}
		hint := "app." + t.name
		if ts := a.ix.tables("", t.name); len(ts) == 1 {
			hint = ts[0].namespace + "." + ts[0].obj.GetName().GetStored()
		}
		return nil, a.errAt("query-unknown-table", t.off, "%s names no schema; name it with its schema, as in %s, since SQL Server finds an unqualified name in the login's default schema", t.name, hint)
	}
	ts := a.ix.tables(t.schema, t.name)
	switch {
	case len(ts) == 0:
		return nil, a.errAt("query-unknown-table", t.off, "%s.%s is not a table or view of the catalog", t.schema, t.name)
	case len(ts) > 1:
		return nil, a.errAt("query-ambiguous-table", t.off, "%s.%s names a table in more than one catalog", t.schema, t.name)
	}
	f := ts[0]
	r.table = true
	a.catalogs[f.catalog] = true
	q := columnName(f, "")
	a.tables[qkey(q.GetCatalog(), q.GetNamespace(), q.GetObject())] = q
	for _, c := range f.obj.GetColumns() {
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

// from adds the relations of a FROM clause, or of a write's, to sc.
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
		if len(x.cols) > 0 {
			if len(x.cols) != len(cols) {
				return nil, a.errAt("query-unsupported", x.off, "the subquery %s names %d columns but returns %d", x.alias, len(x.cols), len(cols))
			}
			cols = slices.Clone(cols)
			for i, n := range x.cols {
				cols[i].name = n
			}
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

// targetColumn finds the column of a write's table that n names. A
// computed column, a rowversion and an IDENTITY column cannot be written.
func (a *analyzer) targetColumn(target *rel, n name) (column, error) {
	for _, c := range target.cols {
		if !strings.EqualFold(c.name, n.text) {
			continue
		}
		switch {
		case c.v.col.GetGeneration() != nil:
			return column{}, a.errAt("query-unsupported", n.off, "%s is a computed column, which a write cannot set", c.name)
		case identity(c.v.col):
			return column{}, a.errAt("query-unsupported", n.off, "%s is an IDENTITY column, which a write cannot set", c.name)
		}
		return c, nil
	}
	return column{}, a.errAt("query-unknown-column", n.off, "%s is not a column of %s", n.text, target.name)
}

func identity(c *irv1.Column) bool {
	return c.GetDefault().GetClass() == irv1.DefaultClass_DEFAULT_IDENTITY_ALWAYS
}

// writeTarget returns the relation a write's table names, and the scope
// its WHERE and SET see. With a FROM clause, a target that names one of
// its relations, by alias or by name, is that relation; any other target
// is a relation of its own beside them.
func (a *analyzer) writeTarget(t *tableRef, f from, sc *scope) (*rel, *scope, error) {
	inner := &scope{parent: sc}
	if f != nil {
		if err := a.from(f, inner); err != nil {
			return nil, nil, err
		}
		if t.schema == "" {
			for _, r := range inner.rels {
				if r.table && strings.EqualFold(r.name, t.name) {
					return r, inner, nil
				}
			}
		}
	}
	target, err := a.tableRel(t, sc)
	if err != nil {
		return nil, nil, err
	}
	inner.rels = append([]*rel{target}, inner.rels...)
	return target, inner, nil
}

// insert types an INSERT: its values take the types of their columns.
func (a *analyzer) insert(s *insertStmt, parent *scope) ([]column, error) {
	a.writes = true
	sc, err := a.with(s.with, parent)
	if err != nil {
		return nil, err
	}
	if err := a.count(s.top, sc, tInt64()); err != nil {
		return nil, err
	}
	target, err := a.tableRel(&s.table, sc)
	if err != nil {
		return nil, err
	}
	var cols []column
	for _, c := range target.cols {
		if c.v.col.GetGeneration() == nil && !identity(c.v.col) {
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
	for _, row := range s.values {
		if len(row) != len(cols) {
			return nil, a.errAt("query-unsupported", row[0].at(), "a VALUES row has %d values for %d columns", len(row), len(cols))
		}
		for i, n := range row {
			if err := a.assignValue(n, cols[i], sc); err != nil {
				return nil, err
			}
		}
	}
	if s.sel != nil {
		got, err := a.selectStmt(s.sel, sc)
		if err != nil {
			return nil, err
		}
		if len(got) != len(cols) {
			return nil, a.errAt("query-unsupported", s.sel.off, "the SELECT returns %d columns for %d columns", len(got), len(cols))
		}
		for i := range got {
			if _, err := a.expect(got[i].v, cols[i].v.typ, true); err != nil {
				return nil, err
			}
		}
	}
	return a.output(s.output, target, []string{"inserted"}, &scope{parent: sc})
}

// assignValue types a value written to column c: DEFAULT, or an
// expression of c's type.
func (a *analyzer) assignValue(n expr, c column, sc *scope) error {
	if _, ok := n.(*defaultExpr); ok {
		return nil
	}
	v, err := a.expr(n, sc)
	if err != nil {
		return err
	}
	_, err = a.expect(v, c.v.typ, true)
	return err
}

// set types the assignments of an UPDATE.
func (a *analyzer) set(list []assign, sc *scope, target *rel) error {
	for _, as := range list {
		c, err := a.targetColumn(target, as.col)
		if err != nil {
			return err
		}
		if as.op != "" {
			if _, ok := as.e.(*defaultExpr); ok {
				return a.errAt("query-unsupported", as.e.at(), "%s DEFAULT is not a value", as.op)
			}
		}
		if err := a.assignValue(as.e, c, sc); err != nil {
			return err
		}
	}
	return nil
}

// output types a write's OUTPUT clause, which reads the rows the write
// changed as inserted and deleted, and the write's other relations, and
// returns its columns, or nil without one.
func (a *analyzer) output(items []*resultCol, target *rel, names []string, sc *scope) ([]column, error) {
	if items == nil {
		return nil, nil
	}
	out := &scope{parent: sc.parent, rels: slices.Clone(sc.rels)}
	for _, n := range names {
		out.rels = append(out.rels, &rel{name: n, cols: target.cols, qualified: true})
	}
	for _, it := range items {
		if it.star && it.table == "" {
			return nil, a.errAt("query-unsupported", it.off, "OUTPUT names its columns with inserted. or deleted., as in OUTPUT inserted.*")
		}
	}
	return a.targets(items, out)
}

// update types an UPDATE, which may read other tables with FROM.
func (a *analyzer) update(s *updateStmt, parent *scope) ([]column, error) {
	a.writes = true
	sc, err := a.with(s.with, parent)
	if err != nil {
		return nil, err
	}
	if err := a.count(s.top, sc, tInt64()); err != nil {
		return nil, err
	}
	target, inner, err := a.writeTarget(&s.table, s.from, sc)
	if err != nil {
		return nil, err
	}
	if err := a.set(s.sets, inner, target); err != nil {
		return nil, err
	}
	if err := a.condition(s.where, inner); err != nil {
		return nil, err
	}
	return a.output(s.output, target, []string{"inserted", "deleted"}, inner)
}

// delete types a DELETE, which may read other tables with FROM.
func (a *analyzer) delete(s *deleteStmt, parent *scope) ([]column, error) {
	a.writes = true
	sc, err := a.with(s.with, parent)
	if err != nil {
		return nil, err
	}
	if err := a.count(s.top, sc, tInt64()); err != nil {
		return nil, err
	}
	target, inner, err := a.writeTarget(&s.table, s.from, sc)
	if err != nil {
		return nil, err
	}
	if err := a.condition(s.where, inner); err != nil {
		return nil, err
	}
	return a.output(s.output, target, []string{"deleted"}, inner)
}

// position reads an integer literal's value, for ORDER BY 1.
func position(text string) int {
	n, _ := strconv.Atoi(text)
	return n
}
