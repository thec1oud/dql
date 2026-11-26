import { AsyncLocalStorage } from "node:async_hooks";
import { afterEach, describe, expect, test } from "bun:test";
import { Row } from "../src/decode.js";
import type { Sent } from "../src/driver.js";
import { type Config, Executor, type ExecutorOptions } from "../src/executor.js";
import {
  Consistency,
  type Hooks,
  Leaked,
  type Logger,
  type QueryEvent,
  Redacted,
  type TxEvent,
  execute,
  formatDuration,
  logHooks,
  queryAll,
  queryOne,
  currentSession,
  rawWrite,
  withSession,
  withSignal,
} from "../src/index.js";
import { pg } from "./executor.test.js";
import { FakeDriver, FakeNative } from "./fake-driver.js";

const replicated = { ...pg, replication: { position: "POSITION", lag: "LAG", replayed: "REPLAYED" } };

const main: Config = { sources: { main: { primary: { dsn: "pg", maxConns: 3 } } }, catalogs: { shop: "main" } };

let open: Executor[] = [];

afterEach(async () => {
  for (const e of open) {
    await e.close();
  }
  open = [];
});

async function newExec(d: FakeDriver, hooks: Hooks[], c: Config = main, o: ExecutorOptions = {}): Promise<Executor> {
  const e = await Executor.open(d, replicated, c, { random: () => 0.5, trackInterval: 3_600_000, hooks, ...o });
  open.push(e);
  return e;
}

/** query writes a query event without its duration. */
function query(q: QueryEvent): string {
  const parts = [q.query, q.kind, q.dialect, q.catalog, q.source, q.consistency.toString(), `attempt ${q.attempt}`, q.sql];
  if (q.replica) {
    parts.push("replica");
  }
  if (q.params !== undefined) {
    parts.push(`params ${q.params.map(String).join(",")}`);
  }
  parts.push(`rows ${q.rows}`);
  if (q.error !== undefined) {
    parts.push(`error ${q.class ?? "-"} ${(q.error as Error).message}`);
  }
  return parts.join(" ");
}

/** tx writes a transaction event without its durations. */
function tx(t: TxEvent): string {
  const parts: string[] = [t.propagation, t.isolation ?? "-", t.readOnly ? "read" : "write", `attempt ${t.attempt}`];
  if (t.savepoint) {
    parts.push("savepoint");
  }
  if (t.outcome !== undefined) {
    parts.push(t.outcome);
  }
  if (t.delay !== undefined) {
    parts.push(`delay ${t.delay}`);
  }
  if (t.error !== undefined) {
    parts.push(`error ${(t.error as Error).message}`);
  }
  return parts.join(" ");
}

/** Recorder records every event as a line, and numbers the states its start hooks return. */
class Recorder {
  lines: string[] = [];
  private n = 0;

  hooks(params = false): Hooks<number> {
    return {
      params,
      queryStart: (q) => {
        this.lines.push(`query start ${query(q)}`);
        return ++this.n;
      },
      queryEnd: (q, s) => this.lines.push(`query end #${s} ${query(q)}`),
      txBegin: (t) => {
        this.lines.push(`tx begin ${tx(t)}`);
        return ++this.n;
      },
      txEnd: (t, s) => this.lines.push(`tx end #${s} ${tx(t)}`),
      txRetry: (t, s) => this.lines.push(`tx retry #${s} ${tx(t)}`),
      txLeaked: (t, s) => this.lines.push(`tx leaked #${s} ${tx(t)}`),
      poolWait: (p) => this.lines.push(`pool wait ${p.source}${p.replica ? " replica" : ""} ${p.wait >= 0}`),
      rotation: (r) => this.lines.push(`rotation ${r.source} ${r.replica} lag ${r.lag} ${r.in ? "in" : "out"}`),
    };
  }

  take(): string[] {
    const l = this.lines;
    this.lines = [];
    return l;
  }
}

const stmt = (sql: string, more: Record<string, unknown> = {}) => ({ query: "q", kind: "select", catalog: "shop", sql, ...more });

