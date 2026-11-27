package fuzz

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	harness "github.com/thec1oud/dql/conformance/harness/go"
)

// Reference is the dialect the others are compared with (guide question 24).
const Reference = "postgres"

// Finding is a step whose outcome on a dialect differs from Postgres's.
// It is explained when a fragment the step renders carries emulated or
// approximate fidelity on either dialect (design record §9.2), and is a
// failure otherwise.
type Finding struct {
	Seed      uint64
	Dialect   string
	Step      string
	Op        string
	Want, Got any
	// Fragments are the step's fragments that are not exact, with their
	// fidelity, on either dialect.
	Fragments []string
}

// Explained reports whether a fidelity label accounts for the finding.
func (f Finding) Explained() bool { return len(f.Fragments) > 0 }

func (f Finding) String() string {
	why := "unexplained"
	if f.Explained() {
		why = "explained by " + strings.Join(f.Fragments, ", ")
	}
	return fmt.Sprintf("seed %d step %s (%s) on %s, %s:\n  postgres: %s\n  %s: %s", f.Seed, f.Step, f.Op, f.Dialect, why, show(f.Want), f.Dialect, show(f.Got))
}

// show writes v as JSON.
func show(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// Report is what comparing a run's results found: the findings, the steps
// a dialect's package could not run, the problems of Postgres's runs,
// whose results the scenario expects, and how many seeds and packages it
// compared.
type Report struct {
	Findings  []Finding
	Skipped   []string
	Reference []string
	Seeds     int
	Packages  int
}

// Compare compares each seed's results in out, written by RunAll, with
// Postgres's, using the bundles Prepare wrote under dir. A seed's
// comparison on a dialect stops at the first write that differs, whose
// rows then differ too.
func (r *Run) Compare(out string) (Report, error) {
	var rep Report
	for _, seed := range r.Seeds {
		c := Generate(seed)
		ref, err := readResult(out, seed, Reference)
		if err != nil {
			return rep, err
		}
		rep.Seeds++
		rep.Packages++
		// The scenario expects Postgres's results where it knows them.
		for _, p := range ref.Problems {
			rep.Reference = append(rep.Reference, fmt.Sprintf("seed %d: %s", seed, p))
		}
		steps := c.Scenario().Steps
		for _, d := range Dialects[1:] {
			if len(c.Lacks(d)) > 0 {
				continue
			}
			got, err := readResult(out, seed, d)
			if err != nil {
				return rep, err
			}
			rep.Packages++
			// A step that failed or met another result than expected is
			// compared below; one the package could not run is skipped.
			for _, p := range got.Problems {
				if !strings.Contains(p, "unexpected error: ") && !strings.Contains(p, "want ") && !strings.Contains(p, "error class: ") {
					rep.Skipped = append(rep.Skipped, fmt.Sprintf("seed %d on %s: %s", seed, d, p))
				}
			}
			fidelity, err := r.fidelity(seed, d)
			if err != nil {
				return rep, err
			}
			byStep := map[string]harness.Outcome{}
			for _, o := range got.Outcomes {
				byStep[o.Step] = o
			}
			for _, want := range ref.Outcomes {
				g, ok := byStep[want.Step]
				if !ok || same(want, g) {
					continue
				}
				st := steps[must(index(want.Step))]
				rep.Findings = append(rep.Findings, Finding{
					Seed: seed, Dialect: d, Step: want.Step, Op: want.Op, Want: compared(want), Got: compared(g),
					Fragments: unexact(fragments(st), fidelity),
				})
				if st.Op != "select" {
					break
				}
			}
		}
	}
	return rep, nil
}

// index is the 0-based index of step, numbered from 1.
func index(step string) (int, error) {
	var i int
	_, err := fmt.Sscan(step, &i)
	return i - 1, err
}

// readResult reads a package's Result from out.
func readResult(out string, seed uint64, d string) (Result, error) {
	var res Result
	b, err := os.ReadFile(filepath.Join(out, fmt.Sprintf("s%d-%s.json", seed, d)))
	if err == nil {
		err = json.Unmarshal(b, &res)
	}
	return res, err
}

// compared is what of an outcome is compared: its result, or its error's
// class. Constraint names and messages are each dialect's own.
func compared(o harness.Outcome) any {
	if o.Error != nil {
		return map[string]any{"error": o.Error["class"]}
	}
	return o.Result
}

// same reports whether two outcomes of a step agree.
func same(a, b harness.Outcome) bool { return reflect.DeepEqual(compared(a), compared(b)) }

// fidelity reads the fidelity of each fragment the bundles of seed on
// Postgres and on d carry that is not exact, as "id (dialect: fidelity)".
func (r *Run) fidelity(seed uint64, d string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, dialect := range []string{Reference, d} {
		var b struct {
			Queries struct {
				Fragments struct {
					Fragments []struct{ ID, Fidelity string }
				}
			}
		}
		raw, err := os.ReadFile(filepath.Join(r.Dir, fmt.Sprintf("s%d", seed), dialect, "bundle.json"))
		if err == nil {
			err = json.Unmarshal(raw, &b)
		}
		if err != nil {
			return nil, err
		}
		for _, f := range b.Queries.Fragments.Fragments {
			if f.Fidelity != "" && f.Fidelity != "FIDELITY_EXACT" {
				out[f.ID] = append(out[f.ID], fmt.Sprintf("%s (%s: %s)", f.ID, dialect, strings.ToLower(strings.TrimPrefix(f.Fidelity, "FIDELITY_"))))
			}
		}
	}
	return out, nil
}

// writes are the prefixes of the fragments each kind of write renders.
var writes = map[string][]string{
	"insert":           {"insert", "values", "returning", "value"},
	"insert_or_ignore": {"insert", "values", "returning", "value", "ignore"},
	"upsert":           {"insert", "values", "returning", "value", "upsert", "excluded"},
	"patch":            {"update", "set", "returning", "value"},
	"delete":           {"delete"},
	"get":              {"select"},
}

// fragments are the ids, or id prefixes ending in a dot, of the fragments
// st renders.
func fragments(st harness.Step) []string {
	if st.Op != "select" {
		_, op, _ := strings.Cut(st.Op, ".")
		var ps []string
		for _, p := range writes[op] {
			ps = append(ps, p+".")
		}
		return ps
	}
	ids := []string{"select.", "from."}
	for _, f := range st.Filter {
		ids = append(ids, "op."+f.Op)
	}
	for _, o := range st.Order {
		ids = append(ids, map[bool]string{false: "order.asc", true: "order.desc"}[o.Desc])
	}
	if st.Limit > 0 || st.Offset > 0 {
		ids = append(ids, "clause.limit", "clause.offset", "clause.page")
	}
	if st.Read == "count" {
		ids = append(ids, "count.")
	}
	return ids
}

// unexact returns the labels of ids, or of the fragments their prefixes
// name, that are not exact.
func unexact(ids []string, fidelity map[string][]string) []string {
	var out []string
	for id, labels := range fidelity {
		for _, want := range ids {
			if id == want || strings.HasSuffix(want, ".") && strings.HasPrefix(id, want) {
				out = append(out, labels...)
				break
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
