// Package facts derives the facts the core computes over a catalog IR that a
// source produced: whether each uniqueness fact identifies rows, entity keys
// and logical keys (design record §11.1, §11.2), relations with their kinds,
// nullability, policies and names (§12, §4.5), and the host names of routine
// overloads (§4.6).
//
// Identifiers arrive with their words already split (§4.2), and annotations
// arrive merged, one effective annotation per key (§7.1).
package facts

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Options carries what Derive needs from outside the catalogs.
type Options struct {
	// Dialect is the bundle's dialect, named in diagnostics.
	Dialect irv1.Dialect
	// CrossCatalogReferences is the dialect capability that allows a
	// reference to cross catalogs (§5.2).
	CrossCatalogReferences bool
	// DefaultCatalog is the catalog whose objects get no catalog prefix when
	// the bundle has several catalogs (§4.3).
	DefaultCatalog string
}

// Diagnostic codes. They are stable: tools and tests match on them.
const (
	CodeLogicalKeyWithPrimaryKey   = "logical-key-with-primary-key"
	CodeLogicalKeyUnknownColumn    = "logical-key-unknown-column"
	CodeLogicalKeyNullable         = "logical-key-nullable"
	CodeLogicalKeyNotUnique        = "logical-key-not-unique"
	CodeAnnotationValueInvalid     = "annotation-value-invalid"
	CodeRelationAnnotationInvalid  = "relation-annotation-invalid"
	CodeRelationThroughUnsupported = "relation-through-unsupported"
	CodeRelationTargetMissing      = "relation-target-missing"
	CodeRelationCrossCatalog       = "relation-cross-catalog"
	CodeRemovalUnlinkNotNullable   = "removal-unlink-not-nullable"
	CodeColumnNameCollision        = "column-name-collision"
	CodeRelationNameCollision      = "relation-name-collision"
	CodeRelationNameEqualsColumn   = "relation-name-equals-column"
	CodeOverloadNameCollision      = "overload-name-collision"
)

// Derive computes every fact of this package in place over catalogs and
// returns the diagnostics, sorted (§3.2). Any error diagnostic fails
// generation.
//
// Derive fills Uniqueness.identifies, Table.logical_key from a logical-key
// annotation, Table.relations, and Overload.signature and host_name. It sorts
// tables and routines by stored name, and uniqueness facts, foreign keys,
// relations and overloads as §3.2 requires, so its output does not depend on
// the order of its input.
func Derive(catalogs []*irv1.Catalog, opts Options) []*irv1.Diagnostic {
	d := &deriver{opts: opts, tables: map[string]*tableRef{}, named: map[string][]string{}}
	d.index(catalogs)
	d.keys()
	d.relations()
	d.overloads(catalogs)
	SortDiagnostics(d.diags)
	return d.diags
}

// deriver holds the state of one Derive call.
type deriver struct {
	opts   Options
	diags  []*irv1.Diagnostic
	order  []*tableRef           // every table, in qualified-name order
	tables map[string]*tableRef  // by qnKey
	named  map[string][]string   // enum, domain and composite words by qnKey
	rels   map[string][]*pending // relations by owner qnKey
}

// tableRef is a table together with where it lives.
type tableRef struct {
	catalog *irv1.Catalog
	qn      *irv1.QualifiedName
	t       *irv1.Table
}

func (d *deriver) index(catalogs []*irv1.Catalog) {
	for _, c := range catalogs {
		for _, ns := range c.GetNamespaces() {
			slices.SortFunc(ns.Tables, func(a, b *irv1.Table) int {
				return strings.Compare(a.GetName().GetStored(), b.GetName().GetStored())
			})
			slices.SortFunc(ns.Routines, func(a, b *irv1.Routine) int {
				return strings.Compare(a.GetName().GetStored(), b.GetName().GetStored())
			})
			for _, t := range ns.GetTables() {
				r := &tableRef{catalog: c, t: t, qn: &irv1.QualifiedName{
					Catalog: c.GetName().GetStored(), Namespace: ns.GetName().GetStored(), Object: t.GetName().GetStored(),
				}}
				d.tables[qnKey(r.qn)] = r
				d.order = append(d.order, r)
			}
			for _, e := range ns.GetEnums() {
				d.addNamed(c, ns, e.GetName())
			}
			for _, e := range ns.GetDomains() {
				d.addNamed(c, ns, e.GetName())
			}
			for _, e := range ns.GetComposites() {
				d.addNamed(c, ns, e.GetName())
			}
		}
	}
	slices.SortFunc(d.order, func(a, b *tableRef) int { return compareQN(a.qn, b.qn) })
}

