package mapping

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	pg "github.com/pganalyze/pg_query_go/v6"

	pgextv1 "github.com/thec1oud/dql/proto/dql/ext/postgres/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/dpgsnapshot/internal/format"
	"github.com/thec1oud/dql/sources/internal/pgsql"
)

// tableFields are the TABLE record fields the IR or a Postgres extension
// carries; every other field stays in the table's dpg record.
var tableFields = []string{
	"schema", "name", "owner", "tablespace", "storage_params", "rls_enabled", "rls_forced", "unlogged",
	"inherits", "partition_by", "columns", "constraints", "indexes", "policies", "triggers",
}

// table maps a table (§6.1). Its partitions are recorded inside it, so they
// travel in its dpg record (fidelity.Partitions).
func (m *mapper) table(t *format.Table) *irv1.Table {
	name := t.Schema + "." + t.Name
	text, field := dbComment(t.Comment, t.Deprecated)
	tbl := &irv1.Table{Name: ident(t.Name), Kind: irv1.TableKind_RELATION_KIND_TABLE}
	tbl.Annotations = m.annotations(m.qn(t.Schema, t.Name, ""), "COMMENT ON TABLE "+name, text)
	e := &exts{m: m}
	e.comment(text)
	e.owner(t.Owner)

	cons := m.parseConstraints(name, t.Constraints)
	inKey := map[string]bool{}
	for _, c := range cons {
		if c.Type == "PRIMARY KEY" {
			for _, k := range names(c.parsed.GetKeys()) {
				inKey[k] = true
			}
		}
	}
	checked := map[string]*irv1.Type{}
	for i, c := range t.Columns {
		tbl.Columns = append(tbl.Columns, m.column(t, c, i, inKey[c.Name]))
		checked[c.Name] = m.checkType("column "+name+"."+c.Name, t.Schema, c.Type)
	}
	m.constraints(tbl, t, cons, checked, e)
	m.indexes(tbl, t.Schema, t.Name, t.Indexes, e)
	m.relationExtensions(t, e)
	e.record("table", "", t, append(slices.Clone(tableFields), field)...)
	tbl.Extensions = e.list()
	return tbl
}

// relationExtensions carries what the IR does not model about t (§6.2).
func (m *mapper) relationExtensions(t *format.Table, e *exts) {
	name := t.Schema + "." + t.Name
	e.tablespace(deref(t.Tablespace))
	if t.StorageParams != "" {
		e.add(&pgextv1.StorageParameters{Parameters: strings.Split(t.StorageParams, ", ")})
	}
	if t.RLSEnabled || t.RLSForced {
		e.add(&pgextv1.RowSecurity{Enabled: t.RLSEnabled, Forced: t.RLSForced})
	}
	if t.Unlogged {
		e.add(&pgextv1.Unlogged{})
	}
	if t.PartitionBy != "" {
		e.add(&pgextv1.PartitionKey{Definition: t.PartitionBy})
	}
	for _, p := range t.Inherits {
		s, n := m.relationName(t.Schema, p)
		e.add(&pgextv1.PartitionOf{Parent: m.qn(s, n, "")})
	}
	for _, p := range t.Policies {
		m.ignored(m.qn(t.Schema, t.Name, p.Name), "COMMENT ON POLICY "+p.Name+" ON "+name, p.Comment,
			"dpg-snapshot reads no annotations on policies")
		e.add(&pgextv1.Policy{Name: p.Name, Command: cmp.Or(p.Command, "ALL"), Permissive: p.Permissive,
			Roles: policyRoles(p.Roles), Using: p.Using, WithCheck: p.WithCheck, Comment: p.Comment})
		e.record("policy", p.Name, p, "name", "command", "permissive", "roles", "using", "with_check", "comment")
	}
	for _, tr := range t.Triggers {
		m.ignored(m.qn(t.Schema, t.Name, tr.Name), "COMMENT ON TRIGGER "+tr.Name+" ON "+name, tr.Comment,
			"dpg-snapshot reads no annotations on triggers")
		fs, fn := m.relationName(t.Schema, strings.TrimSpace(tr.Function[:strings.IndexByte(tr.Function+"(", '(')]))
		e.add(&pgextv1.Trigger{Name: tr.Name, Timing: tr.When, Events: triggerEvents(tr.Events), Level: tr.ForEach,
			Function: m.qn(fs, fn, ""), Enabled: triggerEnabled[tr.EnableState], Comment: tr.Comment})
		e.record("trigger", tr.Name, tr, "name", "when", "events", "for_each", "enable_state", "comment")
	}
}

