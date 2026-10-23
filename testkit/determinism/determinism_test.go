package determinism

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestRepeatAcceptsStableOutput(t *testing.T) {
	if err := Repeat(5, func() ([]byte, error) { return []byte("same"), nil }); err != nil {
		t.Fatal(err)
	}
}

func TestRepeatDetectsDifference(t *testing.T) {
	n := 0
	err := Repeat(3, func() ([]byte, error) { n++; return []byte{byte(n)}, nil })
	if err == nil || !strings.Contains(err.Error(), "run 2 differs") {
		t.Fatal(err)
	}
}

func TestRepeatReportsError(t *testing.T) {
	boom := errors.New("boom")
	if err := Repeat(2, func() ([]byte, error) { return nil, boom }); !errors.Is(err, boom) {
		t.Fatal(err)
	}
}

func TestShuffledIsFixedBySeedAndCopies(t *testing.T) {
	in := []int{1, 2, 3, 4, 5, 6, 7, 8}
	a, b := Shuffled(7, in), Shuffled(7, in)
	if !slices.Equal(a, b) {
		t.Fatal("same seed gave different orders")
	}
	if !slices.Equal(in, []int{1, 2, 3, 4, 5, 6, 7, 8}) {
		t.Fatal("input was modified")
	}
	sorted := slices.Clone(a)
	slices.Sort(sorted)
	if !slices.Equal(sorted, in) {
		t.Fatal("shuffle lost elements")
	}
}

func TestCommandAcceptsStableOutput(t *testing.T) {
	if err := Command(Environments, "sh", "-c", "echo stable"); err != nil {
		t.Fatal(err)
	}
}

func TestCommandDetectsLocaleDependence(t *testing.T) {
	err := Command(Environments, "sh", "-c", `echo "$LANG"`)
	if err == nil || !strings.Contains(err.Error(), "tr_TR") {
		t.Fatal(err)
	}
}

func TestCommandReportsFailure(t *testing.T) {
	err := Command(Environments, "sh", "-c", "echo bad >&2; exit 3")
	if err == nil || !strings.Contains(err.Error(), "bad") {
		t.Fatal(err)
	}
}
