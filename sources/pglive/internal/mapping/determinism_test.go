package mapping

import (
	"bytes"
	"testing"

	"github.com/thec1oud/dql/core/bundle"
	"github.com/thec1oud/dql/sources/pglive/internal/introspect"
	"github.com/thec1oud/dql/testkit/determinism"
)

// TestDeterminism maps each Snapshot repeatedly and with every list that has
// no database order shuffled, and requires byte-identical output (§3.2,
// implementation guide §6.2).
func TestDeterminism(t *testing.T) {
	for _, s := range []*introspect.Snapshot{shop, fixture, examples} {
		gen := func(s *introspect.Snapshot) func() ([]byte, error) {
			return func() ([]byte, error) {
				c, _, err := Map(s, shopOptions)
				if err != nil {
					return nil, err
				}
				return bundle.MarshalJSON(c)
			}
		}
		if err := determinism.Repeat(3, gen(s)); err != nil {
			t.Fatal(err)
		}
		want, _ := gen(s)()
		for seed := range uint64(5) {
			got, err := gen(shuffled(seed, s))()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("seed %d: the catalog depends on the order of the Snapshot", seed)
			}
		}
	}
}

// shuffled returns a copy of s with every sorted list shuffled.
func shuffled(seed uint64, s *introspect.Snapshot) *introspect.Snapshot {
	c := *s
	sh := func(n int) uint64 { return seed*1000 + uint64(n) }
	c.Extensions = determinism.Shuffled(sh(1), s.Extensions)
	c.Schemas = determinism.Shuffled(sh(2), s.Schemas)
	for i := range c.Schemas {
		c.Schemas[i].Grants = determinism.Shuffled(sh(3), c.Schemas[i].Grants)
	}
	c.Collations = determinism.Shuffled(sh(4), s.Collations)
	c.Relations = determinism.Shuffled(sh(5), s.Relations)
	for i := range c.Relations {
		r := &c.Relations[i]
		r.Columns = append([]introspect.Column(nil), r.Columns...)
		for j := range r.Columns {
			r.Columns[j].Grants = determinism.Shuffled(sh(6), r.Columns[j].Grants)
		}
		r.DependsOn = determinism.Shuffled(sh(7), r.DependsOn)
		r.Constraints = determinism.Shuffled(sh(8), r.Constraints)
		r.Indexes = determinism.Shuffled(sh(9), r.Indexes)
		r.Triggers = determinism.Shuffled(sh(10), r.Triggers)
		r.Policies = determinism.Shuffled(sh(11), r.Policies)
		r.Grants = determinism.Shuffled(sh(12), r.Grants)
	}
	c.Enums = determinism.Shuffled(sh(13), s.Enums)
	c.Domains = determinism.Shuffled(sh(14), s.Domains)
	for i := range c.Domains {
		c.Domains[i].Checks = determinism.Shuffled(sh(15), c.Domains[i].Checks)
	}
	c.Composites = determinism.Shuffled(sh(16), s.Composites)
	c.Ranges = determinism.Shuffled(sh(17), s.Ranges)
	c.BaseTypes = determinism.Shuffled(sh(18), s.BaseTypes)
	c.Sequences = determinism.Shuffled(sh(19), s.Sequences)
	c.Routines = determinism.Shuffled(sh(20), s.Routines)
	for i := range c.Routines {
		c.Routines[i].Grants = determinism.Shuffled(sh(21), c.Routines[i].Grants)
	}
	c.Statistics = determinism.Shuffled(sh(22), s.Statistics)
	return &c
}
