package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/thec1oud/dql/cmd/dql/internal/config"
	"github.com/thec1oud/dql/core/shapes"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// This file is the M2 gate (implementation guide §4 M2): dql generate on
// testdata/shop with the custom shapes of §14.8 produces a shape layer that
// matches every shape example of the design record about shop:
//
//   - the users expansion of §14.3, with §7.5 step 4 and §14.10;
//   - the custom shapes of §14.8, and quick_invite without bind failing with
//     the documented diagnostic;
//   - the user conversion table of §14.9 and the conversions of §14.10;
//   - the nested Drafts and Partials of user and post of §14.5 and §14.6,
//     the relation consistency rule included, and the Nests of §12.5;
//   - the upsert of §14.7.
//
// Examples are compared by containment, as in the M1 gate. Go and
// TypeScript examples are compared through the IR they are generated from:
// a field's presence and nullability (§14.1), a relation field's target and
// the variants of its union. What the examples say that the IR spells
// differently:
//
//   - §7.5 step 4 lists password_hash in user.full.flat with
//     "presence": "PRESENCE_FORBIDDEN"; the IR lists a forbidden column
//     under forbidden, with its reason, FORBIDDEN_WRITE_ONLY.
//   - §14.7 and §14.10 write shape ids in their dotted spelling, such as
//     "insertShape": "user.draft.flat"; the IR carries the structured
//     ShapeId, whose dotted spelling is the display form.
//   - §14.10 shows the lineage step without its entity and form, which the
//     IR also carries.

// gateShapes generates the running example with the custom shapes of §14.8
// and returns the bundle and a lookup of its shapes by dotted spelling.
func gateShapes(t *testing.T) (*irv1.Bundle, func(string) *irv1.Shape) {
	t.Helper()
	_, b := generateBundle(t, shopConfig(t, recordShapes), shopURL)
	return b, func(name string) *irv1.Shape {
		t.Helper()
		return named(t, b.GetShapes().GetShapes(), func(s *irv1.Shape) string { return spell(b, s.GetId()) }, name)
	}
}

// fields renders a shape's fields as the record's tables write them: one
// line per field in column order with its presence, nullability and
// redaction, then the forbidden columns and relations with their reasons,
// then the bound columns.
func fields(s *irv1.Shape) string {
	var b strings.Builder
	for _, f := range s.GetFields() {
		fmt.Fprintf(&b, "%s %s", f.GetColumn(), strings.TrimPrefix(f.GetPresence().String(), "PRESENCE_"))
		if f.GetNullable() {
			b.WriteString(" nullable")
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
		fmt.Fprintf(&b, "-%s%s %s\n", f.GetColumn(), f.GetRelation(), strings.TrimPrefix(f.GetReason().String(), "FORBIDDEN_"))
	}
	for _, c := range s.GetBind() {
		fmt.Fprintf(&b, "bind %s\n", c)
	}
	return b.String()
}

// nests renders a shape's relation fields: presence and nullability, the
// shape or union the field takes, the columns it supplies and its policy.
func nests(b *irv1.Bundle, s *irv1.Shape) string {
	var out strings.Builder
	for _, n := range s.GetNests() {
		fmt.Fprintf(&out, "%s %s", n.GetRelation(), strings.TrimPrefix(n.GetPresence().String(), "PRESENCE_"))
		if n.GetNullable() {
			out.WriteString(" nullable")
		}
		if n.GetShape() != nil {
			out.WriteString(" " + spell(b, n.GetShape()))
		} else {
			out.WriteString(" " + unionWords(b, n.GetUnion()))
		}
		if len(n.GetSupplies()) > 0 {
			out.WriteString(" supplies(" + strings.Join(n.GetSupplies(), ",") + ")")
		}
		if n.GetNeedsOne() {
			out.WriteString(" needs-one")
		}
		if p := n.GetPolicy(); p != nil {
			fmt.Fprintf(&out, " %s", strings.ToLower(strings.TrimPrefix(p.GetCollection().String(), "COLLECTION_POLICY_")))
			if p.GetRemoval() != irv1.Removal_REMOVAL_UNSPECIFIED {
				fmt.Fprintf(&out, "+%s", strings.ToLower(strings.TrimPrefix(p.GetRemoval().String(), "REMOVAL_")))
			}
		}
		out.WriteString("\n")
	}
	for _, f := range s.GetForbidden() {
		if f.GetRelation() != "" {
			fmt.Fprintf(&out, "-%s %s\n", f.GetRelation(), strings.TrimPrefix(f.GetReason().String(), "FORBIDDEN_"))
		}
	}
	return out.String()
}

// unionWords is the host name of the union id names, joined by
// underscores, as the Go examples spell it in camel case.
func unionWords(b *irv1.Bundle, id *irv1.RelationUnionId) string {
	for _, u := range b.GetShapes().GetUnions() {
		if u.GetId().String() == id.String() {
			return strings.Join(u.GetHostName().GetWords(), "_")
		}
	}
	return "?"
}

// variants lists the changes of the union named by its host words.
func variants(t *testing.T, b *irv1.Bundle, words string) string {
	t.Helper()
	u := named(t, b.GetShapes().GetUnions(), func(u *irv1.RelationUnion) string { return strings.Join(u.GetHostName().GetWords(), "_") }, words)
	var out []string
	for _, v := range u.GetVariants() {
		s := strings.ToLower(strings.TrimPrefix(v.GetChange().String(), "RELATION_CHANGE_"))
		if v.GetKey() != nil {
			s += " key " + spell(b, v.GetKey())
		}
		if v.GetPayload() != nil {
			s += " " + spell(b, v.GetPayload())
		}
		out = append(out, s)
	}
	return strings.Join(out, "; ")
}

func assertText(t *testing.T, what, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s:\n got:\n%s\nwant:\n%s", what, got, want)
	}
}

