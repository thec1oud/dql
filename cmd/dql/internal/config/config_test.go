package config

import (
	"os"
	"path/filepath"
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
