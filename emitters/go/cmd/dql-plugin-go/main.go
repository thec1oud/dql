// Command dql-plugin-go runs the go emitter over the stdio protocol (ADR
// 0002). It reads its emit entry from each invocation (ADR 0003); its flags,
// when given, replace the entry's settings.
//
//	dql-plugin-go [-dialect postgres] [-package shopdb] [-out /abs/internal/shopdb] [-type public.citext=string ...]
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	goemit "github.com/thec1oud/dql/emitters/go"
	"github.com/thec1oud/dql/plugin"
)

var exit = os.Exit

func main() {
	exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dql-plugin-go", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var s goemit.Settings
	fs.StringVar(&s.Dialect, "dialect", "", "the dialect the package targets")
	fs.StringVar(&s.Package, "package", "", "the generated package's name")
	fs.StringVar(&s.Out, "out", "", "the directory the package is written to")
	fs.Func("type", "namespace.type=GoType: the Go type of an opaque native type; repeatable", func(v string) error {
		native, spec, ok := strings.Cut(v, "=")
		if !ok {
			return fmt.Errorf("want namespace.type=GoType")
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
	if err := plugin.Serve(context.Background(), goemit.New("", s), stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "dql-plugin-go: %v\n", err)
		return 1
	}
	return 0
}
