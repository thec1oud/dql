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
	"strings"
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

func TestSelect(t *testing.T) {
	ctx := context.Background()
	db := shopdb.New(executor(t))
	acme := must(db.Tenants.Insert(ctx, shopdb.TenantDraft{Name: "acme"}))
	other := must(db.Tenants.Insert(ctx, shopdb.TenantDraft{Name: "other"}))
	h := db.WithScope(shopdb.UserScope{TenantID: acme.ID})
	o := db.WithScope(shopdb.UserScope{TenantID: other.ID})
	ann := "Ann"
	a := must(h.Users.Insert(ctx, shopdb.UserDraft{Email: "ann@acme.com", PasswordHash: "x", DisplayName: opt.Some(&ann)}))
	b := must(h.Users.Insert(ctx, shopdb.UserDraft{Email: "bob@acme.com", PasswordHash: "x"}))
	must(o.Users.Insert(ctx, shopdb.UserDraft{Email: "ann@other.com", PasswordHash: "x"}))
	labels := []*string{&ann}
	p := must(h.Posts.Insert(ctx, shopdb.PostDraft{AuthorID: a.ID, Title: "Hello", Labels: opt.Some(&labels)}))

	// The scope filters every read; Where, OrderBy, Limit and Offset compose.
	users := shopdb.Select(h.Users, shopdb.Shapes.UserFull)
	all := must(users.OrderBy(shopdb.Users.Email.Desc()).All(ctx))
	if len(all) != 2 || all[0].ID != b.ID || all[1].ID != a.ID {
		t.Errorf("all %+v", all)
	}
	if got := must(users.Where(shopdb.Users.Email.ILike("ANN%")).One(ctx)); got.ID != a.ID {
		t.Errorf("ilike %+v", got)
	}
	if got := must(users.OrderBy(shopdb.Users.ID.Asc()).Limit(1).Offset(1).All(ctx)); len(got) != 1 || got[0].ID != b.ID {
		t.Errorf("page %+v", got)
	}
	if n := must(users.Where(dql.Or(shopdb.Users.DisplayName.IsNull(), shopdb.Users.ID.In([]int64{a.ID}))).Count(ctx)); n != 2 {
		t.Errorf("count %d", n)
	}
	if v := must(users.Where(dql.Not(shopdb.Users.Email.Eq("ann@acme.com")), shopdb.Users.DisplayName.IsNotNull()).OptionalOne(ctx)); v.IsSome() {
		t.Errorf("optional %v", v)
	}
	if _, err := users.One(ctx); dql.ClassOf(err) != dql.ClassTooManyRows {
		t.Errorf("one of two: %v", err)
	}

	// Across scopes, every tenant's rows.
	if n := must(shopdb.Select(shopdb.Unscoped(h).Users, shopdb.Shapes.UserFull).Where(shopdb.Users.Email.StartsWith("ann@")).Count(ctx)); n != 2 {
		t.Errorf("unscoped count %d", n)
	}

	// Relation predicates.
	if got := must(users.Where(shopdb.Users.PostsToAuthor.Any(shopdb.Posts.Title.Eq("Hello"))).All(ctx)); len(got) != 1 || got[0].ID != a.ID {
		t.Errorf("any %+v", got)
	}
	if got := must(users.Where(shopdb.Users.PostsToAuthor.None()).All(ctx)); len(got) != 1 || got[0].ID != b.ID {
		t.Errorf("none %+v", got)
	}
	if n := must(users.Where(shopdb.Users.PostsToAuthor.All(shopdb.Posts.Title.Eq("Hello"))).Count(ctx)); n != 2 {
		t.Errorf("all %d", n)
	}
	posts := shopdb.Select(h.Posts, shopdb.Shapes.PostFull)
	if ok := must(posts.Where(shopdb.Posts.Labels.ArrayContains(&ann), shopdb.Posts.Author.Any(shopdb.Users.Email.Contains("acme"))).Exists(ctx)); !ok {
		t.Error("exists")
	}

	// Distinct, locks and Iter.
	if got := must(shopdb.Select(h.Tenants, shopdb.Shapes.TenantFull).Distinct().All(ctx)); len(got) != 2 {
		t.Errorf("distinct %+v", got)
	}
	err := h.Write(ctx, func(ctx context.Context, tx shopdb.UserScopedWriteTx) error {
		got, err := shopdb.Select(tx.Posts, shopdb.Shapes.PostFull).Where(shopdb.Posts.ID.Eq(p.ID)).ForUpdate().NoWait().One(ctx)
		if got.ID != p.ID {
			t.Errorf("locked %+v", got)
		}
		return err
	})
	if err != nil {
		t.Error(err)
	}
	n := 0
	for u, err := range users.NoLog().Consistency(dql.Primary).Iter(ctx) {
		if err != nil || u.TenantID != acme.ID {
			t.Error(u, err)
		}
		n++
	}
	if n != 2 {
		t.Errorf("iter %d", n)
	}
	if _, err := shopdb.Select(h.Users, shopdb.Shapes.UserFull).ForShare().SkipLocked().All(ctx); err != nil {
		t.Error(err)
	}
}

