import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { Row } from "../src/decode.js";
import type { Conn } from "../src/driver.js";
import { classOf } from "../src/errors.js";
import {
  type Config,
  type Dialect,
  Executor,
  type ExecutorOptions,
  ReadTx,
  UsageError,
  WriteTx,
  Slots,
  savepoints,
} from "../src/executor.js";
import {
  RAW_QUERY,
  type Statement,
  currentSignal,
  execute,
  queryAll,
  queryOne,
  queryOptional,
  raw,
  rawWrite,
  withSignal,
  withTimeout,
} from "../src/index.js";
import { FakeDriver, FakeNative, type FakePool, rows } from "./fake-driver.js";

export const pg: Dialect = {
  name: "postgres",
  setup: ["SET TIME ZONE 'UTC'"],
  errors: {
    codes: {
      "23505": "UniqueViolation",
      "23503": "ForeignKeyViolation",
      "23514": "CheckViolation",
      "23502": "NotNullViolation",
      "23P01": "ExclusionViolation",
      "40001": "SerializationFailure",
      "40P01": "Deadlock",
      "55P03": "LockTimeout",
      "57014": "Timeout",
      "08006": "ConnectionError",
    },
    violations: [
      [
        { catalog: "shop", schema: "app", object: "users", class: "UniqueViolation", name: "users_tenant_email_key" },
        { name: "shop.app.users.users_tenant_email_key", kind: "unique", sensitive: false },
      ],
      [
        { catalog: "shop", schema: "app", object: "users", class: "CheckViolation", name: "users_check" },
        { name: "shop.app.users.users_check", kind: "check", sensitive: true },
      ],
    ],
  },
};

const main: Config = { sources: { main: { primary: { dsn: "pg", maxConns: 3 } } }, catalogs: { shop: "main" } };

let open: Executor[] = [];

async function newExec(d: FakeDriver, c: Config = main, o: ExecutorOptions = {}): Promise<Executor> {
  const e = await Executor.open(d, pg, c, { random: () => 0, ...o });
  open.push(e);
  d.calls();
  return e;
}

beforeEach(() => {
  savepoints.count = 0;
});

afterEach(async () => {
  for (const e of open) {
    await e.close();
  }
  open = [];
});

const stmt = (sql: string, more: Partial<Statement> = {}): Statement => ({ query: "q", catalog: "shop", sql, ...more });

/** failure returns what fn throws. */
async function failure(fn: () => Promise<unknown>): Promise<unknown> {
  try {
    await fn();
  } catch (e) {
    return e;
  }
  throw new Error("no error");
}

async function message(fn: () => Promise<unknown>): Promise<string> {
  return ((await failure(fn)) as Error).message;
}

