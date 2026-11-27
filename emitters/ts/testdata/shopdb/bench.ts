// This file is hand-written (§26.5): it measures the runtime's overhead per
// call of generated code against the fake driver, so the numbers count dql's
// own work (building statements, binding, validating, decoding) and not a
// database's. Run it with Bun: bun bench.ts.

import { Fake } from "@dql/runtime/fake";
import { Db, dialect, parseSlugText } from "./index.dql.ts";

/** measure runs fn n times after a warm-up and prints its mean time per call. */
async function measure(name: string, fn: () => Promise<unknown>, n = 20_000): Promise<void> {
  for (let i = 0; i < n / 10; i++) {
    await fn();
  }
  const start = performance.now();
  for (let i = 0; i < n; i++) {
    await fn();
  }
  const ns = ((performance.now() - start) * 1e6) / n;
  console.log(`${name}\t${Math.round(ns)} ns/op`);
}

/** db returns a generated Db on a fake answering every call of query with row. */
async function db(query: string, row: unknown[]): Promise<{ db: Db; close: () => Promise<void> }> {
  const f = new Fake();
  f.onAny(query).returns(row);
  const e = await f.executor(dialect, ["shop"]);
  return { db: new Db(e), close: () => e.close() };
}

const find = await db("tag.find.by_primary", [1n, "go"]);
await measure("Find", () => find.db.tags.find({ id: 1n }));
await find.close();

const count = await db("tag.count", [5n]);
await measure("Count", () => count.db.tags.count());
await count.close();

const insert = await db("tag.insert", [1n, "go"]);
const name = parseSlugText("go");
await measure("Insert", () => insert.db.tags.insert({ name }));
await insert.close();
