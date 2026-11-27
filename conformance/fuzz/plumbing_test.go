package fuzz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	harness "github.com/thec1oud/dql/conformance/harness/go"
	"github.com/thec1oud/dql/emitters/go/testdata/shopdb"
	"github.com/thec1oud/dql/runtime/go/dql"
)

// write writes v into dir/name, as JSON unless it is a string.
func write(t *testing.T, dir, name string, v any) {
	t.Helper()
	b, ok := v.(string)
	if !ok {
		j, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		b = string(j)
	}
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(b), 0o644); err != nil {
		t.Fatal(err)
	}
}

// bundle is a bundle.json holding fragments, by id and fidelity.
func bundle(frags ...string) map[string]any {
	var fs []map[string]string
	for i := 0; i < len(frags); i += 2 {
		fs = append(fs, map[string]string{"id": frags[i], "fidelity": frags[i+1]})
	}
	return map[string]any{"queries": map[string]any{"fragments": map[string]any{"fragments": fs}}}
}

func outcome(step, op string, result any) harness.Outcome {
	return harness.Outcome{Step: step, Op: op, Result: result}
}

// compareFixture writes the results and bundles of seeds 0 and 1: seed 0
// differs on mysql at its first step, a write, and at its third, a select,
// which the write's difference hides; seed 1 differs on sqlserver at its
// first step, a select, unexplained, and at its second, a count,
// explained by an emulated count fragment.
func compareFixture(t *testing.T) (*Run, string) {
	r := &Run{Seeds: []uint64{0, 1}, Dir: t.TempDir()}
	out := t.TempDir()
	for _, seed := range r.Seeds {
		for _, d := range Dialects {
			if len(Generate(seed).Lacks(d)) > 0 {
				continue
			}
			b := bundle("select.x", "FIDELITY_EXACT", "insert.x", "")
			if seed == 1 && d == "sqlserver" {
				b = bundle("count.x", "FIDELITY_EMULATED", "op.eq", "FIDELITY_APPROXIMATE")
			}
			write(t, r.Dir, fmt.Sprintf("s%d/%s/bundle.json", seed, d), b)
			res := Result{Outcomes: []harness.Outcome{
				outcome("1", "x", 1), outcome("2", "x", 2), outcome("3", "x", 3),
			}}
			switch {
			case seed == 0 && d == Reference:
				res.Problems = []string{"step 9: unexpected error: boom"}
			case seed == 0 && d == "mysql":
				res.Outcomes[0].Error = map[string]any{"class": "Conflict", "message": "dup"}
				res.Outcomes[2].Result = 4
				res.Problems = []string{"step 1: unexpected error: x", "step 2: want 2", "step 3: error class: x", "step 4: t1 has no Ilike"}
			case seed == 0 && d == "mariadb":
				res.Outcomes = res.Outcomes[1:]
			case seed == 1 && d == "sqlserver":
				res.Outcomes[0].Result = 9
				res.Outcomes[1].Result = 9
			}
			write(t, out, fmt.Sprintf("s%d-%s.json", seed, d), res)
		}
	}
	return r, out
}