describe("open (§22.1)", () => {
  test("opens sources in name order, connecting each once", async () => {
    const d = new FakeDriver();
    process.env["SHOP_URL"] = "pg://shop";
    const e = await Executor.open(d, pg, {
      sources: { main: { primary: { dsn: "env:SHOP_URL", maxConns: 2 } }, analytics: { primary: { dsn: "pg://wh" }, statementMode: "describe" } },
      catalogs: { shop: "main" },
    });
    expect(d.calls()).toBe(
      `open pg://wh max 0 mode describe setup ["SET TIME ZONE 'UTC'"]; pg://wh#1 put back; ` +
        `open pg://shop max 2 mode prepare setup ["SET TIME ZONE 'UTC'"]; pg://shop#1 put back`,
    );
    await e.close();
    expect(d.calls()).toBe("close pg://wh; close pg://shop");
  });

  test("fails", async () => {
    const one = (dsn: string): Config => ({ sources: { main: { primary: { dsn, maxConns: 1 } } } });
    expect(await message(() => Executor.open(new FakeDriver(), pg, one("env:NOPE_NOT_SET")))).toBe(
      "dql: source main: environment variable NOPE_NOT_SET is not set",
    );
    const bad = new FakeDriver();
    bad.openError = new Error("bad dsn");
    expect(await message(() => Executor.open(bad, pg, one("x")))).toBe("dql: ConnectionError on source main (postgres): bad dsn");
    const refused = new FakeDriver();
    refused.fail.set("acquire", new Error("refused"));
    expect(await message(() => Executor.open(refused, pg, one("x")))).toBe("dql: ConnectionError on source main (postgres): refused");
    expect(refused.pools[0]?.closed).toBe(true);
    const unknown = new FakeDriver();
    expect(await message(() => Executor.open(unknown, pg, { ...one("x"), catalogs: { shop: "other" } }))).toBe(
      "dql: catalog shop names source other, which is not configured",
    );
    // Sources opened before a failure are closed.
    expect(unknown.pools[0]?.closed).toBe(true);
  });

  test("statement modes (§26.4)", async () => {
    const dialect: Dialect = { ...pg, statementModes: ["prepare", "describe"] };
    const c = (mode: "prepare" | "describe" | "simple"): Config => ({ sources: { main: { primary: { dsn: "p" }, statementMode: mode } } });
    await (await Executor.open(new FakeDriver(), dialect, c("describe"))).close();
    const e = (await failure(() => Executor.open(new FakeDriver(), dialect, c("simple")))) as UsageError;
    expect(e).toBeInstanceOf(UsageError);
    expect(e.reason).toBe("statement_mode");
    expect(e.message).toBe("dql: source main: dql: the dialect does not support the statement mode: simple");
    // A dialect that declares none allows none.
    expect(await failure(() => Executor.open(new FakeDriver(), { ...pg, statementModes: [] }, c("prepare")))).toBeInstanceOf(UsageError);
  });
});

describe("statements", () => {
  test("query, one, optional and execute (§19)", async () => {
    const d = new FakeDriver();
    const e = await newExec(d);
    expect(await queryAll(e, stmt("SELECT", { args: [7], types: ["pg_catalog.int4"] }))).toEqual([new Row([1])]);
    expect(d.calls()).toBe("pg#2 query SELECT [7]; pg#2 put back");
    d.handle = rows();
    expect(await queryAll(e, stmt("SELECT"))).toEqual([]);
    const nf = await failure(() => queryOne(e, stmt("SELECT")));
    expect([classOf(nf), (nf as Error).message]).toEqual(["NotFound", "dql: NotFound in q (postgres)"]);
    expect(await queryOptional(e, stmt("SELECT"))).toBe(undefined);
    d.handle = rows([1], [2]);
    expect(classOf(await failure(() => queryOne(e, stmt("SELECT"))))).toBe("TooManyRows");
    // A proven statement reads one row and stops.
    expect(await queryOne(e, stmt("SELECT", { atMostOne: true }))).toEqual(new Row([1]));
    d.calls();
    expect(await execute(e, stmt("UPDATE"))).toBe(2);
    expect(d.calls()).toBe("pg#8 exec UPDATE []; pg#8 put back");
    expect(classOf(await failure(() => execute(e, stmt("FAIL 23505"))))).toBe("UniqueViolation");
    expect(classOf(await failure(() => queryAll(e, stmt("FAIL 40001"))))).toBe("SerializationFailure");
    d.fail.set("next", new Error("broken"));
    expect(classOf(await failure(() => queryAll(e, stmt("SELECT"))))).toBe("ConnectionError");
    expect(await message(() => queryAll(e, stmt("SELECT", { catalog: "nope" })))).toBe("dql: no source serves catalog nope");
  });

  test("raw SQL on a source by name (§17, §22.1)", async () => {
    const d = new FakeDriver();
    const e = await newExec(d);
    expect(await raw(e, "main").query("SELECT $1", 1)).toEqual([new Row([1])]);
    expect(await rawWrite(e, "main").query("INSERT RETURNING", 2)).toEqual([new Row([1])]);
    expect(await rawWrite(e, "main").exec("DELETE")).toBe(1);
    expect(d.calls()).toBe('pg#2 query SELECT $1 [1]; pg#2 put back; pg#3 query INSERT RETURNING [2]; pg#3 put back; pg#4 exec DELETE []; pg#4 put back');
    const u = (await failure(() => raw(e, "nope").query("SELECT"))) as UsageError;
    expect([u.reason, u.message]).toEqual(["unknown_source", "dql: no source is named: nope"]);
    expect(classOf(await failure(() => raw(e, "main").query("FAIL 23505")))).toBe("UniqueViolation");
    expect(RAW_QUERY).toBe("raw");
    d.calls();
    // Raw SQL in a transaction runs in it.
    await e.write(async (tx) => {
      await rawWrite(tx, "main").exec("UPDATE");
    });
    expect(d.calls()).toBe("pg#6 begin; pg#6 exec UPDATE []; pg#6 commit; pg#6 put back");
  });
});

