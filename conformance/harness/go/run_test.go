package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/thec1oud/dql/runtime/go/dql"
)

// Each way a step can go wrong is reported, and nothing else is.
func TestRunProblems(t *testing.T) {
	target := open(t, Schemas["shop"], false, 0, dql.ModePrepare)
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
		{"- op: select\n  from: tenants\n  shape: nope\n", []string{"no shape nope"}},
		{"- op: select\n  from: nope\n  shape: tenant_full\n", []string{"no store nope"}},
		{"- op: select\n  from: tags\n  shape: tenant_full\n", []string{"shape tenant_full is not read through tags"}},
		{"- op: select\n  from: tenants\n  shape: tenant_full\n  filter: [{ column: nope, op: eq }]\n", []string{"no column nope of tenants"}},
		{"- op: select\n  from: tenants\n  shape: tenant_full\n  filter: [{ column: name, op: nope }]\n", []string{"column name has no nope"}},
		{"- op: select\n  from: tenants\n  shape: tenant_full\n  filter: [{ column: name, op: eq, value: [1] }]\n", []string{"filter name: "}},
		{"- op: select\n  from: tenants\n  shape: tenant_full\n  filter: [{ column: name, op: in, value: { generate: { count: x } } }]\n", []string{"filter name: generate: count x is not a number"}},
		{"- op: select\n  from: tenants\n  shape: tenant_full\n  order: [{ column: nope }]\n", []string{"no column nope of tenants"}},
		{"- op: select\n  from: tenants\n  shape: tenant_full\n  read: nope\n", []string{"no read nope"}},
		{"- op: select\n  from: tenants\n  shape: tenant_full\n  filter: [{ column: name, op: regex_match, value: \"(\" }]\n  expect: { error: { class: DatabaseError } }\n", nil},
		{"- op: select\n  from: tenants\n  shape: tenant_full\n  order: [{ column: name }]\n  limit: 1\n  expect: { rows: [{ name: nope }] }\n", []string{"row 0 name: want nope, got acme"}},
		{"- op: sleep\n  duration: x\n", []string{`time: invalid duration "x"`}},
		{"- op: select\n  from: tenants\n  shape: tenant_full\n  consistency: nope\n", []string{"no consistency nope"}},
		{"- op: select\n  from: tenants\n  shape: tenant_full\n  consistency: bounded\n  bound: x\n", []string{"bound: time: invalid duration"}},
		{"- op: select\n  from: tenants\n  shape: tenant_full\n  consistency: read_your_writes\n  wait: x\n", []string{"wait: time: invalid duration"}},
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
		{"- op: tenants.count\n  inject: { class: Timeout, times: 1 }\n", []string{"inject: no injected failure of class Timeout"}},
		{"- op: tenants.count\n  inject: { delay: x, times: 1 }\n", []string{"inject: delay: time: invalid duration"}},
		{"- op: tenants.count\n  deadline: x\n", []string{`deadline: time: invalid duration "x"`}},
		{"- op: tenants.count\n  cancel: x\n", []string{`cancel: time: invalid duration "x"`}},
		{"- op: tenants.count\n  terminate: x\n", []string{`terminate: time: invalid duration "x"`}},
		{"- op: convert\n  from: nope\n", []string{"no shape type nope"}},
		{"- op: convert\n  from: tenant_full\n  input: { name: 1 }\n", []string{"input: "}},
		{"- op: convert\n  from: tenant_full\n  call: key\n  expect: { generated: false }\n", []string{"key is generated"}},
		{"- op: convert\n  from: tenant_full\n  call: nope\n", []string{"no conversion nope"}},
		{"- op: convert\n  call: nope\n", []string{"no conversion nope"}},
		{"- op: convert\n  from: tenant_full\n  call: key\n  args: [1]\n", []string{"key takes 0 arguments, got 1"}},
		{"- op: convert\n  call: merge_tenant_by_key\n  args: [x, []]\n", []string{"argument 1: "}},
		{"- op: convert\n  call: merge_tenant_by_key\n  args: [[], []]\n  expect: { results: [[]] }\n", []string{"want 1 results, got 2"}},
		{"- op: convert\n  call: merge_tenant_by_key\n  args: [[$acme], []]\n  expect: { results: [[{ name: x }], null] }\n", []string{"result 1.0.name: want x, got acme"}},
		{"- op: select\n  from: tenants\n  shape: tenant_full\n  deleted: with\n", []string{"the schema has no soft delete"}},
		{"- op: select\n  from: tenants\n  shape: tenant_full\n  deleted: nope\n", []string{"no deleted nope"}},
		{"- op: select\n  from: users\n  scope: { tenant_id: [1] }\n  shape: user_full\n", []string{"scope: "}},
		{"- op: write\n  scope: { tenant_id: [1] }\n", []string{"scope: "}},
		{"- op: sql\n  sql: { sqlite: x }\n", []string{"no sql for postgres"}},
		{"- op: raw\n  source: main\n  sql: { sqlite: x }\n", []string{"no sql for postgres"}},
		{"- op: raw\n  source: main\n  sql: { postgres: SELECT $1 }\n  args: [$nope.x]\n", []string{"args: no saved row nope"}},
		{"- op: raw\n  source: main\n  sql: { postgres: NOT SQL }\n", []string{"unexpected error: "}},
		{"- op: sql\n  sql: { postgres: NOT SQL }\n", []string{"syntax error"}},
		{"- op: begin_write\n  end: sideways\n", []string{"no end sideways"}},
		{"- op: read\n  steps: [{ op: begin_write, expect: { error: { misuse: ReadOnly } } }, { op: begin_write }]\n", []string{"step 2 (begin_write): unexpected error: "}},
		{"- op: begin_read\n  end: commit\n  steps: [{ op: tenants.count, expect: { value: 0 } }]\n", []string{"step 1 (tenants.count): want value 0, got "}},
		{"- op: operation\n  steps: [{ op: tenants.count, expect: { value: 0 } }]\n  expect: { events: [] }\n", []string{"step 1 (tenants.count): want value 0, got ", "want 0 events, got 1"}},
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

	// A fallible conversion's error is its outcome.
	fallible := Target{Schema: Schema{Funcs: map[string]any{"fail": func() (int, error) { return 0, errors.New("off") }}}}
	if got := Run(ctx, Scenario{Name: "s", Steps: []Step{{Op: "convert", Call: "fail", Expect: map[string]any{"error": map[string]any{"class": ""}}}}}, fallible); got != nil {
		t.Errorf("fallible: %q", got)
	}

	// A handle without pair-form transactions has no begin steps.
	if got := Run(ctx, Scenario{Name: "s", Steps: []Step{{Op: "begin_read"}}}, Target{DB: struct{}{}}); len(got) != 1 || !strings.Contains(got[0], "no transaction begin_read") {
		t.Errorf("no BeginRead: %q", got)
	}

	// A termination that fails is reported.
	down := Target{DB: target.DB, Schema: Schema{Terminate: func(context.Context, string) error { return errors.New("down") }}}
	if got := Run(ctx, Scenario{Name: "s", Steps: []Step{{Op: "nope.count", Terminate: "1ms"}}}, down); len(got) != 2 || got[1] != "s step 1 (nope.count): terminate: down" {
		t.Errorf("terminate failed: %q", got)
	}

	// A handle without WithScope has no scoped stores.
	if got := Run(ctx, Scenario{Name: "s", Steps: []Step{{Op: "users.count", Scope: map[string]any{}}}}, Target{DB: struct{}{}}); len(got) != 1 || !strings.Contains(got[0], "the handle has no WithScope") {
		t.Errorf("no WithScope: %q", got)
	}
}

