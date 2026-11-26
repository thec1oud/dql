// Command dql-plugin-ts runs the ts emitter over the stdio protocol (ADR
// 0002). It reads its emit entry from each invocation (ADR 0003); its flags,
// when given, replace the entry's settings.
//
//	dql-plugin-ts [-dialect postgres] [-out /abs/src/shopdb] [-type public.citext=string ...]
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	tsemit "github.com/thec1oud/dql/emitters/ts"
	"github.com/thec1oud/dql/plugin"
)

var exit = os.Exit

func main() {
	exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dql-plugin-ts", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var s tsemit.Settings
	fs.StringVar(&s.Dialect, "dialect", "", "the dialect the package targets")
	fs.StringVar(&s.Out, "out", "", "the directory the package is written to")
	fs.Func("type", "namespace.type=TsType: the TypeScript type of an opaque native type; repeatable", func(v string) error {
		native, spec, ok := strings.Cut(v, "=")
		if !ok {
			return fmt.Errorf("want namespace.type=TsType")
		}
		if s.Types == nil {
			s.Types = map[string]string{}
		}
		s.Types[native] = spec
		return nil
	})
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := plugin.Serve(context.Background(), tsemit.New("", s), stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "dql-plugin-ts: %v\n", err)
		return 1
	}
	return 0
}
