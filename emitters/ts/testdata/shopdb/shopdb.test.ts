// This file is hand-written (§26.5): it runs the generated package against
// the running example's schema on the Postgres server DQL_TEST_POSTGRES_URL
// names, in a fresh database. The ts emitter's tests run it with Bun.

import { afterAll, beforeAll, expect, test } from "bun:test";
import { PgDriver } from "@dql/pg";
import { Consistency, type Executor, classOf, isViolation, not, or, parseUuid } from "@dql/runtime";
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
  posts,
  shapes,
  unscoped,
  users,
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
