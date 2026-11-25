// This file is hand-written (§26.5): it runs the generated package against
// the running example's schema on the Postgres server DQL_TEST_POSTGRES_URL
// names, in a fresh database. The ts emitter's tests run it with Bun.

import { afterAll, beforeAll, expect, test } from "bun:test";
import { PgDriver } from "@dql/pg";
import { Consistency, DEFAULT, type Executor, type Pred, ValidationError, type ValidationIssue, classOf, isViolation, not, now, or, parseUuid } from "@dql/runtime";
import {
  Db,
  type EmailAddress,
  type UnscopedDbReadTx,
  type UserScopedReadTx,
  type UserScopedWriteTx,
  UserFull,
  newExecutor,
  parseEmailAddress,
  parseSlugText,
  PostFull,
  type PostsTable,
  posts,
  profiles,
  shapes,
  unscoped,
  users,
  type UsersTable,
} from "./index.dql.ts";

const base = process.env["DQL_TEST_POSTGRES_URL"] ?? "";
let admin: Executor | undefined;
const made: { name: string; exec: Executor }[] = [];

function config(dsn: string) {
  return { sources: { main: { primary: { dsn, maxConns: 4 } } }, catalogs: { shop: "main" } };
}

beforeAll(async () => {
  if (base === "") {
    throw new Error("DQL_TEST_POSTGRES_URL is not set");
  }
  admin = await newExecutor(new PgDriver(), config(base));
});

afterAll(async () => {
  for (const { name, exec } of made) {
    await exec.close();
    await new Db(admin as Executor).raw("main").exec(`DROP DATABASE ${name} WITH (FORCE)`);
  }
  await admin?.close();
});

/** database returns an executor on a fresh database holding testdata/shop/schema.sql, dropped when the tests end. */
async function database(): Promise<Executor> {
  const name = `shopdb_test_${process.pid}_${made.length}_${Date.now()}`;
  await new Db(admin as Executor).raw("main").exec(`CREATE DATABASE ${name} TEMPLATE template0 ENCODING 'UTF8' LOCALE 'C'`);
  const u = new URL(base);
  u.pathname = `/${name}`;
  const exec = await newExecutor(new PgDriver(), config(u.toString()));
  made.push({ name, exec });
  // Raw statements run one at a time; the schema's statements end their lines.
  const schema = await Bun.file(new URL("../../../../testdata/shop/schema.sql", import.meta.url)).text();
  for (const s of schema.split(/;\s*$/m)) {
    if (s.trim() !== "") {
      await new Db(exec).raw("main").exec(s);
    }
  }
  return exec;
}

/** failure returns the class of the error p rejects with. */
async function failure(p: Promise<unknown>): Promise<string | undefined> {
  try {
    await p;
  } catch (e) {
    return classOf(e);
  }
  return "no error";
}

