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

func TestInsertNested(t *testing.T) {
	ctx := context.Background()
	db := shopdb.New(executor(t))
	acme := must(db.Tenants.Insert(ctx, shopdb.TenantDraft{Name: "acme"}))
	h := db.WithScope(shopdb.UserScope{TenantID: acme.ID})

	// The user first, then its dependents with its key; the bound tenant
	// is read back.
	u := must(h.Users.InsertNested(ctx, shopdb.UserDraftNested{
		UserDraft: shopdb.UserDraft{Email: "ann@acme.com", PasswordHash: "x"},
		UserDraftNests: shopdb.UserDraftNests{
			Profile:       opt.Some(shopdb.ProfileDraftForUser{Bio: opt.Some(ptr("Hi"))}),
			PostsToAuthor: []shopdb.PostDraftForAuthor{{Title: "Hello"}, {Title: "Again"}},
		},
	}))
	if u.Tenant != acme || u.Profile == nil || *u.Profile.Bio != "Hi" || u.Profile.UserID != u.ID ||
		len(u.PostsToAuthor) != 2 || u.PostsToAuthor[1].AuthorID != u.ID || len(u.PostsToEditor) != 0 {
		t.Errorf("user %+v", u)
	}
	b := must(shopdb.Unscoped(h).Users.InsertNested(ctx, shopdb.UserScope{TenantID: acme.ID}, shopdb.UserDraftNested{
		UserDraft: shopdb.UserDraft{Email: "bob@acme.com", PasswordHash: "x"},
		UserDraftNests: shopdb.UserDraftNests{
			PostsToEditor: []shopdb.PostDraftForEditor{{AuthorID: u.ID, Title: "Edited"}},
		},
	}, shopdb.Users.CreatedAt.Set(dql.Now())))
	if b.Profile != nil || len(b.PostsToEditor) != 1 || *b.PostsToEditor[0].EditorID != b.ID {
		t.Errorf("editor %+v", b)
	}

	// A failing child rolls the whole graph back.
	if _, err := h.Users.InsertNested(ctx, shopdb.UserDraftNested{
		UserDraft:      shopdb.UserDraft{Email: "cid@acme.com", PasswordHash: "x"},
		UserDraftNests: shopdb.UserDraftNests{PostsToAuthor: []shopdb.PostDraftForAuthor{{ID: opt.Some(u.PostsToAuthor[0].ID), Title: "Dup"}}},
	}); dql.ClassOf(err) != dql.ClassUniqueViolation {
		t.Errorf("duplicate: %v", err)
	}
	if ok := must(shopdb.Select(h.Users, shopdb.Shapes.UserFull).Where(shopdb.Users.Email.Eq("cid@acme.com")).Exists(ctx)); ok {
		t.Error("rolled back user exists")
	}
	if _, err := h.Users.InsertNested(ctx, shopdb.UserDraftNested{UserDraft: shopdb.UserDraft{Email: shopdb.EmailAddress(strings.Repeat("a", 321))}}); err == nil {
		t.Error("invalid graph written")
	}

	// Many-to-many: links and creates write junction rows; junction
	// children read their far rows back.
	p := u.PostsToAuthor[0]
	tag := must(db.Tags.InsertNested(ctx, shopdb.TagDraftNested{
		TagDraft: shopdb.TagDraft{Name: "go"},
		TagDraftNests: shopdb.TagDraftNests{
			PostTags: []shopdb.PostTagDraftForTag{{PostID: p.ID}},
			Posts:    []shopdb.PostLink{shopdb.PostLinkKey(shopdb.PostKey{ID: u.PostsToAuthor[1].ID}), shopdb.PostLinkCreate(shopdb.PostDraft{AuthorID: u.ID, Title: "New"})},
		},
	}))
	if len(tag.PostTags) != 3 || len(tag.Posts) != 3 || tag.Posts[0].ID != p.ID || tag.Posts[2].Title != "New" || tag.PostTags[2].PostID != tag.Posts[2].ID {
		t.Errorf("tag %+v", tag)
	}

	// Owning relations: a Create is inserted first, a Link sets the key.
	pt := must(db.PostTags.InsertNested(ctx, shopdb.PostTagDraftNested{
		Post: opt.Some(shopdb.PostRefLink(shopdb.PostKey{ID: b.PostsToEditor[0].ID})),
		Tag:  opt.Some(shopdb.TagRefCreate(shopdb.TagDraft{Name: "db"})),
	}))
	if pt.Post.ID != b.PostsToEditor[0].ID || pt.Tag.Name != "db" || pt.TagID != pt.Tag.ID {
		t.Errorf("post tag %+v", pt)
	}
	pt = must(db.PostTags.InsertNested(ctx, shopdb.PostTagDraftNested{PostID: opt.Some(p.ID), Tag: opt.Some(shopdb.TagRefLink(shopdb.TagKey{ID: pt.TagID}))}))
	if pt.Post.ID != p.ID || pt.Tag.Name != "db" {
		t.Errorf("by key %+v", pt)
	}
	if _, err := db.PostTags.InsertNested(ctx, shopdb.PostTagDraftNested{PostID: opt.Some(p.ID), Post: opt.Some(shopdb.PostRefLink(shopdb.PostKey{ID: b.PostsToEditor[0].ID})), TagID: opt.Some(pt.TagID)}); err == nil {
		t.Error("conflicting key and relation")
	}
}

