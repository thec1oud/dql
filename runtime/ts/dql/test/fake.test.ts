import { describe, expect, test } from "bun:test";
import { Row } from "../src/decode.js";
import { classOf } from "../src/errors.js";
import { type Executor, UniqueViolation } from "../src/index.js";
import { Fake, Unexpected } from "../src/fake.js";
import { execute, iterate, queryAll, queryOne } from "../src/index.js";
import { pg } from "./executor.test.js";

const stmt = (query: string, args: unknown[] = []) => ({ query, catalog: "shop", sql: `SQL of ${query}`, args });

async function withFake(fn: (f: Fake, e: Executor) => Promise<void>): Promise<void> {
  const f = new Fake();
  const e = await f.executor(pg, ["shop"]);
  try {
    await fn(f, e);
  } finally {
    await e.close();
  }
}

describe("the recording fake executor (§26.3)", () => {
  test("answers by query name and arguments, the latest expectation first", () =>
    withFake(async (f, e) => {
      const any = f.onAny("users.select").returns([1, "a"], new Row([2, "b"]));
      const exact = f.on("users.select", "acme", 7n).returns([3, "c"]);
      expect((await queryAll(e, stmt("users.select", ["other"]))).map((r) => r.values)).toEqual([
        [1, "a"],
        [2, "b"],
      ]);
      expect((await queryOne(e, stmt("users.select", ["acme", 7n]))).values).toEqual([3, "c"]);
      // Deep equality: an equal array matches.
      f.on("users.byIds", [1, 2]).returns([9]);
      expect((await queryOne(e, stmt("users.byIds", [[1, 2]]))).values).toEqual([9]);
      // No arguments match only a call with none.
      const none = f.on("users.count").returns([5]);
      expect((await queryOne(e, stmt("users.count"))).values).toEqual([5]);
      expect([any.calls, exact.calls, none.calls]).toEqual([1, 1, 1]);
      // Streams read the same answers.
      const streamed: unknown[] = [];
      for await (const r of iterate(e, stmt("users.select", ["x"]))) {
        streamed.push(r.values[0]);
      }
      expect(streamed).toEqual([1, 2]);
      f.verify();
    }));

  test("writes, failures and transactions", () =>
    withFake(async (f, e) => {
      f.on("users.delete", 1).affected(3);
      f.on("users.insert", "dup").fails(new UniqueViolation({ name: "shop.app.users.users_email_key", kind: "unique", sensitive: false }, "", { dialect: "postgres", query: "users.insert" }));
      f.on("users.patch").returns([1]);
      expect(await execute(e, stmt("users.delete", [1]))).toBe(3);
      expect(await execute(e, stmt("users.patch"))).toBe(1);
      const dup = await execute(e, stmt("users.insert", ["dup"])).catch((x) => x);
      expect(classOf(dup)).toBe("UniqueViolation");
      await e.write(async (tx) => {
        await execute(tx, stmt("users.delete", [1]));
        await e.write((n) => execute(n, stmt("users.delete", [1])), { propagation: "nested" });
        await e
          .write(async (n) => {
            await execute(n, stmt("users.delete", [1]));
            throw new Error("undo");
          }, { propagation: "nested" })
          .catch(() => {});
      });
      await e
        .write(async (tx) => {
          await execute(tx, stmt("users.delete", [1]));
          throw new Error("undo");
        })
        .catch(() => {});
      // A plain error is the driver's, so a DatabaseError.
      f.on("users.find").fails(new Error("down"));
      expect(classOf(await queryAll(e, stmt("users.find")).catch((x) => x))).toBe("DatabaseError");
      expect(f.calls().map((c) => [c.query, c.sql, c.args, c.inTx])).toEqual([
        ["users.delete", "SQL of users.delete", [1], false],
        ["users.patch", "SQL of users.patch", [], false],
        ["users.insert", "SQL of users.insert", ["dup"], false],
        ["users.delete", "SQL of users.delete", [1], true],
        ["users.delete", "SQL of users.delete", [1], true],
        ["users.delete", "SQL of users.delete", [1], true],
        ["users.delete", "SQL of users.delete", [1], true],
        ["users.find", "SQL of users.find", [], false],
      ]);
      f.verify();
    }));

  test("an unexpected call fails, and verify reports it", () =>
    withFake(async (f, e) => {
      f.on("users.get", 1).returns([1]);
      const err = await queryAll(e, stmt("users.get", [2, "x"])).catch((x) => x);
      expect([err instanceof Unexpected, classOf(err), err.message]).toEqual([true, "DatabaseError", 'dqlfake: unexpected call of users.get with [2, "x"]']);
      expect(err.call.args).toEqual([2, "x"]);
      expect(f.unexpected().map((c) => c.query)).toEqual(["users.get"]);
      expect(() => f.verify()).toThrow('dqlfake: unexpected call of users.get with [2, "x"]');
    }));
});