func TestQueries(t *testing.T) {
	ctx := context.Background()
	db := shopdb.New(executor(t))
	acme := must(db.Tenants.Insert(ctx, shopdb.TenantDraft{Name: "acme"}))
	other := must(db.Tenants.Insert(ctx, shopdb.TenantDraft{Name: "other"}))
	h := db.WithScope(shopdb.UserScope{TenantID: acme.ID})
	a := must(h.Users.Insert(ctx, shopdb.UserDraft{Email: "ann@acme.com", PasswordHash: "x"}))
	must(h.Posts.Insert(ctx, shopdb.PostDraft{AuthorID: a.ID, Title: "One"}))
	must(h.Posts.Insert(ctx, shopdb.PostDraft{AuthorID: a.ID, Title: "Two"}))

	// The handle's scope fills @scope.tenant_id.
	if got := must(h.Queries.GetUserByEmail(ctx, "ann@acme.com")); got != a {
		t.Errorf("get %+v", got)
	}
	if _, err := db.WithScope(shopdb.UserScope{TenantID: other.ID}).Queries.GetUserByEmail(ctx, "ann@acme.com"); dql.ClassOf(err) != dql.ClassNotFound {
		t.Errorf("another scope: %v", err)
	}
	if got := must(h.Queries.AuthorPostCounts(ctx)); len(got) != 1 || got[0].AuthorID != a.ID || got[0].DisplayName != nil || got[0].Posts != 2 {
		t.Errorf("counts %+v", got)
	}
	name := "Ann"
	if n := must(h.Queries.RenameUser(ctx, &name, a.ID, a.Version)); n != 1 {
		t.Errorf("rename %d", n)
	}

	// Across scopes the scope is an argument; a read transaction reads.
	admin := shopdb.Unscoped(h)
	if n := must(admin.Queries.RenameUser(ctx, shopdb.UserScope{TenantID: acme.ID}, nil, a.ID, a.Version)); n != 0 {
		t.Errorf("stale rename %d", n)
	}
	err := admin.Read(ctx, func(ctx context.Context, tx shopdb.UnscopedReadTx) error {
		u, err := tx.Queries.GetUserByEmail(ctx, shopdb.UserScope{TenantID: acme.ID}, "ann@acme.com")
		if u.DisplayName == nil || *u.DisplayName != "Ann" || u.Version != a.Version+1 {
			t.Errorf("renamed %+v", u)
		}
		return err
	})
	if err != nil {
		t.Error(err)
	}
}