// §21.1: a nested write reaching a scoped entity runs on a scoped handle.
func TestInsertNestedScoped(t *testing.T) {
	ctx := context.Background()
	db := shopdb.New(executor(t))
	acme := must(db.Tenants.Insert(ctx, shopdb.TenantDraft{Name: "acme"}))
	other := must(db.Tenants.Insert(ctx, shopdb.TenantDraft{Name: "other"}))
	h := db.WithScope(shopdb.UserScope{TenantID: acme.ID})
	ann := must(h.Users.Insert(ctx, shopdb.UserDraft{Email: "ann@acme.com", PasswordHash: "x"}))
	go_ := must(db.Tags.Insert(ctx, shopdb.TagDraft{Name: "go"}))

	p := must(h.Posts.InsertNested(ctx, shopdb.PostDraftNested{
		Title:    "Hello",
		Author:   opt.Some(shopdb.UserRefLink(ann.Key())),
		Editor:   opt.Some(shopdb.UserRefCreate(shopdb.UserDraft{Email: "ed@acme.com", PasswordHash: "x"})),
		PostTags: []shopdb.PostTagDraftForPost{{TagID: go_.ID}},
		Tags:     []shopdb.TagLink{shopdb.TagLinkCreate(shopdb.TagDraft{Name: "db"})},
	}))
	if p.Author.ID != ann.ID || p.Editor == nil || p.Editor.TenantID != acme.ID || *p.EditorID != p.Editor.ID ||
		len(p.Tags) != 2 || p.Tags[0].ID != go_.ID || p.Tags[1].Name != "db" || len(p.PostTags) != 2 {
		t.Errorf("post %+v", p)
	}
	q := must(h.Posts.InsertNested(ctx, shopdb.PostDraftNested{Title: "Plain", AuthorID: opt.Some(ann.ID)}))
	if q.Author.ID != ann.ID || q.Editor != nil || len(q.Tags) != 0 {
		t.Errorf("plain %+v", q)
	}

	// The scope reads the author back: one in another tenant is not found,
	// and the post is rolled back.
	bob := must(db.WithScope(shopdb.UserScope{TenantID: other.ID}).Users.Insert(ctx, shopdb.UserDraft{Email: "bob@other.com", PasswordHash: "x"}))
	if _, err := h.Posts.InsertNested(ctx, shopdb.PostDraftNested{Title: "Cross", AuthorID: opt.Some(bob.ID)}); dql.ClassOf(err) != dql.ClassNotFound {
		t.Errorf("cross tenant: %v", err)
	}
	if ok := must(shopdb.Select(h.Posts, shopdb.Shapes.PostFull).Where(shopdb.Posts.Title.Eq("Cross")).Exists(ctx)); ok {
		t.Error("cross-tenant post written")
	}

	pr := must(h.Profiles.InsertNested(ctx, shopdb.ProfileDraftNested{User: opt.Some(shopdb.UserRefCreate(shopdb.UserDraft{Email: "cid@acme.com", PasswordHash: "x"}))}))
	if pr.User.Email != "cid@acme.com" || pr.UserID != pr.User.ID {
		t.Errorf("profile %+v", pr)
	}
	err := h.Write(ctx, func(ctx context.Context, tx shopdb.UserScopedWriteTx) error {
		_, err := tx.Profiles.InsertNested(ctx, shopdb.ProfileDraftNested{UserID: opt.Some(ann.ID)})
		return err
	})
	if err != nil {
		t.Error(err)
	}
}