test("stores", async () => {
  const db = new Db(await database());

  const acme = await db.tenants.insert({ name: "acme" });
  const other = await db.tenants.insert({ id: parseUuid(crypto.randomUUID()), name: "other" });
  expect(acme.name).toBe("acme");
  const h = db.withScope({ tenantId: acme.id });

  // Insert fills the bound column from the scope and the optional fields
  // left out from their defaults.
  const u = await h.users.insert({ email: parseEmailAddress("a@acme.com"), passwordHash: "x" });
  expect([u.tenantId, u.version, u.displayName]).toEqual([acme.id, 1, null]);
  expect(await h.users.get(UserFull.key(u))).toEqual(u);
  expect((await h.users.getByTenantIdAndEmail(parseEmailAddress("a@acme.com"))).id).toBe(u.id);
  expect(await h.users.count()).toBe(1n);
  expect(await h.users.exists()).toBe(true);

  // The scope filters every read.
  const o = db.withScope({ tenantId: other.id });
  expect(await failure(o.users.get(UserFull.key(u)))).toBe("NotFound");
  expect(await o.users.find(UserFull.key(u))).toBeUndefined();
  expect(await o.users.count()).toBe(0n);

  // A write is validated before it reaches the database (§20.3).
  expect(await failure(h.users.insert({ email: "a".repeat(321) as EmailAddress, passwordHash: "x" }))).toBe("ValidationErrors");

  // A unique violation names the constraint (§23.2).
  const dup = await h.users.insert({ email: parseEmailAddress("a@acme.com"), passwordHash: "y" }).catch((e: unknown) => e);
  expect(isViolation(dup, { name: "users_tenant_email_key", kind: "unique", sensitive: false })).toBe(true);

  // Patch compares and increments the version (§20.4).
  const p = await h.users.patch(UserFull.key(u), { displayName: "Ann" });
  expect([p.version, p.displayName, p.email]).toEqual([2, "Ann", u.email]);
  expect(await failure(h.users.patch(UserFull.key(u), { displayName: null }))).toBe("ConcurrentModification");
  expect(await failure(h.users.patch({ id: 999n, version: 1 }, {}))).toBe("NotFound");
  expect(await failure(h.users.delete(UserFull.key(u)))).toBe("ConcurrentModification");

  // Upsert by the tenant's email updates the row that conflicts.
  const up = await h.users.upsertByTenantIdAndEmail({ email: parseEmailAddress("a@acme.com"), passwordHash: "z" });
  expect([up.id, up.version]).toEqual([u.id, 3]);
  expect(await h.users.insertOrIgnoreByTenantIdAndEmail({ email: parseEmailAddress("a@acme.com"), passwordHash: "z" })).toBeUndefined();

  // Across scopes, the scope is an argument (§21.2).
  const all = unscoped(h);
  const b = await all.users.insert({ tenantId: other.id }, { email: parseEmailAddress("b@other.com"), passwordHash: "x" });
  expect(b.tenantId).toBe(other.id);
  expect((await all.users.get({ tenantId: other.id }, UserFull.key(b))).id).toBe(b.id);
  expect(await all.users.count({ tenantId: other.id })).toBe(1n);

  // A profile has no version: an empty Partial returns the row as it is,
  // and a missing key is NotFound.
  const pr = await h.profiles.insert({ userId: up.id });
  expect(await h.profiles.patch({ userId: pr.userId }, {})).toEqual(pr);
  expect((await h.profiles.patch({ userId: pr.userId }, { bio: "hi" })).bio).toBe("hi");
  expect(await failure(h.profiles.patch({ userId: 999n }, { bio: "hi" }))).toBe("NotFound");

  // Posts, tags and their links; composite keys.
  const post = await h.posts.insert({ authorId: up.id, title: "Hello world" });
  expect([post.status, post.slug]).toEqual(["draft", "hello-world"]);
  const tag = await h.tags.insert({ name: parseSlugText("ts") });
  expect((await h.tags.upsertByName({ name: parseSlugText("ts") })).id).toBe(tag.id);
  expect(await h.tags.insertOrIgnoreByName({ name: parseSlugText("ts") })).toBeUndefined();
  const link = await h.postTags.insert({ postId: post.id, tagId: tag.id });
  expect(await h.postTags.get(link)).toEqual(link);
  await h.postTags.delete(link);
  expect(await h.postTags.find(link)).toBeUndefined();
  expect(await failure(h.postTags.delete(link))).toBe("NotFound");
  expect(await db.publishedPosts.count()).toBe(0n);

  // A write transaction commits or rolls back as a whole, and a read
  // transaction's stores only read.
  const boom = new Error("boom");
  const err = await h
    .write(async (tx: UserScopedWriteTx) => {
      await tx.tags.insert({ name: parseSlugText("rolled-back") });
      throw boom;
    })
    .catch((e: unknown) => e);
  expect(err).toBe(boom);
  expect(await h.tags.findByName(parseSlugText("rolled-back"))).toBeUndefined();
  expect(await h.read((tx: UserScopedReadTx) => tx.users.count())).toBe(1n);
  const tx = await db.beginWrite();
  await tx.tags.insert({ name: parseSlugText("committed") });
  await tx.commit();
  expect(await db.tags.findByName(parseSlugText("committed"))).toBeDefined();
  const rtx = await h.beginRead();
  try {
    expect(await rtx.users.exists()).toBe(true);
    expect(await rtx.raw("main").query("SELECT 1 AS one")).toHaveLength(1);
    expect(rtx.run(() => 7)).toBe(7);
  } finally {
    await rtx.rollback();
  }

  // Delete by key on a versioned entity compares the version.
  const cur = await h.users.get(UserFull.key(u));
  await h.profiles.delete(pr);
  await h.posts.delete(post);
  await h.users.delete(UserFull.key(cur));
  expect(await failure(h.users.delete(UserFull.key(cur)))).toBe("NotFound");
});

