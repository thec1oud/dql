// Command dql is the dql compiler.
//
//	dql check    [-config dql.yaml]                  validate the configuration
//	dql generate [-config dql.yaml] [-bundle <file>]  build the bundle, and write it to file
//	dql inspect  <bundle.json|bundle.pb>             print a bundle as canonical JSON
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/thec1oud/dql/cmd/dql/internal/config"
	"github.com/thec1oud/dql/core/bundle"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

const usage = `usage:
  dql check    [-config dql.yaml]
  dql generate [-config dql.yaml] [-bundle <file>]
  dql inspect  <bundle.json|bundle.pb>
`

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

// loadConfig parses flags, with the command's own added by more, loads
// dql.yaml and its annotations, and prints their diagnostics. It returns nil
// and an exit code when the command cannot continue.
func loadConfig(name string, args []string, stderr io.Writer, more func(*flag.FlagSet)) (*config.Config, map[string][]*irv1.Annotation, int) {
	fs := flag.NewFlagSet("dql "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("config", "dql.yaml", "project configuration")
	more(fs)
	if err := fs.Parse(args); err != nil {
		return nil, nil, 2
	}
	c, err := config.Load(*path)
	if err != nil {
		fmt.Fprintf(stderr, "dql %s: %v\n", name, err)
		return nil, nil, 1
	}
	anns, annotationDiags := c.ConfigAnnotations()
	ds := append(c.Validate(), annotationDiags...)
	printDiagnostics(stderr, ds)
	if len(ds) > 0 {
		return nil, nil, 1
	}
	return c, anns, 0
}

func check(args []string, stderr io.Writer) int {
	_, _, code := loadConfig("check", args, stderr, func(*flag.FlagSet) {})
	return code
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
