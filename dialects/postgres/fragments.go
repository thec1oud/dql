package postgres

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/thec1oud/dql/core/diagnostics"
	"github.com/thec1oud/dql/core/queries"
	"github.com/thec1oud/dql/core/shapes"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Fragments returns the fragment table of b: every SQL piece the builder
// may join, as a template with holes (§16.2, §18.4,
// plans/fragment-table-proposal.md). Templates never hold values; the
// runtime fills holes, joins the pieces and numbers the placeholders.
//
// Ids name a table as queries.TableID does, and a shape by its dotted
// spelling (§14.1). Each table has one
// alias, the initials of its name's words, numbered from 2 when tables
// share them, in the order of their qualified names.
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
	return &irv1.FragmentTable{Dialect: irv1.Dialect_DIALECT_POSTGRES, Placeholder: irv1.Placeholder_PLACEHOLDER_DOLLAR_NUMBERED, Fragments: f.out}
}

// operatorTemplates renders each operator of the catalog (§9.1). An
// operator that yields a value is parenthesized, so it nests as an operand.
// A list is one placeholder, so in compares with ANY (guide §5.5). A JSON
// path is an SQL/JSON path, such as $.a.b.
var operatorTemplates = map[string]string{
	"add":              "({0} + {1})",
	"any":              "{0} = ANY({1})",
	"array_contains":   "{1} = ANY({0})",
	"array_overlaps":   "{0} && {1}",
	"between":          "{0} BETWEEN {1} AND {2}",
	"contains":         "strpos({0}, {1}) > 0",
	"div":              "({0} / {1})",
	"eq":               "{0} = {1}",
	"ge":               "{0} >= {1}",
	"gt":               "{0} > {1}",
	"ilike":            "{0} ILIKE {1}",
	"in":               "{0} = ANY({1})",
	"is_distinct_from": "{0} IS DISTINCT FROM {1}",
	"is_not_null":      "{0} IS NOT NULL",
	"is_null":          "{0} IS NULL",
	"json_contains":    "{0}::jsonb @> {1}::jsonb",
	"json_get":         "({0} -> {1})",
	"json_path":        "jsonb_path_query_first({0}::jsonb, {1}::jsonpath)",
	"le":               "{0} <= {1}",
	"like":             "{0} LIKE {1}",
	"lt":               "{0} < {1}",
	"mod":              "({0} % {1})",
	"mul":              "({0} * {1})",
	"ne":               "{0} <> {1}",
	"neg":              "(-{0})",
	"range_contains":   "{0} @> {1}",
	"range_overlaps":   "{0} && {1}",
	"regex_match":      "{0} ~ {1}",
	"starts_with":      "starts_with({0}, {1})",
	"sub":              "({0} - {1})",
}

type fragments struct {
	out    []*irv1.Fragment
	tables []*table
	// Tables by qualified name.
	byName map[string]*table
}

