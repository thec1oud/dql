import { afterEach, describe, expect, test } from "bun:test";
import { Row } from "../src/decode.js";
import type { Sent } from "../src/driver.js";
import { classOf } from "../src/errors.js";
import { type Config, Executor, type SourceConfig, UsageError } from "../src/executor.js";
import { Consistency, Session, currentSession, execute, queryAll, queryOne, rawWrite, withSession, withTimeout } from "../src/index.js";
import { pg } from "./executor.test.js";
import { FakeDriver, FakeNative } from "./fake-driver.js";

const replicated = { ...pg, replication: { position: "POSITION", lag: "LAG", replayed: "REPLAYED" } };

/** Cluster answers a primary pg and replicas r1, r2 and r3: lag each replica's lag in seconds, replayed whether it has replayed, and position the primary's position. */
class Cluster {
  lag: Record<string, unknown> = { r1: 0, r2: 0, r3: 0 };
  replayed: Record<string, unknown> = {};
  position: unknown = "0/1";
  /** Statements that fail, as "dsn sql". */
  readonly failed = new Set<string>();

  serve = (dsn: string, s: Sent): Row[] => {
    if (this.failed.has(`${dsn} ${s.sql}`)) {
      throw new Error(`${dsn} failed ${s.sql}`);
    }
    if (s.sql.startsWith("FAIL ")) {
      throw new FakeNative({ code: s.sql.slice(5) });
    }
    switch (s.sql) {
      case "LAG":
        return [new Row([this.lag[dsn] ?? null])];
      case "REPLAYED":
        return [new Row([this.replayed[dsn] ?? false])];
      case "POSITION":
        return [new Row([this.position])];
    }
    return [new Row([dsn])];
  };
}

const source: SourceConfig = {
  primary: { dsn: "pg", maxConns: 3 },
  replicas: [
    { dsn: "r1", maxConns: 2 },
    { dsn: "r2", maxConns: 2 },
    { dsn: "r3", maxConns: 2 },
  ],
};

let open: Executor[] = [];

afterEach(async () => {
  for (const e of open) {
    await e.close();
  }
  open = [];
});

async function cluster(c: Cluster, config: Partial<Config> = {}, trackInterval = 3_600_000): Promise<[FakeDriver, Executor]> {
  const d = new FakeDriver();
  d.serve = c.serve;
  const e = await Executor.open(d, replicated, { sources: { main: source }, catalogs: { shop: "main" }, ...config }, { trackInterval });
  open.push(e);
  d.calls();
  return [d, e];
}

const select = (consistency?: Consistency) => ({ query: "q", catalog: "shop", sql: "SELECT", ...(consistency === undefined ? {} : { consistency }) });

async function served(e: Executor, c?: Consistency): Promise<unknown> {
  return (await queryOne(e, select(c))).values[0];
}

function rotation(e: Executor): string {
  return (e.sources[0]?.replicas ?? []).map((r, i) => (r.in ? `r${i + 1}` : "-")).join(" ");
}

