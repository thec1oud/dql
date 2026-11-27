// Package sqlserver is the SQL Server query-source plugin: it parses query
// files with the ANTLR grammars-v4 T-SQL grammar of package grammar, and
// types each query against the bundle's catalogs (design record §16.1,
// §17). It runs in-process through the plugin.Plugin interface, and as the
// executable dql-plugin-sqlserver-queries (ADR 0002).
//
// A query file holds queries in sqlc's conventions (§17.1). For each, the
// plugin rewrites its parameters to @p1, @p2 and on, numbered by the order
// of the query's parameters, then its scope columns: the names the runtime
// gives its arguments, so a parameter used twice is one argument. A list,
// IN (sqlc.slice(name)), is one JSON array, which the statement reads with
// OPENJSON as the compared column's type (§25.3). Parameter and result
// types follow the other plugins' rules; result columns are non-null only
// through the proofs of implementation guide §5.4.
//
// A query names each table, view and function with its schema, as in
// app.users: SQL Server finds an unqualified name in the login's default
// schema, which a query file cannot know. A write returns rows with
// OUTPUT, so it may be :one or :many. SQL Server refuses OUTPUT without
// INTO on a table with an enabled trigger; such a write is :exec or
// :execrows.
package sqlserver

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/thec1oud/dql/core/diagnostics"
	"github.com/thec1oud/dql/core/naming"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/querysources/queryfile"
)

// Name is the plugin's name.
const Name = "sqlserver-queries"

// Version is the plugin's version.
const Version = "1.0.0"

// Plugin types the query files of every invocation.
type Plugin struct{}

