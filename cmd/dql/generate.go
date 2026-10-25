package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/thec1oud/dql/cmd/dql/internal/config"
	"github.com/thec1oud/dql/core/bundle"
	"github.com/thec1oud/dql/core/catalog"
	"github.com/thec1oud/dql/core/conversions"
	"github.com/thec1oud/dql/core/diagnostics"
	"github.com/thec1oud/dql/core/operators"
	"github.com/thec1oud/dql/core/shapes"
	"github.com/thec1oud/dql/dialects/postgres"
	goemit "github.com/thec1oud/dql/emitters/go"
	"github.com/thec1oud/dql/plugin"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	pgqueries "github.com/thec1oud/dql/querysources/postgres"
	"github.com/thec1oud/dql/sources/dpgsnapshot"
	"github.com/thec1oud/dql/sources/pglive"
)

// sources are the shipped source plugins, which run in-process (ADR 0002).
// Each is built from its sources entry and reads its settings from there.
var sources = map[string]func(c *config.Config, s config.Source) plugin.Plugin{
	pglive.Name: func(_ *config.Config, s config.Source) plugin.Plugin {
		return pglive.New(pglive.Settings{Catalog: s.Catalog, Connection: s.Connection,
			Namespaces: s.Namespaces, DefaultNamespace: s.DefaultNamespace})
	},
	dpgsnapshot.Name: func(c *config.Config, s config.Source) plugin.Plugin {
		return dpgsnapshot.New(dpgsnapshot.Settings{Snapshot: c.Resolve(s.Snapshot), Catalog: s.Catalog,
			Namespaces: s.Namespaces, DefaultNamespace: s.DefaultNamespace})
	},
}

// dialects are the shipped dialect plugins, by the dialect they serve, which
// run in-process (ADR 0002).
var dialects = map[irv1.Dialect]func() plugin.Plugin{
	irv1.Dialect_DIALECT_POSTGRES: func() plugin.Plugin { return postgres.New() },
}

// querySources are the shipped query-source plugins, by the dialect whose
// query files they type, which run in-process (ADR 0002).
var querySources = map[irv1.Dialect]func() plugin.Plugin{
	irv1.Dialect_DIALECT_POSTGRES: func() plugin.Plugin { return pgqueries.New() },
}

// emitters are the shipped emitter plugins, which run in-process (ADR 0002).
// Each resolves a relative out against the directory of dql.yaml.
var emitters = map[string]func(c *config.Config) plugin.Plugin{
	goemit.Name: func(c *config.Config) plugin.Plugin { return goemit.New(c.Dir(), goemit.Settings{}) },
}

// planned are emitters that ship in a later milestone; their entries are
// skipped until then.
var planned = map[string]bool{"ts": true}

