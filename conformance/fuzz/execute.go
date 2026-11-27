package fuzz

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	harness "github.com/thec1oud/dql/conformance/harness/go"
	"github.com/thec1oud/dql/runtime/go/dql"
	"github.com/thec1oud/dql/runtime/go/dqlmysql"
	"github.com/thec1oud/dql/runtime/go/dqlpgx"
	"github.com/thec1oud/dql/runtime/go/dqlsqlite"
	"github.com/thec1oud/dql/runtime/go/dqlsqlserver"
)

// OutEnv names the directory RunAll writes each package's outcomes to.
const OutEnv = "DQL_FUZZ_OUT"

// drivers make each dialect's driver.
var drivers = map[string]func() dql.Driver{
	"postgres":  func() dql.Driver { return dqlpgx.New() },
	"mysql":     func() dql.Driver { return dqlmysql.New() },
	"mariadb":   func() dql.Driver { return dqlmysql.New() },
	"sqlite":    func() dql.Driver { return dqlsqlite.New() },
	"sqlserver": func() dql.Driver { return dqlsqlserver.New() },
}

// Result is what a package's run of its seed's scenario produced: each
// step's outcome, and the problems that kept steps from running, such as a
// method the package lacks.
type Result struct {
	Outcomes []harness.Outcome `json:"outcomes"`
	Problems []string          `json:"problems"`
}

// TB is the part of testing.TB RunAll uses.
type TB interface {
	Fatal(args ...any)
	Logf(format string, args ...any)
}

// RunAll runs each package's scenario on a fresh database of its dialect,
// on the server its schema's URL environment variable names, and writes
// its Result as s<seed>-<dialect>.json into the directory OutEnv names.
func RunAll(t TB, pkgs []Package) {
	ctx := context.Background()
	for _, p := range pkgs {
		c := Generate(p.Seed)
		script, _ := c.Script(p.Dialect)
		res, err := Execute(ctx, p, script, c.Scenario(), os.Getenv(harness.Matrix[p.Dialect]["shop"].URLEnv))
		if err == nil {
			var b []byte
			b, err = json.MarshalIndent(res, "", "  ")
			if err == nil {
				err = os.WriteFile(filepath.Join(os.Getenv(OutEnv), fmt.Sprintf("s%d-%s.json", p.Seed, p.Dialect)), b, 0o644)
			}
		}
		if err != nil {
			t.Fatal(fmt.Sprintf("seed %d on %s: %v", p.Seed, p.Dialect, err))
		}
		t.Logf("seed %d on %s: %d outcomes, %d problems", p.Seed, p.Dialect, len(res.Outcomes), len(res.Problems))
	}
}

// Execute runs scenario sc with package p on a fresh database from script
// on the server base names.
func Execute(ctx context.Context, p Package, script string, sc harness.Scenario, base string) (Result, error) {
	file := filepath.Join(os.TempDir(), fmt.Sprintf("dql-fuzz-%d-%d-%s.sql", os.Getpid(), p.Seed, p.Dialect))
	if err := os.WriteFile(file, []byte(script), 0o644); err != nil {
		return Result{}, err
	}
	defer os.Remove(file)
	s := harness.Matrix[p.Dialect]["shop"]
	dsn, drop, err := s.Database(ctx, base, file)
	if err != nil {
		return Result{}, err
	}
	defer drop()
	exec, err := p.NewExecutor(ctx, drivers[p.Dialect](), dql.Config{
		Sources:  map[string]dql.SourceConfig{"fz": {Primary: dql.ConnConfig{DSN: dsn, MaxConns: 2}}},
		Catalogs: map[string]string{p.Catalog: "fz"},
	})
	if err != nil {
		return Result{}, err
	}
	defer exec.Close()
	res := Result{Outcomes: []harness.Outcome{}}
	schema := harness.Schema{Dialect: s.Dialect, Tables: p.Tables, Shapes: p.Shapes, Selects: p.Selects}
	res.Problems = harness.Run(ctx, sc, harness.Target{
		Schema: schema, DB: p.New(exec), Exec: exec, DSN: dsn, Events: &harness.Recorder{},
		Record: func(o harness.Outcome) { res.Outcomes = append(res.Outcomes, o) },
	})
	return res, nil
}