// TestGateUsersExpansion: the presets of app.users match §14.3 exactly, and
// §7.5 step 4 and §14.10 follow from them.
func TestGateUsersExpansion(t *testing.T) {
	_, shape := gateShapes(t)
	assertText(t, "§14.3 Full", fields(shape("user.full.flat")), `id REQUIRED
tenant_id REQUIRED
email REQUIRED
display_name REQUIRED nullable
created_at REQUIRED
updated_at REQUIRED
version REQUIRED
-password_hash WRITE_ONLY
`)
	assertText(t, "§14.3 Draft", fields(shape("user.draft.flat")), `email REQUIRED
display_name OPTIONAL nullable
password_hash REQUIRED redact
created_at OPTIONAL
updated_at OPTIONAL
version OPTIONAL
-id IDENTITY_ALWAYS
-tenant_id BOUND
`)
	assertText(t, "§14.3 Partial", fields(shape("user.partial.flat")), `email OPTIONAL
display_name OPTIONAL nullable
password_hash OPTIONAL redact
-id KEY
-tenant_id BOUND
-created_at IMMUTABLE
-updated_at DB_UPDATED
-version VERSION
`)
	assertText(t, "§14.3 Key", fields(shape("user.key")), "id REQUIRED\nversion REQUIRED EXPECTED_VERSION\n")
	assertText(t, "§14.3 Scope", fields(shape("user.scope")), "tenant_id REQUIRED\n")

	assertContains(t, "§14.10", shape("user.partial.flat"), `{
	  "id": {
	    "entity": { "catalog": "shop", "namespace": "app", "object": "users" },
	    "preset": "PRESET_PARTIAL",
	    "form": "FORM_FLAT"
	  },
	  "hostName": { "words": ["user", "partial"] },
	  "lineage": [{ "op": "PRESET", "preset": "PRESET_PARTIAL" }],
	  "fields": [
	    { "column": "email", "presence": "PRESENCE_OPTIONAL", "nullable": false,
	      "valueKind": "VALUE_KIND_VALUE_OR_EXPRESSION",
	      "type": { "named": { "domain": { "catalog": "shop", "namespace": "app", "object": "email_address" } } },
	      "validation": ["email_address_check"] },
	    { "column": "display_name", "presence": "PRESENCE_OPTIONAL", "nullable": true,
	      "valueKind": "VALUE_KIND_VALUE_OR_EXPRESSION",
	      "type": { "varchar": { "length": 80, "unit": "LENGTH_UNIT_CHARACTERS" } } },
	    { "column": "password_hash", "presence": "PRESENCE_OPTIONAL", "nullable": false,
	      "valueKind": "VALUE_KIND_VALUE_OR_EXPRESSION",
	      "type": { "text": {} }, "redact": true }
	  ],
	  "forbidden": [
	    { "column": "id", "reason": "FORBIDDEN_KEY" },
	    { "column": "tenant_id", "reason": "FORBIDDEN_BOUND" },
	    { "column": "created_at", "reason": "FORBIDDEN_IMMUTABLE" },
	    { "column": "updated_at", "reason": "FORBIDDEN_DB_UPDATED" },
	    { "column": "version", "reason": "FORBIDDEN_VERSION" }
	  ],
	  "scope": ["tenant_id"]
	}`)
}