test("select", async () => {
  const db = new Db(await database());
  const acme = await db.tenants.insert({ name: "acme" });
  const other = await db.tenants.insert({ name: "other" });
  const h = db.withScope({ tenantId: acme.id });
  const o = db.withScope({ tenantId: other.id });
  const a = await h.users.insert({ email: parseEmailAddress("ann@acme.com"), passwordHash: "x", displayName: "Ann" });
  const b = await h.users.insert({ email: parseEmailAddress("bob@acme.com"), passwordHash: "x" });
  await o.users.insert({ email: parseEmailAddress("ann@other.com"), passwordHash: "x" });
  const p = await h.posts.insert({ authorId: a.id, title: "Hello", labels: ["Ann"] });

  // The scope filters every read; where, orderBy, limit and offset compose.
  const all = h.users.select(shapes.userFull);
  expect((await all.orderBy(users.email.desc()).all()).map((u) => u.id)).toEqual([b.id, a.id]);
  expect((await all.where(users.email.ilike("ANN%")).one()).id).toBe(a.id);
  expect((await all.orderBy(users.id.asc()).limit(1).offset(1).all()).map((u) => u.id)).toEqual([b.id]);
  expect(await all.where(or(users.displayName.isNull(), users.id.in([a.id]))).count()).toBe(2n);
  expect(await all.where(not(users.email.eq(parseEmailAddress("ann@acme.com"))), users.displayName.isNotNull()).optionalOne()).toBeUndefined();
  expect(await failure(all.one())).toBe("TooManyRows");

  // Across scopes, every tenant's rows.
  expect(await unscoped(h).users.select(shapes.userFull).where(users.email.startsWith("ann@")).count()).toBe(2n);

  // Relation predicates.
  expect((await all.where(users.postsToAuthor.any(posts.title.eq("Hello"))).all()).map((u) => u.id)).toEqual([a.id]);
  expect((await all.where(users.postsToAuthor.none()).all()).map((u) => u.id)).toEqual([b.id]);
  expect(await all.where(users.postsToAuthor.all(posts.title.eq("Hello"))).count()).toBe(2n);
  const ps = h.posts.select(shapes.postFull);
  expect(await ps.where(posts.labels.arrayContains("Ann"), posts.author.any(users.email.contains("acme"))).exists()).toBe(true);

  // Distinct, locks and iter.
  expect(await h.tenants.select(shapes.tenantFull).distinct().all()).toHaveLength(2);
  const locked = await h.write((tx: UserScopedWriteTx) => tx.posts.select(shapes.postFull).where(posts.id.eq(p.id)).forUpdate().noWait().one());
  expect(locked.id).toBe(p.id);
  let n = 0;
  for await (const u of all.noLog().consistency(Consistency.primary).iter()) {
    expect(u.tenantId).toBe(acme.id);
    n++;
  }
  expect(n).toBe(2);
  expect(await h.users.select(shapes.userFull).forShare().skipLocked().all()).toHaveLength(2);
});

test("queries", async () => {
  const db = new Db(await database());
  const acme = await db.tenants.insert({ name: "acme" });
  const other = await db.tenants.insert({ name: "other" });
  const h = db.withScope({ tenantId: acme.id });
  const a = await h.users.insert({ email: parseEmailAddress("ann@acme.com"), passwordHash: "x" });
  await h.posts.insert({ authorId: a.id, title: "One" });
  await h.posts.insert({ authorId: a.id, title: "Two" });

  // The handle's scope fills @scope.tenant_id.
  expect(await h.queries.getUserByEmail(parseEmailAddress("ann@acme.com"))).toEqual(a);
  expect(await failure(db.withScope({ tenantId: other.id }).queries.getUserByEmail(parseEmailAddress("ann@acme.com")))).toBe("NotFound");
  expect(await h.queries.authorPostCounts()).toEqual([{ authorId: a.id, displayName: null, posts: 2n }]);
  expect(await h.queries.renameUser("Ann", a.id, a.version)).toBe(1);

  // Across scopes the scope is an argument; a read transaction reads.
  const all = unscoped(h);
  expect(await all.queries.renameUser({ tenantId: acme.id }, null, a.id, a.version)).toBe(0);
  const u = await all.read((tx: UnscopedDbReadTx) => tx.queries.getUserByEmail({ tenantId: acme.id }, parseEmailAddress("ann@acme.com")));
  expect([u.displayName, u.version]).toEqual(["Ann", a.version + 1]);
});

