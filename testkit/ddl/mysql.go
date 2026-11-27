package ddl

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/types/known/anypb"

	mysqlextv1 "github.com/thec1oud/dql/proto/dql/ext/mysql/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// mysql writes DDL as mysql-live reads it back from MySQL or MariaDB. The
// database, its character set and collation are the caller's to create.
// A view's definition names its tables with the database they were read
// from, as MySQL prints it; the caller rewrites that name.
type mysql struct {
	script
	keys, views, routines []string
}

// myChecks are the forms the MySQL recognizer reads.
var myChecks = checkStyle{
	dialect: "MySQL",
	quote:   myString,
	length: func(subject string, unit irv1.LengthUnit) (string, uint32) {
		switch unit {
		case irv1.LengthUnit_LENGTH_UNIT_CHARACTERS:
			return "char_length(" + subject + ")", 1
		case irv1.LengthUnit_LENGTH_UNIT_BYTES:
			return "length(" + subject + ")", 1
		}
		return "", 0
	},
	// MariaDB has no regexp_like; both read a leading (?-i) as case
	// sensitive.
	pattern:  func(subject, re string) string { return subject + " REGEXP " + myString("(?-i)"+re) },
	notEmpty: true,
}

func (w *mysql) write(c *irv1.Catalog) (string, error) {
	for _, m := range w.unpack("catalog "+c.GetName().GetStored(), c.GetExtensions()) {
		if _, ok := m.(*mysqlextv1.Database); !ok {
			w.unwritten("catalog", m)
		}
	}
	for _, ns := range c.GetNamespaces() {
		for _, t := range ordered(ns.GetTables()) {
			if isView(t) {
				w.view(t)
			} else {
				w.table(t)
			}
		}
		for _, r := range ns.GetRoutines() {
			for _, o := range r.GetOverloads() {
				w.routine(r, o)
			}
		}
	}
	w.stmts = append(w.stmts, w.keys...)
	w.stmts = append(w.stmts, w.views...)
	w.stmts = append(w.stmts, w.routines...)
	return w.join(";\n")
}

func (w *mysql) table(t *irv1.Table) {
	name := myIdent(t.GetName().GetStored())
	what := "table " + name
	var parts []string
	for _, col := range t.GetColumns() {
		parts = append(parts, w.column(name, col))
	}
	for _, u := range t.GetUniqueness() {
		var idx *mysqlextv1.Index
		for _, m := range w.unpack("uniqueness "+u.GetName(), u.GetExtensions()) {
			if e, ok := m.(*mysqlextv1.Index); ok {
				idx = e
			} else {
				w.unwritten("uniqueness "+u.GetName(), m)
			}
		}
		w.plainIndex(what, idx)
		if u.GetKind() == irv1.UniquenessKind_UNIQUENESS_PRIMARY_KEY {
			parts = append(parts, "PRIMARY KEY ("+myIdents(u.GetColumns())+")")
		} else {
			parts = append(parts, fmt.Sprintf("UNIQUE KEY %s (%s)", myIdent(u.GetName()), myIdents(u.GetColumns())))
		}
	}
	for _, c := range t.GetConstraints() {
		if c.GetKind() != irv1.ConstraintKind_CONSTRAINT_KIND_CHECK {
			w.fail("%s: no writer for constraint %s of kind %s", what, c.GetName(), c.GetKind())
			continue
		}
		cond := ""
		if o := c.GetOpaque(); o != nil {
			cond = o.GetNative()
		} else {
			cond = w.portable(myChecks, what, myIdent(c.GetColumns()[0]), c)
		}
		parts = append(parts, fmt.Sprintf("CONSTRAINT %s CHECK (%s)", myIdent(c.GetName()), cond))
		w.noExts("constraint "+c.GetName(), c.GetExtensions())
	}
	options := ""
	for _, m := range w.unpack(what, t.GetExtensions()) {
		switch e := m.(type) {
		case *mysqlextv1.Index:
			w.plainIndex(what, e)
			columns := make([]string, len(e.GetParts()))
			for i, p := range e.GetParts() {
				columns[i] = p.GetColumn()
			}
			parts = append(parts, fmt.Sprintf("KEY %s (%s)", myIdent(e.GetName()), myIdents(columns)))
		case *mysqlextv1.TableOptions:
			if e.GetEngine() != "" {
				options += " ENGINE=" + e.GetEngine()
			}
			if e.GetCollation() != "" {
				options += " COLLATE=" + e.GetCollation()
			}
			if e.GetCreateOptions() != "" {
				w.fail("%s: no writer for options %s", what, e.GetCreateOptions())
			}
		case *mysqlextv1.Comment:
			options += " COMMENT=" + myString(e.GetText())
		default:
			w.unwritten(what, m)
		}
	}
	w.add("CREATE TABLE %s (\n  %s\n)%s", name, strings.Join(parts, ",\n  "), options)
	for _, fk := range t.GetForeignKeys() {
		w.foreignKey(name, fk)
	}
}

