package harness

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"

	"github.com/thec1oud/dql/conformance/harness/go/testdata/sqliteshopdb"
	"github.com/thec1oud/dql/runtime/go/dql"
	"github.com/thec1oud/dql/runtime/go/dqlsqlite"
	"github.com/thec1oud/dql/runtime/go/opt"
)

// SQLiteSchemas are the schemas scenarios name on SQLite: shop is the
// running example on SQLite (conformance/schemas/sqlite/shop.sql), generated
// by shop.yaml into testdata/sqliteshopdb. SQLite runs in the process, so it
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
