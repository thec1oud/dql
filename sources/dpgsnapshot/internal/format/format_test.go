package format

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// shop.json was written by dpg itself, with dpg plan --record, from
// testdata/shop.dpg: the design record's running example (§1.3) in dpg's
// language, in a project with cluster local and database shop.
func TestLoadShop(t *testing.T) {
	path := filepath.Join("testdata", "shop.json")
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Version != "0.2.0" || s.Cluster != "local" || s.Database != "shop" ||
		s.SourceRevision != "" || s.Migration != "20261023T031426Z-4f7e46f1690e" {
		t.Errorf("header = %+v", s)
	}
	if want := Fingerprint(fixture(t, "shop.json")); s.Fingerprint != want {
		t.Errorf("fingerprint = %s, want %s", s.Fingerprint, want)
	}

	var keys []string
	for _, e := range s.Objects {
		keys = append(keys, e.Key)
	}
	want := []string{
		"app", "app.email_address", "app.money_amount", "app.post_score(bigint)",
		"app.post_score(bigint, integer)", "app.post_status", "app.post_tags",
		"app.posts", "app.profiles", "app.published_posts", "app.slug_text",
		"app.tags", "app.tenants", "app.users",
	}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %q, want %q", keys, want)
	}

	users := object(t, s, "app.users").Table
	if users == nil || len(users.Columns) != 8 {
		t.Fatalf("app.users = %+v", users)
	}
	if c := users.Columns[0]; c.Name != "id" || c.Type != "bigint" || !c.NotNull || *c.Identity != "ALWAYS" {
		t.Errorf("users.id = %+v", c)
	}
	if c := users.Columns[4]; c.Name != "password_hash" || *c.Comment != "@write-only @sensitive" {
		t.Errorf("users.password_hash = %+v", c)
	}
	if c := users.Constraints[2]; c.Name != "users_tenant_email_key" || c.Type != "UNIQUE" ||
		c.Expr != `UNIQUE ("tenant_id", "email")` {
		t.Errorf("users_tenant_email_key = %+v", c)
	}

	email := object(t, s, "app.email_address").Type
	if email.Variant != "DOMAIN" || email.DomainBaseType != "text" ||
		email.DomainConstraints[0].Expr != "CHECK (char_length(value) <= 320)" {
		t.Errorf("app.email_address = %+v", email)
	}
	status := object(t, s, "app.post_status").Type
	if !reflect.DeepEqual(status.Values, []string{"draft", "published", "archived"}) {
		t.Errorf("app.post_status values = %q", status.Values)
	}
	score := object(t, s, "app.post_score(bigint, integer)").Function
	if score.Args != "bigint, integer" || score.ReturnType != "numeric" || !score.Strict ||
		score.Volatility != "STABLE" || *score.Comment != "@public" {
		t.Errorf("post_score(bigint, integer) = %+v", score)
	}
	view := object(t, s, "app.published_posts").View
	if *view.Comment != "@logical-key(id)" || !strings.HasPrefix(view.Query, "SELECT id, title") {
		t.Errorf("app.published_posts = %+v", view)
	}
}

func object(t *testing.T, s *Snapshot, key string) *Object {
	t.Helper()
	for _, e := range s.Objects {
		if e.Key == key {
			return e.Object
		}
	}
	t.Fatalf("no object %q", key)
	return nil
}

