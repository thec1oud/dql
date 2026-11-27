// Package catalog assembles the catalog layer of a bundle from what source
// plugins produced (design record §2.2, §5, §6): it merges the catalogs of
// every source invocation into one bundle of one dialect, computes the words
// of every identifier and the host names of §4, attaches config annotations
// to their targets and merges them over comment annotations (§7.1), applies
// the annotations that narrow catalog facts (§6.4, §8.5, §8.6, §13), and runs
// the core's checks and derivations over the result.
//
// Extensions and plugin annotations pass through untouched (§6.3, §7.2), and
// the output does not depend on the order of the input (§3.2).
//
// Assembly lives here rather than in core/bundle because core/bundle is the
// codec that the other core packages test against; it cannot import them.
package catalog

import (
	"slices"

	"google.golang.org/protobuf/proto"

	"github.com/thec1oud/dql/core/bundle"
	"github.com/thec1oud/dql/core/constraints"
	"github.com/thec1oud/dql/core/diagnostics"
	"github.com/thec1oud/dql/core/facts"
	"github.com/thec1oud/dql/core/types"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// SourceOutput is what one source invocation produced: the catalog layer
// (§2.2), with stored names only, and the config annotations its source
// plugin read (§1.3, §7.1).
type SourceOutput struct {
	// Dialect is the dialect of every catalog in Catalogs.
	Dialect irv1.Dialect
	// Catalogs carry stored names; words and host names are the core's to
	// compute (§4.2). Annotations the source read from comments sit on their
	// objects with ORIGIN_DB_COMMENT.
	Catalogs []*irv1.Catalog
	// Config holds the config annotations, with ORIGIN_CONFIG, keyed by the
	// qualified name of their target as dql.yaml writes it: its non-empty
	// parts joined by dots, such as shop.app.users,
	// shop.app.users.password_hash or shop.app.money_amount.amount (§1.3).
	// An overload is shop.app.post_score(int64, int32): its routine and its
	// signature (§4.6); the routine's name alone names it when it is the only
	// overload. The implicit namespace of MySQL and SQLite is left out (§5.3).
	Config map[string][]*irv1.Annotation
	// Diagnostics the source plugin reported; they are carried into the
	// bundle's diagnostics.
	Diagnostics []*irv1.Diagnostic
}

// Options carries what Assemble needs from outside the sources.
type Options struct {
	// CrossCatalogReferences is the dialect capability that lets a reference
	// cross catalogs (§5.2).
	CrossCatalogReferences bool
	// DefaultCatalog is the project's default-catalog setting: the catalog
	// whose objects get no catalog prefix when the bundle has several (§4.3).
	DefaultCatalog string
	// OpaqueMapped lists the native types that type plugins and emitter
	// config map (§8.3).
	OpaqueMapped []*irv1.QualifiedName
	// OpaqueFallback is the project's opaque-fallback setting: "", "string"
	// or "bytes" (§8.3).
	OpaqueFallback string
}

// Diagnostic codes. They are stable: tools and tests match on them
// (implementation guide §7 rule 13).
const (
	// CodeNoCatalogs: the sources produced no catalog, so the bundle has no
	// dialect (§5.2).
	CodeNoCatalogs = "catalog-none"
	// CodeDialectMissing: a source produced catalogs without naming their
	// dialect.
	CodeDialectMissing = "catalog-dialect-missing"
	// CodeDialectMixed: catalogs of more than one dialect (§5.2).
	CodeDialectMixed = "catalog-dialect-mixed"
	// CodeNameMissing: a catalog without a name.
	CodeNameMissing = "catalog-name-missing"
	// CodeNameDuplicate: two catalogs with one name (§5.2).
	CodeNameDuplicate = "catalog-name-duplicate"
	// CodeDefaultCatalogUnknown: default-catalog names no catalog of the
	// bundle (§4.3).
	CodeDefaultCatalogUnknown = "default-catalog-unknown"
	// CodeAnnotationTargetUnknown: a config annotation key names no object
	// (§1.3, §7.1).
	CodeAnnotationTargetUnknown = "annotation-target-unknown"
	// CodeAnnotationTargetAmbiguous: a config annotation key names more than
	// one object.
	CodeAnnotationTargetAmbiguous = "annotation-target-ambiguous"
	// CodeHostNameCollision: objects that become host types in the one
	// generated package have equal words (§4.3, §5.2).
	CodeHostNameCollision = "host-name-collision"
	// CodeColumnNameCollision: fields of one host struct have equal words
	// (§4.3). core/facts reports it for the columns of tables and views; this
	// package for composite attributes and result columns.
	CodeColumnNameCollision = facts.CodeColumnNameCollision
	// CodeDependencyCrossCatalog: a view depends on another catalog's object,
	// and the dialect cannot reference across catalogs (§5.2).
	CodeDependencyCrossCatalog = "dependency-cross-catalog"
	// CodeEnumLabelUnnamed: an enum label with no words, from which no
	// emitter can name a constant (§4.2, §8.4).
	CodeEnumLabelUnnamed = "enum-label-unnamed"
	// CodeEnumLabelCollision: labels of one enum have equal words (§4.3).
	CodeEnumLabelCollision = "enum-label-collision"
)

// Assemble builds the bundle's catalog layer from the outputs of every
// source invocation and returns it with every diagnostic, sorted (§3.2 rule
// 5). Any error diagnostic fails generation, and the bundle is then nil.
// Otherwise the bundle carries the warnings in its diagnostics (§3.1).
//
// The inputs are not modified. In order, Assemble:
//
//  1. merges the catalogs into one bundle: one dialect, unique catalog names
//     (§5.2);
//  2. computes the words of every identifier, once (§4.2);
//  3. attaches each config annotation to the object its key names, reads
//     the values sources wrote as text by their key's form (§7.4) and merges
//     config over the comment annotations, config first (§7.1);
//  4. applies nonnull (§6.4, §8.5, §13) and array-elements-nonnull (§8.6),
//     then checks every target's annotations (§7.3);
//  5. computes host names with their prefixes (§4.3, §4.4) and checks host
//     types and struct fields for collisions;
//  6. checks view dependencies across catalogs (§5.2);
//  7. derives keys, logical keys, relations and overload names with
//     core/facts, unless an annotation failed its check, since those
//     derivations read the annotations;
//  8. checks types, opaque mappings and constraints (§8, §10), and sets the
//     version, dialect and required features (§3.3).
func Assemble(inputs []SourceOutput, opts Options) (*irv1.Bundle, []*irv1.Diagnostic) {
	a := &assembler{opts: opts}
	dialect, cats, config := a.merge(inputs)
	if failed(a.diags) {
		return nil, a.finish()
	}
	a.dialect = dialect
	a.defaultCatalog(cats)
	for _, c := range cats {
		fillWords(c.ProtoReflect())
	}
	b := &irv1.Bundle{Version: bundle.Version(), Dialect: dialect, Catalogs: cats}
	before := len(a.diags)
	a.annotate(b, config)
	annotationsFailed := failed(a.diags[before:])
	a.hostNames(cats)
	a.crossCatalog(cats)
	sortCatalogs(cats)
	if !annotationsFailed {
		a.add(facts.Derive(cats, facts.Options{
			Dialect: dialect, CrossCatalogReferences: opts.CrossCatalogReferences, DefaultCatalog: opts.DefaultCatalog,
		})...)
	}
	a.add(types.Check(b)...)
	a.add(types.CheckOpaque(b, opts.OpaqueMapped, opts.OpaqueFallback)...)
	a.add(constraints.Check(b)...)
	ds := a.finish()
	if failed(ds) {
		return nil, ds
	}
	b.RequiredFeatures = types.RequiredFeatures(b)
	b.Diagnostics = ds
	return b, ds
}

// assembler holds the state of one Assemble call.
type assembler struct {
	opts    Options
	dialect irv1.Dialect
	diags   []*irv1.Diagnostic
}

func (a *assembler) add(ds ...*irv1.Diagnostic) { a.diags = append(a.diags, ds...) }

func (a *assembler) errorf(code string, target *irv1.QualifiedName, format string, args ...any) {
	a.add(diagnostics.Errorf(code, target, format, args...))
}

func (a *assembler) finish() []*irv1.Diagnostic {
	diagnostics.Sort(a.diags)
	return a.diags
}

func failed(ds []*irv1.Diagnostic) bool {
	return slices.ContainsFunc(ds, func(d *irv1.Diagnostic) bool { return d.GetSeverity() == irv1.Severity_SEVERITY_ERROR })
}

// merge checks the inputs against §5.2 and returns the bundle's dialect, a
// copy of every catalog, and a copy of every config annotation by key.
func (a *assembler) merge(inputs []SourceOutput) (irv1.Dialect, []*irv1.Catalog, map[string][]*irv1.Annotation) {
	type entry struct {
		c       *irv1.Catalog
		dialect irv1.Dialect
	}
	var all []entry
	config := map[string][]*irv1.Annotation{}
	for _, in := range inputs {
		for _, c := range in.Catalogs {
			all = append(all, entry{proto.CloneOf(c), in.Dialect})
		}
		for k, anns := range in.Config {
			for _, ann := range anns {
				config[k] = append(config[k], proto.CloneOf(ann))
			}
		}
		for _, d := range in.Diagnostics {
			a.add(proto.CloneOf(d))
		}
	}
	if len(all) == 0 {
		a.errorf(CodeNoCatalogs, &irv1.QualifiedName{},
			"the sources produced no catalog, so the bundle has no dialect (§5.2); check each source's connection and namespaces")
		return irv1.Dialect_DIALECT_UNSPECIFIED, nil, nil
	}

	dialects := map[irv1.Dialect][]string{}
	byName := map[string][]*irv1.Catalog{}
	var cats []*irv1.Catalog
	for _, e := range all {
		name := e.c.GetName().GetStored()
		at := &irv1.QualifiedName{Catalog: name}
		switch {
		case name == "":
			a.errorf(CodeNameMissing, at, "source %s produced a catalog without a name; the source plugin must name every catalog it produces", source(e.c))
		case e.dialect == irv1.Dialect_DIALECT_UNSPECIFIED:
			a.errorf(CodeDialectMissing, at, "source %s produced catalog %s without naming its dialect; the source plugin must set the bundle's dialect", source(e.c), name)
		default:
			dialects[e.dialect] = append(dialects[e.dialect], name)
		}
		byName[name] = append(byName[name], e.c)
		cats = append(cats, e.c)
	}
	sortCatalogs(cats)

	var dialect irv1.Dialect
	for d := range dialects {
		dialect = d
	}
	if len(dialects) > 1 {
		// §5.2: one query runs against one server, so a bundle has one dialect.
		var used []string
		for _, d := range sortedDialects(dialects) {
			slices.Sort(dialects[d])
			used = append(used, dialectName(d)+" ("+joinNames(dialects[d])+")")
		}
		for _, d := range sortedDialects(dialects) {
			for _, name := range dialects[d] {
				a.errorf(CodeDialectMixed, &irv1.QualifiedName{Catalog: name},
					"catalog %s is %s, but the bundle mixes dialects %s, and all catalogs of a bundle share one dialect (§5.2); generate each dialect's catalogs in a separate project",
					name, dialectName(d), joinList(used))
			}
		}
	}
	reported := map[string]bool{}
	for _, c := range cats {
		name := c.GetName().GetStored()
		if group := byName[name]; name != "" && len(group) > 1 && !reported[name] {
			reported[name] = true
			var from []string
			for _, g := range group {
				from = append(from, source(g))
			}
			slices.Sort(from)
			a.errorf(CodeNameDuplicate, &irv1.QualifiedName{Catalog: name},
				"%d catalogs are named %s, from sources %s, and catalog names are unique within a bundle (§5.2); give each source a distinct catalog name",
				len(group), name, joinList(from))
		}
	}
	return dialect, cats, config
}

// defaultCatalog checks that default-catalog names a catalog of the bundle.
func (a *assembler) defaultCatalog(cats []*irv1.Catalog) {
	name := a.opts.DefaultCatalog
	if name == "" || slices.ContainsFunc(cats, func(c *irv1.Catalog) bool { return c.GetName().GetStored() == name }) {
		return
	}
	var names []string
	for _, c := range cats {
		names = append(names, c.GetName().GetStored())
	}
	a.errorf(CodeDefaultCatalogUnknown, &irv1.QualifiedName{Catalog: name},
		"default-catalog is %s, which is not a catalog of the bundle (§4.3); set it to one of %s", name, joinList(names))
}

// crossCatalog fails a view dependency on another catalog's object when the
// dialect cannot reference across catalogs (§5.2). Relations are checked by
// core/facts.
func (a *assembler) crossCatalog(cats []*irv1.Catalog) {
	if a.opts.CrossCatalogReferences {
		return
	}
	for _, c := range cats {
		for _, ns := range c.GetNamespaces() {
			for _, t := range ns.GetTables() {
				at := &irv1.QualifiedName{Catalog: c.GetName().GetStored(), Namespace: ns.GetName().GetStored(), Object: t.GetName().GetStored()}
				for _, dep := range t.GetDependsOn() {
					if dep.GetCatalog() != "" && dep.GetCatalog() != at.GetCatalog() {
						a.errorf(CodeDependencyCrossCatalog, at,
							"view %s depends on %s in another catalog, and dialect %s cannot reference across catalogs (§5.2); keep the view and what it reads in one catalog",
							diagnostics.Name(at), diagnostics.Name(dep), dialectName(a.dialect))
					}
				}
			}
		}
	}
}

func source(c *irv1.Catalog) string {
	if s := c.GetProvenance().GetSource(); s != "" {
		return s
	}
	return "(unnamed)"
}