describe("read consistency (§22.5)", () => {
  test("replicas need a dialect that routes to them", async () => {
    const err = (await Executor.open(new FakeDriver(), pg, { sources: { main: source } }).catch((x) => x)) as UsageError;
    expect([err.reason, err.message]).toEqual(["no_replication", "dql: source main: dql: the dialect cannot route reads to replicas"]);
    // A replica that cannot open fails the source, closing what opened.
    const d = new FakeDriver();
    d.openError = new Error("bad");
    expect(classOf(await Executor.open(d, replicated, { sources: { main: source } }).catch((x) => x))).toBe("ConnectionError");
    const down = new FakeDriver();
    down.down.add("pg");
    expect(classOf(await Executor.open(down, replicated, { sources: { main: source } }).catch((x) => x))).toBe("ConnectionError");
    expect(down.pools.map((p) => p.closed)).toEqual([true, true, true, true]);
  });

  test("the tracker keeps replicas within max lag in rotation", async () => {
    const c = new Cluster();
    c.lag = { r1: 0, r2: null, r3: 5 };
    const [, e] = await cluster(c);
    expect(rotation(e)).toBe("r1 - -");
    c.lag = { r1: 0, r2: 0.5, r3: 1 };
    c.failed.add("r1 LAG");
    await e.poll();
    expect(rotation(e)).toBe("- r2 r3");
    // A replica that does not connect leaves rotation.
    c.failed.clear();
    const [d2, e2] = await cluster(c);
    d2.down.add("r2");
    await e2.poll();
    expect(rotation(e2)).toBe("r1 - r3");
  });

  test("the tracker polls in the background until close", async () => {
    const c = new Cluster();
    const [, e] = await cluster(c, {}, 5);
    expect(rotation(e)).toBe("r1 r2 r3");
    c.lag = { r1: 10 };
    await new Promise((r) => setTimeout(r, 40));
    expect(rotation(e)).toBe("- - -");
    await e.close();
    c.lag = { r1: 0, r2: 0, r3: 0 };
    await new Promise((r) => setTimeout(r, 20));
    expect(rotation(e)).toBe("- - -");
    // A source without replicas runs no tracker.
    const plain = await Executor.open(new FakeDriver(), replicated, { sources: { main: { primary: { dsn: "pg" } } } }, { trackInterval: 1 });
    await plain.close();
  });

  test("levels route reads", async () => {
    const c = new Cluster();
    c.lag = { r1: 0, r2: 1, r3: 0.05 };
    const [d, e] = await cluster(c);
    // The default is primary, and writes never leave it.
    expect(await served(e)).toBe("pg");
    expect(await queryAll(e, { ...select(Consistency.any), writes: true })).toEqual([new Row(["pg"])]);
    // Any turns across the replicas in rotation.
    expect([await served(e, Consistency.any), await served(e, Consistency.any), await served(e, Consistency.any)]).toEqual(["r2", "r3", "r1"]);
    expect(await served(e, Consistency.bounded(100))).toBe("r3");
    expect(await served(e, Consistency.bounded(10))).toBe("r1");
    expect(await served(e, Consistency.primary)).toBe("pg");
    // A replica that does not connect leaves rotation, and the read moves on.
    d.down.add("r2");
    const reads = [await served(e, Consistency.any), await served(e, Consistency.any), await served(e, Consistency.any)];
    expect(reads).not.toContain("r2");
    expect(reads).not.toContain("pg");
    expect(rotation(e)).toBe("r1 - r3");
    // With none left, the read uses the primary.
    d.down.add("r1");
    d.down.add("r3");
    expect(await served(e, Consistency.any)).toBe("pg");
    // The executor's configured level applies to reads that set none.
    const [, configured] = await cluster(new Cluster(), { consistency: Consistency.any });
    expect(await served(configured)).not.toBe("pg");
    // Raw SQL reads the primary.
    expect((await rawWrite(configured, "main").query("SELECT"))[0]?.values[0]).toBe("pg");
  });

  test("read-your-writes follows the session's writes", async () => {
    const c = new Cluster();
    const [d, e] = await cluster(c);
    const ryw = Consistency.readYourWrites;
    // Without writes, any replica serves.
    expect(await served(e, ryw)).not.toBe("pg");
    await withSession(async () => {
      const session = currentSession() as Session;
      // The same session is kept.
      withSession(() => expect(currentSession()).toBe(session));
      await execute(e, { query: "q", catalog: "shop", sql: "UPDATE" });
      expect(session.token()).toBe("main=0%2F1");
      // No replica has replayed it: the primary serves, or the read fails.
      expect(await served(e, ryw)).toBe("pg");
      const err = await served(e, ryw.orFail()).catch((x) => x);
      expect([classOf(err), (err as Error).message]).toEqual(["ReplicaUnavailable", "dql: ReplicaUnavailable on source main in q (postgres)"]);
      // One that has serves.
      c.replayed = { r3: true };
      expect(await served(e, ryw)).toBe("r3");
      // A wait gives a replica time to replay.
      c.replayed = {};
      setTimeout(() => {
        c.replayed = { r2: true };
      }, 10);
      expect(await served(e, ryw.wait(1000))).toBe("r2");
      // A replica that fails to answer leaves rotation.
      c.replayed = {};
      c.failed.add("r1 REPLAYED");
      expect(await served(e, ryw)).toBe("pg");
      expect(rotation(e)).toBe("- r2 r3");
      // A wait stops at the context's deadline.
      expect(await withTimeout(20, () => served(e, ryw.wait(10_000)))).toBe("pg");
      d.calls();
    });
    await withSession(async () => {
      // A position that cannot be read sends the session's reads to the primary.
      c.position = null;
      await execute(e, { query: "q", catalog: "shop", sql: "UPDATE" });
      c.replayed = { r2: true, r3: true };
      expect(await served(e, ryw)).toBe("pg");
      c.failed.add("pg POSITION");
      c.position = "0/2";
      await execute(e, { query: "q", catalog: "shop", sql: "UPDATE" });
      expect(currentSession()?.token()).toBe("main=");
    });
  });

  test("transactions route and record", async () => {
    const c = new Cluster();
    const [d, e] = await cluster(c);
    expect(await e.read((tx) => queryAll(tx, select()), { consistency: Consistency.any })).toEqual([new Row(["r2"])]);
    expect(await e.read((tx) => queryAll(tx, select()))).toEqual([new Row(["pg"])]);
    await withSession(async () => {
      await e.write((tx) => execute(tx, { query: "q", catalog: "shop", sql: "UPDATE" }));
      expect(currentSession()?.token()).toBe("main=0%2F1");
      // A failed write records nothing.
      c.position = "0/9";
      await e.write((tx) => execute(tx, { query: "q", catalog: "shop", sql: "FAIL 23505" })).catch(() => {});
      expect(currentSession()?.token()).toBe("main=0%2F1");
    });
    // Without a session nothing is read.
    d.calls();
    await execute(e, { query: "q", catalog: "shop", sql: "UPDATE" });
    expect(d.calls()).toBe("pg#5 exec UPDATE []; pg#5 put back");
  });
});

describe("sessions", () => {
  test("tokens carry positions between requests, as Go's do", () => {
    const s = new Session();
    s.record("main", "0/16B3748");
    s.record("main", "0/16B3748");
    s.record("a b", "x*y~!'()");
    expect(s.token()).toBe("a+b=x%2Ay~%21%27%28%29&main=0%2F16B3748");
    const t = new Session();
    t.import(s.token());
    expect(t.token()).toBe(s.token());
    expect(t.pending("main")).toEqual([["0/16B3748"], false]);
    expect(t.pending("none")).toEqual([[], false]);
    t.record("main", "");
    expect(t.pending("main")).toEqual([[], true]);
  });
});