/** issues returns the validation issues p rejects with. */
async function issues(p: Promise<unknown>): Promise<readonly ValidationIssue[]> {
  const e = await p.catch((e: unknown) => e);
  return e instanceof ValidationError ? e.issues : [];
}

test("filtered writes", async () => {
  const db = new Db(await database());
  const acme = await db.tenants.insert({ name: "acme" });
  const other = await db.tenants.insert({ name: "other" });
  const h = db.withScope({ tenantId: acme.id });
  const o = db.withScope({ tenantId: other.id });
  const email = parseEmailAddress;
  const long = "a".repeat(321) as EmailAddress;

  // insertMany inserts every draft, in order, or none of them.
  const us = await h.users.insertMany([
    { email: email("ann@acme.com"), passwordHash: "x" },
    { email: email("bob@acme.com"), passwordHash: "x" },
  ]);
  expect(us.map((u) => u.email as string)).toEqual(["ann@acme.com", "bob@acme.com"]);
  expect(us.every((u) => u.tenantId === acme.id)).toBe(true);
  const dup = await h.users
    .insertMany([
      { email: email("cid@acme.com"), passwordHash: "x" },
      { email: email("ann@acme.com"), passwordHash: "x" },
    ])
    .catch((e: unknown) => e);
  expect(classOf(dup)).toBe("UniqueViolation");
  expect(await failure(h.users.insertMany([{ email: email("dan@acme.com"), passwordHash: "x" }, { email: long, passwordHash: "x" }]))).toBe("ValidationErrors");
  expect(await h.users.insertMany([])).toEqual([]);
  expect(await h.users.count()).toBe(2n);
  await o.users.insert({ email: email("ann@other.com"), passwordHash: "x" });
  expect(await h.users.insertOrIgnoreByTenantIdAndEmail({ email: email("cid@acme.com"), passwordHash: "y" })).toBeDefined();

  // updateWhere writes the rows the filter keeps, in scope, and bumps their
  // versions.
  const [ann] = us as [UserFull, UserFull];
  expect(await h.users.updateWhere(users.email.startsWith("ann@"), { displayName: "Ann" })).toBe(1);
  expect((await h.users.get({ id: ann.id, version: ann.version + 1 })).displayName).toBe("Ann");
  const none = {} as Pred<UsersTable>;
  expect(await failure(h.users.updateWhere(none, {}))).toBeUndefined(); // a usage error, outside the taxonomy
  expect(await failure(h.users.updateWhere(users.allRows(), { email: long }))).toBe("ValidationErrors");
  await h.profiles.insert({ userId: ann.id });
  expect(await h.profiles.updateWhere(profiles.allRows(), {})).toBe(0);
  expect(await h.profiles.updateWhere(profiles.allRows(), { bio: "hi" })).toBe(1);

  // deleteWhere deletes in scope; allRows deletes every row of it.
  expect(await h.users.deleteWhere(users.email.eq(email("cid@acme.com")))).toBe(1);
  expect(await failure(h.users.deleteWhere(none))).toBeUndefined(); // a usage error, outside the taxonomy
  await h.profiles.deleteWhere(profiles.allRows());
  expect(await unscoped(h).users.deleteWhere({ tenantId: acme.id }, users.allRows())).toBe(2);
  expect(await o.users.count()).toBe(1n);
});