describe("transactions (§22.3)", () => {
  test("closures commit, roll back and return", async () => {
    const d = new FakeDriver();
    const e = await newExec(d);
    expect(await e.write(async (tx) => (await queryAll(tx, stmt("SELECT"))).length, { isolation: "serializable" })).toBe(1);
    expect(d.calls()).toBe("pg#2 begin serializable; pg#2 query SELECT []; pg#2 commit; pg#2 put back");
    const boom = new Error("boom");
    expect(
      await failure(() =>
        e.write(async (tx) => {
          await execute(tx, stmt("UPDATE"));
          throw boom;
        }),
      ),
    ).toBe(boom);
    expect(d.calls()).toBe("pg#3 begin; pg#3 exec UPDATE []; pg#3 rollback; pg#3 put back");
    // A transaction that runs nothing takes no connection.
    expect(await e.read(() => 5)).toBe(5);
    expect(d.calls()).toBe("");
    // The executor joins the context's transaction.
    await e.read(async () => {
      await queryAll(e, stmt("SELECT"));
      await queryAll(e, stmt("SELECT"));
    });
    expect(d.calls()).toBe("pg#4 begin read only; pg#4 query SELECT []; pg#4 query SELECT []; pg#4 commit; pg#4 put back");
  });

  test("handles are typed by capability (§22.2)", async () => {
    const d = new FakeDriver();
    const e = await newExec(d);
    await e.read(async (tx) => {
      expect(tx).toBeInstanceOf(ReadTx);
      // @ts-expect-error a read transaction cannot write
      const err = (await failure(() => execute(tx, stmt("UPDATE")))) as UsageError;
      expect(err.reason).toBe("read_only");
      expect(err.message).toBe("dql: a write transaction cannot join a read transaction");
      // Nor can the executor in its context.
      expect(((await failure(() => execute(e, stmt("UPDATE")))) as UsageError).reason).toBe("read_only");
      expect(((await failure(() => e.write(() => 1))) as UsageError).reason).toBe("read_only");
      expect(((await failure(() => e.write(() => 1, { propagation: "nested" }))) as UsageError).reason).toBe("read_only");
    });
  });

  test("propagation", async () => {
    const d = new FakeDriver();
    const e = await newExec(d);
    expect(((await failure(() => e.write(() => 1, { propagation: "mandatory" }))) as UsageError).message).toBe(
      "dql: a mandatory transaction found no transaction to join",
    );
    await e.write(async (outer) => {
      await execute(outer, stmt("A"));
      // Join and mandatory reuse the transaction; their commit does nothing.
      await e.write(async (tx) => {
        await execute(tx, stmt("B"));
        await tx.commit();
        await tx.rollback();
      });
      await e.read((tx) => queryAll(tx, stmt("C")), { propagation: "mandatory" });
      expect(((await failure(() => e.write(() => 1, { propagation: "never" }))) as UsageError).message).toBe(
        "dql: a transaction that must not nest began inside one",
      );
      // Independent runs on its own connection.
      await e.write((tx) => execute(tx, stmt("D")), { propagation: "independent" });
    });
    expect(d.calls()).toBe(
      "pg#2 begin; pg#2 exec A []; pg#2 exec B []; pg#2 query C []; pg#3 begin; pg#3 exec D []; pg#3 commit; pg#3 put back; pg#2 commit; pg#2 put back",
    );
    await e.write(() => 1, { propagation: "never" });
  });

  test("nested savepoints", async () => {
    const d = new FakeDriver();
    const e = await newExec(d);
    // Without an enclosing transaction, nested begins one.
    await e.write((tx) => execute(tx, stmt("A")), { propagation: "nested" });
    expect(d.calls()).toBe("pg#2 begin; pg#2 exec A []; pg#2 commit; pg#2 put back");
    const boom = new Error("boom");
    await e.write(async (tx) => {
      await execute(tx, stmt("A"));
      expect(
        await e.write(async (inner) => {
          // Savepoints are made on first use, outermost first.
          await e.read(async (innermost) => {
            await queryAll(innermost, stmt("B"));
          }, { propagation: "nested" });
          return execute(inner, stmt("C"));
        }, { propagation: "nested" }),
      ).toBe(1);
      expect(
        await failure(() =>
          e.write(async (inner) => {
            await execute(inner, stmt("D"));
            throw boom;
          }, { propagation: "nested" }),
        ),
      ).toBe(boom);
      // A savepoint nothing used is never made.
      await e.write(() => 1, { propagation: "nested" });
    });
    expect(d.calls()).toBe(
      "pg#3 begin; pg#3 exec A []; pg#3 savepoint dql_1; pg#3 savepoint dql_2; pg#3 query B []; pg#3 releaseSavepoint dql_2; " +
        "pg#3 exec C []; pg#3 releaseSavepoint dql_1; pg#3 savepoint dql_3; pg#3 exec D []; pg#3 rollbackTo dql_3; pg#3 releaseSavepoint dql_3; " +
        "pg#3 commit; pg#3 put back",
    );
    // Failures making or ending a savepoint.
    await e.write(async () => {
      d.fail.set("savepoint", new FakeNative({ code: "40001" }));
      expect(classOf(await failure(() => e.write((tx) => execute(tx, stmt("E")), { propagation: "nested" })))).toBe("SerializationFailure");
      d.fail.set("releaseSavepoint", new FakeNative({ code: "08006" }));
      expect(classOf(await failure(() => e.write((tx) => execute(tx, stmt("F")), { propagation: "nested" })))).toBe("ConnectionError");
      // A failure rolling back to a savepoint leaves the closure's error.
      d.fail.set("rollbackTo", new Error("lost"));
      expect(
        await failure(() =>
          e.write(async (tx) => {
            await execute(tx, stmt("G"));
            throw boom;
          }, { propagation: "nested" }),
        ),
      ).toBe(boom);
    }).catch(() => {});
  });

  test("the pair form", async () => {
    const d = new FakeDriver();
    const e = await newExec(d);
    const tx = await e.beginWrite();
    expect(tx).toBeInstanceOf(WriteTx);
    await execute(tx, stmt("A"));
    // run puts the transaction in the context.
    await tx.run(() => execute(e, stmt("B")));
    await tx.commit();
    await tx.rollback();
    expect(d.calls()).toBe("pg#2 begin; pg#2 exec A []; pg#2 exec B []; pg#2 commit; pg#2 put back");
    const done = (await failure(() => tx.commit())) as UsageError;
    expect([done.reason, done.message]).toEqual(["done", "dql: the transaction has already committed or rolled back"]);
    expect(((await failure(() => execute(tx, stmt("C")))) as UsageError).reason).toBe("done");
    const r = await e.beginRead({ isolation: "repeatable read" });
    await queryAll(r, stmt("SELECT"));
    await r.rollback();
    expect(d.calls()).toBe("pg#3 begin read only repeatable read; pg#3 query SELECT []; pg#3 rollback; pg#3 put back");
    // A pair-form transaction in a closure joins it.
    await e.write(async () => {
      const joined = await e.beginWrite();
      await execute(joined, stmt("D"));
      await joined.commit();
      await joined.rollback();
    });
    expect(d.calls()).toBe("pg#4 begin; pg#4 exec D []; pg#4 commit; pg#4 put back");
  });

  test("operations roll back leaked transactions", async () => {
    const d = new FakeDriver();
    const e = await newExec(d);
    let committed!: WriteTx;
    expect(
      await e.operation(async () => {
        const leaked = await e.beginRead();
        await queryAll(leaked, stmt("SELECT"));
        committed = await e.beginWrite({ propagation: "independent" });
        await execute(committed, stmt("UPDATE"));
        await committed.commit();
        return 7;
      }),
    ).toBe(7);
    expect(d.calls()).toBe("pg#2 begin read only; pg#2 query SELECT []; pg#3 begin; pg#3 exec UPDATE []; pg#3 commit; pg#3 put back; pg#2 rollback; pg#2 put back");
    // A failing rollback does not hide the operation's result.
    d.fail.set("rollback", new Error("lost"));
    expect(
      await e.operation(async () => {
        await queryAll(await e.beginRead(), stmt("SELECT"));
        return 8;
      }),
    ).toBe(8);
  });

  test("ending a root that spans sources", async () => {
    const d = new FakeDriver();
    const e = await newExec(d, {
      sources: { main: { primary: { dsn: "pg" } }, other: { primary: { dsn: "wh" } } },
      catalogs: { shop: "main", events: "other" },
    });
    d.fail.set("commit", new FakeNative({ code: "40001" }));
    const err = await failure(() =>
      e.write(
        async (tx) => {
          await execute(tx, stmt("A"));
          await execute(tx, stmt("B", { catalog: "events" }));
        },
        { retry: { attempts: 1, initial: 0, max: 0 } },
      ),
    );
    expect(classOf(err)).toBe("SerializationFailure");
    // Commit stops at the first failure and rolls back the rest.
    expect(d.calls()).toBe("pg#2 begin; pg#2 exec A []; wh#2 begin; wh#2 exec B []; pg#2 commit; pg#2 put back; wh#2 rollback; wh#2 put back");
  });

  test("beginning fails", async () => {
    const d = new FakeDriver();
    const e = await newExec(d);
    d.fail.set("begin", new Error("broken"));
    expect(classOf(await failure(() => e.write((tx) => execute(tx, stmt("A")))))).toBe("ConnectionError");
    expect(d.calls()).toBe("pg#2 begin; pg#2 put back");
    d.fail.set("acquire", new Error("broken"));
    expect(classOf(await failure(() => e.write((tx) => execute(tx, stmt("A")))))).toBe("ConnectionError");
    // A root that ends while a connection begins rolls it back.
    let release!: () => void;
    d.gate = new Promise((r) => {
      release = r;
    });
    const tx = await e.beginWrite();
    const pending = execute(tx, stmt("A"));
    const ended = tx.rollback();
    // Its rollback failing changes nothing.
    d.fail.set("rollback", new Error("lost"));
    release();
    d.gate = undefined;
    await ended;
    expect(((await failure(() => pending)) as UsageError).reason).toBe("done");
  });

  test("concurrent statements share the transaction's connection", async () => {
    const d = new FakeDriver();
    const e = await newExec(d);
    await e.write(async (tx) => {
      await Promise.all([execute(tx, stmt("A")), execute(tx, stmt("B"))]);
      await e.write(async (inner) => {
        await Promise.all([execute(inner, stmt("C")), execute(inner, stmt("D"))]);
      }, { propagation: "nested" });
    });
    expect(d.calls()).toBe(
      "pg#2 begin; pg#2 exec A []; pg#2 exec B []; pg#2 savepoint dql_1; pg#2 exec C []; pg#2 exec D []; pg#2 releaseSavepoint dql_1; pg#2 commit; pg#2 put back",
    );
  });
});