// TestGateCustomShapes: the custom shapes of §14.8 expand as its Go types
// show, and quick_invite without bind fails with the documented diagnostic.
func TestGateCustomShapes(t *testing.T) {
	b, shape := gateShapes(t)
	// type UserSummary struct { ID int64; Email EmailAddress; DisplayName *string }
	assertText(t, "user_summary", fields(shape("user_summary")), `id REQUIRED
email REQUIRED
display_name REQUIRED nullable
-tenant_id OMITTED
-password_hash WRITE_ONLY
-created_at OMITTED
-updated_at OMITTED
-version OMITTED
`)
	// DisplayName string // narrowed; enforced when decoding
	narrowed := shape("user_summary_named")
	assertText(t, "user_summary_named", fields(narrowed), strings.Replace(fields(shape("user_summary")), "display_name REQUIRED nullable", "display_name REQUIRED", 1))
	if f := narrowed.GetFields()[2]; f.GetNullabilitySource() != irv1.NullabilitySource_NULLABILITY_SOURCE_SHAPE_OPERATOR {
		t.Errorf("user_summary_named: display_name's narrowing is not recorded: %v", f)
	}
	// type PostWithTags struct { PostFull; Tags []TagFull; Author UserSummary }
	withTags := shape("post_with_tags")
	assertText(t, "post_with_tags fields", fields(withTags), fields(shape("post.full.flat")))
	assertText(t, "post_with_tags nests", nests(b, withTags), "author REQUIRED user_summary\ntags REQUIRED tag.full.flat\n")
	// type Signup struct { Email EmailAddress; PasswordHash string }
	assertText(t, "signup", fields(shape("signup")), `email REQUIRED
password_hash REQUIRED redact
-id IDENTITY_ALWAYS
-tenant_id BOUND
-display_name OMITTED
-created_at OMITTED
-updated_at OMITTED
-version OMITTED
`)
	// type QuickInvite struct { Email EmailAddress // password_hash is bound }
	assertText(t, "quick_invite", fields(shape("quick_invite")), `email REQUIRED
-id IDENTITY_ALWAYS
-tenant_id BOUND
-display_name OMITTED
-created_at OMITTED
-updated_at OMITTED
-version OMITTED
bind password_hash
`)

	// Without bind, quick_invite fails with the diagnostic of §14.8.
	path := shopConfig(t, strings.Replace(recordShapes, "    bind: [password_hash]\n", "", 1))
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	_, ds, err := shapes.Layer(b.GetCatalogs(), c.CustomShapes())
	if err != nil || len(ds) != 1 {
		t.Fatalf("%v %v", err, ds)
	}
	assertContains(t, "§14.8 diagnostic", ds[0], `{
	  "severity": "SEVERITY_ERROR",
	  "code": "write-shape-incomplete",
	  "shape": "quick_invite",
	  "message": "shape derived from user.draft drops required field password_hash; keep it or bind it"
	}`)
	if code, _, stderr := dql("generate", "-config", path); code != 1 ||
		!strings.Contains(stderr, "error write-shape-incomplete: shape derived from user.draft drops required field password_hash; keep it or bind it\n") {
		t.Errorf("dql generate: %d %q", code, stderr)
	}
}