func (w *mysql) column(table string, col *irv1.Column) string {
	cname := myIdent(col.GetName().GetStored())
	what := fmt.Sprintf("column %s.%s", table, cname)
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s", cname, w.typeText(what, col.GetType()))
	comment := ""
	for _, m := range w.unpack(what, col.GetExtensions()) {
		switch e := m.(type) {
		case *mysqlextv1.ColumnCharset:
			b.WriteString(" CHARACTER SET " + e.GetCharacterSet())
		case *mysqlextv1.Comment:
			comment = " COMMENT " + myString(e.GetText())
		default:
			w.unwritten(what, m)
		}
	}
	if n := col.GetCollation().GetName(); n != "" {
		b.WriteString(" COLLATE " + n)
	}
	if g := col.GetGeneration(); g != nil {
		kind := "STORED"
		if g.GetKind() == irv1.GenerationKind_GENERATION_VIRTUAL {
			kind = "VIRTUAL"
		}
		fmt.Fprintf(&b, " GENERATED ALWAYS AS (%s) %s", g.GetNative(), kind)
	}
	if !col.GetNullable() && col.GetNullabilitySource() == irv1.NullabilitySource_NULLABILITY_SOURCE_DATABASE {
		b.WriteString(" NOT NULL")
	} else if col.GetGeneration() == nil {
		b.WriteString(" NULL")
	}
	switch def := col.GetDefault(); def.GetClass() {
	case irv1.DefaultClass_DEFAULT_UNSPECIFIED:
	case irv1.DefaultClass_DEFAULT_IDENTITY_BY_DEFAULT:
		b.WriteString(" AUTO_INCREMENT")
	case irv1.DefaultClass_DEFAULT_CONSTANT:
		b.WriteString(" DEFAULT " + literal(myChecks, def.GetValue()))
	case irv1.DefaultClass_DEFAULT_EXPRESSION:
		if strings.HasPrefix(strings.ToUpper(def.GetNative()), "CURRENT_TIMESTAMP") {
			b.WriteString(" DEFAULT " + def.GetNative())
		} else {
			b.WriteString(" DEFAULT (" + def.GetNative() + ")")
		}
		if def.GetOnUpdate() {
			b.WriteString(" ON UPDATE " + def.GetNative())
		}
	default:
		w.fail("%s: no writer for default %s", what, def.GetClass())
	}
	b.WriteString(comment)
	return b.String()
}

// plainIndex fails unless idx, if any, is a visible B-tree index of whole
// columns with no comment: the only kind the writer writes.
func (w *mysql) plainIndex(what string, idx *mysqlextv1.Index) {
	for _, p := range idx.GetParts() {
		if p.GetExpression() != "" || p.GetPrefix() > 0 || p.GetDescending() {
			w.fail("%s: no writer for index part %v", what, p)
		}
	}
	if idx != nil && (idx.GetType() != "BTREE" || !idx.GetVisible() || idx.GetComment() != "") {
		w.fail("%s: no writer for index %v", what, idx)
	}
}