test("expressions", async () => {
  const db = new Db(await database());
  const acme = await db.tenants.insert({ name: "acme" });
  const h = db.withScope({ tenantId: acme.id });
  const a = await h.users.insert({ email: parseEmailAddress("ann@acme.com"), passwordHash: "x" });
  const b = await h.users.insert({ email: parseEmailAddress("bob@acme.com"), passwordHash: "x" });

  // An expression travels on the operation, in place of a value.
  let p = await h.posts.insert({ authorId: a.id, title: "Hello", status: "published" }, posts.publishedAt.set(now));
  expect(p.publishedAt).not.toBeNull();
  const ps = await h.posts.insertMany(
    [
      { authorId: a.id, title: "One", status: "published" },
      { authorId: a.id, title: "Two", status: "published" },
    ],
    posts.publishedAt.set(now),
  );
  expect(ps.every((x) => x.publishedAt !== null)).toBe(true);
  p = await h.posts.patch(PostFull.key(p), {}, posts.publishedAt.set(DEFAULT), posts.authorId.add(b.id - a.id));
  expect([p.publishedAt, p.authorId]).toEqual([null, b.id]);
  expect(await h.posts.updateWhere(posts.authorId.eq(b.id), {}, posts.authorId.sub(b.id - a.id))).toBe(1);
  await h.profiles.insert({ userId: a.id, bio: "hi" });
  expect(await h.profiles.updateWhere(profiles.allRows(), {}, profiles.bio.set(DEFAULT))).toBe(1);
  expect((await h.profiles.get({ userId: a.id })).bio).toBeNull();
  const u = await unscoped(h).users.patch({ tenantId: acme.id }, UserFull.key(a), {}, users.displayName.set(DEFAULT));
  expect(u.version).toBe(a.version + 1);

  // A field the payload sets too is field_set_twice; a column the write
  // does not take is an error.
  expect(await issues(h.posts.patch(PostFull.key(p), { title: "Hi" }, posts.title.set(DEFAULT)))).toEqual([
    { shape: "post.partial.flat", field: "title", kind: "field_set_twice" },
  ]);
  expect(await failure(h.users.patch(UserFull.key(u), {}, users.createdAt.set(now)))).toBeUndefined(); // a usage error, outside the taxonomy
  expect(await failure(h.posts.insertMany([{ authorId: a.id, title: "Three" }], posts.title.set(DEFAULT)))).toBe("ValidationErrors");
});

test("upsertSet", async () => {
  const db = new Db(await database());
  const acme = await db.tenants.insert({ name: "acme" });
  const h = db.withScope({ tenantId: acme.id });

  // The Partial replaces the default update side: on conflict only the
  // fields it holds change.
  const d = { email: parseEmailAddress("ann@acme.com"), passwordHash: "x" };
  const a = await h.users.upsertSetByTenantIdAndEmail(d, {});
  let got = await h.users.upsertSetByTenantIdAndEmail({ ...d, passwordHash: "y", displayName: "Draft" }, { displayName: "Ann" });
  expect([got.id, got.displayName, got.version]).toEqual([a.id, "Ann", a.version + 1]);
  // A Partial that sets nothing still returns the row.
  got = await h.users.upsertSetByTenantIdAndEmail(d, {});
  expect([got.id, got.displayName]).toEqual([a.id, "Ann"]);
  const b = await h.users.insert({ email: parseEmailAddress("bob@acme.com"), passwordHash: "x" });

  // Expressions go to the update side.
  const p = await h.posts.insert({ authorId: a.id, title: "Hello", status: "published" });
  const q = await h.posts.upsertSet({ id: p.id, authorId: a.id, title: "Other" }, {}, posts.authorId.add(b.id - a.id), posts.publishedAt.set(now));
  expect([q.id, q.title, q.authorId]).toEqual([p.id, "Hello", b.id]);
  expect(q.publishedAt).not.toBeNull();
  expect((await issues(h.posts.upsertSet({ id: p.id, authorId: a.id, title: "Other" }, { publishedAt: null }, posts.publishedAt.set(now))))[0]?.kind).toBe("field_set_twice");
  expect(await failure(h.users.upsertSet(d, { email: "a".repeat(321) as EmailAddress }))).toBe("ValidationErrors");
  expect(await failure(h.users.upsertSet({ ...d, email: "a".repeat(321) as EmailAddress }, {}))).toBe("ValidationErrors");
});

