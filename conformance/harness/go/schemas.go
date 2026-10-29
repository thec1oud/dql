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
