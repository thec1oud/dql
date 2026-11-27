package harness

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"

	"github.com/thec1oud/dql/conformance/harness/go/testdata/sqliteshopdb"
	"github.com/thec1oud/dql/conformance/harness/go/testdata/sqlitevariantdb"
	"github.com/thec1oud/dql/runtime/go/dql"
	"github.com/thec1oud/dql/runtime/go/dqlsqlite"
	"github.com/thec1oud/dql/runtime/go/opt"
)

// SQLiteSchemas are the schemas scenarios name on SQLite: shop is the
// running example on SQLite (conformance/schemas/sqlite/shop.sql), generated
// by shop.yaml into testdata/sqliteshopdb, and shop_variants the running
// example with the design record's variants of it
// (conformance/schemas/sqlite/variants.yaml), generated into
// testdata/sqlitevariantdb. SQLite runs in the process, so it
// needs no server, and it has no injected failures, stalls or replicas; its
// statements are always prepared.
var SQLiteSchemas = map[string]Schema{
	"shop": {
		DDL:      []string{"conformance/schemas/sqlite/shop.sql"},
		Dialect:  "sqlite",
		Database: databaseSQLite,
		Modes:    []dql.StatementMode{dql.ModePrepare},
		Features: []string{"params-32766", "returning", "settable-identity"},
		Exec:     runSQLite,
		Open:     opener(func() dql.Driver { return dqlsqlite.New() }, sqliteshopdb.NewExecutor, sqliteshopdb.New),
		Tables: map[string]any{
			"tenants": sqliteshopdb.Tenants, "users": sqliteshopdb.Users, "profiles": sqliteshopdb.Profiles,
			"posts": sqliteshopdb.Posts, "tags": sqliteshopdb.Tags, "post_tags": sqliteshopdb.PostTags,
		},
		Changes: []any{
			sqliteshopdb.PostAuthorCreate, sqliteshopdb.PostAuthorLink, sqliteshopdb.PostAuthorUpdate,
			sqliteshopdb.PostCreateForAuthor, sqliteshopdb.PostDeleteForAuthor, sqliteshopdb.PostUpdateForAuthor,
			sqliteshopdb.PostCreateForEditor, sqliteshopdb.PostDeleteForEditor, sqliteshopdb.PostUpdateForEditor,
			sqliteshopdb.PostEditorCreate, sqliteshopdb.PostEditorLink, sqliteshopdb.PostEditorUpdate,
			sqliteshopdb.PostLinkCreate, sqliteshopdb.PostLinkKey, sqliteshopdb.PostRefCreate, sqliteshopdb.PostRefLink,
			sqliteshopdb.PostTagCreateForPost, sqliteshopdb.PostTagDeleteForPost, sqliteshopdb.PostTagUpdateForPost,
			sqliteshopdb.PostTagCreateForTag, sqliteshopdb.PostTagDeleteForTag, sqliteshopdb.PostTagUpdateForTag,
			sqliteshopdb.PostTagPostUpdate, sqliteshopdb.PostTagTagUpdate,
			sqliteshopdb.PostTagsCreate, sqliteshopdb.PostTagsLink, sqliteshopdb.PostTagsUnlink,
			sqliteshopdb.ProfileCreateForUser, sqliteshopdb.ProfileDeleteForUser, sqliteshopdb.ProfileUpdateForUser,
			sqliteshopdb.ProfileUserUpdate,
			sqliteshopdb.TagLinkCreate, sqliteshopdb.TagLinkKey, sqliteshopdb.TagPostsCreate, sqliteshopdb.TagPostsLink, sqliteshopdb.TagPostsUnlink,
			sqliteshopdb.TagRefCreate, sqliteshopdb.TagRefLink, sqliteshopdb.UserRefCreate, sqliteshopdb.UserRefLink,
			opt.Some[sqliteshopdb.PostAuthorChange], opt.Some[sqliteshopdb.PostEditorChange], opt.Some[sqliteshopdb.PostRef],
			opt.Some[sqliteshopdb.PostTagPostChange], opt.Some[sqliteshopdb.PostTagTagChange],
			opt.Some[sqliteshopdb.ProfileChangeForUser], opt.Some[sqliteshopdb.ProfileUserChange],
			opt.Some[sqliteshopdb.TagRef], opt.Some[sqliteshopdb.UserRef],
			opt.Some[[]sqliteshopdb.PostChangeForAuthor], opt.Some[[]sqliteshopdb.PostChangeForEditor],
			opt.Some[[]sqliteshopdb.PostTagChangeForPost], opt.Some[[]sqliteshopdb.PostTagChangeForTag],
			opt.Some[[]sqliteshopdb.PostTagsChange], opt.Some[[]sqliteshopdb.TagPostsChange],
		},
		Fill: map[string]map[string]func() any{
			"TenantDraft": {"id": newUUID}, "TenantDraftNested": {"id": newUUID},
		},
		Inject:    injectSQLite,
		Terminate: terminateSQLite,
		Replay:    replaySQLite,
		Shapes:    sqliteshopdb.Shapes,
		Types: []any{
			sqliteshopdb.PostDraft{}, sqliteshopdb.PostDraftNested{}, sqliteshopdb.PostDraftNests{}, sqliteshopdb.PostFull{},
			sqliteshopdb.PostFullNested{}, sqliteshopdb.PostFullNests{}, sqliteshopdb.PostKey{}, sqliteshopdb.PostPartial{},
			sqliteshopdb.PostPartialNested{}, sqliteshopdb.PostPartialNests{}, sqliteshopdb.PostTagDraft{},
			sqliteshopdb.PostTagDraftNested{}, sqliteshopdb.PostTagDraftNests{}, sqliteshopdb.PostTagFull{},
			sqliteshopdb.PostTagFullNested{}, sqliteshopdb.PostTagFullNests{}, sqliteshopdb.PostTagKey{}, sqliteshopdb.PostTagPartial{},
			sqliteshopdb.PostTagPartialNested{}, sqliteshopdb.PostTagPartialNests{}, sqliteshopdb.ProfileDraft{},
			sqliteshopdb.ProfileDraftNested{}, sqliteshopdb.ProfileDraftNests{}, sqliteshopdb.ProfileFull{},
			sqliteshopdb.ProfileFullNested{}, sqliteshopdb.ProfileFullNests{}, sqliteshopdb.ProfileKey{}, sqliteshopdb.ProfilePartial{},
			sqliteshopdb.ProfilePartialNested{}, sqliteshopdb.ProfilePartialNests{}, sqliteshopdb.PublishedPostFull{},
			sqliteshopdb.PublishedPostFullNested{}, sqliteshopdb.PublishedPostFullNests{},
			sqliteshopdb.TagDraft{}, sqliteshopdb.TagDraftNested{}, sqliteshopdb.TagDraftNests{}, sqliteshopdb.TagFull{},
			sqliteshopdb.TagFullNested{}, sqliteshopdb.TagFullNests{}, sqliteshopdb.TagKey{}, sqliteshopdb.TagPartial{},
			sqliteshopdb.TagPartialNested{}, sqliteshopdb.TagPartialNests{}, sqliteshopdb.TenantDraft{},
			sqliteshopdb.TenantDraftNested{}, sqliteshopdb.TenantDraftNests{}, sqliteshopdb.TenantFull{},
			sqliteshopdb.TenantFullNested{}, sqliteshopdb.TenantFullNests{}, sqliteshopdb.TenantKey{}, sqliteshopdb.TenantPartial{},
			sqliteshopdb.TenantPartialNested{}, sqliteshopdb.TenantPartialNests{}, sqliteshopdb.UserDraft{},
			sqliteshopdb.UserDraftNested{}, sqliteshopdb.UserDraftNests{}, sqliteshopdb.UserFull{}, sqliteshopdb.UserFullNested{},
			sqliteshopdb.UserFullNests{}, sqliteshopdb.UserKey{}, sqliteshopdb.UserPartial{}, sqliteshopdb.UserPartialNested{},
			sqliteshopdb.UserPartialNests{}, sqliteshopdb.UserScope{},
		},
		Funcs: map[string]any{
			"merge_post_by_key": sqliteshopdb.MergePostByKey, "merge_post_tag_by_key": sqliteshopdb.MergePostTagByKey,
			"merge_profile_by_key": sqliteshopdb.MergeProfileByKey, "merge_tag_by_key": sqliteshopdb.MergeTagByKey,
			"merge_tenant_by_key": sqliteshopdb.MergeTenantByKey, "merge_user_by_key": sqliteshopdb.MergeUserByKey,
		},
		Selects: map[string]any{
			"post_full":           sqliteshopdb.Select[sqliteshopdb.PostFull, sqliteshopdb.PostsTable],
			"post_tag_full":       sqliteshopdb.Select[sqliteshopdb.PostTagFull, sqliteshopdb.PostTagsTable],
			"profile_full":        sqliteshopdb.Select[sqliteshopdb.ProfileFull, sqliteshopdb.ProfilesTable],
			"published_post_full": sqliteshopdb.Select[sqliteshopdb.PublishedPostFull, sqliteshopdb.PublishedPostsTable],
			"tag_full":            sqliteshopdb.Select[sqliteshopdb.TagFull, sqliteshopdb.TagsTable],
			"tenant_full":         sqliteshopdb.Select[sqliteshopdb.TenantFull, sqliteshopdb.TenantsTable],
			"user_full":           sqliteshopdb.Select[sqliteshopdb.UserFull, sqliteshopdb.UsersTable],
		},
	},
	"shop_variants": {
		DDL:      []string{"conformance/schemas/sqlite/shop.sql", "conformance/schemas/sqlite/variants.sql"},
		Dialect:  "sqlite",
		Database: databaseSQLite,
		Modes:    []dql.StatementMode{dql.ModePrepare},
		Features: []string{"params-32766", "returning", "settable-identity"},
		Exec:     runSQLite,
		Open:     opener(func() dql.Driver { return dqlsqlite.New() }, sqlitevariantdb.NewExecutor, sqlitevariantdb.New),
		Tables: map[string]any{
			"tenants": sqlitevariantdb.Tenants, "users": sqlitevariantdb.Users, "profiles": sqlitevariantdb.Profiles,
			"posts": sqlitevariantdb.Posts, "tags": sqlitevariantdb.Tags, "post_tags": sqlitevariantdb.PostTags,
		},
		Changes: []any{
			sqlitevariantdb.PostAuthorCreate, sqlitevariantdb.PostAuthorLink, sqlitevariantdb.PostAuthorUpdate,
			sqlitevariantdb.PostCreateForAuthor, sqlitevariantdb.PostDeleteForAuthor, sqlitevariantdb.PostUpdateForAuthor,
			sqlitevariantdb.PostCreateForEditor, sqlitevariantdb.PostDeleteForEditor, sqlitevariantdb.PostUpdateForEditor,
			sqlitevariantdb.PostEditorCreate, sqlitevariantdb.PostEditorLink, sqlitevariantdb.PostEditorUpdate,
			sqlitevariantdb.PostLinkCreate, sqlitevariantdb.PostLinkKey, sqlitevariantdb.PostRefCreate, sqlitevariantdb.PostRefLink,
			sqlitevariantdb.PostTagCreateForPost, sqlitevariantdb.PostTagDeleteForPost, sqlitevariantdb.PostTagUpdateForPost,
			sqlitevariantdb.PostTagCreateForTag, sqlitevariantdb.PostTagDeleteForTag, sqlitevariantdb.PostTagUpdateForTag,
			sqlitevariantdb.PostTagPostUpdate, sqlitevariantdb.PostTagTagUpdate,
			sqlitevariantdb.PostTagsCreate, sqlitevariantdb.PostTagsLink, sqlitevariantdb.PostTagsUnlink,
			sqlitevariantdb.ProfileCreateForUser, sqlitevariantdb.ProfileDeleteForUser, sqlitevariantdb.ProfileUpdateForUser,
			sqlitevariantdb.ProfileUserUpdate,
			sqlitevariantdb.TagLinkCreate, sqlitevariantdb.TagLinkKey, sqlitevariantdb.TagPostsCreate, sqlitevariantdb.TagPostsLink, sqlitevariantdb.TagPostsUnlink,
			sqlitevariantdb.TagRefCreate, sqlitevariantdb.TagRefLink, sqlitevariantdb.UserRefCreate, sqlitevariantdb.UserRefLink,
			opt.Some[sqlitevariantdb.PostAuthorChange], opt.Some[sqlitevariantdb.PostEditorChange], opt.Some[sqlitevariantdb.PostRef],
			opt.Some[sqlitevariantdb.PostTagPostChange], opt.Some[sqlitevariantdb.PostTagTagChange],
			opt.Some[sqlitevariantdb.ProfileChangeForUser], opt.Some[sqlitevariantdb.ProfileUserChange],
			opt.Some[sqlitevariantdb.TagRef], opt.Some[sqlitevariantdb.UserRef],
			opt.Some[[]sqlitevariantdb.PostChangeForAuthor], opt.Some[[]sqlitevariantdb.PostChangeForEditor],
			opt.Some[[]sqlitevariantdb.PostTagChangeForPost], opt.Some[[]sqlitevariantdb.PostTagChangeForTag],
			opt.Some[[]sqlitevariantdb.PostTagsChange], opt.Some[[]sqlitevariantdb.TagPostsChange],
		},
		Fill: map[string]map[string]func() any{
			"TenantDraft": {"id": newUUID}, "TenantDraftNested": {"id": newUUID},
		},
		Inject:    injectSQLite,
		Terminate: terminateSQLite,
		Replay:    replaySQLite,
		Shapes:    sqlitevariantdb.Shapes,
		Types: []any{
			sqlitevariantdb.PostDraft{}, sqlitevariantdb.PostDraftNested{}, sqlitevariantdb.PostDraftNests{}, sqlitevariantdb.PostFull{},
			sqlitevariantdb.PostFullNested{}, sqlitevariantdb.PostFullNests{}, sqlitevariantdb.PostKey{}, sqlitevariantdb.PostPartial{},
			sqlitevariantdb.PostPartialNested{}, sqlitevariantdb.PostPartialNests{}, sqlitevariantdb.PostTagDraft{},
			sqlitevariantdb.PostTagDraftNested{}, sqlitevariantdb.PostTagDraftNests{}, sqlitevariantdb.PostTagFull{},
			sqlitevariantdb.PostTagFullNested{}, sqlitevariantdb.PostTagFullNests{}, sqlitevariantdb.PostTagKey{}, sqlitevariantdb.PostTagPartial{},
			sqlitevariantdb.PostTagPartialNested{}, sqlitevariantdb.PostTagPartialNests{}, sqlitevariantdb.ProfileDraft{},
			sqlitevariantdb.ProfileDraftNested{}, sqlitevariantdb.ProfileDraftNests{}, sqlitevariantdb.ProfileFull{},
			sqlitevariantdb.ProfileFullNested{}, sqlitevariantdb.ProfileFullNests{}, sqlitevariantdb.ProfileKey{}, sqlitevariantdb.ProfilePartial{},
			sqlitevariantdb.ProfilePartialNested{}, sqlitevariantdb.ProfilePartialNests{}, sqlitevariantdb.PublishedPostFull{},
			sqlitevariantdb.PublishedPostFullNested{}, sqlitevariantdb.PublishedPostFullNests{},
			sqlitevariantdb.TagDraft{}, sqlitevariantdb.TagDraftNested{}, sqlitevariantdb.TagDraftNests{}, sqlitevariantdb.TagFull{},
			sqlitevariantdb.TagFullNested{}, sqlitevariantdb.TagFullNests{}, sqlitevariantdb.TagKey{}, sqlitevariantdb.TagPartial{},
			sqlitevariantdb.TagPartialNested{}, sqlitevariantdb.TagPartialNests{}, sqlitevariantdb.TenantDraft{},
			sqlitevariantdb.TenantDraftNested{}, sqlitevariantdb.TenantDraftNests{}, sqlitevariantdb.TenantFull{},
			sqlitevariantdb.TenantFullNested{}, sqlitevariantdb.TenantFullNests{}, sqlitevariantdb.TenantKey{}, sqlitevariantdb.TenantPartial{},
			sqlitevariantdb.TenantPartialNested{}, sqlitevariantdb.TenantPartialNests{}, sqlitevariantdb.UserDraft{},
			sqlitevariantdb.UserDraftNested{}, sqlitevariantdb.UserDraftNests{}, sqlitevariantdb.UserFull{}, sqlitevariantdb.UserFullNested{},
			sqlitevariantdb.UserFullNests{}, sqlitevariantdb.UserKey{}, sqlitevariantdb.UserPartial{}, sqlitevariantdb.UserPartialNested{},
			sqlitevariantdb.UserPartialNests{}, sqlitevariantdb.UserScope{}, sqlitevariantdb.UserSummary{}, sqlitevariantdb.UserSummaryNamed{}, sqlitevariantdb.PostWithTags{},
		},
		Funcs: map[string]any{
			"merge_post_by_key": sqlitevariantdb.MergePostByKey, "merge_post_tag_by_key": sqlitevariantdb.MergePostTagByKey,
			"merge_profile_by_key": sqlitevariantdb.MergeProfileByKey, "merge_tag_by_key": sqlitevariantdb.MergeTagByKey,
			"merge_tenant_by_key": sqlitevariantdb.MergeTenantByKey, "merge_user_by_key": sqlitevariantdb.MergeUserByKey,
		},
		Selects: map[string]any{
			"post_full":           sqlitevariantdb.Select[sqlitevariantdb.PostFull, sqlitevariantdb.PostsTable],
			"post_tag_full":       sqlitevariantdb.Select[sqlitevariantdb.PostTagFull, sqlitevariantdb.PostTagsTable],
			"profile_full":        sqlitevariantdb.Select[sqlitevariantdb.ProfileFull, sqlitevariantdb.ProfilesTable],
			"published_post_full": sqlitevariantdb.Select[sqlitevariantdb.PublishedPostFull, sqlitevariantdb.PublishedPostsTable],
			"tag_full":            sqlitevariantdb.Select[sqlitevariantdb.TagFull, sqlitevariantdb.TagsTable],
			"tenant_full":         sqlitevariantdb.Select[sqlitevariantdb.TenantFull, sqlitevariantdb.TenantsTable],
			"user_full":           sqlitevariantdb.Select[sqlitevariantdb.UserFull, sqlitevariantdb.UsersTable],
			"user_summary":        sqlitevariantdb.Select[sqlitevariantdb.UserSummary, sqlitevariantdb.UsersTable],
			"user_summary_named":  sqlitevariantdb.Select[sqlitevariantdb.UserSummaryNamed, sqlitevariantdb.UsersTable],
		},
	},
}