describe("query events (§24)", () => {
  test("a statement's start and end", async () => {
    const r = new Recorder();
    const e = await newExec(new FakeDriver(), [r.hooks()]);
    r.take();
    await queryAll(e, stmt("SELECT", { args: [1, "secret"], sensitive: [false, true] }));
    expect(r.take()).toEqual([
      "query start q select postgres shop main primary attempt 1 SELECT rows 0",
      "pool wait main true",
      "query end #1 q select postgres shop main primary attempt 1 SELECT rows 1",
    ]);
    // A write and a failure, with its class; a statement without a kind has "".
    await execute(e, { query: "w", catalog: "shop", sql: "UPDATE", consistency: Consistency.any });
    expect(await execute(e, stmt("FAIL 23505")).catch((x) => (x as Error).name)).toBe("UniqueViolation");
    expect(r.take()).toEqual([
      "query start w  postgres shop main primary attempt 1 UPDATE rows 0",
      "pool wait main true",
      "query end #2 w  postgres shop main primary attempt 1 UPDATE rows 1",
      "query start q select postgres shop main primary attempt 1 FAIL 23505 rows 0",
      "pool wait main true",
      "query end #3 q select postgres shop main primary attempt 1 FAIL 23505 rows 0 error UniqueViolation dql: UniqueViolation of shop in q (postgres 23505)",
    ]);
    // A raw statement on a source that is not configured names none; an error without a class has none.
    await rawWrite(e, "nowhere")
      .exec("DELETE")
      .catch(() => {});
    const d = new FakeDriver();
    const e2 = await newExec(d, [r.hooks()]);
    d.handle = () => {
      throw "odd";
    };
    await queryAll(e2, stmt("SELECT")).catch(() => {});
    expect(r.take()).toEqual([
      "query start raw raw postgres   primary attempt 1 DELETE rows 0",
      "query end #4 raw raw postgres   primary attempt 1 DELETE rows 0 error - dql: no source is named: nowhere",
      "query start q select postgres shop main primary attempt 1 SELECT rows 0",
      "pool wait main true",
      "query end #5 q select postgres shop main primary attempt 1 SELECT rows 0 error DatabaseError dql: DatabaseError in q (postgres)",
    ]);
  });

  test("parameters reach only the hooks that ask, redacted where sensitive", async () => {
    const plain = new Recorder();
    const withParams = new Recorder();
    const e = await newExec(new FakeDriver(), [plain.hooks(), withParams.hooks(true)]);
    plain.take();
    withParams.take();
    await queryOne(e, stmt("SELECT", { args: [1, "secret"], sensitive: [false, true] }));
    expect(plain.take()[0]).toBe("query start q select postgres shop main primary attempt 1 SELECT rows 0");
    expect(withParams.take()[0]).toBe("query start q select postgres shop main primary attempt 1 SELECT params 1,[redacted] rows 0");
    await queryOne(e, stmt("SELECT"));
    expect(withParams.take()[0]).toBe("query start q select postgres shop main primary attempt 1 SELECT params  rows 0");
    expect([String(Redacted), JSON.stringify({ v: Redacted })]).toEqual(["[redacted]", '{"v":"[redacted]"}']);
  });

  test("a call that opts out fires no events, its pool wait included", async () => {
    const r = new Recorder();
    const e = await newExec(new FakeDriver(), [r.hooks()]);
    r.take();
    await queryAll(e, stmt("SELECT", { noLog: true }));
    await execute(e, stmt("UPDATE", { noLog: true }));
    expect(r.take()).toEqual([]);
    // An executor without hooks fires none either.
    const quiet = await Executor.open(new FakeDriver(), pg, main);
    open.push(quiet);
    expect(await queryAll(quiet, stmt("SELECT"))).toHaveLength(1);
  });

  test("consistency and replicas", async () => {
    const r = new Recorder();
    const d = new FakeDriver();
    d.serve = (dsn: string, s: Sent) => (s.sql === "LAG" ? [new Row([0])] : [new Row([dsn])]);
    const c: Config = { sources: { main: { primary: { dsn: "pg" }, replicas: [{ dsn: "r1" }] } }, catalogs: { shop: "main" }, consistency: Consistency.any };
    const e = await newExec(d, [r.hooks()], c);
    expect(r.take()).toEqual(["rotation main 0 lag 0 in"]);
    // The executor's level; a read on a replica.
    await queryAll(e, stmt("SELECT"));
    expect(r.take()).toEqual([
      "query start q select postgres shop main any attempt 1 SELECT rows 0",
      "pool wait main replica true",
      "query end #1 q select postgres shop main any attempt 1 SELECT replica rows 1",
    ]);
    // A read transaction's level, on a replica.
    await e.read((t) => queryAll(t, stmt("SELECT", { consistency: Consistency.primary })), { consistency: Consistency.bounded(1500) });
    expect(r.take().filter((l) => l.startsWith("query end"))).toEqual(["query end #3 q select postgres shop main bounded(1.5s) attempt 1 SELECT replica rows 1"]);
    // A read transaction without one, and a read in a write transaction, take the statement's or primary.
    await e.read((t) => queryAll(t, stmt("SELECT", { consistency: Consistency.readYourWrites.wait(250).orFail() })));
    await e.write((t) => queryAll(t, stmt("SELECT", { consistency: Consistency.any })));
    expect(r.take().filter((l) => l.startsWith("query end"))).toEqual([
      "query end #5 q select postgres shop main read-your-writes.wait(250ms).or-fail attempt 1 SELECT replica rows 1",
      "query end #7 q select postgres shop main primary attempt 1 SELECT rows 1",
    ]);
  });
});

