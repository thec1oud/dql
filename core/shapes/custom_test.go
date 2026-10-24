package shapes

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/thec1oud/dql/core/bundle"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/testkit/golden"
)

// The custom shapes of §14.8.
func customRecord() []Custom {
	return []Custom{
		{Name: "user_summary", From: "user.full", Pick: []string{"id", "email", "display_name"}},
		{Name: "user_summary_named", From: "user_summary", Nonnull: []string{"display_name"}},
		{Name: "post_with_tags", From: "post.full", Nest: map[string]string{"tags": "tag.full", "author": "user_summary"}},
		{Name: "signup", From: "user.draft", Pick: []string{"email", "password_hash"}},
		{Name: "quick_invite", From: "user.draft", Pick: []string{"email"}, Bind: []string{"password_hash"}},
	}
}

func deriveCustom(t *testing.T, customs []Custom, nester WriteNester) (map[string]*irv1.Shape, []*irv1.Shape, []*irv1.RelationUnion, []*irv1.Diagnostic) {
	t.Helper()
	shapes, unions, ds := DeriveCustom(customs, customPresets(t), customCatalogs(), nester)
	byName := map[string]*irv1.Shape{}
	for _, s := range shapes {
		if name := s.GetId().GetCustom(); name != "" && s.GetId().GetForRelation() == "" {
			byName[name] = s
		}
	}
	return byName, shapes, unions, ds
}

func assertShape(t *testing.T, got *irv1.Shape, want string) {
	t.Helper()
	if w := customShape(t, want); !proto.Equal(got, w) {
		gj, _ := bundle.MarshalJSON(got)
		wj, _ := bundle.MarshalJSON(w)
		t.Errorf("shape differs\n got: %s\nwant: %s", gj, wj)
	}
}

