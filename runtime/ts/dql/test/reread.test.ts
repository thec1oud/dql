// Re-reading written rows on a dialect without RETURNING (design record
// §20.1), as the Go runtime's reread_test does.

import { afterEach, expect, test } from "bun:test";
import { Fake } from "../src/fake.js";
import {
  ConcurrentModification,
  DatabaseError,
  Executor,
  type Fragments,
  type Node,
  NotFound,
  type Query,
  Row,
  UsageError,
  insertMany,
  list,
  oneOf,
  optionalOf,
  param,
  statementOf,
  use,
  versionedOne,
} from "../src/index.js";
import { pg } from "./executor.test.js";
import { FakeDriver } from "./fake-driver.js";

// A MySQL-like table: no RETURNING, so a write's row is read back by key.
const table: Fragments = {
  placeholder: "question",
  templates: {
    "insert.users": "INSERT INTO `users` ({cols}) VALUES {rows}",
    "select.user": "SELECT `users`.`id` FROM `users`",
    "name.users.id": "`id`",
    "name.users.a": "`a`",
    "values.row": "({0})",
    "value.default": "DEFAULT",
    "clause.where": " WHERE {0}",
    "op.eq": "{0} = {1}",
    "col.users.id": "`users`.`id`",
  },
};

const insert = (reread: readonly Node[] = [use("select.user"), use("clause.where", { "0": use("op.eq", { "0": use("col.users.id"), "1": param("id") }) })]): Query => ({
  name: "user.insert",
  catalog: "shop",
  kind: "insert",
  writes: true,
  plan: [
    use("insert.users", {
      cols: list([use("name.users.id"), use("name.users.a")], ", "),
      rows: use("values.row", { "0": list([param("id"), param("a")], ", ") }),
    }),
  ],
  reread,
  generated: "id",
});

const first = (r: Row) => r.values[0];

let open: Executor[] = [];

afterEach(async () => {
  for (const e of open) {
    await e.close();
  }
  open = [];
});

async function fake(): Promise<[Fake, Executor]> {
  const f = new Fake();
  const e = await f.executor(pg, ["shop"]);
  open.push(e);
  return [f, e];
}

test("a write's row is read back by key in its transaction (§20.1)", async () => {
  const [f, e] = await fake();
  f.onAny("user.insert").affected(1).insertId(41n);
  f.on("user.insert", 41n).returns([41n]);
  expect(await oneOf(e, statementOf(insert(), table, { a: 1 }), first)).toBe(41n);
  expect(f.calls().map((c) => [c.sql, c.args, c.inTx])).toEqual([
    ["INSERT INTO `users` (`id`, `a`) VALUES (DEFAULT, ?)", [1], true],
    ["SELECT `users`.`id` FROM `users` WHERE `users`.`id` = ?", [41n], true],
  ]);
  // Hooks see the write with the rows it affected.
  const rows: number[] = [];
  const hooked = await f.executor(pg, ["shop"], { hooks: [{ queryEnd: (ev) => rows.push(ev.rows) }] });
  open.push(hooked);
  await oneOf(hooked, statementOf(insert(), table, { a: 1 }), first);
  expect(rows).toEqual([1, 1]);
  // A key the call gives is read by its own value.
  f.on("user.insert", 9n).returns([9n]);
  expect(await oneOf(e, statementOf(insert(), table, { id: 9n, a: 1 }), first)).toBe(9n);
});

test("a write that affects nothing returns no row", async () => {
  const [f, e] = await fake();
  f.onAny("user.insert").affected(0);
  const s = statementOf(insert(), table, { a: 1 });
  await expect(oneOf(e, s, first)).rejects.toBeInstanceOf(NotFound);
  expect(await optionalOf(e, s, first)).toBeUndefined();
  f.on("user.find").returns([1n]);
  const exists = { query: "user.find", catalog: "shop", sql: "SELECT 1" };
  await expect(versionedOne(e, s, exists, first)).rejects.toBeInstanceOf(ConcurrentModification);
  expect(f.calls().some((c) => c.sql.startsWith("SELECT `users`"))).toBe(false);
});

test("the write's and the read's errors come back", async () => {
  const [f, e] = await fake();
  const s = statementOf(insert(), table, { a: 1 });
  f.onAny("user.insert").fails(new Error("boom"));
  await expect(oneOf(e, s, first)).rejects.toBeInstanceOf(DatabaseError);
  f.onAny("user.insert").affected(1).insertId(5n);
  f.on("user.insert", 5n).fails(new Error("read boom"));
  await expect(oneOf(e, s, first)).rejects.toBeInstanceOf(DatabaseError);
  // A read that does not assemble fails the write's transaction.
  const bad = statementOf(insert([use("select.nothing")]), table, { a: 1 });
  f.onAny("user.insert").affected(1).insertId(5n);
  await expect(oneOf(e, bad, first)).rejects.toThrow("no select.nothing in query user.insert");
  // Only a writer writes.
  await expect(e.read((tx) => oneOf(tx, s, first))).rejects.toBeInstanceOf(UsageError);
});

