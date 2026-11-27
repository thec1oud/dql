// Command dql-plugin-pg-live runs the pg-live source plugin over the stdio
// protocol (ADR 0002). Its flags are the settings of a dql.yaml sources entry.
//
//	dql-plugin-pg-live -catalog shop -connection env:SHOP_DATABASE_URL [-namespaces app,audit] [-default-namespace app]
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/thec1oud/dql/plugin"
	"github.com/thec1oud/dql/sources/pglive"
)

var exit = os.Exit

func main() {
	exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dql-plugin-pg-live", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var s pglive.Settings
	fs.StringVar(&s.Catalog, "catalog", "", "the catalog's name")
	fs.StringVar(&s.Connection, "connection", "", "a Postgres connection string, or env:NAME")
	namespaces := fs.String("namespaces", "", "comma-separated schemas to read; default: the default namespace")
	fs.StringVar(&s.DefaultNamespace, "default-namespace", "", "the namespace whose objects get no prefix; default: public")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *namespaces != "" {
		s.Namespaces = strings.Split(*namespaces, ",")
	}
	if err := plugin.Serve(context.Background(), pglive.New(s), stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "dql-plugin-pg-live: %v\n", err)
		return 1
	}
	return 0
}