func TestPatchNested(t *testing.T) {
	ctx := context.Background()
	db := shopdb.New(executor(t))
	acme := must(db.Tenants.Insert(ctx, shopdb.TenantDraft{Name: "acme"}))
	h := db.WithScope(shopdb.UserScope{TenantID: acme.ID})
	u := must(h.Users.InsertNested(ctx, shopdb.UserDraftNested{
		UserDraft: shopdb.UserDraft{Email: "ann@acme.com", PasswordHash: "x"},
		UserDraftNests: shopdb.UserDraftNests{
			Profile:       opt.Some(shopdb.ProfileDraftForUser{Bio: opt.Some(ptr("Hi"))}),
			PostsToAuthor: []shopdb.PostDraftForAuthor{{Title: "One"}, {Title: "Two"}, {Title: "Three"}},
		},
	}))
	ed := must(h.Users.Insert(ctx, shopdb.UserDraft{Email: "ed@acme.com", PasswordHash: "x"}))
	edited := must(h.Posts.Insert(ctx, shopdb.PostDraft{AuthorID: u.ID, EditorID: opt.Some(ptr(ed.ID)), Title: "Edited"}))
	one, two := u.PostsToAuthor[0], u.PostsToAuthor[1]

	// posts_to_author is replace + delete: the post the changes leave out is
	// deleted. posts_to_editor is merge + unlink: Delete clears editor_id.
	got := must(h.Users.PatchNested(ctx, u.Key(), shopdb.UserPartialNested{
		UserPartial: shopdb.UserPartial{DisplayName: opt.Some(ptr("Ann"))},
		UserPartialNests: shopdb.UserPartialNests{
			PostsToAuthor: opt.Some([]shopdb.PostChangeForAuthor{
				shopdb.PostUpdateForAuthor(one.Key(), shopdb.PostPartialForAuthor{Title: opt.Some("First")}),
				shopdb.PostDeleteForAuthor(two.Key()),
				shopdb.PostCreateForAuthor(shopdb.PostDraftForAuthor{Title: "Four"}),
			}),
			Profile: opt.Some(shopdb.ProfileUpdateForUser(shopdb.ProfileKey{UserID: u.ID}, shopdb.ProfilePartialForUser{Bio: opt.Some[*string](nil)})),
		},
	}))
	// The edited post is u's too, and replace keeps only the changed ones.
	if *got.DisplayName != "Ann" || got.Version != u.Version+1 || len(got.PostsToAuthor) != 2 || got.PostsToAuthor[0].Title != "First" ||
		got.PostsToAuthor[1].Title != "Four" || got.Profile == nil || got.Profile.Bio != nil || got.Tenant != acme {
		t.Errorf("patched %+v", got)
	}
	if _, err := h.Posts.Get(ctx, edited.Key()); dql.ClassOf(err) != dql.ClassNotFound {
		t.Errorf("replaced post: %v", err)
	}
	e := must(h.Users.PatchNested(ctx, ed.Key(), shopdb.UserPartialNested{UserPartialNests: shopdb.UserPartialNests{
		PostsToEditor: opt.Some([]shopdb.PostChangeForEditor{shopdb.PostCreateForEditor(shopdb.PostDraftForEditor{AuthorID: u.ID, Title: "Draft"})}),
		Profile:       opt.Some(shopdb.ProfileCreateForUser(shopdb.ProfileDraftForUser{})),
	}}))
	if len(e.PostsToEditor) != 1 || e.Profile == nil {
		t.Errorf("editor %+v", e)
	}
	e = must(shopdb.Unscoped(h).Users.PatchNested(ctx, shopdb.UserScope{TenantID: acme.ID}, e.Key(), shopdb.UserPartialNested{UserPartialNests: shopdb.UserPartialNests{
		PostsToEditor: opt.Some([]shopdb.PostChangeForEditor{
			shopdb.PostUpdateForEditor(e.PostsToEditor[0].Key(), shopdb.PostPartialForEditor{Title: opt.Some("Edit")}),
			shopdb.PostDeleteForEditor(e.PostsToEditor[0].Key()),
		}),
		Profile: opt.Some(shopdb.ProfileDeleteForUser(shopdb.ProfileKey{UserID: ed.ID})),
	}}))
	if len(e.PostsToEditor) != 0 || e.Profile != nil {
		t.Errorf("unlinked %+v", e)
	}

	// A change naming another parent's child is NotFound, and nothing of
	// the patch stays.
	_, err := h.Users.PatchNested(ctx, e.Key(), shopdb.UserPartialNested{
		UserPartial:      shopdb.UserPartial{DisplayName: opt.Some(ptr("Ed"))},
		UserPartialNests: shopdb.UserPartialNests{PostsToAuthor: opt.Some([]shopdb.PostChangeForAuthor{shopdb.PostUpdateForAuthor(one.Key(), shopdb.PostPartialForAuthor{})})},
	})
	if dql.ClassOf(err) != dql.ClassNotFound {
		t.Errorf("another's child: %v", err)
	}
	if v := must(h.Users.Get(ctx, e.Key())); v.DisplayName != nil {
		t.Errorf("rolled back %+v", v)
	}
	for _, p := range []shopdb.UserPartialNested{
		{UserPartialNests: shopdb.UserPartialNests{PostsToEditor: opt.Some([]shopdb.PostChangeForEditor{shopdb.PostDeleteForEditor(one.Key())})}},
		{UserPartialNests: shopdb.UserPartialNests{PostsToEditor: opt.Some([]shopdb.PostChangeForEditor{shopdb.PostUpdateForEditor(one.Key(), shopdb.PostPartialForEditor{})})}},
		{UserPartialNests: shopdb.UserPartialNests{Profile: opt.Some(shopdb.ProfileUpdateForUser(shopdb.ProfileKey{UserID: u.ID}, shopdb.ProfilePartialForUser{}))}},
		{UserPartialNests: shopdb.UserPartialNests{Profile: opt.Some(shopdb.ProfileDeleteForUser(shopdb.ProfileKey{UserID: u.ID}))}},
		{UserPartialNests: shopdb.UserPartialNests{PostsToAuthor: opt.Some([]shopdb.PostChangeForAuthor{shopdb.PostDeleteForAuthor(one.Key())})}},
	} {
		if _, err := h.Users.PatchNested(ctx, e.Key(), p); dql.ClassOf(err) != dql.ClassNotFound {
			t.Errorf("another's child %+v: %v", p, err)
		}
	}
	if _, err := h.Users.PatchNested(ctx, e.Key(), shopdb.UserPartialNested{UserPartial: shopdb.UserPartial{Email: opt.Some(shopdb.EmailAddress(strings.Repeat("a", 321)))}}); err == nil {
		t.Error("invalid partial written")
	}
}