test("insertMany inserts and reads back one row at a time, in one transaction", async () => {
  const [f, e] = await fake();
  let next = 0n;
  f.onAny("user.insert").affected(1);
  const q = insert();
  const rows = [{ a: 1 }, { a: 2 }, { id: 7n, a: 3 }];
  // The fake reports one ID per expectation, so each write gets its own.
  const got: unknown[] = [];
  for (const r of rows) {
    next++;
    f.onAny("user.insert").affected(1).insertId(next);
    f.on("user.insert", "id" in r ? r.id : next).returns(["id" in r ? r.id : next]);
    got.push(...(await insertMany(e, table, q, [r], first)));
  }
  expect(got).toEqual([1n, 2n, 7n]);
  f.onAny("user.insert").affected(1).insertId(3n);
  f.on("user.insert", 3n).returns([3n]);
  expect(await insertMany(e, table, q, [{ a: 1 }, { a: 2 }], first)).toEqual([3n, 3n]);
  const calls = f.calls().slice(-4);
  expect(calls.every((c) => c.inTx)).toBe(true);
});

test("a connection held back for independent transactions passes the insert ID through, or 0 without one", async () => {
  const reserved = (dsn: string) => ({ sources: { main: { primary: { dsn, maxConns: 2 }, independentReserve: 1 } }, catalogs: { shop: "main" } });
  const f = new Fake();
  const e = await Executor.open(f, pg, reserved("fake"));
  open.push(e);
  f.onAny("user.insert").affected(1).insertId(41n);
  f.on("user.insert", 41n).returns([41n]);
  expect(await optionalOf(e, statementOf(insert(), table, { a: 1 }), first)).toBe(41n);
  // A driver that reports no insert ID still writes, and reads back by the key the call gives.
  for (const config of [reserved("pg"), { sources: { main: { primary: { dsn: "pg" } } }, catalogs: { shop: "main" } }]) {
    const d = new FakeDriver();
    d.handle = (s) => [new Row(s.sql.startsWith("SELECT") ? [s.args[0]] : [])];
    const plain = await Executor.open(d, pg, config);
    open.push(plain);
    expect(await optionalOf(plain, statementOf(insert(), table, { id: 5, a: 1 }), first)).toBe(5);
  }
});

// ranged is table with reread.from, so that insertMany reads a statement's rows back at once.
const ranged: Fragments = {
  placeholder: "question",
  templates: {
    ...table.templates,
    "reread.from": "{0} >= {1} AND MOD({0} - {2}, 1) = 0",
    "clause.order": " ORDER BY {0}",
    "order.asc": "{0} ASC",
    "clause.limit": " LIMIT {0}",
  },
};

test("insertMany reads each statement's rows back at once from its last insert ID, where every row leaves the key out (§20.1, §25.2)", async () => {
  const f = new Fake();
  const e = await f.executor({ ...pg, maxParams: 4 }, ["shop"]);
  open.push(e);
  f.on("user.insert", 1, 2).affected(2).insertId(1n);
  f.on("user.insert", 3).affected(1).insertId(3n);
  f.on("user.insert", 1n, 1n, 2n).returns([1n], [2n]);
  f.on("user.insert", 3n, 3n, 1n).returns([3n]);
  expect(await insertMany(e, ranged, insert(), [{ a: 1 }, { a: 2 }, { a: 3 }], first)).toEqual([1n, 2n, 3n]);
  const calls = f.calls();
  expect(calls.map((c) => c.sql)).toEqual([
    "INSERT INTO `users` (`id`, `a`) VALUES (DEFAULT, ?), (DEFAULT, ?)",
    "SELECT `users`.`id` FROM `users` WHERE `users`.`id` >= ? AND MOD(`users`.`id` - ?, 1) = 0 ORDER BY `users`.`id` ASC LIMIT ?",
    "INSERT INTO `users` (`id`, `a`) VALUES (DEFAULT, ?)",
    "SELECT `users`.`id` FROM `users` WHERE `users`.`id` >= ? AND MOD(`users`.`id` - ?, 1) = 0 ORDER BY `users`.`id` ASC LIMIT ?",
  ]);
  expect(calls.every((c) => c.inTx)).toBe(true);

  // A row that sets its key takes a row at a time.
  f.on("user.insert", 9n, 4).affected(1).insertId(9n);
  f.on("user.insert", 9n).returns([9n]);
  expect(await insertMany(e, ranged, insert(), [{ id: 9n, a: 4 }], first)).toEqual([9n]);

  // A read that finds fewer rows than the insert wrote is an error.
  f.on("user.insert", 5).affected(1).insertId(5n);
  f.on("user.insert", 5n, 5n, 1n).returns();
  await expect(insertMany(e, ranged, insert(), [{ a: 5 }], first)).rejects.toThrow("dql: query user.insert inserted 1 rows and read back 0 of 1");
});

test("insertMany takes a row at a time without reread.from, a generated key, or a read by it", async () => {
  const [f, e] = await fake();
  f.onAny("user.insert").affected(1).insertId(1n);
  f.on("user.insert", 1n).returns([1n]);
  const unkeyed: Query = { ...insert(), generated: "" };
  const plain = [use("select.user"), use("clause.where", { "0": list([param("id")], "") })];
  for (const [t, q] of [
    [table, insert()],
    [ranged, unkeyed],
    [ranged, insert(plain)],
  ] as const) {
    f.on("user.insert", ...(q.generated === "" ? [] : [1n])).returns([1n]);
    await insertMany(e, t, q, [{ a: 1 }], first).catch(() => undefined);
  }
  expect(f.calls().some((c) => c.sql.includes(">="))).toBe(false);
});