describe("retries (§22.4)", () => {
  test("a root closure reruns after a serialization failure or deadlock", async () => {
    const d = new FakeDriver();
    const e = await newExec(d);
    let runs = 0;
    expect(
      await e.write(async (tx) => {
        runs++;
        await execute(tx, stmt(runs < 3 ? (runs === 1 ? "FAIL 40001" : "FAIL 40P01") : "OK"));
        return runs;
      }),
    ).toBe(3);
    // The last run's error reports how many ran.
    runs = 0;
    const err = (await failure(() => e.write((tx) => execute(tx, stmt("FAIL 40001"))))) as Error;
    expect([classOf(err), err.message]).toEqual(["SerializationFailure", "dql: SerializationFailure in q (postgres 40001) (after 3 attempts)"]);
    // Other errors and joined closures do not rerun.
    expect(classOf(await failure(() => e.write((tx) => execute(tx, stmt("FAIL 55P03")))))).toBe("LockTimeout");
    runs = 0;
    await failure(() =>
      e.write(() =>
        e.write(async (tx) => {
          runs++;
          await execute(tx, stmt("FAIL 40001"));
        }),
      ),
    );
    expect(runs).toBe(3);
    // A closure's policy overrides the executor's.
    runs = 0;
    await failure(() =>
      e.write(
        async (tx) => {
          runs++;
          await execute(tx, stmt("FAIL 40001"));
        },
        { retry: { attempts: 5, initial: 1, max: 1 } },
      ),
    );
    expect(runs).toBe(5);
  });

  test("retries stop at the deadline", async () => {
    const d = new FakeDriver();
    const e = await newExec(d, main, { random: () => 1 });
    let runs = 0;
    await withTimeout(50, () =>
      failure(() =>
        e.write(
          async (tx) => {
            runs++;
            await execute(tx, stmt("FAIL 40001"));
          },
          { retry: { attempts: 5, initial: 1000, max: 1000 } },
        ),
      ),
    );
    expect(runs).toBe(1);
  });
});

