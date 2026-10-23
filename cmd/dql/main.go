// Command dql is the dql compiler.
//
//	dql check    [-config dql.yaml]   validate the configuration
//	dql generate [-config dql.yaml]   generate code for every emit entry
//	dql inspect  <bundle.json|bundle.pb>   print a bundle as canonical JSON
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/thec1oud/dql/cmd/dql/internal/config"
	"github.com/thec1oud/dql/core/bundle"
	"github.com/thec1oud/dql/plugin"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

const usage = `usage:
  dql check    [-config dql.yaml]
  dql generate [-config dql.yaml]
  dql inspect  <bundle.json|bundle.pb>
`

// builtins are the shipped plugins that run in-process (ADR 0002). The first
// arrive with M1.
var builtins = map[string]plugin.Plugin{}

var exit = os.Exit

// marshalJSON is a variable so tests can make encoding fail.
var marshalJSON = bundle.MarshalJSON

func main() {
	exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "check":
		return check(args[1:], stderr)
	case "generate":
		return generate(args[1:], stderr)
	case "inspect":
		return inspect(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "dql: unknown command %q\n%s", args[0], usage)
		return 2
	}
}

// loadConfig parses flags, loads dql.yaml and prints its diagnostics. It
// returns nil and an exit code when the command cannot continue.
func loadConfig(name string, args []string, stderr io.Writer) (*config.Config, int) {
	fs := flag.NewFlagSet("dql "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("config", "dql.yaml", "project configuration")
	if err := fs.Parse(args); err != nil {
		return nil, 2
	}
	c, err := config.Load(*path)
	if err != nil {
		fmt.Fprintf(stderr, "dql %s: %v\n", name, err)
		return nil, 1
	}
	ds := c.Validate()
	for _, d := range ds {
		fmt.Fprintf(stderr, "error %s: %s\n", d.GetCode(), d.GetMessage())
	}
	if len(ds) > 0 {
		return nil, 1
	}
	return c, 0
}

func check(args []string, stderr io.Writer) int {
	_, code := loadConfig("check", args, stderr)
	return code
}

func generate(args []string, stderr io.Writer) int {
	c, code := loadConfig("generate", args, stderr)
	if c == nil {
		return code
	}
	failed := false
	for _, s := range c.Sources {
		if _, err := plugin.Locate(s.Plugin, s.Path, builtins); err != nil {
			fmt.Fprintf(stderr, "dql generate: %v\n", err)
			failed = true
		}
	}
	for _, e := range c.Emit {
		if _, err := plugin.Locate(e.Plugin, e.Path, builtins); err != nil {
			fmt.Fprintf(stderr, "dql generate: %v\n", err)
			failed = true
		}
	}
	if failed {
		return 1
	}
	fmt.Fprintln(stderr, "dql generate: every plugin was found; building the catalog arrives with M1")
	return 1
}

func inspect(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	raw, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "dql inspect: %v\n", err)
		return 1
	}
	b := &irv1.Bundle{}
	if filepath.Ext(args[0]) == ".json" {
		err = bundle.UnmarshalJSON(raw, b)
	} else {
		err = bundle.UnmarshalBinary(raw, b)
	}
	if err != nil {
		fmt.Fprintf(stderr, "dql inspect: %s: %v\n", args[0], err)
		return 1
	}
	out, err := marshalJSON(b)
	if err != nil {
		fmt.Fprintf(stderr, "dql inspect: %v\n", err)
		return 1
	}
	stdout.Write(out)
	return 0
}