// generate runs every source, assembles the catalog layer (§2.2, §5.2), the
// shape layer (§14), the operators (§9) and the query layer (§16, §17),
// writes the bundle to -bundle, in canonical JSON (§3.1), and runs every
// emitter on it (§2.3).
func generate(args []string, stderr io.Writer) int {
	var out string
	c, anns, code := loadConfig("generate", args, stderr, func(fs *flag.FlagSet) {
		fs.StringVar(&out, "bundle", "", "write the bundle to this file, as canonical JSON")
	})
	if c == nil {
		return code
	}
	ctx := context.Background()
	var outputs []catalog.SourceOutput
	failed := false
	for i, s := range c.Sources {
		o, err := runSource(ctx, c, s)
		if err != nil {
			fmt.Fprintf(stderr, "dql generate: sources[%d] (%s): %v\n", i, s.Plugin, err)
			failed = true
			continue
		}
		outputs = append(outputs, o)
	}
	if failed {
		return 1
	}
	attach(outputs, c.Sources, anns)

	b, ds := catalog.Assemble(outputs, catalog.Options{
		// Dialect plugins publish the capability (§5.2) in a later
		// milestone; until then no reference may cross catalogs, which is
		// Postgres's rule (§5.1).
		CrossCatalogReferences: false,
		DefaultCatalog:         c.DefaultCatalog,
		OpaqueMapped:           opaqueMapped(c),
		OpaqueFallback:         c.OpaqueFallback,
	})
	printDiagnostics(stderr, ds)
	if b == nil {
		return 1
	}
	ds, err := shapeLayer(b, c)
	if err != nil {
		fmt.Fprintf(stderr, "dql generate: %v\n", err)
		return 1
	}
	printDiagnostics(stderr, ds)
	if hasErrors(ds) {
		return 1
	}
	ds, err = operatorLayer(ctx, b, c)
	if err != nil {
		fmt.Fprintf(stderr, "dql generate: %v\n", err)
		return 1
	}
	printDiagnostics(stderr, ds)
	if hasErrors(ds) {
		return 1
	}
	ds, err = queryLayer(ctx, b, c)
	if err != nil {
		fmt.Fprintf(stderr, "dql generate: %v\n", err)
		return 1
	}
	printDiagnostics(stderr, ds)
	if hasErrors(ds) {
		return 1
	}
	if out != "" {
		js, err := marshalJSON(b)
		if err == nil {
			err = os.WriteFile(out, js, 0o644)
		}
		if err != nil {
			fmt.Fprintf(stderr, "dql generate: %v\n", err)
			return 1
		}
		fmt.Fprintf(stderr, "dql generate: wrote the bundle to %s\n", out)
	}
	for i, e := range c.Emit {
		if planned[e.Plugin] {
			fmt.Fprintf(stderr, "dql generate: skipped emit[%d] (%s): the %s emitter arrives in a later milestone\n", i, e.Plugin, e.Plugin)
			continue
		}
		ds, err := runEmitter(ctx, c, e, b)
		printDiagnostics(stderr, ds)
		if err != nil {
			fmt.Fprintf(stderr, "dql generate: emit[%d] (%s): %v\n", i, e.Plugin, err)
		}
		if err != nil || hasErrors(ds) {
			failed = true
		}
	}
	if failed {
		return 1
	}
	return 0
}

// runEmitter invokes one emitter plugin with the bundle and its emit entry,
// and returns its diagnostics. The core refuses an emitter that lacks a
// feature the bundle requires (§3.3, §15.12).
func runEmitter(ctx context.Context, c *config.Config, e config.Emit, b *irv1.Bundle) ([]*irv1.Diagnostic, error) {
	builtins := map[string]plugin.Plugin{}
	if build, ok := emitters[e.Plugin]; ok {
		builtins[e.Plugin] = build(c)
	}
	t, err := plugin.Locate(e.Plugin, c.Resolve(e.Path), builtins)
	if err != nil {
		return nil, err
	}
	t.Dir = c.Dir()
	if _, ok := emitters[e.Plugin]; ok {
		t.Args = emitFlags(c, e)
	}
	hello := &irv1.CoreHello{IrVersion: b.GetVersion(), RequiredFeatures: b.GetRequiredFeatures()}
	client, err := plugin.Open(ctx, t, hello, irv1.PluginKind_PLUGIN_KIND_EMITTER)
	if err != nil {
		return nil, err
	}
	res, err := client.Invoke(ctx, &irv1.Invoke{Bundle: b, Settings: e.Settings})
	if cerr := client.Close(); err == nil {
		err = cerr
	}
	return res.GetDiagnostics(), err
}

// emitFlags are the settings of a shipped emitter's emit entry as its
// executable takes them, out as an absolute path (ADR 0003): -dialect
// postgres -package shopdb -out /abs/internal/shopdb -type public.citext=string.
func emitFlags(c *config.Config, e config.Emit) []string {
	var args []string
	for _, f := range [][2]string{{"dialect", e.Dialect}, {"package", e.Package}, {"out", c.Resolve(e.Out)}} {
		if f[1] != "" {
			args = append(args, "-"+f[0], f[1])
		}
	}
	for _, k := range slices.Sorted(maps.Keys(e.Types)) {
		if spec, ok := e.Types[k][e.Plugin].(string); ok {
			args = append(args, "-type", k+"="+spec)
		}
	}
	return args
}

