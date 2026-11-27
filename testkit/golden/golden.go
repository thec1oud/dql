// Package golden compares test output with files under testdata. Golden files
// change only when DQL_UPDATE_GOLDEN=1 is set, and every change is reviewed as
// a diff (implementation guide §6.1).
package golden

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
)

// UpdateEnv is the environment variable that rewrites golden files.
const UpdateEnv = "DQL_UPDATE_GOLDEN"

// TB is the part of testing.TB that Assert needs.
type TB interface {
	Helper()
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
}

// Assert fails t unless got equals the contents of path once every match of
// masks is blanked in both. With UpdateEnv set to 1 it writes got, unmasked,
// to path instead.
func Assert(t TB, path string, got []byte, masks ...*regexp.Regexp) {
	t.Helper()
	if os.Getenv(UpdateEnv) == "1" {
		if err := write(path, got); err != nil {
			t.Fatalf("golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("golden: %s does not exist; run with %s=1 to create it", path, UpdateEnv)
		return
	}
	if err != nil {
		t.Fatalf("golden: %v", err)
		return
	}
	got, want = Mask(got, masks...), Mask(want, masks...)
	if !bytes.Equal(got, want) {
		t.Errorf("golden: %s differs at %s; run with %s=1 to update it", path, firstDifference(got, want), UpdateEnv)
	}
}

// Masked replaces what a mask matches.
const Masked = "<masked>"

// Mask returns b with every match of masks replaced by Masked.
func Mask(b []byte, masks ...*regexp.Regexp) []byte {
	for _, m := range masks {
		b = m.ReplaceAllLiteral(b, []byte(Masked))
	}
	return b
}

func write(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// firstDifference names the line and byte offset of the first difference.
func firstDifference(a, b []byte) string {
	line := 1
	for i := 0; ; i++ {
		if i == len(a) || i == len(b) || a[i] != b[i] {
			return fmt.Sprintf("line %d (byte %d)", line, i)
		}
		if a[i] == '\n' {
			line++
		}
	}
}