func TestFilteredWrites(t *testing.T) {
	ctx := context.Background()
	db := shopdb.New(executor(t))
	acme := must(db.Tenants.Insert(ctx, shopdb.TenantDraft{Name: "acme"}))
	other := must(db.Tenants.Insert(ctx, shopdb.TenantDraft{Name: "other"}))
	h := db.WithScope(shopdb.UserScope{TenantID: acme.ID})
	o := db.WithScope(shopdb.UserScope{TenantID: other.ID})

	// InsertMany inserts every draft, in order, or none of them.
	us := must(h.Users.InsertMany(ctx, []shopdb.UserDraft{{Email: "ann@acme.com", PasswordHash: "x"}, {Email: "bob@acme.com", PasswordHash: "x"}}))
	if len(us) != 2 || us[0].Email != "ann@acme.com" || us[1].Email != "bob@acme.com" || us[1].TenantID != acme.ID {
		t.Errorf("many %+v", us)
	}
	if _, err := h.Users.InsertMany(ctx, []shopdb.UserDraft{{Email: "cid@acme.com", PasswordHash: "x"}, {Email: "ann@acme.com", PasswordHash: "x"}}); dql.ClassOf(err) != dql.ClassUniqueViolation {
		t.Errorf("duplicate: %v", err)
	}
	long := shopdb.EmailAddress(strings.Repeat("a", 321))
	if _, err := h.Users.InsertMany(ctx, []shopdb.UserDraft{{Email: "dan@acme.com", PasswordHash: "x"}, {Email: long, PasswordHash: "x"}}); err == nil {
		t.Error("invalid draft inserted")
	}
	if got := must(h.Users.InsertMany(ctx, nil)); len(got) != 0 {
		t.Errorf("none %+v", got)
	}
	must(o.Users.Insert(ctx, shopdb.UserDraft{Email: "ann@other.com", PasswordHash: "x"}))

	// InsertOrIgnore returns None for a row its uniqueness fact already has.
	if v := must(h.Users.InsertOrIgnoreByTenantIDAndEmail(ctx, shopdb.UserDraft{Email: "ann@acme.com", PasswordHash: "y"})); v.IsSome() {
		t.Errorf("ignored %v", v)
	}
	if v := must(h.Users.InsertOrIgnoreByTenantIDAndEmail(ctx, shopdb.UserDraft{Email: "cid@acme.com", PasswordHash: "y"})); !v.IsSome() {
		t.Error("not inserted")
	}

	// UpdateWhere writes the rows the filter keeps, in scope, and bumps
	// their versions.
	name := "Ann"
	if n := must(h.Users.UpdateWhere(ctx, shopdb.Users.Email.StartsWith("ann@"), shopdb.UserPartial{DisplayName: opt.Some(&name)})); n != 1 {
		t.Errorf("update %d", n)
	}
	if u := must(h.Users.Get(ctx, shopdb.UserKey{ID: us[0].ID, Version: us[0].Version + 1})); u.DisplayName == nil || *u.DisplayName != "Ann" {
		t.Errorf("updated %+v", u)
	}
	if _, err := h.Users.UpdateWhere(ctx, dql.Pred[shopdb.UsersTable]{}, shopdb.UserPartial{}); err == nil {
		t.Error("update without a filter")
	}
	if _, err := h.Users.UpdateWhere(ctx, shopdb.Users.AllRows(), shopdb.UserPartial{Email: opt.Some(long)}); err == nil {
		t.Error("invalid partial written")
	}
	bio := "hi"
	must(h.Profiles.Insert(ctx, shopdb.ProfileDraft{UserID: us[0].ID}))
	if n := must(h.Profiles.UpdateWhere(ctx, shopdb.Profiles.AllRows(), shopdb.ProfilePartial{})); n != 0 {
		t.Errorf("empty update %d", n)
	}
	if n := must(h.Profiles.UpdateWhere(ctx, shopdb.Profiles.AllRows(), shopdb.ProfilePartial{Bio: opt.Some(&bio)})); n != 1 {
		t.Errorf("profile update %d", n)
	}

	// DeleteWhere deletes in scope; AllRows deletes every row of it.
	if n := must(h.Users.DeleteWhere(ctx, shopdb.Users.Email.Eq("cid@acme.com"))); n != 1 {
		t.Errorf("delete %d", n)
	}
	if _, err := h.Users.DeleteWhere(ctx, dql.Pred[shopdb.UsersTable]{}); err == nil {
		t.Error("delete without a filter")
	}
	must(h.Profiles.DeleteWhere(ctx, shopdb.Profiles.AllRows()))
	if n := must(shopdb.Unscoped(h).Users.DeleteWhere(ctx, shopdb.UserScope{TenantID: acme.ID}, shopdb.Users.AllRows())); n != 2 {
		t.Errorf("delete all %d", n)
	}
	if n := must(o.Users.Count(ctx)); n != 1 {
		t.Errorf("other tenant %d", n)
	}
}

