package fuzz_test

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	harness "github.com/thec1oud/dql/conformance/harness/go"
	"github.com/thec1oud/dql/conformance/fuzz"
)

func TestScenarioReplays(t *testing.T) {
	for s := range uint64(100) {
		c := fuzz.Generate(s)
		sc := c.Scenario()
		if !reflect.DeepEqual(sc, c.Scenario()) {
			t.Fatalf("seed %d generated two scenarios", s)
		}
		if !reflect.DeepEqual(c, fuzz.Generate(s)) {
			t.Fatalf("seed %d: the scenario changed its catalog", s)
		}
		if sc.Name != fmt.Sprintf("fuzz-%d", s) || !slices.Equal(sc.Requires, c.Features()) {
			t.Errorf("seed %d: %s requires %v", s, sc.Name, sc.Requires)
		}
	}
}

// TestScenarioSteps checks that every step names its table's columns, with
// operators and values that fit them, and that every kind of step occurs.
func TestScenarioSteps(t *testing.T) {
	seen := map[string]int{}
	for s := range uint64(seeds) {
		c := fuzz.Generate(s)
		sc := c.Scenario()
		for i, st := range sc.Steps {
			for _, err := range wellFormed(c, st) {
				t.Errorf("seed %d step %d %+v: %v", s, i+1, st, err)
			}
			kind := st.Op
			if _, op, ok := strings.Cut(st.Op, "."); ok {
				kind = op
			}
			if st.Read != "" {
				kind += " " + st.Read
			}
			if st.Offset > 0 {
				seen["offset"]++
			}
			if e, ok := st.Expect["error"].(map[string]any); ok {
				kind += " " + e["class"].(string)
			}
			for _, f := range st.Filter {
				seen[f.Op]++
			}
			seen[kind]++
		}
		last := sc.Steps[len(sc.Steps)-len(c.Tables):]
		for i, tb := range c.Tables {
			if last[i].Op != "select" || last[i].From != tb.Name || len(last[i].Filter) != 0 {
				t.Errorf("seed %d does not end reading %s: %+v", s, tb.Name, last[i])
			}
		}
	}
	for _, k := range []string{
		"select", "select count", "insert", "insert_or_ignore", "upsert", "patch", "delete", "delete ForeignKeyViolation",
		"insert UniqueViolation", "insert ForeignKeyViolation", "get NotFound", "offset",
		"eq", "ne", "lt", "le", "gt", "ge", "between", "in", "is_distinct_from", "like", "ilike", "starts_with", "contains", "is_null", "is_not_null",
	} {
		if seen[k] < 10 {
			t.Errorf("%d steps of %s", seen[k], k)
		}
	}
}

// wellFormed returns how st does not fit catalog c.
func wellFormed(c *fuzz.Catalog, st harness.Step) []error {
	name, op, _ := strings.Cut(st.Op, ".")
	if st.Op == "select" {
		name = st.From
	}
	tb := c.Table(name)
	if tb == nil {
		return []error{fmt.Errorf("no table %s", name)}
	}
	var errs []error
	fit := func(m any, partial bool) {
		row, _ := m.(map[string]any)
		for k, v := range row {
			col := tb.Column(k)
			if col == nil {
				errs = append(errs, fmt.Errorf("no column %s", k))
			} else if err := fits(*col, v); err != nil {
				errs = append(errs, err)
			}
		}
		if !partial && len(row) != len(tb.Columns) {
			errs = append(errs, fmt.Errorf("%d of %d columns", len(row), len(tb.Columns)))
		}
	}
	switch op {
	case "insert", "insert_or_ignore", "upsert":
		fit(st.Input, false)
	case "patch":
		fit(st.Input, true)
		fit(st.Key, true)
	case "delete", "get":
		fit(st.Key, true)
	case "":
		if st.Shape != name+"_full" {
			errs = append(errs, fmt.Errorf("shape %s", st.Shape))
		}
		for _, f := range st.Filter {
			if col := tb.Column(f.Column); col == nil {
				errs = append(errs, fmt.Errorf("no column %s", f.Column))
			} else if ops := operators[col.Kind]; !slices.Contains(ops, f.Op) && !(col.Null && strings.HasSuffix(f.Op, "null")) {
				errs = append(errs, fmt.Errorf("%s on %s", f.Op, f.Column))
			}
		}
		if st.Read == "" {
			n := len(st.Order) - len(tb.Key)
			if n < 0 || !slices.Equal(names(st.Order[n:]), tb.Key) {
				errs = append(errs, fmt.Errorf("order %v does not end with the key", st.Order))
			}
		}
	default:
		errs = append(errs, fmt.Errorf("op %s", op))
	}
	return errs
}

// operators are the filters each kind of column takes.
var operators = map[fuzz.Kind][]string{
	fuzz.Integer: {"eq", "ne", "lt", "le", "gt", "ge", "between", "in", "is_distinct_from"},
	fuzz.Text:    {"eq", "ne", "lt", "le", "gt", "ge", "between", "in", "is_distinct_from", "like", "ilike", "starts_with", "contains"},
	fuzz.Bool:    {"eq", "ne", "is_distinct_from"},
	fuzz.Decimal: {"eq", "ne", "lt", "le", "gt", "ge", "between", "in"},
}

func names(o []harness.OrderKey) []string {
	var ns []string
	for _, k := range o {
		ns = append(ns, k.Column)
	}
	return ns
}
