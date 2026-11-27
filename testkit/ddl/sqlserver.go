package ddl

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/types/known/anypb"

	sqlserverextv1 "github.com/thec1oud/dql/proto/dql/ext/sqlserver/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// sqlserver writes DDL as sqlserver-live reads it back, in batches that
// lines holding only GO separate. SQL Server keeps the text of a view,
// trigger or routine exactly as its batch held it, leading comments and
// blank lines included, so each is written as a batch of exactly that text.
// The database and its collation are the caller's to create.
type sqlserver struct {
	script
	keys, modules, comments []string
}

// msChecks are the forms the SQL Server recognizer reads: lengths in
// DATALENGTH's bytes, and no portable pattern or not_empty.
var msChecks = checkStyle{
	dialect: "SQL Server",
	quote:   pgString,
	length: func(subject string, unit irv1.LengthUnit) (string, uint32) {
		switch unit {
		case irv1.LengthUnit_LENGTH_UNIT_UTF16_CODE_UNITS:
			return "DATALENGTH(" + subject + ")", 2
		case irv1.LengthUnit_LENGTH_UNIT_BYTES:
			return "DATALENGTH(" + subject + ")", 1
		}
		return "", 0
	},
}

func (w *sqlserver) write(c *irv1.Catalog) (string, error) {
	for _, m := range w.unpack("catalog "+c.GetName().GetStored(), c.GetExtensions()) {
		if _, ok := m.(*sqlserverextv1.Database); !ok {
			w.unwritten("catalog", m)
		}
	}
	for _, ns := range c.GetNamespaces() {
		w.namespace(ns)
	}
	if w.err != nil {
		return "", w.err
	}
	// A generated batch starts and ends with a newline, so its GO lines
	// stand alone; a module's batch is its text as SQL Server kept it.
	var b strings.Builder
	for i, s := range w.stmts {
		w.stmts[i] = "\n" + s + ";\n"
	}
	for _, s := range w.keys {
		w.stmts = append(w.stmts, "\n"+s+";\n")
	}
	w.stmts = append(w.stmts, w.modules...)
	if len(w.comments) > 0 {
		w.stmts = append(w.stmts, "\n"+strings.Join(w.comments, ";\n")+";\n")
	}
	for _, s := range w.stmts {
		// A text that does not start and end a line gains the newlines
		// its GO lines need.
		if !strings.HasPrefix(s, "\n") {
			s = "\n" + s
		}
		if !strings.HasSuffix(s, "\n") {
			s += "\n"
		}
		b.WriteString(s + "GO")
	}
	return strings.TrimPrefix(b.String(), "\n") + "\n", nil
}

func (w *sqlserver) namespace(ns *irv1.Namespace) {
	schema := ns.GetName().GetStored()
	if schema != "dbo" {
		w.add("CREATE SCHEMA %s", msIdent(schema))
	}
	for _, m := range w.unpack("namespace "+schema, ns.GetExtensions()) {
		w.unwritten("namespace "+schema, m)
	}
	for _, t := range ordered(ns.GetTables()) {
		if isView(t) {
			w.view(schema, t)
		} else {
			w.table(schema, t)
		}
	}
	for _, r := range ns.GetRoutines() {
		for _, o := range r.GetOverloads() {
			w.routine(schema, r, o)
		}
	}
}

// comment adds the MS_Description of the object levels name.
func (w *sqlserver) comment(text string, levels ...string) {
	args := []string{msString("MS_Description"), msString(text)}
	for _, l := range levels {
		args = append(args, msString(l))
	}
	w.comments = append(w.comments, "EXEC sp_addextendedproperty "+strings.Join(args, ", "))
}

// comments writes the comments among exts and fails for anything else.
func (w *sqlserver) commentsOf(what string, exts []*anypb.Any, levels ...string) {
	for _, m := range w.unpack(what, exts) {
		if e, ok := m.(*sqlserverextv1.Comment); ok {
			w.comment(e.GetText(), levels...)
		} else {
			w.unwritten(what, m)
		}
	}
}

