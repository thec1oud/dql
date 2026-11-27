package gen

import (
	"fmt"
	"slices"
	"strings"
	"time"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// classes are the taxonomy classes of the error kinds a dialect maps
// native codes to (§23.1), as the runtime's ErrorClass names them.
var classes = map[irv1.ErrorKind]string{
	irv1.ErrorKind_ERROR_KIND_UNIQUE_VIOLATION:      "UniqueViolation",
	irv1.ErrorKind_ERROR_KIND_FOREIGN_KEY_VIOLATION: "ForeignKeyViolation",
	irv1.ErrorKind_ERROR_KIND_CHECK_VIOLATION:       "CheckViolation",
	irv1.ErrorKind_ERROR_KIND_NOT_NULL_VIOLATION:    "NotNullViolation",
	irv1.ErrorKind_ERROR_KIND_EXCLUSION_VIOLATION:   "ExclusionViolation",
	irv1.ErrorKind_ERROR_KIND_SERIALIZATION_FAILURE: "SerializationFailure",
	irv1.ErrorKind_ERROR_KIND_DEADLOCK:              "Deadlock",
	irv1.ErrorKind_ERROR_KIND_LOCK_TIMEOUT:          "LockTimeout",
	irv1.ErrorKind_ERROR_KIND_CONNECTION:            "ConnectionError",
	irv1.ErrorKind_ERROR_KIND_TIMEOUT:               "Timeout",
}

// constraintKinds are the kinds of constraint a violation names (§23.2); a
// unique violation of a primary key names a primary key.
var constraintKinds = map[irv1.ErrorKind]string{
	irv1.ErrorKind_ERROR_KIND_UNIQUE_VIOLATION:      "unique",
	irv1.ErrorKind_ERROR_KIND_FOREIGN_KEY_VIOLATION: "foreign_key",
	irv1.ErrorKind_ERROR_KIND_CHECK_VIOLATION:       "check",
	irv1.ErrorKind_ERROR_KIND_NOT_NULL_VIOLATION:    "not_null",
	irv1.ErrorKind_ERROR_KIND_EXCLUSION_VIOLATION:   "exclusion",
}

// placeholders are the runtime's placeholder styles (§16.2).
var placeholders = map[irv1.Placeholder]string{
	irv1.Placeholder_PLACEHOLDER_DOLLAR_NUMBERED: "dollar",
	irv1.Placeholder_PLACEHOLDER_QUESTION:        "question",
	irv1.Placeholder_PLACEHOLDER_AT_NUMBERED:     "at",
}

// statementModes are the runtime's names of the statement modes (§26.4).
var statementModes = map[irv1.StatementMode]string{
	irv1.StatementMode_STATEMENT_MODE_PREPARE:  "prepare",
	irv1.StatementMode_STATEMENT_MODE_DESCRIBE: "describe",
	irv1.StatementMode_STATEMENT_MODE_SIMPLE:   "simple",
}

// dialectName is the dialect's name in errors (§23.1), as dql.yaml spells it.
func dialectName(d irv1.Dialect) string {
	return strings.ToLower(strings.TrimPrefix(d.String(), "DIALECT_"))
}

// dialect generates dql-dialect.dql.ts: the Dialect the executor runs the
// package's statements with, from the capability profile (§22.5, §22.7,
// §23, §25.2), the catalogs the package reads, and newExecutor.
func (g *generator) dialect() {
	f := g.newFile("dql-dialect")
	p := g.b.GetCapabilities()
	for _, n := range []string{"dialect", "catalogs", "newExecutor"} {
		g.declare(f, n, "the package's "+n, nil)
	}
	name := dialectName(g.b.GetDialect())
	f.printf("/**\n * dialect is how the executor runs this package's statements on %s:\n * its session setup, its errors, its replicas and its limits.\n */\n", name)
	f.printf("export const dialect: %s = {\n  name: %s,\n  setup: [", f.rt("Dialect", false), jsString(name))
	if len(p.GetSessionSetup()) > 0 {
		f.printf("\n")
		for _, s := range p.GetSessionSetup() {
			f.printf("    %s,\n", jsString(s))
		}
		f.printf("  ")
	}
	f.printf("],\n  errors: {\n    codes: {\n")
	for _, c := range p.GetErrorCodes() {
		if class, ok := classes[c.GetKind()]; ok {
			f.printf("      %s: %s,\n", jsString(c.GetCode()), jsString(class))
		}
	}
	f.printf("    },\n    violations: [")
	var violations []string
	for _, v := range p.GetViolations() {
		class, ok := classes[v.GetKind()]
		kind, ok2 := constraintKinds[v.GetKind()]
		if !ok || !ok2 {
			continue
		}
		o := v.GetObject()
		if g.primaryKey(o, v.GetRule()) {
			kind = "primary_key"
		}
		violations = append(violations, fmt.Sprintf("      [{ catalog: %s, schema: %s, object: %s, class: %s, name: %s }, { name: %s, kind: %s, sensitive: %t }],\n",
			jsString(o.GetCatalog()), jsString(o.GetNamespace()), jsString(o.GetObject()), jsString(class), jsString(v.GetReported()),
			jsString(v.GetRule()), jsString(kind), v.GetRedact()))
	}
	if len(violations) > 0 {
		f.printf("\n%s    ", strings.Join(violations, ""))
	}
	f.printf("],\n  },\n")
	if r := p.GetReplication(); r != nil && g.replicas() {
		f.printf("  replication: {\n    position: %s,\n    lag: %s,\n    replayed: %s,\n  },\n",
			jsString(r.GetPosition()), jsString(r.GetLag()), jsString(r.GetReplayed()))
	}
	if p.GetMaxParams() > 0 {
		f.printf("  maxParams: %d,\n", p.GetMaxParams())
	}
	// The statement modes a source may choose (§26.4). A mode this runtime
	// does not know is left out, so a source cannot choose it.
	var modes []string
	for _, m := range p.GetStatementModes() {
		if name, ok := statementModes[m]; ok {
			modes = append(modes, jsString(name))
		}
	}
	if len(p.GetStatementModes()) > 0 {
		f.printf("  statementModes: [%s],\n", strings.Join(modes, ", "))
	}
	f.printf("};\n\n")
	var catalogs []string
	for _, c := range g.b.GetCatalogs() {
		catalogs = append(catalogs, jsString(c.GetName().GetStored()))
	}
	f.printf("/** catalogs are the catalogs this package reads, each bound to a source in the executor's configuration (§22.1). */\nexport const catalogs: readonly string[] = [%s];\n\n", strings.Join(catalogs, ", "))
	f.printf(`/** newExecutor opens an executor running this package's statements on driver, with config binding every catalog to a source (§22.1). */
export function newExecutor(driver: %s, config: %s, options?: %s): Promise<%s> {
  return %s.open(driver, dialect, config, options);
}
`, f.rt("Driver", false), f.rt("Config", false), f.rt("ExecutorOptions", false), f.rt("Executor", true), f.rt("Executor", true))
	if g.b.GetQueries().GetFragments() != nil {
		g.fragmentTable()
	}
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

// fragmentTable generates dql-fragments.dql.ts: the dialect plugin's
// fragment table, which builder queries are assembled from (§16.2). It is
// the package's own: the index leaves it out.
func (g *generator) fragmentTable() {
	f := g.newFile("dql-fragments")
	f.internal = true
	t := g.b.GetQueries().GetFragments()
	g.fragmentIDs = map[string]bool{}
	g.declare(f, "fragments", "the fragment table", nil)
	f.printf("/** fragments is the fragment table of %s, rendered by its dialect plugin (§16.2, §18.4). */\nexport const fragments: %s = {\n", dialectName(g.b.GetDialect()), f.rt("Fragments", false))
	p, ok := placeholders[t.GetPlaceholder()]
	if !ok {
		// A style the runtime does not know is left to its default.
		p = "dollar"
	}
	f.printf("  placeholder: %s,\n  templates: {\n", jsString(p))
	for _, fr := range t.GetFragments() {
		g.fragmentIDs[fr.GetId()] = true
		f.printf("    %s: %s,\n", jsString(fr.GetId()), jsString(fr.GetTemplate()))
	}
	f.printf("  },\n};\n")
}

// consistency is the TypeScript value of a query's default consistency
// (§22.5): "" for the runtime's default when unset, the primary at a level
// the dialect lacks.
func (g *generator) consistency(f *file, c *irv1.Consistency) string {
	l := c.GetLevel()
	if l == irv1.ConsistencyLevel_CONSISTENCY_UNSPECIFIED {
		return ""
	}
	cons := f.rt("Consistency", true)
	if !slices.Contains(g.b.GetCapabilities().GetConsistencyLevels(), l) {
		return cons + ".primary"
	}
	switch l {
	case irv1.ConsistencyLevel_CONSISTENCY_ANY:
		return cons + ".any"
	case irv1.ConsistencyLevel_CONSISTENCY_BOUNDED_STALENESS:
		return fmt.Sprintf("%s.bounded(%d)", cons, c.GetMaxStaleness().AsDuration()/time.Millisecond)
	case irv1.ConsistencyLevel_CONSISTENCY_READ_YOUR_WRITES:
		return cons + ".readYourWrites"
	}
	return cons + ".primary"
}

// dbType is the database type a value of t is sent as, written
// namespace.name as the database names it, such as pg_catalog.int4: a
// domain's base type, or, for a list of t, its array type. A runtime
// encodes arguments by it where the server does not describe the
// statement (§26.4). It is "" where the bundle does not say.
func (g *generator) dbType(t *irv1.Type, list bool) string {
	for t.GetNamed().GetDomain() != nil {
		t = g.named[keyOf(t.GetNamed().GetDomain())].base
	}
	if a := t.GetArray(); a != nil {
		if list {
			return ""
		}
		if a.GetElement().GetNamed().GetDomain() != nil {
			return g.dbType(a.GetElement(), true)
		}
	}
	n := t.GetNative().GetName()
	name := n.GetObject()
	if name == "" {
		return ""
	}
	if list {
		name = "_" + name
	}
	if ns := n.GetNamespace(); ns != "" {
		name = ns + "." + name
	}
	return name
}