describe("independent transactions and pools (§22.6)", () => {
  test("an independent transaction fails fast when the pool is exhausted", async () => {
    const d = new FakeDriver();
    const e = await newExec(d, { sources: { main: { primary: { dsn: "pg", maxConns: 1 } } }, catalogs: { shop: "main" } }, { independentTimeout: 5 });
    const err = await failure(() =>
      e.write(async (tx) => {
        await execute(tx, stmt("A"));
        await e.write((inner) => execute(inner, stmt("B")), { propagation: "independent" });
      }),
    );
    expect([classOf(err), (err as Error).message]).toEqual(["PoolExhausted", "dql: PoolExhausted on source main in q (postgres)"]);
    // A canceled wait is Canceled, not PoolExhausted.
    const ac = new AbortController();
    const canceled = failure(() =>
      e.write(async (tx) => {
        await execute(tx, stmt("A"));
        await withSignal(ac.signal, () => e.write((inner) => execute(inner, stmt("B")), { propagation: "independent" }));
      }),
    );
    ac.abort();
    expect(classOf(await canceled)).toBe("Canceled");
  });

  test("independent transactions nest at most so deep", async () => {
    const d = new FakeDriver();
    const e = await newExec(d, main, { independentDepth: 1 });
    const err = (await failure(() =>
      e.write(() => e.write(() => e.write(() => 1, { propagation: "independent" }), { propagation: "independent" }), { propagation: "independent" }),
    )) as UsageError;
    expect([err.reason, err.message]).toEqual(["independent_depth", "dql: independent transactions nest too deep"]);
  });

  test("a reserve keeps connections for independent transactions", async () => {
    const d = new FakeDriver();
    const e = await newExec(d, { sources: { main: { primary: { dsn: "pg", maxConns: 2 }, independentReserve: 1 } }, catalogs: { shop: "main" } });
    // One general connection is out; the next waits for it, and the
    // independent transaction takes the reserved one.
    let order: string[] = [];
    const tx = await e.beginWrite();
    await execute(tx, stmt("A"));
    const waiting = execute(e, stmt("B")).then(() => order.push("general"));
    await tx.run(() =>
      e.write(async (inner) => {
        await execute(inner, stmt("C"));
        await e.write(async (n) => {
          await execute(n, stmt("D"));
          await queryAll(n, stmt("E"));
        }, { propagation: "nested" });
        order.push("independent");
      }, { propagation: "independent" }),
    );
    expect(order).toEqual(["independent"]);
    await tx.commit();
    await waiting;
    expect(order).toEqual(["independent", "general"]);
    // A canceled wait for a general slot gives up its place.
    order = [];
    const ac = new AbortController();
    const holder = await e.beginWrite();
    await execute(holder, stmt("A"));
    const gone = withSignal(ac.signal, () => failure(() => execute(e, stmt("B"))));
    ac.abort();
    expect(classOf(await gone)).toBe("Canceled");
    await holder.commit();
    // A failed acquire frees its slot.
    d.fail.set("acquire", new Error("broken"));
    expect(classOf(await failure(() => execute(e, stmt("A"))))).toBe("ConnectionError");
    await execute(e, stmt("A"));
    await execute(e, stmt("A"));
    // The reserve's connections begin, commit and roll back like any other.
    await e.write(async (tx) => {
      await execute(tx, stmt("A"));
      await e.write(async (n) => {
        await execute(n, stmt("B"));
        throw new Error("undo");
      }, { propagation: "nested" }).catch(() => {});
    });
    expect(await e.write(async (tx) => (await queryAll(tx, stmt("F"))).length)).toBe(1);
    await failure(() => e.write(async (tx) => {
      await execute(tx, stmt("A"));
      throw new Error("undo");
    }));
  });
});

