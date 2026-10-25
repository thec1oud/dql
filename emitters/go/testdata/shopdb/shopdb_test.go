package shopdb_test

// This file is hand-written (§26.5): it runs the generated package against
// the running example's schema on the Postgres server DQL_TEST_POSTGRES_URL
// names, in a fresh database. The go emitter's tests run it.

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/thec1oud/dql/emitters/go/testdata/shopdb"
	"github.com/thec1oud/dql/runtime/go/dql"
	"github.com/thec1oud/dql/runtime/go/dqlpgx"
	"github.com/thec1oud/dql/runtime/go/opt"
)

// database creates a fresh database holding testdata/shop/schema.sql and
// returns its URL; it is dropped when the test ends.
func database(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	base := os.Getenv("DQL_TEST_POSTGRES_URL")
	if base == "" {
		t.Fatal("DQL_TEST_POSTGRES_URL is not set")
	}
	schema, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "testdata", "shop", "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("shopdb_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name+" TEMPLATE template0 ENCODING 'UTF8' LOCALE 'C'"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(ctx)
	})
	u, _ := url.Parse(base)
	u.Path = "/" + name
	c, err := pgx.Connect(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	if _, err := c.Exec(ctx, string(schema)); err != nil {
		t.Fatal(err)
	}
	return u.String()
}

