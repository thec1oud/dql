// Command dql-plugin-sqlite runs the sqlite dialect plugin over the
// stdio protocol (ADR 0002).
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/thec1oud/dql/dialects/sqlite"
	"github.com/thec1oud/dql/plugin"
)

var exit = os.Exit

func main() {
	exit(run(os.Stdin, os.Stdout, os.Stderr))
}

func run(stdin io.Reader, stdout, stderr io.Writer) int {
	if err := plugin.Serve(context.Background(), sqlite.New(), stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "dql-plugin-sqlite: %v\n", err)
		return 1
	}
	return 0
}
