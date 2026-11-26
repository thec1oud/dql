package gen

import (
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// userPreset is the Flat preset p of users in b.
func userPreset(b *irv1.Bundle, p irv1.Preset) *irv1.Shape {
	for _, s := range b.GetShapes().GetShapes() {
		id := s.GetId()
		if id.GetEntity().GetObject() == "users" && id.GetPreset() == p && id.GetForm() == irv1.Form_FORM_FLAT && id.GetForRelation() == "" {
			return s
		}
	}
	return nil
}

// customShape adds a custom shape of from named name, holding only column
// keep and binding bind.
func customShape(b *irv1.Bundle, from *irv1.Shape, name, keep string, bind ...string) {
	s := proto.Clone(from).(*irv1.Shape)
	s.Id = &irv1.ShapeId{Base: &irv1.ShapeId_Custom{Custom: name}}
	s.HostName = &irv1.Identifier{Stored: name, Words: strings.Split(name, "_")}
	s.Fields = slices.DeleteFunc(s.Fields, func(f *irv1.Field) bool { return f.GetColumn() != keep })
	s.Bind = bind
	b.Shapes.Shapes = append(b.Shapes.Shapes, s)
}

// customQuery adds the query of a custom shape, planned as from's with
// the parameters keep only (§16.3); nullable makes email nullable.
func customQuery(t *testing.T, b *irv1.Bundle, from, name string, nullable bool, keep ...string) {
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

// §14.8: a custom write shape inserts through insert<Shape>, its bound
// fields as parameters after the payload, and one derived from the Partial
// patches through patch<Shape>. Neither gets insertMany.
func TestCustomShapeWrites(t *testing.T) {
	b := shopShapes(t)
	// The running example has quick_invite and signup.
	customShape(b, userPreset(b, irv1.Preset_PRESET_PARTIAL), "rename", "display_name")
	customQuery(t, b, "user.insert", "user.insert.quick_invite", false, "email", "password_hash")
	customQuery(t, b, "user.insert", "user.insert.signup", false, "email")
	customQuery(t, b, "user.patch", "user.patch.rename", false, "display_name", "key.id", "key.version")
	// An unknown shape, and a custom shape on an operation that takes none.
	customQuery(t, b, "user.insert", "user.insert.nope", false, "email")
	customQuery(t, b, "user.find.by_primary", "user.find.signup", false, "key.id")
	files, codes := generate(t, b, Options{})
	if want := []string{"SEVERITY_WARNING ts-query-unsupported shop.app.users", "SEVERITY_WARNING ts-query-unsupported shop.app.users"}; !slices.Equal(codes, want) {
		t.Fatalf("codes %q", codes)
	}
	contains(t, files, "user.dql.ts",
		"  insertQuickInvite(d: QuickInvite, passwordHash: string, ...as: Assignment<UsersTable>[]): Promise<UserFull>;\n",
		"  insertSignup(d: Signup, ...as: Assignment<UsersTable>[]): Promise<UserFull>;\n",
		"  patchRename(k: UserKey, p: Rename, ...as: Update<UsersTable>[]): Promise<UserFull>;\n",
		"  insertQuickInvite(scope: UserScope, d: QuickInvite, passwordHash: string, ...as: Assignment<UsersTable>[]): Promise<UserFull>;\n",
	)
	contains(t, files, "dql-stores.dql.ts",
		"    throwIfIssues(validateQuickInvite(d));\n    const args: Args = { \"email\": d.email, \"password_hash\": passwordHash, \"scope.tenant_id\": this.scope.tenantId };\n",
		"    assign(userInsertQuickInvite, \"quick_invite\", args, as);\n",
		"    if (p.displayName !== undefined) {\n      args[\"display_name\"] = p.displayName;\n    }\n    assign(userPatchRename, \"rename\", args, as);\n",
		"    return versionedOne(this.w, statementOf(userPatchRename, fragments, args), found, decodeUserFull);\n",
		"    return new UsersWrites(this.w, scope).insertQuickInvite(d, passwordHash, ...as);\n",
	)
	for _, absent := range []string{"insertManyQuickInvite", "findSignup"} {
		if strings.Contains(files["user.dql.ts"], absent) {
			t.Errorf("user.dql.ts has %s", absent)
		}
	}
	typecheck(t, files)
}

// §10.5, §14.8: a bound field is checked against its column's
// client-validated constraints, as the shape's own fields are, before
// anything is sent; a nullable one when it is set. A Partial-derived shape
// takes its bound fields after the payload too.
func TestBoundFieldChecks(t *testing.T) {
	b := shopShapes(t)
	draft, partial := userPreset(b, irv1.Preset_PRESET_DRAFT), userPreset(b, irv1.Preset_PRESET_PARTIAL)
	customShape(b, draft, "email_invite", "password_hash", "email")
	customShape(b, draft, "maybe_invite", "password_hash", "email")
	customShape(b, partial, "rename_mail", "display_name", "email")
	customQuery(t, b, "user.insert", "user.insert.email_invite", false, "email", "password_hash")
	customQuery(t, b, "user.insert", "user.insert.maybe_invite", true, "email", "password_hash")
	customQuery(t, b, "user.patch", "user.patch.rename_mail", false, "display_name", "email", "key.id", "key.version")
	files, codes := generate(t, b, Options{})
	if len(codes) != 0 {
		t.Fatalf("codes %q", codes)
	}
	contains(t, files, "dql-stores.dql.ts",
		"  async insertEmailInvite(d: EmailInvite, email: EmailAddress, ...as: Assignment<UsersTable>[]): Promise<UserFull> {\n"+
			"    const issues = validateEmailInvite(d);\n"+
			"    dqlEmailAddressCheck.check(issues, \"email_invite\", \"email\", email);\n"+
			"    throwIfIssues(issues);\n",
		"  async insertMaybeInvite(d: MaybeInvite, email: EmailAddress | null, ...as: Assignment<UsersTable>[]): Promise<UserFull> {\n"+
			"    const issues = validateMaybeInvite(d);\n"+
			"    if (email !== null) {\n      dqlEmailAddressCheck.check(issues, \"maybe_invite\", \"email\", email);\n    }\n",
		"  async patchRenameMail(k: UserKey, p: RenameMail, email: EmailAddress, ...as: Update<UsersTable>[]): Promise<UserFull> {\n"+
			"    const issues = validateRenameMail(p);\n",
		"    return new UsersWrites(this.w, scope).patchRenameMail(k, p, email, ...as);\n",
		"    const args: Args = { \"email\": email, \"key.id\": k.id, \"key.version\": k.version, \"scope.tenant_id\": this.scope.tenantId };\n",
	)
	typecheck(t, files)

	// A bound field is always written, so a patch of an unversioned entity
	// with one does not read the row back in its place.
	b = shopShapes(t)
	for _, s := range b.GetShapes().GetShapes() {
		if s.GetId().GetEntity().GetObject() == "profiles" && s.GetId().GetPreset() == irv1.Preset_PRESET_PARTIAL && s.GetId().GetForm() == irv1.Form_FORM_FLAT && s.GetId().GetForRelation() == "" {
			customShape(b, s, "set_bio", "birthday", "bio")
		}
	}
	customQuery(t, b, "profile.patch", "profile.patch.set_bio", false, "bio", "birthday", "key.user_id")
	files, codes = generate(t, b, Options{})
	if len(codes) != 0 {
		t.Fatalf("codes %q", codes)
	}
	stores := files["dql-stores.dql.ts"]
	m := stores[strings.Index(stores, "async patchSetBio("):]
	if m = m[:strings.Index(m, "\n  }\n")]; strings.Contains(m, "this.get(k)") || !strings.Contains(m, "\"bio\": bio") {
		t.Error(m)
	}
	typecheck(t, files)
}
