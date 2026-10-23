package plugin

import (
	"context"
	"fmt"
	"os/exec"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// ExecutablePrefix names third-party plugin executables: plugin "foo" is
// dql-plugin-foo on PATH.
const ExecutablePrefix = "dql-plugin-"

// Target is where a plugin runs: in-process, or as an executable.
type Target struct {
	Name    string
	Builtin Plugin
	Path    string
}

// Locate resolves a plugin named in dql.yaml. An explicit path wins; then a
// shipped plugin built into dql; then dql-plugin-<name> on PATH (ADR 0002).
func Locate(name, path string, builtins map[string]Plugin) (Target, error) {
	if path != "" {
		return Target{Name: name, Path: path}, nil
	}
	if p, ok := builtins[name]; ok {
		return Target{Name: name, Builtin: p}, nil
	}
	found, err := exec.LookPath(ExecutablePrefix + name)
	if err != nil {
		return Target{}, fmt.Errorf("plugin %s is not built in and %s%s is not on PATH; set path: to its executable",
			name, ExecutablePrefix, name)
	}
	return Target{Name: name, Path: found}, nil
}

// Open negotiates with the plugin at t.
func Open(ctx context.Context, t Target, hello *irv1.CoreHello, kind irv1.PluginKind) (Client, error) {
	if t.Builtin != nil {
		return InProcess(t.Builtin, hello, kind)
	}
	return Start(ctx, t.Path, nil, hello, kind)
}
