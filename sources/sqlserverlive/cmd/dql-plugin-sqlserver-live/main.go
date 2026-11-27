// Command dql-plugin-sqlserver-live runs the sqlserver-live source plugin over the
// stdio protocol (ADR 0002). Its flags are the settings of a dql.yaml
// sources entry.
//
//	dql-plugin-sqlserver-live -catalog shop -connection env:SHOP_DATABASE_URL
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/thec1oud/dql/plugin"
	"github.com/thec1oud/dql/sources/sqlserverlive"
)

var exit = os.Exit

func main() {
	exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dql-plugin-sqlserver-live", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var s sqlserverlive.Settings
	fs.StringVar(&s.Catalog, "catalog", "", "the catalog's name")
	fs.StringVar(&s.Connection, "connection", "", "a go-mssqldb connection string naming the database, or env:NAME")
	fs.StringVar(&s.DefaultNamespace, "default-namespace", "", "the default namespace, if not dbo")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := plugin.Serve(context.Background(), sqlserverlive.New(s), stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "dql-plugin-sqlserver-live: %v\n", err)
		return 1
	}
	return 0
}
