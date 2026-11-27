// Command dql-plugin-mysql-live runs the mysql-live source plugin over the
// stdio protocol (ADR 0002). Its flags are the settings of a dql.yaml
// sources entry.
//
//	dql-plugin-mysql-live -catalog shop -connection env:SHOP_DATABASE_URL
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/thec1oud/dql/plugin"
	"github.com/thec1oud/dql/sources/mysqllive"
)

var exit = os.Exit

func main() {
	exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dql-plugin-mysql-live", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var s mysqllive.Settings
	fs.StringVar(&s.Catalog, "catalog", "", "the catalog's name")
	fs.StringVar(&s.Connection, "connection", "", "a MySQL DSN naming the database, or env:NAME")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := plugin.Serve(context.Background(), mysqllive.New(s), stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "dql-plugin-mysql-live: %v\n", err)
		return 1
	}
	return 0
}
