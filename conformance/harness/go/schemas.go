package harness

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/thec1oud/dql/emitters/go/testdata/shopdb"
	"github.com/thec1oud/dql/runtime/go/dql"
	"github.com/thec1oud/dql/runtime/go/dqlpgx"
	"github.com/thec1oud/dql/runtime/go/opt"
)

// Schemas are the schemas scenarios name, each with the Go emitter's code
// for it: shop is the running example, generated into the emitter's golden
// package.
var Schemas = map[string]Schema{
	"shop": {
		DDL: "testdata/shop/schema.sql",
		Open: func(ctx context.Context, dsn string) (any, func(), error) {
			e, err := shopdb.NewExecutor(ctx, dqlpgx.New(), dql.Config{
				Sources:  map[string]dql.SourceConfig{"main": {Primary: dql.ConnConfig{DSN: dsn, MaxConns: 4}}},
				Catalogs: map[string]string{"shop": "main"},
			})
			if err != nil {
				return nil, nil, err
			}
			return shopdb.New(e), e.Close, nil
		},
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
	},
}

// Database creates a database from the DDL file on the server base names,
// and returns its URL and how to drop it.
func Database(ctx context.Context, base, ddl string) (string, func(), error) {
	schema, err := os.ReadFile(ddl)
	if err != nil {
		return "", nil, err
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
