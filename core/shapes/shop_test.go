package shapes

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/thec1oud/dql/core/bundle"
	_ "github.com/thec1oud/dql/proto/dql/ext/postgres/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/testkit/golden"
)

// shop loads the golden catalog IR of the running example (§1.3), which the
// M1 gate pins.
func shop(t *testing.T) []*irv1.Catalog {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "shop", "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	var b irv1.Bundle
	if err := protojson.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	return b.GetCatalogs()
}

// summary renders a shape as one line per column, in column order: the
// field's presence, nullability, value kind, validation, redaction and role,
// or its forbidden reason. It is how the tests below write the record's
// tables.
func summary(s *irv1.Shape) string {
	var b strings.Builder
	for _, f := range s.GetFields() {
		fmt.Fprintf(&b, "%s %s", f.GetColumn(), strings.TrimPrefix(f.GetPresence().String(), "PRESENCE_"))
		if f.GetNullable() {
			b.WriteString(" nullable")
		}
		if f.GetValueKind() != irv1.ValueKind_VALUE_KIND_UNSPECIFIED {
			b.WriteString(" " + strings.TrimPrefix(f.GetValueKind().String(), "VALUE_KIND_"))
		}
		if len(f.GetValidation()) > 0 {
			b.WriteString(" validate(" + strings.Join(f.GetValidation(), ",") + ")")
		}
		if f.GetRedact() {
			b.WriteString(" redact")
		}
		if f.GetRole() != irv1.FieldRole_FIELD_ROLE_UNSPECIFIED {
			b.WriteString(" " + strings.TrimPrefix(f.GetRole().String(), "FIELD_ROLE_"))
		}
		b.WriteString("\n")
	}
	for _, f := range s.GetForbidden() {
		fmt.Fprintf(&b, "%s %s\n", f.GetColumn(), f.GetReason())
	}
	return b.String()
}

func byID(t *testing.T, shapes []*irv1.Shape, object string, p irv1.Preset) *irv1.Shape {
	t.Helper()
	for _, s := range shapes {
		if s.GetEntity().GetObject() == object && s.GetId().GetPreset() == p {
			return s
		}
	}
	t.Fatalf("no %s %s", object, p)
	return nil
}

// TestUsersExpansion is the M2 gate item: the presets of app.users match
// §14.3 exactly, and the Partial matches §14.10.
func TestUsersExpansion(t *testing.T) {
	shapes := Presets(shop(t))
	users := []string{
		irv1.Preset_PRESET_FULL: `id REQUIRED
tenant_id REQUIRED
email REQUIRED
display_name REQUIRED nullable
created_at REQUIRED
updated_at REQUIRED
version REQUIRED
password_hash FORBIDDEN_WRITE_ONLY
`,
		irv1.Preset_PRESET_DRAFT: `email REQUIRED VALUE_OR_EXPRESSION validate(email_address_check)
display_name OPTIONAL nullable VALUE_OR_EXPRESSION
password_hash REQUIRED VALUE_OR_EXPRESSION redact
created_at OPTIONAL VALUE_OR_EXPRESSION
updated_at OPTIONAL VALUE_OR_EXPRESSION
version OPTIONAL VALUE_OR_EXPRESSION
id FORBIDDEN_IDENTITY_ALWAYS
tenant_id FORBIDDEN_BOUND
`,
		irv1.Preset_PRESET_PARTIAL: `email OPTIONAL VALUE_OR_EXPRESSION validate(email_address_check)
display_name OPTIONAL nullable VALUE_OR_EXPRESSION
password_hash OPTIONAL VALUE_OR_EXPRESSION redact
id FORBIDDEN_KEY
tenant_id FORBIDDEN_BOUND
created_at FORBIDDEN_IMMUTABLE
updated_at FORBIDDEN_DB_UPDATED
version FORBIDDEN_VERSION
`,
		irv1.Preset_PRESET_KEY: `id REQUIRED
version REQUIRED EXPECTED_VERSION
`,
		irv1.Preset_PRESET_SCOPE: `tenant_id REQUIRED
`,
	}
	for p, want := range users {
		if p == 0 {
			continue
		}
		s := byID(t, shapes, "users", irv1.Preset(p))
		if got := summary(s); got != want {
			t.Errorf("users %s:\n got %s\nwant %s", irv1.Preset(p), got, want)
		}
		if got := s.GetScope(); len(got) != 1 || got[0] != "tenant_id" {
			t.Errorf("users %s scope %v", irv1.Preset(p), got)
		}
	}

	// The identity and lineage of §14.10.
	partial := byID(t, shapes, "users", irv1.Preset_PRESET_PARTIAL)
	if got := Spelling(partial.GetId(), []string{"user"}); got != "user.partial.flat" {
		t.Errorf("id %s", got)
	}
	if got := strings.Join(partial.GetHostName().GetWords(), " "); got != "user partial" || partial.GetHostName().GetStored() != "" {
		t.Errorf("host name %v", partial.GetHostName())
	}
	root := partial.GetLineage()
	if len(root) != 1 || root[0].GetOp() != irv1.ShapeOp_PRESET || root[0].GetPreset() != irv1.Preset_PRESET_PARTIAL ||
		root[0].GetForm() != irv1.Form_FORM_FLAT || root[0].GetEntity().GetObject() != "users" {
		t.Errorf("lineage %v", root)
	}
	if got := partial.GetFields()[1].GetType().GetVarchar().GetLength(); got != 80 {
		t.Errorf("display_name type %v", partial.GetFields()[1].GetType())
	}
	key := byID(t, shapes, "users", irv1.Preset_PRESET_KEY)
	if key.GetId().GetForm() != irv1.Form_FORM_UNSPECIFIED || Spelling(key.GetId(), []string{"user"}) != "user.key" {
		t.Errorf("key id %v", key.GetId())
	}
}