func TestCustomRecordShapes(t *testing.T) {
	byName, shapes, unions, ds := deriveCustom(t, customRecord(), nil)
	if len(ds) != 0 || len(unions) != 0 || len(shapes) != 5 {
		t.Fatalf("diagnostics %v, unions %v, %d shapes", ds, unions, len(shapes))
	}
	for i, name := range []string{"post_with_tags", "quick_invite", "signup", "user_summary", "user_summary_named"} {
		if got := shapes[i].GetId().GetCustom(); got != name {
			t.Errorf("shape %d is %s, want %s", i, got, name)
		}
	}

	// type UserSummary struct { ID int64; Email EmailAddress; DisplayName *string }
	assertShape(t, byName["user_summary"], `{
	  "id": { "custom": "user_summary" },
	  "hostName": { "words": ["user", "summary"] },
	  "entity": { "catalog": "shop", "namespace": "app", "object": "users" },
	  "lineage": [
	    { "op": "PRESET", "entity": { "catalog": "shop", "namespace": "app", "object": "users" }, "preset": "PRESET_FULL", "form": "FORM_FLAT" },
	    { "op": "PICK", "columns": ["id", "email", "display_name"] }
	  ],
	  "fields": [
	    { "column": "id", "presence": "PRESENCE_REQUIRED", "nullabilitySource": "NULLABILITY_SOURCE_DATABASE", "type": `+customI64+` },
	    { "column": "email", "presence": "PRESENCE_REQUIRED", "nullabilitySource": "NULLABILITY_SOURCE_DATABASE", "type": `+customMail+` },
	    { "column": "display_name", "presence": "PRESENCE_REQUIRED", "nullable": true, "nullabilitySource": "NULLABILITY_SOURCE_UNPROVEN", "type": `+customText+` }
	  ],
	  "forbidden": [
	    { "column": "tenant_id", "reason": "FORBIDDEN_OMITTED" },
	    { "column": "password_hash", "reason": "FORBIDDEN_WRITE_ONLY" },
	    { "column": "created_at", "reason": "FORBIDDEN_OMITTED" },
	    { "column": "updated_at", "reason": "FORBIDDEN_OMITTED" },
	    { "column": "version", "reason": "FORBIDDEN_OMITTED" }
	  ],
	  "scope": ["tenant_id"]
	}`)

	// DisplayName string // narrowed; enforced when decoding
	named := byName["user_summary_named"]
	if l := named.GetLineage(); len(l) != 4 || l[2].GetOp() != irv1.ShapeOp_FROM || l[2].GetShape().GetCustom() != "user_summary" ||
		l[3].GetOp() != irv1.ShapeOp_NONNULL || !slices.Equal(l[3].GetColumns(), []string{"display_name"}) {
		t.Errorf("user_summary_named lineage: %v", l)
	}
	dn := named.GetFields()[2]
	if dn.GetNullable() || dn.GetNullabilitySource() != irv1.NullabilitySource_NULLABILITY_SOURCE_SHAPE_OPERATOR || dn.GetNullabilityShape().GetCustom() != "user_summary_named" {
		t.Errorf("display_name is not narrowed by user_summary_named: %v", dn)
	}

	// type PostWithTags struct { PostFull; Tags []TagFull; Author UserSummary }
	assertShape(t, byName["post_with_tags"], `{
	  "id": { "custom": "post_with_tags" },
	  "hostName": { "words": ["post", "with", "tags"] },
	  "entity": { "catalog": "shop", "namespace": "app", "object": "posts" },
	  "lineage": [
	    { "op": "PRESET", "entity": { "catalog": "shop", "namespace": "app", "object": "posts" }, "preset": "PRESET_FULL", "form": "FORM_FLAT" },
	    { "op": "NEST", "relation": "author", "shape": { "custom": "user_summary" } },
	    { "op": "NEST", "relation": "tags", "shape": { "entity": { "catalog": "shop", "namespace": "app", "object": "tags" }, "preset": "PRESET_FULL", "form": "FORM_FLAT" } }
	  ],
	  "fields": [
	    { "column": "id", "presence": "PRESENCE_REQUIRED", "type": `+customI64+` },
	    { "column": "author_id", "presence": "PRESENCE_REQUIRED", "type": `+customI64+` },
	    { "column": "editor_id", "presence": "PRESENCE_REQUIRED", "nullable": true, "type": `+customI64+` },
	    { "column": "title", "presence": "PRESENCE_REQUIRED", "type": `+customText+` }
	  ],
	  "nests": [
	    { "relation": "author", "presence": "PRESENCE_REQUIRED", "shape": { "custom": "user_summary" } },
	    { "relation": "tags", "presence": "PRESENCE_REQUIRED",
	      "shape": { "entity": { "catalog": "shop", "namespace": "app", "object": "tags" }, "preset": "PRESET_FULL", "form": "FORM_FLAT" } }
	  ]
	}`)

	// type Signup struct { Email EmailAddress; PasswordHash string }
	if f := byName["signup"].GetFields(); len(f) != 2 || f[0].GetColumn() != "email" || f[1].GetColumn() != "password_hash" || !f[1].GetRedact() {
		t.Errorf("signup fields: %v", f)
	}

	// type QuickInvite struct { Email EmailAddress } // password_hash is bound
	assertShape(t, byName["quick_invite"], `{
	  "id": { "custom": "quick_invite" },
	  "hostName": { "words": ["quick", "invite"] },
	  "entity": { "catalog": "shop", "namespace": "app", "object": "users" },
	  "lineage": [
	    { "op": "PRESET", "entity": { "catalog": "shop", "namespace": "app", "object": "users" }, "preset": "PRESET_DRAFT", "form": "FORM_FLAT" },
	    { "op": "PICK", "columns": ["email"] },
	    { "op": "BIND", "columns": ["password_hash"] }
	  ],
	  "fields": [
	    { "column": "email", "presence": "PRESENCE_REQUIRED", "nullabilitySource": "NULLABILITY_SOURCE_DATABASE", "valueKind": "VALUE_KIND_VALUE_OR_EXPRESSION", "type": `+customMail+`, "validation": ["email_address_check"] }
	  ],
	  "forbidden": [
	    { "column": "id", "reason": "FORBIDDEN_IDENTITY_ALWAYS" },
	    { "column": "tenant_id", "reason": "FORBIDDEN_BOUND" },
	    { "column": "display_name", "reason": "FORBIDDEN_OMITTED" },
	    { "column": "created_at", "reason": "FORBIDDEN_OMITTED" },
	    { "column": "updated_at", "reason": "FORBIDDEN_OMITTED" },
	    { "column": "version", "reason": "FORBIDDEN_OMITTED" }
	  ],
	  "scope": ["tenant_id"],
	  "bind": ["password_hash"]
	}`)

	js, err := bundle.MarshalJSON(&irv1.ShapeLayer{Shapes: shapes})
	if err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, filepath.Join("testdata", "custom", "record.json"), js)
}