describe("the context", () => {
  test("signals cancel and time out statements", async () => {
    const d = new FakeDriver();
    const e = await newExec(d);
    expect(currentSignal()).toBe(undefined);
    const ac = new AbortController();
    ac.abort(new Error("stop"));
    const canceled = await withSignal(ac.signal, () => failure(() => queryAll(e, stmt("SELECT"))));
    expect([classOf(canceled), (canceled as Error).message]).toEqual(["Canceled", "dql: Canceled in q (postgres): stop"]);
    const timedOut = await withTimeout(1, async () => {
      await new Promise((r) => setTimeout(r, 5));
      return failure(() => queryAll(e, stmt("SELECT")));
    });
    expect(classOf(timedOut)).toBe("Timeout");
    // Signals and deadlines join the enclosing ones.
    const outer = new AbortController();
    await withSignal(outer.signal, () =>
      withSignal(new AbortController().signal, async () => {
        outer.abort();
        expect(currentSignal()?.aborted).toBe(true);
      }),
    );
    await withTimeout(1000, () =>
      withTimeout(10_000, async () => {
        const e2 = await failure(() => withTimeout(1, () => new Promise((_, reject) => setTimeout(() => reject(currentSignal()?.reason), 5))));
        expect((e2 as Error).name).toBe("TimeoutError");
      }),
    );
  });
});