// table is one table, view or materialized view with what its fragments
// need.
type table struct {
	t *irv1.Table
	// The qualified name, the id spelling and the quoted name.
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
// OR, so only OR and NOT are parenthesized.
func (f *fragments) static() {
	for id, t := range map[string]string{
		"bool.and":           "{0} AND {1}",
		"bool.not":           "NOT ({0})",
		"bool.or":            "({0} OR {1})",
		"clause.and":         " AND {0}",
		"clause.having":      " HAVING {0}",
		"clause.limit":       " LIMIT {0}",
		"clause.offset":      " OFFSET {0}",
		"clause.order":       " ORDER BY {0}",
		"clause.where":       " WHERE {0}",
		"keyset.after":       "({cols}) > ({vals})",
		"keyset.after_desc":  "({cols}) < ({vals})",
		"lock.nowait":        " NOWAIT",
		"lock.share":         " FOR SHARE",
		"lock.skip_locked":   " SKIP LOCKED",
		"lock.update":        " FOR UPDATE",
		"order.asc":          "{0} ASC",
		"order.desc":         "{0} DESC",
		"select.distinct":    "SELECT DISTINCT {cols} FROM {from}",
		"select.distinct_on": "SELECT DISTINCT ON ({keys}) {cols} FROM {from}",
		"value.default":      "DEFAULT",
		"value.now":          "now()",
		"value.one":          "1",
		"value.true":         "TRUE",
		"values.row":         "({0})",
	} {
		f.add(id, t)
	}
}

// collect gathers every table with its id spelling and alias.
func (f *fragments) collect(cats []*irv1.Catalog) {
	for _, cat := range cats {
		for _, ns := range cat.GetNamespaces() {
			for _, t := range ns.GetTables() {
				name := &irv1.QualifiedName{Catalog: cat.GetName().GetStored(), Namespace: ns.GetName().GetStored(), Object: t.GetName().GetStored()}
				tb := &table{t: t, name: name, id: queries.TableID(cats, name), quoted: quote(name.GetNamespace()) + "." + quote(name.GetObject())}
				f.tables = append(f.tables, tb)
				f.byName[diagnostics.Name(name)] = tb
			}
		}
	}
	slices.SortFunc(f.tables, func(a, b *table) int { return diagnostics.CompareNames(a.name, b.name) })
	used := map[string]bool{}
	for _, tb := range f.tables {
		base := initials(tb.t.GetName())
		alias := base
		for n := 2; used[alias] || reserved[alias]; n++ {
			alias = fmt.Sprintf("%s%d", base, n)
		}
		used[alias] = true
		tb.alias = alias
	}
}

// initials returns the first letter of each word of a table's name, or t
// when a word does not start with a letter.
func initials(name *irv1.Identifier) string {
	var b strings.Builder
	for _, w := range name.GetWords() {
		r := []rune(w)[0]
		if !unicode.IsLetter(r) {
			r = 't'
		}
		b.WriteRune(unicode.ToLower(r))
	}
	if b.Len() == 0 {
		return "t"
	}
	return b.String()
}

// table adds a table's fragments: its from item, its columns, count and
// exists, its relation predicates and, on a base table, its writes.
func (f *fragments) table(tb *table) {
	from := tb.quoted + " " + tb.alias
	f.add("from."+tb.id, from)
	f.add("count."+tb.id, "SELECT count(*) FROM "+from+"{where}")
	f.add("exists."+tb.id, "SELECT EXISTS (SELECT 1 FROM "+from+"{where})")
	self := false
	for _, c := range tb.t.GetColumns() {
		f.add("col."+tb.id+"."+c.GetName().GetStored(), tb.alias+"."+quote(c.GetName().GetStored()))
	}
	for _, r := range tb.t.GetRelations() {
		if r.GetCrossCatalog() {
			continue
		}
		self = f.relation(tb, r) || self
	}
	if self {
		for _, c := range tb.t.GetColumns() {
			f.add("col_r."+tb.id+"."+c.GetName().GetStored(), tb.alias+"_r."+quote(c.GetName().GetStored()))
		}
	}
	if tb.t.GetKind() != irv1.TableKind_RELATION_KIND_TABLE {
		return
	}
	target := "INTO " + tb.quoted + " AS " + tb.alias
	f.add("insert."+tb.id, "INSERT "+target+" ({cols}) VALUES {rows}{returning}")
	f.add("update."+tb.id, "UPDATE "+tb.quoted+" AS "+tb.alias+" SET {sets}{where}{returning}")
	f.add("delete."+tb.id, "DELETE FROM "+tb.quoted+" AS "+tb.alias+"{where}{returning}")
	for _, c := range tb.t.GetColumns() {
		col := quote(c.GetName().GetStored())
		suffix := tb.id + "." + c.GetName().GetStored()
		f.add("name."+suffix, col)
		f.add("set."+suffix, col+" = {0}")
		f.add("excluded."+suffix, "EXCLUDED."+col)
	}
}

// relation adds a relation predicate: EXISTS over the related rows,
// correlated with the owner's alias, with a hole for a further filter
// (§18.2). The related table keeps its own alias, so filters on it use its
// col fragments; when it is the owner itself, its alias gets _r and the
// filters use col_r. It reports whether the relation is such a
// self-relation.
func (f *fragments) relation(owner *table, r *irv1.Relation) bool {
	target := f.byName[diagnostics.Name(r.GetTo().GetTable())]
	alias := target.alias
	self := target == owner
	if self {
		alias += "_r"
	}
	var tmpl string
	if through := r.GetThrough(); through != nil {
		j := f.byName[diagnostics.Name(through)]
		toOwner, toTarget := junctionKeys(j.t, owner.name, target.name)
		tmpl = fmt.Sprintf("EXISTS (SELECT 1 FROM %s %s JOIN %s %s ON %s WHERE %s{and})",
			j.quoted, j.alias, target.quoted, alias,
			equal(alias, toTarget.GetReferences().GetColumns(), j.alias, toTarget.GetColumns()),
			equal(j.alias, toOwner.GetColumns(), owner.alias, toOwner.GetReferences().GetColumns()))
	} else {
		tmpl = fmt.Sprintf("EXISTS (SELECT 1 FROM %s %s WHERE %s{and})",
			target.quoted, alias, equal(alias, r.GetTo().GetColumns(), owner.alias, r.GetFrom().GetColumns()))
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

// shape adds the select and the returning clause of a read shape: a Flat
// Full or a custom shape derived from a Full (§14).
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
		cols[i] = tb.alias + "." + quote(fd.GetColumn())
	}
	list := strings.Join(cols, ", ")
	f.add("select."+spelling, "SELECT "+list+" FROM "+tb.quoted+" "+tb.alias)
	f.add("returning."+spelling, " RETURNING "+list)
}

// upsert adds the upsert and the insert-or-ignore of one conflict target
// (§14.7, §20.1). The conflict target names the key's columns and
// expressions, with a partial index's predicate, so Postgres infers the
// unique index (proposal question 3).
func (f *fragments) upsert(u *irv1.Upsert) {
	tb := f.byName[diagnostics.Name(u.GetEntity())]
	i := slices.IndexFunc(tb.t.GetUniqueness(), func(q *irv1.Uniqueness) bool { return q.GetName() == u.GetConflictTarget() })
	key := tb.t.GetUniqueness()[i]
	var elems []string
	cols, exprs := key.GetColumns(), key.GetExpressions()
	for pos, n := 0, len(cols)+len(exprs); pos < n; pos++ {
		if j := slices.IndexFunc(exprs, func(e *irv1.IndexExpression) bool { return int(e.GetPosition()) == pos }); j >= 0 {
			elems = append(elems, "("+exprs[j].GetNative()+")")
			continue
		}
		elems, cols = append(elems, quote(cols[0])), cols[1:]
	}
	conflict := " ON CONFLICT (" + strings.Join(elems, ", ") + ")"
	if p := key.GetPredicate(); p != nil {
		conflict += " WHERE " + p.GetNative()
	}
	insert := "INSERT INTO " + tb.quoted + " AS " + tb.alias + " ({cols}) VALUES {rows}" + conflict
	suffix := tb.id + "." + key.GetName()
	f.add("upsert."+suffix, insert+" DO UPDATE SET {sets}{returning}")
	f.add("ignore."+suffix, insert+" DO NOTHING{returning}")
}

// quote quotes an identifier.
func quote(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// reserved are the keywords Postgres does not take as a bare table alias:
// its reserved keywords and those that may name only a type or function.
var reserved = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`all analyse analyze and any array as asc asymmetric authorization binary both
		case cast check collate collation column concurrently constraint create cross current_catalog current_date
		current_role current_schema current_time current_timestamp current_user default deferrable desc distinct do
		else end except false fetch for foreign freeze from full grant group having ilike in initially inner intersect
		into is isnull join lateral leading left like limit localtime localtimestamp natural not notnull null offset on
		only or order outer overlaps placing primary references returning right select session_user similar some
		symmetric system_user table tablesample then to trailing true union unique user using variadic verbose when
		where window with`) {
		reserved[w] = true
	}
}