func (w *mysql) foreignKey(table string, fk *irv1.ForeignKey) {
	ref := fk.GetReferences()
	w.keys = append(w.keys, fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s (%s) ON UPDATE %s ON DELETE %s",
		table, myIdent(fk.GetName().GetStored()), myIdents(fk.GetColumns()), myIdent(ref.GetTable().GetObject()), myIdents(ref.GetColumns()),
		action(fk.GetOnUpdate()), action(fk.GetOnDelete())))
	w.noExts("foreign key "+fk.GetName().GetStored(), fk.GetExtensions())
}

// noExts fails for every extension of what.
func (w *mysql) noExts(what string, exts []*anypb.Any) {
	for _, m := range w.unpack(what, exts) {
		w.unwritten(what, m)
	}
}

func (w *mysql) view(t *irv1.Table) {
	name := myIdent(t.GetName().GetStored())
	what := "view " + name
	var def *mysqlextv1.ViewDefinition
	for _, m := range w.unpack(what, t.GetExtensions()) {
		if e, ok := m.(*mysqlextv1.ViewDefinition); ok {
			def = e
		} else {
			w.unwritten(what, m)
		}
	}
	if def == nil {
		w.fail("%s: no definition", what)
		return
	}
	if def.GetAlgorithm() != "" || def.GetCheckOption() != "NONE" {
		w.fail("%s: no writer for ALGORITHM or CHECK OPTION", what)
	}
	w.views = append(w.views, fmt.Sprintf("CREATE SQL SECURITY %s VIEW %s AS %s", def.GetSecurityType(), name, def.GetDefinition()))
	for _, col := range t.GetColumns() {
		for _, m := range w.unpack(what, col.GetExtensions()) {
			if _, ok := m.(*mysqlextv1.ColumnCharset); !ok {
				w.unwritten(what, m)
			}
		}
	}
}

// routine writes a function. MySQL ends the body of a function before
// other statements at its semicolon but keeps that semicolon in the body,
// so routines come last.
func (w *mysql) routine(r *irv1.Routine, o *irv1.Overload) {
	name := myIdent(r.GetName().GetStored())
	what := "routine " + name
	var props *mysqlextv1.RoutineProperties
	comment := ""
	for _, m := range w.unpack(what, o.GetExtensions()) {
		switch e := m.(type) {
		case *mysqlextv1.RoutineProperties:
			props = e
		case *mysqlextv1.Comment:
			comment = " COMMENT " + myString(e.GetText())
		default:
			w.unwritten(what, m)
		}
	}
	if props == nil || o.GetKind() != irv1.RoutineKind_ROUTINE_KIND_FUNCTION {
		w.fail("%s: no writer for a routine other than a function, with its definition", what)
		return
	}
	args := make([]string, len(o.GetArguments()))
	for i, a := range o.GetArguments() {
		args[i] = myIdent(a.GetName().GetStored()) + " " + w.typeText(what, a.GetType())
	}
	deterministic := map[bool]string{true: "DETERMINISTIC", false: "NOT DETERMINISTIC"}[props.GetDeterministic()]
	w.routines = append(w.routines, fmt.Sprintf("CREATE FUNCTION %s(%s) RETURNS %s %s %s SQL SECURITY %s%s %s",
		name, strings.Join(args, ", "), w.typeText(what, o.GetReturns().GetScalar().GetType()), deterministic,
		props.GetDataAccess(), props.GetSecurityType(), comment, props.GetDefinition()))
}

// myIdent quotes an identifier.
func myIdent(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}

// myIdents quotes and joins identifiers.
func myIdents(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = myIdent(s)
	}
	return strings.Join(q, ", ")
}

// myString quotes a string literal, escaping the backslashes MySQL reads
// as escapes.
func myString(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, "'", "''").Replace(s) + "'"
}
