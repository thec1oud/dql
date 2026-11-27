package fuzz_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/thec1oud/dql/conformance/fuzz"
	harness "github.com/thec1oud/dql/conformance/harness/go"
	"github.com/thec1oud/dql/testkit/golden"
)

// seeds is how many seeds the tests generate.
const seeds = 500

func TestGenerateReplays(t *testing.T) {
	distinct := map[string]bool{}
	for s := range uint64(seeds) {
		c := fuzz.Generate(s)
		if !reflect.DeepEqual(c, fuzz.Generate(s)) {
			t.Fatalf("seed %d generated two catalogs", s)
		}
		distinct[fmt.Sprintf("%+v", c.Tables)] = true
	}
	if len(distinct) < seeds*9/10 {
		t.Errorf("%d seeds generated %d catalogs", seeds, len(distinct))
	}
}

// TestRowsHold checks every generated row against its table's columns,
// keys, checks and foreign keys, as a database would.
func TestRowsHold(t *testing.T) {
	var rows, features int
	for s := range uint64(seeds) {
		c := fuzz.Generate(s)
		features += len(c.Features())
		for i, tb := range c.Tables {
			rows += len(tb.Rows)
			for _, f := range tb.Foreign {
				if p := c.Table(f.Table); p == nil || slices.Index(c.Tables, p) >= i {
					t.Fatalf("seed %d: %s references %s, which is not before it", s, tb.Name, f.Table)
				}
			}
			for _, err := range holds(c, tb) {
				t.Errorf("seed %d: %s: %v", s, tb.Name, err)
			}
		}
	}
	if rows < seeds*10 || features < seeds/4 {
		t.Errorf("%d seeds generated %d rows and %d features", seeds, rows, features)
	}
	if c := fuzz.Generate(1); c.Table("none") != nil || c.Tables[0].Column("none") != nil || c.Tables[0].Column("id").Kind != fuzz.Integer {
		t.Error("found a table or column that is not there")
	}
}

// holds returns how t's rows break its definition.
func holds(c *fuzz.Catalog, t *fuzz.Table) []error {
	var errs []error
	seen := map[string]bool{}
	for _, r := range t.Rows {
		value := func(name string) any {
			return r[slices.IndexFunc(t.Columns, func(c fuzz.Column) bool { return c.Name == name })]
		}
		for i, col := range t.Columns {
			if err := fits(col, r[i]); err != nil {
				errs = append(errs, err)
			}
		}
		keys := []string{fmt.Sprint("key", t.Key, slices.Collect(func(yield func(any) bool) {
			for _, k := range t.Key {
				yield(value(k))
			}
		}))}
		for _, u := range t.Uniques {
			keys = append(keys, fmt.Sprint(u, value(u)))
		}
		for _, k := range keys {
			if seen[k] {
				errs = append(errs, fmt.Errorf("%s repeats", k))
			}
			seen[k] = true
		}
		for _, k := range t.Checks {
			if v := value(k.Column); v != nil && !checks(k, v) {
				errs = append(errs, fmt.Errorf("%v breaks %+v", v, k))
			}
		}
		for _, f := range t.Foreign {
			v := value(f.Column)
			if v != nil && !slices.ContainsFunc(c.Table(f.Table).Rows, func(p []any) bool { return p[0] == v }) {
				errs = append(errs, fmt.Errorf("%s %v references no row", f.Column, v))
			}
		}
	}
	return errs
}

