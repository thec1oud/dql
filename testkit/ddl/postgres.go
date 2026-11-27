package ddl

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	pgextv1 "github.com/thec1oud/dql/proto/dql/ext/postgres/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// postgres writes DDL as pg-live reads it back. The database, its encoding
// and locale, its installed extensions and its roles are the caller's to
// create; the writer creates what lives in the catalog's namespaces.
type postgres struct {
	script
	// keys need every table, views every routine, and after every object:
	// comments and owners.
	keys, views, after []string
}

func (w *postgres) write(c *irv1.Catalog) (string, error) {
	for _, m := range w.unpack("catalog", c.GetExtensions()) {
		switch m.(type) {
		case *pgextv1.Database, *pgextv1.InstalledExtension:
		default:
			w.unwritten("catalog", m)
		}
	}
	for _, ns := range c.GetNamespaces() {
		w.namespace(ns)
	}
	w.stmts = append(w.stmts, w.keys...)
	w.stmts = append(w.stmts, w.views...)
	w.stmts = append(w.stmts, w.after...)
	return w.join(";\n")
}

func (w *postgres) namespace(ns *irv1.Namespace) {
	name := pgIdent(ns.GetName().GetStored())
	if ns.GetName().GetStored() != "public" {
		w.add("CREATE SCHEMA %s", name)
	}
	w.objectExts("SCHEMA "+name, "namespace "+name, ns.GetExtensions())
	for _, e := range ns.GetEnums() {
		w.enum(ns, e)
	}
	for _, d := range ns.GetDomains() {
		w.domain(ns, d)
	}
	for _, c := range ns.GetComposites() {
		w.composite(ns, c)
	}
	// Every sequence is an identity's, which its column writes.
	sequences := map[string]*irv1.Sequence{}
	for _, s := range ns.GetSequences() {
		sequences[s.GetName().GetStored()] = s
	}
	for _, t := range ordered(ns.GetTables()) {
		if isView(t) {
			w.view(ns, t)
		} else {
			w.table(ns, t, sequences)
		}
	}
	for name := range sequences {
		w.fail("sequence %s: no writer for a sequence that is no identity's", name)
	}
	for _, r := range ns.GetRoutines() {
		for _, o := range r.GetOverloads() {
			w.routine(ns, r, o)
		}
	}
}

// pgQualified returns the quoted name of object in ns.
func pgQualified(ns *irv1.Namespace, object string) string {
	return pgIdent(ns.GetName().GetStored()) + "." + pgIdent(object)
}

// objectExts writes the comment and owner of the object that target names
// in COMMENT ON and ALTER, failing for any other extension.
func (w *postgres) objectExts(target, what string, exts []*anypb.Any) {
	for _, m := range w.unpack(what, exts) {
		w.common(target, what, m)
	}
}

// common writes m if it is a comment or owner of target, and fails for
// anything else.
func (w *postgres) common(target, what string, m proto.Message) {
	switch e := m.(type) {
	case *pgextv1.Comment:
		w.after = append(w.after, fmt.Sprintf("COMMENT ON %s IS %s", target, pgString(e.GetText())))
	case *pgextv1.Owner:
		w.after = append(w.after, fmt.Sprintf("ALTER %s OWNER TO %s", target, pgIdent(e.GetRole())))
	default:
		w.unwritten(what, m)
	}
}

func (w *postgres) enum(ns *irv1.Namespace, e *irv1.Enum) {
	name := pgQualified(ns, e.GetName().GetStored())
	labels := make([]string, len(e.GetLabels()))
	for i, l := range e.GetLabels() {
		labels[i] = pgString(l)
	}
	w.add("CREATE TYPE %s AS ENUM (%s)", name, strings.Join(labels, ", "))
	w.objectExts("TYPE "+name, "enum "+name, e.GetExtensions())
}