describe("pool waits", () => {
  test("slots and pools wait in order and give up when aborted", async () => {
    const slots = new Slots(0);
    const gone = AbortSignal.abort("gone");
    expect(await slots.take(gone).catch((e) => e)).toBe("gone");
    const first = slots.take(undefined);
    slots.give();
    await first;
    const d = new FakeDriver();
    const e = await newExec(d, { sources: { main: { primary: { dsn: "pg", maxConns: 1 } } }, catalogs: { shop: "main" } });
    const tx = await e.beginWrite();
    await execute(tx, stmt("A"));
    const waiting = execute(e, stmt("B"));
    await tx.commit();
    expect(await waiting).toBe(1);
    const pool = d.pools[0] as FakePool;
    const held = await pool.acquire();
    expect(await pool.acquire(gone).catch((x) => x)).toBe("gone");
    held.release();
    // An independent transaction's failed acquire is the driver's error.
    d.fail.set("acquire", new Error("broken"));
    expect(classOf(await failure(() => e.write((t) => execute(t, stmt("A")), { propagation: "independent" })))).toBe("ConnectionError");
  });
});

describe("the driver's connections", () => {
  test("the fake records every call", async () => {
    const d = new FakeDriver();
    const e = await newExec(d);
    const c: Conn = await e.conn(e.sources[0] as never, false, "q");
    await c.rollbackTo("x");
    c.release();
    expect(d.calls()).toBe("pg#2 rollbackTo x; pg#2 put back");
  });
});
