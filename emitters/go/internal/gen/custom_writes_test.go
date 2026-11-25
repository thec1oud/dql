package gen

import (
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// §14.8: a custom write shape inserts through Insert<Shape>, its bound
// fields as parameters after the payload, and one derived from the Partial
// patches through Patch<Shape>. Neither gets InsertMany or a nested form.
func TestCustomShapeWrites(t *testing.T) {
	b := shopShapes(t)
	var partial *irv1.Shape
	for _, s := range b.GetShapes().GetShapes() {
		id := s.GetId()
		if id.GetEntity().GetObject() == "users" && id.GetPreset() == irv1.Preset_PRESET_PARTIAL && id.GetForm() == irv1.Form_FORM_FLAT && id.GetForRelation() == "" {
			partial = s
		}
	}
	rename := proto.Clone(partial).(*irv1.Shape)
	rename.Id = &irv1.ShapeId{Base: &irv1.ShapeId_Custom{Custom: "rename"}}
	rename.HostName = &irv1.Identifier{Stored: "rename", Words: []string{"rename"}}
	rename.Fields = slices.DeleteFunc(rename.Fields, func(f *irv1.Field) bool { return f.GetColumn() != "display_name" })
	b.Shapes.Shapes = append(b.Shapes.Shapes, rename)

	// The core plans a custom shape's insert and patch as the preset's,
	// with the shape's parameters only (§16.3).
	custom := func(from, name string, keep ...string) {
		q := proto.Clone(query(t, b, from)).(*irv1.Query)
		q.Name = &irv1.Identifier{Stored: name}
		q.Params = slices.DeleteFunc(q.Params, func(p *irv1.Param) bool { return !slices.Contains(keep, p.GetName()) })
		b.Queries.Queries = append(b.Queries.Queries, q)
	}
	custom("user.insert", "user.insert.quick_invite", "email", "password_hash")
	custom("user.insert", "user.insert.signup", "email", "password_hash")
	custom("user.patch", "user.patch.rename", "display_name", "key.id", "key.version")
	// An unknown shape, and a custom shape on an operation that takes none.
	custom("user.insert", "user.insert.nope", "email")
	custom("user.find.by_primary", "user.find.signup", "key.id")
	files, codes := generate(t, b, Options{})
	if want := []string{"SEVERITY_WARNING go-query-unsupported shop.app.users", "SEVERITY_WARNING go-query-unsupported shop.app.users"}; !slices.Equal(codes, want) {
		t.Fatalf("codes %q", codes)
	}
	contains(t, files, "user.dql.go",
		"InsertQuickInvite(ctx context.Context, d QuickInvite, passwordHash string, as ...dql.Assignment[UsersTable]) (UserFull, error)\n",
		"InsertSignup(ctx context.Context, d Signup, as ...dql.Assignment[UsersTable]) (UserFull, error)\n",
		"PatchRename(ctx context.Context, k UserKey, p Rename, as ...dql.Update[UsersTable]) (UserFull, error)\n",
		"InsertQuickInvite(ctx context.Context, scope UserScope, d QuickInvite, passwordHash string, as ...dql.Assignment[UsersTable]) (UserFull, error)\n",
		"if err := d.Validate(); err != nil {\n\t\treturn UserFull{}, err\n\t}\n\targs := dql.Args{\"email\": d.Email, \"password_hash\": passwordHash, \"scope.tenant_id\": s.scope.TenantID}\n",
		"if err := dql.Assign(userInsertQuickInvite, \"quick_invite\", args, dql.Values(as)); err != nil {",
		"if v, ok := p.DisplayName.Get(); ok {\n\t\targs[\"display_name\"] = v\n\t}\n\tif err := dql.Assign(userPatchRename, \"rename\", args, as); err != nil {",
		"return dql.VersionedOne(ctx, s.w, st, exists, DecodeUserFull)",
		"return usersWrites{usersReads{u.w, scope}, u.w}.InsertQuickInvite(ctx, d, passwordHash, as...)",
	)
	for _, absent := range []string{"InsertManyQuickInvite", "InsertNestedQuickInvite", "PatchNestedRename", "FindSignup"} {
		if strings.Contains(files["user.dql.go"], absent) {
			t.Errorf("user.dql.go has %s", absent)
		}
	}
}

// §10.5, §14.8: a bound field is checked against its column's
// client-validated constraints, as the shape's own fields are, before
// anything is sent; a nullable one when it is set. A Partial-derived shape
// takes its bound fields after the payload too.
func TestBoundFieldChecks(t *testing.T) {
	b := shopShapes(t)
	var draft, partial *irv1.Shape
	for _, s := range b.GetShapes().GetShapes() {
		id := s.GetId()
		if id.GetEntity().GetObject() != "users" || id.GetForm() != irv1.Form_FORM_FLAT || id.GetForRelation() != "" {
			continue
		}
		switch id.GetPreset() {
		case irv1.Preset_PRESET_DRAFT:
			draft = s
		case irv1.Preset_PRESET_PARTIAL:
			partial = s
		}
	}
	shape := func(from *irv1.Shape, name, keep, bind string) {
		s := proto.Clone(from).(*irv1.Shape)
		s.Id = &irv1.ShapeId{Base: &irv1.ShapeId_Custom{Custom: name}}
		s.HostName = &irv1.Identifier{Stored: name, Words: strings.Split(name, "_")}
		s.Fields = slices.DeleteFunc(s.Fields, func(f *irv1.Field) bool { return f.GetColumn() != keep })
		s.Bind = []string{bind}
		b.Shapes.Shapes = append(b.Shapes.Shapes, s)
	}
	shape(draft, "email_invite", "password_hash", "email")
	shape(draft, "maybe_invite", "password_hash", "email")
	shape(partial, "rename_mail", "display_name", "email")
	custom := func(from, name string, nullable bool, keep ...string) {
		q := proto.Clone(query(t, b, from)).(*irv1.Query)
		q.Name = &irv1.Identifier{Stored: name}
		q.Params = slices.DeleteFunc(q.Params, func(p *irv1.Param) bool { return !slices.Contains(keep, p.GetName()) })
		for _, p := range q.Params {
			if p.GetName() == "email" {
				p.Nullable = nullable
			}
		}
		b.Queries.Queries = append(b.Queries.Queries, q)
	}
	custom("user.insert", "user.insert.email_invite", false, "email", "password_hash")
	custom("user.insert", "user.insert.maybe_invite", true, "email", "password_hash")
	custom("user.patch", "user.patch.rename_mail", false, "display_name", "email", "key.id", "key.version")
	files, codes := generate(t, b, Options{})
	if len(codes) != 0 {
		t.Fatalf("codes %q", codes)
	}
	contains(t, files, "user.dql.go",
		"InsertEmailInvite(ctx context.Context, d EmailInvite, email EmailAddress, as ...dql.Assignment[UsersTable]) (UserFull, error) {\n"+
			"\terrs := d.validate()\n"+
			"\tdqlEmailAddressCheck.Check(&errs, \"email_invite\", \"email\", string(email))\n"+
			"\tif err := errs.Err(); err != nil {\n\t\treturn UserFull{}, err\n\t}\n",
		"InsertMaybeInvite(ctx context.Context, d MaybeInvite, email *EmailAddress, as ...dql.Assignment[UsersTable]) (UserFull, error) {\n"+
			"\terrs := d.validate()\n"+
			"\tif email != nil {\n\t\tdqlEmailAddressCheck.Check(&errs, \"maybe_invite\", \"email\", string(*email))\n\t}\n",
		"PatchRenameMail(ctx context.Context, k UserKey, p RenameMail, email EmailAddress, as ...dql.Update[UsersTable]) (UserFull, error) {\n"+
			"\terrs := p.validate()\n"+
			"\tdqlEmailAddressCheck.Check(&errs, \"rename_mail\", \"email\", string(email))\n",
		"return usersWrites{usersReads{u.w, scope}, u.w}.PatchRenameMail(ctx, k, p, email, as...)",
		"args := dql.Args{\"email\": email, \"key.id\": k.ID, \"key.version\": k.Version, \"scope.tenant_id\": s.scope.TenantID}",
	)
}
