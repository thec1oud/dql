package facts

import (
	"slices"
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func TestSoftDeleteUnique(t *testing.T) {
	instant := &irv1.Type{Kind: &irv1.Type_Instant{Instant: &irv1.TimeType{}}}
	withOffset := &irv1.Type{Kind: &irv1.Type_InstantWithOffset{InstantWithOffset: &irv1.TimeType{}}}
	local := &irv1.Type{Kind: &irv1.Type_LocalDatetime{LocalDatetime: &irv1.TimeType{}}}
	boolean := &irv1.Type{Kind: &irv1.Type_Bool{Bool: &irv1.Empty{}}}
	soft := func(c *irv1.Column) *irv1.Column {
		c.Annotations = []*irv1.Annotation{commented(keySoftDelete, "config", nil)}
		return c
	}
	partial := func(u *irv1.Uniqueness, null, notTrue []string) *irv1.Uniqueness {
		u.Kind = irv1.UniquenessKind_UNIQUENESS_INDEX
		u.Predicate = &irv1.NativeText{Native: "(...)"}
		u.PredicateNullColumns, u.PredicateNotTrueColumns = null, notTrue
		return u
	}
	posts := func(deleted *irv1.Type, us ...*irv1.Uniqueness) *irv1.Table {
		tb := table("posts", []string{"posts"}, col("id", i64(), "id"), col("slug", text(), "slug"), soft(col("deleted?", deleted, "deleted")))
		tb.Uniqueness = append([]*irv1.Uniqueness{pk("posts_pkey", "id")}, us...)
		return tb
	}
	plainTable := table("tags", []string{"tags"}, col("id", i64(), "id"), col("slug", text(), "slug"))
	plainTable.Uniqueness = []*irv1.Uniqueness{uniq("tags_slug_key", "slug")}
	view := posts(instant, uniq("v_slug_key", "slug"))
	view.Kind = irv1.TableKind_RELATION_KIND_VIEW
	for _, tc := range []struct {
		name string
		tb   *irv1.Table
		want []string
	}{
		{"unique constraint", posts(instant, uniq("posts_slug_key", "slug")), []string{"WARNING soft-delete-unique shop.app.posts.posts_slug_key"}},
		// The record's own example: a predicate that says nothing of deleted_at.
		{"other predicate", posts(instant, partial(uniq("posts_one_published_slug", "slug"), nil, nil)), []string{"WARNING soft-delete-unique shop.app.posts.posts_one_published_slug"}},
		{"timestamp IS NULL", posts(instant, partial(uniq("i", "slug"), []string{"deleted"}, nil)), nil},
		{"timestamp with offset IS NULL", posts(withOffset, partial(uniq("i", "slug"), []string{"deleted"}, nil)), nil},
		{"local timestamp not true", posts(local, partial(uniq("i", "slug"), nil, []string{"deleted"})), []string{"WARNING soft-delete-unique shop.app.posts.i"}},
		{"boolean not true", posts(boolean, partial(uniq("i", "slug"), nil, []string{"deleted"})), nil},
		{"boolean IS NULL", posts(boolean, partial(uniq("i", "slug"), []string{"deleted"}, nil)), nil},
		{"other column", posts(boolean, partial(uniq("i", "slug"), []string{"slug"}, []string{"slug"})), []string{"WARNING soft-delete-unique shop.app.posts.i"}},
		{"primary key only", posts(instant), nil},
		{"no soft-delete column", plainTable, nil},
		{"view", view, nil},
	} {
		got := diagLines(derive(tc.tb))
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
	ds := derive(posts(instant, partial(uniq("posts_one_published_slug", "slug"), nil, nil), uniq("posts_slug_key", "slug")))
	for i, want := range []string{
		"unique index on a soft-delete table does not exclude deleted rows; a deleted row's values cannot be reused",
		"unique constraint on a soft-delete table does not exclude deleted rows; a deleted row's values cannot be reused",
	} {
		if ds[i].GetMessage() != want {
			t.Errorf("message %q, want %q", ds[i].GetMessage(), want)
		}
	}
}
