package harness

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"

	"github.com/thec1oud/dql/conformance/harness/go/testdata/mysqlshopdb"
	"github.com/thec1oud/dql/runtime/go/dql"
	"github.com/thec1oud/dql/runtime/go/dqlmysql"
	"github.com/thec1oud/dql/runtime/go/opt"
)

// MySQLSchemas are the schemas scenarios name on MySQL: shop is the running
// example on MySQL (conformance/schemas/mysql/shop.sql), generated into
// testdata/mysqlshopdb.
var MySQLSchemas = map[string]Schema{
	"shop": {
		DDL:      []string{"conformance/schemas/mysql/shop.sql", "conformance/schemas/mysql/inject.sql"},
		Dialect:  "mysql",
		URLEnv:   "DQL_TEST_MYSQL_URL",
		Database: databaseMySQL,
		Modes:    []dql.StatementMode{dql.ModePrepare, dql.ModeSimple},
		Features: []string{"tinyint-bool"},
		Exec:     runMySQL,
		Open:     opener(func() dql.Driver { return dqlmysql.New() }, mysqlshopdb.NewExecutor, mysqlshopdb.New),
		Tables: map[string]any{
			"tenants": mysqlshopdb.Tenants, "users": mysqlshopdb.Users, "profiles": mysqlshopdb.Profiles,
			"posts": mysqlshopdb.Posts, "tags": mysqlshopdb.Tags, "post_tags": mysqlshopdb.PostTags,
		},
		Changes: []any{
			mysqlshopdb.PostAuthorCreate, mysqlshopdb.PostAuthorLink, mysqlshopdb.PostAuthorUpdate,
			mysqlshopdb.PostCreateForAuthor, mysqlshopdb.PostDeleteForAuthor, mysqlshopdb.PostUpdateForAuthor,
			mysqlshopdb.PostCreateForEditor, mysqlshopdb.PostDeleteForEditor, mysqlshopdb.PostUpdateForEditor,
			mysqlshopdb.PostEditorCreate, mysqlshopdb.PostEditorLink, mysqlshopdb.PostEditorUpdate,
			mysqlshopdb.PostLinkCreate, mysqlshopdb.PostLinkKey, mysqlshopdb.PostRefCreate, mysqlshopdb.PostRefLink,
			mysqlshopdb.PostTagCreateForPost, mysqlshopdb.PostTagDeleteForPost, mysqlshopdb.PostTagUpdateForPost,
			mysqlshopdb.PostTagCreateForTag, mysqlshopdb.PostTagDeleteForTag, mysqlshopdb.PostTagUpdateForTag,
			mysqlshopdb.PostTagPostUpdate, mysqlshopdb.PostTagTagUpdate,
			mysqlshopdb.PostTagsCreate, mysqlshopdb.PostTagsLink, mysqlshopdb.PostTagsUnlink,
			mysqlshopdb.ProfileCreateForUser, mysqlshopdb.ProfileDeleteForUser, mysqlshopdb.ProfileUpdateForUser,
			mysqlshopdb.ProfileUserUpdate,
			mysqlshopdb.TagLinkCreate, mysqlshopdb.TagLinkKey, mysqlshopdb.TagPostsCreate, mysqlshopdb.TagPostsLink, mysqlshopdb.TagPostsUnlink,
			mysqlshopdb.TagRefCreate, mysqlshopdb.TagRefLink, mysqlshopdb.UserRefCreate, mysqlshopdb.UserRefLink,
			opt.Some[mysqlshopdb.PostAuthorChange], opt.Some[mysqlshopdb.PostEditorChange], opt.Some[mysqlshopdb.PostRef],
			opt.Some[mysqlshopdb.PostTagPostChange], opt.Some[mysqlshopdb.PostTagTagChange],
			opt.Some[mysqlshopdb.ProfileChangeForUser], opt.Some[mysqlshopdb.ProfileUserChange],
			opt.Some[mysqlshopdb.TagRef], opt.Some[mysqlshopdb.UserRef],
			opt.Some[[]mysqlshopdb.PostChangeForAuthor], opt.Some[[]mysqlshopdb.PostChangeForEditor],
			opt.Some[[]mysqlshopdb.PostTagChangeForPost], opt.Some[[]mysqlshopdb.PostTagChangeForTag],
			opt.Some[[]mysqlshopdb.PostTagsChange], opt.Some[[]mysqlshopdb.TagPostsChange],
		},
		Fill: map[string]map[string]func() any{
			"TenantDraft": {"id": newUUID}, "TenantDraftNested": {"id": newUUID},
		},
		Inject:    injectMySQL,
		Terminate: terminateMySQL,
		Shapes:    mysqlshopdb.Shapes,
		Types: []any{
			mysqlshopdb.PostDraft{}, mysqlshopdb.PostDraftNested{}, mysqlshopdb.PostDraftNests{}, mysqlshopdb.PostFull{},
			mysqlshopdb.PostFullNested{}, mysqlshopdb.PostFullNests{}, mysqlshopdb.PostKey{}, mysqlshopdb.PostPartial{},
			mysqlshopdb.PostPartialNested{}, mysqlshopdb.PostPartialNests{}, mysqlshopdb.PostTagDraft{},
			mysqlshopdb.PostTagDraftNested{}, mysqlshopdb.PostTagDraftNests{}, mysqlshopdb.PostTagFull{},
			mysqlshopdb.PostTagFullNested{}, mysqlshopdb.PostTagFullNests{}, mysqlshopdb.PostTagKey{}, mysqlshopdb.PostTagPartial{},
			mysqlshopdb.PostTagPartialNested{}, mysqlshopdb.PostTagPartialNests{}, mysqlshopdb.ProfileDraft{},
			mysqlshopdb.ProfileDraftNested{}, mysqlshopdb.ProfileDraftNests{}, mysqlshopdb.ProfileFull{},
			mysqlshopdb.ProfileFullNested{}, mysqlshopdb.ProfileFullNests{}, mysqlshopdb.ProfileKey{}, mysqlshopdb.ProfilePartial{},
			mysqlshopdb.ProfilePartialNested{}, mysqlshopdb.ProfilePartialNests{}, mysqlshopdb.PublishedPostFull{},
			mysqlshopdb.PublishedPostFullNested{}, mysqlshopdb.PublishedPostFullNests{},
			mysqlshopdb.TagDraft{}, mysqlshopdb.TagDraftNested{}, mysqlshopdb.TagDraftNests{}, mysqlshopdb.TagFull{},
			mysqlshopdb.TagFullNested{}, mysqlshopdb.TagFullNests{}, mysqlshopdb.TagKey{}, mysqlshopdb.TagPartial{},
			mysqlshopdb.TagPartialNested{}, mysqlshopdb.TagPartialNests{}, mysqlshopdb.TenantDraft{},
			mysqlshopdb.TenantDraftNested{}, mysqlshopdb.TenantDraftNests{}, mysqlshopdb.TenantFull{},
			mysqlshopdb.TenantFullNested{}, mysqlshopdb.TenantFullNests{}, mysqlshopdb.TenantKey{}, mysqlshopdb.TenantPartial{},
			mysqlshopdb.TenantPartialNested{}, mysqlshopdb.TenantPartialNests{}, mysqlshopdb.UserDraft{},
			mysqlshopdb.UserDraftNested{}, mysqlshopdb.UserDraftNests{}, mysqlshopdb.UserFull{}, mysqlshopdb.UserFullNested{},
			mysqlshopdb.UserFullNests{}, mysqlshopdb.UserKey{}, mysqlshopdb.UserPartial{}, mysqlshopdb.UserPartialNested{},
			mysqlshopdb.UserPartialNests{}, mysqlshopdb.UserScope{},
		},
		Funcs: map[string]any{
			"merge_post_by_key": mysqlshopdb.MergePostByKey, "merge_post_tag_by_key": mysqlshopdb.MergePostTagByKey,
			"merge_profile_by_key": mysqlshopdb.MergeProfileByKey, "merge_tag_by_key": mysqlshopdb.MergeTagByKey,
			"merge_tenant_by_key": mysqlshopdb.MergeTenantByKey, "merge_user_by_key": mysqlshopdb.MergeUserByKey,
		},
		Selects: map[string]any{
			"post_full":           mysqlshopdb.Select[mysqlshopdb.PostFull, mysqlshopdb.PostsTable],
			"post_tag_full":       mysqlshopdb.Select[mysqlshopdb.PostTagFull, mysqlshopdb.PostTagsTable],
			"profile_full":        mysqlshopdb.Select[mysqlshopdb.ProfileFull, mysqlshopdb.ProfilesTable],
			"published_post_full": mysqlshopdb.Select[mysqlshopdb.PublishedPostFull, mysqlshopdb.PublishedPostsTable],
			"tag_full":            mysqlshopdb.Select[mysqlshopdb.TagFull, mysqlshopdb.TagsTable],
			"tenant_full":         mysqlshopdb.Select[mysqlshopdb.TenantFull, mysqlshopdb.TenantsTable],
			"user_full":           mysqlshopdb.Select[mysqlshopdb.UserFull, mysqlshopdb.UsersTable],
		},
	},
}

