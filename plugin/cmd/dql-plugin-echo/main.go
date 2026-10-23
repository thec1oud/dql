// Command dql-plugin-echo runs the echo plugin over the stdio protocol.
//
//	dql-plugin-echo [-kind emitter] [-min 1.0.0] [-max-major 1] [-features a,b]
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/thec1oud/dql/plugin"
	"github.com/thec1oud/dql/plugin/echo"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

var exit = os.Exit

func main() {
	exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dql-plugin-echo", flag.ContinueOnError)
	fs.SetOutput(stderr)
	kind := fs.String("kind", "emitter", "plugin kind: source, dialect, type or emitter")
	minimum := fs.String("min", "1.0.0", "lowest IR version read")
	maxMajor := fs.Uint("max-major", 1, "highest IR major version read")
	features := fs.String("features", "", "comma-separated features")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	k, ok := irv1.PluginKind_value["PLUGIN_KIND_"+strings.ToUpper(*kind)]
	if !ok {
		fmt.Fprintf(stderr, "dql-plugin-echo: unknown kind %q\n", *kind)
		return 2
	}
	var fl []string
	if *features != "" {
		fl = strings.Split(*features, ",")
	}
	p := echo.New(irv1.PluginKind(k), *minimum, uint32(*maxMajor), fl...)
	if err := plugin.Serve(context.Background(), p, stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "dql-plugin-echo: %v\n", err)
		return 1
	}
	return 0
}
