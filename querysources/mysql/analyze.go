package mysql

import (
	"fmt"
	"slices"
	"strings"

	"github.com/pingcap/tidb/pkg/parser/ast"

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
	// The JSON_TABLE column type of each list placeholder, by its
	// position among the placeholders.
	lists map[int]string
	// The catalogs of the tables and routines the statement uses.
	catalogs map[string]bool
	// The tables the statement reads or writes, by qualified name.
	tables map[string]*irv1.QualifiedName
	// Set by a statement that writes, a locking read, or a call of a
	// volatile function (§17.2, §22).
	writes bool
	// The table an INSERT writes, while its ON DUPLICATE KEY UPDATE is
	// typed: VALUES(column) reads the row it proposed.
	values *rel
	// The time unit of the call being typed, as in DATE_ADD(d, INTERVAL 1
	// DAY).
	unit ast.TimeUnitType
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
	// The placeholder's position, for a list's JSON_TABLE.
	use int
	// An untyped string literal: text unless its context says otherwise.
	literal bool
	// The name MySQL gives a result column of this expression, which
	// query files keep only for a column.
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

// rel is a relation of a FROM clause: a table or view, a CTE or a derived
// table.
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
		if r, ok := s.ctes[name]; ok {
			return r, true
		}
	}
	return nil, false
}

// statement types a statement, nested in parent's scope or, for the
// query's own statement, nil, and returns its result columns: nil for a
// statement that returns no rows, as MySQL's writes all do.
func (a *analyzer) statement(n ast.Node, parent *scope) ([]column, error) {
	switch s := n.(type) {
	case *ast.SelectStmt:
		return a.selectStmt(s, parent)
	case *ast.SetOprStmt:
		return a.setOpStmt(s, parent)
	case *ast.InsertStmt:
		return nil, a.insert(s, parent)
	case *ast.UpdateStmt:
		return nil, a.update(s, parent)
	case *ast.DeleteStmt:
		return nil, a.delete(s, parent)
	}
	return nil, a.errAt("query-unsupported", -1, "only SELECT, INSERT, REPLACE, UPDATE and DELETE statements are supported in query files")
}

// with adds a WITH clause's CTEs to a new scope over parent.
func (a *analyzer) with(w *ast.WithClause, parent *scope) (*scope, error) {
	sc := &scope{parent: parent, ctes: map[string]*rel{}}
	if w == nil {
		return sc, nil
	}
	if w.IsRecursive {
		return nil, a.errAt("query-unsupported", -1, "WITH RECURSIVE is not supported in query files yet")
	}
	for _, c := range w.CTEs {
		cols, err := a.statement(c.Query.Query, sc)
		if err != nil {
			return nil, err
		}
		if names := c.ColNameList; len(names) > 0 {
			if len(names) != len(cols) {
				return nil, a.errAt("query-unsupported", c.Query.OriginTextPosition(), "CTE %s names %d columns but returns %d", c.Name.O, len(names), len(cols))
			}
			cols = slices.Clone(cols)
			for i, nm := range names {
				cols[i].name = nm.O
			}
		}
		sc.ctes[c.Name.O] = &rel{name: c.Name.O, cols: cols}
	}
	return sc, nil
}

// resultSet types a query that returns rows: a SELECT or a set operation.
func (a *analyzer) resultSet(n ast.Node, parent *scope) ([]column, error) {
	if s, ok := n.(*ast.SetOprStmt); ok {
		return a.setOpStmt(s, parent)
	}
	return a.selectStmt(n.(*ast.SelectStmt), parent)
}