describe("transaction events (§24)", () => {
  test("closures begin and end, with their outcome", async () => {
    const r = new Recorder();
    const e = await newExec(new FakeDriver(), [r.hooks()]);
    r.take();
    await e.write((t) => execute(t, stmt("UPDATE")), { isolation: "serializable" });
    expect(r.take()).toEqual([
      "tx begin join serializable write attempt 1",
      "query start q select postgres shop main primary attempt 1 UPDATE rows 0",
      "pool wait main true",
      "query end #2 q select postgres shop main primary attempt 1 UPDATE rows 1",
      "tx end #1 join serializable write attempt 1 commit",
    ]);
    // A failing closure rolls back with its error; a joined one begins none.
    const boom = new Error("boom");
    await e
      .read(() =>
        e.read(() => {
          throw boom;
        }),
      )
      .catch(() => {});
    expect(r.take()).toEqual(["tx begin join - read attempt 1", "tx end #3 join - read attempt 1 rollback error boom"]);
    // A failing commit ends in a rollback with the commit's error.
    const d = new FakeDriver();
    const e2 = await newExec(d, [r.hooks()]);
    d.fail.set("commit", new FakeNative({ code: "40001" }));
    await e2.write((t) => execute(t, stmt("UPDATE", { noLog: true })), { retry: { attempts: 1, initial: 0, max: 0 } }).catch(() => {});
    expect(r.take().filter((l) => l.startsWith("tx"))).toEqual([
      "tx begin join - write attempt 1",
      "tx end #4 join - write attempt 1 rollback error dql: SerializationFailure (postgres 40001)",
    ]);
  });

  test("a rerun fires a retry, and each attempt begins anew", async () => {
    const r = new Recorder();
    const e = await newExec(new FakeDriver(), [r.hooks()]);
    r.take();
    let runs = 0;
    await e.write(async (t) => {
      runs++;
      await execute(t, stmt(runs === 1 ? "FAIL 40001" : "OK", { noLog: true }));
    });
    expect(r.take()).toEqual([
      "tx begin join - write attempt 1",
      "tx end #1 join - write attempt 1 rollback error dql: SerializationFailure in q (postgres 40001)",
      "tx retry #1 join - write attempt 1 delay 5 error dql: SerializationFailure in q (postgres 40001)",
      "tx begin join - write attempt 2",
      "tx end #2 join - write attempt 2 commit",
    ]);
    // A query in a rerun reports its attempt.
    runs = 0;
    await e.write(async (t) => {
      runs++;
      await execute(t, stmt(runs === 1 ? "FAIL 40P01" : "OK"));
    });
    expect(r.take().filter((l) => l.startsWith("query end"))).toEqual([
      "query end #4 q select postgres shop main primary attempt 1 FAIL 40P01 rows 0 error Deadlock dql: Deadlock in q (postgres 40P01)",
      "query end #6 q select postgres shop main primary attempt 2 OK rows 1",
    ]);
    // No retry fires when the backoff would pass the deadline.
    const late = await newExec(new FakeDriver(), [r.hooks()], main, { random: () => 0.99 });
    const { withTimeout } = await import("../src/index.js");
    await withTimeout(1, () => late.write((t) => execute(t, stmt("FAIL 40001", { noLog: true })))).catch(() => {});
    expect(r.take().filter((l) => l.startsWith("tx retry"))).toEqual([]);
    // A signal that aborts during the backoff stops the rerun after the retry fired.
    const ac = new AbortController();
    const stopped = withSignal(ac.signal, () => late.write((t) => execute(t, stmt("FAIL 40001", { noLog: true })))).catch((x) => (x as Error).message);
    setTimeout(() => ac.abort(), 1);
    expect(await stopped).toBe("dql: SerializationFailure in q (postgres 40001)");
    expect(r.take().filter((l) => l.startsWith("tx retry"))).toHaveLength(1);
    // A rollback that fails does not hide the closure's error.
    const d = new FakeDriver();
    const lost = await newExec(d, [r.hooks()]);
    d.fail.set("rollback", new Error("lost"));
    expect(await lost.write((t) => execute(t, stmt("FAIL 55P03", { noLog: true }))).catch((x) => (x as Error).name)).toBe("LockTimeout");
  });

  test("nested savepoints begin and end", async () => {
    const r = new Recorder();
    const d = new FakeDriver();
    const e = await newExec(d, [r.hooks()]);
    r.take();
    await e.write(async (t) => {
      await execute(t, stmt("A", { noLog: true }));
      await e.read(() => 1, { propagation: "nested" });
      await e.write(() => Promise.reject(new Error("inner")), { propagation: "nested" }).catch(() => {});
      await e.write((n) => execute(n, stmt("B", { noLog: true })), { propagation: "nested" });
      d.fail.set("releaseSavepoint", new Error("lost"));
      await e.write((n) => execute(n, stmt("C", { noLog: true })), { propagation: "nested" }).catch(() => {});
    });
    expect(r.take()).toEqual([
      "tx begin join - write attempt 1",
      "tx begin nested - read attempt 1 savepoint",
      "tx end #2 nested - read attempt 1 savepoint commit",
      "tx begin nested - write attempt 1 savepoint",
      "tx end #3 nested - write attempt 1 savepoint rollback error inner",
      "tx begin nested - write attempt 1 savepoint",
      "tx end #4 nested - write attempt 1 savepoint commit",
      "tx begin nested - write attempt 1 savepoint",
      "tx end #5 nested - write attempt 1 savepoint rollback error dql: DatabaseError (postgres)",
      "tx end #1 join - write attempt 1 commit",
    ]);
  });

  test("operations report leaked pair-form transactions", async () => {
    const r = new Recorder();
    const e = await newExec(new FakeDriver(), [r.hooks()]);
    r.take();
    await e.operation(async () => {
      const leaked = await e.beginWrite({ propagation: "independent" });
      await execute(leaked, stmt("UPDATE", { noLog: true }));
      const done = await e.beginRead();
      await done.commit();
    });
    expect(r.take()).toEqual([
      "tx begin independent - write attempt 1",
      "tx begin join - read attempt 1",
      "tx end #2 join - read attempt 1 commit",
      "tx leaked #1 independent - write attempt 1",
      "tx end #1 independent - write attempt 1 rollback error dql: a transaction was still open when its operation ended",
    ]);
    expect(new Leaked().name).toBe("Leaked");
  });
});

