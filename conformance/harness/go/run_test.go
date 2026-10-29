package harness

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Each way a step can go wrong is reported, and nothing else is.
func TestRunProblems(t *testing.T) {
	db, tables := open(t, "shop")
	ctx := context.Background()
	setup := `
- op: tenants.insert
  input: { name: acme }
  save: acme
- op: users.insert
  scope: { tenant_id: $acme.id }
  input: { email: a@acme.com, password_hash: x }
  save: ann
`
	scope := "  scope: { tenant_id: $acme.id }\n"
	for _, c := range []struct {
		step string
		want []string
	}{
		{"- op: users.count\n" + scope + "  expect: { value: 1 }\n", nil},
		{"- op: nope.insert\n", []string{"no store nope"}},
		{"- op: tenants.nope\n", []string{"no method nope on tenants"}},
		{"- op: users.count\n  scope: { tenant_id: [1] }\n", []string{"scope: "}},
		{"- op: users.get\n" + scope + "  key: { id: x }\n", []string{"key: "}},
		{"- op: tenants.insert\n  input: { name: 1 }\n", []string{"input: "}},
		{"- op: users.delete_where\n" + scope + "  where: some\n", []string{`where "some": only all is supported`}},
		{"- op: users.get\n" + scope + "  key: { id: $nope.id }\n", []string{"key: no saved row nope"}},
		{"- op: users.get\n" + scope + "  key: { id: $ann.nope }\n", []string{"key: saved row ann has no nope"}},
		{"- op: posts.insert\n" + scope + "  input: { author_id: $ann.id, title: x, labels: [a, $nope.x] }\n", []string{"input: no saved row nope"}},
		{"- op: posts.insert\n" + scope + "  input: { author_id: $ann.id, title: x, labels: [a] }\n  expect: { row: { labels: [a] } }\n", nil},
		{"- op: posts.insert\n" + scope + "  input: { author_id: $ann.id, title: x }\n  assign: [{ column: nope, set: now }]\n", []string{"no column nope of posts"}},
		{"- op: posts.insert\n" + scope + "  input: { author_id: $ann.id, title: x }\n  assign: [{ column: title }]\n", []string{"assign title: no expression"}},
		{"- op: posts.insert\n" + scope + "  input: { author_id: $ann.id, title: x }\n  assign: [{ column: title, set: now }]\n", []string{"column title cannot be set to now"}},
		{"- op: posts.patch\n" + scope + "  key: { id: 1 }\n  input: {}\n  assign: [{ column: title, add: 1 }]\n", []string{"column title has no Add"}},
		{"- op: posts.patch\n" + scope + "  key: { id: 1 }\n  input: {}\n  assign: [{ column: author_id, sub: x }]\n", []string{"assign author_id: "}},
		{"- op: users.get\n" + scope + "  key: { id: 999999 }\n", []string{"unexpected error: "}},
		{"- op: users.count\n" + scope + "  expect: { error: { class: NotFound } }\n", []string{"want error map[class:NotFound], got none"}},
		{"- op: users.get\n" + scope + "  key: { id: 999999 }\n  expect: { error: NotFound }\n", []string{"error expectation NotFound is not a map"}},
		{"- op: users.get\n" + scope + "  key: { id: 999999 }\n  expect: { error: { class: TooManyRows } }\n", []string{"error class: want TooManyRows, got NotFound"}},
		{"- op: users.count\n" + scope + "  expect: { value: 2, none: true, nope: 1 }\n", []string{"want none true, got 1", "unknown expectation nope", "want value 2, got 1"}},
		{"- op: users.count\n" + scope + "  expect: { row: { id: 1 } }\n", []string{"want row map[id:1], got 1"}},
		{"- op: users.get\n" + scope + "  key: { id: $ann.id }\n  expect: { row: { nope: 1, display_name: present, email: b, id: $nope.id } }\n",
			[]string{"displayname is null", "email: want b, got a@acme.com", "no saved row nope", "no field nope"}},
		{"- op: posts.insert_many\n" + scope + "  input: [{ author_id: $ann.id, title: x }, { author_id: $ann.id, title: y }]\n  expect: { rows: [{ title: x }, { title: z }] }\n", []string{"row 1 title: want z, got y"}},
		{"- op: posts.insert_many\n" + scope + "  input: [{ author_id: $ann.id, title: x }]\n  expect: { rows: [] }\n", []string{"want 0 rows, got 1"}},
	} {
		var steps []Step
		if err := yaml.Unmarshal([]byte(setup+c.step), &steps); err != nil {
			t.Fatal(err)
		}
		got := Run(ctx, Scenario{Name: "s", Steps: steps}, db, tables)
		if len(got) != len(c.want) {
			t.Errorf("%s: got %q, want %q", c.step, got, c.want)
			continue
		}
		for i, w := range c.want {
			if !strings.Contains(got[i], w) || !strings.HasPrefix(got[i], "s step 3 (") {
				t.Errorf("%s: got %q, want %q", c.step, got[i], w)
			}
		}
	}

	// Filtered writes need the store's table.
	if got := Run(ctx, Scenario{Name: "s", Steps: []Step{{Op: "tenants.delete_where", Where: "all"}}}, db, nil); len(got) != 1 || !strings.Contains(got[0], "no table tenants") {
		t.Errorf("no table: %q", got)
	}

	// A handle without WithScope has no scoped stores.
	if got := Run(ctx, Scenario{Name: "s", Steps: []Step{{Op: "users.count", Scope: map[string]any{}}}}, struct{}{}, nil); len(got) != 1 || !strings.Contains(got[0], "the handle has no WithScope") {
		t.Errorf("no WithScope: %q", got)
	}
}

func TestLoad(t *testing.T) {
	if _, err := Load("["); err == nil {
		t.Error("bad pattern loaded")
	}
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "dir.yaml"), 0o755)
	if _, err := Load(dir); err == nil {
		t.Error("unreadable file loaded")
	}
	dir = t.TempDir()
	os.WriteFile(filepath.Join(dir, "bad.yaml"), []byte("steps: {"), 0o644)
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "bad.yaml") {
		t.Errorf("bad yaml: %v", err)
	}
}

func TestDatabase(t *testing.T) {
	ctx := context.Background()
	base := os.Getenv("DQL_TEST_POSTGRES_URL")
	if _, _, err := Database(ctx, base, "nope.sql"); err == nil {
		t.Error("missing DDL")
	}
	ddl := filepath.Join(t.TempDir(), "bad.sql")
	os.WriteFile(ddl, []byte("NOT SQL"), 0o644)
	if _, _, err := Database(ctx, base, ddl); err == nil {
		t.Error("bad DDL")
	}
	if _, _, err := Database(ctx, "postgres://nobody@127.0.0.1:1/x", ddl); err == nil {
		t.Error("no server")
	}
	if _, _, err := Schemas["shop"].Open(ctx, "postgres://%zz"); err == nil {
		t.Error("bad DSN opened")
	}
}