func (w *sqlserver) table(schema string, t *irv1.Table) {
	tname := t.GetName().GetStored()
	name := msIdent(schema) + "." + msIdent(tname)
	what := "table " + name
	var parts []string
	for _, col := range t.GetColumns() {
		parts = append(parts, w.column(schema, tname, col))
	}
	for _, u := range t.GetUniqueness() {
		w.uniqueness(name, u, &parts)
	}
	for _, c := range t.GetConstraints() {
		if c.GetKind() != irv1.ConstraintKind_CONSTRAINT_KIND_CHECK {
			w.fail("%s: no writer for constraint %s of kind %s", what, c.GetName(), c.GetKind())
			continue
		}
		cond := c.GetOpaque().GetNative()
		if c.GetOpaque() == nil {
			cond = "(" + w.portable(msChecks, what, msIdent(c.GetColumns()[0]), c) + ")"
		}
		parts = append(parts, fmt.Sprintf("CONSTRAINT %s CHECK %s", msIdent(c.GetName()), cond))
		w.commentsOf("constraint "+c.GetName(), c.GetExtensions(), "SCHEMA", schema, "TABLE", tname, "CONSTRAINT", c.GetName())
	}
	for _, fk := range t.GetForeignKeys() {
		ref := fk.GetReferences()
		fkName := fk.GetName().GetStored()
		w.keys = append(w.keys, fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s.%s (%s) ON UPDATE %s ON DELETE %s",
			name, msIdent(fkName), msIdents(fk.GetColumns()), msIdent(ref.GetTable().GetNamespace()), msIdent(ref.GetTable().GetObject()),
			msIdents(ref.GetColumns()), action(fk.GetOnUpdate()), action(fk.GetOnDelete())))
		w.commentsOf("foreign key "+fkName, fk.GetExtensions(), "SCHEMA", schema, "TABLE", tname, "CONSTRAINT", fkName)
	}
	for _, m := range w.unpack(what, t.GetExtensions()) {
		switch e := m.(type) {
		case *sqlserverextv1.Comment:
			w.comment(e.GetText(), "SCHEMA", schema, "TABLE", tname)
		case *sqlserverextv1.Trigger:
			w.modules = append(w.modules, e.GetDefinition())
		case *sqlserverextv1.Index:
			w.keys = append(w.keys, w.msIndex(name, e))
		default:
			w.unwritten(what, m)
		}
	}
	w.add("CREATE TABLE %s (\n  %s\n)", name, strings.Join(parts, ",\n  "))
}

// uniqueness writes a key into a table's parts, or a unique index after it.
func (w *sqlserver) uniqueness(table string, u *irv1.Uniqueness, parts *[]string) {
	what := "uniqueness " + u.GetName()
	var idx *sqlserverextv1.Index
	for _, m := range w.unpack(what, u.GetExtensions()) {
		if e, ok := m.(*sqlserverextv1.Index); ok {
			idx = e
		} else {
			w.unwritten(what, m)
		}
	}
	if idx == nil {
		w.fail("%s: no index", what)
		return
	}
	switch u.GetKind() {
	case irv1.UniquenessKind_UNIQUENESS_PRIMARY_KEY:
		*parts = append(*parts, fmt.Sprintf("CONSTRAINT %s PRIMARY KEY %s (%s)", msIdent(u.GetName()), idx.GetType(), w.msParts(idx)))
	case irv1.UniquenessKind_UNIQUENESS_CONSTRAINT:
		*parts = append(*parts, fmt.Sprintf("CONSTRAINT %s UNIQUE %s (%s)", msIdent(u.GetName()), idx.GetType(), w.msParts(idx)))
	default:
		w.keys = append(w.keys, w.msIndex(table, idx))
	}
}

// msIndex is the CREATE INDEX of idx on table.
func (w *sqlserver) msIndex(table string, idx *sqlserverextv1.Index) string {
	unique := ""
	if idx.GetUnique() {
		unique = "UNIQUE "
	}
	s := fmt.Sprintf("CREATE %s%s INDEX %s ON %s (%s)", unique, idx.GetType(), msIdent(idx.GetName()), table, w.msParts(idx))
	if idx.GetFilter() != "" {
		s += " WHERE " + idx.GetFilter()
	}
	return s
}

