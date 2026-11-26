// Command dql-plugin-sqlite-live runs the sqlite-live source plugin over the
// stdio protocol (ADR 0002). Its flags are the settings of a dql.yaml
// sources entry.
//
//	dql-plugin-sqlite-live -catalog shop -connection env:SHOP_DATABASE
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/thec1oud/dql/plugin"
	"github.com/thec1oud/dql/sources/sqlitelive"
)

var exit = os.Exit

func main() {
	exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dql-plugin-sqlite-live", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var s sqlitelive.Settings
	fs.StringVar(&s.Catalog, "catalog", "", "the catalog's name")
	fs.StringVar(&s.Connection, "connection", "", "the database file's path, a file: URI, or env:NAME")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := plugin.Serve(context.Background(), sqlitelive.New(s), stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "dql-plugin-sqlite-live: %v\n", err)
		return 1
	}
	return 0
}
