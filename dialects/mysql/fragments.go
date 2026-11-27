package mysql

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
// plans/fragment-table-proposal.md). Templates never hold values; the
// runtime fills holes, joins the pieces and writes a ? for each value.
//
// A MySQL database is one catalog with one namespace, and a statement runs
// in its connection's database, so tables go unqualified. Each table is
// its own alias: MySQL's ON DUPLICATE KEY UPDATE names the target's
// columns through the table's name, as no alias can be given to an
// INSERT's target. A table joined to itself is aliased name_r.
//
// No write has a returning clause: the runtime re-reads a written row by
// its key (§20.1).
func Fragments(b *irv1.Bundle, p *irv1.CapabilityProfile, flavor Flavor) *irv1.FragmentTable {
	f := &fragments{byName: map[string]*table{}, flavor: flavor}
	f.static()
	for _, s := range p.GetOperators() {
		t, ok := operatorTemplates[s.GetName()]
		if m, mariadb := mariadbTemplates[s.GetName()]; mariadb && flavor == MariaDB {
			t = m
		}
		if ok {
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
	return &irv1.FragmentTable{Dialect: irv1.Dialect_DIALECT_MYSQL, Placeholder: irv1.Placeholder_PLACEHOLDER_QUESTION, Fragments: f.out}
}

// operatorTemplates renders each operator of the catalog the dialect has
// (§9.1). An operator that yields a value is parenthesized, so it nests as
// an operand. A list is one placeholder, a JSON array (§25.3): on a column,
// in and any expand it with JSON_TABLE into items of the column's type
// (listType); on any other operand, MEMBER OF compares the value with the
// items as JSON, which is approximate. starts_with escapes
// the prefix's wildcards with !, which needs no backslash and so means the
// same under every sql_mode.
var operatorTemplates = map[string]string{
	"add":              "({0} + {1})",
	"any":              "{0} MEMBER OF ({1})",
	"between":          "{0} BETWEEN {1} AND {2}",
	"contains":         "LOCATE({1}, {0}) > 0",
	"div":              "({0} / {1})",
	"eq":               "{0} = {1}",
	"ge":               "{0} >= {1}",
	"gt":               "{0} > {1}",
	"ilike":            "LOWER({0}) LIKE LOWER({1})",
	"in":               "{0} MEMBER OF ({1})",
	"is_distinct_from": "NOT ({0} <=> {1})",
	"is_not_null":      "{0} IS NOT NULL",
	"is_null":          "{0} IS NULL",
	"json_contains":    "JSON_CONTAINS({0}, {1})",
	"json_get":         "JSON_EXTRACT({0}, CONCAT('$.', JSON_QUOTE({1})))",
	"le":               "{0} <= {1}",
	"like":             "{0} LIKE {1}",
	"lt":               "{0} < {1}",
	"mod":              "({0} % {1})",
	"mul":              "({0} * {1})",
	"ne":               "{0} <> {1}",
	"neg":              "(-{0})",
	"regex_match":      "REGEXP_LIKE({0}, {1}, 'c')",
	"starts_with":      "{0} LIKE CONCAT(REPLACE(REPLACE(REPLACE({1}, '!', '!!'), '%', '!%'), '_', '!_'), '%') ESCAPE '!'",
	"sub":              "({0} - {1})",
}

// mariadbTemplates are MariaDB's spellings of operators MySQL writes
// otherwise. MariaDB has no REGEXP_LIKE: its REGEXP is PCRE's, which
// matches case sensitively under (?-i) whatever the collation. Nor has it
// MEMBER OF: JSON_CONTAINS finds the value as an item of the list.
var mariadbTemplates = map[string]string{
	"any":         "JSON_CONTAINS({1}, JSON_ARRAY({0}))",
	"in":          "JSON_CONTAINS({1}, JSON_ARRAY({0}))",
	"regex_match": "{0} REGEXP CONCAT('(?-i)', {1})",
}

type fragments struct {
	out    []*irv1.Fragment
	tables []*table
	// Tables by qualified name.
	byName map[string]*table
	flavor Flavor
}

// table is one table or view with what its fragments need: its qualified
// name, its id spelling and its quoted name, which is also its alias.
type table struct {
	t      *irv1.Table
	name   *irv1.QualifiedName
	id     string
	quoted string
}

func (f *fragments) add(id, template string) *irv1.Fragment {
	fr := &irv1.Fragment{Id: id, Template: template}
	f.out = append(f.out, fr)
	return fr
}

// static adds the fragments no table or shape owns. AND binds tighter than
// OR, so only OR and NOT are parenthesized. MySQL has no OFFSET without a
// LIMIT, so an offset alone takes value.no_limit, the largest row count.
// MariaDB writes a share lock the older way. Neither has DISTINCT ON.
// reread.from keeps the keys a multi-row insert generated from its last
// insert ID {1} on: one statement's keys are consecutive values of the
// session's auto_increment_increment from it, so a read in key order
// limited to the row count finds them, though other writers' keys with
// another offset fall in their range.
func (f *fragments) static() {
	share := " FOR SHARE"
	if f.flavor == MariaDB {
		share = " LOCK IN SHARE MODE"
	}
	for id, t := range map[string]string{
		"bool.and":          "{0} AND {1}",
		"bool.not":          "NOT ({0})",
		"bool.or":           "({0} OR {1})",
		"clause.and":        " AND {0}",
		"clause.having":     " HAVING {0}",
		"clause.limit":      " LIMIT {0}",
		"clause.offset":     " OFFSET {0}",
		"clause.order":      " ORDER BY {0}",
		"clause.where":      " WHERE {0}",
		"keyset.after":      "({cols}) > ({vals})",
		"keyset.after_desc": "({cols}) < ({vals})",
		"lock.nowait":       " NOWAIT",
		"lock.share":        share,
		"lock.skip_locked":  " SKIP LOCKED",
		"lock.update":       " FOR UPDATE",
		// NULLs sort last ascending and first descending, as on Postgres;
		// MySQL sorts them first ascending.
		"order.asc":       "{0} IS NULL, {0} ASC",
		"order.desc":      "{0} IS NULL DESC, {0} DESC",
		"reread.from":     "{0} >= {1} AND MOD({0} - {2}, @@SESSION.auto_increment_increment) = 0",
		"select.distinct": "SELECT DISTINCT {cols} FROM {from}",
		"value.default":   "DEFAULT",
		"value.no_limit":  "18446744073709551615",
		"value.now":       "CURRENT_TIMESTAMP(6)",
		"value.one":       "1",
		"value.true":      "TRUE",
		"values.row":      "({0})",
	} {
		f.add(id, t)
	}
}

// collect gathers every table with its id spelling.
func (f *fragments) collect(cats []*irv1.Catalog) {
	for _, cat := range cats {
		for _, ns := range cat.GetNamespaces() {
			for _, t := range ns.GetTables() {
				name := &irv1.QualifiedName{Catalog: cat.GetName().GetStored(), Namespace: ns.GetName().GetStored(), Object: t.GetName().GetStored()}
				tb := &table{t: t, name: name, id: queries.TableID(cats, name), quoted: quote(name.GetObject())}
				f.tables = append(f.tables, tb)
				f.byName[diagnostics.Name(name)] = tb
			}
		}
	}
	slices.SortFunc(f.tables, func(a, b *table) int { return diagnostics.CompareNames(a.name, b.name) })
}

// table adds a table's fragments: its from item, its columns, count and
// exists, its relation predicates and, on a base table, its writes.
func (f *fragments) table(tb *table) {
	f.add("from."+tb.id, tb.quoted)
	f.add("count."+tb.id, "SELECT count(*) FROM "+tb.quoted+"{where}")
	f.add("exists."+tb.id, "SELECT EXISTS (SELECT 1 FROM "+tb.quoted+"{where})")
	self := false
	for _, c := range tb.t.GetColumns() {
		f.add("col."+tb.id+"."+c.GetName().GetStored(), tb.quoted+"."+quote(c.GetName().GetStored()))
		if t := f.listType(c); t != "" {
			for _, op := range []string{"any", "in"} {
				f.add("op."+op+"."+tb.id+"."+c.GetName().GetStored(), "{0} IN (SELECT dql_list.v FROM JSON_TABLE({1}, '$[*]' COLUMNS (v "+t+" PATH '$')) AS dql_list)")
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
	f.add("insert."+tb.id, "INSERT INTO "+tb.quoted+" ({cols}) VALUES {rows}")
	f.add("update."+tb.id, "UPDATE "+tb.quoted+" SET {sets}{where}")
	f.add("delete."+tb.id, "DELETE FROM "+tb.quoted+"{where}")
	for _, c := range tb.t.GetColumns() {
		col := quote(c.GetName().GetStored())
		suffix := tb.id + "." + c.GetName().GetStored()
		f.add("name."+suffix, col)
		f.add("set."+suffix, col+" = {0}")
		f.add("excluded."+suffix, f.excluded(col))
	}
}

// listType is the type a list compared with column c holds its items as:
// the column's own, in its character set and collation, so that items
// compare as the column's values do. An opaque column has none.
func (f *fragments) listType(c *irv1.Column) string {
	if c.GetType().GetOpaque() != nil || c.GetType().GetNative().GetRaw() == "" {
		return ""
	}
	t := c.GetType().GetNative().GetRaw()
	// MariaDB's JSON_TABLE has no enum or set columns; their items compare
	// with the column as text.
	if f.flavor == MariaDB && (strings.HasPrefix(t, "enum(") || strings.HasPrefix(t, "set(")) {
		t = "text"
	}
	if co := c.GetCollation().GetName(); co != "" {
		cs, _, _ := strings.Cut(co, "_")
		t += " CHARACTER SET " + cs + " COLLATE " + co
	}
	return t
}

// excluded is the value an upsert proposed for a column: through the row
// alias dql_new on MySQL, and VALUES() on MariaDB, which has no row alias.
func (f *fragments) excluded(col string) string {
	if f.flavor == MariaDB {
		return "VALUES(" + col + ")"
	}
	return "`dql_new`." + col
}

// relation adds a relation predicate: EXISTS over the related rows,
// correlated with the owner, with a hole for a further filter (§18.2).
// When the related table is the owner itself, it is aliased name_r and the
// filters use col_r. It reports whether the relation is such a
// self-relation.
func (f *fragments) relation(owner *table, r *irv1.Relation) bool {
	target := f.byName[diagnostics.Name(r.GetTo().GetTable())]
	from, alias := target.quoted, target.quoted
	self := target == owner
	if self {
		alias = quote(target.name.GetObject() + "_r")
		from += " AS " + alias
	}
	var tmpl string
	if through := r.GetThrough(); through != nil {
		j := f.byName[diagnostics.Name(through)]
		toOwner, toTarget := junctionKeys(j.t, owner.name, target.name)
		tmpl = fmt.Sprintf("EXISTS (SELECT 1 FROM %s JOIN %s ON %s WHERE %s{and})",
			j.quoted, from,
			equal(alias, toTarget.GetReferences().GetColumns(), j.quoted, toTarget.GetColumns()),
			equal(j.quoted, toOwner.GetColumns(), owner.quoted, toOwner.GetReferences().GetColumns()))
	} else {
		tmpl = fmt.Sprintf("EXISTS (SELECT 1 FROM %s WHERE %s{and})",
			from, equal(alias, r.GetTo().GetColumns(), owner.quoted, r.GetFrom().GetColumns()))
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

// shape adds the select of a read shape: a Flat Full or a custom shape
// derived from a Full (§14). The runtime re-reads a written row through
// it.
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
	for i, fd := range s.GetFields() {
		cols[i] = tb.quoted + "." + quote(fd.GetColumn())
	}
	f.add("select."+spelling, "SELECT "+strings.Join(cols, ", ")+" FROM "+tb.quoted)
}

// upsert adds the upsert and the insert-or-ignore of one conflict target
// (§14.7, §20.1). ON DUPLICATE KEY UPDATE fires on a conflict with any
// unique key of the table, so the upsert is exact only where the conflict
// target is the table's one uniqueness fact, and approximate elsewhere.
// The insert-or-ignore is INSERT IGNORE, which also turns other errors,
// such as a missing value, into warnings, so it is approximate and needs
// the opt-in (§20.1); unlike a no-op ON DUPLICATE KEY UPDATE, it reports no
// affected row when it skips, under the found-rows counting the driver
// adapter asks for.
func (f *fragments) upsert(u *irv1.Upsert) {
	tb := f.byName[diagnostics.Name(u.GetEntity())]
	fidelity := irv1.Fidelity_FIDELITY_EXACT
	if len(tb.t.GetUniqueness()) > 1 {
		fidelity = irv1.Fidelity_FIDELITY_APPROXIMATE
	}
	insert := "INSERT INTO " + tb.quoted + " ({cols}) VALUES {rows}"
	if f.flavor == MySQL {
		insert += " AS `dql_new`"
	}
	suffix := tb.id + "." + u.GetConflictTarget()
	f.add("upsert."+suffix, insert+" ON DUPLICATE KEY UPDATE {sets}").Fidelity = fidelity
	f.add("ignore."+suffix, "INSERT IGNORE INTO "+tb.quoted+" ({cols}) VALUES {rows}").Fidelity = irv1.Fidelity_FIDELITY_APPROXIMATE
}

// quote quotes an identifier.
func quote(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}