func (w *postgres) domain(ns *irv1.Namespace, d *irv1.Domain) {
	name := pgQualified(ns, d.GetName().GetStored())
	what := "domain " + name
	var b strings.Builder
	fmt.Fprintf(&b, "CREATE DOMAIN %s AS %s%s", name, w.typeText(what, d.GetBase()), pgCollate(d.GetCollation()))
	if def := d.GetDefault(); def != nil {
		fmt.Fprintf(&b, " DEFAULT %s", def.GetNative())
	}
	if !d.GetNullable() {
		b.WriteString(" NOT NULL")
	}
	for _, c := range d.GetConstraints() {
		fmt.Fprintf(&b, " CONSTRAINT %s %s", pgIdent(c.GetName()), w.check(what, "VALUE", c))
		w.objectExts(fmt.Sprintf("CONSTRAINT %s ON DOMAIN %s", pgIdent(c.GetName()), name), what, c.GetExtensions())
	}
	w.add("%s", b.String())
	w.objectExts("DOMAIN "+name, what, d.GetExtensions())
}

func (w *postgres) composite(ns *irv1.Namespace, c *irv1.Composite) {
	name := pgQualified(ns, c.GetName().GetStored())
	what := "composite " + name
	attrs := make([]string, len(c.GetAttributes()))
	for i, a := range c.GetAttributes() {
		attrs[i] = pgIdent(a.GetName().GetStored()) + " " + w.typeText(what, a.GetType()) + pgCollate(a.GetCollation())
		w.objectExts(fmt.Sprintf("COLUMN %s.%s", name, pgIdent(a.GetName().GetStored())), what, a.GetExtensions())
	}
	w.add("CREATE TYPE %s AS (%s)", name, strings.Join(attrs, ", "))
	w.objectExts("TYPE "+name, what, c.GetExtensions())
}

func (w *postgres) table(ns *irv1.Namespace, t *irv1.Table, sequences map[string]*irv1.Sequence) {
	name := pgQualified(ns, t.GetName().GetStored())
	what := "table " + name
	var parts []string
	for _, col := range t.GetColumns() {
		parts = append(parts, w.column(ns, name, col, sequences))
	}
	for _, u := range t.GetUniqueness() {
		parts = append(parts, w.uniqueness(name, u)...)
	}
	for _, c := range t.GetConstraints() {
		subject := ""
		if len(c.GetColumns()) > 0 {
			subject = pgIdent(c.GetColumns()[0])
		}
		parts = append(parts, fmt.Sprintf("CONSTRAINT %s %s", pgIdent(c.GetName()), w.check(what, subject, c)))
		w.objectExts(fmt.Sprintf("CONSTRAINT %s ON %s", pgIdent(c.GetName()), name), what, c.GetExtensions())
	}
	for _, fk := range t.GetForeignKeys() {
		w.foreignKey(name, fk)
	}
	w.add("CREATE TABLE %s (\n  %s\n)", name, strings.Join(parts, ",\n  "))
	w.objectExts("TABLE "+name, what, t.GetExtensions())
}

// uniqueness returns the table constraint of a key, or writes a unique
// index, once its table exists, from the definition pg-live read.
func (w *postgres) uniqueness(table string, u *irv1.Uniqueness) []string {
	what := "uniqueness " + u.GetName()
	var def string
	for _, m := range w.unpack(what, u.GetExtensions()) {
		if e, ok := m.(*pgextv1.Index); ok {
			def = e.GetDefinition()
		} else {
			w.unwritten(what, m)
		}
	}
	kind := "UNIQUE"
	switch {
	case u.GetKind() == irv1.UniquenessKind_UNIQUENESS_INDEX:
		if def == "" {
			w.fail("%s on %s: no definition", what, table)
		}
		w.keys = append(w.keys, def)
		return nil
	case u.GetKind() == irv1.UniquenessKind_UNIQUENESS_PRIMARY_KEY:
		kind = "PRIMARY KEY"
	case !u.GetNullsDistinct():
		kind = "UNIQUE NULLS NOT DISTINCT"
	}
	return []string{fmt.Sprintf("CONSTRAINT %s %s (%s)", pgIdent(u.GetName()), kind, pgIdents(u.GetColumns()))}
}