// New returns the SQL Server query-source plugin.
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
	if d := b.GetDialect(); d != irv1.Dialect_DIALECT_SQLSERVER {
		return nil, fmt.Errorf("sqlserver-queries: the bundle's catalogs are %s, not SQL Server", d)
	}
	ix := newIndex(b)
	var (
		qs   []*irv1.Query
		ds   []*irv1.Diagnostic
		seen = map[string]string{}
	)
	for _, f := range in.GetQueryFiles() {
		blocks, fds := queryfile.Split(f)
		ds = append(ds, fds...)
		for _, blk := range blocks {
			if at, dup := seen[blk.Name]; dup {
				ds = append(ds, queryfile.Error("query-duplicate", blk.Name, "%s: query %s is also defined at %s; give each query its own name", blk.Location, blk.Name, at))
				continue
			}
			seen[blk.Name] = blk.Location
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
func typeQuery(ix *index, path string, blk *queryfile.Block) (*irv1.Query, *irv1.Diagnostic) {
	fail := func(code string, off int, format string, args ...any) (*irv1.Query, *irv1.Diagnostic) {
		return nil, queryfile.Error(code, blk.Name, "%s: query %s: %s", blk.At(path, off), blk.Name, fmt.Sprintf(format, args...))
	}
	r, err := rewrite(blk.Body)
	if err != nil {
		return fail("query-param", -1, "%v", err)
	}
	stmts, err := parse(r)
	switch {
	case err != nil:
		off := r.original(err.(*syntaxError).off)
		return fail("query-syntax", off, "%s", syntaxMessage(blk.Body, off))
	case len(stmts) != 1:
		return fail("query-statements", -1, "holds %d statements; a query is one statement", len(stmts))
	}
	a := &analyzer{ix: ix, r: r, lists: map[int]string{}, catalogs: map[string]bool{}, tables: map[string]*irv1.QualifiedName{}}
	for _, p := range r.params {
		a.params = append(a.params, &param{raw: p})
	}
	cols, err := a.statement(stmts[0], nil)
	if err != nil {
		var qe *queryError
		errors.As(err, &qe)
		return fail(qe.code, r.original(qe.off), "%s", qe.msg)
	}
	q := &irv1.Query{
		Name:        &irv1.Identifier{Stored: blk.Name, Words: naming.Words(blk.Name)},
		Source:      irv1.QuerySource_QUERY_SOURCE_FILE,
		Cardinality: blk.Cardinality,
		Consistency: blk.Consistency,
		Writes:      a.writes,
		Location:    blk.Location,
		Returns:     blk.Returns,
		Sql:         &irv1.Query_Text{Text: a.text()},
	}
	if len(a.catalogs) > 1 {
		return fail("query-cross-catalog", -1, "reads the catalogs %s; a query runs on one catalog's source", strings.Join(slices.Sorted(maps.Keys(a.catalogs)), " and "))
	}
	for c := range a.catalogs {
		q.Catalog = c
	}
	for _, k := range slices.Sorted(maps.Keys(a.tables)) {
		q.Tables = append(q.Tables, a.tables[k])
	}
	scope := &irv1.ScopeUse{Unscoped: blk.Unscoped}
	for _, p := range a.params {
		if p.raw.kind == scopeParam {
			scope.Columns = append(scope.Columns, p.raw.name)
			continue
		}
		if p.typ == nil {
			return fail("query-param-untyped", p.raw.start, "the type of parameter %s cannot be inferred; cast it where it is used, such as CAST(@%s AS int)", p.raw.name, p.raw.name)
		}
		q.Params = append(q.Params, &irv1.Param{Name: p.raw.name, Type: bare(p.typ),
			Nullable: p.raw.kind == nargParam, List: p.raw.kind == sliceParam})
	}
	if len(scope.Columns) > 0 || scope.Unscoped {
		q.Scope = scope
	}
	if blk.Cardinality == irv1.Cardinality_CARDINALITY_EXEC || blk.Cardinality == irv1.Cardinality_CARDINALITY_EXEC_ROWS {
		return q, nil
	}
	if cols == nil {
		return fail("query-no-rows", -1, "returns no rows, but its cardinality needs them; add OUTPUT, or use :exec or :execrows")
	}
	fields, qe := resultFields(cols, blk)
	if qe != nil {
		return fail(qe.code, r.original(qe.off), "%s", qe.msg)
	}
	q.Result = &irv1.ResultShape{Kind: &irv1.ResultShape_Anonymous{Anonymous: &irv1.AnonymousShape{Fields: fields}}}
	return q, nil
}

// text returns the statement as SQL Server runs it: each list's (@pN)
// becomes a subquery over OPENJSON of the JSON array its placeholder
// takes, read as the compared column's type (§25.3).
func (a *analyzer) text() string {
	t := a.r.text
	for _, use := range slices.Backward(slices.Sorted(maps.Keys(a.lists))) {
		at := a.r.offsets[use]
		ph := placeholderName(a.r.uses[use])
		t = t[:at-1] + "(SELECT [v] FROM OPENJSON(" + ph + ") WITH ([v] " + a.lists[use] + " '$'))" + t[at+len(ph)+1:]
	}
	return t
}

// resultFields turns result columns into fields and applies -- nonnull:.
func resultFields(cols []column, blk *queryfile.Block) ([]*irv1.ResultField, *queryError) {
	var fields []*irv1.ResultField
	byName := map[string]*irv1.ResultField{}
	for _, c := range cols {
		if c.name == "" {
			return nil, &queryError{code: "query-column-name", off: c.off, msg: "a result column has no name; give it one with AS"}
		}
		v := settle(c.v)
		if v.typ == nil {
			return nil, &queryError{code: "query-param-untyped", off: c.off, msg: fmt.Sprintf("result column %s has no type; cast it", c.name)}
		}
		if byName[strings.ToLower(c.name)] != nil {
			return nil, &queryError{code: "query-column-duplicate", off: c.off, msg: fmt.Sprintf("two result columns are named %s; rename one with AS", c.name)}
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
		byName[strings.ToLower(c.name)] = f
		fields = append(fields, f)
	}
	for _, n := range blk.Nonnull {
		f := byName[strings.ToLower(n)]
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

// syntaxMessage describes a syntax error at offset off of the statement
// body.
func syntaxMessage(body string, off int) string {
	rest, _, _ := strings.Cut(body[off:], "\n")
	if rest == "" {
		return "syntax error at the end of the statement"
	}
	if len(rest) > 40 {
		rest = rest[:40] + "..."
	}
	return fmt.Sprintf("syntax error at %q", rest)
}
