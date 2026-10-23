package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/thec1oud/dql/core/bundle"
	"github.com/thec1oud/dql/plugin"
	"github.com/thec1oud/dql/plugin/echo"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/testkit/golden"
)

func write(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func dql(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

const valid = "sources:\n  - plugin: pg-live\n    catalog: shop\n    connection: env:SHOP_DATABASE_URL\nemit:\n  - plugin: go\n    dialect: postgres\n"

func TestUsage(t *testing.T) {
	if code, _, stderr := dql(); code != 2 || !strings.Contains(stderr, "usage:") {
		t.Errorf("no args: %d %q", code, stderr)
	}
	if code, _, stderr := dql("frobnicate"); code != 2 || !strings.Contains(stderr, `unknown command "frobnicate"`) {
		t.Errorf("unknown: %d %q", code, stderr)
	}
}

func TestCheck(t *testing.T) {
	if code, _, stderr := dql("check", "-config", write(t, "dql.yaml", valid)); code != 0 {
		t.Errorf("valid config: %d %q", code, stderr)
	}
	code, _, stderr := dql("check", "-config", write(t, "dql.yaml", "emit:\n  - plugin: go\n    dialect: oracle\n"))
	if code != 1 || !strings.Contains(stderr, "error config-no-sources") || !strings.Contains(stderr, "error config-dialect-unknown") {
		t.Errorf("invalid config: %d %q", code, stderr)
	}
	if code, _, stderr := dql("check", "-config", filepath.Join(t.TempDir(), "none.yaml")); code != 1 || !strings.Contains(stderr, "dql check:") {
		t.Errorf("missing config: %d %q", code, stderr)
	}
	if code, _, _ := dql("check", "-bogus"); code != 2 {
		t.Errorf("bad flag: %d", code)
	}
}

func TestGenerate(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	code, _, stderr := dql("generate", "-config", write(t, "dql.yaml", valid))
	if code != 1 || !strings.Contains(stderr, "dql-plugin-pg-live is not on PATH") || !strings.Contains(stderr, "dql-plugin-go is not on PATH") {
		t.Errorf("unresolved plugins: %d %q", code, stderr)
	}

	builtins["pg-live"] = echo.New(irv1.PluginKind_PLUGIN_KIND_SOURCE, "1.0.0", 1)
	defer delete(builtins, "pg-live")
	cfg := "sources:\n  - plugin: pg-live\n    catalog: shop\n    connection: env:SHOP_DATABASE_URL\nemit:\n  - plugin: go\n    path: /opt/dql-plugin-go\n    dialect: postgres\n"
	code, _, stderr = dql("generate", "-config", write(t, "dql.yaml", cfg))
	if code != 1 || !strings.Contains(stderr, "every plugin was found") {
		t.Errorf("resolved plugins: %d %q", code, stderr)
	}
	if code, _, _ := dql("generate", "-config", write(t, "dql.yaml", "")); code != 1 {
		t.Errorf("invalid config: %d", code)
	}
}

func TestInspect(t *testing.T) {
	b := &irv1.Bundle{Version: bundle.Version(), Dialect: irv1.Dialect_DIALECT_SQLITE}
	js, _ := bundle.MarshalJSON(b)
	bin, _ := bundle.MarshalBinary(b)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "b.json"), js, 0o644)
	os.WriteFile(filepath.Join(dir, "b.pb"), bin, 0o644)
	for _, name := range []string{"b.json", "b.pb"} {
		code, stdout, stderr := dql("inspect", filepath.Join(dir, name))
		if code != 0 {
			t.Fatalf("%s: %d %q", name, code, stderr)
		}
		golden.Assert(t, filepath.Join("testdata", "inspect.json"), []byte(stdout))
	}
}

func TestInspectFailures(t *testing.T) {
	bin, _ := bundle.MarshalBinary(&irv1.Bundle{RequiredFeatures: []string{"\xff"}})
	cases := []struct {
		args []string
		code int
		want string
	}{
		{[]string{"inspect"}, 2, "usage:"},
		{[]string{"inspect", filepath.Join(t.TempDir(), "none.pb")}, 1, "no such file"},
		{[]string{"inspect", write(t, "b.json", "{")}, 1, "b.json"},
		{[]string{"inspect", write(t, "b.pb", string(bin))}, 1, "b.pb"},
	}
	for _, c := range cases {
		code, _, stderr := dql(c.args...)
		if code != c.code || !strings.Contains(stderr, c.want) {
			t.Errorf("%v: %d %q", c.args, code, stderr)
		}
	}
}

func TestInspectReportsEncodingFailure(t *testing.T) {
	defer func(f func(proto.Message) ([]byte, error)) { marshalJSON = f }(marshalJSON)
	marshalJSON = func(proto.Message) ([]byte, error) { return nil, errors.New("boom") }
	js, _ := bundle.MarshalJSON(&irv1.Bundle{})
	if code, _, stderr := dql("inspect", write(t, "b.json", string(js))); code != 1 || !strings.Contains(stderr, "boom") {
		t.Errorf("%d %q", code, stderr)
	}
}

func TestMainExits(t *testing.T) {
	defer func(f func(int), args []string) { exit, os.Args = f, args }(exit, os.Args)
	os.Args = []string{"dql"}
	code := -1
	exit = func(c int) { code = c }
	main()
	if code != 2 {
		t.Fatalf("exit %d", code)
	}
}

var _ plugin.Plugin = (*echo.Plugin)(nil)