describe("activation", () => {
  test("closures and queries run with their hooks' states active", async () => {
    const active = new AsyncLocalStorage<string>();
    const seen: string[] = [];
    const tracing: Hooks<string> = {
      txBegin: (t) => `${active.getStore() ?? "root"}>tx${t.savepoint ? "-sp" : ""}`,
      queryStart: (q) => {
        seen.push(`start ${q.sql} in ${active.getStore() ?? "root"}`);
        return `${active.getStore() ?? "root"}>${q.sql}`;
      },
      poolWait: () => seen.push(`wait in ${active.getStore()}`),
      activate: (state, fn) => active.run(state as string, fn),
    };
    // A second set of hooks without activate runs inside the first's.
    const e = await newExec(new FakeDriver(), [tracing, { queryEnd: () => seen.push(`end in ${active.getStore()}`) }]);
    await e.write(async (t) => {
      await execute(t, stmt("A"));
      await e.write((n) => execute(n, stmt("B")), { propagation: "nested" });
    });
    const pair = await e.beginWrite();
    await pair.run(() => execute(e, stmt("C")));
    await pair.commit();
    expect(seen).toEqual([
      "start A in root>tx",
      "wait in root>tx>A",
      "end in root>tx>A",
      "start B in root>tx>tx-sp",
      "end in root>tx>tx-sp>B",
      "start C in root>tx",
      "wait in root>tx>C",
      "end in root>tx>C",
    ]);
  });
});