// msParts writes an index's columns, failing for what the writer does not
// write.
func (w *sqlserver) msParts(idx *sqlserverextv1.Index) string {
	if len(idx.GetIncluded()) > 0 || idx.GetDisabled() || idx.GetComment() != "" {
		w.fail("index %s: no writer for INCLUDE, a disabled index or its comment", idx.GetName())
	}
	columns := make([]string, len(idx.GetParts()))
	for i, p := range idx.GetParts() {
		if p.GetDescending() {
			w.fail("index %s: no writer for a descending part", idx.GetName())
		}
		columns[i] = p.GetColumn()
	}
	return msIdents(columns)
}

func (w *sqlserver) column(schema, table string, col *irv1.Column) string {
	cname := col.GetName().GetStored()
	what := fmt.Sprintf("column %s.%s", table, cname)
	var identity *sqlserverextv1.Identity
	defaultName := ""
	for _, m := range w.unpack(what, col.GetExtensions()) {
		switch e := m.(type) {
		case *sqlserverextv1.Identity:
			identity = e
		case *sqlserverextv1.DefaultConstraint:
			defaultName = e.GetName()
		case *sqlserverextv1.Comment:
			w.comment(e.GetText(), "SCHEMA", schema, "TABLE", table, "COLUMN", cname)
		default:
			w.unwritten(what, m)
		}
	}
	if g := col.GetGeneration(); g != nil {
		s := msIdent(cname) + " AS " + g.GetNative()
		if g.GetKind() == irv1.GenerationKind_GENERATION_STORED {
			s += " PERSISTED"
		}
		return s
	}
	var b strings.Builder
	b.WriteString(msIdent(cname) + " " + w.typeText(what, col.GetType()))
	if n := col.GetCollation().GetName(); n != "" {
		b.WriteString(" COLLATE " + n)
	}
	if identity != nil {
		fmt.Fprintf(&b, " IDENTITY(%s, %s)", identity.GetSeed(), identity.GetIncrement())
	}
	if !col.GetNullable() && col.GetNullabilitySource() == irv1.NullabilitySource_NULLABILITY_SOURCE_DATABASE {
		b.WriteString(" NOT NULL")
	} else {
		b.WriteString(" NULL")
	}
	if def := col.GetDefault(); def.GetNative() != "" {
		if defaultName != "" {
			b.WriteString(" CONSTRAINT " + msIdent(defaultName))
		}
		b.WriteString(" DEFAULT " + def.GetNative())
	}
	return b.String()
}

func (w *sqlserver) view(schema string, t *irv1.Table) {
	vname := t.GetName().GetStored()
	what := "view " + vname
	written := false
	for _, m := range w.unpack(what, t.GetExtensions()) {
		switch e := m.(type) {
		case *sqlserverextv1.ViewDefinition:
			w.modules = append(w.modules, e.GetDefinition())
			written = true
		case *sqlserverextv1.Comment:
			w.comment(e.GetText(), "SCHEMA", schema, "VIEW", vname)
		default:
			w.unwritten(what, m)
		}
	}
	if !written {
		w.fail("%s: no definition", what)
	}
	for _, col := range t.GetColumns() {
		w.commentsOf(what, col.GetExtensions(), "SCHEMA", schema, "VIEW", vname, "COLUMN", col.GetName().GetStored())
	}
}

func (w *sqlserver) routine(schema string, r *irv1.Routine, o *irv1.Overload) {
	rname := r.GetName().GetStored()
	what := "routine " + rname
	kind := "FUNCTION"
	if o.GetKind() == irv1.RoutineKind_ROUTINE_KIND_PROCEDURE {
		kind = "PROCEDURE"
	}
	written := false
	for _, m := range w.unpack(what, o.GetExtensions()) {
		switch e := m.(type) {
		case *sqlserverextv1.RoutineProperties:
			w.modules = append(w.modules, e.GetDefinition())
			written = true
		case *sqlserverextv1.Comment:
			w.comment(e.GetText(), "SCHEMA", schema, kind, rname)
		default:
			w.unwritten(what, m)
		}
	}
	if !written {
		w.fail("%s: no definition", what)
	}
}

// msIdent quotes an identifier.
func msIdent(s string) string {
	return "[" + strings.ReplaceAll(s, "]", "]]") + "]"
}

// msIdents quotes and joins identifiers.
func msIdents(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = msIdent(s)
	}
	return strings.Join(q, ", ")
}

// msString quotes a Unicode string literal.
func msString(s string) string {
	return "N'" + strings.ReplaceAll(s, "'", "''") + "'"
}
