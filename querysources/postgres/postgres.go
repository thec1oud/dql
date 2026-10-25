// Package postgres is the Postgres query-source plugin: it parses query
// files with Postgres's own grammar (libpg_query) and types each query
// against the bundle's catalogs (design record §16.1, §17). It runs
// in-process through the plugin.Plugin interface, and as the executable
// dql-plugin-pg-queries (ADR 0002).
//
// A query file holds queries in sqlc's conventions (§17.1). For each, the
// plugin rewrites its parameters to $n placeholders, infers their types from
// where they are used, and types its result columns, which are non-null
// only through the proofs of implementation guide §5.4. The core resolves
// -- returns:, checks scopes and write-only columns, and proves
// cardinalities (§16–21).
package postgres

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	pg "github.com/pganalyze/pg_query_go/v6"
	pgparser "github.com/pganalyze/pg_query_go/v6/parser"

	"github.com/thec1oud/dql/core/diagnostics"
	"github.com/thec1oud/dql/core/naming"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Name is the plugin's name.
const Name = "pg-queries"

// Version is the plugin's version.
const Version = "1.0.0"

// Plugin types the query files of every invocation.
type Plugin struct{}

// New returns the Postgres query-source plugin.
func New() *Plugin { return &Plugin{} }

// Handshake declares a query-source plugin reading IR 1.x.
func (*Plugin) Handshake() *irv1.PluginHandshake {
	return &irv1.PluginHandshake{
		Plugin:     &irv1.PluginInfo{Name: Name, Kind: irv1.PluginKind_PLUGIN_KIND_QUERY_SOURCE, Version: Version},
		IrVersions: &irv1.IrVersionRange{Min: "1.0.0", MaxMajor: 1},
	}
}

// Invoke returns a bundle holding the queries of the invocation's query
// files, sorted by name, and a diagnostic for every query it could not
// type. It refuses a bundle of another dialect.
func (*Plugin) Invoke(_ context.Context, in *irv1.Invoke) (*irv1.InvokeResult, error) {
	b := in.GetBundle()
	if d := b.GetDialect(); d != irv1.Dialect_DIALECT_POSTGRES {
		return nil, fmt.Errorf("pg-queries: the bundle's catalogs are %s, not Postgres", d)
	}
	ix := newIndex(b)
	var (
		qs   []*irv1.Query
		ds   []*irv1.Diagnostic
		seen = map[string]string{}
	)
	for _, f := range in.GetQueryFiles() {
		blocks, fds := split(f)
		ds = append(ds, fds...)
		for _, blk := range blocks {
			if at, dup := seen[blk.name]; dup {
				ds = append(ds, fileError("query-duplicate", blk.name, "%s: query %s is also defined at %s; give each query its own name", blk.location, blk.name, at))
				continue
			}
			seen[blk.name] = blk.location
			q, d := typeQuery(ix, f.GetPath(), blk)
			if d != nil {
				ds = append(ds, d)
				continue
			}
			qs = append(qs, q)
		}
	}
	slices.SortFunc(qs, func(a, b *irv1.Query) int { return strings.Compare(a.GetName().GetStored(), b.GetName().GetStored()) })
	diagnostics.Sort(ds)
	return &irv1.InvokeResult{Bundle: &irv1.Bundle{Queries: &irv1.QueryLayer{Queries: qs}}, Diagnostics: ds}, nil
}

