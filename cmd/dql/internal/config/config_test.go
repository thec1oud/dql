package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The running example's dql.yaml (design record §1.3).
const shop = `
sources:
  - plugin: pg-live
    catalog: shop
    connection: env:SHOP_DATABASE_URL
    namespaces: [app]
    default-namespace: app

annotations:
  shop.app.users:           { name: user }
  shop.app.profiles.profiles_user_id_fkey: { inverse-name: profile }
  shop.app.posts.posts_author_id_fkey:     { collection-policy: replace, removal: delete }
  shop.app.money_amount.amount: { nonnull }

shapes:
  user_summary:
    from: user.full
    pick: [id, email, display_name]

emit:
  - plugin: go
    dialect: postgres
    package: shopdb
    out: internal/shopdb
    types:
      public.citext: { go: string }
  - plugin: ts
    dialect: postgres
    out: src/shopdb
`

func TestParseRunningExample(t *testing.T) {
	c, err := Parse([]byte(shop))
	if err != nil {
		t.Fatal(err)
	}
	if c.Sources[0].Plugin != "pg-live" || c.Sources[0].DefaultNamespace != "app" || c.Sources[0].Namespaces[0] != "app" {
		t.Errorf("sources: %+v", c.Sources)
	}
	if n := c.Annotations["shop.app.users"]; len(n.Content) != 2 || n.Content[0].Value != "name" || n.Content[1].Value != "user" {
		t.Errorf("annotations: %v", c.Annotations)
	}
	if n := c.Annotations["shop.app.money_amount.amount"]; len(n.Content) != 2 || n.Content[0].Value != "nonnull" {
		t.Error("a valueless annotation was lost")
	}
	if len(c.Emit) != 2 || c.Emit[0].Package != "shopdb" || c.Emit[0].Types["public.citext"]["go"] != "string" {
		t.Errorf("emit: %+v", c.Emit)
	}
	if ds := c.Validate(); len(ds) != 0 {
		t.Errorf("unexpected diagnostics: %v", ds)
	}
}

func TestParseErrors(t *testing.T) {
	if _, err := Parse([]byte("sources: []\nsurces: []\n")); err == nil || !strings.Contains(err.Error(), "surces") {
		t.Errorf("unknown key: %v", err)
	}
	c, err := Parse(nil)
	if err != nil || len(c.Sources) != 0 {
		t.Errorf("empty file: %v", err)
	}
}

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dql.yaml")
	os.WriteFile(path, []byte(shop), 0o644)
	c, err := Load(path)
	if err != nil || c.Sources[0].Catalog != "shop" {
		t.Fatal(err)
	}
	// Paths in dql.yaml are relative to its directory.
	dir := filepath.Dir(path)
	if got := c.Resolve(filepath.Join("db", "shop.json")); got != filepath.Join(dir, "db", "shop.json") {
		t.Errorf("relative: %s", got)
	}
	if got := c.Resolve("/abs/shop.json"); got != "/abs/shop.json" {
		t.Errorf("absolute: %s", got)
	}
	if got := c.Resolve(""); got != "" {
		t.Errorf("empty: %s", got)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("want an error for a missing file")
	}
	os.WriteFile(path, []byte("surces: []\n"), 0o644)
	if _, err := Load(path); err == nil {
		t.Fatal("want an error for an invalid file")
	}
}