// selectStmt types a SELECT.
func (a *analyzer) selectStmt(s *ast.SelectStmt, parent *scope) ([]column, error) {
	switch {
	case s.Kind != ast.SelectStmtKindSelect:
		return nil, a.errAt("query-unsupported", -1, "TABLE and VALUES statements are not supported in query files; write SELECT")
	case s.SelectIntoOpt != nil:
		return nil, a.errAt("query-unsupported", -1, "SELECT INTO is not supported in query files")
	case len(s.WindowSpecs) > 0:
		return nil, a.errAt("query-unsupported", -1, "named windows are not supported in query files")
	}
	sc, err := a.with(s.With, parent)
	if err != nil {
		return nil, err
	}
	if s.From != nil {
		if err := a.from(s.From.TableRefs, sc); err != nil {
			return nil, err
		}
	}
	if err := a.condition(s.Where, sc); err != nil {
		return nil, err
	}
	sc.grouped = s.GroupBy != nil
	cols, err := a.targets(s.Fields.Fields, sc)
	if err != nil {
		return nil, err
	}
	sc.outputs = cols
	if s.GroupBy != nil {
		if err := a.byItems(s.GroupBy.Items, sc); err != nil {
			return nil, err
		}
	}
	if s.Having != nil {
		v, err := a.outputExpr(s.Having.Expr, sc)
		if err != nil {
			return nil, err
		}
		if _, err := a.expect(v, tBool(), true); err != nil {
			return nil, err
		}
	}
	if err := a.order(s.OrderBy, s.Limit, sc); err != nil {
		return nil, err
	}
	if s.LockInfo != nil && s.LockInfo.LockType != ast.SelectLockNone {
		a.writes = true
	}
	return cols, nil
}

// order types ORDER BY and LIMIT.
func (a *analyzer) order(by *ast.OrderByClause, limit *ast.Limit, sc *scope) error {
	if by != nil {
		if err := a.byItems(by.Items, sc); err != nil {
			return err
		}
	}
	if limit == nil {
		return nil
	}
	for _, n := range []ast.ExprNode{limit.Count, limit.Offset} {
		if n == nil {
			continue
		}
		// LIMIT takes a literal or a placeholder, which type without error.
		v, _ := a.expr(n, sc)
		if _, err := a.expect(v, tInt64(), true); err != nil {
			return err
		}
	}
	return nil
}

func (a *analyzer) byItems(items []*ast.ByItem, sc *scope) error {
	for _, it := range items {
		if _, err := a.outputExpr(it.Expr, sc); err != nil {
			return err
		}
	}
	return nil
}

// setOpStmt types UNION, EXCEPT and INTERSECT: the first query names the
// columns, a column is nullable when any side's is, and a parameter or
// literal takes its type from another side.
func (a *analyzer) setOpStmt(s *ast.SetOprStmt, parent *scope) ([]column, error) {
	sc, err := a.with(s.With, parent)
	if err != nil {
		return nil, err
	}
	cols, err := a.setOpList(s.SelectList, sc)
	if err != nil {
		return nil, err
	}
	sc.outputs = cols
	return cols, a.order(s.OrderBy, s.Limit, sc)
}