// typeQuery parses and types one query (§17.2).
func typeQuery(ix *index, path string, blk *block) (*irv1.Query, *irv1.Diagnostic) {
	fail := func(code string, off int, format string, args ...any) (*irv1.Query, *irv1.Diagnostic) {
		return nil, fileError(code, blk.name, "%s: query %s: %s", blk.at(path, off), blk.name, fmt.Sprintf(format, args...))
	}
	r, err := rewrite(blk.body)
	if err != nil {
		return fail("query-param", -1, "%v", err)
	}
	tree, err := pg.Parse(r.text)
	switch {
	case err != nil:
		return fail("query-syntax", r.original(syntaxOffset(err)), "%v", err)
	case len(tree.GetStmts()) != 1:
		return fail("query-statements", -1, "holds %d statements; a query is one statement", len(tree.GetStmts()))
	}
	stmt := tree.GetStmts()[0]
	a := &analyzer{ix: ix, catalogs: map[string]bool{}}
	for _, p := range r.params {
		a.params = append(a.params, &param{raw: p})
	}
	cols, err := a.statement(stmt.GetStmt(), nil)
	if err != nil {
		var qe *queryError
		errors.As(err, &qe)
		return fail(qe.code, r.original(qe.off), "%s", qe.msg)
	}
	q := &irv1.Query{
		Name:        &irv1.Identifier{Stored: blk.name, Words: naming.Words(blk.name)},
		Source:      irv1.QuerySource_QUERY_SOURCE_FILE,
		Cardinality: blk.cardinality,
		Consistency: blk.consistency,
		Writes:      a.writes,
		Location:    blk.location,
		Returns:     blk.returns,
		Sql:         &irv1.Query_Text{Text: statementText(r.text, stmt)},
	}
	if len(a.catalogs) > 1 {
		return fail("query-cross-catalog", -1, "reads the catalogs %s; a query runs on one catalog's source", strings.Join(slices.Sorted(maps.Keys(a.catalogs)), " and "))
	}
	for c := range a.catalogs {
		q.Catalog = c
	}
	scope := &irv1.ScopeUse{Unscoped: blk.unscoped}
	for _, p := range a.params {
		if p.raw.kind == scopeParam {
			scope.Columns = append(scope.Columns, p.raw.name)
			continue
		}
		if p.typ == nil {
			return fail("query-param-untyped", p.raw.start, "the type of parameter %s cannot be inferred; cast it where it is used, such as @%s::bigint", p.raw.name, p.raw.name)
		}
		q.Params = append(q.Params, &irv1.Param{Name: p.raw.name, Type: p.typ,
			Nullable: p.raw.kind == nargParam, List: p.raw.kind == sliceParam})
	}
	if len(scope.Columns) > 0 || scope.Unscoped {
		q.Scope = scope
	}
	if blk.cardinality == irv1.Cardinality_CARDINALITY_EXEC || blk.cardinality == irv1.Cardinality_CARDINALITY_EXEC_ROWS {
		return q, nil
	}
	if cols == nil {
		return fail("query-no-rows", -1, "returns no rows, but its cardinality needs them; add RETURNING or use :exec")
	}
	fields, qe := resultFields(cols, blk)
	if qe != nil {
		return fail(qe.code, r.original(qe.off), "%s", qe.msg)
	}
	q.Result = &irv1.ResultShape{Kind: &irv1.ResultShape_Anonymous{Anonymous: &irv1.AnonymousShape{Fields: fields}}}
	return q, nil
}

// resultFields turns result columns into fields and applies -- nonnull:.
func resultFields(cols []column, blk *block) ([]*irv1.ResultField, *queryError) {
	var fields []*irv1.ResultField
	byName := map[string]*irv1.ResultField{}
	for _, c := range cols {
		if c.name == "" {
			return nil, &queryError{code: "query-column-name", off: int(c.loc), msg: "a result column has no name; give it one with AS"}
		}
		v := settle(c.v)
		if v.typ == nil {
			return nil, &queryError{code: "query-param-untyped", off: int(c.loc), msg: fmt.Sprintf("result column %s has no type; cast it", c.name)}
		}
		if byName[c.name] != nil {
			return nil, &queryError{code: "query-column-duplicate", off: int(c.loc), msg: fmt.Sprintf("two result columns are named %s; rename one with AS", c.name)}
		}
		f := &irv1.ResultField{Name: c.name, Type: v.typ, Nullable: v.nullable, Origin: v.origin}
		switch {
		case v.nullable:
			f.NullabilitySource = irv1.NullabilitySource_NULLABILITY_SOURCE_UNPROVEN
		case v.annotated:
			f.NullabilitySource = irv1.NullabilitySource_NULLABILITY_SOURCE_ANNOTATION
		default:
			f.NullabilitySource = irv1.NullabilitySource_NULLABILITY_SOURCE_PROVEN
		}
		byName[c.name] = f
		fields = append(fields, f)
	}
	for _, n := range blk.nonnull {
		f := byName[n]
		if f == nil {
			return nil, &queryError{code: "query-nonnull-unknown", off: -1, msg: fmt.Sprintf("-- nonnull: names %s, which is not a result column", n)}
		}
		if f.Nullable {
			// Decoding enforces what -- nonnull: asserts (§13, §17.2).
			f.Nullable, f.NullabilitySource = false, irv1.NullabilitySource_NULLABILITY_SOURCE_ANNOTATION
		}
	}
	return fields, nil
}

// statementText returns the statement's text as Postgres runs it, with its
// placeholders.
func statementText(text string, stmt *pg.RawStmt) string {
	start := int(stmt.GetStmtLocation())
	end := len(text)
	if l := int(stmt.GetStmtLen()); l > 0 {
		end = start + l
	}
	return strings.TrimSpace(text[start:end])
}

// syntaxOffset returns where the parser found a syntax error, or -1.
func syntaxOffset(err error) int {
	var perr *pgparser.Error
	if errors.As(err, &perr) {
		return perr.Cursorpos - 1
	}
	return -1
}

// at returns where offset off of the statement is, as path:line:column,
// or the -- name: line for -1.
func (b *block) at(path string, off int) string {
	if off < 0 {
		return b.location
	}
	off = min(off, len(b.body))
	before := b.body[:off]
	line := b.line + strings.Count(before, "\n")
	col := off - strings.LastIndexByte(before, '\n')
	return fmt.Sprintf("%s:%d:%d", path, line, col)
}