// errnos are the error numbers MySQL raises for the classes a step injects.
// MySQL reports a serialization conflict as a deadlock, so it has no
// serialization failure to inject.
var errnos = map[string]int{
	string(dql.ClassDeadlock):    1213,
	string(dql.ClassLockTimeout): 1205,
}

// injectMySQL sets the plan of conformance/schemas/mysql/inject.sql: the
// next in.Times writes sleep for in.Delay, or else fail with in.Class's
// error number. Times 0 clears it.
func injectMySQL(ctx context.Context, dsn string, in Inject) error {
	code, ok := errnos[in.Class]
	delay := "NULL"
	if in.Delay != "" {
		d, err := time.ParseDuration(in.Delay)
		if err != nil {
			return fmt.Errorf("delay: %w", err)
		}
		delay, ok = fmt.Sprint(d.Microseconds()), true
	}
	if !ok && in.Times > 0 {
		return fmt.Errorf("no injected failure of class %s", in.Class)
	}
	return runMySQL(ctx, dsn, fmt.Sprintf(`DELETE FROM conformance_injection;
INSERT INTO conformance_injection VALUES (%d, %d, %s);
TRUNCATE conformance_injected;`, code, in.Times, delay))
}

// terminateMySQL ends every other connection to the database at dsn, as a
// server shutting them down would.
func terminateMySQL(ctx context.Context, dsn string) error {
	db, err := openMySQL(dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	var ids sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT GROUP_CONCAT(id) FROM information_schema.processlist
WHERE db = DATABASE() AND id <> CONNECTION_ID()`).Scan(&ids); err != nil {
		return err
	}
	for _, id := range strings.Split(ids.String, ",") {
		if id != "" {
			// A connection may have ended on its own meanwhile.
			db.ExecContext(ctx, "KILL "+id)
		}
	}
	return nil
}

// databaseMySQL creates a database from the DDL files, in order, on the
// server base names, a DSN without a database, and returns its DSN and how
// to drop it.
func databaseMySQL(ctx context.Context, base string, ddl ...string) (string, func(), error) {
	var schema []byte
	for _, f := range ddl {
		b, err := os.ReadFile(f)
		if err != nil {
			return "", nil, err
		}
		schema = append(append(schema, b...), '\n')
	}
	cfg, err := mysql.ParseDSN(base)
	if err != nil {
		return "", nil, err
	}
	name := fmt.Sprintf("conformance_%d_%d", os.Getpid(), time.Now().UnixNano())
	if err := runMySQL(ctx, base, "CREATE DATABASE "+name+" CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"); err != nil {
		return "", nil, err
	}
	drop := func() { runMySQL(ctx, base, "DROP DATABASE IF EXISTS "+name) }
	cfg.DBName = name
	dsn := cfg.FormatDSN()
	if err := runMySQL(ctx, dsn, string(schema)); err != nil {
		drop()
		return "", nil, err
	}
	return dsn, drop, nil
}

// runMySQL runs statements, separated by semicolons, on a connection of
// its own to dsn.
func runMySQL(ctx context.Context, dsn, stmts string) error {
	db, err := openMySQL(dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.ExecContext(ctx, stmts)
	return err
}

// openMySQL opens one connection to dsn that runs several statements at
// once.
func openMySQL(dsn string) (*sql.DB, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, err
	}
	cfg.MultiStatements = true
	// ParseDSN checked the configuration NewConnector would refuse.
	c, _ := mysql.NewConnector(cfg)
	db := sql.OpenDB(c)
	db.SetMaxOpenConns(1)
	return db, nil
}

// newUUID returns a random UUID's text, as MySQL's uuid() default would
// write one.
func newUUID() any { return uuid.NewString() }