// §14.8: without bind, quick_invite fails with the documented diagnostic.
func TestCustomQuickInviteWithoutBind(t *testing.T) {
	_, shapes, _, ds := deriveCustom(t, []Custom{{Name: "quick_invite", From: "user.draft", Pick: []string{"email"}}}, nil)
	want := &irv1.Diagnostic{
		Severity: irv1.Severity_SEVERITY_ERROR,
		Code:     "write-shape-incomplete",
		Shape:    "quick_invite",
		Message:  "shape derived from user.draft drops required field password_hash; keep it or bind it",
	}
	if len(shapes) != 0 || len(ds) != 1 || !proto.Equal(ds[0], want) {
		t.Errorf("shapes %v, diagnostics %v", shapes, ds)
	}
}

// Every way a custom shape can fail, pinned with its message for review.
func TestCustomDiagnostics(t *testing.T) {
	cases := []struct {
		name    string
		customs []Custom
	}{
		{"names", []Custom{{Name: "UserSummary", From: "user.full"}, {Name: "", From: "user.full"}, {Name: "user_full", From: "user.full"}}},
		{"unknown from", []Custom{
			{Name: "a", From: "users.full"}, {Name: "b", From: "user.fancy"}, {Name: "c", From: "user.key.flat"},
			{Name: "d", From: "user.full.flat.for"}, {Name: "e", From: "user.full.weird"}, {Name: "f"},
			{Name: "g", From: ".full"}, {Name: "h", From: "nothing"}, {Name: "i", From: "post.draft.for.editor"},
		}},
		{"cycle", []Custom{{Name: "a", From: "b"}, {Name: "b", From: "a"}, {Name: "c", From: "a"}}},
		{"self nest", []Custom{{Name: "post_tree", From: "post.full", Nest: map[string]string{"author": "user_tree"}}, {Name: "user_tree", From: "user.full", Nest: map[string]string{"posts_to_author": "post_tree"}}}},
		{"unknown and forbidden fields", []Custom{
			{Name: "a", From: "user.full", Pick: []string{"nope", "password_hash"}},
			{Name: "b", From: "post.draft.nested", Require: []string{"author"}},
			{Name: "invite", From: "user.draft", Pick: []string{"email"}, Bind: []string{"password_hash"}},
			{Name: "c", From: "invite", Nullify: []string{"password_hash"}},
		}},
		{"operators not allowed", []Custom{
			{Name: "a", From: "user.full", Require: []string{"email"}, Optionalize: []string{"id"}, Bind: []string{"email"}},
			{Name: "b", From: "user.key", Nest: map[string]string{"posts_to_author": "post.full"}},
		}},
		{"conflicts", []Custom{
			{Name: "a", From: "user.full", Pick: []string{"id", "id"}},
			{Name: "b", From: "user.full", Pick: []string{"id"}, Omit: []string{"id"}},
			{Name: "c", From: "user.draft", Require: []string{"email"}, Optionalize: []string{"email"}},
			{Name: "d", From: "user.full", Nullify: []string{"email"}, Nonnull: []string{"email"}},
			{Name: "e", From: "user.full", Omit: []string{"email"}, Nonnull: []string{"email"}},
			{Name: "f", From: "user.full.nested", Pick: []string{"posts_to_author"}, Nest: map[string]string{"posts_to_author": "post.full"}},
		}},
		{"nests", []Custom{
			{Name: "a", From: "post.full", Nest: map[string]string{"nope": "tag.full"}},
			{Name: "b", From: "tag.full", Nest: map[string]string{"posts": "post.full"}},
			{Name: "c", From: "post.full", Nest: map[string]string{"author": "tag.full"}},
			{Name: "d", From: "post.full", Nest: map[string]string{"author": "user.draft"}},
			{Name: "e", From: "post.full", Nest: map[string]string{"author": "user.fancy"}},
			{Name: "f", From: "post.full", Nest: map[string]string{"author": "h"}},
			{Name: "h", From: "nothing"},
		}},
		{"write rule", []Custom{
			{Name: "a", From: "user.draft", Omit: []string{"email", "password_hash", "display_name"}},
			{Name: "b", From: "post.draft.nested", Omit: []string{"author", "author_id"}},
			{Name: "c", From: "a", Pick: []string{"version"}},
		}},
	}
	var b strings.Builder
	for _, c := range cases {
		_, shapes, _, ds := deriveCustom(t, c.customs, nil)
		fmt.Fprintf(&b, "%s:\n", c.name)
		for _, d := range ds {
			if d.GetSeverity() != irv1.Severity_SEVERITY_ERROR || d.GetTarget() != nil {
				t.Errorf("%s: %v", c.name, d)
			}
			fmt.Fprintf(&b, "  %s %s: %s\n", d.GetShape(), d.GetCode(), d.GetMessage())
		}
		for _, s := range shapes {
			fmt.Fprintf(&b, "  derived %s\n", s.GetId().GetCustom())
		}
	}
	golden.Assert(t, filepath.Join("testdata", "custom", "diagnostics.txt"), []byte(b.String()))
}

