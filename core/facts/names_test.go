package facts

import (
	"slices"
	"strings"
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Small schemas for the naming rules of §4.5 and the collisions of §4.3.
// Every rule has a case that passes and a case that fails.

func usersTable(extra ...*irv1.Column) *irv1.Table {
	u := table("users", []string{"users"}, append([]*irv1.Column{col("id", i64(), "id")}, extra...)...)
	u.Uniqueness = []*irv1.Uniqueness{pk("users_pkey", "id")}
	return u
}

// ref is a foreign key from cols to shop.app.target(refs).
func ref(name string, cols []string, target string, refs ...string) *irv1.ForeignKey {
	return fk(name, strings.Split(name, "_"), cols, "shop", "app", target, refs...)
}

// names lists every relation as owner.name, in output order.
func names(c *irv1.Catalog) []string {
	var out []string
	for _, ns := range c.GetNamespaces() {
		for _, tb := range ns.GetTables() {
			for _, r := range tb.GetRelations() {
				out = append(out, tb.GetName().GetStored()+"."+r.GetName().GetStored())
			}
		}
	}
	return out
}

type namingCase struct {
	name   string
	tables func() []*irv1.Table
	want   []string // relations
	diags  []string
}

func (tc namingCase) run(t *testing.T) {
	t.Helper()
	c := catalogOf(tc.tables()...)
	ds := diagLines(Derive([]*irv1.Catalog{c}, postgres))
	if got := names(c); !slices.Equal(got, tc.want) {
		t.Errorf("%s: relations %v, want %v", tc.name, got, tc.want)
	}
	if !slices.Equal(ds, tc.diags) {
		t.Errorf("%s: diagnostics %v, want %v", tc.name, ds, tc.diags)
	}
}

func TestRule1ToOne(t *testing.T) {
	for _, tc := range []namingCase{
		{"FK column without its id word", func() []*irv1.Table {
			p := table("posts", []string{"posts"}, col("id", i64(), "id"), col("author_id", i64(), "author", "id"))
			p.ForeignKeys = []*irv1.ForeignKey{ref("posts_author_id_fkey", []string{"author_id"}, "users", "id")}
			return []*irv1.Table{p, usersTable()}
		}, []string{"posts.author", "users.posts"}, nil},
		{"last word is not id: the referenced table's words", func() []*irv1.Table {
			p := table("posts", []string{"posts"}, col("id", i64(), "id"), col("owner", i64(), "owner"))
			p.ForeignKeys = []*irv1.ForeignKey{ref("posts_owner_fkey", []string{"owner"}, "users", "id")}
			return []*irv1.Table{p, usersTable()}
		}, []string{"posts.users", "users.posts"}, nil},
		{"a column named id alone: the referenced table's words", func() []*irv1.Table {
			p := table("accounts", []string{"accounts"}, col("id", i64(), "id"))
			p.ForeignKeys = []*irv1.ForeignKey{ref("accounts_id_fkey", []string{"id"}, "users", "id")}
			return []*irv1.Table{p, usersTable()}
		}, []string{"accounts.users", "users.accounts"}, nil},
		{"composite FK: the referenced table's words", func() []*irv1.Table {
			p := table("posts", []string{"posts"}, col("id", i64(), "id"), col("tenant_id", i64(), "tenant", "id"), col("user_id", i64(), "user", "id"))
			p.ForeignKeys = []*irv1.ForeignKey{ref("posts_user_fkey", []string{"tenant_id", "user_id"}, "users", "tenant_id", "id")}
			return []*irv1.Table{p, usersTable(col("tenant_id", i64(), "tenant", "id"))}
		}, []string{"posts.users", "users.posts"}, nil},
		{"fails when the name equals a column", func() []*irv1.Table {
			p := table("posts", []string{"posts"}, col("author", text(), "author"), col("author_id", i64(), "author", "id"))
			p.ForeignKeys = []*irv1.ForeignKey{ref("posts_author_id_fkey", []string{"author_id"}, "users", "id")}
			return []*irv1.Table{p, usersTable()}
		}, []string{"posts.author", "users.posts"}, []string{"ERROR relation-name-equals-column shop.app.posts.author"}},
	} {
		tc.run(t)
	}
}

func TestRule2InverseToMany(t *testing.T) {
	posts := func() *irv1.Table {
		p := table("posts", []string{"posts"}, col("id", i64(), "id"), col("author_id", i64(), "author", "id"))
		p.ForeignKeys = []*irv1.ForeignKey{ref("posts_author_id_fkey", []string{"author_id"}, "users", "id")}
		return p
	}
	for _, tc := range []namingCase{
		{"the referencing table's words", func() []*irv1.Table {
			return []*irv1.Table{posts(), usersTable()}
		}, []string{"posts.author", "users.posts"}, nil},
		{"fails when the name equals a column of the referenced entity", func() []*irv1.Table {
			return []*irv1.Table{posts(), usersTable(col("posts", i32(), "posts"))}
		}, []string{"posts.author", "users.posts"}, []string{"ERROR relation-name-equals-column shop.app.users.posts"}},
	} {
		tc.run(t)
	}
}

func TestRule3InverseToOne(t *testing.T) {
	profiles := func() *irv1.Table {
		p := table("profiles", []string{"profiles"}, col("user_id", i64(), "user", "id"))
		p.Uniqueness = []*irv1.Uniqueness{pk("profiles_pkey", "user_id")}
		p.ForeignKeys = []*irv1.ForeignKey{ref("profiles_user_id_fkey", []string{"user_id"}, "users", "id")}
		return p
	}
	for _, tc := range []namingCase{
		{"the referencing table's words", func() []*irv1.Table {
			return []*irv1.Table{profiles(), usersTable()}
		}, []string{"profiles.user", "users.profiles"}, nil},
		{"fails when the name equals a column", func() []*irv1.Table {
			return []*irv1.Table{profiles(), usersTable(col("profiles", text(), "profiles"))}
		}, []string{"profiles.user", "users.profiles"}, []string{"ERROR relation-name-equals-column shop.app.users.profiles"}},
	} {
		tc.run(t)
	}
	c := catalogOf(profiles(), usersTable())
	Derive([]*irv1.Catalog{c}, postgres)
	r := relationOf(t, c, "users", "profiles")
	if r.GetKind() != irv1.RelationKind_RELATION_KIND_INVERSE_TO_ONE || !r.GetOptional() {
		t.Errorf("inverse to-one: %v", r)
	}
}

func junctionTables(extra ...*irv1.Column) []*irv1.Table {
	posts := table("posts", []string{"posts"}, col("id", i64(), "id"))
	posts.Uniqueness = []*irv1.Uniqueness{pk("posts_pkey", "id")}
	tags := table("tags", []string{"tags"}, col("id", i64(), "id"))
	tags.Uniqueness = []*irv1.Uniqueness{pk("tags_pkey", "id")}
	pt := table("post_tags", []string{"post", "tags"}, append([]*irv1.Column{col("post_id", i64(), "post", "id"), col("tag_id", i64(), "tag", "id")}, extra...)...)
	pt.Uniqueness = []*irv1.Uniqueness{pk("post_tags_pkey", "post_id", "tag_id")}
	pt.ForeignKeys = []*irv1.ForeignKey{
		ref("post_tags_post_id_fkey", []string{"post_id"}, "posts", "id"),
		ref("post_tags_tag_id_fkey", []string{"tag_id"}, "tags", "id"),
	}
	return []*irv1.Table{posts, tags, pt}
}

func TestRule4ManyToMany(t *testing.T) {
	junction := []string{"post_tags.post", "post_tags.tag", "posts.post_tags", "tags.post_tags"}
	for _, tc := range []namingCase{
		{"the far table's words, both directions", func() []*irv1.Table { return junctionTables() },
			[]string{"post_tags.post", "post_tags.tag", "posts.post_tags", "posts.tags", "tags.post_tags", "tags.posts"}, nil},
		{"a junction with extra columns is an ordinary entity", func() []*irv1.Table {
			return junctionTables(col("role", text(), "role"))
		}, junction, nil},
		{"a view is never a junction", func() []*irv1.Table {
			ts := junctionTables()
			ts[2].Kind = irv1.TableKind_RELATION_KIND_VIEW
			return ts
		}, junction, nil},
		{"no primary key", func() []*irv1.Table {
			ts := junctionTables()
			ts[2].Uniqueness = nil
			return ts
		}, junction, nil},
		{"primary key is not the foreign keys' columns", func() []*irv1.Table {
			ts := junctionTables()
			ts[2].Uniqueness = []*irv1.Uniqueness{pk("post_tags_pkey", "post_id")}
			return ts
		}, []string{"post_tags.post", "post_tags.tag", "posts.post_tags", "tags.post_tags"}, nil},
		{"overlapping foreign keys", func() []*irv1.Table {
			ts := junctionTables()
			ts[2].ForeignKeys[1] = ref("post_tags_tag_id_fkey", []string{"post_id"}, "tags", "id")
			return ts
		}, []string{"post_tags.post_to_post", "post_tags.post_to_post", "posts.post_tags", "tags.post_tags"},
			[]string{"ERROR relation-name-collision shop.app.post_tags.post_to_post"}},
		{"a third foreign key", func() []*irv1.Table {
			ts := junctionTables()
			ts[2].ForeignKeys = append(ts[2].ForeignKeys, ref("post_tags_x_fkey", []string{"tag_id"}, "tags", "id"))
			return ts
		}, []string{"post_tags.post", "post_tags.tag_to_tag", "post_tags.tag_to_tag", "posts.post_tags",
			"tags.post_tags_to_tag", "tags.post_tags_to_tag"},
			[]string{"ERROR relation-name-collision shop.app.post_tags.tag_to_tag", "ERROR relation-name-collision shop.app.tags.post_tags_to_tag"}},
		{"fails when the name equals a column", func() []*irv1.Table {
			ts := junctionTables()
			ts[0].Columns = append(ts[0].Columns, col("tags", text(), "tags"))
			return ts
		}, []string{"post_tags.post", "post_tags.tag", "posts.post_tags", "posts.tags", "tags.post_tags", "tags.posts"},
			[]string{"ERROR relation-name-equals-column shop.app.posts.tags"}},
	} {
		tc.run(t)
	}
	c := catalogOf(junctionTables()...)
	c.Namespaces[0].Tables[2].ForeignKeys[1].References.Table.Object = "missing"
	if ds := diagLines(Derive([]*irv1.Catalog{c}, postgres)); !slices.Equal(ds, []string{"ERROR relation-target-missing shop.app.post_tags.post_tags_tag_id_fkey"}) {
		t.Errorf("missing far table: %v", ds)
	}
}

func TestRule5EqualNames(t *testing.T) {
	posts := func(fks ...*irv1.ForeignKey) *irv1.Table {
		p := table("posts", []string{"posts"}, col("id", i64(), "id"), col("author_id", i64(), "author", "id"),
			col("editor_id?", i64(), "editor", "id"), col("reviewer_id?", i64(), "reviewer", "id"))
		p.ForeignKeys = append([]*irv1.ForeignKey{
			ref("posts_author_id_fkey", []string{"author_id"}, "users", "id"),
			ref("posts_editor_id_fkey", []string{"editor_id"}, "users", "id"),
		}, fks...)
		return p
	}
	for _, tc := range []namingCase{
		{"both get to and their FK column's words", func() []*irv1.Table {
			return []*irv1.Table{posts(), usersTable()}
		}, []string{"posts.author", "posts.editor", "users.posts_to_author", "users.posts_to_editor"}, nil},
		{"an annotated name is kept, the other renamed", func() []*irv1.Table {
			reviewer := ref("posts_reviewer_id_fkey", []string{"reviewer_id"}, "users", "id")
			reviewer.Annotations = []*irv1.Annotation{configured(keyInverseName, "x", wordsValue("posts"))}
			p := posts()
			p.ForeignKeys = []*irv1.ForeignKey{p.ForeignKeys[0], reviewer}
			return []*irv1.Table{p, usersTable()}
		}, []string{"posts.author", "posts.reviewer", "users.posts", "users.posts_to_author"}, nil},
		{"fails when names still collide", func() []*irv1.Table {
			reviewer := ref("posts_reviewer_id_fkey", []string{"reviewer_id"}, "users", "id")
			reviewer.Annotations = []*irv1.Annotation{configured(keyInverseName, "x", wordsValue("posts", "to", "author"))}
			return []*irv1.Table{posts(reviewer), usersTable()}
		}, []string{"posts.author", "posts.editor", "posts.reviewer", "users.posts_to_author", "users.posts_to_author", "users.posts_to_editor"},
			[]string{"ERROR relation-name-collision shop.app.users.posts_to_author"}},
	} {
		tc.run(t)
	}
}

func TestRule6RelationNamedLikeColumn(t *testing.T) {
	posts := func(anns ...*irv1.Annotation) *irv1.Table {
		p := table("posts", []string{"posts"}, col("id", i64(), "id"), col("author", text(), "author"), col("author_id", i64(), "author", "id"))
		f := ref("posts_author_id_fkey", []string{"author_id"}, "users", "id")
		f.Annotations = anns
		p.ForeignKeys = []*irv1.ForeignKey{f}
		return p
	}
	for _, tc := range []namingCase{
		{"fails until annotated", func() []*irv1.Table { return []*irv1.Table{posts(), usersTable()} },
			[]string{"posts.author", "users.posts"}, []string{"ERROR relation-name-equals-column shop.app.posts.author"}},
		{"relation-name resolves it", func() []*irv1.Table {
			return []*irv1.Table{posts(configured(keyRelationName, "x", wordsValue("writer"))), usersTable()}
		}, []string{"posts.writer", "users.posts"}, nil},
		{"a single-word string value", func() []*irv1.Table {
			return []*irv1.Table{posts(configured(keyRelationName, "x", stringValue("writer"))), usersTable()}
		}, []string{"posts.writer", "users.posts"}, nil},
		{"an annotated name equal to a column still fails", func() []*irv1.Table {
			return []*irv1.Table{posts(configured(keyRelationName, "x", wordsValue("author", "id"))), usersTable()}
		}, []string{"posts.author_id", "users.posts"}, []string{"ERROR relation-name-equals-column shop.app.posts.author_id"}},
		{"inverse-name resolves the inverse", func() []*irv1.Table {
			return []*irv1.Table{posts(configured(keyRelationName, "x", wordsValue("writer")), configured(keyInverseName, "x", wordsValue("writings"))),
				usersTable(col("posts", i32(), "posts"))}
		}, []string{"posts.writer", "users.writings"}, nil},
		{"invalid name values", func() []*irv1.Table {
			return []*irv1.Table{posts(configured(keyRelationName, "x", stringValue("Not One Word")), configured(keyInverseName, "x", nil)), usersTable()}
		}, []string{"posts.author", "users.posts"}, []string{
			"ERROR relation-name-equals-column shop.app.posts.author",
			"ERROR annotation-value-invalid shop.app.posts.posts_author_id_fkey",
			"ERROR annotation-value-invalid shop.app.posts.posts_author_id_fkey",
		}},
	} {
		tc.run(t)
	}
}

// §4.3: names collide when their word lists are equal.
func TestColumnCollisions(t *testing.T) {
	for _, tc := range []namingCase{
		{"distinct words pass", func() []*irv1.Table {
			return []*irv1.Table{table("t", []string{"t"}, col("user_id", i64(), "user", "id"), col("user", i64(), "user"))}
		}, nil, nil},
		{"user_id and userId fail", func() []*irv1.Table {
			return []*irv1.Table{table("t", []string{"t"}, col("user_id", i64(), "user", "id"), col("userId", i64(), "user", "id"))}
		}, nil, []string{"ERROR column-name-collision shop.app.t.user_id"}},
		{"a name annotation that removes the collision passes", func() []*irv1.Table {
			c := col("userId", i64(), "user", "id")
			c.HostName = &irv1.Identifier{Words: []string{"legacy", "user", "id"}}
			return []*irv1.Table{table("t", []string{"t"}, col("user_id", i64(), "user", "id"), c)}
		}, nil, nil},
	} {
		tc.run(t)
	}
}