func TestPatchNestedLinks(t *testing.T) {
	ctx := context.Background()
	db := shopdb.New(executor(t))
	acme := must(db.Tenants.Insert(ctx, shopdb.TenantDraft{Name: "acme"}))
	h := db.WithScope(shopdb.UserScope{TenantID: acme.ID})
	u := must(h.Users.Insert(ctx, shopdb.UserDraft{Email: "ann@acme.com", PasswordHash: "x"}))
	one := must(h.Posts.Insert(ctx, shopdb.PostDraft{AuthorID: u.ID, Title: "One"}))
	two := must(h.Posts.Insert(ctx, shopdb.PostDraft{AuthorID: u.ID, Title: "Two"}))
	other := must(db.Tags.Insert(ctx, shopdb.TagDraft{Name: "other"}))
	must(db.PostTags.Insert(ctx, shopdb.PostTagDraft{PostID: one.ID, TagID: other.ID}))

	// Tags: posts links through post_tags; post_tags changes its rows.
	tag := must(db.Tags.Insert(ctx, shopdb.TagDraft{Name: "go"}))
	got := must(db.Tags.PatchNested(ctx, tag.Key(), shopdb.TagPartialNested{
		TagPartial: shopdb.TagPartial{Name: opt.Some(shopdb.SlugText("golang"))},
		TagPartialNests: shopdb.TagPartialNests{Posts: opt.Some([]shopdb.TagPostsChange{
			shopdb.TagPostsLink(one.Key()),
			shopdb.TagPostsLink(one.Key()),
			shopdb.TagPostsCreate(shopdb.PostDraft{AuthorID: u.ID, Title: "Three"}),
		})},
	}))
	if got.Name != "golang" || len(got.Posts) != 2 || got.Posts[0].ID != one.ID || got.Posts[1].Title != "Three" || len(got.PostTags) != 2 {
		t.Errorf("linked %+v", got)
	}
	got = must(db.Tags.PatchNested(ctx, tag.Key(), shopdb.TagPartialNested{TagPartialNests: shopdb.TagPartialNests{
		Posts: opt.Some([]shopdb.TagPostsChange{shopdb.TagPostsUnlink(one.Key())}),
		PostTags: opt.Some([]shopdb.PostTagChangeForTag{
			shopdb.PostTagCreateForTag(shopdb.PostTagDraftForTag{PostID: two.ID}),
			shopdb.PostTagUpdateForTag(shopdb.PostTagKey{PostID: two.ID, TagID: tag.ID}, shopdb.PostTagPartialForTag{}),
		}),
	}}))
	if len(got.Posts) != 2 || got.Posts[0].ID != two.ID || got.Posts[1].Title != "Three" {
		t.Errorf("unlinked %+v", got)
	}
	got = must(db.Tags.PatchNested(ctx, tag.Key(), shopdb.TagPartialNested{TagPartialNests: shopdb.TagPartialNests{
		PostTags: opt.Some([]shopdb.PostTagChangeForTag{shopdb.PostTagDeleteForTag(shopdb.PostTagKey{PostID: two.ID, TagID: tag.ID})}),
	}}))
	if len(got.Posts) != 1 || got.Posts[0].Title != "Three" {
		t.Errorf("deleted %+v", got)
	}
	for _, ch := range []shopdb.PostTagChangeForTag{
		shopdb.PostTagUpdateForTag(shopdb.PostTagKey{PostID: one.ID, TagID: other.ID}, shopdb.PostTagPartialForTag{}),
		shopdb.PostTagDeleteForTag(shopdb.PostTagKey{PostID: one.ID, TagID: other.ID}),
		shopdb.PostTagDeleteForTag(shopdb.PostTagKey{PostID: two.ID, TagID: tag.ID}),
	} {
		_, err := db.Tags.PatchNested(ctx, tag.Key(), shopdb.TagPartialNested{TagPartialNests: shopdb.TagPartialNests{PostTags: opt.Some([]shopdb.PostTagChangeForTag{ch})}})
		if dql.ClassOf(err) != dql.ClassNotFound {
			t.Errorf("another's post_tag %+v: %v", ch, err)
		}
	}

	// Scoped posts: owning changes go first, through users in scope.
	ed := must(h.Users.Insert(ctx, shopdb.UserDraft{Email: "ed@acme.com", PasswordHash: "x"}))
	p := must(h.Posts.PatchNested(ctx, one.Key(), shopdb.PostPartialNested{
		PostPartial: shopdb.PostPartial{Title: opt.Some("First")},
		PostPartialNests: shopdb.PostPartialNests{
			Author: opt.Some(shopdb.PostAuthorUpdate(shopdb.UserPartial{DisplayName: opt.Some(ptr("Ann"))})),
			Editor: opt.Some(shopdb.PostEditorCreate(shopdb.UserDraft{Email: "eve@acme.com", PasswordHash: "x"})),
			Tags:   opt.Some([]shopdb.PostTagsChange{shopdb.PostTagsLink(tag.Key()), shopdb.PostTagsCreate(shopdb.TagDraft{Name: "sql"}), shopdb.PostTagsUnlink(other.Key())}),
		},
	}))
	if p.Title != "First" || p.Author.DisplayName == nil || *p.Author.DisplayName != "Ann" || p.Editor == nil || p.Editor.Email != "eve@acme.com" ||
		len(p.Tags) != 2 || p.Tags[0].ID != tag.ID || p.Tags[1].Name != "sql" {
		t.Errorf("patched post %+v", p)
	}
	p = must(h.Posts.PatchNested(ctx, one.Key(), shopdb.PostPartialNested{PostPartialNests: shopdb.PostPartialNests{
		Author: opt.Some(shopdb.PostAuthorLink(ed.Key())),
		Editor: opt.Some(shopdb.PostEditorUpdate(shopdb.UserPartial{DisplayName: opt.Some(ptr("Eve"))})),
		PostTags: opt.Some([]shopdb.PostTagChangeForPost{
			shopdb.PostTagDeleteForPost(shopdb.PostTagKey{PostID: one.ID, TagID: tag.ID}),
			shopdb.PostTagCreateForPost(shopdb.PostTagDraftForPost{TagID: other.ID}),
			shopdb.PostTagUpdateForPost(shopdb.PostTagKey{PostID: one.ID, TagID: other.ID}, shopdb.PostTagPartialForPost{}),
		}),
	}}))
	if p.Author.ID != ed.ID || p.Editor == nil || *p.Editor.DisplayName != "Eve" || len(p.Tags) != 2 || p.Tags[0].ID != other.ID {
		t.Errorf("relinked post %+v", p)
	}
	p = must(h.Posts.PatchNested(ctx, one.Key(), shopdb.PostPartialNested{PostPartialNests: shopdb.PostPartialNests{
		Author: opt.Some(shopdb.PostAuthorCreate(shopdb.UserDraft{Email: "new@acme.com", PasswordHash: "x"})),
		Editor: opt.Some(shopdb.PostEditorUnlink()),
	}}))
	if p.Author.Email != "new@acme.com" || p.Editor != nil {
		t.Errorf("unlinked editor %+v", p)
	}
	p = must(h.Posts.PatchNested(ctx, one.Key(), shopdb.PostPartialNested{PostPartialNests: shopdb.PostPartialNests{
		Editor: opt.Some(shopdb.PostEditorLink(u.Key())),
	}}))
	if p.Editor == nil || p.Editor.ID != u.ID {
		t.Errorf("linked editor %+v", p)
	}
	for _, c := range []shopdb.PostPartialNested{
		{PostPartialNests: shopdb.PostPartialNests{PostTags: opt.Some([]shopdb.PostTagChangeForPost{shopdb.PostTagDeleteForPost(shopdb.PostTagKey{PostID: two.ID, TagID: tag.ID})})}},
		{PostPartialNests: shopdb.PostPartialNests{PostTags: opt.Some([]shopdb.PostTagChangeForPost{shopdb.PostTagUpdateForPost(shopdb.PostTagKey{PostID: two.ID, TagID: tag.ID}, shopdb.PostTagPartialForPost{})})}},
	} {
		if _, err := h.Posts.PatchNested(ctx, two.Key(), c); dql.ClassOf(err) != dql.ClassNotFound {
			t.Errorf("missing %+v: %v", c, err)
		}
	}
	if _, err := h.Posts.PatchNested(ctx, two.Key(), shopdb.PostPartialNested{PostPartialNests: shopdb.PostPartialNests{
		Editor: opt.Some(shopdb.PostEditorUpdate(shopdb.UserPartial{})),
	}}); dql.ClassOf(err) != dql.ClassNotFound {
		t.Errorf("no editor to update: %v", err)
	}

	// Scoped profiles: the owning user's update.
	must(h.Profiles.Insert(ctx, shopdb.ProfileDraft{UserID: u.ID}))
	pr := must(h.Profiles.PatchNested(ctx, shopdb.ProfileKey{UserID: u.ID}, shopdb.ProfilePartialNested{
		ProfilePartial:      shopdb.ProfilePartial{Bio: opt.Some(ptr("Hi"))},
		ProfilePartialNests: shopdb.ProfilePartialNests{User: opt.Some(shopdb.ProfileUserUpdate(shopdb.UserPartial{DisplayName: opt.Some(ptr("Annie"))}))},
	}))
	if *pr.Bio != "Hi" || *pr.User.DisplayName != "Annie" {
		t.Errorf("patched profile %+v", pr)
	}
}