// TestGateUserConversions: the user conversion table of §14.9, and the
// conversions §14.10 lists.
func TestGateUserConversions(t *testing.T) {
	b, _ := gateShapes(t)
	table := map[string]*irv1.Conversion{}
	for _, c := range b.GetShapes().GetConversions() {
		table[spell(b, c.GetFrom())+" -> "+spell(b, c.GetTo())] = c
	}
	kind := func(pair string) string {
		t.Helper()
		c := table[pair]
		if c == nil {
			t.Errorf("no conversion %s", pair)
			return ""
		}
		s := strings.TrimPrefix(c.GetKind().String(), "CONVERSION_")
		for _, in := range c.GetInputs() {
			s += " " + in.GetColumn() + " " + strings.TrimPrefix(in.GetReason().String(), "INPUT_")
		}
		return s
	}
	for pair, want := range map[string]string{
		"user.full.flat -> user.key":           "TOTAL",
		"user.full.flat -> user.partial.flat":  "TOTAL",
		"user.full.flat -> user.draft.flat":    "NEEDS_INPUTS password_hash WRITE_ONLY",
		"user.draft.flat -> user.partial.flat": "TOTAL",
		"user.partial.flat -> user.full.flat":  "WITH_BASE",
		"user.partial.flat -> user.draft.flat": "WITH_BASE",
		"user.draft.flat -> user.full.flat":    "VIA_DATABASE",
		"user.full.flat -> user_summary":       "TOTAL",
		"user_summary -> user.full.flat":       "IMPOSSIBLE",
		"user.full.nested -> user.full.flat":   "TOTAL",
		"user.full.nested -> user.full.nests":  "TOTAL",
	} {
		if got := kind(pair); got != want {
			t.Errorf("§14.9 %s: %s, want %s", pair, got, want)
		}
	}
	if got := table["user.full.flat -> user_summary"].GetOrigin(); got != irv1.ConversionOrigin_CONVERSION_ORIGIN_LINEAGE {
		t.Errorf("§14.9 Full to user_summary: origin %v", got)
	}
}