describe("pool and rotation events", () => {
  test("replicas entering and leaving rotation", async () => {
    const r = new Recorder();
    const d = new FakeDriver();
    const lag: Record<string, unknown> = { r1: 0, r2: 3 };
    d.serve = (dsn: string, s: Sent) => {
      if (s.sql === "REPLAYED") {
        throw new Error("lost");
      }
      return s.sql === "LAG" ? [new Row([lag[dsn] ?? null])] : [new Row([dsn])];
    };
    const c: Config = { sources: { main: { primary: { dsn: "pg" }, replicas: [{ dsn: "r1" }, { dsn: "r2" }] } }, catalogs: { shop: "main" } };
    const e = await newExec(d, [r.hooks()], c);
    expect(r.take()).toEqual(["rotation main 0 lag 0 in"]);
    lag["r2"] = 1;
    await e.poll();
    await e.poll();
    expect(r.take()).toEqual(["rotation main 1 lag 1000 in"]);
    // A replica that does not connect, or cannot answer, leaves rotation.
    d.down.add("r2");
    await queryAll(e, stmt("SELECT", { consistency: Consistency.any, noLog: true }));
    expect(r.take()).toEqual(["rotation main 1 lag 1000 out"]);
    await withSession(async () => {
      currentSession()?.record("main", "0/1");
      await queryAll(e, stmt("SELECT", { consistency: Consistency.readYourWrites, noLog: true }));
    });
    expect(r.take()).toEqual(["rotation main 0 lag 0 out"]);
  });

  test("the logger hooks", async () => {
    const lines: string[] = [];
    const at =
      (level: string) =>
      (message: string, attrs: Record<string, unknown>): void => {
        const { duration, wait, ...rest } = attrs;
        lines.push(`${level} ${message} ${JSON.stringify(rest)}${duration === undefined ? "" : " +duration"}${wait === undefined ? "" : " +wait"}`);
      };
    const l: Logger = { debug: at("debug"), info: at("info"), warn: at("warn"), error: at("error") };
    const d = new FakeDriver();
    const lag: Record<string, unknown> = { r1: 0 };
    d.serve = (dsn: string, s: Sent) => {
      if (s.sql.startsWith("FAIL ")) {
        throw new FakeNative({ code: s.sql.slice(5) });
      }
      if (s.sql === "THROW") {
        throw "odd";
      }
      return s.sql === "LAG" ? [new Row([lag[dsn] ?? null])] : [new Row([dsn])];
    };
    d.handle = (s) => d.serve?.("pg", s) ?? [];
    const c: Config = { sources: { main: { primary: { dsn: "pg" }, replicas: [{ dsn: "r1" }] } }, catalogs: { shop: "main" } };
    const e = await newExec(d, [logHooks(l, { params: true })], c);
    const quiet = await newExec(d, [logHooks(l)], c);
    lag["r1"] = 9;
    await e.poll();
    await quiet.operation(async () => {
      await quiet.beginWrite();
      await queryAll(quiet, stmt("SELECT", { args: [1] }));
    });
    let runs = 0;
    await e.write(async (t) => {
      runs++;
      await execute(t, stmt(runs === 1 ? "FAIL 40001" : "OK", { args: ["x", "y"], sensitive: [false, true] }));
    });
    await queryAll(e, stmt("THROW", { noLog: false })).catch(() => {});
    const err = new Error("e");
    await e.read(() => Promise.reject(err)).catch(() => {});
    expect(lines).toEqual([
      'info dql replica entered rotation {"source":"main","replica":0,"lag":0}',
      'info dql replica entered rotation {"source":"main","replica":0,"lag":0}',
      'info dql replica left rotation {"source":"main","replica":0,"lag":9000}',
      'debug dql transaction begin {"propagation":"join","isolation":"","read_only":false,"savepoint":false,"attempt":1}',
      'debug dql pool wait {"source":"main","replica":false} +wait',
      'debug dql query {"query":"q","kind":"select","dialect":"postgres","catalog":"shop","source":"main","replica":false,"consistency":"primary","attempt":1,"rows":1,"sql":"SELECT"} +duration',
      'warn dql transaction leaked {"propagation":"join","isolation":"","read_only":false,"savepoint":false,"attempt":1} +duration',
      'debug dql transaction end {"propagation":"join","isolation":"","read_only":false,"savepoint":false,"attempt":1,"outcome":"rollback"} +duration',
      'debug dql transaction begin {"propagation":"join","isolation":"","read_only":false,"savepoint":false,"attempt":1}',
      'debug dql pool wait {"source":"main","replica":false} +wait',
      'warn dql query {"query":"q","kind":"select","dialect":"postgres","catalog":"shop","source":"main","replica":false,"consistency":"primary","attempt":1,"rows":0,"sql":"FAIL 40001","params":["x","[redacted]"],"class":"SerializationFailure","error":"dql: SerializationFailure in q (postgres 40001)"} +duration',
      'debug dql transaction end {"propagation":"join","isolation":"","read_only":false,"savepoint":false,"attempt":1,"outcome":"rollback"} +duration',
      'info dql transaction retry {"propagation":"join","isolation":"","read_only":false,"savepoint":false,"attempt":1,"delay":5,"class":"SerializationFailure"}',
      'debug dql transaction begin {"propagation":"join","isolation":"","read_only":false,"savepoint":false,"attempt":2}',
      'debug dql pool wait {"source":"main","replica":false} +wait',
      'debug dql query {"query":"q","kind":"select","dialect":"postgres","catalog":"shop","source":"main","replica":false,"consistency":"primary","attempt":2,"rows":1,"sql":"OK","params":["x","[redacted]"]} +duration',
      'debug dql transaction end {"propagation":"join","isolation":"","read_only":false,"savepoint":false,"attempt":2,"outcome":"commit"} +duration',
      'debug dql pool wait {"source":"main","replica":false} +wait',
      'warn dql query {"query":"q","kind":"select","dialect":"postgres","catalog":"shop","source":"main","replica":false,"consistency":"primary","attempt":1,"rows":0,"sql":"THROW","params":[],"class":"DatabaseError","error":"dql: DatabaseError in q (postgres)"} +duration',
      'debug dql transaction begin {"propagation":"join","isolation":"","read_only":true,"savepoint":false,"attempt":1}',
      'debug dql transaction end {"propagation":"join","isolation":"","read_only":true,"savepoint":false,"attempt":1,"outcome":"rollback"} +duration',
    ]);
  });
});

describe("formatting", () => {
  test("durations read as Go's", () => {
    expect([0, 1.5, 250, 1000, 1500, 60_000, 90_500, 3_600_000, 0.5, 0.0005, -2000].map(formatDuration)).toEqual([
      "0s",
      "1.5ms",
      "250ms",
      "1s",
      "1.5s",
      "1m0s",
      "1m30.5s",
      "1h0m0s",
      "500µs",
      "500ns",
      "-2s",
    ]);
  });

  test("consistency levels read as Go's", () => {
    expect(
      [Consistency.primary, Consistency.any, Consistency.bounded(2000), Consistency.readYourWrites, Consistency.readYourWrites.wait(100).orFail()].map(String),
    ).toEqual(["primary", "any", "bounded(2s)", "read-your-writes", "read-your-writes.wait(100ms).or-fail"]);
  });
});
