package plugin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thec1oud/dql/plugin/echo"
)

func echoWith(min string) Plugin {
	return echo.New(emitter, min, 1)
}

func TestLocate(t *testing.T) {
	builtins := map[string]Plugin{"go": echoWith("1.0.0")}
	dir := t.TempDir()
	onPath := filepath.Join(dir, "dql-plugin-thirdparty")
	if err := os.Symlink(echoBin, onPath); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	if got, _ := Locate("go", "/opt/dql-plugin-go", builtins); got.Path != "/opt/dql-plugin-go" || got.Builtin != nil {
		t.Errorf("explicit path should win: %+v", got)
	}
	if got, _ := Locate("go", "", builtins); got.Builtin == nil {
		t.Errorf("built-in not found: %+v", got)
	}
	if got, _ := Locate("thirdparty", "", builtins); got.Path != onPath {
		t.Errorf("PATH lookup: %+v", got)
	}
	if _, err := Locate("missing", "", builtins); err == nil || !strings.Contains(err.Error(), "dql-plugin-missing is not on PATH") {
		t.Errorf("missing plugin: %v", err)
	}
}

func TestOpen(t *testing.T) {
	ctx := context.Background()
	for _, target := range []Target{{Name: "echo", Builtin: echoWith("1.0.0")}, {Name: "echo", Path: echoBin}} {
		c, err := Open(ctx, target, hello(), emitter)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
	// An executable is started with its arguments.
	c, err := Open(ctx, Target{Name: "echo", Path: echoBin, Args: []string{"-features", "opaque-types"}}, hello("opaque-types"), emitter)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if _, err := Open(ctx, Target{Name: "echo", Path: echoBin}, hello("opaque-types"), emitter); err == nil {
		t.Error("an executable started without -features lacks opaque-types")
	}
}
