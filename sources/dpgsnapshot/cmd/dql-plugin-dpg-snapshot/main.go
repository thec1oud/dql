// Command dql-plugin-dpg-snapshot runs the dpg-snapshot source plugin over
// the stdio protocol (ADR 0002). Its flags are the settings of a dql.yaml
// sources entry.
//
//	dql-plugin-dpg-snapshot -snapshot db/shop.json [-catalog shop] [-namespaces app,audit] [-default-namespace app]
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/thec1oud/dql/plugin"
	"github.com/thec1oud/dql/sources/dpgsnapshot"
)

var exit = os.Exit

func main() {
	exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dql-plugin-dpg-snapshot", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var s dpgsnapshot.Settings
	fs.StringVar(&s.Snapshot, "snapshot", "", "the path of the dpg snapshot file")
	fs.StringVar(&s.Catalog, "catalog", "", "the catalog's name; default: the snapshot's database")
	namespaces := fs.String("namespaces", "", "comma-separated schemas to read; default: the default namespace")
	fs.StringVar(&s.DefaultNamespace, "default-namespace", "", "the namespace whose objects get no prefix; default: public")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *namespaces != "" {
		s.Namespaces = strings.Split(*namespaces, ",")
	}
	if err := plugin.Serve(context.Background(), dpgsnapshot.New(s), stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "dql-plugin-dpg-snapshot: %v\n", err)
		return 1
	}
	return 0
}
