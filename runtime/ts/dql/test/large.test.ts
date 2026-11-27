import { afterEach, describe, expect, test } from "bun:test";
import { Row } from "../src/decode.js";
import { classOf } from "../src/errors.js";
import { type Config, type Dialect, Executor } from "../src/executor.js";
import { type Bulk, type Statement, atomic, execute, executeBulk, iterate, queryBulk } from "../src/index.js";
import { pg } from "./executor.test.js";
import { FakeDriver, rows } from "./fake-driver.js";

const main: Config = { sources: { main: { primary: { dsn: "pg", maxConns: 3 } } }, catalogs: { shop: "main" } };

let open: Executor[] = [];

afterEach(async () => {
  for (const e of open) {
    await e.close();
  }
  open = [];
});

async function newExec(d: FakeDriver, dialect: Dialect = pg): Promise<Executor> {
  const e = await Executor.open(d, dialect, main, { random: () => 0 });
  open.push(e);
  d.calls();
  return e;
}

const stmt = (sql: string, more: Partial<Statement> = {}): Statement => ({ query: "q", catalog: "shop", sql, ...more });

describe("streaming (§25.1)", () => {
  test("rows arrive one step at a time on one connection", async () => {
    const d = new FakeDriver();
    d.handle = rows([1], [2], [3]);
    const e = await newExec(d);
    const seen: unknown[] = [];
    for await (const r of iterate(e, stmt("SELECT"))) {
      seen.push(r.values[0]);
      // The connection is held while the caller iterates.
      seen.push(d.calls());
    }
    expect(seen).toEqual([1, "pg#2 query SELECT []", 2, "", 3, ""]);
    expect(d.calls()).toBe("pg#2 put back");
    // Each iteration runs the statement anew; a finished one stays finished.
    const it = iterate(e, stmt("SELECT"))[Symbol.asyncIterator]();
    expect((await it.next()).value).toEqual(new Row([1]));
    expect(await it.next()).toEqual({ done: false, value: new Row([2]) });
    expect(await it.next()).toEqual({ done: false, value: new Row([3]) });
    expect(await it.next()).toEqual({ done: true, value: undefined });
    expect(await it.next()).toEqual({ done: true, value: undefined });    // Hooks see the rows the iteration took.
    const ended: number[] = [];
    const hooked = await Executor.open(d, pg, main, { hooks: [{ queryEnd: (q) => ended.push(q.rows) }] });
    open.push(hooked);
    for await (const r of iterate(hooked, stmt("SELECT"))) {
      if (r.values[0] === 2) {
        break;
      }
    }
    expect(ended).toEqual([2]);
  });

  test("breaking out releases the connection", async () => {
    const d = new FakeDriver();
    d.handle = rows([1], [2], [3]);
    const e = await newExec(d);
    for await (const r of iterate(e, stmt("SELECT"))) {
      expect(r.values[0]).toBe(1);
      break;
    }
    expect(d.calls()).toBe("pg#2 query SELECT []; pg#2 put back");
    // An iteration stopped before it began runs nothing.
    const it = iterate(e, stmt("SELECT"))[Symbol.asyncIterator]();
    expect(await it.return?.()).toEqual({ done: true, value: undefined });
    expect(d.calls()).toBe("");
  });

  test("failures end the iteration, translated", async () => {
    const d = new FakeDriver();
    const e = await newExec(d);
    const failing = async (s: Statement): Promise<unknown> => {
      try {
        for await (const _ of iterate(e, s)) {
          // drain
        }
      } catch (err) {
        return classOf(err);
      }
      return "none";
    };
    expect(await failing(stmt("FAIL 23505"))).toBe("UniqueViolation");
    d.handle = rows([1], [2]);
    d.fail.set("next", new Error("broken"));
    expect(await failing(stmt("SELECT"))).toBe("ConnectionError");
    // An error outside the taxonomy, such as an unknown catalog, has no class.
    expect(await failing(stmt("SELECT", { catalog: "nope" }))).toBe(undefined);
    // A stream in a transaction runs on its connection.
    d.handle = rows([1]);
    d.calls();
    await e.write(async (tx) => {
      for await (const r of iterate(tx, stmt("SELECT"))) {
        expect(r.values[0]).toBe(1);
      }
    });
    expect(d.calls()).toBe("pg#4 begin; pg#4 query SELECT []; pg#4 commit; pg#4 put back");
  });
});

