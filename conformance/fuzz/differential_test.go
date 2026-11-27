package fuzz_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/thec1oud/dql/conformance/fuzz"
	harness "github.com/thec1oud/dql/conformance/harness/go"
)

// SeedsEnv names the seeds TestDifferential runs, as from-to; it runs
// seeds 0 and 1 by default.
const SeedsEnv = "DQL_FUZZ_SEEDS"

func seedRange(t *testing.T) []uint64 {
	from, to := uint64(0), uint64(1)
	if v := os.Getenv(SeedsEnv); v != "" {
		a, b, _ := strings.Cut(v, "-")
		var err error
		if from, err = strconv.ParseUint(a, 10, 64); err == nil {
			to, err = strconv.ParseUint(b, 10, 64)
		}
		if err != nil {
			t.Fatalf("%s=%s: %v", SeedsEnv, v, err)
		}
	}
	var seeds []uint64
	for s := from; s <= to; s++ {
		seeds = append(seeds, s)
	}
	return seeds
}

// TestDifferential generates each seed's packages on every dialect, runs
// their scenarios, and fails on every disagreement with Postgres no
// fidelity label explains (guide §6.4).
func TestDifferential(t *testing.T) {
	ctx := context.Background()
	repo, _ := filepath.Abs("../..")
	r := &fuzz.Run{Seeds: seedRange(t), Dir: t.TempDir(), Repo: repo, Bases: map[string]string{}, DQL: filepath.Join(t.TempDir(), "dql")}
	for _, d := range fuzz.Dialects {
		if env := harness.Matrix[d]["shop"].URLEnv; env != "" {
			if r.Bases[d] = os.Getenv(env); r.Bases[d] == "" {
				t.Skipf("%s is not set", env)
			}
		}
	}
	build := exec.Command("go", "build", "-o", r.DQL, "./cmd/dql")
	build.Dir = repo
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building dql: %v\n%s", err, out)
	}
	if _, err := r.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if err := r.RunScenarios(ctx, out); err != nil {
		t.Fatal(err)
	}
	rep, err := r.Compare(out)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("compared %d seeds in %d packages", rep.Seeds, rep.Packages)
	for _, s := range rep.Skipped {
		t.Logf("skipped: %s", s)
	}
	for _, f := range rep.Findings {
		if f.Explained() {
			t.Logf("%s", f)
		} else {
			t.Errorf("%s\nreplay it with %s=%d-%d", f, SeedsEnv, f.Seed, f.Seed)
		}
	}
	for _, p := range rep.Reference {
		t.Errorf("on postgres, %s", p)
	}
}
