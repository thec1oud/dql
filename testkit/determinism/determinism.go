// Package determinism checks that generation produces byte-identical output:
// across repeated runs, across shuffled inputs, and across process
// environments such as a Turkish locale (implementation guide §6.2).
package determinism

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"strings"
)

// Repeat runs gen n times and fails on an error or on any output that differs
// from the first.
func Repeat(n int, gen func() ([]byte, error)) error {
	var first []byte
	for i := 0; i < n; i++ {
		out, err := gen()
		if err != nil {
			return fmt.Errorf("determinism: run %d: %w", i+1, err)
		}
		if i == 0 {
			first = out
		} else if !bytes.Equal(out, first) {
			return fmt.Errorf("determinism: run %d differs from run 1", i+1)
		}
	}
	return nil
}

// Shuffled returns a copy of s in an order fixed by seed, for generating from
// shuffled introspection results.
func Shuffled[T any](seed uint64, s []T) []T {
	out := append([]T(nil), s...)
	r := rand.New(rand.NewPCG(seed, seed))
	r.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

// Environments are the process environments generation must be insensitive to.
var Environments = [][]string{
	{"LANG=C.UTF-8", "GOMAXPROCS=1"},
	{"LANG=tr_TR.UTF-8", "LC_ALL=tr_TR.UTF-8", "GOMAXPROCS=1"},
	{"LANG=C.UTF-8", "GOMAXPROCS=8"},
}

// Command runs name with args once per environment, each added to the current
// environment, and fails unless every run succeeds with identical stdout.
func Command(envs [][]string, name string, args ...string) error {
	var first []byte
	for i, env := range envs {
		cmd := exec.Command(name, args...)
		cmd.Env = append(os.Environ(), env...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("determinism: %s under %s: %w: %s", name, strings.Join(env, " "), err, stderr.String())
		}
		if i == 0 {
			first = out
		} else if !bytes.Equal(out, first) {
			return fmt.Errorf("determinism: %s output under %s differs from %s",
				name, strings.Join(env, " "), strings.Join(envs[0], " "))
		}
	}
	return nil
}
