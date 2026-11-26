package gen

import (
	"strings"
	"testing"
)

// §20.1, §20.2, §14.7: bulk and filtered writes, upsertSet and the column
// handles' assignments.
func TestWrites(t *testing.T) {
	files, codes := generate(t, shopShapes(t), Options{})
	if len(codes) != 0 {
		t.Fatal(codes)
	}
	contains(t, files, "user.dql.ts",
		"  insertMany(ds: readonly UserDraft[], ...as: Assignment<UsersTable>[]): Promise<UserFull[]>;\n",
		"  upsertSet(d: UserDraft, p: UserPartial, ...as: Update<UsersTable>[]): Promise<UserFull>;\n",
		"  updateWhere(where: Pred<UsersTable>, p: UserPartial, ...as: Update<UsersTable>[]): Promise<number>;\n",
		"  deleteWhere(scope: UserScope, where: Pred<UsersTable>): Promise<number>;\n",
		// A time column takes now; a Draft-only column has no arithmetic.
		"    set(e: Expr): Assignment<UsersTable> {\n      return set<UsersTable>(\"created_at\", e);\n",
		"    set(e: Expr<\"default\">): Assignment<UsersTable> {\n      return set<UsersTable>(\"email\", e);\n",
	)
	contains(t, files, "post.dql.ts",
		"    add(v: bigint): Update<PostsTable> {\n      return arith<PostsTable>(\"op.add\", \"author_id\", \"col.posts.author_id\", arg(v, false, \"pg_catalog.int8\"));\n",
		"    sub(v: bigint): Update<PostsTable> {",
	)
	contains(t, files, "dql-stores.dql.ts",
		// insertMany validates every draft before it writes any.
		"    for (const d of ds) {\n      throwIfIssues(validatePostDraft(d));\n    }\n    const rows: Args[] = [];\n    for (const d of ds) {\n      const args: Args = { \"author_id\": d.authorId, \"title\": d.title };\n      if (d.id !== undefined) {\n        args[\"id\"] = d.id;\n      }\n",
		"      assign(postInsert, \"post.draft.flat\", args, as);\n      rows.push(args);\n    }\n    return insertMany(this.w, fragments, postInsert, rows, decodePostFull);\n",
		// upsertSet's Partial fills the update side.
		"    throwIfIssues(validateUserDraft(d));\n    throwIfIssues(validateUserPartial(p));\n",
		"    if (p.email !== undefined) {\n      args[\"set.email\"] = p.email;\n    }\n",
		"    assign(userUpsertSetByPrimary, \"user.partial.flat\", args, as, \"set.\");\n",
		// An unversioned updateWhere with nothing set writes nothing.
		"    const q = filtered(tagUpdateWhere, where);\n",
		"    assign(q, \"tag.partial.flat\", args, as);\n    if (!set && as.length === 0) {\n      // Nothing to change.\n      return 0;\n    }\n    return execute(this.w, statementOf(q, fragments, args));\n",
		"    const q = filtered(userDeleteWhere, where);\n    return execute(this.w, statementOf(q, fragments, { \"scope.tenant_id\": this.scope.tenantId }));\n",
		"    return new UsersWrites(this.w, scope).insertMany(ds, ...as);\n",
		// An empty Partial with no assignments reads the row back.
		"    if (!set && as.length === 0) {\n      // Nothing to change: the row as it is.\n      return this.get(k);\n",
	)
	// A versioned updateWhere always writes: it bumps the version.
	stores := files["dql-stores.dql.ts"]
	users := stores[strings.Index(stores, "async updateWhere(where: Pred<UsersTable>"):]
	if strings.Contains(users[:strings.Index(users, "\n  }\n")], "Nothing to change") {
		t.Error("versioned updateWhere short-cut")
	}
	typecheck(t, files)
}