// operatorLayer adds the operator catalog to b, runs the shipped dialect
// plugin of b's dialect in-process for its capability profile and the
// operators it contributes, and resolves every column's operators (§9). It returns the diagnostics, sorted, and adds them to the
// bundle's.
func operatorLayer(ctx context.Context, b *irv1.Bundle, c *config.Config) (ds []*irv1.Diagnostic, err error) {
	build, ok := dialects[b.GetDialect()]
	if !ok {
		return nil, fmt.Errorf("no dialect plugin serves %s", b.GetDialect())
	}
	b.Operators = operators.Catalog()
	p := build()
	name := p.Handshake().GetPlugin().GetName()
	client, err := plugin.InProcess(p, &irv1.CoreHello{IrVersion: b.GetVersion()}, irv1.PluginKind_PLUGIN_KIND_DIALECT)
	if err == nil {
		var res *irv1.InvokeResult
		if res, err = client.Invoke(ctx, &irv1.Invoke{Bundle: b}); err == nil {
			b.Capabilities = res.GetBundle().GetCapabilities()
			var added []*irv1.Diagnostic
			b.Operators, added = operators.Add(b.Operators, res.GetBundle().GetOperators(), irv1.OperatorOriginKind_OPERATOR_ORIGIN_KIND_DIALECT_PLUGIN, name)
			ds = append(res.GetDiagnostics(), added...)
		}
		if cerr := client.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		return nil, fmt.Errorf("dialect %s: %w", name, err)
	}
	ds = append(ds, operators.Resolve(b, operators.Options{AllowApproximate: c.Operators.AllowApproximate})...)
	diagnostics.Sort(ds)
	b.Diagnostics = append(b.Diagnostics, ds...)
	diagnostics.Sort(b.Diagnostics)
	return ds, nil
}

// queryLayer types the query files of c with the shipped query-source
// plugin of b's dialect and adds their queries to b (§16, §17). It returns
// the plugin's diagnostics, sorted, and adds them to the bundle's. Without
// query files the bundle has no query layer.
func queryLayer(ctx context.Context, b *irv1.Bundle, c *config.Config) ([]*irv1.Diagnostic, error) {
	files, err := c.QueryFiles()
	if err != nil || len(files) == 0 {
		return nil, err
	}
	build, ok := querySources[b.GetDialect()]
	if !ok {
		return nil, fmt.Errorf("no query-source plugin types %s query files", b.GetDialect())
	}
	p := build()
	name := p.Handshake().GetPlugin().GetName()
	client, err := plugin.InProcess(p, &irv1.CoreHello{IrVersion: b.GetVersion()}, irv1.PluginKind_PLUGIN_KIND_QUERY_SOURCE)
	var ds []*irv1.Diagnostic
	if err == nil {
		var res *irv1.InvokeResult
		if res, err = client.Invoke(ctx, &irv1.Invoke{Bundle: b, QueryFiles: files}); err == nil {
			b.Queries = &irv1.QueryLayer{Queries: res.GetBundle().GetQueries().GetQueries()}
			ds = res.GetDiagnostics()
		}
		if cerr := client.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		return nil, fmt.Errorf("query source %s: %w", name, err)
	}
	diagnostics.Sort(ds)
	b.Diagnostics = append(b.Diagnostics, ds...)
	diagnostics.Sort(b.Diagnostics)
	return ds, nil
}

// buildShapes is a variable so tests can make the shape layer fail.
var buildShapes = shapes.Layer

// shapeLayer adds the shape layer to b: the shapes, unions and upserts of
// the catalogs and the custom shapes of c (§14.3 to §14.8), then the
// conversion table, with the conversions c declares, and the merges (§14.9,
// §8.9). It returns the diagnostics of both, sorted, and adds them to the
// bundle's.
func shapeLayer(b *irv1.Bundle, c *config.Config) ([]*irv1.Diagnostic, error) {
	layer, ds, err := buildShapes(b.GetCatalogs(), c.CustomShapes())
	if err != nil {
		return nil, err
	}
	b.Shapes = layer
	var declared []conversions.Declared
	for _, v := range c.Conversions {
		declared = append(declared, conversions.Declared{
			From: shapes.Ref(b.GetCatalogs(), layer, v.From),
			To:   shapes.Ref(b.GetCatalogs(), layer, v.To),
		})
	}
	ds = append(ds, conversions.Derive(b, declared)...)
	diagnostics.Sort(ds)
	b.Diagnostics = append(b.Diagnostics, ds...)
	diagnostics.Sort(b.Diagnostics)
	return ds, nil
}

