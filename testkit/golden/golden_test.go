package golden

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type fakeTB struct{ errors, fatals []string }

func (f *fakeTB) Helper() {}
func (f *fakeTB) Errorf(format string, args ...any) {
	f.errors = append(f.errors, fmt.Sprintf(format, args...))
}
func (f *fakeTB) Fatalf(format string, args ...any) {
	f.fatals = append(f.fatals, fmt.Sprintf(format, args...))
}

func TestAssertMatches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.json")
	os.WriteFile(path, []byte("x\ny\n"), 0o644)
	f := &fakeTB{}
	Assert(f, path, []byte("x\ny\n"))
	if len(f.errors)+len(f.fatals) != 0 {
		t.Fatal(f.errors, f.fatals)
	}
}

func TestAssertReportsDifference(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.json")
	os.WriteFile(path, []byte("x\ny\n"), 0o644)
	f := &fakeTB{}
	Assert(f, path, []byte("x\nz\n"))
	if len(f.errors) != 1 || !strings.Contains(f.errors[0], "line 2 (byte 2)") {
		t.Fatal(f.errors)
	}
}

func TestAssertMasks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.json")
	os.WriteFile(path, []byte("x 12\ny\n"), 0o644)
	digits := regexp.MustCompile(`[0-9]+`)
	f := &fakeTB{}
	Assert(f, path, []byte("x 345\ny\n"), digits)
	if len(f.errors)+len(f.fatals) != 0 {
		t.Fatal(f.errors, f.fatals)
	}
	Assert(f, path, []byte("x 345\nz\n"), digits)
	if len(f.errors) != 1 || !strings.Contains(f.errors[0], "line 2") {
		t.Fatal(f.errors)
	}
}

func TestAssertUpdatesUnmasked(t *testing.T) {
	t.Setenv(UpdateEnv, "1")
	path := filepath.Join(t.TempDir(), "a.json")
	Assert(&fakeTB{}, path, []byte("x 1"), regexp.MustCompile(`[0-9]`))
	if b, _ := os.ReadFile(path); string(b) != "x 1" {
		t.Fatal(string(b))
	}
}

func TestAssertReportsShorterOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.json")
	os.WriteFile(path, []byte("x\ny\n"), 0o644)
	f := &fakeTB{}
	Assert(f, path, []byte("x\n"))
	if len(f.errors) != 1 || !strings.Contains(f.errors[0], "byte 2") {
		t.Fatal(f.errors)
	}
}

func TestAssertMissingFile(t *testing.T) {
	f := &fakeTB{}
	Assert(f, filepath.Join(t.TempDir(), "missing.json"), []byte("x"))
	if len(f.fatals) != 1 || !strings.Contains(f.fatals[0], "does not exist") {
		t.Fatal(f.fatals)
	}
}

func TestAssertUnreadableFile(t *testing.T) {
	f := &fakeTB{}
	Assert(f, t.TempDir(), []byte("x")) // a directory cannot be read as a file
	if len(f.fatals) != 1 {
		t.Fatal(f.fatals)
	}
}

func TestAssertUpdates(t *testing.T) {
	t.Setenv(UpdateEnv, "1")
	path := filepath.Join(t.TempDir(), "sub", "a.json")
	f := &fakeTB{}
	Assert(f, path, []byte("new"))
	if b, _ := os.ReadFile(path); string(b) != "new" || len(f.fatals) != 0 {
		t.Fatal(string(b), f.fatals)
	}
}

func TestAssertUpdateFails(t *testing.T) {
	t.Setenv(UpdateEnv, "1")
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	os.WriteFile(blocker, nil, 0o644)
	f := &fakeTB{}
	Assert(f, filepath.Join(blocker, "a.json"), []byte("new")) // parent is a file
	if len(f.fatals) != 1 {
		t.Fatal(f.fatals)
	}
}
