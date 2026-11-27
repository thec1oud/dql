package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// annotationsJSON renders ConfigAnnotations' result as one line per key.
func annotationsJSON(t *testing.T, c *Config) string {
	t.Helper()
	got, ds := c.ConfigAnnotations()
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	var out []string
	for _, k := range []string{"shop.app.users", "shop.app.money_amount.amount", "shop.app.published_posts",
		"shop.app.posts.published_at", "billing.invoices.user_id", "shop.app.profiles.profiles_user_id_fkey", "shop.app.anchors"} {
		var anns []string
		for _, a := range got[k] {
			b, _ := protojson.Marshal(a)
			anns = append(anns, string(b))
		}
		if len(anns) > 0 {
			out = append(out, k+" "+strings.Join(anns, " "))
		}
	}
	return strings.ReplaceAll(strings.Join(out, "\n"), " ", "")
}

// §1.3, §5.3, §7.6, §7.7: config annotations are typed by their key's form,
// sorted by key, and located in dql.yaml by the qualified name they are
// written under.
func TestConfigAnnotations(t *testing.T) {
	c, err := Parse([]byte(`
annotations:
  shop.app.users: { name: user, x.go.type: 1.10 }
  shop.app.money_amount.amount: { nonnull }
  shop.app.published_posts: { logical-key: id }
  shop.app.posts.published_at: { x.ts.time-type: date, x.go.tags: [a, ~, { b: c }] }
  billing.invoices.user_id:
    relation: { to: shop.users.id, name: user, inverse-name: invoices }
  shop.app.profiles.profiles_user_id_fkey: { inverse-name: [user, profile], removal: delete }
  shop.app.anchors: { logical-key: &k [tenant_id, slug], x.go.keys: *k, x.go.nested: [*k] }
`))
	if err != nil {
		t.Fatal(err)
	}
	const at = `"origin":{"kind":"ORIGIN_CONFIG","location":"dql.yaml#annotations.`
	want := strings.Join([]string{
		`shop.app.users {"key":"core.name","value":{"string":"user"},` + at + `shop.app.users"}}` +
			` {"key":"x.go.type","value":{"string":"1.10"},` + at + `shop.app.users"}}`,
		`shop.app.money_amount.amount {"key":"core.nonnull",` + at + `shop.app.money_amount.amount"}}`,
		`shop.app.published_posts {"key":"core.logical-key","value":{"list":["id"]},` + at + `shop.app.published_posts"}}`,
		`shop.app.posts.published_at {"key":"x.go.tags","value":{"list":["a",null,{"b":"c"}]},` + at + `shop.app.posts.published_at"}}` +
			` {"key":"x.ts.time-type","value":{"string":"date"},` + at + `shop.app.posts.published_at"}}`,
		`billing.invoices.user_id {"key":"core.relation","value":{"struct":{"inverse-name":"invoices","name":"user","to":"shop.users.id"}},` +
			at + `billing.invoices.user_id"}}`,
		`shop.app.profiles.profiles_user_id_fkey {"key":"core.inverse-name","value":{"words":["user","profile"]},` +
			at + `shop.app.profiles.profiles_user_id_fkey"}}` +
			` {"key":"core.removal","value":{"string":"delete"},` + at + `shop.app.profiles.profiles_user_id_fkey"}}`,
		`shop.app.anchors {"key":"core.logical-key","value":{"list":["tenant_id","slug"]},` + at + `shop.app.anchors"}}` +
			` {"key":"x.go.keys","value":{"list":["tenant_id","slug"]},` + at + `shop.app.anchors"}}` +
			` {"key":"x.go.nested","value":{"list":[["tenant_id","slug"]]},` + at + `shop.app.anchors"}}`,
	}, "\n")
	if got := annotationsJSON(t, c); got != strings.ReplaceAll(want, " ", "") {
		t.Errorf("got:\n%s\nwant:\n%s", got, strings.ReplaceAll(want, " ", ""))
	}
}

// The location names the file Load read.
func TestConfigAnnotationsLocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shop.yaml")
	os.WriteFile(path, []byte("annotations:\n  shop.app.users: { name: user }\n"), 0o644)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := c.ConfigAnnotations()
	if loc := got["shop.app.users"][0].GetOrigin().GetLocation(); loc != "shop.yaml#annotations.shop.app.users" {
		t.Errorf("location = %s", loc)
	}
}

func TestConfigAnnotationsInvalid(t *testing.T) {
	c, err := Parse([]byte("annotations:\n  shop.app.users: user\n  shop.app.tags: { name: tag }\n"))
	if err != nil {
		t.Fatal(err)
	}
	got, ds := c.ConfigAnnotations()
	if len(ds) != 1 || ds[0].GetCode() != CodeAnnotationsInvalid || ds[0].GetSeverity() != irv1.Severity_SEVERITY_ERROR ||
		!strings.Contains(ds[0].GetMessage(), "dql.yaml line 2: annotations.shop.app.users is not a mapping") {
		t.Errorf("diagnostics: %v", ds)
	}
	if len(got) != 1 || len(got["shop.app.tags"]) != 1 {
		t.Errorf("annotations: %v", got)
	}
}