test("insertNested", async () => {
  const db = new Db(await database());
  const acme = await db.tenants.insert({ name: "acme" });
  const h = db.withScope({ tenantId: acme.id });

  // The user first, then its dependents with its key; the bound tenant is
  // read back.
  const u = await h.users.insertNested({
    email: parseEmailAddress("ann@acme.com"),
    passwordHash: "x",
    profile: { bio: "Hi" },
    postsToAuthor: [{ title: "Hello" }, { title: "Again" }],
  });
  expect(u.tenant).toEqual(acme);
  expect([u.profile?.bio, u.profile?.userId]).toEqual(["Hi", u.id]);
  expect(u.postsToAuthor.map((p) => [p.title, p.authorId])).toEqual([
    ["Hello", u.id],
    ["Again", u.id],
  ]);
  expect(u.postsToEditor).toEqual([]);
  const b = await unscoped(h).users.insertNested(
    { tenantId: acme.id },
    { email: parseEmailAddress("bob@acme.com"), passwordHash: "x", postsToEditor: [{ authorId: u.id, title: "Edited" }] },
    users.createdAt.set(now),
  );
  expect(b.profile).toBeNull();
  expect(b.postsToEditor.map((p) => p.editorId)).toEqual([b.id]);

  // A failing child rolls the whole graph back.
  const first = u.postsToAuthor[0] as PostFull;
  const dup = await h.users
    .insertNested({ email: parseEmailAddress("cid@acme.com"), passwordHash: "x", postsToAuthor: [{ id: first.id, title: "Dup" }] })
    .catch((e: unknown) => e);
  expect(classOf(dup)).toBe("UniqueViolation");
  expect(await h.users.select(shapes.userFull).where(users.email.eq(parseEmailAddress("cid@acme.com"))).exists()).toBe(false);
  expect(await failure(h.users.insertNested({ email: "a".repeat(321) as EmailAddress, passwordHash: "x" }))).toBe("ValidationErrors");

  // Many-to-many: links and creates write junction rows; junction children
  // read their far rows back.
  const second = u.postsToAuthor[1] as PostFull;
  const tag = await db.tags.insertNested({
    name: parseSlugText("go"),
    postTags: [{ postId: first.id }],
    posts: [
      { kind: "link", key: { id: second.id } },
      { kind: "create", draft: { authorId: u.id, title: "New" } },
    ],
  });
  expect(tag.posts.map((p) => p.title)).toEqual(["Hello", "Again", "New"]);
  expect(tag.postTags.map((pt) => pt.postId)).toEqual(tag.posts.map((p) => p.id));

  // Owning relations: a create is inserted first, a link sets the key.
  const edited = b.postsToEditor[0] as PostFull;
  let pt = await db.postTags.insertNested({ post: { kind: "link", key: { id: edited.id } }, tag: { kind: "create", draft: { name: parseSlugText("db") } } });
  expect([pt.post.id, pt.tag.name as string, pt.tagId]).toEqual([edited.id, "db", pt.tag.id]);
  pt = await db.postTags.insertNested({ postId: first.id, tag: { kind: "link", key: { id: pt.tagId } } });
  expect([pt.post.id, pt.tag.name as string]).toEqual([first.id, "db"]);
  expect(await failure(db.postTags.insertNested({ postId: first.id, post: { kind: "link", key: { id: edited.id } }, tagId: pt.tagId }))).not.toBe("no error");
});

test("insertNested on a scoped handle", async () => {
  const db = new Db(await database());
  const acme = await db.tenants.insert({ name: "acme" });
  const other = await db.tenants.insert({ name: "other" });
  const h = db.withScope({ tenantId: acme.id });
  const ann = await h.users.insert({ email: parseEmailAddress("ann@acme.com"), passwordHash: "x" });
  const go = await db.tags.insert({ name: parseSlugText("go") });

  const p = await h.posts.insertNested({
    title: "Hello",
    author: { kind: "link", key: UserFull.key(ann) },
    editor: { kind: "create", draft: { email: parseEmailAddress("ed@acme.com"), passwordHash: "x" } },
    postTags: [{ tagId: go.id }],
    tags: [{ kind: "create", draft: { name: parseSlugText("db") } }],
  });
  expect([p.author.id, p.editor?.tenantId, p.editorId]).toEqual([ann.id, acme.id, p.editor?.id ?? null]);
  expect(p.tags.map((t) => t.name as string)).toEqual(["go", "db"]);
  expect(p.postTags).toHaveLength(2);
  const q = await h.posts.insertNested({ title: "Plain", authorId: ann.id });
  expect([q.author.id, q.editor, q.tags]).toEqual([ann.id, null, []]);

  // The scope reads the author back: one in another tenant is not found,
  // and the post is rolled back.
  const bob = await db.withScope({ tenantId: other.id }).users.insert({ email: parseEmailAddress("bob@other.com"), passwordHash: "x" });
  expect(await failure(h.posts.insertNested({ title: "Cross", authorId: bob.id }))).toBe("NotFound");
  expect(await h.posts.select(shapes.postFull).where(posts.title.eq("Cross")).exists()).toBe(false);

  const pr = await h.profiles.insertNested({ user: { kind: "create", draft: { email: parseEmailAddress("cid@acme.com"), passwordHash: "x" } } });
  expect([pr.user.email as string, pr.userId]).toEqual(["cid@acme.com", pr.user.id]);
  await h.write((tx: UserScopedWriteTx) => tx.profiles.insertNested({ userId: ann.id }));
});