// Lossless (§6.3): re-encoding the decoded objects gives back the file's
// objects value for value.
func TestShopRoundTrip(t *testing.T) {
	data := fixture(t, "shop.json")
	s, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	objects := map[string]*Object{}
	for _, e := range s.Objects {
		objects[e.Key] = e.Object
	}
	again, err := json.Marshal(objects)
	if err != nil {
		t.Fatal(err)
	}
	var orig struct {
		Objects any `json:"objects"`
	}
	if err := json.Unmarshal(data, &orig); err != nil {
		t.Fatal(err)
	}
	var back any
	if err := json.Unmarshal(again, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(orig.Objects, back) {
		t.Errorf("objects changed in a round trip:\n%s", again)
	}
}

// §3.2 rule 3: applied_at never reaches anything the plugin can output.
func TestAppliedAtIsNotKept(t *testing.T) {
	s, err := Parse(fixture(t, "shop.json"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("2026-10-23T03:14:26Z")) || bytes.Contains(bytes.ToLower(out), []byte("applied")) {
		t.Errorf("applied_at survived decoding: %s", out)
	}
	// Any JSON value is accepted there, and none is kept.
	if _, err := Parse([]byte(`{"dpg_version": "0.2.0", "applied_at": {"a": [1, null]}}`)); err != nil {
		t.Error(err)
	}
}

func TestFingerprint(t *testing.T) {
	if got, want := Fingerprint([]byte("abc")), "sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"; got != want {
		t.Errorf("Fingerprint = %s, want %s", got, want)
	}
	if Fingerprint([]byte("abc ")) == Fingerprint([]byte("abc")) {
		t.Error("fingerprint ignores bytes")
	}
}

func TestVersions(t *testing.T) {
	for _, v := range []string{"0.2.0", "0.2.1", "0.2.17"} {
		s, err := Parse([]byte(`{"dpg_version": "` + v + `"}`))
		if err != nil || s.Version != v || s.Objects != nil {
			t.Errorf("%s: %+v, %v", v, s, err)
		}
	}
	for _, v := range []string{"", "0.2", "0.2.01", "0.2.0-rc1", "0.2.0+build", "0.1.9", "0.3.0", "1.0.0", "v0.2.0", " 0.2.0"} {
		_, err := Parse([]byte(`{"dpg_version": "` + v + `"}`))
		var ve *VersionError
		if !errors.As(err, &ve) || ve.Found != v || ve.Missing {
			t.Errorf("%q: %v", v, err)
			continue
		}
		want := `dpg snapshot format version "` + v + `" is not supported; supported snapshot format versions: 0.2.x`
		if err.Error() != want {
			t.Errorf("%q: message %q", v, err)
		}
	}
}

func TestFixturesRefused(t *testing.T) {
	for _, tc := range []struct {
		file, want string
	}{
		// The version is checked before the unknown field it carries.
		{"wrong-version.json", `testdata/wrong-version.json: dpg snapshot format version "0.3.0" is not supported; supported snapshot format versions: 0.2.x`},
		{"missing-version.json", "testdata/missing-version.json: dpg snapshot has no dpg_version; supported snapshot format versions: 0.2.x"},
		{"unknown-field.json", `testdata/unknown-field.json: dpg snapshot 0.2.0: json: unknown field "element_not_null"`},
		{"malformed.json", "testdata/malformed.json: dpg snapshot is not a valid snapshot file: unexpected end of JSON input"},
	} {
		_, err := Load(filepath.Join("testdata", tc.file))
		if err == nil || err.Error() != tc.want {
			t.Errorf("%s: got %v\nwant %s", tc.file, err, tc.want)
		}
	}
	var ve *VersionError
	if _, err := Load(filepath.Join("testdata", "missing-version.json")); !errors.As(err, &ve) || !ve.Missing {
		t.Errorf("missing version: %v", err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "none.json"))
	if !errors.Is(err, os.ErrNotExist) || !strings.HasPrefix(err.Error(), "dpg snapshot: ") {
		t.Errorf("got %v", err)
	}
}

func TestParseRefuses(t *testing.T) {
	for _, tc := range []struct {
		name, data, want string
	}{
		{"not an object", `[]`, "dpg snapshot is not a valid snapshot file: json: cannot unmarshal array"},
		{"trailing data", `{"dpg_version": "0.2.0"} {}`, "dpg snapshot is not a valid snapshot file: invalid character '{' after top-level value"},
		{"version not a string", `{"dpg_version": 2}`, "dpg snapshot is not a valid snapshot file: json: cannot unmarshal number"},
		{"null version", `{"dpg_version": null}`, "dpg snapshot has no dpg_version"},
		{"null file", `null`, "dpg snapshot has no dpg_version"},
		{"field of the wrong type", `{"dpg_version": "0.2.0", "cluster": 1}`, "dpg snapshot 0.2.0: json: cannot unmarshal number into Go struct field file.cluster of type string"},
		{"unknown top-level field", `{"dpg_version": "0.2.0", "live": true}`, `dpg snapshot 0.2.0: json: unknown field "live"`},
		{"unknown nested field", `{"dpg_version": "0.2.0", "objects": {"s": {"kind": "schema", "schema": {"name": "s", "x": 1}}}}`, `dpg snapshot 0.2.0: json: unknown field "x"`},
		{"null object", `{"dpg_version": "0.2.0", "objects": {"s": null}}`, `dpg snapshot 0.2.0: objects["s"]: object is null`},
		{"unknown kind", `{"dpg_version": "0.2.0", "objects": {"s": {"kind": "rule"}}}`, `dpg snapshot 0.2.0: objects["s"]: unknown object kind "rule"`},
		{"missing kind", `{"dpg_version": "0.2.0", "objects": {"s": {"schema": {"name": "s"}}}}`, `dpg snapshot 0.2.0: objects["s"]: unknown object kind ""`},
		{"opaque is not a kind", `{"dpg_version": "0.2.0", "objects": {"s": {"kind": "opaque", "opaque": {"kind": "opaque", "name": "s"}}}}`, `dpg snapshot 0.2.0: objects["s"]: unknown object kind "opaque"`},
		{"no payload", `{"dpg_version": "0.2.0", "objects": {"s": {"kind": "schema"}}}`, `dpg snapshot 0.2.0: objects["s"]: kind "schema" needs exactly the field "schema", found []`},
		{"wrong payload", `{"dpg_version": "0.2.0", "objects": {"s": {"kind": "schema", "table": {"schema": "s", "name": "t"}}}}`, `dpg snapshot 0.2.0: objects["s"]: kind "schema" needs exactly the field "schema", found [table]`},
		{"two payloads", `{"dpg_version": "0.2.0", "objects": {"s": {"kind": "view", "view": {"schema": "s", "name": "v", "query": ""}, "schema": {"name": "s"}}}}`, `dpg snapshot 0.2.0: objects["s"]: kind "view" needs exactly the field "view", found [schema, view]`},
		{"opaque kind without opaque", `{"dpg_version": "0.2.0", "objects": {"p": {"kind": "procedure", "function": {"schema": "s", "name": "p", "args": "", "return_type": "", "language": "", "volatility": "", "body_hash": ""}}}}`, `dpg snapshot 0.2.0: objects["p"]: kind "procedure" needs exactly the field "opaque", found [function]`},
		{"opaque kind mismatch", `{"dpg_version": "0.2.0", "objects": {"p": {"kind": "procedure", "opaque": {"kind": "aggregate", "name": "p"}}}}`, `dpg snapshot 0.2.0: objects["p"]: kind "procedure" holds an opaque record of kind "aggregate"`},
	} {
		_, err := Parse([]byte(tc.data))
		if err == nil || !strings.HasPrefix(err.Error(), tc.want) {
			t.Errorf("%s: got %v\nwant %s", tc.name, err, tc.want)
		}
	}
}

// Every kind dpg writes decodes into its own payload.
func TestEveryKind(t *testing.T) {
	payloads := map[string]string{
		"table":                `"table": {"schema": "s", "name": "t"}`,
		"view":                 `"view": {"schema": "s", "name": "v", "query": "SELECT 1"}`,
		"function":             `"function": {"schema": "s", "name": "f", "args": "", "return_type": "integer", "language": "sql", "volatility": "VOLATILE", "body_hash": ""}`,
		"type":                 `"type": {"schema": "s", "name": "e", "variant": "ENUM"}`,
		"schema":               `"schema": {"name": "s"}`,
		"extension":            `"extension": {"name": "pgcrypto"}`,
		"sequence":             `"sequence": {"schema": "s", "name": "q"}`,
		"role":                 `"role": {"name": "r"}`,
		"virtual_type":         `"virtual_type": {"name": "vt", "body": {"kind": "type_ref", "name": "text"}}`,
		"default_privileges":   `"default_privileges": {"object_type": "TABLES"}`,
		"parameter_privileges": `"parameter_privileges": {}`,
	}
	for _, k := range opaqueKinds {
		payloads[k] = `"opaque": {"kind": "` + k + `", "name": "o"}`
	}
	for kind, payload := range payloads {
		data := `{"dpg_version": "0.2.0", "objects": {"o": {"kind": "` + kind + `", ` + payload + `}}}`
		s, err := Parse([]byte(data))
		if err != nil {
			t.Errorf("%s: %v", kind, err)
			continue
		}
		if s.Objects[0].Object.Kind != kind {
			t.Errorf("%s: decoded as %q", kind, s.Objects[0].Object.Kind)
		}
	}
}
