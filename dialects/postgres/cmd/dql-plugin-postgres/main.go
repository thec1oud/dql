// Command dql-plugin-postgres runs the postgres dialect plugin over the
// stdio protocol (ADR 0002).
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/thec1oud/dql/dialects/postgres"
	"github.com/thec1oud/dql/plugin"
)

var exit = os.Exit

func main() {
	exit(run(os.Stdin, os.Stdout, os.Stderr))
}

func run(stdin io.Reader, stdout, stderr io.Writer) int {
	if err := plugin.Serve(context.Background(), postgres.New(), stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "dql-plugin-postgres: %v\n", err)
		return 1
	}
	return 0
}