// fits reports how v does not fit col.
func fits(col fuzz.Column, v any) error {
	if v == nil {
		if col.Null {
			return nil
		}
		return fmt.Errorf("%s is NULL", col.Name)
	}
	ok := false
	switch col.Kind {
	case fuzz.Integer:
		_, ok = v.(int64)
	case fuzz.Text:
		s, _ := v.(string)
		ok = len(s) <= col.Max && strings.Trim(s, " ") == s && strings.Trim(s, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789 ") == ""
	case fuzz.Bool:
		_, ok = v.(bool)
	case fuzz.Decimal:
		d, _ := v.(fuzz.DecimalValue)
		digits, neg := strings.CutPrefix(string(d), "-")
		whole, frac, dot := strings.Cut(digits, ".")
		ok = dot == (col.S > 0) && len(frac) == col.S && whole != "" && len(whole) <= max(col.P-col.S, 1) &&
			strings.Trim(whole+frac, "0123456789") == "" && !(neg && strings.Trim(whole+frac, "0") == "")
	}
	if !ok {
		return fmt.Errorf("%s holds %#v", col.Name, v)
	}
	return nil
}

// checks reports whether v holds k.
func checks(k fuzz.Check, v any) bool {
	switch k.Kind {
	case fuzz.NotEmpty:
		return v != ""
	case fuzz.MinLength:
		return len(v.(string)) >= k.N
	case fuzz.Membership:
		return slices.Contains(k.Labels, v.(string))
	}
	return v.(int64) >= k.Lo && v.(int64) <= k.Hi
}

// golden seeds: one catalog every dialect holds, one with booleans and
// decimals.
var goldens = map[string]uint64{"portable": 0, "features": 0}

func init() {
	for s := uint64(1); goldens["portable"] == 0 || goldens["features"] == 0; s++ {
		c := fuzz.Generate(s)
		name := "portable"
		if len(c.Features()) == 2 {
			name = "features"
		} else if len(c.Features()) > 0 || len(c.Tables) < 3 {
			continue
		}
		if goldens[name] == 0 {
			goldens[name] = s
		}
	}
}

func TestScripts(t *testing.T) {
	for name, seed := range goldens {
		c := fuzz.Generate(seed)
		for _, d := range fuzz.Dialects {
			got, err := c.Script(d)
			if len(c.Lacks(d)) > 0 {
				if err == nil || !strings.Contains(err.Error(), d+" lacks booleans and decimals") {
					t.Errorf("%s on %s: %v", name, d, err)
				}
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			golden.Assert(t, filepath.Join("testdata", name+"-"+d+".sql"), []byte(got))
		}
	}
	if _, err := fuzz.Generate(1).Script("oracle"); err == nil || err.Error() != "fuzz: no dialect oracle" {
		t.Error(err)
	}
}

// applied is how many seeds TestApply creates on every dialect.
const applied = 40

// TestApply creates catalogs on every dialect's server: each holds its
// definition and accepts its rows.
func TestApply(t *testing.T) {
	ctx := context.Background()
	for _, d := range fuzz.Dialects {
		t.Run(d, func(t *testing.T) {
			t.Parallel()
			s := harness.Matrix[d]["shop"]
			base := ""
			if s.URLEnv != "" {
				if base = os.Getenv(s.URLEnv); base == "" {
					t.Skipf("%s is not set", s.URLEnv)
				}
			}
			n := 0
			for seed := range uint64(applied) {
				c := fuzz.Generate(seed)
				script, err := c.Script(d)
				if err != nil {
					continue
				}
				n++
				file := filepath.Join(t.TempDir(), "catalog.sql")
				if err := os.WriteFile(file, []byte(script), 0o644); err != nil {
					t.Fatal(err)
				}
				_, drop, err := s.Database(ctx, base, file)
				if err != nil {
					t.Fatalf("seed %d: %v\n%s", seed, err, script)
				}
				drop()
			}
			if n < applied/2 {
				t.Errorf("%d of %d seeds are written for %s", n, applied, d)
			}
		})
	}
}

// A link table's two tables are related by nothing else: a second relation
// between them would take the same name (§4.5).
func TestLinksAreAlone(t *testing.T) {
	for s := range uint64(seeds) {
		c := fuzz.Generate(s)
		pairs := map[[2]string]int{}
		for _, tb := range c.Tables {
			for _, f := range tb.Foreign {
				pair := [2]string{tb.Name, f.Table}
				if len(tb.Key) == 2 {
					pair = [2]string{tb.Foreign[0].Table, tb.Foreign[1].Table}
				}
				slices.Sort(pair[:])
				pairs[pair]++
			}
		}
		for _, tb := range c.Tables {
			if len(tb.Key) != 2 {
				continue
			}
			pair := [2]string{tb.Foreign[0].Table, tb.Foreign[1].Table}
			slices.Sort(pair[:])
			if pairs[pair] != 2 {
				t.Errorf("seed %d: %s links %v, which more relate", s, tb.Name, pair)
			}
		}
	}
}