func (w *postgres) column(ns *irv1.Namespace, table string, col *irv1.Column, sequences map[string]*irv1.Sequence) string {
	cname := pgIdent(col.GetName().GetStored())
	what := fmt.Sprintf("column %s.%s", table, cname)
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s%s", cname, w.typeText(what, col.GetType()), pgCollate(col.GetCollation()))
	if g := col.GetGeneration(); g != nil {
		fmt.Fprintf(&b, " GENERATED ALWAYS AS (%s) STORED", g.GetNative())
	}
	switch def := col.GetDefault(); def.GetClass() {
	case irv1.DefaultClass_DEFAULT_UNSPECIFIED:
	case irv1.DefaultClass_DEFAULT_IDENTITY_ALWAYS, irv1.DefaultClass_DEFAULT_IDENTITY_BY_DEFAULT:
		w.identity(ns, what, def, sequences, &b)
	default:
		fmt.Fprintf(&b, " DEFAULT %s", def.GetNative())
	}
	if !col.GetNullable() && col.GetNullabilitySource() == irv1.NullabilitySource_NULLABILITY_SOURCE_DATABASE {
		b.WriteString(" NOT NULL")
	}
	w.objectExts("COLUMN "+table+"."+cname, what, col.GetExtensions())
	return b.String()
}

// identity writes an identity with every option of its sequence, so none
// depends on a default. The sequence belongs to its table, owner and all.
func (w *postgres) identity(ns *irv1.Namespace, what string, def *irv1.Default, sequences map[string]*irv1.Sequence, b *strings.Builder) {
	name := def.GetSequence().GetObject()
	s := sequences[name]
	if s == nil {
		w.fail("%s: no sequence %s for its identity", what, name)
		return
	}
	delete(sequences, name)
	when := map[irv1.DefaultClass]string{
		irv1.DefaultClass_DEFAULT_IDENTITY_ALWAYS:     "ALWAYS",
		irv1.DefaultClass_DEFAULT_IDENTITY_BY_DEFAULT: "BY DEFAULT",
	}[def.GetClass()]
	fmt.Fprintf(b, " GENERATED %s AS IDENTITY (SEQUENCE NAME %s START WITH %d INCREMENT BY %d MINVALUE %d MAXVALUE %d",
		when, pgQualified(ns, name), s.GetStart(), s.GetIncrement(), s.GetMin(), s.GetMax())
	if s.GetCycle() {
		b.WriteString(" CYCLE")
	}
	b.WriteString(")")
	for _, m := range w.unpack("sequence "+name, s.GetExtensions()) {
		if _, ok := m.(*pgextv1.Owner); !ok {
			w.unwritten("sequence "+name, m)
		}
	}
}

func (w *postgres) foreignKey(table string, fk *irv1.ForeignKey) {
	name := pgIdent(fk.GetName().GetStored())
	what := "foreign key " + name
	ref := fk.GetReferences()
	if fk.GetMatch() != irv1.ForeignKeyMatch_FOREIGN_KEY_MATCH_SIMPLE || fk.GetDeferrable() {
		w.fail("%s: no writer for MATCH %s or DEFERRABLE", what, fk.GetMatch())
	}
	w.keys = append(w.keys, fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s.%s (%s) ON UPDATE %s ON DELETE %s",
		table, name, pgIdents(fk.GetColumns()), pgIdent(ref.GetTable().GetNamespace()), pgIdent(ref.GetTable().GetObject()),
		pgIdents(ref.GetColumns()), action(fk.GetOnUpdate()), action(fk.GetOnDelete())))
	w.objectExts(fmt.Sprintf("CONSTRAINT %s ON %s", name, table), what, fk.GetExtensions())
}

func (w *postgres) view(ns *irv1.Namespace, t *irv1.Table) {
	name := pgQualified(ns, t.GetName().GetStored())
	what := "view " + name
	if t.GetKind() != irv1.TableKind_RELATION_KIND_VIEW {
		w.fail("%s: no writer for %s", what, t.GetKind())
	}
	def := ""
	for _, m := range w.unpack(what, t.GetExtensions()) {
		if e, ok := m.(*pgextv1.ViewDefinition); ok {
			def = strings.TrimSuffix(strings.TrimSpace(e.GetDefinition()), ";")
		} else {
			w.common("VIEW "+name, what, m)
		}
	}
	if def == "" {
		w.fail("%s: no definition", what)
	}
	w.views = append(w.views, fmt.Sprintf("CREATE VIEW %s AS %s", name, def))
	for _, col := range t.GetColumns() {
		w.objectExts("COLUMN "+name+"."+pgIdent(col.GetName().GetStored()), what, col.GetExtensions())
	}
}