func TestCustomPresetReferences(t *testing.T) {
	byName, _, _, ds := deriveCustom(t, []Custom{
		{Name: "a", From: "post.draft.for.author"},
		{Name: "b", From: "post.draft.nested.for.author"},
		{Name: "c", From: "user.key", Pick: []string{"id"}},
		{Name: "d", From: "user.full.flat"},
	}, nil)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	for name, want := range map[string]string{"a": "author", "b": "author", "c": "", "d": ""} {
		if l := byName[name].GetLineage(); l[len(l)-1].GetRelation() != want && want != "" {
			t.Errorf("%s lineage: %v", name, l)
		}
	}
	if f := byName["c"].GetFields(); len(f) != 1 || byName["c"].GetForbidden()[0].GetColumn() != "version" {
		t.Errorf("c: %v", byName["c"])
	}
}

func TestCustomModalities(t *testing.T) {
	byName, _, _, ds := deriveCustom(t, []Custom{
		{Name: "patch_email", From: "user.partial", Pick: []string{"email"}, Require: []string{"email"}},
		{Name: "bind_two", From: "user.draft", Bind: []string{"password_hash", "email"}},
		{Name: "loose", From: "user.draft", Optionalize: []string{"email"}, Nullify: []string{"email", "display_name"}, Nonnull: []string{"created_at"}},
	}, nil)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	if f := byName["patch_email"].GetFields(); len(f) != 1 || f[0].GetPresence() != irv1.Presence_PRESENCE_REQUIRED {
		t.Errorf("patch_email: %v", f)
	}
	if b := byName["bind_two"]; !slices.Equal(b.GetBind(), []string{"email", "password_hash"}) || !slices.Equal(b.GetLineage()[1].GetColumns(), []string{"password_hash", "email"}) {
		t.Errorf("bind_two: %v", b)
	}
	f := byName["loose"].GetFields()
	email, display, created := f[0], f[1], f[3]
	if email.GetPresence() != irv1.Presence_PRESENCE_OPTIONAL || !email.GetNullable() ||
		email.GetNullabilitySource() != irv1.NullabilitySource_NULLABILITY_SOURCE_SHAPE_OPERATOR || email.GetNullabilityShape().GetCustom() != "loose" {
		t.Errorf("email: %v", email)
	}
	// Already nullable and already non-null: nothing to record.
	if display.GetNullabilitySource() != irv1.NullabilitySource_NULLABILITY_SOURCE_UNPROVEN || display.GetNullabilityShape() != nil {
		t.Errorf("display_name: %v", display)
	}
	if created.GetNullable() || created.GetNullabilitySource() != irv1.NullabilitySource_NULLABILITY_SOURCE_DATABASE {
		t.Errorf("created_at: %v", created)
	}
}

