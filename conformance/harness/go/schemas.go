package harness

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/thec1oud/dql/conformance/harness/go/testdata/variantdb"
	"github.com/thec1oud/dql/emitters/go/testdata/shopdb"
	"github.com/thec1oud/dql/runtime/go/dql"
	"github.com/thec1oud/dql/runtime/go/dqlpgx"
	"github.com/thec1oud/dql/runtime/go/opt"
)

// Schemas are the schemas scenarios name, each with the Go emitter's code
// for it: shop is the running example, generated into the emitter's golden
// package, and shop_variants the running example with the design record's
// variants of it (conformance/schemas/postgres/variants.yaml), generated
// into testdata/variantdb.
var Schemas = map[string]Schema{
	"shop": {
		DDL:     []string{"testdata/shop/schema.sql", "conformance/schemas/postgres/inject.sql"},
		Dialect: "postgres",
		Exec:    run,
		Open:    opener(shopdb.NewExecutor, shopdb.New),
		Tables: map[string]any{
			"tenants": shopdb.Tenants, "users": shopdb.Users, "profiles": shopdb.Profiles,
			"posts": shopdb.Posts, "tags": shopdb.Tags, "post_tags": shopdb.PostTags,
		},
		Changes: []any{
			shopdb.PostAuthorCreate, shopdb.PostAuthorLink, shopdb.PostAuthorUpdate,
			shopdb.PostCreateForAuthor, shopdb.PostDeleteForAuthor, shopdb.PostUpdateForAuthor,
			shopdb.PostCreateForEditor, shopdb.PostDeleteForEditor, shopdb.PostUpdateForEditor,
			shopdb.PostEditorCreate, shopdb.PostEditorLink, shopdb.PostEditorUpdate,
			shopdb.PostLinkCreate, shopdb.PostLinkKey, shopdb.PostRefCreate, shopdb.PostRefLink,
			shopdb.PostTagCreateForPost, shopdb.PostTagDeleteForPost, shopdb.PostTagUpdateForPost,
			shopdb.PostTagCreateForTag, shopdb.PostTagDeleteForTag, shopdb.PostTagUpdateForTag,
			shopdb.PostTagPostUpdate, shopdb.PostTagTagUpdate,
			shopdb.PostTagsCreate, shopdb.PostTagsLink, shopdb.PostTagsUnlink,
			shopdb.ProfileCreateForUser, shopdb.ProfileDeleteForUser, shopdb.ProfileUpdateForUser,
			shopdb.ProfileUserUpdate,
			shopdb.TagLinkCreate, shopdb.TagLinkKey, shopdb.TagPostsCreate, shopdb.TagPostsLink, shopdb.TagPostsUnlink,
			shopdb.TagRefCreate, shopdb.TagRefLink, shopdb.UserRefCreate, shopdb.UserRefLink,
			opt.Some[shopdb.PostAuthorChange], opt.Some[shopdb.PostEditorChange], opt.Some[shopdb.PostRef],
			opt.Some[shopdb.PostTagPostChange], opt.Some[shopdb.PostTagTagChange],
			opt.Some[shopdb.ProfileChangeForUser], opt.Some[shopdb.ProfileUserChange],
			opt.Some[shopdb.TagRef], opt.Some[shopdb.UserRef],
			opt.Some[[]shopdb.PostChangeForAuthor], opt.Some[[]shopdb.PostChangeForEditor],
			opt.Some[[]shopdb.PostTagChangeForPost], opt.Some[[]shopdb.PostTagChangeForTag],
			opt.Some[[]shopdb.PostTagsChange], opt.Some[[]shopdb.TagPostsChange],
		},
		Inject:    injectPostgres,
		Terminate: terminatePostgres,
		Replay:    replayPostgres,
		Shapes:    shopdb.Shapes,
		Types: []any{
			shopdb.PostDraft{}, shopdb.PostDraftNested{}, shopdb.PostDraftNests{}, shopdb.PostFull{},
			shopdb.PostFullNested{}, shopdb.PostFullNests{}, shopdb.PostKey{}, shopdb.PostPartial{},
			shopdb.PostPartialNested{}, shopdb.PostPartialNests{}, shopdb.PostTagDraft{},
			shopdb.PostTagDraftNested{}, shopdb.PostTagDraftNests{}, shopdb.PostTagFull{},
			shopdb.PostTagFullNested{}, shopdb.PostTagFullNests{}, shopdb.PostTagKey{}, shopdb.PostTagPartial{},
			shopdb.PostTagPartialNested{}, shopdb.PostTagPartialNests{}, shopdb.ProfileDraft{},
			shopdb.ProfileDraftNested{}, shopdb.ProfileDraftNests{}, shopdb.ProfileFull{},
			shopdb.ProfileFullNested{}, shopdb.ProfileFullNests{}, shopdb.ProfileKey{}, shopdb.ProfilePartial{},
			shopdb.ProfilePartialNested{}, shopdb.ProfilePartialNests{}, shopdb.PublishedPostFull{},
			shopdb.PublishedPostFullNested{}, shopdb.PublishedPostFullNests{}, shopdb.PublishedPostKey{},
			shopdb.TagDraft{}, shopdb.TagDraftNested{}, shopdb.TagDraftNests{}, shopdb.TagFull{},
			shopdb.TagFullNested{}, shopdb.TagFullNests{}, shopdb.TagKey{}, shopdb.TagPartial{},
			shopdb.TagPartialNested{}, shopdb.TagPartialNests{}, shopdb.TenantDraft{},
			shopdb.TenantDraftNested{}, shopdb.TenantDraftNests{}, shopdb.TenantFull{},
			shopdb.TenantFullNested{}, shopdb.TenantFullNests{}, shopdb.TenantKey{}, shopdb.TenantPartial{},
			shopdb.TenantPartialNested{}, shopdb.TenantPartialNests{}, shopdb.UserDraft{},
			shopdb.UserDraftNested{}, shopdb.UserDraftNests{}, shopdb.UserFull{}, shopdb.UserFullNested{},
			shopdb.UserFullNests{}, shopdb.UserKey{}, shopdb.UserPartial{}, shopdb.UserPartialNested{},
			shopdb.UserPartialNests{}, shopdb.UserScope{},
		},
		Funcs: map[string]any{
			"merge_post_by_key": shopdb.MergePostByKey, "merge_post_tag_by_key": shopdb.MergePostTagByKey,
			"merge_profile_by_key": shopdb.MergeProfileByKey, "merge_tag_by_key": shopdb.MergeTagByKey,
			"merge_tenant_by_key": shopdb.MergeTenantByKey, "merge_user_by_key": shopdb.MergeUserByKey,
		},
		Selects: map[string]any{
			"post_full":           shopdb.Select[shopdb.PostFull, shopdb.PostsTable],
			"post_tag_full":       shopdb.Select[shopdb.PostTagFull, shopdb.PostTagsTable],
			"profile_full":        shopdb.Select[shopdb.ProfileFull, shopdb.ProfilesTable],
			"published_post_full": shopdb.Select[shopdb.PublishedPostFull, shopdb.PublishedPostsTable],
			"tag_full":            shopdb.Select[shopdb.TagFull, shopdb.TagsTable],
			"tenant_full":         shopdb.Select[shopdb.TenantFull, shopdb.TenantsTable],
			"user_full":           shopdb.Select[shopdb.UserFull, shopdb.UsersTable],
		},
	},
	"shop_variants": {
		DDL:     []string{"testdata/shop/schema.sql", "conformance/schemas/postgres/variants.sql", "conformance/schemas/postgres/inject.sql"},
		Dialect: "postgres",
		Exec:    run,
		Open:    opener(variantdb.NewExecutor, variantdb.New),
		Tables: map[string]any{
			"tenants": variantdb.Tenants, "users": variantdb.Users, "profiles": variantdb.Profiles,
			"posts": variantdb.Posts, "tags": variantdb.Tags, "post_tags": variantdb.PostTags,
		},
		Changes: []any{
			variantdb.PostAuthorCreate, variantdb.PostAuthorLink, variantdb.PostAuthorUpdate,
			variantdb.PostCreateForAuthor, variantdb.PostDeleteForAuthor, variantdb.PostUpdateForAuthor,
			variantdb.PostCreateForEditor, variantdb.PostDeleteForEditor, variantdb.PostUpdateForEditor,
			variantdb.PostEditorCreate, variantdb.PostEditorLink, variantdb.PostEditorUpdate,
			variantdb.PostLinkCreate, variantdb.PostLinkKey, variantdb.PostRefCreate, variantdb.PostRefLink,
			variantdb.PostTagCreateForPost, variantdb.PostTagDeleteForPost, variantdb.PostTagUpdateForPost,
			variantdb.PostTagCreateForTag, variantdb.PostTagDeleteForTag, variantdb.PostTagUpdateForTag,
			variantdb.PostTagPostUpdate, variantdb.PostTagTagUpdate,
			variantdb.PostTagsCreate, variantdb.PostTagsLink, variantdb.PostTagsUnlink,
			variantdb.ProfileCreateForUser, variantdb.ProfileDeleteForUser, variantdb.ProfileUpdateForUser,
			variantdb.ProfileUserUpdate,
			variantdb.TagLinkCreate, variantdb.TagLinkKey, variantdb.TagPostsCreate, variantdb.TagPostsLink, variantdb.TagPostsUnlink,
			variantdb.TagRefCreate, variantdb.TagRefLink, variantdb.UserRefCreate, variantdb.UserRefLink,
			opt.Some[variantdb.PostAuthorChange], opt.Some[variantdb.PostEditorChange], opt.Some[variantdb.PostRef],
			opt.Some[variantdb.PostTagPostChange], opt.Some[variantdb.PostTagTagChange],
			opt.Some[variantdb.ProfileChangeForUser], opt.Some[variantdb.ProfileUserChange],
			opt.Some[variantdb.TagRef], opt.Some[variantdb.UserRef],
			opt.Some[[]variantdb.PostChangeForAuthor], opt.Some[[]variantdb.PostChangeForEditor],
			opt.Some[[]variantdb.PostTagChangeForPost], opt.Some[[]variantdb.PostTagChangeForTag],
			opt.Some[[]variantdb.PostTagsChange], opt.Some[[]variantdb.TagPostsChange],
		},
		Inject:    injectPostgres,
		Terminate: terminatePostgres,
		Replay:    replayPostgres,
		Shapes:    variantdb.Shapes,
		Types: []any{
			variantdb.PostDraft{}, variantdb.PostDraftNested{}, variantdb.PostDraftNests{}, variantdb.PostFull{},
			variantdb.PostFullNested{}, variantdb.PostFullNests{}, variantdb.PostKey{}, variantdb.PostPartial{},
			variantdb.PostPartialNested{}, variantdb.PostPartialNests{}, variantdb.PostTagDraft{},
			variantdb.PostTagDraftNested{}, variantdb.PostTagDraftNests{}, variantdb.PostTagFull{},
			variantdb.PostTagFullNested{}, variantdb.PostTagFullNests{}, variantdb.PostTagKey{}, variantdb.PostTagPartial{},
			variantdb.PostTagPartialNested{}, variantdb.PostTagPartialNests{}, variantdb.ProfileDraft{},
			variantdb.ProfileDraftNested{}, variantdb.ProfileDraftNests{}, variantdb.ProfileFull{},
			variantdb.ProfileFullNested{}, variantdb.ProfileFullNests{}, variantdb.ProfileKey{}, variantdb.ProfilePartial{},
			variantdb.ProfilePartialNested{}, variantdb.ProfilePartialNests{}, variantdb.PublishedPostFull{},
			variantdb.PublishedPostFullNested{}, variantdb.PublishedPostFullNests{}, variantdb.PublishedPostKey{},
			variantdb.TagDraft{}, variantdb.TagDraftNested{}, variantdb.TagDraftNests{}, variantdb.TagFull{},
			variantdb.TagFullNested{}, variantdb.TagFullNests{}, variantdb.TagKey{}, variantdb.TagPartial{},
			variantdb.TagPartialNested{}, variantdb.TagPartialNests{}, variantdb.TenantDraft{},
			variantdb.TenantDraftNested{}, variantdb.TenantDraftNests{}, variantdb.TenantFull{},
			variantdb.TenantFullNested{}, variantdb.TenantFullNests{}, variantdb.TenantKey{}, variantdb.TenantPartial{},
			variantdb.TenantPartialNested{}, variantdb.TenantPartialNests{}, variantdb.UserDraft{},
			variantdb.UserDraftNested{}, variantdb.UserDraftNests{}, variantdb.UserFull{}, variantdb.UserFullNested{},
			variantdb.UserFullNests{}, variantdb.UserKey{}, variantdb.UserPartial{}, variantdb.UserPartialNested{},
			variantdb.UserPartialNests{}, variantdb.UserScope{}, variantdb.UserSummary{}, variantdb.UserSummaryNamed{}, variantdb.PostWithTags{},
		},
		Funcs: map[string]any{
			"merge_post_by_key": variantdb.MergePostByKey, "merge_post_tag_by_key": variantdb.MergePostTagByKey,
			"merge_profile_by_key": variantdb.MergeProfileByKey, "merge_tag_by_key": variantdb.MergeTagByKey,
			"merge_tenant_by_key": variantdb.MergeTenantByKey, "merge_user_by_key": variantdb.MergeUserByKey,
		},
		Selects: map[string]any{
			"post_full":           variantdb.Select[variantdb.PostFull, variantdb.PostsTable],
			"post_tag_full":       variantdb.Select[variantdb.PostTagFull, variantdb.PostTagsTable],
			"profile_full":        variantdb.Select[variantdb.ProfileFull, variantdb.ProfilesTable],
			"published_post_full": variantdb.Select[variantdb.PublishedPostFull, variantdb.PublishedPostsTable],
			"tag_full":            variantdb.Select[variantdb.TagFull, variantdb.TagsTable],
			"tenant_full":         variantdb.Select[variantdb.TenantFull, variantdb.TenantsTable],
			"user_full":           variantdb.Select[variantdb.UserFull, variantdb.UsersTable],
			"user_summary":        variantdb.Select[variantdb.UserSummary, variantdb.UsersTable],
			"user_summary_named":  variantdb.Select[variantdb.UserSummaryNamed, variantdb.UsersTable],
		},
	},
}