// hasErrors reports whether any of ds is an error.
func hasErrors(ds []*irv1.Diagnostic) bool {
	return slices.ContainsFunc(ds, func(d *irv1.Diagnostic) bool { return d.GetSeverity() == irv1.Severity_SEVERITY_ERROR })
}

// runSource invokes one source plugin with a bundle of the core's IR version
// and returns what it produced.
func runSource(ctx context.Context, c *config.Config, s config.Source) (catalog.SourceOutput, error) {
	builtins := map[string]plugin.Plugin{}
	if build, ok := sources[s.Plugin]; ok {
		builtins[s.Plugin] = build(c, s)
	}
	t, err := plugin.Locate(s.Plugin, c.Resolve(s.Path), builtins)
	if err != nil {
		return catalog.SourceOutput{}, err
	}
	t.Dir = c.Dir()
	if _, ok := sources[s.Plugin]; ok {
		t.Args = flags(c, s)
	}
	hello := &irv1.CoreHello{IrVersion: bundle.Version()}
	client, err := plugin.Open(ctx, t, hello, irv1.PluginKind_PLUGIN_KIND_SOURCE)
	if err != nil {
		return catalog.SourceOutput{}, err
	}
	res, err := client.Invoke(ctx, &irv1.Invoke{Bundle: &irv1.Bundle{Version: bundle.Version()}, Settings: s.Settings})
	if cerr := client.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return catalog.SourceOutput{}, err
	}
	return catalog.SourceOutput{
		Dialect:     res.GetBundle().GetDialect(),
		Catalogs:    res.GetBundle().GetCatalogs(),
		Diagnostics: res.GetDiagnostics(),
	}, nil
}

// flags are the settings of a shipped plugin's sources entry as its
// executable takes them: -catalog shop -namespaces app,audit. Any other
// executable reads its settings from the invocation.
func flags(c *config.Config, s config.Source) []string {
	var args []string
	for _, f := range [][2]string{
		{"catalog", s.Catalog},
		{"connection", s.Connection},
		{"snapshot", c.Resolve(s.Snapshot)},
		{"namespaces", strings.Join(s.Namespaces, ",")},
		{"default-namespace", s.DefaultNamespace},
	} {
		if f[1] != "" {
			args = append(args, "-"+f[0], f[1])
		}
	}
	return args
}

// attach hands each config annotation to the output whose catalog its key
// names, and names that output's source plugin in its origin: in §7.6 the
// source plugin is the one that read it. A key that names no catalog of any
// output goes to the first output, for Assemble to report. outputs and
// entries are in the same order.
func attach(outputs []catalog.SourceOutput, entries []config.Source, byKey map[string][]*irv1.Annotation) {
	for key, anns := range byKey {
		at, catalogName := 0, ""
		for i, o := range outputs {
			for _, cat := range o.Catalogs {
				// The longest catalog name that prefixes the key wins, so a
				// catalog name may hold dots.
				name := cat.GetName().GetStored()
				if strings.HasPrefix(key, name+".") && len(name) > len(catalogName) {
					at, catalogName = i, name
				}
			}
		}
		source := ""
		if catalogName != "" {
			source = entries[at].Plugin
		}
		for _, a := range anns {
			a.Origin.Plugin = source
		}
		if outputs[at].Config == nil {
			outputs[at].Config = map[string][]*irv1.Annotation{}
		}
		outputs[at].Config[key] = anns
	}
}

// opaqueMapped lists the native types that emitter config maps (§8.3), from
// the keys of every emit entry's types, written namespace.type.
func opaqueMapped(c *config.Config) []*irv1.QualifiedName {
	var out []*irv1.QualifiedName
	for _, e := range c.Emit {
		var keys []string
		for k := range e.Types {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			ns, object, _ := strings.Cut(k, ".")
			out = append(out, &irv1.QualifiedName{Namespace: ns, Object: object})
		}
	}
	return out
}

// printDiagnostics prints diagnostics as dql check prints configuration
// errors: severity, code and message.
func printDiagnostics(w io.Writer, ds []*irv1.Diagnostic) {
	for _, d := range ds {
		severity := strings.ToLower(strings.TrimPrefix(d.GetSeverity().String(), "SEVERITY_"))
		fmt.Fprintf(w, "%s %s: %s\n", severity, d.GetCode(), d.GetMessage())
	}
}