func TestCustomReadNests(t *testing.T) {
	byName, _, _, ds := deriveCustom(t, []Custom{
		{Name: "post_summary", From: "post.full", Pick: []string{"id", "title"}},
		{Name: "user_posts", From: "user.full.nested", Pick: []string{"id", "posts_to_author"}},
		{Name: "user_post_summaries", From: "user.full.nested", Nest: map[string]string{"posts_to_author": "post_summary"}},
		{Name: "user_bare", From: "user.full.nested", Omit: []string{"posts_to_author"}},
		{Name: "user_again", From: "user_bare", Nest: map[string]string{"posts_to_author": "post.full"}},
		{Name: "post_editor", From: "post.full", Nest: map[string]string{"editor": "user_summary"}},
		{Name: "user_summary", From: "user.full", Pick: []string{"id"}},
	}, nil)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	up := byName["user_posts"]
	if len(up.GetFields()) != 1 || len(up.GetNests()) != 1 || up.GetNests()[0].GetRelation() != "posts_to_author" {
		t.Errorf("user_posts: %v", up)
	}
	if f := up.GetForbidden(); f[len(f)-1].GetRelation() != "profile" || f[len(f)-1].GetReason() != irv1.ForbiddenReason_FORBIDDEN_OMITTED || f[0].GetColumn() != "email" {
		t.Errorf("user_posts forbidden: %v", f)
	}
	if n := byName["user_post_summaries"].GetNests(); len(n) != 2 || n[0].GetShape().GetCustom() != "post_summary" {
		t.Errorf("user_post_summaries nests: %v", n)
	}
	again := byName["user_again"]
	if n := again.GetNests(); len(n) != 2 || n[0].GetRelation() != "posts_to_author" || n[1].GetRelation() != "profile" {
		t.Errorf("user_again nests: %v", n)
	}
	for _, f := range again.GetForbidden() {
		if f.GetRelation() != "" {
			t.Errorf("user_again still forbids %s", f.GetRelation())
		}
	}
	if n := byName["post_editor"].GetNests(); len(n) != 1 || !n[0].GetNullable() {
		t.Errorf("post_editor: an optional to-one relation is nullable: %v", n)
	}
}

func TestCustomWriteNests(t *testing.T) {
	f := &customFakeNester{}
	byName, shapes, unions, ds := deriveCustom(t, []Custom{
		{Name: "post_tagged", From: "post.draft", Nest: map[string]string{"tags": "tag.draft", "author": "user.draft"}},
	}, f)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	if !slices.Equal(f.calls, []string{"post_tagged.author", "post_tagged.tags"}) {
		t.Errorf("calls: %v", f.calls)
	}
	if len(shapes) != 3 || shapes[1].GetId().GetForRelation() != "author" || len(unions) != 2 {
		t.Errorf("shapes %v, unions %v", shapes, unions)
	}
	if n := byName["post_tagged"].GetNests(); len(n) != 2 || n[0].GetUnion().GetCustom() != "post_tagged" {
		t.Errorf("nests: %v", n)
	}

	fail := &irv1.Diagnostic{Severity: irv1.Severity_SEVERITY_ERROR, Code: "nest-failed", Shape: "post_tagged"}
	_, shapes, _, ds = deriveCustom(t, []Custom{{Name: "post_tagged", From: "post.draft", Nest: map[string]string{"tags": "tag.draft"}}}, &customFakeNester{fail: fail})
	if len(shapes) != 0 || len(ds) != 1 || ds[0] != fail {
		t.Errorf("a failed nest: shapes %v, diagnostics %v", shapes, ds)
	}
}