// opener opens a schema's handle, made by newDB on the executor newExec
// makes: on the database at dsn with its replicas, at most maxConns
// connections to it, 4 by default.
func opener[DB any](
	newExec func(context.Context, dql.Driver, dql.Config, ...dql.Option) (*dql.Executor, error),
	newDB func(*dql.Executor) DB,
) func(ctx context.Context, dsn string, replicas []string, maxConns int, opts ...dql.Option) (any, *dql.Executor, error) {
	return func(ctx context.Context, dsn string, replicas []string, maxConns int, opts ...dql.Option) (any, *dql.Executor, error) {
		if maxConns == 0 {
			maxConns = 4
		}
		src := dql.SourceConfig{Primary: dql.ConnConfig{DSN: dsn, MaxConns: maxConns}, MaxLag: maxLag}
		for _, r := range replicas {
			src.Replicas = append(src.Replicas, dql.ConnConfig{DSN: r, MaxConns: 4})
		}
		e, err := newExec(ctx, dqlpgx.New(), dql.Config{
			Sources:  map[string]dql.SourceConfig{"main": src},
			Catalogs: map[string]string{"shop": "main"},
		}, append([]dql.Option{dql.WithTrackInterval(trackInterval)}, opts...)...)
		if err != nil {
			return nil, nil, err
		}
		return newDB(e), e, nil
	}
}