// TestUsersUpsert checks the upsert of §14.7 on users_tenant_email_key.
func TestUsersUpsert(t *testing.T) {
	var got *irv1.Upsert
	for _, u := range Upserts(shop(t)) {
		if u.GetEntity().GetObject() == "users" && u.GetConflictTarget() == "users_tenant_email_key" {
			got = u
		}
	}
	if got == nil {
		t.Fatal("no upsert on users_tenant_email_key")
	}
	if s := Spelling(got.GetInsertShape(), []string{"user"}); s != "user.draft.flat" {
		t.Errorf("insert shape %s", s)
	}
	var excludes []string
	for _, e := range got.GetDefaultUpdateExcludes() {
		excludes = append(excludes, e.GetColumn()+" "+e.GetReason().String())
	}
	want := "tenant_id EXCLUDE_BOUND, email EXCLUDE_CONFLICT_KEY, created_at EXCLUDE_IMMUTABLE, " +
		"updated_at EXCLUDE_DB_UPDATED, version EXCLUDE_VERSION"
	if s := strings.Join(excludes, ", "); s != want {
		t.Errorf("excludes\n got %s\nwant %s", s, want)
	}
}

// TestShopGolden pins every preset and upsert of the running example.
func TestShopGolden(t *testing.T) {
	cs := shop(t)
	layer := &irv1.ShapeLayer{Shapes: Presets(cs), Upserts: Upserts(cs)}
	raw, err := bundle.MarshalJSON(layer)
	if err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, filepath.Join("testdata", "presets", "shop.json"), raw)
}

// TestShopEntities checks which presets each kind of entity gets: a view only
// Full and Key (§6.4), and an entity without bound columns no Scope.
func TestShopEntities(t *testing.T) {
	var got []string
	for _, s := range Presets(shop(t)) {
		got = append(got, s.GetEntity().GetObject()+"."+strings.TrimPrefix(s.GetId().GetPreset().String(), "PRESET_"))
	}
	want := "post_tags.FULL post_tags.DRAFT post_tags.PARTIAL post_tags.KEY " +
		"posts.FULL posts.DRAFT posts.PARTIAL posts.KEY " +
		"profiles.FULL profiles.DRAFT profiles.PARTIAL profiles.KEY " +
		"published_posts.FULL published_posts.KEY " +
		"tags.FULL tags.DRAFT tags.PARTIAL tags.KEY " +
		"tenants.FULL tenants.DRAFT tenants.PARTIAL tenants.KEY " +
		"users.FULL users.DRAFT users.PARTIAL users.KEY users.SCOPE"
	if s := strings.Join(got, " "); s != want {
		t.Errorf("presets\n got %s\nwant %s", s, want)
	}
}

// TestPostsPresets covers the column classes users lacks: identity by
// default, generated, constant default and a table check (§11.3, §14.3).
func TestPostsPresets(t *testing.T) {
	shapes := Presets(shop(t))
	draft := `id OPTIONAL VALUE_OR_EXPRESSION
author_id REQUIRED VALUE_OR_EXPRESSION
editor_id OPTIONAL nullable VALUE_OR_EXPRESSION
title REQUIRED VALUE_OR_EXPRESSION validate(posts_title_check)
status OPTIONAL VALUE_OR_EXPRESSION
price OPTIONAL nullable VALUE_OR_EXPRESSION
labels OPTIONAL nullable VALUE_OR_EXPRESSION
published_at OPTIONAL nullable VALUE_OR_EXPRESSION
slug FORBIDDEN_GENERATED
`
	if got := summary(byID(t, shapes, "posts", irv1.Preset_PRESET_DRAFT)); got != draft {
		t.Errorf("posts draft:\n%s", got)
	}
	partial := `author_id OPTIONAL VALUE_OR_EXPRESSION
editor_id OPTIONAL nullable VALUE_OR_EXPRESSION
title OPTIONAL VALUE_OR_EXPRESSION validate(posts_title_check)
status OPTIONAL VALUE_OR_EXPRESSION
price OPTIONAL nullable VALUE_OR_EXPRESSION
labels OPTIONAL nullable VALUE_OR_EXPRESSION
published_at OPTIONAL nullable VALUE_OR_EXPRESSION
id FORBIDDEN_KEY
slug FORBIDDEN_GENERATED
`
	if got := summary(byID(t, shapes, "posts", irv1.Preset_PRESET_PARTIAL)); got != partial {
		t.Errorf("posts partial:\n%s", got)
	}
	if got := summary(byID(t, shapes, "published_posts", irv1.Preset_PRESET_FULL)); got != `id REQUIRED
title REQUIRED
author_id REQUIRED nullable
published_at REQUIRED nullable
` {
		t.Errorf("published_posts full:\n%s", got)
	}
}