// Dropping an owning relation from a Nested Draft leaves its foreign key to
// the presence of the Flat form; dropping the key leaves the relation as the
// only supplier.
func TestCustomNestedDraftKeys(t *testing.T) {
	byName, _, _, ds := deriveCustom(t, []Custom{
		{Name: "no_author", From: "post.draft.nested", Omit: []string{"author"}},
		{Name: "no_author_id", From: "post.draft.nested", Omit: []string{"author_id", "editor_id"}},
		{Name: "bound_author", From: "post.draft.nested", Bind: []string{"author_id"}},
		{Name: "required_author", From: "post.draft.nested", Require: []string{"author_id"}},
		{Name: "required_no_author", From: "required_author", Omit: []string{"author"}},
		{Name: "child_no_editor", From: "post.draft.nested.for.author", Omit: []string{"editor"}},
	}, nil)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	presence := func(s *irv1.Shape, col string) irv1.Presence {
		for _, f := range s.GetFields() {
			if f.GetColumn() == col {
				return f.GetPresence()
			}
		}
		return irv1.Presence_PRESENCE_UNSPECIFIED
	}
	if s := byName["no_author"]; presence(s, "author_id") != irv1.Presence_PRESENCE_REQUIRED || presence(s, "editor_id") != irv1.Presence_PRESENCE_OPTIONAL ||
		s.GetForbidden()[0].GetRelation() != "author" {
		t.Errorf("no_author: %v", s)
	}
	if n := byName["no_author_id"].GetNests(); n[0].GetPresence() != irv1.Presence_PRESENCE_REQUIRED || n[0].GetNeedsOne() || len(n[0].GetSupplies()) != 0 ||
		n[1].GetPresence() != irv1.Presence_PRESENCE_OPTIONAL || len(n[1].GetSupplies()) != 0 {
		t.Errorf("no_author_id: %v", n)
	}
	if n := byName["bound_author"].GetNests()[0]; n.GetPresence() != irv1.Presence_PRESENCE_OPTIONAL || n.GetNeedsOne() {
		t.Errorf("bound_author: %v", n)
	}
	if s := byName["required_no_author"]; presence(s, "author_id") != irv1.Presence_PRESENCE_REQUIRED {
		t.Errorf("required_no_author: %v", s)
	}
	if s := byName["child_no_editor"]; presence(s, "editor_id") != irv1.Presence_PRESENCE_OPTIONAL || len(s.GetNests()) != 0 {
		t.Errorf("child_no_editor: %v", s)
	}

	// Without a Flat form to restore from, the presence stays.
	var presets []*irv1.Shape
	for _, s := range customPresets(t) {
		if s.GetId().GetForm() != irv1.Form_FORM_FLAT || s.GetId().GetPreset() != irv1.Preset_PRESET_DRAFT {
			presets = append(presets, s)
		}
	}
	shapes, _, ds := DeriveCustom([]Custom{{Name: "no_author", From: "post.draft.nested", Omit: []string{"author"}}}, presets, customCatalogs(), nil)
	if len(ds) != 0 || shapes[0].GetFields()[1].GetPresence() != irv1.Presence_PRESENCE_OPTIONAL {
		t.Errorf("no flat form: %v %v", shapes, ds)
	}
}

// §3.2: the input order of custom shapes does not change the output.
func TestCustomDeterministic(t *testing.T) {
	want, _, _ := DeriveCustom(customRecord(), customPresets(t), customCatalogs(), nil)
	customs := customRecord()
	slices.Reverse(customs)
	got, _, _ := DeriveCustom(customs, customPresets(t), customCatalogs(), nil)
	gj, _ := bundle.MarshalJSON(&irv1.ShapeLayer{Shapes: got})
	wj, _ := bundle.MarshalJSON(&irv1.ShapeLayer{Shapes: want})
	if string(gj) != string(wj) {
		t.Error("reversed input changed the output")
	}
}
