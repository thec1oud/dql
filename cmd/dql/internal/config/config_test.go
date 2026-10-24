package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/thec1oud/dql/core/shapes"
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
  user_summary_named:
    from: user_summary
    nonnull: [display_name]
  post_with_tags:
    from: post.full
    nest:
      tags: tag.full
      author: user_summary
  signup:
    from: user.draft
    pick: [email, password_hash]
  quick_invite:
    from: user.draft
    pick: [email]
    bind: [password_hash]

conversions:
  - { from: user_summary, to: user_summary_named }

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

// The shapes: entries of §14.8 reach the shape layer as written.
func TestCustomShapes(t *testing.T) {
	c, err := Parse([]byte(shop))
	if err != nil {
		t.Fatal(err)
	}
	got := c.CustomShapes()
	want := []shapes.Custom{
		{Name: "post_with_tags", From: "post.full", Nest: map[string]string{"tags": "tag.full", "author": "user_summary"}},
		{Name: "quick_invite", From: "user.draft", Pick: []string{"email"}, Bind: []string{"password_hash"}},
		{Name: "signup", From: "user.draft", Pick: []string{"email", "password_hash"}},
		{Name: "user_summary", From: "user.full", Pick: []string{"id", "email", "display_name"}},
		{Name: "user_summary_named", From: "user_summary", Nonnull: []string{"display_name"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("custom shapes:\n got %+v\nwant %+v", got, want)
	}
	if !reflect.DeepEqual(c.Conversions, []Conversion{{From: "user_summary", To: "user_summary_named"}}) {
		t.Errorf("conversions: %+v", c.Conversions)
	}
	every, err := Parse([]byte("shapes:\n  s: { from: user.draft, pick: [a], omit: [b], require: [c], optionalize: [d], nullify: [e], nonnull: [f], bind: [g] }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s := every.CustomShapes()[0]; s.Pick[0]+s.Omit[0]+s.Require[0]+s.Optionalize[0]+s.Nullify[0]+s.Nonnull[0]+s.Bind[0] != "abcdefg" {
		t.Errorf("operators: %+v", s)
	}
	if (&Config{}).CustomShapes() != nil {
		t.Error("no shapes: want nil")
	}
}

func TestShapesAndConversionsErrors(t *testing.T) {
	for _, tc := range []struct{ yaml, want string }{
		{"shapes:\n  s: { from: user.full, picks: [id] }\n", "field picks not found"},
		{"shapes:\n  s: { from: user.full, nest: { author: { shape: user.full } } }\n", "cannot unmarshal"},
		{"conversions:\n  - user.full\n", "cannot unmarshal"},
		{"conversions:\n  - { from: user.full, to: user.key, kind: total }\n", "field kind not found"},
	} {
		if _, err := Parse([]byte(tc.yaml)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: %v", tc.yaml, err)
		}
	}
	c, err := Parse([]byte("sources: [{ plugin: x }]\nshapes:\n  s:\n  t: { pick: [id] }\nconversions:\n  - { from: user.full }\n  - { to: user.key }\n"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range c.Validate() {
		got = append(got, d.GetCode()+": "+d.GetMessage())
	}
	want := []string{
		"config-setting-missing: shapes.s has no from; set from: to the shape it derives from, such as user.full",
		"config-setting-missing: shapes.t has no from; set from: to the shape it derives from, such as user.full",
		"config-setting-missing: conversions[0] has no to; set to: to a shape, such as user_summary",
		"config-setting-missing: conversions[1] has no from; set from: to a shape, such as user.full",
	}
	if !slices.Equal(got, want) {
		t.Errorf("diagnostics:\n got %q\nwant %q", got, want)
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
	// Paths in dql.yaml are relative to its directory, made absolute.
	dir := filepath.Dir(path)
	if c.Dir() != dir {
		t.Errorf("dir: %s", c.Dir())
	}
	t.Chdir(dir)
	if c, err := Load("dql.yaml"); err != nil || c.Dir() != dir || c.Resolve("shop.json") != filepath.Join(dir, "shop.json") {
		t.Errorf("relative load: %v %v", c, err)
	}
	if c, _ := Parse(nil); c.Dir() != "" || c.Resolve("shop.json") != "shop.json" {
		t.Errorf("parsed: %q", c.Dir())
	}
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
	// The go emitter needs a package and an output directory.
	c, _ = Parse([]byte(`
sources:
  - plugin: dpg-snapshot
    snapshot: shop.json
emit:
  - plugin: go
    dialect: postgres
  - plugin: go
    dialect: postgres
    package: shopdb
    out: gen
  - plugin: kotlin
    dialect: postgres
`))
	msgs = nil
	for _, d := range c.Validate() {
		msgs = append(msgs, d.GetCode()+": "+d.GetMessage())
	}
	want = strings.Join([]string{
		"config-setting-missing: emit[0] (go) has no package; set package:",
		"config-setting-missing: emit[0] (go) has no out; set out:",
	}, "\n")
	if got := strings.Join(msgs, "\n"); got != want {
		t.Errorf("emit settings:\n%s\nwant:\n%s", got, want)
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
		{"emit:\n  - plugin: go\n    out: gen\n    tags: [json]\n", "line 4: field tags not found in type config.Emit"},
		{"sources:\n  - mysql-live\n", "line 2: cannot unmarshal !!str `mysql-live` into config.entry"},
	} {
		if _, err := Parse([]byte(e.config)); err == nil || !strings.Contains(err.Error(), e.want) {
			t.Errorf("%q: %v; want %q", e.config, err, e.want)
		}
	}
}