// A replica leaves rotation 1s behind, which the tracker sees within 20ms,
// so that the routing scenario need not wait out the 2s default. Less than
// 1s, and replay of other tests' writes, which run at the same time, can
// keep it out of rotation.
const (
	maxLag        = time.Second
	trackInterval = 20 * time.Millisecond
)

// replayPostgres pauses or resumes WAL replay on the replica at dsn.
func replayPostgres(ctx context.Context, dsn string, pause bool) error {
	if pause {
		return run(ctx, dsn, "SELECT pg_wal_replay_pause()")
	}
	return run(ctx, dsn, "SELECT pg_wal_replay_resume()")
}

// sqlstates are the codes Postgres raises for the retryable classes.
var sqlstates = map[string]string{
	string(dql.ClassSerializationFailure): "40001",
	string(dql.ClassDeadlock):             "40P01",
}

// injectPostgres sets the plan of conformance/schemas/postgres/inject.sql:
// the next in.Times writes sleep for in.Delay, or else fail with in.Class's
// SQLSTATE. Times 0 clears it.
func injectPostgres(ctx context.Context, dsn string, in Inject) error {
	code, ok := sqlstates[in.Class]
	delay := "NULL"
	if in.Delay != "" {
		d, err := time.ParseDuration(in.Delay)
		if err != nil {
			return fmt.Errorf("delay: %w", err)
		}
		delay, ok = fmt.Sprintf("'%d microseconds'", d.Microseconds()), true
	}
	if !ok && in.Times > 0 {
		return fmt.Errorf("no injected failure of class %s", in.Class)
	}
	return run(ctx, dsn, fmt.Sprintf(`TRUNCATE conformance.injection;
INSERT INTO conformance.injection VALUES ('%s', %d, %s);
SELECT setval('conformance.injected', 1, false);`, code, in.Times, delay))
}