func (a *analyzer) setOpList(l *ast.SetOprSelectList, sc *scope) ([]column, error) {
	if l.With != nil || l.OrderBy != nil || l.Limit != nil {
		inner, err := a.with(l.With, sc)
		if err != nil {
			return nil, err
		}
		sc = inner
	}
	var out []column
	for i, n := range l.Selects {
		var (
			cols []column
			err  error
		)
		if nested, ok := n.(*ast.SetOprSelectList); ok {
			cols, err = a.setOpList(nested, sc)
		} else {
			cols, err = a.selectStmt(n.(*ast.SelectStmt), sc)
		}
		if err != nil {
			return nil, err
		}
		if i == 0 {
			out = cols
			continue
		}
		if len(cols) != len(out) {
			return nil, a.errAt("query-unsupported", -1, "the queries of a set operation return %d and %d columns", len(out), len(cols))
		}
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
	sc.outputs = out
	return out, a.order(l.OrderBy, l.Limit, sc)
}

// condition types a WHERE or ON clause, which must be boolean.
func (a *analyzer) condition(n ast.ExprNode, sc *scope) error {
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

// outputExpr types an ORDER BY, GROUP BY or HAVING item, which may also
// name a result column or give its position.
func (a *analyzer) outputExpr(n ast.ExprNode, sc *scope) (value, error) {
	if pos, ok := n.(*ast.PositionExpr); ok {
		if pos.N < 1 || pos.N > len(sc.outputs) {
			return value{}, a.errAt("query-unsupported", -1, "position %d is not a result column", pos.N)
		}
		return sc.outputs[pos.N-1].v, nil
	}
	return a.expr(n, sc)
}

// targets types a SELECT list.
func (a *analyzer) targets(fields []*ast.SelectField, sc *scope) ([]column, error) {
	var out []column
	for _, f := range fields {
		if w := f.WildCard; w != nil {
			cols, err := a.star(w, sc)
			if err != nil {
				return nil, err
			}
			out = append(out, cols...)
			continue
		}
		v, err := a.expr(f.Expr, sc)
		if err != nil {
			return nil, err
		}
		name := f.AsName.O
		if name == "" {
			name = v.name
		}
		out = append(out, column{name: name, v: v, off: f.Expr.OriginTextPosition()})
	}
	return out, nil
}

// star expands * or rel.* into the columns of the FROM clause.
func (a *analyzer) star(w *ast.WildCardField, sc *scope) ([]column, error) {
	if w.Table.O == "" {
		if len(sc.rels) == 0 {
			return nil, a.errAt("query-unsupported", -1, "SELECT * needs a FROM clause")
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
	r, err := a.findRel(w.Schema.O, w.Table.O, sc, -1)
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
// outward from sc. A database qualifier is an error: the catalog is one
// database, and the generated code runs on whichever the connection names.
func (a *analyzer) findRel(schema, name string, sc *scope, off int) (*rel, error) {
	if schema != "" {
		return nil, a.errAt("query-unsupported", off, "%s.%s names a database; name tables and columns without it, as the connection picks the database", schema, name)
	}
	for s := sc; s != nil; s = s.parent {
		var match []*rel
		for _, r := range s.rels {
			if r.name == name {
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
// MySQL's column names ignore case. Once a SELECT's result columns are
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
func (a *analyzer) tableRel(tn *ast.TableName, alias string, sc *scope) (*rel, error) {
	off := -1
	if tn.Schema.O != "" {
		return nil, a.errAt("query-unsupported", off, "%s.%s names a database; name tables without it, as the connection picks the database", tn.Schema.O, tn.Name.O)
	}
	r := &rel{name: tn.Name.O}
	if alias != "" {
		r.name = alias
	}
	if c, ok := sc.cte(tn.Name.O); ok {
		r.cols = c.cols
		return r, nil
	}
	ts := a.ix.tables(tn.Name.O)
	switch {
	case len(ts) == 0:
		return nil, a.errAt("query-unknown-table", off, "%s is not a table or view of the catalog", tn.Name.O)
	case len(ts) > 1:
		return nil, a.errAt("query-ambiguous-table", off, "%s names a table in more than one catalog", tn.Name.O)
	}
	t := ts[0]
	a.catalogs[t.catalog] = true
	q := columnName(t, "")
	a.tables[qkey(q.GetCatalog(), q.GetNamespace(), q.GetObject())] = q
	for _, c := range t.obj.GetColumns() {
		r.cols = append(r.cols, column{name: c.GetName().GetStored(), v: value{
			typ:       c.GetType(),
			nullable:  c.GetNullable(),
			annotated: c.GetNullabilitySource() == irv1.NullabilitySource_NULLABILITY_SOURCE_ANNOTATION,
			origin:    columnName(t, c.GetName().GetStored()),
			col:       c,
			name:      c.GetName().GetStored(),
		}})
	}
	return r, nil
}

// from adds the relations of a FROM clause, or of an UPDATE's or DELETE's
// tables, to sc.
func (a *analyzer) from(n ast.ResultSetNode, sc *scope) error {
	rels, err := a.fromItem(n, sc)
	if err != nil {
		return err
	}
	sc.rels = append(sc.rels, rels...)
	return nil
}

// fromItem types one FROM item, a join or a table source, and returns its
// relations. The parser flattens parenthesized joins.
func (a *analyzer) fromItem(n ast.ResultSetNode, sc *scope) ([]*rel, error) {
	if j, ok := n.(*ast.Join); ok {
		return a.join(j, sc)
	}
	return a.tableSource(n.(*ast.TableSource), sc)
}

// tableSource types a table or a derived table: a SELECT or a set
// operation, which sees the enclosing query's relations only when LATERAL.
func (a *analyzer) tableSource(ts *ast.TableSource, sc *scope) ([]*rel, error) {
	if tn, ok := ts.Source.(*ast.TableName); ok {
		r, err := a.tableRel(tn, ts.AsName.O, sc)
		if err != nil {
			return nil, err
		}
		return []*rel{r}, nil
	}
	parent := sc.parent
	if ts.Lateral {
		parent = sc
	}
	cols, err := a.resultSet(ts.Source, &scope{parent: parent, ctes: sc.ctes})
	if err != nil {
		return nil, err
	}
	return []*rel{{name: ts.AsName.O, cols: cols}}, nil
}

// join types a join. An outer join makes the relations on its nullable
// side nullable (§17.2, guide §5.4).
func (a *analyzer) join(j *ast.Join, sc *scope) ([]*rel, error) {
	if j.NaturalJoin {
		return nil, a.errAt("query-unsupported", -1, "NATURAL joins are not supported in query files; join with ON or USING")
	}
	l, err := a.fromItem(j.Left, sc)
	if err != nil || j.Right == nil {
		return l, err
	}
	// A LATERAL derived table on the right sees the relations on the left.
	r, err := a.fromItem(j.Right, &scope{rels: append(slices.Clone(sc.rels), l...), parent: sc.parent, ctes: sc.ctes})
	if err != nil {
		return nil, err
	}
	switch j.Tp {
	case ast.LeftJoin:
		nullable(r)
	case ast.RightJoin:
		nullable(l)
	}
	rels := append(slices.Clone(l), r...)
	if len(j.Using) > 0 {
		merged, err := a.using(j.Using, l, r, j.Tp)
		if err != nil {
			return nil, err
		}
		rels = append([]*rel{merged}, rels...)
	}
	if j.On != nil {
		on := &scope{rels: append(slices.Clone(sc.rels), rels...), parent: sc.parent, ctes: sc.ctes}
		if err := a.condition(j.On.Expr, on); err != nil {
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
// which unqualified references reach: the left side's column, or the right
// side's for a right join.
func (a *analyzer) using(names []*ast.ColumnName, l, r []*rel, kind ast.JoinType) (*rel, error) {
	merged := &rel{}
	for _, n := range names {
		name := n.Name.O
		lr, lc, lok := a.findColumn(name, &scope{rels: l})
		rr, rc, rok := a.findColumn(name, &scope{rels: r})
		if !lok || !rok || lr == nil || rr == nil {
			return nil, a.errAt("query-unknown-column", -1, "USING column %s is not a column of exactly one relation on each side of the join", name)
		}
		v := read(lr, lc)
		if kind == ast.RightJoin {
			v = read(rr, rc)
		}
		merged.cols = append(merged.cols, column{name: lc.name, v: v})
		for _, x := range []*rel{lr, rr} {
			if x.hidden == nil {
				x.hidden = map[string]bool{}
			}
			x.hidden[strings.ToLower(name)] = true
		}
	}
	return merged, nil
}

// target returns the one table an INSERT writes, which the parser reads
// only as a table name.
func (a *analyzer) target(refs *ast.TableRefsClause, sc *scope) (*rel, error) {
	ts := refs.TableRefs.Left.(*ast.TableSource)
	return a.tableRel(ts.Source.(*ast.TableName), ts.AsName.O, sc)
}

// insert types an INSERT or REPLACE: its values take the types of their
// columns.
func (a *analyzer) insert(s *ast.InsertStmt, parent *scope) error {
	a.writes = true
	sc := &scope{parent: parent}
	target, err := a.target(s.Table, sc)
	if err != nil {
		return err
	}
	cols := target.cols
	if len(s.Columns) > 0 {
		cols = nil
		for _, n := range s.Columns {
			c, err := a.targetColumn(target, n)
			if err != nil {
				return err
			}
			cols = append(cols, c)
		}
	}
	for _, row := range s.Lists {
		if len(row) != len(cols) {
			return a.errAt("query-unsupported", -1, "a VALUES row has %d values for %d columns", len(row), len(cols))
		}
		if err := a.assign(cols, row, sc); err != nil {
			return err
		}
	}
	if s.Select != nil {
		got, err := a.resultSet(s.Select, sc)
		if err != nil {
			return err
		}
		if len(got) != len(cols) {
			return a.errAt("query-unsupported", -1, "the SELECT returns %d columns for %d columns", len(got), len(cols))
		}
		for i := range got {
			if _, err := a.expect(got[i].v, cols[i].v.typ, true); err != nil {
				return err
			}
		}
	}
	// ON DUPLICATE KEY UPDATE reads the target's row, and the proposed one
	// through VALUES(column).
	return a.set(s.OnDuplicate, &scope{rels: []*rel{target}, parent: sc}, target)
}

// assign types values written into columns.
func (a *analyzer) assign(cols []column, vals []ast.ExprNode, sc *scope) error {
	for i, n := range vals {
		if _, ok := n.(*ast.DefaultExpr); ok {
			continue
		}
		v, err := a.expr(n, sc)
		if err != nil {
			return err
		}
		if _, err := a.expect(v, cols[i].v.typ, true); err != nil {
			return err
		}
	}
	return nil
}

// targetColumn finds the column an INSERT column list names.
func (a *analyzer) targetColumn(target *rel, n *ast.ColumnName) (column, error) {
	for _, c := range target.cols {
		if strings.EqualFold(c.name, n.Name.O) {
			return c, nil
		}
	}
	return column{}, a.errAt("query-unknown-column", -1, "%s is not a column of %s", n.Name.O, target.name)
}

// set types an UPDATE's or ON DUPLICATE KEY UPDATE's assignments. insert is
// the table an INSERT writes, whose proposed row VALUES() reads.
func (a *analyzer) set(list []*ast.Assignment, sc *scope, insert *rel) error {
	for _, as := range list {
		ref := &ast.ColumnNameExpr{Name: as.Column}
		ref.SetOriginTextPosition(-1)
		c, err := a.columnRef(ref, sc)
		if err != nil {
			return err
		}
		if _, ok := as.Expr.(*ast.DefaultExpr); ok {
			continue
		}
		a.values = insert
		v, err := a.expr(as.Expr, sc)
		a.values = nil
		if err != nil {
			return err
		}
		if _, err := a.expect(v, c.typ, true); err != nil {
			return err
		}
	}
	return nil
}

// update types an UPDATE, of one table or of a join.
func (a *analyzer) update(s *ast.UpdateStmt, parent *scope) error {
	a.writes = true
	sc, err := a.with(s.With, parent)
	if err != nil {
		return err
	}
	if err := a.from(s.TableRefs.TableRefs, sc); err != nil {
		return err
	}
	if err := a.set(s.List, sc, nil); err != nil {
		return err
	}
	if err := a.condition(s.Where, sc); err != nil {
		return err
	}
	return a.order(s.Order, s.Limit, sc)
}

// delete types a DELETE, of one table or of the tables of a join.
func (a *analyzer) delete(s *ast.DeleteStmt, parent *scope) error {
	a.writes = true
	sc, err := a.with(s.With, parent)
	if err != nil {
		return err
	}
	if err := a.from(s.TableRefs.TableRefs, sc); err != nil {
		return err
	}
	if s.Tables != nil {
		for _, t := range s.Tables.Tables {
			if _, err := a.findRel(t.Schema.O, t.Name.O, sc, -1); err != nil {
				return err
			}
		}
	}
	if err := a.condition(s.Where, sc); err != nil {
		return err
	}
	return a.order(s.Order, s.Limit, sc)
}
