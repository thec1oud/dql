package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/thec1oud/dql/cmd/dql/internal/config"
	"github.com/thec1oud/dql/core/bundle"
	"github.com/thec1oud/dql/core/catalog"
	"github.com/thec1oud/dql/plugin"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/dpgsnapshot"
	"github.com/thec1oud/dql/sources/pglive"
)

// sources are the shipped source plugins, which run in-process (ADR 0002).
// The protocol carries no settings, so each is built from its sources entry.
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

// generate runs every source, assembles the catalog layer (§2.2, §5.2) and
// writes the bundle to -bundle, in canonical JSON (§3.1). Emitters arrive in
// later milestones, so emit entries are skipped.
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
		fmt.Fprintf(stderr, "dql generate: skipped emit[%d] (%s): emitters arrive in a later milestone\n", i, e.Plugin)
	}
	return 0
}

// runSource invokes one source plugin with a bundle of the core's IR version
// and returns what it produced.
func runSource(ctx context.Context, c *config.Config, s config.Source) (catalog.SourceOutput, error) {
	builtins := map[string]plugin.Plugin{}
	if build, ok := sources[s.Plugin]; ok {
		builtins[s.Plugin] = build(c, s)
	}
	t, err := plugin.Locate(s.Plugin, s.Path, builtins)
	if err != nil {
		return catalog.SourceOutput{}, err
	}
	t.Args = flags(c, s)
	hello := &irv1.CoreHello{IrVersion: bundle.Version()}
	client, err := plugin.Open(ctx, t, hello, irv1.PluginKind_PLUGIN_KIND_SOURCE)
	if err != nil {
		return catalog.SourceOutput{}, err
	}
	res, err := client.Invoke(ctx, &irv1.Bundle{Version: bundle.Version()})
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

// flags are the settings of a sources entry as an executable takes them,
// as the shipped source executables do: -catalog shop -namespaces app,audit.
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