// terminatePostgres ends every other connection to the database at dsn, as
// a server shutting them down would.
func terminatePostgres(ctx context.Context, dsn string) error {
	return run(ctx, dsn, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity
WHERE datname = current_database() AND pid <> pg_backend_pid()`)
}

// Database creates a database from the DDL files, in order, on the server
// base names, and returns its URL and how to drop it.
func Database(ctx context.Context, base string, ddl ...string) (string, func(), error) {
	var schema []byte
	for _, f := range ddl {
		b, err := os.ReadFile(f)
		if err != nil {
			return "", nil, err
		}
		schema = append(append(schema, b...), '\n')
	}
	name := fmt.Sprintf("conformance_%d_%d", os.Getpid(), time.Now().UnixNano())
	if err := run(ctx, base, "CREATE DATABASE "+name+" TEMPLATE template0 ENCODING 'UTF8' LOCALE 'C'"); err != nil {
		return "", nil, err
	}
	drop := func() { run(ctx, base, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)") }
	u, _ := url.Parse(base)
	u.Path = "/" + name
	if err := run(ctx, u.String(), string(schema)); err != nil {
		drop()
		return "", nil, err
	}
	return u.String(), drop, nil
}

// run runs sql on a connection of its own to dsn.
func run(ctx context.Context, dsn, sql string) error {
	c, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer c.Close(ctx)
	_, err = c.Exec(ctx, sql)
	return err
}