describe("bulk writes (§25.2)", () => {
  const limited: Dialect = { ...pg, maxParams: 5 };

  /** insert writes rows [from, to) as one statement, answering a row per row. */
  const insert = (n: number, paramsPerRow = 2): Bulk => ({
    rows: n,
    paramsPerRow,
    statement: (from, to) => stmt(`INSERT ${from}-${to}`, { writes: true }),
  });

  test("a write within the limit runs alone", async () => {
    const d = new FakeDriver();
    const e = await newExec(d, limited);
    expect(await executeBulk(e, insert(2))).toBe(1);
    expect(d.calls()).toBe("pg#2 exec INSERT 0-2 []; pg#2 put back");
    // No rows run nothing; without a limit, every row goes in one statement.
    expect(await executeBulk(e, insert(0))).toBe(0);
    const free = await newExec(new FakeDriver());
    expect(await executeBulk(free, insert(100))).toBe(1);
    expect(await executeBulk(free, insert(100, 0))).toBe(1);
  });

  test("a larger write splits within one transaction", async () => {
    const d = new FakeDriver();
    const e = await newExec(d, limited);
    expect(await executeBulk(e, insert(5))).toBe(3);
    expect(d.calls()).toBe("pg#2 begin; pg#2 exec INSERT 0-2 []; pg#2 exec INSERT 2-4 []; pg#2 exec INSERT 4-5 []; pg#2 commit; pg#2 put back");
    // A row that binds more than the limit still goes one per statement.
    d.handle = rows([1]);
    expect((await queryBulk(e, insert(2, 9))).length).toBe(2);
    expect(d.calls()).toBe("pg#3 begin; pg#3 query INSERT 0-1 []; pg#3 query INSERT 1-2 []; pg#3 commit; pg#3 put back");
  });

  test("a write splits at the dialect's VALUES rows too", async () => {
    const d = new FakeDriver();
    // The row limit binds first, as SQL Server's 1000 rows do for small rows.
    const e = await newExec(d, { ...pg, maxParams: 8, maxRows: 2 });
    expect(await executeBulk(e, insert(5))).toBe(3);
    expect(d.calls()).toBe("pg#2 begin; pg#2 exec INSERT 0-2 []; pg#2 exec INSERT 2-4 []; pg#2 exec INSERT 4-5 []; pg#2 commit; pg#2 put back");
    // Rows that bind nothing split at the row limit alone.
    expect(await executeBulk(await newExec(new FakeDriver(), { ...pg, maxRows: 2 }), insert(3, 0))).toBe(2);
  });

  test("a failing chunk rolls back every chunk, and a rerun starts over", async () => {
    const d = new FakeDriver();
    const e = await newExec(d, limited);
    let runs = 0;
    const b: Bulk = {
      rows: 4,
      paramsPerRow: 2,
      statement: (from, to) => stmt(from === 2 && ++runs === 1 ? "FAIL 40001" : `INSERT ${from}-${to}`),
    };
    d.handle = rows([1]);
    expect((await queryBulk(e, b)).length).toBe(2);
    expect(await executeBulk(e, { ...b, statement: () => stmt("FAIL 23505") }).catch(classOf)).toBe("UniqueViolation");
  });

  test("a bulk write joins the transaction its handle or the context carries", async () => {
    const d = new FakeDriver();
    const e = await newExec(d, limited);
    await e.write(async (tx) => {
      await execute(tx, stmt("FIRST"));
      await executeBulk(tx, insert(4));
    });
    expect(d.calls()).toBe("pg#2 begin; pg#2 exec FIRST []; pg#2 exec INSERT 0-2 []; pg#2 exec INSERT 2-4 []; pg#2 commit; pg#2 put back");
    const pair = await e.beginWrite();
    await executeBulk(pair, insert(4));
    await pair.commit();
    expect(d.calls()).toBe("pg#3 begin; pg#3 exec INSERT 0-2 []; pg#3 exec INSERT 2-4 []; pg#3 commit; pg#3 put back");
    // atomic returns what its function does.
    expect(await atomic(e, async () => 9)).toBe(9);
  });
});