// errNoServer is the error of what only a database server does: SQLite runs
// in the process, so nothing injects its failures, stalls it or ends its
// connections, and no scenario that needs these runs on it.
var errNoServer = errors.New("SQLite runs in the process, with no server to inject failures, end connections or replicate")

func injectSQLite(context.Context, string, Inject) error { return errNoServer }
func terminateSQLite(context.Context, string) error      { return errNoServer }
func replaySQLite(context.Context, string, bool) error   { return errNoServer }

// databaseSQLite creates a database file from the DDL files, in order, in a
// directory of its own, and returns its DSN, which waits up to five seconds
// for another connection's lock, and how to remove it. base is unused: there
// is no server.
func databaseSQLite(ctx context.Context, _ string, ddl ...string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "conformance-sqlite-")
	if err != nil {
		return "", nil, err
	}
	drop := func() { os.RemoveAll(dir) }
	dsn := "file:" + filepath.Join(dir, "shop.db") + "?_pragma=busy_timeout(5000)"
	for _, f := range ddl {
		b, err := os.ReadFile(f)
		if err == nil {
			err = runSQLite(ctx, dsn, string(b))
		}
		if err != nil {
			drop()
			return "", nil, err
		}
	}
	return dsn, drop, nil
}

// runSQLite runs statements, separated by semicolons, on a connection of
// its own to dsn.
func runSQLite(ctx context.Context, dsn, stmts string) error {
	// dqlsqlite registers the driver, so Open does not fail.
	db, _ := sql.Open("sqlite", dsn)
	defer db.Close()
	_, err := db.ExecContext(ctx, stmts)
	return err
}