func executor(t *testing.T) *dql.Executor {
	t.Helper()
	e, err := shopdb.NewExecutor(context.Background(), dqlpgx.New(), dql.Config{
		Sources:  map[string]dql.SourceConfig{"main": {Primary: dql.ConnConfig{DSN: database(t), MaxConns: 4}}},
		Catalogs: map[string]string{"shop": "main"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return e
}

// must returns v, and fails the test, by panicking, on an error.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestStores(t *testing.T) {
	ctx := context.Background()
	db := shopdb.New(executor(t))

	acme := must(db.Tenants.Insert(ctx, shopdb.TenantDraft{Name: "acme"}))
	other := must(db.Tenants.Insert(ctx, shopdb.TenantDraft{ID: opt.Some(uuid.New()), Name: "other"}))
	if acme.Name != "acme" || acme.ID == uuid.Nil {
		t.Errorf("tenant %+v", acme)
	}
	h := db.WithScope(shopdb.UserScope{TenantID: acme.ID})

	// Insert fills the bound column from the scope and the optional fields
	// left out from their defaults.
	u := must(h.Users.Insert(ctx, shopdb.UserDraft{Email: "a@acme.com", PasswordHash: "x"}))
	if u.TenantID != acme.ID || u.Version != 1 || u.DisplayName != nil {
		t.Errorf("user %+v", u)
	}
	if got := must(h.Users.Get(ctx, u.Key())); got != u {
		t.Errorf("get %+v", got)
	}
	if got := must(h.Users.GetByTenantIDAndEmail(ctx, "a@acme.com")); got.ID != u.ID {
		t.Errorf("get by email %+v", got)
	}
	if n := must(h.Users.Count(ctx)); n != 1 {
		t.Errorf("count %d", n)
	}
	if ok := must(h.Users.Exists(ctx)); !ok {
		t.Error("exists")
	}

	// The scope filters every read.
	o := db.WithScope(shopdb.UserScope{TenantID: other.ID})
	if _, err := o.Users.Get(ctx, u.Key()); dql.ClassOf(err) != dql.ClassNotFound {
		t.Errorf("get in another scope: %v", err)
	}
	if v := must(o.Users.Find(ctx, u.Key())); v.IsSome() {
		t.Error("find in another scope")
	}
	if n := must(o.Users.Count(ctx)); n != 0 {
		t.Errorf("count in another scope %d", n)
	}

	// A write is validated before it reaches the database (§20.3).
	long := make([]byte, 321)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := h.Users.Insert(ctx, shopdb.UserDraft{Email: shopdb.EmailAddress(long), PasswordHash: "x"}); dql.ClassOf(err) != dql.ClassValidationErrors {
		t.Errorf("invalid draft: %v", err)
	}

	// A unique violation names the constraint (§23.2).
	_, err := h.Users.Insert(ctx, shopdb.UserDraft{Email: "a@acme.com", PasswordHash: "y"})
	if !dql.IsViolation(err, dql.Constraint{Name: "users_tenant_email_key"}) {
		t.Errorf("duplicate: %v", err)
	}

	// Patch compares and increments the version (§20.4).
	name := "Ann"
	p := must(h.Users.Patch(ctx, u.Key(), shopdb.UserPartial{DisplayName: opt.Some(&name)}))
	if p.Version != 2 || *p.DisplayName != "Ann" || p.Email != u.Email {
		t.Errorf("patch %+v", p)
	}
	if _, err := h.Users.Patch(ctx, u.Key(), shopdb.UserPartial{DisplayName: opt.Some[*string](nil)}); dql.ClassOf(err) != dql.ClassConcurrentModification {
		t.Errorf("stale patch: %v", err)
	}
	if _, err := h.Users.Patch(ctx, shopdb.UserKey{ID: 999, Version: 1}, shopdb.UserPartial{}); dql.ClassOf(err) != dql.ClassNotFound {
		t.Errorf("patch of no row: %v", err)
	}
	if err := h.Users.Delete(ctx, u.Key()); dql.ClassOf(err) != dql.ClassConcurrentModification {
		t.Errorf("stale delete: %v", err)
	}

	// Upsert by the tenant's email updates the row that conflicts.
	up := must(h.Users.UpsertByTenantIDAndEmail(ctx, shopdb.UserDraft{Email: "a@acme.com", PasswordHash: "z"}))
	if up.ID != u.ID || up.Version != 3 {
		t.Errorf("upsert %+v", up)
	}

	// Across scopes, the scope is an argument (§21.2).
	admin := shopdb.Unscoped(h)
	b := must(admin.Users.Insert(ctx, shopdb.UserScope{TenantID: other.ID}, shopdb.UserDraft{Email: "b@other.com", PasswordHash: "x"}))
	if b.TenantID != other.ID {
		t.Errorf("unscoped insert %+v", b)
	}
	if got := must(admin.Users.Get(ctx, shopdb.UserScope{TenantID: other.ID}, b.Key())); got.ID != b.ID {
		t.Errorf("unscoped get %+v", got)
	}

	// A profile has no version: an empty Partial returns the row as it is,
	// and a missing key is NotFound.
	pr := must(h.Profiles.Insert(ctx, shopdb.ProfileDraft{UserID: up.ID}))
	if got := must(h.Profiles.Patch(ctx, pr.Key(), shopdb.ProfilePartial{})); got != pr {
		t.Errorf("empty patch %+v", got)
	}
	bio := "hi"
	if got := must(h.Profiles.Patch(ctx, pr.Key(), shopdb.ProfilePartial{Bio: opt.Some(&bio)})); *got.Bio != "hi" {
		t.Errorf("patch %+v", got)
	}
	if _, err := h.Profiles.Patch(ctx, shopdb.ProfileKey{UserID: 999}, shopdb.ProfilePartial{Bio: opt.Some(&bio)}); dql.ClassOf(err) != dql.ClassNotFound {
		t.Errorf("patch of no row: %v", err)
	}

	// Posts, tags and their links; composite keys.
	post := must(h.Posts.Insert(ctx, shopdb.PostDraft{AuthorID: up.ID, Title: "Hello world"}))
	if post.Status != shopdb.PostStatusDraft || post.Slug == nil || *post.Slug != "hello-world" {
		t.Errorf("post %+v", post)
	}
	tag := must(h.Tags.Insert(ctx, shopdb.TagDraft{Name: "go"}))
	again := must(h.Tags.UpsertByName(ctx, shopdb.TagDraft{Name: "go"}))
	if again.ID != tag.ID {
		t.Errorf("tag upsert %+v", again)
	}
	link := must(h.PostTags.Insert(ctx, shopdb.PostTagDraft{PostID: post.ID, TagID: tag.ID}))
	if got := must(h.PostTags.Get(ctx, link.Key())); got != link {
		t.Errorf("post tag %+v", got)
	}
	if err := h.PostTags.Delete(ctx, link.Key()); err != nil {
		t.Error(err)
	}
	if v := must(h.PostTags.Find(ctx, link.Key())); v.IsSome() {
		t.Error("deleted link found")
	}

	// A write transaction commits or rolls back as a whole, and a read
	// transaction's stores only read.
	errBoom := errors.New("boom")
	err = h.Write(ctx, func(ctx context.Context, tx shopdb.UserScopedWriteTx) error {
		if _, err := tx.Tags.Insert(ctx, shopdb.TagDraft{Name: "rolled-back"}); err != nil {
			return err
		}
		return errBoom
	})
	if !errors.Is(err, errBoom) {
		t.Error(err)
	}
	if v := must(h.Tags.FindByName(ctx, "rolled-back")); v.IsSome() {
		t.Error("rolled back tag found")
	}
	err = h.Read(ctx, func(ctx context.Context, tx shopdb.UserScopedReadTx) error {
		n, err := tx.Users.Count(ctx)
		if n != 1 {
			t.Errorf("count in read tx %d", n)
		}
		return err
	})
	if err != nil {
		t.Error(err)
	}
	tx := must(db.BeginWrite(ctx))
	must(tx.Tags.Insert(ctx, shopdb.TagDraft{Name: "committed"}))
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if v := must(db.Tags.FindByName(ctx, "committed")); !v.IsSome() {
		t.Error("committed tag missing")
	}

	// Delete by key on a versioned entity compares the version.
	cur := must(h.Users.Get(ctx, u.Key()))
	if err := h.Profiles.Delete(ctx, pr.Key()); err != nil {
		t.Fatal(err)
	}
	if err := h.Posts.Delete(ctx, post.Key()); err != nil {
		t.Fatal(err)
	}
	if err := h.Users.Delete(ctx, cur.Key()); err != nil {
		t.Fatal(err)
	}
	if err := h.Users.Delete(ctx, cur.Key()); dql.ClassOf(err) != dql.ClassNotFound {
		t.Errorf("delete of no row: %v", err)
	}
}