test("patchNested", async () => {
  const db = new Db(await database());
  const acme = await db.tenants.insert({ name: "acme" });
  const h = db.withScope({ tenantId: acme.id });
  const u = await h.users.insertNested({
    email: parseEmailAddress("ann@acme.com"),
    passwordHash: "x",
    profile: { bio: "Hi" },
    postsToAuthor: [{ title: "One" }, { title: "Two" }, { title: "Three" }],
  });
  const ed = await h.users.insert({ email: parseEmailAddress("ed@acme.com"), passwordHash: "x" });
  const edited = await h.posts.insert({ authorId: u.id, editorId: ed.id, title: "Edited" });
  const [one, two] = u.postsToAuthor as [PostFull, PostFull];

  // posts_to_author is replace + delete: the posts the changes leave out
  // are deleted. posts_to_editor is merge + unlink: delete clears editor_id.
  const got = await h.users.patchNested(UserFull.key(u), {
    displayName: "Ann",
    postsToAuthor: [
      { kind: "update", key: PostFull.key(one), partial: { title: "First" } },
      { kind: "delete", key: PostFull.key(two) },
      { kind: "create", draft: { title: "Four" } },
    ],
    profile: { kind: "update", key: { userId: u.id }, partial: { bio: null } },
  });
  expect([got.displayName, got.version]).toEqual(["Ann", u.version + 1]);
  expect(got.postsToAuthor.map((p) => p.title)).toEqual(["First", "Four"]);
  expect([got.profile?.bio, got.tenant]).toEqual([null, acme]);
  expect(await failure(h.posts.get(PostFull.key(edited)))).toBe("NotFound");
  let e = await h.users.patchNested(UserFull.key(ed), {
    postsToEditor: [{ kind: "create", draft: { authorId: u.id, title: "Draft" } }],
    profile: { kind: "create", draft: {} },
  });
  expect(e.postsToEditor).toHaveLength(1);
  expect(e.profile).not.toBeNull();
  const draft = e.postsToEditor[0] as PostFull;
  e = await unscoped(h).users.patchNested({ tenantId: acme.id }, UserFull.key(e), {
    postsToEditor: [
      { kind: "update", key: PostFull.key(draft), partial: { title: "Edit" } },
      { kind: "delete", key: PostFull.key(draft) },
    ],
    profile: { kind: "delete", key: { userId: ed.id } },
  });
  expect([e.postsToEditor, e.profile]).toEqual([[], null]);

  // A change naming another parent's child is NotFound, and nothing of the
  // patch stays.
  expect(
    await failure(
      h.users.patchNested(UserFull.key(e), { displayName: "Ed", postsToAuthor: [{ kind: "update", key: PostFull.key(one), partial: {} }] }),
    ),
  ).toBe("NotFound");
  expect((await h.users.get(UserFull.key(e))).displayName).toBeNull();
  for (const p of [
    { postsToEditor: [{ kind: "delete" as const, key: PostFull.key(one) }] },
    { postsToEditor: [{ kind: "update" as const, key: PostFull.key(one), partial: {} }] },
    { profile: { kind: "update" as const, key: { userId: u.id }, partial: {} } },
    { profile: { kind: "delete" as const, key: { userId: u.id } } },
    { postsToAuthor: [{ kind: "delete" as const, key: PostFull.key(one) }] },
  ]) {
    expect(await failure(h.users.patchNested(UserFull.key(e), p))).toBe("NotFound");
  }
  expect(await failure(h.users.patchNested(UserFull.key(e), { email: "a".repeat(321) as EmailAddress }))).toBe("ValidationErrors");
});