func TestCompare(t *testing.T) {
	r, out := compareFixture(t)
	rep, err := r.Compare(out)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Seeds != 2 || rep.Packages != 9 {
		t.Errorf("compared %d seeds in %d packages, want 2 in 9", rep.Seeds, rep.Packages)
	}
	if want := []string{"seed 0 on mysql: step 4: t1 has no Ilike"}; !reflect.DeepEqual(rep.Skipped, want) {
		t.Errorf("skipped %q, want %q", rep.Skipped, want)
	}
	if want := []string{"seed 0: step 9: unexpected error: boom"}; !reflect.DeepEqual(rep.Reference, want) {
		t.Errorf("reference problems %q, want %q", rep.Reference, want)
	}
	var got []string
	for _, f := range rep.Findings {
		got = append(got, f.String())
	}
	want := []string{
		"seed 0 step 1 (x) on mysql, unexplained:\n  postgres: 1\n  mysql: {\"error\":\"Conflict\"}",
		"seed 1 step 1 (x) on sqlserver, unexplained:\n  postgres: 1\n  sqlserver: 9",
		"seed 1 step 2 (x) on sqlserver, explained by count.x (sqlserver: emulated):\n  postgres: 2\n  sqlserver: 9",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("findings\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestCompareMissing(t *testing.T) {
	for name, remove := range map[string]string{
		"reference result": "out/s0-postgres.json",
		"dialect result":   "out/s0-mysql.json",
		"bundle":           "dir/s0/mysql/bundle.json",
	} {
		t.Run(name, func(t *testing.T) {
			r, out := compareFixture(t)
			p := filepath.Join(out, filepath.Base(remove))
			if strings.HasPrefix(remove, "dir/") {
				p = filepath.Join(r.Dir, strings.TrimPrefix(remove, "dir/"))
			}
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			if _, err := r.Compare(out); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("got %v, want a missing file", err)
			}
		})
	}
	r, out := compareFixture(t)
	write(t, r.Dir, "s0/postgres/bundle.json", "{")
	if _, err := r.Compare(out); err == nil {
		t.Error("a malformed bundle compared")
	}
}

func TestFragments(t *testing.T) {
	for _, tc := range []struct {
		step harness.Step
		want []string
	}{
		{harness.Step{Op: "t1.upsert"}, []string{"insert.", "values.", "returning.", "value.", "upsert.", "excluded."}},
		{harness.Step{Op: "t1.delete"}, []string{"delete."}},
		{harness.Step{Op: "select"}, []string{"select.", "from."}},
		{harness.Step{Op: "select", Filter: []harness.Filter{{Op: "eq"}}, Order: []harness.OrderKey{{}, {Desc: true}}, Offset: 2, Read: "count"},
			[]string{"select.", "from.", "op.eq", "order.asc", "order.desc", "clause.limit", "clause.offset", "clause.page", "count."}},
	} {
		if got := fragments(tc.step); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("fragments of %+v = %q, want %q", tc.step, got, tc.want)
		}
	}
	fid := map[string][]string{"op.eq": {"a"}, "insert.t1": {"b", "c"}, "order.asc": {"a"}}
	if got, want := unexact([]string{"op.eq", "insert.", "order.asc"}, fid), []string{"a", "b", "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("unexact = %q, want %q", got, want)
	}
}

func TestIndex(t *testing.T) {
	if i, err := index("x"); err == nil {
		t.Errorf("index(x) = %d", i)
	}
}

// failing is a TB that records what fails.
type failing struct{ fatal []string }

func (f *failing) Fatal(args ...any)            { f.fatal = append(f.fatal, fmt.Sprint(args...)) }
func (f *failing) Logf(format string, _ ...any) {}

// shop is the Postgres shop schema's package, as a fuzzed package of seed
// 0: its scenario's steps name tables shop lacks, which are its problems.
func shop() Package {
	s := harness.Schemas["shop"]
	return Package{
		Seed: 0, Dialect: Reference, Catalog: "shop",
		New:         func(e *dql.Executor) any { return shopdb.New(e) },
		NewExecutor: shopdb.NewExecutor, Tables: s.Tables, Shapes: s.Shapes, Selects: s.Selects,
	}
}

func base(t *testing.T) string {
	b := os.Getenv(harness.Schemas["shop"].URLEnv)
	if b == "" {
		t.Skipf("%s is not set", harness.Schemas["shop"].URLEnv)
	}
	return b
}

func TestExecute(t *testing.T) {
	b := base(t)
	ctx := context.Background()
	schema, err := os.ReadFile("../../testdata/shop/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sc := harness.Scenario{Name: "x", Schema: "shop", Steps: []harness.Step{
		{Op: "tags.insert", Input: map[string]any{"name": "db"}},
		{Op: "select", From: "tags", Shape: "tag_full"},
	}}
	res, err := Execute(ctx, shop(), string(schema), sc, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Problems) > 0 || len(res.Outcomes) != 2 {
		t.Fatalf("got %+v", res)
	}
	if got := show(res.Outcomes[1].Result); !strings.Contains(got, `"name":"db"`) {
		t.Errorf("select = %s", got)
	}

	// A package whose executor cannot open.
	p := shop()
	p.NewExecutor = func(context.Context, dql.Driver, dql.Config, ...dql.Option) (*dql.Executor, error) {
		return nil, errors.New("no executor")
	}
	if _, err := Execute(ctx, p, string(schema), sc, b); err == nil || err.Error() != "no executor" {
		t.Errorf("got %v", err)
	}
	// A script the server refuses.
	if _, err := Execute(ctx, shop(), "not sql", sc, b); err == nil {
		t.Error("a malformed script ran")
	}
	// No temporary directory to write the script into.
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "none"))
	if _, err := Execute(ctx, shop(), string(schema), sc, b); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("got %v", err)
	}
}