func TestValidate(t *testing.T) {
	c, _ := Parse([]byte(`
sources:
  - catalog: shop
emit:
  - dialect: oracle
opaque-fallback: json
`))
	var codes []string
	for _, d := range c.Validate() {
		codes = append(codes, d.GetCode())
	}
	want := "config-plugin-missing config-plugin-missing config-dialect-unknown config-opaque-fallback-invalid"
	if strings.Join(codes, " ") != want {
		t.Errorf("got %v", codes)
	}

	// The shipped sources need their own settings and no other plugin's.
	c, _ = Parse([]byte(`
sources:
  - plugin: pg-live
    snapshot: shop.json
  - plugin: dpg-snapshot
    connection: env:SHOP_DATABASE_URL
  - plugin: dpg-snapshot
    snapshot: shop.json
  - plugin: mysql-live
    snapshot: shop.json
`))
	var msgs []string
	for _, d := range c.Validate() {
		msgs = append(msgs, d.GetCode()+": "+d.GetMessage())
	}
	want = strings.Join([]string{
		"config-setting-missing: sources[0] (pg-live) has no catalog; set catalog:",
		"config-setting-missing: sources[0] (pg-live) has no connection; set connection:",
		"config-setting-unused: sources[0] (pg-live) sets snapshot, which pg-live does not read; remove it",
		"config-setting-missing: sources[1] (dpg-snapshot) has no snapshot; set snapshot:",
		"config-setting-unused: sources[1] (dpg-snapshot) sets connection, which dpg-snapshot does not read; remove it",
	}, "\n")
	if got := strings.Join(msgs, "\n"); got != want {
		t.Errorf("settings:\n%s\nwant:\n%s", got, want)
	}
	if ds := (&Config{}).Validate(); len(ds) != 1 || ds[0].GetCode() != "config-no-sources" {
		t.Errorf("no sources: %v", ds)
	}
	if ds := (&Config{Sources: []Source{{Plugin: "pg-live", Catalog: "shop", Connection: "env:URL"}}, OpaqueFallback: "bytes"}).Validate(); len(ds) != 0 {
		t.Errorf("bytes fallback: %v", ds)
	}
}

// Each plugin entry keeps every key as written, for the plugin request. A
// shipped plugin's entry holds only the keys dql reads; any other plugin's
// may hold its own, which the plugin validates.
func TestSettings(t *testing.T) {
	c, err := Parse([]byte(`
sources:
  - plugin: pg-live
    catalog: shop
    connection: env:SHOP_DATABASE_URL
  - plugin: mysql-live
    path: bin/dql-plugin-mysql-live
    dsn: env:MYSQL_URL
    since: 2001-12-14
    cert: !!binary aGVsbG8=
    retries: 3
    ports: {1: primary, true: on}
    pool: &pool {size: 4}
    replica: *pool
    <<: {timeout: ~}
emit:
  - plugin: kotlin
    dialect: postgres
    out: gen
    nullable: [{boxed: true}]
`))
	if err != nil {
		t.Fatal(err)
	}
	got := []string{
		fmt.Sprint(c.Sources[0].Settings.AsMap()),
		fmt.Sprint(c.Sources[1].Settings.AsMap()),
		fmt.Sprint(c.Emit[0].Settings.AsMap()),
	}
	want := []string{
		"map[catalog:shop connection:env:SHOP_DATABASE_URL plugin:pg-live]",
		"map[cert:aGVsbG8= dsn:env:MYSQL_URL path:bin/dql-plugin-mysql-live plugin:mysql-live pool:map[size:4] " +
			"ports:map[1:primary true:on] replica:map[size:4] retries:3 since:2001-12-14 timeout:<nil>]",
		"map[dialect:postgres nullable:[map[boxed:true]] out:gen plugin:kotlin]",
	}
	if !slices.Equal(got, want) {
		t.Errorf("settings:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if c.Sources[1].Path != "bin/dql-plugin-mysql-live" || c.Emit[0].Out != "gen" {
		t.Errorf("known keys: %+v %+v", c.Sources[1], c.Emit[0])
	}
	if ds := c.Validate(); len(ds) != 0 {
		t.Errorf("unexpected diagnostics: %v", ds)
	}

	for _, e := range []struct{ config, want string }{
		{"sources:\n  - plugin: pg-live\n    catalog: shop\n    dsn: x\n    pool: 4\n",
			"line 4: field dsn not found in type config.Source\n  line 5: field pool not found in type config.Source"},
		{"sources:\n  - plugin: mysql-live\n    pool:\n      ? [a, b]\n      : 4\n", "line 4: a key in a plugin's settings must be a scalar"},
		{"emit:\n  - plugin: kotlin\n    out: [gen]\n", "line 3: cannot unmarshal !!seq into string"},
		{"sources:\n  - mysql-live\n", "line 2: cannot unmarshal !!str `mysql-live` into config.entry"},
	} {
		if _, err := Parse([]byte(e.config)); err == nil || !strings.Contains(err.Error(), e.want) {
			t.Errorf("%q: %v; want %q", e.config, err, e.want)
		}
	}
}