test("patchNested links", async () => {
  const db = new Db(await database());
  const acme = await db.tenants.insert({ name: "acme" });
  const h = db.withScope({ tenantId: acme.id });
  const u = await h.users.insert({ email: parseEmailAddress("ann@acme.com"), passwordHash: "x" });
  const one = await h.posts.insert({ authorId: u.id, title: "One" });
  const two = await h.posts.insert({ authorId: u.id, title: "Two" });
  const other = await db.tags.insert({ name: parseSlugText("other") });
  await db.postTags.insert({ postId: one.id, tagId: other.id });

  // Tags: posts links through post_tags; post_tags changes its rows.
  const tag = await db.tags.insert({ name: parseSlugText("go") });
  let got = await db.tags.patchNested(
    { id: tag.id },
    {
      name: parseSlugText("golang"),
      posts: [
        { kind: "link", key: { id: one.id } },
        { kind: "link", key: { id: one.id } },
        { kind: "create", draft: { authorId: u.id, title: "Three" } },
      ],
    },
  );
  expect(got.name as string).toBe("golang");
  expect(got.posts.map((p) => p.title)).toEqual(["One", "Three"]);
  expect(got.postTags).toHaveLength(2);
  got = await db.tags.patchNested(
    { id: tag.id },
    {
      posts: [{ kind: "unlink", key: { id: one.id } }],
      postTags: [
        { kind: "create", draft: { postId: two.id } },
        { kind: "update", key: { postId: two.id, tagId: tag.id }, partial: {} },
      ],
    },
  );
  expect(got.posts.map((p) => p.title)).toEqual(["Two", "Three"]);
  got = await db.tags.patchNested({ id: tag.id }, { postTags: [{ kind: "delete", key: { postId: two.id, tagId: tag.id } }] });
  expect(got.posts.map((p) => p.title)).toEqual(["Three"]);
  for (const ch of [
    { kind: "update" as const, key: { postId: one.id, tagId: other.id }, partial: {} },
    { kind: "delete" as const, key: { postId: one.id, tagId: other.id } },
    { kind: "delete" as const, key: { postId: two.id, tagId: tag.id } },
  ]) {
    expect(await failure(db.tags.patchNested({ id: tag.id }, { postTags: [ch] }))).toBe("NotFound");
  }

  // Scoped posts: owning changes go first, through users in scope.
  const ed = await h.users.insert({ email: parseEmailAddress("ed@acme.com"), passwordHash: "x" });
  let p = await h.posts.patchNested(PostFull.key(one), {
    title: "First",
    author: { kind: "update", partial: { displayName: "Ann" } },
    editor: { kind: "create", draft: { email: parseEmailAddress("eve@acme.com"), passwordHash: "x" } },
    tags: [
      { kind: "link", key: { id: tag.id } },
      { kind: "create", draft: { name: parseSlugText("sql") } },
      { kind: "unlink", key: { id: other.id } },
    ],
  });
  expect([p.title, p.author.displayName, p.editor?.email as string]).toEqual(["First", "Ann", "eve@acme.com"]);
  expect(p.tags.map((t) => t.name as string)).toEqual(["golang", "sql"]);
  p = await h.posts.patchNested(PostFull.key(one), {
    author: { kind: "link", key: UserFull.key(ed) },
    editor: { kind: "update", partial: { displayName: "Eve" } },
    postTags: [
      { kind: "delete", key: { postId: one.id, tagId: tag.id } },
      { kind: "create", draft: { tagId: other.id } },
      { kind: "update", key: { postId: one.id, tagId: other.id }, partial: {} },
    ],
  });
  expect([p.author.id, p.editor?.displayName, p.tags[0]?.id]).toEqual([ed.id, "Eve", other.id]);
  expect(p.tags).toHaveLength(2);
  p = await h.posts.patchNested(PostFull.key(one), {
    author: { kind: "create", draft: { email: parseEmailAddress("new@acme.com"), passwordHash: "x" } },
    editor: { kind: "unlink" },
  });
  expect([p.author.email as string, p.editor]).toEqual(["new@acme.com", null]);
  p = await h.posts.patchNested(PostFull.key(one), { editor: { kind: "link", key: UserFull.key(u) } });
  expect(p.editor?.id).toBe(u.id);
  for (const c of [
    { postTags: [{ kind: "delete" as const, key: { postId: two.id, tagId: tag.id } }] },
    { postTags: [{ kind: "update" as const, key: { postId: two.id, tagId: tag.id }, partial: {} }] },
  ]) {
    expect(await failure(h.posts.patchNested(PostFull.key(two), c))).toBe("NotFound");
  }
  expect(await failure(h.posts.patchNested(PostFull.key(two), { editor: { kind: "update", partial: {} } }))).toBe("NotFound");

  // Scoped profiles: the owning user's update.
  await h.profiles.insert({ userId: u.id });
  const pr = await h.profiles.patchNested({ userId: u.id }, { bio: "Hi", user: { kind: "update", partial: { displayName: "Annie" } } });
  expect([pr.bio, pr.user.displayName]).toEqual(["Hi", "Annie"]);
});