// routine writes a function of IN arguments with no defaults, returning a
// scalar.
func (w *postgres) routine(ns *irv1.Namespace, r *irv1.Routine, o *irv1.Overload) {
	name := pgQualified(ns, r.GetName().GetStored())
	what := "routine " + name
	var src *pgextv1.RoutineSource
	props := &pgextv1.RoutineProperties{}
	var rest []proto.Message
	for _, m := range w.unpack(what, o.GetExtensions()) {
		switch e := m.(type) {
		case *pgextv1.RoutineSource:
			src = e
		case *pgextv1.RoutineProperties:
			props = e
		default:
			rest = append(rest, m)
		}
	}
	if src == nil || o.GetKind() != irv1.RoutineKind_ROUTINE_KIND_FUNCTION || o.GetReturns().GetScalar() == nil ||
		len(props.GetArgumentDefaults()) > 0 || props.GetSecurityDefiner() || props.GetLeakproof() {
		w.fail("%s: no writer for a routine other than a function of a scalar, with its source", what)
		return
	}
	var args, types []string
	for _, a := range o.GetArguments() {
		if a.GetMode() != irv1.ArgMode_ARG_MODE_IN {
			w.fail("%s: no writer for an argument of mode %s", what, a.GetMode())
		}
		typ := w.typeText(what, a.GetType())
		args = append(args, pgIdent(a.GetName().GetStored())+" "+typ)
		types = append(types, typ)
	}
	strict := ""
	if o.GetNullBehavior() == irv1.NullBehavior_NULL_BEHAVIOR_STRICT {
		strict = " STRICT"
	}
	if strings.Contains(src.GetBody(), "$dql$") {
		w.fail("%s: a body holding $dql$", what)
	}
	w.add("CREATE FUNCTION %s(%s) RETURNS %s LANGUAGE %s %s%s PARALLEL %s AS $dql$%s$dql$",
		name, strings.Join(args, ", "), w.typeText(what, o.GetReturns().GetScalar().GetType()), src.GetLanguage(),
		strings.TrimPrefix(o.GetVolatility().String(), "VOLATILITY_"), strict, props.GetParallel(), src.GetBody())
	target := fmt.Sprintf("FUNCTION %s(%s)", name, strings.Join(types, ", "))
	for _, m := range rest {
		w.common(target, what, m)
	}
}

// pgChecks are the forms RecognizeCheck lists first.
var pgChecks = checkStyle{
	dialect: "Postgres",
	quote:   pgString,
	length: func(subject string, unit irv1.LengthUnit) (string, uint32) {
		if unit != irv1.LengthUnit_LENGTH_UNIT_CHARACTERS {
			return "", 0
		}
		return "char_length(" + subject + ")", 1
	},
	pattern:  func(subject, re string) string { return subject + " ~ " + pgString(re) },
	notEmpty: true,
}

// check writes a check constraint on subject: its native text when opaque,
// else its portable kind.
func (w *postgres) check(what, subject string, c *irv1.Constraint) string {
	if c.GetKind() != irv1.ConstraintKind_CONSTRAINT_KIND_CHECK {
		w.fail("%s: no writer for constraint %s of kind %s", what, c.GetName(), c.GetKind())
	}
	if o := c.GetOpaque(); o != nil {
		return o.GetNative()
	}
	return "CHECK (" + w.portable(pgChecks, what, subject, c) + ")"
}

// action is the SQL of a referential action.
func action(a irv1.ForeignKeyAction) string {
	return strings.ReplaceAll(strings.TrimPrefix(a.String(), "FOREIGN_KEY_ACTION_"), "_", " ")
}

// pgCollate is the COLLATE clause of c, empty for the default collation.
func pgCollate(c *irv1.Collation) string {
	if c.GetName() == "" || c.GetName() == "default" {
		return ""
	}
	return " COLLATE " + pgIdent(c.GetName())
}

// pgIdent quotes an identifier.
func pgIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// pgIdents quotes and joins identifiers.
func pgIdents(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = pgIdent(s)
	}
	return strings.Join(q, ", ")
}

// pgString quotes a string literal.
func pgString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
