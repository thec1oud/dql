package postgres

import (
	"fmt"
	"slices"

	pg "github.com/pganalyze/pg_query_go/v6"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// queryError is a problem with one query: a diagnostic code, a message and
// where in the rewritten statement it is, or -1.
type queryError struct {
	code string
	off  int
	msg  string
}

func (e *queryError) Error() string { return e.msg }

func (a *analyzer) errAt(code string, loc int32, format string, args ...any) error {
	return &queryError{code: code, off: int(loc), msg: fmt.Sprintf(format, args...)}
}

// analyzer types one statement against the catalog (§17.2).
type analyzer struct {
	ix *index
	// The parameters by placeholder number - 1, as rewrite found them.
	params []*param
	// The catalogs of the tables and routines the statement uses.
	catalogs map[string]bool
	// The tables the statement reads or writes, by qualified name.
	tables map[string]*irv1.QualifiedName
	// Set by a statement that writes, a locking read, or a call of a
	// volatile function (§17.2, §22).
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
	// The column the value reads unchanged.
	origin *irv1.QualifiedName
	// The placeholder number of a bare parameter.
	param int
	// An untyped string literal: text unless its context says otherwise.
	literal bool
	// The name Postgres gives a result column of this expression; empty
	// where Postgres writes ?column?.
	name string
	// Where a parameter is used.
	loc int32
}

// column is a column of a relation or of a result.
type column struct {
	// Empty for a result column Postgres would name ?column?.
	name string
	v    value
	// Where a result column is written.
	loc int32
}

// rel is a relation of a FROM clause: a table or view, a CTE or a subquery.
type rel struct {
	// The alias, or the table's or CTE's name.
	name string
	// The table's namespace, for references such as app.users.id.
	namespace string
	cols      []column
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
	// The result columns, which ORDER BY, GROUP BY and DISTINCT ON may
	// name.
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
// statement that returns no rows.
func (a *analyzer) statement(n *pg.Node, parent *scope) ([]column, error) {
	switch s := n.GetNode().(type) {
	case *pg.Node_SelectStmt:
		return a.selectStmt(s.SelectStmt, parent)
	case *pg.Node_InsertStmt:
		return a.insert(s.InsertStmt, parent)
	case *pg.Node_UpdateStmt:
		return a.update(s.UpdateStmt, parent)
	case *pg.Node_DeleteStmt:
		return a.delete(s.DeleteStmt, parent)
	}
	return nil, a.errAt("query-unsupported", location(n), "only SELECT, INSERT, UPDATE and DELETE statements are supported in query files")
}

// with adds a WITH clause's CTEs to a new scope over parent.
func (a *analyzer) with(w *pg.WithClause, parent *scope) (*scope, error) {
	sc := &scope{parent: parent, ctes: map[string]*rel{}}
	if w == nil {
		return sc, nil
	}
	if w.GetRecursive() {
		return nil, a.errAt("query-unsupported", w.GetLocation(), "WITH RECURSIVE is not supported in query files yet")
	}
	for _, n := range w.GetCtes() {
		c := n.GetCommonTableExpr()
		cols, err := a.statement(c.GetCtequery(), sc)
		if err != nil {
			return nil, err
		}
		if cols == nil {
			return nil, a.errAt("query-unsupported", c.GetLocation(), "CTE %s returns no rows; add RETURNING", c.GetCtename())
		}
		if names := c.GetAliascolnames(); len(names) > 0 {
			if len(names) > len(cols) {
				return nil, a.errAt("query-unsupported", c.GetLocation(), "CTE %s names %d columns but returns %d", c.GetCtename(), len(names), len(cols))
			}
			cols = slices.Clone(cols)
			for i, nm := range names {
				cols[i].name = nm.GetString_().GetSval()
			}
		}
		sc.ctes[c.GetCtename()] = &rel{name: c.GetCtename(), cols: cols}
	}
	return sc, nil
}

// selectStmt types a SELECT, a set operation or a VALUES list.
func (a *analyzer) selectStmt(s *pg.SelectStmt, parent *scope) ([]column, error) {
	sc, err := a.with(s.GetWithClause(), parent)
	if err != nil {
		return nil, err
	}
	var cols []column
	switch {
	case s.GetOp() != pg.SetOperation_SETOP_NONE:
		cols, err = a.setOp(s, sc)
	case len(s.GetValuesLists()) > 0:
		return nil, a.errAt("query-unsupported", 0, "a VALUES list is supported only in INSERT")
	default:
		cols, err = a.plainSelect(s, sc)
	}
	if err != nil {
		return nil, err
	}
	sc.outputs = cols
	for _, n := range s.GetSortClause() {
		if _, err := a.outputExpr(n.GetSortBy().GetNode(), sc); err != nil {
			return nil, err
		}
	}
	for _, n := range []*pg.Node{s.GetLimitCount(), s.GetLimitOffset()} {
		if n == nil {
			continue
		}
		v, err := a.expr(n, sc)
		if err != nil {
			return nil, err
		}
		if _, err := a.expect(v, tInt64(), true); err != nil {
			return nil, err
		}
	}
	if len(s.GetLockingClause()) > 0 {
		a.writes = true
	}
	return cols, nil
}

// setOp types UNION, INTERSECT and EXCEPT: the left query names the columns,
// a column is nullable when either side's is, and a parameter or literal
// takes its type from the other side.
func (a *analyzer) setOp(s *pg.SelectStmt, sc *scope) ([]column, error) {
	l, err := a.selectStmt(s.GetLarg(), sc)
	if err != nil {
		return nil, err
	}
	r, err := a.selectStmt(s.GetRarg(), sc)
	if err != nil {
		return nil, err
	}
	if len(l) != len(r) {
		return nil, a.errAt("query-unsupported", 0, "the queries of a set operation return %d and %d columns", len(l), len(r))
	}
	out := make([]column, len(l))
	for i := range l {
		lv, rv, err := a.unify(l[i].v, r[i].v, true)
		if err != nil {
			return nil, err
		}
		v := value{typ: lv.typ, nullable: lv.nullable || rv.nullable, annotated: lv.annotated || rv.annotated, literal: lv.literal}
		if lv.origin != nil && rv.origin != nil && lv.origin.String() == rv.origin.String() {
			v.origin = lv.origin
		}
		out[i] = column{name: l[i].name, v: v}
	}
	return out, nil
}

// plainSelect types a SELECT's FROM, WHERE, GROUP BY, result and HAVING.
func (a *analyzer) plainSelect(s *pg.SelectStmt, sc *scope) ([]column, error) {
	if s.GetIntoClause() != nil {
		return nil, a.errAt("query-unsupported", 0, "SELECT INTO is not supported in query files")
	}
	if err := a.from(s.GetFromClause(), sc); err != nil {
		return nil, err
	}
	if err := a.condition(s.GetWhereClause(), sc); err != nil {
		return nil, err
	}
	sc.grouped = len(s.GetGroupClause()) > 0
	cols, err := a.targets(s.GetTargetList(), sc)
	if err != nil {
		return nil, err
	}
	sc.outputs = cols
	for _, n := range s.GetGroupClause() {
		if _, err := a.outputExpr(n, sc); err != nil {
			return nil, err
		}
	}
	if err := a.condition(s.GetHavingClause(), sc); err != nil {
		return nil, err
	}
	for _, n := range s.GetDistinctClause() {
		if n.GetNode() == nil {
			continue
		}
		if _, err := a.outputExpr(n, sc); err != nil {
			return nil, err
		}
	}
	return cols, nil
}

// condition types a WHERE, HAVING or ON clause, which must be boolean.
func (a *analyzer) condition(n *pg.Node, sc *scope) error {
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

// outputExpr types an ORDER BY, GROUP BY or DISTINCT ON item, which may
// also name a result column or give its position.
func (a *analyzer) outputExpr(n *pg.Node, sc *scope) (value, error) {
	if c := n.GetAConst(); c != nil && c.GetIval() != nil {
		return value{typ: tInt32()}, nil
	}
	if ref := n.GetColumnRef(); ref != nil {
		if names, ok := nameList(ref.GetFields()); ok && len(names) == 1 {
			if _, _, found := a.findColumn(names[0], sc); !found {
				for _, c := range sc.outputs {
					if c.name == names[0] {
						return c.v, nil
					}
				}
			}
		}
	}
	return a.expr(n, sc)
}

// targets types a SELECT list or a RETURNING list.
func (a *analyzer) targets(list []*pg.Node, sc *scope) ([]column, error) {
	var out []column
	for _, n := range list {
		rt := n.GetResTarget()
		if ref := rt.GetVal().GetColumnRef(); ref != nil && isStar(ref) {
			cols, err := a.star(ref, sc)
			if err != nil {
				return nil, err
			}
			out = append(out, cols...)
			continue
		}
		v, err := a.expr(rt.GetVal(), sc)
		if err != nil {
			return nil, err
		}
		name := rt.GetName()
		if name == "" {
			name = v.name
		}
		out = append(out, column{name: name, v: v, loc: rt.GetLocation()})
	}
	return out, nil
}

func isStar(ref *pg.ColumnRef) bool {
	f := ref.GetFields()
	return f[len(f)-1].GetAStar() != nil
}

// star expands * or rel.* into the columns of the FROM clause.
func (a *analyzer) star(ref *pg.ColumnRef, sc *scope) ([]column, error) {
	f := ref.GetFields()
	if len(f) == 1 {
		if len(sc.rels) == 0 {
			return nil, a.errAt("query-unsupported", ref.GetLocation(), "SELECT * needs a FROM clause")
		}
		var out []column
		for _, r := range sc.rels {
			out = append(out, r.columns()...)
		}
		return out, nil
	}
	names, ok := nameList(f[:len(f)-1])
	if !ok || len(names) > 2 {
		return nil, a.errAt("query-unsupported", ref.GetLocation(), "this column reference is not supported")
	}
	r, err := a.findRel(names, sc, ref.GetLocation())
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

// findRel finds the relation a qualified reference names, as rel or
// namespace.rel, searching outward from sc.
func (a *analyzer) findRel(names []string, sc *scope, loc int32) (*rel, error) {
	for s := sc; s != nil; s = s.parent {
		var match []*rel
		for _, r := range s.rels {
			if r.name == names[len(names)-1] && (len(names) == 1 || r.namespace == names[0]) {
				match = append(match, r)
			}
		}
		if len(match) > 1 {
			return nil, a.errAt("query-ambiguous-column", loc, "%s names more than one relation of the FROM clause; give them different aliases", joinDots(names))
		}
		if len(match) == 1 {
			return match[0], nil
		}
	}
	return nil, a.errAt("query-unknown-column", loc, "%s is not a relation of the FROM clause", joinDots(names))
}

// findColumn finds an unqualified column, searching outward from sc. An
// ambiguous name is found, with its relation nil.
func (a *analyzer) findColumn(name string, sc *scope) (*rel, column, bool) {
	for s := sc; s != nil; s = s.parent {
		var (
			match []*rel
			col   column
		)
		for _, r := range s.rels {
			if r.hidden[name] {
				continue
			}
			for _, c := range r.cols {
				if c.name == name {
					match = append(match, r)
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

// tableRel returns the relation a RangeVar names: a CTE in scope, or a
// table or view of the catalog.
func (a *analyzer) tableRel(rv *pg.RangeVar, sc *scope) (*rel, error) {
	alias := rv.GetAlias().GetAliasname()
	if rv.GetSchemaname() == "" && rv.GetCatalogname() == "" {
		if c, ok := sc.cte(rv.GetRelname()); ok {
			r := &rel{name: c.name, cols: c.cols}
			if alias != "" {
				r.name = alias
			}
			return a.renamed(r, rv.GetAlias())
		}
	}
	ts := a.ix.tables(rv.GetCatalogname(), rv.GetSchemaname(), rv.GetRelname())
	name := joinDots(slices.DeleteFunc([]string{rv.GetCatalogname(), rv.GetSchemaname(), rv.GetRelname()}, func(s string) bool { return s == "" }))
	switch {
	case len(ts) == 0:
		return nil, a.errAt("query-unknown-table", rv.GetLocation(), "%s is not a table or view of the catalog", name)
	case len(ts) > 1:
		return nil, a.errAt("query-ambiguous-table", rv.GetLocation(), "%s names a table in more than one namespace or catalog; qualify it", name)
	}
	t := ts[0]
	a.catalogs[t.catalog] = true
	q := columnName(t, "")
	a.tables[qkey(q.GetCatalog(), q.GetNamespace(), q.GetObject())] = q
	r := &rel{name: t.obj.GetName().GetStored(), namespace: t.namespace}
	if alias != "" {
		r.name, r.namespace = alias, ""
	}
	for _, c := range t.obj.GetColumns() {
		r.cols = append(r.cols, column{name: c.GetName().GetStored(), v: value{
			typ:       c.GetType(),
			nullable:  c.GetNullable(),
			annotated: c.GetNullabilitySource() == irv1.NullabilitySource_NULLABILITY_SOURCE_ANNOTATION,
			origin:    columnName(t, c.GetName().GetStored()),
			name:      c.GetName().GetStored(),
		}})
	}
	return a.renamed(r, rv.GetAlias())
}

// renamed applies an alias's column names, such as t(a, b), to r.
func (a *analyzer) renamed(r *rel, alias *pg.Alias) (*rel, error) {
	names := alias.GetColnames()
	if len(names) == 0 {
		return r, nil
	}
	if len(names) > len(r.cols) {
		return nil, a.errAt("query-unsupported", 0, "%s has %d columns but its alias names %d", r.name, len(r.cols), len(names))
	}
	r.cols = slices.Clone(r.cols)
	for i, n := range names {
		r.cols[i].name = n.GetString_().GetSval()
	}
	return r, nil
}

// from adds the relations of a FROM clause to sc.
func (a *analyzer) from(items []*pg.Node, sc *scope) error {
	for _, n := range items {
		rels, err := a.fromItem(n, sc)
		if err != nil {
			return err
		}
		sc.rels = append(sc.rels, rels...)
	}
	return nil
}

// fromItem types one FROM item and returns its relations. A join's ON
// clause sees the relations of both its sides.
func (a *analyzer) fromItem(n *pg.Node, sc *scope) ([]*rel, error) {
	switch x := n.GetNode().(type) {
	case *pg.Node_RangeVar:
		r, err := a.tableRel(x.RangeVar, sc)
		if err != nil {
			return nil, err
		}
		return []*rel{r}, nil
	case *pg.Node_RangeSubselect:
		parent := sc.parent
		if x.RangeSubselect.GetLateral() {
			parent = sc
		}
		inner := &scope{parent: parent, ctes: sc.ctes}
		cols, err := a.selectStmt(x.RangeSubselect.GetSubquery().GetSelectStmt(), inner)
		if err != nil {
			return nil, err
		}
		r, err := a.renamed(&rel{name: x.RangeSubselect.GetAlias().GetAliasname(), cols: cols}, x.RangeSubselect.GetAlias())
		if err != nil {
			return nil, err
		}
		return []*rel{r}, nil
	case *pg.Node_JoinExpr:
		return a.join(x.JoinExpr, sc)
	}
	return nil, a.errAt("query-unsupported", 0, "this FROM item is not supported in query files; use tables, views, CTEs, subqueries and joins")
}

// join types a join. An outer join makes the relations on its nullable
// side nullable (§17.2, guide §5.4).
func (a *analyzer) join(j *pg.JoinExpr, sc *scope) ([]*rel, error) {
	if j.GetIsNatural() {
		return nil, a.errAt("query-unsupported", 0, "NATURAL joins are not supported in query files; join with ON or USING")
	}
	if j.GetAlias() != nil {
		return nil, a.errAt("query-unsupported", 0, "an alias on a join is not supported in query files")
	}
	l, err := a.fromItem(j.GetLarg(), sc)
	if err != nil {
		return nil, err
	}
	r, err := a.fromItem(j.GetRarg(), sc)
	if err != nil {
		return nil, err
	}
	switch j.GetJointype() {
	case pg.JoinType_JOIN_LEFT:
		nullable(r)
	case pg.JoinType_JOIN_RIGHT:
		nullable(l)
	case pg.JoinType_JOIN_FULL:
		nullable(l)
		nullable(r)
	}
	rels := append(slices.Clone(l), r...)
	if using := j.GetUsingClause(); len(using) > 0 {
		merged, err := a.using(using, l, r, j.GetJointype())
		if err != nil {
			return nil, err
		}
		rels = append([]*rel{merged}, rels...)
	}
	on := &scope{rels: append(slices.Clone(sc.rels), rels...), parent: sc.parent, ctes: sc.ctes}
	if err := a.condition(j.GetQuals(), on); err != nil {
		return nil, err
	}
	return rels, nil
}

func nullable(rels []*rel) {
	for _, r := range rels {
		r.nullable = true
	}
}

// using merges the USING columns of a join into a relation of their own,
// which unqualified references reach. The merged column is the left side's,
// the right side's for a right join, and nullable unless one side's column
// is non-null for a full join.
func (a *analyzer) using(names []*pg.Node, l, r []*rel, kind pg.JoinType) (*rel, error) {
	merged := &rel{}
	for _, n := range names {
		name := n.GetString_().GetSval()
		lr, lc, lok := a.findColumn(name, &scope{rels: l})
		rr, rc, rok := a.findColumn(name, &scope{rels: r})
		if !lok || !rok || lr == nil || rr == nil {
			return nil, a.errAt("query-unknown-column", 0, "USING column %s is not a column of exactly one relation on each side of the join", name)
		}
		lv, rv := lc.v, rc.v
		lv.nullable = lv.nullable || lr.nullable
		rv.nullable = rv.nullable || rr.nullable
		v := lv
		switch kind {
		case pg.JoinType_JOIN_RIGHT:
			v = rv
		case pg.JoinType_JOIN_FULL:
			v.nullable = lv.nullable && rv.nullable
			v.annotated = lv.annotated || rv.annotated
			v.origin = nil
		}
		merged.cols = append(merged.cols, column{name: name, v: v})
		for _, x := range []*rel{lr, rr} {
			if x.hidden == nil {
				x.hidden = map[string]bool{}
			}
			x.hidden[name] = true
		}
	}
	return merged, nil
}

// insert types an INSERT: its values take the types of their columns.
func (a *analyzer) insert(s *pg.InsertStmt, parent *scope) ([]column, error) {
	a.writes = true
	sc, err := a.with(s.GetWithClause(), parent)
	if err != nil {
		return nil, err
	}
	target, err := a.tableRel(s.GetRelation(), sc)
	if err != nil {
		return nil, err
	}
	cols := target.cols
	if len(s.GetCols()) > 0 {
		cols = nil
		for _, n := range s.GetCols() {
			rt := n.GetResTarget()
			c, err := a.targetColumn(target, rt)
			if err != nil {
				return nil, err
			}
			cols = append(cols, c)
		}
	}
	if sel := s.GetSelectStmt().GetSelectStmt(); sel != nil {
		if err := a.insertSource(sel, cols, sc); err != nil {
			return nil, err
		}
	}
	if oc := s.GetOnConflictClause(); oc != nil {
		tsc := &scope{rels: []*rel{target}, parent: sc}
		for _, n := range oc.GetInfer().GetIndexElems() {
			if e := n.GetIndexElem(); e.GetExpr() != nil {
				if _, err := a.expr(e.GetExpr(), tsc); err != nil {
					return nil, err
				}
			}
		}
		if err := a.condition(oc.GetInfer().GetWhereClause(), tsc); err != nil {
			return nil, err
		}
		// DO UPDATE also reads the proposed row as excluded, which only a
		// qualified reference reaches.
		excluded := &rel{name: "excluded", cols: target.cols, hidden: map[string]bool{}}
		for _, c := range target.cols {
			excluded.hidden[c.name] = true
		}
		csc := &scope{rels: []*rel{target, excluded}, parent: sc}
		if err := a.set(oc.GetTargetList(), target, csc); err != nil {
			return nil, err
		}
		if err := a.condition(oc.GetWhereClause(), csc); err != nil {
			return nil, err
		}
	}
	return a.returning(s.GetReturningList(), &scope{rels: []*rel{target}, parent: sc})
}

// insertSource types an INSERT's VALUES rows or SELECT against the columns
// they fill.
func (a *analyzer) insertSource(sel *pg.SelectStmt, cols []column, sc *scope) error {
	if rows := sel.GetValuesLists(); len(rows) > 0 && sel.GetOp() == pg.SetOperation_SETOP_NONE {
		for _, row := range rows {
			items := row.GetList().GetItems()
			if len(items) != len(cols) {
				return a.errAt("query-unsupported", 0, "a VALUES row has %d values for %d columns", len(items), len(cols))
			}
			for i, it := range items {
				if it.GetSetToDefault() != nil {
					continue
				}
				v, err := a.expr(it, sc)
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
		return a.errAt("query-unsupported", 0, "the SELECT returns %d columns for %d columns", len(got), len(cols))
	}
	for i := range got {
		if _, err := a.expect(got[i].v, cols[i].v.typ, true); err != nil {
			return err
		}
	}
	return nil
}

// targetColumn finds the column an INSERT column list or SET names.
func (a *analyzer) targetColumn(target *rel, rt *pg.ResTarget) (column, error) {
	if len(rt.GetIndirection()) > 0 {
		return column{}, a.errAt("query-unsupported", rt.GetLocation(), "assigning to part of a column is not supported in query files")
	}
	for _, c := range target.cols {
		if c.name == rt.GetName() {
			return c, nil
		}
	}
	return column{}, a.errAt("query-unknown-column", rt.GetLocation(), "%s is not a column of %s", rt.GetName(), target.name)
}

// set types an UPDATE's or ON CONFLICT's SET list.
func (a *analyzer) set(list []*pg.Node, target *rel, sc *scope) error {
	for _, n := range list {
		rt := n.GetResTarget()
		if rt.GetVal().GetMultiAssignRef() != nil {
			return a.errAt("query-unsupported", rt.GetLocation(), "SET (a, b) = ... is not supported in query files; set each column")
		}
		c, err := a.targetColumn(target, rt)
		if err != nil {
			return err
		}
		if rt.GetVal().GetSetToDefault() != nil {
			continue
		}
		v, err := a.expr(rt.GetVal(), sc)
		if err != nil {
			return err
		}
		if _, err := a.expect(v, c.v.typ, true); err != nil {
			return err
		}
	}
	return nil
}

// returning types a RETURNING list; without one the statement returns no
// rows.
func (a *analyzer) returning(list []*pg.Node, sc *scope) ([]column, error) {
	if len(list) == 0 {
		return nil, nil
	}
	return a.targets(list, sc)
}

// update types an UPDATE.
func (a *analyzer) update(s *pg.UpdateStmt, parent *scope) ([]column, error) {
	a.writes = true
	sc, err := a.with(s.GetWithClause(), parent)
	if err != nil {
		return nil, err
	}
	target, err := a.tableRel(s.GetRelation(), sc)
	if err != nil {
		return nil, err
	}
	sc.rels = []*rel{target}
	if err := a.from(s.GetFromClause(), sc); err != nil {
		return nil, err
	}
	if err := a.set(s.GetTargetList(), target, sc); err != nil {
		return nil, err
	}
	if err := a.condition(s.GetWhereClause(), sc); err != nil {
		return nil, err
	}
	return a.returning(s.GetReturningList(), sc)
}

// delete types a DELETE.
func (a *analyzer) delete(s *pg.DeleteStmt, parent *scope) ([]column, error) {
	a.writes = true
	sc, err := a.with(s.GetWithClause(), parent)
	if err != nil {
		return nil, err
	}
	target, err := a.tableRel(s.GetRelation(), sc)
	if err != nil {
		return nil, err
	}
	sc.rels = []*rel{target}
	if err := a.from(s.GetUsingClause(), sc); err != nil {
		return nil, err
	}
	if err := a.condition(s.GetWhereClause(), sc); err != nil {
		return nil, err
	}
	return a.returning(s.GetReturningList(), sc)
}
