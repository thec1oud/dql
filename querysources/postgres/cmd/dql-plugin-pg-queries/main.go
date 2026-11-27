// Command dql-plugin-pg-queries runs the Postgres query-source plugin over the
// stdio protocol (ADR 0002).
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/thec1oud/dql/plugin"
	"github.com/thec1oud/dql/querysources/postgres"
)

var exit = os.Exit

func main() {
	exit(run(os.Stdin, os.Stdout, os.Stderr))
}

func run(stdin io.Reader, stdout, stderr io.Writer) int {
	if err := plugin.Serve(context.Background(), postgres.New(), stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "dql-plugin-pg-queries: %v\n", err)
		return 1
	}
	return 0
}