// A replay that fails is reported, and a scenario that ends with replay
// paused resumes it.
func TestReplay(t *testing.T) {
	ctx := context.Background()
	var calls []bool
	fail := map[bool]bool{}
	target := Target{Schema: Schema{Replay: func(_ context.Context, dsn string, pause bool) error {
		calls = append(calls, pause)
		if fail[pause] {
			return errors.New("down")
		}
		return nil
	}}}
	steps := []Step{{Op: "pause_replay"}}
	if got := Run(ctx, Scenario{Name: "s", Steps: steps}, target); got != nil || !reflect.DeepEqual(calls, []bool{true, false}) {
		t.Errorf("paused at the end: %q, %v", got, calls)
	}
	fail[false] = true
	if got := Run(ctx, Scenario{Name: "s", Steps: steps}, target); len(got) != 1 || got[0] != "s: resume_replay: down" {
		t.Errorf("resume failed: %q", got)
	}
	fail[true] = true
	if got := Run(ctx, Scenario{Name: "s", Steps: steps}, target); len(got) != 1 || got[0] != "s step 1 (pause_replay): down" {
		t.Errorf("pause failed: %q", got)
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
	if _, _, err := Schemas["shop"].Open(ctx, "postgres://%zz", []string{"postgres://%zz"}, 0, dql.ModePrepare); err == nil {
		t.Error("bad DSN opened")
	}
	// A primary is no replica, so it never enters rotation.
	defer func(d time.Duration) { rotationWait = d }(rotationWait)
	rotationWait = 100 * time.Millisecond
	var shop []string
	for _, f := range Schemas["shop"].DDL {
		shop = append(shop, filepath.Join(root, f))
	}
	dsn, drop, err := Database(ctx, base, shop...)
	if err != nil {
		t.Fatal(err)
	}
	defer drop()
	if _, _, err := Schemas["shop"].Open(ctx, dsn, []string{dsn}, 0, dql.ModePrepare); err == nil || err.Error() != "the replica did not enter rotation" {
		t.Errorf("a replica out of rotation: %v", err)
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
	got, err = generate(map[string]any{"generate": map[string]any{"count": 2, "value": "v-$i"}})
	if err != nil || !reflect.DeepEqual(got, []any{"v-1", "v-2"}) {
		t.Errorf("generated %v, %v", got, err)
	}
}

// The MySQL schema's database, failure injection and termination fail as
// Postgres's do, and a step that builds a list of Drafts fills them.
func TestMySQL(t *testing.T) {
	ctx := context.Background()
	base := os.Getenv("DQL_TEST_MYSQL_URL")
	if _, _, err := databaseMySQL(ctx, base, "nope.sql"); err == nil {
		t.Error("missing DDL")
	}
	ddl := filepath.Join(t.TempDir(), "bad.sql")
	os.WriteFile(ddl, []byte("NOT SQL"), 0o644)
	if _, _, err := databaseMySQL(ctx, base, ddl); err == nil {
		t.Error("bad DDL")
	}
	if _, _, err := databaseMySQL(ctx, "bad dsn", ddl); err == nil {
		t.Error("bad DSN")
	}
	if _, _, err := databaseMySQL(ctx, "root@tcp(127.0.0.1:1)/", ddl); err == nil {
		t.Error("no server")
	}
	if err := terminateMySQL(ctx, "bad dsn"); err == nil {
		t.Error("bad DSN")
	}
	if err := terminateMySQL(ctx, "root@tcp(127.0.0.1:1)/"); err == nil {
		t.Error("no server")
	}
	if err := runMySQL(ctx, "bad dsn", "SELECT 1"); err == nil {
		t.Error("bad DSN")
	}
	target := open(t, MySQLSchemas["shop"], false, 0, dql.ModePrepare)
	steps := `
- op: tenants.insert_many
  input: [{ name: a }, { name: b }]
  expect: { count: 2 }
- op: tenants.insert_many
  input: [{ name: 1 }]
- op: write
  inject: { class: SerializationFailure, times: 1 }
- op: tenants.count
  inject: { delay: x, times: 1 }
`
	var sc Scenario
	if err := yaml.Unmarshal([]byte("scenario: s\nsteps:"+steps), &sc); err != nil {
		t.Fatal(err)
	}
	got := Run(ctx, sc, target)
	want := []string{"s step 2 (tenants.insert_many): input: 0: ", "s step 3 (write): inject: no injected failure of class SerializationFailure", "s step 4 (tenants.count): inject: delay: time: invalid duration"}
	if len(got) != len(want) {
		t.Fatalf("problems %q", got)
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("problem %d: %q, want %q", i, got[i], want[i])
		}
	}
}
