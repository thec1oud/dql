package sqlserver

import (
	"fmt"
	"slices"
	"strings"

	"github.com/thec1oud/dql/core/diagnostics"
	"github.com/thec1oud/dql/core/queries"
	"github.com/thec1oud/dql/core/shapes"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Fragments returns the fragment table of b: every SQL piece the builder
// may join, as a template with holes (§16.2, §18.4,
// plans/fragment-table-proposal.md, plans/sqlserver-fragments-proposal.md).
// Templates never hold values; the runtime fills holes, joins the pieces
// and writes @p1, @p2, ... for the values.
//
// A database is a catalog and its schemas the namespaces, and a statement
// runs in its connection's database, so tables are schema.name. Each table
// is aliased by its name, or by schema_name when two schemas hold tables
// of one name; a table joined to itself is aliased name_r.
//
// A write's rows come back through OUTPUT, which SQL Server refuses on a
// table with an enabled trigger unless it writes INTO a table. So every
// write outputs its rows INTO @dql_out, a table variable of the table's
// columns and dql_n, and returning.<shape> selects the shape's columns from
// it. A write that returns nothing leaves the variable unread. OUTPUT
// returns rows in no promised order, so an insert, like an upsert, is a
// MERGE, whose OUTPUT can name its source's dql_n, the row's place in the
// VALUES list, which returning orders by.
func Fragments(b *irv1.Bundle, p *irv1.CapabilityProfile) *irv1.FragmentTable {
	f := &fragments{byName: map[string]*table{}}
	f.static()
	for _, s := range p.GetOperators() {
		if t, ok := operatorTemplates[s.GetName()]; ok {
			f.add("op."+s.GetName(), t).Fidelity = s.GetFidelity()
		}
	}
	f.collect(b.GetCatalogs())
	for _, t := range f.tables {
		f.table(t)
	}
	for _, s := range b.GetShapes().GetShapes() {
		f.shape(s)
	}
	for _, u := range b.GetShapes().GetUpserts() {
		f.upsert(u)
	}
	slices.SortFunc(f.out, func(a, b *irv1.Fragment) int { return strings.Compare(a.GetId(), b.GetId()) })
	return &irv1.FragmentTable{Dialect: irv1.Dialect_DIALECT_SQLSERVER, Placeholder: irv1.Placeholder_PLACEHOLDER_AT_NUMBERED, Fragments: f.out}
}

// operatorTemplates renders each operator of the catalog SQL Server has
// (§9.1). An operator that yields a value is parenthesized, so it nests as
// an operand. A list is one placeholder, a JSON array (§25.3): on a
// column, in and any read its items with OPENJSON as the column's type
// (listType); on any other operand they are text. LIKE escapes with a
// backslash, as Postgres's does by default; starts_with escapes the
// prefix's wildcards with brackets, which LIKE reads as literals.
var operatorTemplates = map[string]string{
	"add":              "({0} + {1})",
	"any":              "{0} IN (SELECT [value] FROM OPENJSON({1}))",
	"between":          "{0} BETWEEN {1} AND {2}",
	"contains":         "CHARINDEX({1}, {0}) > 0",
	"div":              "({0} / {1})",
	"eq":               "{0} = {1}",
	"ge":               "{0} >= {1}",
	"gt":               "{0} > {1}",
	"ilike":            "LOWER({0}) LIKE LOWER({1}) ESCAPE '\\'",
	"in":               "{0} IN (SELECT [value] FROM OPENJSON({1}))",
	"is_distinct_from": "{0} IS DISTINCT FROM {1}",
	"is_not_null":      "{0} IS NOT NULL",
	"is_null":          "{0} IS NULL",
	"le":               "{0} <= {1}",
	"like":             "{0} LIKE {1} ESCAPE '\\'",
	"lt":               "{0} < {1}",
	"mod":              "({0} % {1})",
	"mul":              "({0} * {1})",
	"ne":               "{0} <> {1}",
	"neg":              "(-{0})",
	"starts_with":      "{0} LIKE REPLACE(REPLACE(REPLACE({1}, '[', '[[]'), '%', '[%]'), '_', '[_]') + '%'",
	"sub":              "({0} - {1})",
}

type fragments struct {
	out    []*irv1.Fragment
	tables []*table
	// Tables by qualified name.
	byName map[string]*table
}

// table is one table or view with what its fragments need: its qualified
// name, its id spelling, its quoted schema.name and its quoted alias.
type table struct {
	t      *irv1.Table
	name   *irv1.QualifiedName
	id     string
	quoted string
	alias  string
}

func (f *fragments) add(id, template string) *irv1.Fragment {
	fr := &irv1.Fragment{Id: id, Template: template}
	f.out = append(f.out, fr)
	return fr
}

// static adds the fragments no table or shape owns. AND binds tighter than
// OR, so only OR and NOT are parenthesized. A page is OFFSET ... FETCH,
// which needs an ORDER BY: order.none orders a page the call left
// unordered. Locks are table hints, which lock.hints writes into a select's
// {locks} hole. T-SQL has no boolean literal, so value.true is a
// comparison, nor row values, so no keyset fragments, nor DISTINCT ON.
// A MERGE's source rows take no DEFAULT, so there is no value.default: the
// runtime leaves a write's unset columns out, and a SET to DEFAULT renders
// default.<t>.<c>, which is DEFAULT.
func (f *fragments) static() {
	for id, t := range map[string]string{
		"bool.and":         "{0} AND {1}",
		"bool.not":         "NOT ({0})",
		"bool.or":          "({0} OR {1})",
		"clause.and":       " AND {0}",
		"clause.having":    " HAVING {0}",
		"clause.offset":    " OFFSET {0} ROWS",
		"clause.order":     " ORDER BY {0}",
		"clause.page":      " OFFSET {offset} ROWS FETCH NEXT {limit} ROWS ONLY",
		"clause.where":     " WHERE {0}",
		"lock.hints":       " WITH ({0})",
		"lock.nowait":      "NOWAIT",
		"lock.share":       "HOLDLOCK, ROWLOCK",
		"lock.skip_locked": "READPAST",
		"lock.update":      "UPDLOCK, ROWLOCK",
		"order.asc":        "{0} ASC",
		"order.desc":       "{0} DESC",
		"order.none":       " ORDER BY (SELECT NULL)",
		"select.distinct":  "SELECT DISTINCT {cols} FROM {from}{locks}",
		"value.now":        "TODATETIMEOFFSET(SYSUTCDATETIME(), 0)",
		"value.one":        "1",
		"value.true":       "1 = 1",
		"value.zero":       "0",
		"values.row":       "({0})",
	} {
		f.add(id, t)
	}
}

// collect gathers every table with its id spelling and alias.
func (f *fragments) collect(cats []*irv1.Catalog) {
	objects := map[string]int{}
	for _, cat := range cats {
		for _, ns := range cat.GetNamespaces() {
			for _, t := range ns.GetTables() {
				name := &irv1.QualifiedName{Catalog: cat.GetName().GetStored(), Namespace: ns.GetName().GetStored(), Object: t.GetName().GetStored()}
				tb := &table{t: t, name: name, id: queries.TableID(cats, name),
					quoted: quote(name.GetNamespace()) + "." + quote(name.GetObject())}
				f.tables = append(f.tables, tb)
				f.byName[diagnostics.Name(name)] = tb
				objects[name.GetObject()]++
			}
		}
	}
	for _, tb := range f.tables {
		tb.alias = quote(tb.name.GetObject())
		if objects[tb.name.GetObject()] > 1 {
			tb.alias = quote(tb.name.GetNamespace() + "_" + tb.name.GetObject())
		}
	}
	slices.SortFunc(f.tables, func(a, b *table) int { return diagnostics.CompareNames(a.name, b.name) })
}

// from is a table's from item.
func (tb *table) from() string { return tb.quoted + " AS " + tb.alias }

// table adds a table's fragments: its from item, its columns, count and
// exists, its relation predicates and, on a base table, its writes.
func (f *fragments) table(tb *table) {
	f.add("from."+tb.id, tb.from())
	f.add("count."+tb.id, "SELECT COUNT_BIG(*) FROM "+tb.from()+"{where}")
	f.add("exists."+tb.id, "SELECT CAST(CASE WHEN EXISTS (SELECT 1 FROM "+tb.from()+"{where}) THEN 1 ELSE 0 END AS bit)")
	self := false
	for _, c := range tb.t.GetColumns() {
		f.add("col."+tb.id+"."+c.GetName().GetStored(), tb.alias+"."+quote(c.GetName().GetStored()))
		if t := listType(c); t != "" {
			for _, op := range []string{"any", "in"} {
				f.add("op."+op+"."+tb.id+"."+c.GetName().GetStored(), "{0} IN (SELECT [v] FROM OPENJSON({1}) WITH ([v] "+t+" '$'))")
			}
		}
	}
	for _, r := range tb.t.GetRelations() {
		if r.GetCrossCatalog() {
			continue
		}
		self = f.relation(tb, r) || self
	}
	if self {
		alias := quote(tb.name.GetObject() + "_r")
		for _, c := range tb.t.GetColumns() {
			f.add("col_r."+tb.id+"."+c.GetName().GetStored(), alias+"."+quote(c.GetName().GetStored()))
		}
	}
	if tb.t.GetKind() != irv1.TableKind_RELATION_KIND_TABLE {
		return
	}
	declare := "DECLARE @dql_out TABLE (" + outColumns(tb.t) + "); "
	f.add("insert."+tb.id, declare+"MERGE INTO "+tb.quoted+" AS "+tb.alias+" USING "+numbered+" ON 1 = 0"+
		" WHEN NOT MATCHED THEN INSERT ({cols}) VALUES ({news}) OUTPUT INSERTED.*, [dql_new].[dql_n] INTO @dql_out;{returning}")
	f.add("update."+tb.id, declare+"UPDATE "+tb.alias+" SET {sets} OUTPUT INSERTED.*, NULL INTO @dql_out FROM "+tb.from()+"{where};{returning}")
	f.add("delete."+tb.id, declare+"DELETE "+tb.alias+" OUTPUT DELETED.*, NULL INTO @dql_out FROM "+tb.from()+"{where};{returning}")
	for _, c := range tb.t.GetColumns() {
		col := quote(c.GetName().GetStored())
		suffix := tb.id + "." + c.GetName().GetStored()
		f.add("name."+suffix, col)
		f.add("set."+suffix, col+" = {0}")
		f.add("excluded."+suffix, "[dql_new]."+col)
		f.add("default."+suffix, "DEFAULT")
	}
}

// outColumns declares @dql_out's columns: each of the table's, in order,
// of its type and nullable. A rowversion's value is binary(8), which a
// table variable can store.
func outColumns(t *irv1.Table) string {
	cols := make([]string, len(t.GetColumns()))
	for i, c := range t.GetColumns() {
		typ := c.GetType().GetNative().GetRaw()
		if typ == "timestamp" {
			typ = "binary(8)"
		}
		cols[i] = quote(c.GetName().GetStored()) + " " + typ + " NULL"
	}
	return strings.Join(append(cols, "[dql_n] bigint NULL"), ", ")
}

// numbered is a write's new rows, as dql_new, each numbered dql_n in the
// order of the VALUES list, which a scan of the list keeps. OUTPUT
// returns rows in no promised order, and returning.<shape> orders them by
// dql_n, so a bulk write's rows come back in its Drafts' order (§25.2). A
// derived table's VALUES, unlike an INSERT's, holds any number of rows.
const numbered = "(SELECT *, ROW_NUMBER() OVER (ORDER BY (SELECT NULL)) AS [dql_n] FROM (VALUES {rows}) AS [dql_v] ({cols})) AS [dql_new]"

// listType is the type a list compared with column c holds its items as:
// the column's own, in its collation, so that items compare as the
// column's values do. A column of a type OPENJSON cannot read has none.
func listType(c *irv1.Column) string {
	switch c.GetType().GetKind().(type) {
	case *irv1.Type_Opaque, *irv1.Type_Xml, *irv1.Type_Bytes:
		return ""
	}
	t := c.GetType().GetNative().GetRaw()
	if co := c.GetCollation().GetName(); co != "" {
		t += " COLLATE " + co
	}
	return t
}

// relation adds a relation predicate: EXISTS over the related rows,
// correlated with the owner, with a hole for a further filter (§18.2).
// When the related table is the owner itself, it is aliased name_r and the
// filters use col_r. It reports whether the relation is such a
// self-relation.
func (f *fragments) relation(owner *table, r *irv1.Relation) bool {
	target := f.byName[diagnostics.Name(r.GetTo().GetTable())]
	from, alias := target.from(), target.alias
	self := target == owner
	if self {
		alias = quote(target.name.GetObject() + "_r")
		from = target.quoted + " AS " + alias
	}
	var tmpl string
	if through := r.GetThrough(); through != nil {
		j := f.byName[diagnostics.Name(through)]
		toOwner, toTarget := junctionKeys(j.t, owner.name, target.name)
		tmpl = fmt.Sprintf("EXISTS (SELECT 1 FROM %s JOIN %s ON %s WHERE %s{and})",
			j.from(), from,
			equal(alias, toTarget.GetReferences().GetColumns(), j.alias, toTarget.GetColumns()),
			equal(j.alias, toOwner.GetColumns(), owner.alias, toOwner.GetReferences().GetColumns()))
	} else {
		tmpl = fmt.Sprintf("EXISTS (SELECT 1 FROM %s WHERE %s{and})",
			from, equal(alias, r.GetTo().GetColumns(), owner.alias, r.GetFrom().GetColumns()))
	}
	f.add("rel."+owner.id+"."+r.GetName().GetStored(), tmpl)
	return self
}

// junctionKeys returns the junction's foreign keys to the owner and to the
// target. On a junction between a table and itself, the first by name
// leads to the owner.
func junctionKeys(j *irv1.Table, owner, target *irv1.QualifiedName) (toOwner, toTarget *irv1.ForeignKey) {
	for _, fk := range j.GetForeignKeys() {
		switch ref := fk.GetReferences().GetTable(); {
		case toOwner == nil && diagnostics.CompareNames(ref, owner) == 0:
			toOwner = fk
		case toTarget == nil && diagnostics.CompareNames(ref, target) == 0:
			toTarget = fk
		}
	}
	return toOwner, toTarget
}

// equal joins a = b for each pair of columns.
func equal(a string, as []string, b string, bs []string) string {
	parts := make([]string, len(as))
	for i := range as {
		parts[i] = a + "." + quote(as[i]) + " = " + b + "." + quote(bs[i])
	}
	return strings.Join(parts, " AND ")
}

// shape adds the select of a read shape, a Flat Full or a custom shape
// derived from a Full (§14), with a {locks} hole after its from item; and
// its returning clause, which selects the shape's columns from the rows a
// write output.
func (f *fragments) shape(s *irv1.Shape) {
	id := s.GetId()
	if id.GetForRelation() != "" || len(s.GetLineage()) == 0 || s.GetLineage()[0].GetPreset() != irv1.Preset_PRESET_FULL {
		return
	}
	if id.GetCustom() == "" && id.GetForm() != irv1.Form_FORM_FLAT {
		return
	}
	tb := f.byName[diagnostics.Name(s.GetEntity())]
	words := tb.t.GetHostName().GetWords()
	if len(words) == 0 {
		words = tb.t.GetName().GetWords()
	}
	spelling := shapes.Spelling(id, words)
	cols := make([]string, len(s.GetFields()))
	out := make([]string, len(s.GetFields()))
	for i, fd := range s.GetFields() {
		cols[i] = tb.alias + "." + quote(fd.GetColumn())
		out[i] = quote(fd.GetColumn())
	}
	f.add("select."+spelling, "SELECT "+strings.Join(cols, ", ")+" FROM "+tb.from()+"{locks}")
	f.add("returning."+spelling, " SELECT "+strings.Join(out, ", ")+" FROM @dql_out ORDER BY [dql_n];")
}

// upsert adds the upsert and the insert-or-ignore of one conflict target
// (§14.7, §20.1): a MERGE of the rows, as dql_new, on the key's columns,
// under HOLDLOCK, which keeps two upserts of one key from both inserting.
// A filtered index's predicate narrows the target, through a common table
// expression, to the rows it covers. A key with a computed column has no
// upsert: the new rows hold no value for it to match on.
// The insert branch takes the new row's values through the {news} hole,
// dql_new's column for each written column.
func (f *fragments) upsert(u *irv1.Upsert) {
	tb := f.byName[diagnostics.Name(u.GetEntity())]
	i := slices.IndexFunc(tb.t.GetUniqueness(), func(q *irv1.Uniqueness) bool { return q.GetName() == u.GetConflictTarget() })
	key := tb.t.GetUniqueness()[i]
	if slices.ContainsFunc(key.GetColumns(), func(name string) bool {
		c := tb.t.GetColumns()[slices.IndexFunc(tb.t.GetColumns(), func(c *irv1.Column) bool { return c.GetName().GetStored() == name })]
		return c.GetGeneration() != nil || c.GetDefault().GetClass() == irv1.DefaultClass_DEFAULT_IDENTITY_ALWAYS
	}) {
		return
	}
	target, cte := tb.quoted, ""
	if p := key.GetPredicate(); p != nil {
		target, cte = "[dql_target]", "WITH [dql_target] AS (SELECT * FROM "+tb.quoted+" WHERE "+p.GetNative()+") "
	}
	merge := "DECLARE @dql_out TABLE (" + outColumns(tb.t) + "); " + cte + "MERGE INTO " + target + " WITH (HOLDLOCK) AS " + tb.alias +
		" USING " + numbered + " ON " + equal(tb.alias, key.GetColumns(), "[dql_new]", key.GetColumns())
	insert := " WHEN NOT MATCHED THEN INSERT ({cols}) VALUES ({news}) OUTPUT INSERTED.*, [dql_new].[dql_n] INTO @dql_out;{returning}"
	suffix := tb.id + "." + key.GetName()
	f.add("upsert."+suffix, merge+" WHEN MATCHED THEN UPDATE SET {sets}"+insert)
	f.add("ignore."+suffix, merge+insert)
}

// quote quotes an identifier.
func quote(name string) string {
	return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
}
