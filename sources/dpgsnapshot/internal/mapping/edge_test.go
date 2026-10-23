package mapping

import (
	"encoding/json"
	"strings"
	"testing"

	pgextv1 "github.com/thec1oud/dql/proto/dql/ext/postgres/v1"
	"github.com/thec1oud/dql/sources/dpgsnapshot/internal/format"
)

// snapshot builds a snapshot of database d holding objects, given as the
// JSON of dpg's objects map.
func snapshot(t *testing.T, objects string) *format.Snapshot {
	t.Helper()
	s, err := format.Parse([]byte(`{"dpg_version": "0.2.0", "cluster": "c", "database": "d", "objects": ` + objects + `}`))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// table is the objects map of one table app.t.
func table(columns, constraints string) string {
	return `{"app.t": {"kind": "table", "table": {"schema": "app", "name": "t", "columns": ` + columns +
		`, "constraints": ` + constraints + `}}}`
}

func TestErrors(t *testing.T) {
	for _, c := range []struct {
		objects, want string
	}{
		{table(`[{"name": "a", "type": "int("}]`, `[]`), `type "int(" of column app.t.a`},
		{table(`[{"name": "a", "type": "integer", "default": "("}]`, `[]`), "default of column app.t.a"},
		{table(`[{"name": "a", "type": "integer"}]`, `[{"name": "", "type": "CHECK", "expr": "CHECK ("}]`),
			`check constraint "CHECK (" of app.t`},
		{table(`[{"name": "a", "type": "integer"}]`, `[{"name": "", "type": "PRIMARY KEY", "expr": "PRIMARY KEY ("}]`),
			`constraint "PRIMARY KEY (" of app.t`},
		{table(`[{"name": "a", "type": "integer"}]`, `[{"name": "", "type": "UNIQUE", "expr": "UNIQUE (a), ADD UNIQUE (a)"}]`),
			"not one constraint"},
		{table(`[{"name": "a", "type": "integer"}]`, `[{"name": "", "type": "FOREIGN KEY", "expr": "FOREIGN KEY (a) REFERENCES app.t"}]`),
			"references app.t, which has no primary key"},
		{table(`[{"name": "a", "type": "text", "identity": "ALWAYS"}]`, `[]`),
			`sequence app.t_a_seq has type "text"`},
		{`{"app.t": {"kind": "table", "table": {"schema": "app", "name": "t", "indexes": [
			{"name": "i", "unique": true, "method": "btree", "columns": "("}]}}}`, `index app.i: "(" is not a list`},
		{`{"app.s": {"kind": "sequence", "sequence": {"schema": "app", "name": "s", "owned_by": "a.b.c.d"}}}`,
			`owned by "a.b.c.d", which names no column`},
		{`{"app.d": {"kind": "type", "type": {"schema": "app", "name": "d", "variant": "DOMAIN", "domain_base_type": "text",
			"domain_constraints": [{"name": "", "type": "CHECK", "expr": "CHECK ("}]}}}`, "of domain app.d"},
		{`{"app.f(integer)": {"kind": "function", "function": {"schema": "app", "name": "f", "args": "integer",
			"return_type": "record", "return_table": "a", "language": "sql", "volatility": "STABLE", "body_hash": "x",
			"comment": "@public"}}}`, `"a" is not a column`},
		{`{"app.v": {"kind": "view", "view": {"schema": "app", "name": "v", "query": "SELECT 1; SELECT 2"}}}`,
			"the query of view app.v is not one SELECT"},
		{`{"app.v": {"kind": "view", "view": {"schema": "app", "name": "v", "query": "SELECT * FROM app.w"}},
		  "app.w": {"kind": "view", "view": {"schema": "app", "name": "w", "query": "SELECT * FROM app.v"}}}`,
			"view app.v reads itself"},
	} {
		_, _, err := Map(snapshot(t, c.objects), Options{})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.objects, err, c.want)
		}
	}
}

// TestClusterSnapshot refuses a snapshot of a cluster's roles, which
// describes no database.
func TestClusterSnapshot(t *testing.T) {
	s, err := format.Parse([]byte(`{"dpg_version": "0.2.0", "cluster": "c", "objects": {}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Map(s, Options{}); err == nil || !strings.Contains(err.Error(), `cluster "c" describes no database`) {
		t.Errorf("error %v", err)
	}
	c, _ := mapped(t, s, Options{Catalog: "named"})
	if c.Name.Stored != "named" {
		t.Errorf("catalog %v", c.Name)
	}
}

// TestNamespaces reads only the schemas listed: objects elsewhere, and views
// over them, are left out.
func TestNamespaces(t *testing.T) {
	c, ds := mapped(t, load(t, fullSnapshot), Options{Catalog: "other", Namespaces: []string{"audit"}})
	if c.Name.Stored != "other" || len(c.Namespaces) != 1 || c.Namespaces[0].Name.Stored != "audit" || len(ds) != 0 {
		t.Fatalf("catalog %v, diagnostics %v", c, ds)
	}
	s := snapshot(t, `{"app.v": {"kind": "view", "view": {"schema": "app", "name": "v", "query": "SELECT a FROM other.t"}},
		"other.t": {"kind": "table", "table": {"schema": "other", "name": "t", "columns": [{"name": "a", "type": "integer"}]}}}`)
	c, ds = mapped(t, s, Options{Namespaces: []string{"app"}})
	if len(c.Namespaces) != 1 || c.Namespaces[0].Tables[0].Columns != nil || len(ds) != 1 || ds[0].Code != CodeViewColumnsUnknown {
		t.Errorf("catalog %v, diagnostics %v", c, ds)
	}
}

func TestDomainCycle(t *testing.T) {
	s := snapshot(t, `{"app.a": {"kind": "type", "type": {"schema": "app", "name": "a", "variant": "DOMAIN", "domain_base_type": "app.b"}},
		"app.b": {"kind": "type", "type": {"schema": "app", "name": "b", "variant": "DOMAIN", "domain_base_type": "app.a"}}}`)
	c, _ := mapped(t, s, Options{})
	if d := c.Namespaces[0].Domains[0]; d.Collation != nil {
		t.Errorf("domain %v", d)
	}
}

func TestExtensionFailure(t *testing.T) {
	m := newMapper(snapshot(t, `{}`), Options{Catalog: "d"})
	(&exts{m: m}).add(&pgextv1.Comment{Text: "\xff"})
	if m.err == nil {
		t.Error("an extension that does not marshal fails the mapping")
	}
}

func TestStorageParameters(t *testing.T) {
	p, err := parseConstraint("UNIQUE (a) WITH (i=1, f=1.5, s='x', k=off, bare)")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(storageParameters(p.Options))
	if string(got) != `["i=1","f=1.5","s=x","k=off","bare=true"]` {
		t.Errorf("parameters %s", got)
	}
}

func TestUnwrap(t *testing.T) {
	for in, want := range map[string]string{
		"(lower(code))": "lower(code)",
		"lower(code)":   "lower(code)",
		"(a) + (b)":     "(a) + (b)",
		"(a":            "(a",
	} {
		if got := unwrap(in); got != want {
			t.Errorf("unwrap(%q) = %q, want %q", in, got, want)
		}
	}
}
