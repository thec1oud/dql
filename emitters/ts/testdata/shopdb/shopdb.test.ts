// This file is hand-written (§26.5): it runs the generated package against
// the running example's schema on the Postgres server DQL_TEST_POSTGRES_URL
// names, in a fresh database. The ts emitter's tests run it with Bun.

import { afterAll, beforeAll, expect, test } from "bun:test";
import { PgDriver } from "@dql/pg";
import { type Executor, classOf, isViolation, parseUuid } from "@dql/runtime";
import { Db, type EmailAddress, type UserScopedReadTx, type UserScopedWriteTx, UserFull, newExecutor, parseEmailAddress, parseSlugText, unscoped } from "./index.dql.ts";

const base = process.env["DQL_TEST_POSTGRES_URL"] ?? "";
const name = `shopdb_test_${process.pid}_${Date.now()}`;
let admin: Executor;
let exec: Executor;

function config(dsn: string) {
  return { sources: { main: { primary: { dsn, maxConns: 4 } } }, catalogs: { shop: "main" } };
}

// A fresh database holding testdata/shop/schema.sql, dropped when the tests end.
beforeAll(async () => {
  if (base === "") {
    throw new Error("DQL_TEST_POSTGRES_URL is not set");
  }
  admin = await newExecutor(new PgDriver(), config(base));
  await new Db(admin).raw("main").exec(`CREATE DATABASE ${name} TEMPLATE template0 ENCODING 'UTF8' LOCALE 'C'`);
  const u = new URL(base);
  u.pathname = `/${name}`;
  exec = await newExecutor(new PgDriver(), config(u.toString()));
  // Raw statements run one at a time; the schema's statements end their lines.
  const schema = await Bun.file(new URL("../../../../testdata/shop/schema.sql", import.meta.url)).text();
  for (const s of schema.split(/;\s*$/m)) {
    if (s.trim() !== "") {
      await new Db(exec).raw("main").exec(s);
    }
  }
});

afterAll(async () => {
  await exec?.close();
  if (admin !== undefined) {
    await new Db(admin).raw("main").exec(`DROP DATABASE ${name} WITH (FORCE)`);
    await admin.close();
  }
});

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
  const db = new Db(exec);

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
