package gen

import (
	"strings"
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// softDelete marks column col of table name soft-delete (§21.3).
func softDelete(t *testing.T, b *irv1.Bundle, name, col string) *irv1.Column {
	t.Helper()
	for _, c := range table(t, b, name).GetColumns() {
		if c.GetName().GetStored() == col {
			c.Annotations = append(c.Annotations, &irv1.Annotation{Key: "core.soft-delete"})
			return c
		}
	}
	t.Fatalf("no column %s.%s", name, col)
	return nil
}

// §21.3: reads leave out soft-deleted rows, through the store and through
// relations, unless they ask for them.
func TestSoftDeleteReads(t *testing.T) {
	files, codes := generate(t, shopShapes(t), Options{})
	if len(codes) != 0 {
		t.Fatal(codes)
	}
	for name, f := range files {
		if strings.Contains(f, "WithDeleted") || strings.Contains(f, "visible()") {
			t.Errorf("%s reads soft deletes without a soft-delete table", name)
		}
	}

	b := shopShapes(t)
	softDelete(t, b, "posts", "published_at")
	softDelete(t, b, "users", "updated_at")
	files, codes = generate(t, b, Options{})
	if len(codes) != 0 {
		t.Fatal(codes)
	}
	contains(t, files, "post.dql.go",
		"func (PostsTable) visible() dql.Pred[PostsTable] {\n\treturn dql.Compare[PostsTable](\"op.is_null\", \"col.posts.published_at\")\n}",
		"func (s postsReads) from(PostsTable) dql.From {\n\treturn dql.From{H: s.h, Visible: PostsTable{}.visible().Node()}\n}",
	)
	contains(t, files, "user.dql.go",
		"}, Visible: UsersTable{}.visible().Node()}",
		"func (u usersUnscopedReads) from(UsersTable) dql.From {\n\treturn dql.From{H: u.h, Visible: UsersTable{}.visible().Node()}\n}",
		"// Soft-deleted related rows are left out (§21.3).",
		`return dql.Related[UsersTable]("rel.users.posts_to_author", append([]dql.Pred[PostsTable]{PostsTable{}.visible()}, ps...)...)`,
		`return dql.Not(dql.Related[UsersTable]("rel.users.posts_to_author", PostsTable{}.visible(), dql.Not(p)))`,
	)
	contains(t, files, "dql-select.dql.go",
		"func (b Builder[T, E]) WithDeleted() Builder[T, E] { return Builder[T, E]{b.b.WithDeleted()} }",
		"func (b Builder[T, E]) OnlyDeleted() Builder[T, E] { return Builder[T, E]{b.b.OnlyDeleted()} }",
	)

	// A boolean column is deleted when true.
	b = shopShapes(t)
	softDelete(t, b, "posts", "published_at").Type = &irv1.Type{Kind: &irv1.Type_Bool{Bool: &irv1.Empty{}}}
	files, _ = generate(t, b, Options{})
	contains(t, files, "post.dql.go",
		`return dql.Compare[PostsTable]("op.is_distinct_from", "col.posts.published_at", dql.Use{Fragment: "value.true"})`)
}