func TestRunAll(t *testing.T) {
	t.Setenv(harness.Schemas["shop"].URLEnv, base(t))
	out := t.TempDir()
	t.Setenv(OutEnv, out)
	var f failing
	RunAll(&f, []Package{shop()})
	if len(f.fatal) > 0 {
		t.Fatal(f.fatal)
	}
	res, err := readResult(out, 0, Reference)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Problems) == 0 {
		t.Error("seed 0's steps ran on shop")
	}

	t.Setenv(OutEnv, filepath.Join(out, "none"))
	RunAll(&f, []Package{shop()})
	if len(f.fatal) != 1 || !strings.HasPrefix(f.fatal[0], "seed 0 on postgres: ") {
		t.Errorf("got %q", f.fatal)
	}
}

// prepared is a Run of seed 0 in a fresh directory, with dql the program
// that generates its code.
func prepared(t *testing.T, dql string) *Run {
	t.Helper()
	repo, _ := filepath.Abs("../..")
	r := &Run{Seeds: []uint64{0}, DQL: dql, Dir: t.TempDir(), Repo: repo, Bases: map[string]string{}}
	for _, d := range Dialects {
		if env := harness.Matrix[d]["shop"].URLEnv; env != "" {
			if r.Bases[d] = os.Getenv(env); r.Bases[d] == "" {
				t.Skipf("%s is not set", env)
			}
		}
	}
	return r
}

func TestPrepareFails(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		dql   string
		block string // a path made a directory, so nothing writes it
		setup func(r *Run)
		want  string
	}{
		"no directory":       {dql: "true", setup: func(r *Run) { write(t, r.Dir, "s0", "") }, want: "not a directory"},
		"no script":          {dql: "true", block: "s0/postgres/catalog.sql", want: "is a directory"},
		"no server":          {dql: "true", setup: func(r *Run) { r.Bases[Reference] = "postgres://nobody@127.0.0.1:1/x?connect_timeout=1" }, want: "seed 0 on postgres"},
		"no config":          {dql: "true", block: "s0/postgres/dql.yaml", want: "is a directory"},
		"generate fails":     {dql: "false", want: "dql generate: exit status 1"},
		"no glue":            {dql: "true", block: "s0/postgres/dql-fuzz.go", want: "is a directory"},
		"no workspace":       {dql: "true", setup: func(r *Run) { r.Repo = t.TempDir() }, want: "go.work"},
		"no module":          {dql: "true", block: "go.mod", want: "is a directory"},
		"no workspace sum":   {dql: "true", setup: func(r *Run) { r.Repo = workspace(t, false) }, want: ""},
		"with workspace sum": {dql: "true", setup: func(r *Run) { r.Repo = workspace(t, true) }, want: ""},
	} {
		t.Run(name, func(t *testing.T) {
			r := prepared(t, must(exec.LookPath(tc.dql)))
			if tc.block != "" {
				if err := os.MkdirAll(filepath.Join(r.Dir, tc.block), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if tc.setup != nil {
				tc.setup(r)
			}
			_, err := r.Prepare(ctx)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
}

// workspace is a repository root holding a go.work, and a go.work.sum if
// sum.
func workspace(t *testing.T, sum bool) string {
	dir := t.TempDir()
	write(t, dir, "go.work", "go 1.25.0\n\nuse (\n\t./a\n)\n")
	if sum {
		write(t, dir, "go.work.sum", "")
	}
	return dir
}

func TestRunScenariosFails(t *testing.T) {
	r := &Run{Dir: t.TempDir()}
	if err := r.RunScenarios(context.Background(), t.TempDir()); err == nil || !strings.Contains(err.Error(), "running the scenarios") {
		t.Errorf("got %v", err)
	}
}

func TestDrivers(t *testing.T) {
	for _, d := range Dialects {
		if drivers[d]() == nil {
			t.Errorf("no %s driver", d)
		}
	}
}