// TestGateNestedWrites: the nested Drafts and Partials of user and post
// match §14.5 and §14.6, the relation consistency rule included, and the
// Nests of §12.5.
func TestGateNestedWrites(t *testing.T) {
	b, shape := gateShapes(t)

	// §12.5: UserFullNests and PostFullNests.
	assertText(t, "§12.5 user", nests(b, shape("user.full.nests")), `posts_to_author REQUIRED post.full.flat
posts_to_editor REQUIRED post.full.flat
profile REQUIRED nullable profile.full.flat
tenant REQUIRED tenant.full.flat
`)
	assertText(t, "§12.5 post", nests(b, shape("post.full.nests")), `author REQUIRED user.full.flat
editor REQUIRED nullable user.full.flat
post_tags REQUIRED post_tag.full.flat
tags REQUIRED tag.full.flat
`)

	// §14.5: PostDraftNested. FK columns of owning relations are optional
	// in Nested and keep their class in Flat; Author and Editor are
	// UserRefs that supply them, and a NOT NULL FK needs one of the two
	// (the consistency rule's data).
	nested := fields(shape("post.draft.nested"))
	assertText(t, "§14.5 post draft nested fields", nested[:strings.Index(nested, "title")], `id OPTIONAL
author_id OPTIONAL
editor_id OPTIONAL nullable
`)
	if flat := fields(shape("post.draft.flat")); !strings.Contains(flat, "author_id REQUIRED\neditor_id OPTIONAL nullable\n") {
		t.Errorf("§14.5 post draft flat:\n%s", flat)
	}
	assertText(t, "§14.5 post draft nested", nests(b, shape("post.draft.nested")), `author OPTIONAL user_ref supplies(author_id) needs-one
editor OPTIONAL user_ref supplies(editor_id)
post_tags OPTIONAL post_tag.draft.flat.for.post
tags OPTIONAL tag_link
`)
	assertText(t, "§14.5 UserRef", variants(t, b, "user_ref"), "link key user.key; create user.draft.flat")
	assertText(t, "§14.5 TagLink", variants(t, b, "tag_link"), "link key tag.key; create tag.draft.flat")

	// §14.5: UserDraftNests. tenant is left out because tenant_id is bound;
	// the children are named after the forward relation and forbid the FK
	// to their parent.
	assertText(t, "§14.5 user draft nests", nests(b, shape("user.draft.nests")), `posts_to_author OPTIONAL post.draft.flat.for.author
posts_to_editor OPTIONAL post.draft.flat.for.editor
profile OPTIONAL profile.draft.flat.for.user
-tenant BOUND
`)
	if f := fields(shape("post.draft.flat.for.author")); !strings.Contains(f, "-author_id PARENT\n") || strings.Contains(f, "author_id OPTIONAL") {
		t.Errorf("§14.5 PostDraftForAuthor:\n%s", f)
	}

	// §14.6: UserPartialNests, with posts_to_author configured replace +
	// delete (§1.3) and the defaults elsewhere.
	assertText(t, "§14.6 user partial nests", nests(b, shape("user.partial.nests")), `posts_to_author OPTIONAL post_change_for_author replace+delete
posts_to_editor OPTIONAL post_change_for_editor merge+unlink
profile OPTIONAL profile_change_for_user merge+delete
-tenant BOUND
`)
	assertText(t, "§14.6 PostChangeForAuthor", variants(t, b, "post_change_for_author"),
		"create post.draft.flat.for.author; update key post.key post.partial.flat.for.author; delete key post.key")
	if f := fields(shape("post.partial.flat.for.author")); !strings.Contains(f, "title OPTIONAL\n") || !strings.Contains(f, "-author_id PARENT\n") {
		t.Errorf("§14.6 PostPartialForAuthor:\n%s", f)
	}

	// §14.6: PostPartialNests. Owning relations take Link, Create and
	// Update, and Unlink only with a nullable FK; many-to-many takes Link,
	// Unlink and Create.
	assertText(t, "§14.6 post partial nests", nests(b, shape("post.partial.nests")), `author OPTIONAL post_author_change
editor OPTIONAL post_editor_change
post_tags OPTIONAL post_tag_change_for_post merge+delete
tags OPTIONAL post_tags_change merge
`)
	assertText(t, "§14.6 PostAuthorChange", variants(t, b, "post_author_change"),
		"link key user.key; create user.draft.flat; update user.partial.flat")
	assertText(t, "§14.6 PostEditorChange", variants(t, b, "post_editor_change"),
		"link key user.key; create user.draft.flat; update user.partial.flat; unlink")
	assertText(t, "§14.6 PostTagsChange", variants(t, b, "post_tags_change"),
		"link key tag.key; create tag.draft.flat; unlink key tag.key")
}

// TestGateUpsert: the upsert of users on users_tenant_email_key (§14.7).
func TestGateUpsert(t *testing.T) {
	b, _ := gateShapes(t)
	up := named(t, b.GetShapes().GetUpserts(), func(u *irv1.Upsert) string {
		return u.GetEntity().GetObject() + " " + u.GetConflictTarget()
	}, "users users_tenant_email_key")
	if got := spell(b, up.GetInsertShape()); got != "user.draft.flat" {
		t.Errorf("§14.7 insert shape %s", got)
	}
	assertContains(t, "§14.7", up, `{
	  "entity": { "catalog": "shop", "namespace": "app", "object": "users" },
	  "conflictTarget": "users_tenant_email_key",
	  "defaultUpdateExcludes": [
	    { "column": "tenant_id", "reason": "EXCLUDE_BOUND" },
	    { "column": "email", "reason": "EXCLUDE_CONFLICT_KEY" },
	    { "column": "created_at", "reason": "EXCLUDE_IMMUTABLE" },
	    { "column": "updated_at", "reason": "EXCLUDE_DB_UPDATED" },
	    { "column": "version", "reason": "EXCLUDE_VERSION" }
	  ]
	}`)
}