func TestExpressions(t *testing.T) {
	ctx := context.Background()
	db := shopdb.New(executor(t))
	acme := must(db.Tenants.Insert(ctx, shopdb.TenantDraft{Name: "acme"}))
	h := db.WithScope(shopdb.UserScope{TenantID: acme.ID})
	a := must(h.Users.Insert(ctx, shopdb.UserDraft{Email: "ann@acme.com", PasswordHash: "x"}))
	b := must(h.Users.Insert(ctx, shopdb.UserDraft{Email: "bob@acme.com", PasswordHash: "x"}))

	// An expression travels on the operation, in place of a value.
	p := must(h.Posts.Insert(ctx, shopdb.PostDraft{AuthorID: a.ID, Title: "Hello", Status: opt.Some(shopdb.PostStatusPublished)}, shopdb.Posts.PublishedAt.Set(dql.Now())))
	if p.PublishedAt == nil {
		t.Error("published_at not set")
	}
	ps := must(h.Posts.InsertMany(ctx, []shopdb.PostDraft{{AuthorID: a.ID, Title: "One", Status: opt.Some(shopdb.PostStatusPublished)}, {AuthorID: a.ID, Title: "Two", Status: opt.Some(shopdb.PostStatusPublished)}}, shopdb.Posts.PublishedAt.Set(dql.Now())))
	if ps[0].PublishedAt == nil || ps[1].PublishedAt == nil {
		t.Errorf("many %+v", ps)
	}
	p = must(h.Posts.Patch(ctx, p.Key(), shopdb.PostPartial{}, shopdb.Posts.PublishedAt.Set(dql.Default()), shopdb.Posts.AuthorID.Add(b.ID-a.ID)))
	if p.PublishedAt != nil || p.AuthorID != b.ID {
		t.Errorf("patched %+v", p)
	}
	if n := must(h.Posts.UpdateWhere(ctx, shopdb.Posts.AuthorID.Eq(b.ID), shopdb.PostPartial{}, shopdb.Posts.AuthorID.Sub(b.ID-a.ID))); n != 1 {
		t.Errorf("update %d", n)
	}
	must(h.Profiles.Insert(ctx, shopdb.ProfileDraft{UserID: a.ID, Bio: opt.Some(ptr("hi"))}))
	if n := must(h.Profiles.UpdateWhere(ctx, shopdb.Profiles.AllRows(), shopdb.ProfilePartial{}, shopdb.Profiles.Bio.Set(dql.Default()))); n != 1 {
		t.Errorf("profile update %d", n)
	}
	if got := must(h.Profiles.Get(ctx, shopdb.ProfileKey{UserID: a.ID})); got.Bio != nil {
		t.Errorf("bio %v", *got.Bio)
	}
	u := must(shopdb.Unscoped(h).Users.Patch(ctx, shopdb.UserScope{TenantID: acme.ID}, a.Key(), shopdb.UserPartial{}, shopdb.Users.DisplayName.Set(dql.Default())))
	if u.Version != a.Version+1 {
		t.Errorf("unscoped %+v", u)
	}

	// A field the payload sets too is FieldSetTwice; a column the write does
	// not take is an error.
	var v dql.ValidationErrors
	_, err := h.Posts.Patch(ctx, p.Key(), shopdb.PostPartial{Title: opt.Some("Hi")}, shopdb.Posts.Title.Set(dql.Default()))
	if !errors.As(err, &v) || v[0].Kind != dql.KindFieldSetTwice || v[0].Field != "title" {
		t.Errorf("twice: %v", err)
	}
	if _, err := h.Users.Patch(ctx, u.Key(), shopdb.UserPartial{}, shopdb.Users.CreatedAt.Set(dql.Now())); err == nil {
		t.Error("created_at patched")
	}
	if _, err := h.Posts.InsertMany(ctx, []shopdb.PostDraft{{AuthorID: a.ID, Title: "Three"}}, shopdb.Posts.Title.Set(dql.Default())); !errors.As(err, &v) {
		t.Errorf("many twice: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }
