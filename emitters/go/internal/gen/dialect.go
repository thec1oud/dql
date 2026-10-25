package gen

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// classes are the taxonomy classes of the error kinds a dialect maps
// native codes to (§23.1).
var classes = map[irv1.ErrorKind]string{
	irv1.ErrorKind_ERROR_KIND_UNIQUE_VIOLATION:      "ClassUniqueViolation",
	irv1.ErrorKind_ERROR_KIND_FOREIGN_KEY_VIOLATION: "ClassForeignKeyViolation",
	irv1.ErrorKind_ERROR_KIND_CHECK_VIOLATION:       "ClassCheckViolation",
	irv1.ErrorKind_ERROR_KIND_NOT_NULL_VIOLATION:    "ClassNotNullViolation",
	irv1.ErrorKind_ERROR_KIND_EXCLUSION_VIOLATION:   "ClassExclusionViolation",
	irv1.ErrorKind_ERROR_KIND_SERIALIZATION_FAILURE: "ClassSerializationFailure",
	irv1.ErrorKind_ERROR_KIND_DEADLOCK:              "ClassDeadlock",
	irv1.ErrorKind_ERROR_KIND_LOCK_TIMEOUT:          "ClassLockTimeout",
	irv1.ErrorKind_ERROR_KIND_CONNECTION:            "ClassConnectionError",
	irv1.ErrorKind_ERROR_KIND_TIMEOUT:               "ClassTimeout",
}

// constraintKinds are the kinds of constraint a violation names (§23.2); a
// unique violation of a primary key names a primary key.
var constraintKinds = map[irv1.ErrorKind]string{
	irv1.ErrorKind_ERROR_KIND_UNIQUE_VIOLATION:      "ConstraintUnique",
	irv1.ErrorKind_ERROR_KIND_FOREIGN_KEY_VIOLATION: "ConstraintForeignKey",
	irv1.ErrorKind_ERROR_KIND_CHECK_VIOLATION:       "ConstraintCheck",
	irv1.ErrorKind_ERROR_KIND_NOT_NULL_VIOLATION:    "ConstraintNotNull",
	irv1.ErrorKind_ERROR_KIND_EXCLUSION_VIOLATION:   "ConstraintExclusion",
}

// placeholders are the runtime's placeholder styles (§16.2).
var placeholders = map[irv1.Placeholder]string{
	irv1.Placeholder_PLACEHOLDER_DOLLAR_NUMBERED: "DollarNumbered",
	irv1.Placeholder_PLACEHOLDER_QUESTION:        "Question",
	irv1.Placeholder_PLACEHOLDER_AT_NUMBERED:     "AtNumbered",
}

// dialectName is the dialect's name in errors (§23.1), as dql.yaml spells it.
func dialectName(d irv1.Dialect) string {
	return strings.ToLower(strings.TrimPrefix(d.String(), "DIALECT_"))
}

// dialect generates dql-dialect.dql.go: the Dialect the executor runs the
// package's statements with, from the capability profile (§22.5, §22.7,
// §23, §25.2), the catalogs the package reads, and NewExecutor.
func (g *generator) dialect() {
	f := g.newFile("dql-dialect")
	dql := f.use(pkgDQL, "dql")
	p := g.b.GetCapabilities()
	for _, n := range []string{"Dialect", "Catalogs", "NewExecutor", "fragments"} {
		g.declare(n, "the package's "+n, nil)
	}
	f.printf("// Dialect is how the executor runs this package's statements on %s:\n", dialectName(g.b.GetDialect()))
	f.printf("// its session setup, its errors, its replicas and its limits.\nvar Dialect = %s.Dialect{\n", dql)
	f.printf("\tName: %q,\n", dialectName(g.b.GetDialect()))
	if len(p.GetSessionSetup()) > 0 {
		f.printf("\tSetup: []string{\n")
		for _, s := range p.GetSessionSetup() {
			f.printf("\t\t%s,\n", strconv.Quote(s))
		}
		f.printf("\t},\n")
	}
	f.printf("\tErrors: %s.ErrorMap{\n\t\tCodes: map[string]%s.Class{\n", dql, dql)
	for _, c := range p.GetErrorCodes() {
		if class, ok := classes[c.GetKind()]; ok {
			f.printf("\t\t\t%q: %s.%s,\n", c.GetCode(), dql, class)
		}
	}
	f.printf("\t\t},\n")
	if len(p.GetViolations()) > 0 {
		f.printf("\t\tViolations: map[%s.Reported]%s.Constraint{\n", dql, dql)
		for _, v := range p.GetViolations() {
			class, ok := classes[v.GetKind()]
			kind, ok2 := constraintKinds[v.GetKind()]
			if !ok || !ok2 {
				continue
			}
			o := v.GetObject()
			if g.primaryKey(o, v.GetRule()) {
				kind = "ConstraintPrimaryKey"
			}
			f.printf("\t\t\t{Catalog: %q, Schema: %q, Object: %q, Class: %s.%s, Name: %q}: {Name: %q, Kind: %s.%s",
				o.GetCatalog(), o.GetNamespace(), o.GetObject(), dql, class, v.GetReported(), v.GetRule(), dql, kind)
			if v.GetRedact() {
				f.printf(", Sensitive: true")
			}
			f.printf("},\n")
		}
		f.printf("\t\t},\n")
	}
	f.printf("\t},\n")
	if r := p.GetReplication(); r != nil && g.replicas() {
		f.printf("\tReplication: &%s.Replication{\n\t\tPosition: %s,\n\t\tLag: %s,\n\t\tReplayed: %s,\n\t},\n",
			dql, strconv.Quote(r.GetPosition()), strconv.Quote(r.GetLag()), strconv.Quote(r.GetReplayed()))
	}
	if p.GetMaxParams() > 0 {
		f.printf("\tMaxParams: %d,\n", p.GetMaxParams())
	}
	f.printf("}\n\n")
	var catalogs []string
	for _, c := range g.b.GetCatalogs() {
		catalogs = append(catalogs, strconv.Quote(c.GetName().GetStored()))
	}
	f.printf("// Catalogs are the catalogs this package reads, each bound to a source\n// in the executor's configuration (§22.1).\nvar Catalogs = []string{%s}\n\n", strings.Join(catalogs, ", "))
	ctx := f.use("context", "context")
	f.printf(`// NewExecutor returns an executor running this package's statements on
// driver, with c binding every catalog to a source (§22.1).
func NewExecutor(ctx %s.Context, driver %s.Driver, c %s.Config, opts ...%s.Option) (*%s.Executor, error) {
	return %s.New(ctx, driver, Dialect, c, opts...)
}
`, ctx, dql, dql, dql, dql, dql)
	g.fragmentTable()
}

// replicas reports whether the dialect reads from replicas at any level.
func (g *generator) replicas() bool {
	return slices.ContainsFunc(g.b.GetCapabilities().GetConsistencyLevels(), func(l irv1.ConsistencyLevel) bool {
		return l != irv1.ConsistencyLevel_CONSISTENCY_PRIMARY
	})
}

// primaryKey reports whether rule is the primary key of the table o names.
func (g *generator) primaryKey(o *irv1.QualifiedName, rule string) bool {
	for _, u := range g.ix.tables[keyOf(o)].GetUniqueness() {
		if u.GetName() == rule {
			return u.GetKind() == irv1.UniquenessKind_UNIQUENESS_PRIMARY_KEY
		}
	}
	return false
}

// fragmentTable generates dql-fragments.dql.go: the dialect plugin's
// fragment table, which builder queries are assembled from (§16.2).
func (g *generator) fragmentTable() {
	f := g.newFile("dql-fragments")
	dql := f.use(pkgDQL, "dql")
	t := g.b.GetQueries().GetFragments()
	g.fragmentIDs = map[string]bool{}
	f.printf("// fragments is the fragment table of %s, rendered by its dialect\n// plugin (§16.2, §18.4).\nvar fragments = &%s.Fragments{\n", dialectName(g.b.GetDialect()), dql)
	if p, ok := placeholders[t.GetPlaceholder()]; ok {
		f.printf("\tPlaceholder: %s.%s,\n", dql, p)
	}
	f.printf("\tTemplates: map[string]string{\n")
	for _, fr := range t.GetFragments() {
		g.fragmentIDs[fr.GetId()] = true
		f.printf("\t\t%q: %s,\n", fr.GetId(), quote(fr.GetTemplate()))
	}
	f.printf("\t},\n}\n")
}

// quote writes s as a raw string literal where it can, so SQL with quoted
// identifiers stays readable.
func quote(s string) string {
	if strings.ContainsAny(s, "`\r") || !strconv.CanBackquote(s) {
		return strconv.Quote(s)
	}
	return "`" + s + "`"
}

// consistency is the Go value of a query's default consistency (§22.5): the
// runtime's default when unset, the primary at a level the dialect lacks.
func (g *generator) consistency(f *file, c *irv1.Consistency) string {
	l := c.GetLevel()
	if l == irv1.ConsistencyLevel_CONSISTENCY_UNSPECIFIED {
		return ""
	}
	dql := f.use(pkgDQL, "dql")
	if !slices.Contains(g.b.GetCapabilities().GetConsistencyLevels(), l) {
		return dql + ".Primary"
	}
	switch l {
	case irv1.ConsistencyLevel_CONSISTENCY_ANY:
		return dql + ".Any"
	case irv1.ConsistencyLevel_CONSISTENCY_BOUNDED_STALENESS:
		return fmt.Sprintf("%s.Bounded(%d * %s.Millisecond)", dql, c.GetMaxStaleness().AsDuration()/time.Millisecond, f.use("time", "time"))
	case irv1.ConsistencyLevel_CONSISTENCY_READ_YOUR_WRITES:
		return dql + ".ReadYourWrites"
	}
	return dql + ".Primary"
}
