package harness

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Each way a step can go wrong is reported, and nothing else is.
func TestRunProblems(t *testing.T) {
	target := open(t, "shop")
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
		{"- op: nope\n", []string{"no transaction nope"}},
		{"- op: tenants.insert_many\n  input: { generate: { count: x, row: { name: a } } }\n", []string{"generate: count x is not a number"}},
		{"- op: tenants.insert_many\n  input: { generate: { count: 2, row: { name: \"g-$i\" } } }\n  expect: { count: 3, statements: 2, rows: [{ name: g-1 }, { name: g-2 }] }\n", []string{"want count 3, got 2", "want 2 statements, got 1"}},
		{"- op: tenants.count\n  expect: { events: [] }\n", []string{"want 0 events, got 1: "}},
		{"- op: tenants.count\n  expect: { events: [{ query: nope }] }\n", []string{"event 1 query: want nope, got tenant.count"}},
		{"- op: users.get\n" + scope + "  key: { id: 999999 }\n  expect: { error: { class: NotFound, hides: user } }\n", []string{"error shows user: "}},
		{"- op: write\n  expect: { attempts: 2 }\n", []string{"attempts: want 2, got 1"}},
		{"- op: write\n  propagation: sideways\n", []string{"no propagation sideways"}},
		{"- op: write\n  inject: { class: Timeout, times: 1 }\n", []string{"inject: no injected failure of class Timeout"}},
		{"- op: write\n  steps: [{ op: tenants.count, expect: { value: 0 } }]\n", []string{"step 1 (tenants.count): want value 0, got "}},
		{"- op: posts.insert_nested\n" + scope + "  input: { title: x, author: x }\n", []string{"input: Author: a UserRef is an object of one of create, link"}},
		{"- op: posts.insert_nested\n" + scope + "  input: { title: x, author: { unlink: {} } }\n", []string{"input: Author: a UserRef cannot unlink"}},
		{"- op: posts.insert_nested\n" + scope + "  input: { title: x, author: { link: { id: x } } }\n", []string{"input: Author: link: "}},
		{"- op: posts.insert_nested\n" + scope + "  input: { title: x, tags: x }\n", []string{"input: Tags: want a list"}},
		{"- op: posts.insert_nested\n" + scope + "  input: { title: x, tags: [x] }\n", []string{"input: Tags: 0: a TagLink is an object"}},
		{"- op: posts.insert_nested\n" + scope + "  input: x\n", []string{"input: want an object for PostDraftNested"}},
		{"- op: users.insert_nested\n" + scope + "  input: { email: b@acme.com, password_hash: x }\n  expect: { row: { tenant: { name: x }, profile: { bio: x }, posts_to_author: [{ title: x }] } }\n",
			[]string{"poststoauthor: want 1 rows, got 0", "profile.want row map[bio:x], got <nil>", "tenant.name: want x, got acme"}},
	} {
		var steps []Step
		if err := yaml.Unmarshal([]byte(setup+c.step), &steps); err != nil {
			t.Fatal(err)
		}
		got := Run(ctx, Scenario{Name: "s", Steps: steps}, target)
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
	if got := Run(ctx, Scenario{Name: "s", Steps: []Step{{Op: "tenants.delete_where", Where: "all"}}}, Target{DB: target.DB}); len(got) != 1 || !strings.Contains(got[0], "no table tenants") {
		t.Errorf("no table: %q", got)
	}

	// A handle without WithScope has no scoped stores.
	if got := Run(ctx, Scenario{Name: "s", Steps: []Step{{Op: "users.count", Scope: map[string]any{}}}}, Target{DB: struct{}{}}); len(got) != 1 || !strings.Contains(got[0], "the handle has no WithScope") {
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

// A nil Recorder keeps nothing, and a step then fired no events.
func TestNoRecorder(t *testing.T) {
	var r *Recorder
	if r.mark() != 0 || r.since(0) != nil {
		t.Error("a nil recorder holds events")
	}
}

func TestGenerate(t *testing.T) {
	got, err := generate(map[string]any{"generate": map[string]any{"count": 2, "row": map[string]any{"name": "n-$i", "size": 3}}})
	want := []any{map[string]any{"name": "n-1", "size": 3}, map[string]any{"name": "n-2", "size": 3}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("generated %v, %v", got, err)
	}
}