func (d *deriver) addNamed(c *irv1.Catalog, ns *irv1.Namespace, name *irv1.Identifier) {
	qn := &irv1.QualifiedName{Catalog: c.GetName().GetStored(), Namespace: ns.GetName().GetStored(), Object: name.GetStored()}
	d.named[qnKey(qn)] = name.GetWords()
}

func (d *deriver) lookup(qn *irv1.QualifiedName) *tableRef {
	return d.tables[qnKey(&irv1.QualifiedName{Catalog: qn.GetCatalog(), Namespace: qn.GetNamespace(), Object: qn.GetObject()})]
}

func (d *deriver) errorf(code string, target *irv1.QualifiedName, format string, args ...any) {
	d.add(irv1.Severity_SEVERITY_ERROR, code, target, format, args...)
}

func (d *deriver) warnf(code string, target *irv1.QualifiedName, format string, args ...any) {
	d.add(irv1.Severity_SEVERITY_WARNING, code, target, format, args...)
}

func (d *deriver) add(sev irv1.Severity, code string, target *irv1.QualifiedName, format string, args ...any) {
	d.diags = append(d.diags, &irv1.Diagnostic{
		Severity: sev, Code: code, Target: target, Message: fmt.Sprintf(format, args...),
	})
}

// SortDiagnostics sorts diagnostics by severity, then qualified name, then
// code (§3.2), and by message to make the order total.
func SortDiagnostics(ds []*irv1.Diagnostic) {
	slices.SortStableFunc(ds, func(a, b *irv1.Diagnostic) int {
		return cmp.Or(
			cmp.Compare(a.GetSeverity(), b.GetSeverity()),
			compareQN(a.GetTarget(), b.GetTarget()),
			strings.Compare(a.GetCode(), b.GetCode()),
			strings.Compare(a.GetMessage(), b.GetMessage()),
		)
	})
}

// member returns qn with its member replaced.
func member(qn *irv1.QualifiedName, m string) *irv1.QualifiedName {
	return &irv1.QualifiedName{Catalog: qn.GetCatalog(), Namespace: qn.GetNamespace(), Object: qn.GetObject(), Member: m}
}

// qnKey is a map key for a qualified name. NUL cannot occur in an identifier
// the IR carries, so the key is unambiguous.
func qnKey(qn *irv1.QualifiedName) string {
	return qn.GetCatalog() + "\x00" + qn.GetNamespace() + "\x00" + qn.GetObject() + "\x00" + qn.GetMember()
}

func compareQN(a, b *irv1.QualifiedName) int {
	return cmp.Or(
		strings.Compare(a.GetCatalog(), b.GetCatalog()),
		strings.Compare(a.GetNamespace(), b.GetNamespace()),
		strings.Compare(a.GetObject(), b.GetObject()),
		strings.Compare(a.GetMember(), b.GetMember()),
	)
}

// dotted writes a qualified name for messages, leaving out an implicit
// namespace (§5.1).
func dotted(qn *irv1.QualifiedName) string {
	var parts []string
	for _, p := range []string{qn.GetCatalog(), qn.GetNamespace(), qn.GetObject(), qn.GetMember()} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, ".")
}

// dialectName is the dialect as diagnostics name it: DIALECT_POSTGRES is
// "postgres".
func dialectName(d irv1.Dialect) string {
	return strings.ToLower(strings.TrimPrefix(d.String(), "DIALECT_"))
}

// identifier builds the identifier of a computed name: its words, stored
// joined by underscores as in §12.4 ("posts_to_author").
func identifier(words []string) *irv1.Identifier {
	return &irv1.Identifier{Stored: strings.Join(words, "_"), Words: words}
}

// wordsKey is a map key for a word list (§4.3: names collide when their word
// lists are equal).
func wordsKey(words []string) string { return strings.Join(words, "\x00") }

// columnWords is a column's words as its host field uses them: after the
// name annotation when naming has computed them (§4.4).
func columnWords(c *irv1.Column) []string {
	if w := c.GetHostName().GetWords(); len(w) > 0 {
		return w
	}
	return c.GetName().GetWords()
}

func column(t *irv1.Table, name string) *irv1.Column {
	for _, c := range t.GetColumns() {
		if c.GetName().GetStored() == name {
			return c
		}
	}
	return nil
}
