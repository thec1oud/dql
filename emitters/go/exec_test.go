package goemit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/thec1oud/dql/plugin"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// The shipped emitter also runs as the executable dql-plugin-go, and both
// paths generate the same package (ADR 0002). The executable runs in the
// directory a relative out resolves against.
func TestExecutable(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "dql-plugin-go")
	if msg, err := exec.Command("go", "build", "-o", bin, "./cmd/dql-plugin-go").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, msg)
	}
	ctx := context.Background()
	b := shop(t)
	c, err := plugin.Start(ctx, bin, dir, nil, &irv1.CoreHello{IrVersion: b.GetVersion()}, irv1.PluginKind_PLUGIN_KIND_EMITTER)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Invoke(ctx, &irv1.Invoke{Bundle: b, Settings: shopSettings(t, "out")})
	if cerr := c.Close(); err == nil {
		err = cerr
	}
	if err != nil || len(res.GetDiagnostics()) != 0 {
		t.Fatal(err, res.GetDiagnostics())
	}
	want, _ := filepath.Glob(filepath.Join("testdata", "shopdb", "*"))
	for _, w := range want {
		got, err := os.ReadFile(filepath.Join(dir, "out", filepath.Base(w)))
		if err != nil {
			t.Fatal(err)
		}
		if exp, _ := os.ReadFile(w); string(got) != string(exp) {
			t.Errorf("%s differs from the in-process output", filepath.Base(w))
		}
	}
}