// relationName reads a name as dpg writes it, schema.name or name; a name
// without a schema is in the schema of the object using it
// (fidelity.SearchPath).
func (m *mapper) relationName(schema, text string) (string, string) {
	if s, name, ok := strings.Cut(text, "."); ok {
		return s, name
	}
	return schema, text
}

// policyRoles lists a policy's roles as pg-live reads them, sorted, with
// PUBLIC in capitals and for a policy that names none.
func policyRoles(roles []string) []string {
	out := []string{}
	for _, r := range roles {
		if strings.EqualFold(r, "public") {
			r = "PUBLIC"
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		out = append(out, "PUBLIC")
	}
	slices.Sort(out)
	return out
}

// triggerEvents orders a trigger's events as the extension documents them.
func triggerEvents(events string) []string {
	var out []string
	for _, e := range []string{"INSERT", "UPDATE", "DELETE", "TRUNCATE"} {
		if slices.Contains(strings.Split(events, ", "), e) {
			out = append(out, e)
		}
	}
	return out
}

// triggerEnabled maps dpg's enable states to tgenabled's names.
var triggerEnabled = map[string]string{
	"": "ORIGIN", "DISABLED": "DISABLED", "ENABLE REPLICA": "REPLICA", "ENABLE ALWAYS": "ALWAYS",
}

// columnFields are the column record fields the IR column carries.
var columnFields = []string{
	"name", "type", "not_null", "default", "identity", "serial", "generated", "generated_virtual", "collation",
	"identity_increment_by", "identity_min_value", "identity_max_value", "identity_no_min_value",
	"identity_no_max_value", "identity_start_value", "identity_cache", "identity_cycle",
}

// column maps the i-th column of t. A column is non-null when the snapshot
// declares it NOT NULL, as the database does for primary key, identity and
// serial columns; a domain's NOT NULL does not count, as in pg-live. An
// identity or serial column gets the sequence Postgres creates for it, named
// as Postgres names it (fidelity.DerivedNames), owned by the table's owner.
func (m *mapper) column(t *format.Table, c format.Column, i int, inKey bool) *irv1.Column {
	name := t.Schema + "." + t.Name + "." + c.Name
	text, field := dbComment(c.Comment, c.Deprecated)
	col := &irv1.Column{
		Name:              ident(c.Name),
		Ordinal:           uint32(i + 1),
		Type:              m.typeOf("column "+name, t.Schema, c.Type),
		Nullable:          true,
		NullabilitySource: irv1.NullabilitySource_NULLABILITY_SOURCE_UNPROVEN,
		Collation:         m.collation(t.Schema, c.Type, c.Collation),
	}
	if c.NotNull || inKey || c.Identity != nil || c.Serial != nil {
		col.Nullable, col.NullabilitySource = false, irv1.NullabilitySource_NULLABILITY_SOURCE_DATABASE
	}
	switch {
	case c.Identity != nil, c.Serial != nil:
		seq := m.namer(t.Schema).relation(t.Name, c.Name, "seq")
		col.Default = &irv1.Default{Sequence: m.qn(t.Schema, seq, "")}
		switch {
		case c.Serial != nil:
			col.Default = m.defaultOf("column "+name, t.Schema, nextval(t.Schema, seq))
		case *c.Identity == "ALWAYS":
			col.Default.Class = irv1.DefaultClass_DEFAULT_IDENTITY_ALWAYS
		default:
			col.Default.Class = irv1.DefaultClass_DEFAULT_IDENTITY_BY_DEFAULT
		}
		se := &exts{m: m}
		s := m.newSequence(t.Schema, seq, c.Type, seqOptions{
			increment: c.IdentityIncrementBy, min: c.IdentityMinValue, max: c.IdentityMaxValue,
			start: c.IdentityStartValue, cache: c.IdentityCache, noMin: c.IdentityNoMinValue, noMax: c.IdentityNoMaxValue,
			cycle: c.IdentityCycle != nil && *c.IdentityCycle,
		}, se)
		s.OwnedBy = m.qn(t.Schema, t.Name, c.Name)
		se.owner(t.Owner)
		s.Extensions = se.list()
		ns := m.b.get(t.Schema).ns
		ns.Sequences = append(ns.Sequences, s)
	case c.Default != nil:
		col.Default = m.defaultOf("column "+name, t.Schema, *c.Default)
	}
	if c.Generated != nil {
		col.Generation = &irv1.Generation{Kind: irv1.GenerationKind_GENERATION_STORED, Native: *c.Generated}
		if c.GeneratedVirtual {
			col.Generation.Kind = irv1.GenerationKind_GENERATION_VIRTUAL
		}
	}
	col.Annotations = m.annotations(m.qn(t.Schema, t.Name, c.Name), "COMMENT ON COLUMN "+name, text)
	e := &exts{m: m}
	e.comment(text)
	e.record("column", "", c, append(slices.Clone(columnFields), field)...)
	col.Extensions = e.list()
	return col
}

// nextval is the default of a serial column as the database prints it. The
// sequence is qualified unless it is in public, which the search path holds
// (fidelity.SearchPath).
func nextval(schema, seq string) string {
	name := quoteIdent(seq)
	if schema != "public" {
		name = quoteIdent(schema) + "." + name
	}
	return "nextval('" + strings.ReplaceAll(name, "'", "''") + "'::regclass)"
}

// quoteIdent quotes a name the way Postgres prints it in a regclass, unless
// it is a plain lower-case identifier.
func quoteIdent(s string) string {
	plain := s != ""
	for i, r := range s {
		plain = plain && (r == '_' || r >= 'a' && r <= 'z' || i > 0 && (r >= '0' && r <= '9' || r == '$'))
	}
	if plain {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// constraint is a table constraint with its definition parsed, for the kinds
// whose columns and options exist only inside it.
type constraint struct {
	format.Constraint
	parsed *pg.Constraint
}

// parseConstraints parses the definitions of t's primary key, unique,
// foreign key and exclusion constraints.
func (m *mapper) parseConstraints(table string, list []format.Constraint) []constraint {
	var out []constraint
	for _, c := range list {
		cc := constraint{Constraint: c}
		switch c.Type {
		case "PRIMARY KEY", "UNIQUE", "FOREIGN KEY", "EXCLUDE":
			var err error
			if cc.parsed, err = parseConstraint(c.Expr); err != nil {
				m.fail(fmt.Errorf("mapping: constraint %q of %s: %w", c.Expr, table, err))
				continue
			}
		}
		out = append(out, cc)
	}
	return out
}

// parseConstraint parses a constraint definition as dpg writes it.
func parseConstraint(text string) (*pg.Constraint, error) {
	tree, err := pg.Parse("ALTER TABLE t ADD " + text)
	if err != nil {
		return nil, err
	}
	// The statement starts as an ALTER TABLE with one command, so only text
	// that adds statements, commands or a column is not one constraint.
	cmds := tree.Stmts[0].Stmt.GetAlterTableStmt().GetCmds()
	c := cmds[0].GetAlterTableCmd().GetDef().GetConstraint()
	if len(tree.Stmts) != 1 || len(cmds) != 1 || c == nil {
		return nil, fmt.Errorf("not one constraint")
	}
	return c, nil
}

// names returns the strings of a list of String nodes.
func names(list []*pg.Node) []string {
	var out []string
	for _, n := range list {
		out = append(out, n.GetString_().GetSval())
	}
	return out
}

// constraints maps t's check and exclusion constraints, its primary key and
// unique constraints, and its foreign keys, as pg-live maps them; e takes
// what belongs to the table. checked holds each column's type with domains
// resolved, which checks are recognized against. Constraints written without
// a name are named as Postgres names them: checks first, then index-backed
// constraints, then foreign keys, the order in which CREATE TABLE creates
// them (fidelity.DerivedNames).
func (m *mapper) constraints(tbl *irv1.Table, t *format.Table, cons []constraint, checked map[string]*irv1.Type, e *exts) {
	name := t.Schema + "." + t.Name
	names := m.namer(t.Schema)
	loc := func(c string) string { return "COMMENT ON CONSTRAINT " + c + " ON " + name }
	for _, c := range cons {
		if c.Type != "CHECK" {
			continue
		}
		con, err := pgsql.RecognizeCheck(c.Expr, checked)
		if err != nil {
			m.fail(fmt.Errorf("mapping: check constraint %q of %s: %w", c.Expr, name, err))
			continue
		}
		con.Name = c.Name
		if con.Name == "" {
			col := ""
			if len(con.Columns) == 1 {
				col = con.Columns[0]
			}
			con.Name = names.check(t.Name, col, "check")
		}
		con.Annotations = m.annotations(m.qn(t.Schema, t.Name, con.Name), loc(con.Name), c.Comment)
		ce := &exts{m: m}
		ce.comment(c.Comment)
		ce.options(&pgextv1.ConstraintOptions{NotValid: c.NotValid, NoInherit: c.NoInherit})
		ce.record("constraint", "", c.Constraint, checkFields(con)...)
		con.Extensions = ce.list()
		tbl.Constraints = append(tbl.Constraints, con)
	}
	for _, c := range cons {
		switch c.Type {
		case "PRIMARY KEY", "UNIQUE":
			tbl.Uniqueness = append(tbl.Uniqueness, m.uniqueConstraint(t, c, loc))
		case "EXCLUDE":
			tbl.Constraints = append(tbl.Constraints, m.exclusion(t, c, loc))
		}
	}
	for _, c := range cons {
		switch c.Type {
		case "FOREIGN KEY":
			tbl.ForeignKeys = append(tbl.ForeignKeys, m.foreignKey(t, c, loc))
		case "NOT NULL":
			// Nullability is the column's; the record keeps the constraint.
			e.record("constraint", c.Name, c.Constraint)
		}
	}
	slices.SortStableFunc(tbl.Constraints, func(a, b *irv1.Constraint) int { return strings.Compare(a.Name, b.Name) })
	slices.SortStableFunc(tbl.Uniqueness, func(a, b *irv1.Uniqueness) int { return strings.Compare(a.Name, b.Name) })
	slices.SortStableFunc(tbl.ForeignKeys, func(a, b *irv1.ForeignKey) int { return strings.Compare(a.Name.Stored, b.Name.Stored) })
}

// uniqueConstraint maps a primary key or unique constraint to its uniqueness
// fact (§11.2), carrying the index Postgres builds for it: a btree of the
// constraint's name.
func (m *mapper) uniqueConstraint(t *format.Table, c constraint, loc func(string) string) *irv1.Uniqueness {
	p := c.parsed
	u := &irv1.Uniqueness{Name: c.Name, Kind: irv1.UniquenessKind_UNIQUENESS_CONSTRAINT, Columns: names(p.Keys),
		NullsDistinct: !p.NullsNotDistinct}
	label, addition := "key", strings.Join(u.Columns, "_")
	if c.Type == "PRIMARY KEY" {
		u.Kind, label, addition = irv1.UniquenessKind_UNIQUENESS_PRIMARY_KEY, "pkey", ""
	}
	if u.Name == "" {
		u.Name = m.namer(t.Schema).indexConstraint(t.Name, addition, label)
	}
	m.ignored(m.qn(t.Schema, t.Name, u.Name), loc(u.Name), c.Comment,
		"dpg-snapshot reads no annotations on primary keys and unique constraints")
	ce := &exts{m: m}
	ce.comment(c.Comment)
	ce.add(&pgextv1.Index{Name: u.Name, Method: "btree", StorageParameters: storageParameters(p.Options), Tablespace: p.Indexspace})
	ce.options(&pgextv1.ConstraintOptions{Deferrable: c.Deferrable, InitiallyDeferred: c.InitiallyDeferred})
	used := []string{"name", "type", "comment", "deferrable", "initially_deferred"}
	if len(p.Including) == 0 {
		// Only INCLUDE columns are not in the fact or the index.
		used = append(used, "expr")
	}
	ce.record("constraint", "", c.Constraint, used...)
	u.Extensions = ce.list()
	return u
}

// exclusion maps an exclusion constraint, opaque, with the index behind it.
func (m *mapper) exclusion(t *format.Table, c constraint, loc func(string) string) *irv1.Constraint {
	p := c.parsed
	con := &irv1.Constraint{Name: c.Name, Kind: irv1.ConstraintKind_CONSTRAINT_KIND_EXCLUSION,
		Form: &irv1.Constraint_Opaque{Opaque: &irv1.NativeText{Native: c.Expr}}}
	var parts []string
	for _, x := range p.Exclusions {
		el := x.GetList().GetItems()[0].GetIndexElem()
		parts = append(parts, elemName(el))
		if el.Name != "" {
			con.Columns = append(con.Columns, el.Name)
		}
	}
	if con.Name == "" {
		con.Name = m.namer(t.Schema).indexConstraint(t.Name, strings.Join(parts, "_"), "excl")
	}
	con.Annotations = m.annotations(m.qn(t.Schema, t.Name, con.Name), loc(con.Name), c.Comment)
	ce := &exts{m: m}
	ce.comment(c.Comment)
	ce.add(&pgextv1.Index{Name: con.Name, Method: cmp.Or(p.AccessMethod, "btree"),
		StorageParameters: storageParameters(p.Options), Tablespace: p.Indexspace})
	ce.options(&pgextv1.ConstraintOptions{Deferrable: c.Deferrable, InitiallyDeferred: c.InitiallyDeferred})
	ce.record("constraint", "", c.Constraint, "name", "type", "expr", "comment", "deferrable", "initially_deferred")
	con.Extensions = ce.list()
	return con
}

// elemName is the name Postgres gives an index element when it names an
// index: its column, or for an expression the function it calls, else expr.
func elemName(el *pg.IndexElem) string {
	if el.Name != "" {
		return el.Name
	}
	if f := names(el.GetExpr().GetFuncCall().GetFuncname()); len(f) > 0 {
		return f[len(f)-1]
	}
	return "expr"
}

// storageParameters spells WITH options as reloptions holds them, name=value.
func storageParameters(opts []*pg.Node) []string {
	var out []string
	for _, o := range opts {
		d := o.GetDefElem()
		v := ""
		switch a := d.GetArg(); {
		case a.GetInteger() != nil:
			v = strconv.Itoa(int(a.GetInteger().Ival))
		case a.GetFloat() != nil:
			v = a.GetFloat().Fval
		case a.GetString_() != nil:
			v = a.GetString_().Sval
		case a.GetTypeName() != nil:
			v = strings.Join(names(a.GetTypeName().Names), ".")
		default:
			v = "true"
		}
		out = append(out, d.Defname+"="+v)
	}
	return out
}

var matches = map[string]irv1.ForeignKeyMatch{
	"s": irv1.ForeignKeyMatch_FOREIGN_KEY_MATCH_SIMPLE,
	"f": irv1.ForeignKeyMatch_FOREIGN_KEY_MATCH_FULL,
	"p": irv1.ForeignKeyMatch_FOREIGN_KEY_MATCH_PARTIAL,
}

var actions = map[string]irv1.ForeignKeyAction{
	"a": irv1.ForeignKeyAction_FOREIGN_KEY_ACTION_NO_ACTION,
	"r": irv1.ForeignKeyAction_FOREIGN_KEY_ACTION_RESTRICT,
	"c": irv1.ForeignKeyAction_FOREIGN_KEY_ACTION_CASCADE,
	"n": irv1.ForeignKeyAction_FOREIGN_KEY_ACTION_SET_NULL,
	"d": irv1.ForeignKeyAction_FOREIGN_KEY_ACTION_SET_DEFAULT,
}

// foreignKey maps a foreign key (§4.1). A target written without a schema is
// in the table's schema (fidelity.SearchPath), and a target without columns
// references its table's primary key, as in Postgres.
func (m *mapper) foreignKey(t *format.Table, c constraint, loc func(string) string) *irv1.ForeignKey {
	p := c.parsed
	cols := names(p.FkAttrs)
	target := objectKey{cmp.Or(p.Pktable.Schemaname, t.Schema), p.Pktable.Relname}
	refCols := names(p.PkAttrs)
	if len(refCols) == 0 {
		refCols = m.primaryKey(target)
	}
	name := c.Name
	if name == "" {
		name = m.namer(t.Schema).check(t.Name, strings.Join(cols, "_"), "fkey")
	}
	fk := &irv1.ForeignKey{
		Name:       ident(name),
		Columns:    cols,
		References: &irv1.ColumnSet{Table: m.qn(target.schema, target.name, ""), Columns: refCols},
		Match:      matches[p.FkMatchtype],
		OnUpdate:   actions[p.FkUpdAction],
		OnDelete:   actions[p.FkDelAction],
		Deferrable: c.Deferrable,
	}
	fk.Annotations = m.annotations(m.qn(t.Schema, t.Name, name), loc(name), c.Comment)
	ce := &exts{m: m}
	ce.comment(c.Comment)
	ce.options(&pgextv1.ConstraintOptions{InitiallyDeferred: c.InitiallyDeferred, NotValid: c.NotValid,
		OnDeleteColumns: names(p.FkDelSetCols)})
	ce.record("constraint", "", c.Constraint, "name", "type", "expr", "comment", "deferrable", "initially_deferred", "not_valid")
	fk.Extensions = ce.list()
	return fk
}

// primaryKey returns the primary key columns of a table of the snapshot.
func (m *mapper) primaryKey(q objectKey) []string {
	if t := m.tables[q]; t != nil {
		for _, c := range t.Constraints {
			if c.Type == "PRIMARY KEY" {
				if p, err := parseConstraint(c.Expr); err == nil {
					return names(p.Keys)
				}
			}
		}
	}
	m.fail(fmt.Errorf("mapping: a foreign key references %s, which has no primary key in the snapshot", q))
	return nil
}

// indexes maps the indexes of a table or materialized view: a unique one is
// a uniqueness fact (§11.2), any other an extension (§6.2). Keys and
// predicates are dpg's text (fidelity.ExpressionText), and only the server
// prints an index's definition, so it is empty.
func (m *mapper) indexes(tbl *irv1.Table, schema, table string, list []format.Index, e *exts) {
	for _, x := range list {
		m.ignored(m.qn(schema, table, x.Name), "COMMENT ON INDEX "+schema+"."+x.Name, x.Comment,
			"dpg-snapshot reads no annotations on indexes")
		var params []string
		if x.With != "" {
			params = strings.Split(x.With, ", ")
		}
		index := &pgextv1.Index{Name: x.Name, Method: x.Method, StorageParameters: params, Tablespace: x.Tablespace, Comment: x.Comment}
		used := []string{"name", "unique", "method", "with", "tablespace", "comment"}
		if !x.Unique {
			e.add(index)
			e.record("index", x.Name, x, used...)
			continue
		}
		u := &irv1.Uniqueness{Name: x.Name, Kind: irv1.UniquenessKind_UNIQUENESS_INDEX, NullsDistinct: !x.NullsNotDistinct}
		elems, texts := m.indexElems(schema+"."+x.Name, x.Columns)
		for i, el := range elems {
			if el.Name != "" {
				u.Columns = append(u.Columns, el.Name)
			} else {
				u.Expressions = append(u.Expressions, &irv1.IndexExpression{Position: uint32(i), Native: unwrap(texts[i])})
			}
		}
		if x.Where != "" {
			u.Predicate = &irv1.NativeText{Native: x.Where}
		}
		ue := &exts{m: m}
		ue.add(index)
		ue.record("index", "", x, append(used, "where", "nulls_not_distinct")...)
		u.Extensions = ue.list()
		tbl.Uniqueness = append(tbl.Uniqueness, u)
	}
	slices.SortStableFunc(tbl.Uniqueness, func(a, b *irv1.Uniqueness) int { return strings.Compare(a.Name, b.Name) })
}

// indexElems parses an index's key list as dpg writes it, and returns each
// element with its text.
func (m *mapper) indexElems(index, columns string) ([]*pg.IndexElem, []string) {
	tree, err := pg.Parse("CREATE INDEX i ON t (" + columns + ")")
	texts := splitList(columns)
	if err != nil || len(tree.Stmts) != 1 || len(tree.Stmts[0].Stmt.GetIndexStmt().GetIndexParams()) != len(texts) {
		m.fail(fmt.Errorf("mapping: index %s: %q is not a list of index keys", index, columns))
		return nil, nil
	}
	var out []*pg.IndexElem
	for _, p := range tree.Stmts[0].Stmt.GetIndexStmt().GetIndexParams() {
		out = append(out, p.GetIndexElem())
	}
	return out, texts
}

// unwrap removes the parentheses dpg writes around an index expression,
// which pg-live reads without them.
func unwrap(s string) string {
	if inner, ok := strings.CutPrefix(s, "("); ok && strings.HasSuffix(inner, ")") && balanced(inner[:len(inner)-1]) {
		return inner[:len(inner)-1]
	}
	return s
}

// balanced reports whether s closes every parenthesis it opens, and only
// those.
func balanced(s string) bool {
	depth := 0
	for _, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

// splitList splits a comma-separated list at its top-level commas:
// "numeric(12, 2), integer" is "numeric(12, 2)" and "integer". Commas in
// quotes do not split.
func splitList(s string) []string {
	var out []string
	depth, start := 0, 0
	var quote rune
	for i, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		case r == '(':
			depth++
		case r == ')':
			depth--
		case r == ',' && depth == 0:
			out = append(out, strings.TrimSpace(s[start:i]))
			start = i + 1
		}
	}
	if rest := strings.TrimSpace(s[start:]); rest != "" || len(out) > 0 {
		out = append(out, rest)
	}
	return out
}
