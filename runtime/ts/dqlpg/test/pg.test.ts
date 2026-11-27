import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import {
  Decimal,
  type Dialect,
  Executor,
  OffsetTime,
  Row,
  Temporal,
  classOf,
  execute,
  queryAll,
  queryOne,
  raw,
  rawWrite,
  Consistency,
  currentSession,
  withSession,
  withSignal,
  withTimeout,
  iterate,
  queryBulk,
  executeBulk,
  type Bulk,
} from "@dql/runtime";
import pg from "pg";
import { CommitRolledBack, PgDriver, encode, literal } from "../src/index.js";

// The tests run against the Postgres server DQL_TEST_POSTGRES_URL names, in a
// fresh database; they fail rather than skip without one, since they keep the
// package at full coverage.
const base = process.env["DQL_TEST_POSTGRES_URL"];
if (base === undefined || base === "") {
  throw new Error("DQL_TEST_POSTGRES_URL is not set; these tests need a Postgres server, such as postgres://postgres@localhost:5432/postgres");
}

const schema = `
CREATE TYPE mood AS ENUM ('sad', 'happy');
CREATE DOMAIN positive AS int4 CHECK (VALUE > 0);
CREATE TYPE point2 AS (x int4, tags text[], p positive);
CREATE TYPE pair AS (p point2, m mood, j json);
CREATE TYPE floatrange AS RANGE (subtype = float8, multirange_type_name = floatmultirange);
CREATE TABLE item (
  id int8 PRIMARY KEY,
  name text NOT NULL CONSTRAINT name_set CHECK (name <> ''),
  mood mood,
  moods mood[]
);
CREATE UNIQUE INDEX item_name ON item (name);
CREATE TABLE tag (item int8 REFERENCES item (id));
`;

const dialect: Dialect = {
  name: "postgres",
  setup: ["SET TIME ZONE 'UTC'"],
  errors: {
    codes: { "23505": "UniqueViolation", "23503": "ForeignKeyViolation", "23514": "CheckViolation", "23502": "NotNullViolation", "57014": "Timeout", "57P01": "ConnectionError", "40001": "SerializationFailure" },
    violations: [],
  },
};

const name = `dqlpg_test_${process.pid}_${Date.now()}`;
let url = "";
let admin: pg.Client;
const executors: Executor[] = [];

async function open(mode: "prepare" | "describe" = "prepare", maxConns = 4): Promise<Executor> {
  const e = await Executor.open(new PgDriver(), dialect, {
    sources: { main: { primary: { dsn: url, maxConns }, statementMode: mode } },
    catalogs: { shop: "main" },
  });
  executors.push(e);
  return e;
}

beforeAll(async () => {
  admin = new pg.Client({ connectionString: base });
  await admin.connect();
  await admin.query(`CREATE DATABASE ${name} TEMPLATE template0 ENCODING 'UTF8' LOCALE 'C'`);
  const u = new URL(base);
  u.pathname = `/${name}`;
  url = u.toString();
  const c = new pg.Client({ connectionString: url });
  await c.connect();
  await c.query(schema);
  await c.end();
});

afterAll(async () => {
  for (const e of executors) {
    await e.close();
  }
  await admin.query(`DROP DATABASE ${name} WITH (FORCE)`);
  await admin.end();
});

const q = (sql: string, args: unknown[] = []) => ({ query: "q", catalog: "shop", sql, args });

describe("values", () => {
  test("built-in types decode to their host types", async () => {
    for (const mode of ["prepare", "describe"] as const) {
      const e = await open(mode);
      const [r] = await queryAll(
        e,
        q(`SELECT true, 'x'::bytea, 9007199254740993::int8, 7::int2, 8::int4, 26::oid, 1.5::float4, 'NaN'::float8, 'Infinity'::float8, '-Infinity'::float8,
          '12.50'::numeric, 'NaN'::numeric, '-$1,234.50'::money, '{"a":[1,null]}'::json, '[true]'::jsonb, 'text', 'v'::varchar, 'c'::char(2),
          '2024-02-29'::date, 'infinity'::date, '12:34:56.5'::time, '24:00:00'::time, '2024-01-02 03:04:05.123456'::timestamp,
          '2024-01-02 03:04:05+05:30'::timestamptz, '1 year 2 mons 3 days 04:05:06.789'::interval, '12:34:56+02'::timetz, '12:34:56+05:30'::timetz,
          '7d5fe6ea-a2f6-4a5b-8e5c-1f1f2e7b0a1d'::uuid, '10.0.0.1'::inet, '10.0.0.0/8'::cidr, '08:00:2b:01:02:03'::macaddr, B'101'::bit(3), '<a/>'::xml,
          NULL::int4`),
      );
      expect(r?.values).toEqual([
        true,
        new Uint8Array([0x78]),
        9007199254740993n,
        7,
        8,
        26,
        1.5,
        Number.NaN,
        Number.POSITIVE_INFINITY,
        Number.NEGATIVE_INFINITY,
        Decimal.parse("12.50"),
        "NaN",
        Decimal.parse("-1234.50"),
        { a: [1, null] },
        [true],
        "text",
        "v",
        "c ",
        Temporal.PlainDate.from("2024-02-29"),
        "infinity",
        Temporal.PlainTime.from("12:34:56.5"),
        "24:00:00",
        Temporal.PlainDateTime.from("2024-01-02T03:04:05.123456"),
        Temporal.Instant.from("2024-01-01T21:34:05Z"),
        Temporal.Duration.from("P1Y2M3DT4H5M6.789S"),
        OffsetTime.parse("12:34:56+02:00"),
        OffsetTime.parse("12:34:56+05:30"),
        "7d5fe6ea-a2f6-4a5b-8e5c-1f1f2e7b0a1d",
        "10.0.0.1",
        "10.0.0.0/8",
        "08:00:2b:01:02:03",
        "101",
        "<a/>",
        null,
      ]);
    }
  });

  test("arrays, ranges, multiranges and catalog types", async () => {
    const e = await open();
    const [r] = await queryAll(
      e,
      q(`SELECT ARRAY[[1,NULL],[3,4]]::int4[], '{}'::text[], ARRAY['a b', 'c"d', NULL, 'NULL']::text[], '[0:1]={5,6}'::int4[],
        ARRAY['sad', 'happy']::mood[], 'happy'::mood, ROW(ROW(1, ARRAY['x','y'], 5)::point2, 'sad', '{"k":1}')::pair,
        '[1,5)'::int4range, 'empty'::int8range, '(,2024-01-02)'::daterange, '[1.5,2.5]'::numrange, '[1.5,)'::floatrange,
        '{[1,3), [5,7)}'::int4multirange, '{}'::int8multirange, '{[1,2)}'::floatmultirange, ARRAY[ROW(2, NULL, NULL)::point2], 3::positive, ARRAY[4::positive],
        ROW(NULL, NULL, NULL)::point2, ARRAY['[1,2)'::int4range], ARRAY['a'::bytea]`),
    );
    expect(r?.values).toEqual([
      [
        [1, null],
        [3, 4],
      ],
      [],
      ["a b", 'c"d', null, "NULL"],
      [5, 6],
      ["sad", "happy"],
      "happy",
      new Row([new Row([1, ["x", "y"], 5]), "sad", { k: 1 }]),
      { empty: false, lower: { kind: "inclusive", value: 1 }, upper: { kind: "exclusive", value: 5 } },
      { empty: true },
      { empty: false, lower: { kind: "unbounded" }, upper: { kind: "exclusive", value: Temporal.PlainDate.from("2024-01-02") } },
      { empty: false, lower: { kind: "inclusive", value: Decimal.parse("1.5") }, upper: { kind: "inclusive", value: Decimal.parse("2.5") } },
      { empty: false, lower: { kind: "inclusive", value: 1.5 }, upper: { kind: "unbounded" } },
      [
        { empty: false, lower: { kind: "inclusive", value: 1 }, upper: { kind: "exclusive", value: 3 } },
        { empty: false, lower: { kind: "inclusive", value: 5 }, upper: { kind: "exclusive", value: 7 } },
      ],
      [],
      [{ empty: false, lower: { kind: "inclusive", value: 1 }, upper: { kind: "exclusive", value: 2 } }],
      [new Row([2, null, null])],
      3,
      [4],
      new Row([null, null, null]),
      [{ empty: false, lower: { kind: "inclusive", value: 1 }, upper: { kind: "exclusive", value: 2 } }],
      [new Uint8Array([0x61])],
    ]);
  });

  test("host values round-trip as parameters", async () => {
    const e = await open("describe");
    const cases: [string, unknown, unknown?][] = [
      ["bool", true],
      ["int8", 9007199254740993n],
      ["int4", 7],
      ["float8", 0.1],
      ["numeric", Decimal.parse("-0.001")],
      ["bytea", new Uint8Array([0, 255])],
      ["text", 'a "quoted" \\ value'],
      ["json", "just a string"],
      ["jsonb", { a: [1, "x"] }],
      ["date", Temporal.PlainDate.from("2024-02-29")],
      ["time", Temporal.PlainTime.from("01:02:03.5")],
      ["timestamp", Temporal.PlainDateTime.from("2024-01-02T03:04:05.5")],
      ["timestamptz", Temporal.Instant.from("2024-01-02T03:04:05Z")],
      ["timestamptz", Temporal.ZonedDateTime.from("2024-01-02T03:04:05+05:30[+05:30]"), Temporal.Instant.from("2024-01-01T21:34:05Z")],
      ["timestamptz", new Date("2024-01-02T03:04:05Z"), Temporal.Instant.from("2024-01-02T03:04:05Z")],
      ["interval", Temporal.Duration.from("P1Y2M3DT4H5M6S")],
      ["timetz", OffsetTime.parse("12:34:56+02:00")],
      ["int4[]", [[1, null], [3, 4]]],
      ["text[]", ["a,b", null, "{x}"]],
      ["bytea[]", [new Uint8Array([1])]],
      ["mood[]", ["sad"]],
      ["point2", new Row([1, ["x"], null])],
      ["pair", new Row([new Row([1, [], 2]), "happy", { k: [1] }])],
      ["int4range", { empty: false, lower: { kind: "inclusive", value: 1 }, upper: { kind: "exclusive", value: 5 } }],
      ["daterange", { empty: false, lower: { kind: "unbounded" }, upper: { kind: "exclusive", value: Temporal.PlainDate.from("2024-01-02") } }],
      ["int8range", { empty: true }],
      ["numrange", { empty: false, lower: { kind: "exclusive", value: Decimal.parse("1.5") }, upper: { kind: "inclusive", value: Decimal.parse("2") } }],
      ["int4range[]", [{ empty: true }]],
      ["text", null],
    ];
    for (const [type, v, want = v] of cases) {
      const r = await queryOne(e, { query: "q", catalog: "shop", sql: `SELECT $1::${type}`, args: [v], types: [type === "json" ? "pg_catalog.json" : ""] });
      expect([type, r.values[0]]).toEqual([type, want]);
    }
    // Declared types: JSON whatever its JavaScript type, a list of JSON documents, and a multirange.
    const typed = await queryOne(e, {
      query: "q",
      catalog: "shop",
      sql: "SELECT $1::jsonb, $2::jsonb[], $3::int4multirange",
      args: [[1, 2], [{ a: 1 }, null], [{ empty: false, lower: { kind: "inclusive", value: 1 }, upper: { kind: "exclusive", value: 3 } }]],
      types: ["pg_catalog.jsonb", "pg_catalog._jsonb", "pg_catalog.int4multirange"],
    });
    expect(typed.values).toEqual([[1, 2], [{ a: 1 }, null], [{ empty: false, lower: { kind: "inclusive", value: 1 }, upper: { kind: "exclusive", value: 3 } }]]);
  });

  test("bytea in escape form stays text", async () => {
    const e = await open();
    const r = await e.write(async (tx) => {
      await execute(tx, q("SET LOCAL bytea_output = 'escape'"));
      return queryOne(tx, q("SELECT 'a'::bytea"));
    });
    expect(r.values).toEqual(["a"]);
  });

  test("literal and encode", () => {
    expect(literal(Object.create(null))).toBe("{}");
    expect(literal({ a: 1 })).toBe('{"a":1}');
    expect(literal([[1], [2]])).toBe('{{"1"},{"2"}}');
    expect(literal(new Row([null, undefined, "a"]))).toBe('(,,"a")');
    expect(encode(undefined, "")).toBe(null);
    expect(encode([null], "pg_catalog._json")).toBe("{NULL}");
  });
});

describe("statements and transactions", () => {
  test("writes, errors and transactions", async () => {
    for (const mode of ["prepare", "describe"] as const) {
      const e = await open(mode);
      await rawWrite(e, "main").exec("DELETE FROM tag");
      await rawWrite(e, "main").exec("DELETE FROM item");
      expect(await rawWrite(e, "main").exec("INSERT INTO item (id, name, mood, moods) VALUES ($1, $2, $3, $4), (2, 'b', NULL, NULL)", 1, "a", "happy", ["sad"])).toBe(2);
      expect((await raw(e, "main").query("SELECT mood, moods FROM item WHERE id = $1", 1))[0]?.values).toEqual(["happy", ["sad"]]);
      const dup = await rawWrite(e, "main").exec("INSERT INTO item (id, name) VALUES (3, 'a')").catch((x) => x);
      expect([classOf(dup), dup.constraint.name, dup.detail]).toEqual(["UniqueViolation", "public.item.item_name", ""]);
      const check = await rawWrite(e, "main").exec("INSERT INTO item (id, name) VALUES (3, '')").catch((x) => x);
      expect([classOf(check), check.constraint.name]).toEqual(["CheckViolation", "public.item.name_set"]);
      const nn = await rawWrite(e, "main").exec("INSERT INTO item (id) VALUES (3)").catch((x) => x);
      expect([classOf(nn), nn.column]).toEqual(["NotNullViolation", "public.item.name"]);
      const fk = await rawWrite(e, "main").exec("INSERT INTO tag VALUES (99)").catch((x) => x);
      expect(classOf(fk)).toBe("ForeignKeyViolation");
      const syntax = await raw(e, "main").query("SELEC 1").catch((x) => x);
      expect([classOf(syntax), syntax.origin.code]).toEqual(["DatabaseError", "42601"]);

      // A savepoint undoes its writes; the transaction commits the rest.
      await e.write(async (tx) => {
        await execute(tx, q("UPDATE item SET name = 'c' WHERE id = 1"));
        await e
          .write(async (inner) => {
            await execute(inner, q("UPDATE item SET name = 'd' WHERE id = 1"));
            throw new Error("undo");
          }, { propagation: "nested" })
          .catch(() => {});
        await e.write((inner) => execute(inner, q("UPDATE item SET name = 'e' WHERE id = 2")), { propagation: "nested" });
      }, { isolation: "serializable" });
      expect((await queryAll(e, q("SELECT name FROM item ORDER BY id"))).map((r) => r.values[0])).toEqual(["c", "e"]);
      // A read transaction cannot write.
      const ro = await e.read((tx) => queryAll(tx, q("DELETE FROM item RETURNING id"))).catch((x) => x);
      expect(ro.origin.code).toBe("25006");
      // A failed statement leaves the transaction aborted, and its commit rolls back.
      const tx = await e.beginWrite();
      await execute(tx, q("SELEC")).catch(() => {});
      const rolled = await tx.commit().catch((x) => x);
      expect([classOf(rolled), rolled.databaseMessage]).toEqual(["DatabaseError", new CommitRolledBack().message]);
    }
  });

  test("cancellation and timeouts reach the server", async () => {
    const e = await open();
    const timedOut = await withTimeout(50, () => queryAll(e, q("SELECT pg_sleep(5)"))).catch((x) => x);
    expect(classOf(timedOut)).toBe("Timeout");
    const ac = new AbortController();
    const pending = withSignal(ac.signal, () => e.write((tx) => queryAll(tx, q("SELECT pg_sleep(5)")))).catch((x) => x);
    setTimeout(() => ac.abort(), 50);
    expect(classOf(await pending)).toBe("Canceled");
    // The pool still serves statements.
    expect((await queryOne(e, q("SELECT 1"))).values).toEqual([1]);
  });

  test("waiting for a connection gives up when aborted", async () => {
    const e = await open("prepare", 1);
    const tx = await e.beginWrite();
    await execute(tx, q("SELECT 1"));
    const ac = new AbortController();
    const waiting = withSignal(ac.signal, () => queryAll(e, q("SELECT 1"))).catch((x) => x);
    setTimeout(() => ac.abort(), 20);
    expect(classOf(await waiting)).toBe("Canceled");
    const second = new AbortController();
    const fails = withSignal(second.signal, () => queryAll(e, q("SELECT 1"))).catch((x) => x);
    await tx.commit();
    expect((await fails)[0].values).toEqual([1]);
    // An already-aborted wait fails at once.
    const gone = await e.beginRead({ propagation: "independent" }).then((r) => withSignal(AbortSignal.abort(), () => queryAll(r, q("SELECT 1")))).catch((x) => x);
    expect(classOf(gone)).toBe("Canceled");
  });
});

describe("large results and inputs (§25)", () => {
  const values = async (it: AsyncIterable<Row>): Promise<(readonly unknown[])[]> => {
    const out: (readonly unknown[])[] = [];
    for await (const r of it) {
      out.push(r.values);
    }
    return out;
  };

  test("streams read batches through a cursor", async () => {
    const e = await open("prepare", 1);
    const all = await values(iterate(e, q("SELECT g, 'happy'::mood, ARRAY['sad']::mood[] FROM generate_series(1, $1::int4) g", [600])));
    expect(all.length).toBe(600);
    expect(all[599]).toEqual([600, "happy", ["sad"]]);
    // A batch that ends exactly at the end of the rows.
    expect((await values(iterate(e, q("SELECT g FROM generate_series(1, 256) g")))).length).toBe(256);
    // Breaking out closes the cursor; the one connection serves the next statement.
    for await (const r of iterate(e, q("SELECT g FROM generate_series(1, 1000) g"))) {
      expect(r.values).toEqual([1]);
      break;
    }
    expect((await queryOne(e, q("SELECT 2"))).values).toEqual([2]);
    // In a transaction, the stream sees its writes.
    await e.write(async (tx) => {
      await rawWrite(tx, "main").exec("INSERT INTO item (id, name) VALUES (500, 'streamed')");
      expect(await values(iterate(tx, q("SELECT name FROM item WHERE id = 500")))).toEqual([["streamed"]]);
      throw new Error("undo");
    }).catch(() => {});
  });

  test("a stream that fails discards its connection", async () => {
    const e = await open("describe", 1);
    const failed = await values(iterate(e, q("SELECT 1 / (300 - g) FROM generate_series(1, 600) g"))).catch((x) => x);
    expect([classOf(failed), failed.origin.code]).toEqual(["DatabaseError", "22012"]);
    expect((await queryOne(e, q("SELECT 3"))).values).toEqual([3]);
    // A canceled stream reaches the server.
    const ac = new AbortController();
    const canceled = withSignal(ac.signal, () =>
      values(iterate(e, q("SELECT g, CASE WHEN g = 300 THEN pg_sleep(5) END FROM generate_series(1, 600) g"))),
    ).catch((x) => x);
    setTimeout(() => ac.abort(), 100);
    expect(classOf(await canceled)).toBe("Canceled");
    expect((await queryOne(e, q("SELECT 4"))).values).toEqual([4]);
  });

  test("a list is one parameter, and bulk writes split", async () => {
    const e = await open();
    const ids = Array.from({ length: 100_000 }, (_, i) => BigInt(i));
    const counted = await queryOne(e, { ...q("SELECT count(*) FROM generate_series(0, 200000) g WHERE g = ANY($1)", [ids]), types: ["pg_catalog._int8"] });
    expect(counted.values).toEqual([100_000n]);
    const limited = await Executor.open(new PgDriver(), { ...dialect, maxParams: 4 }, { sources: { main: { primary: { dsn: url } } }, catalogs: { shop: "main" } });
    executors.push(limited);
    const bulk: Bulk = {
      rows: 5,
      paramsPerRow: 2,
      statement: (from, to) => {
        const rows = Array.from({ length: to - from }, (_, i) => `($${2 * i + 1}::int8, $${2 * i + 2})`).join(", ");
        const args = Array.from({ length: to - from }, (_, i) => [BigInt(1000 + from + i), `bulk ${from + i}`]).flat();
        return { ...q(`INSERT INTO item (id, name) VALUES ${rows} RETURNING id`, args), writes: true };
      },
    };
    expect((await queryBulk(limited, bulk)).map((r) => r.values[0])).toEqual([1000n, 1001n, 1002n, 1003n, 1004n]);
    const removal = (from: number, to: number) => q(`DELETE FROM item WHERE id >= ${1000 + from} AND id < ${1000 + to}`);
    expect(await executeBulk(limited, { ...bulk, statement: removal })).toBe(5);
  });
});

describe("replicas (§22.5)", () => {
  test("read-your-writes on a streaming replica", async () => {
    const replicaBase = process.env["DQL_TEST_POSTGRES_REPLICA_URL"];
    if (replicaBase === undefined || replicaBase === "") {
      throw new Error("DQL_TEST_POSTGRES_REPLICA_URL is not set; this test needs a streaming replica of DQL_TEST_POSTGRES_URL's server");
    }
    const r = new URL(replicaBase);
    r.pathname = `/${name}`;
    const replication = {
      position: "SELECT pg_current_wal_lsn()::text",
      lag: "SELECT (CASE WHEN NOT pg_is_in_recovery() THEN NULL WHEN pg_last_wal_receive_lsn() = pg_last_wal_replay_lsn() THEN 0 ELSE extract(epoch FROM now() - pg_last_xact_replay_timestamp()) END)::float8",
      replayed: "SELECT pg_last_wal_replay_lsn() >= ALL (SELECT p::pg_lsn FROM unnest($1::text[]) p)",
    };
    const e = await Executor.open(new PgDriver(), { ...dialect, replication }, {
      sources: { main: { primary: { dsn: url }, replicas: [{ dsn: r.toString() }], maxLag: 60_000 } },
      catalogs: { shop: "main" },
    });
    executors.push(e);
    const where = q("SELECT pg_is_in_recovery()");
    expect((await queryOne(e, { ...where, consistency: Consistency.any })).values).toEqual([true]);
    await withSession(async () => {
      await rawWrite(e, "main").exec("DELETE FROM tag");
      expect(currentSession()?.token()).toMatch(/^main=[0-9A-F]+%2F[0-9A-F]+$/);
      const read = await queryOne(e, { ...where, consistency: Consistency.readYourWrites.wait(5000) });
      expect(read.values).toEqual([true]);
    });
  });
});

describe("connections", () => {
  test("a failed connect with a signal is the driver's error", async () => {
    const pool = await new PgDriver().open({ dsn: "postgres://nobody@127.0.0.1:1/x", maxConns: 0, setup: [], mode: "prepare" });
    expect(await pool.acquire(new AbortController().signal).catch((x) => (x as { code?: string }).code)).toBe("ECONNREFUSED");
    await pool.close();
  });

  test("a cancel request that cannot connect leaves the statement to finish", async () => {
    const pool = await new PgDriver().open({ dsn: url, maxConns: 0, setup: [], mode: "describe" });
    const conn = await pool.acquire();
    (pool as unknown as { config: { connectionString: string } }).config.connectionString = "postgres://nobody@127.0.0.1:1/x";
    const ac = new AbortController();
    setTimeout(() => ac.abort(), 20);
    const rows = await conn.query({ query: "q", sql: "SELECT pg_sleep(0.2)", args: [], signal: ac.signal });
    expect(await rows.next()).toEqual(new Row([""]));
    conn.release();
    await pool.close();
  });

  test("a server that does not answer is a connection error", async () => {
    const err = await Executor.open(new PgDriver(), dialect, { sources: { main: { primary: { dsn: "postgres://nobody@127.0.0.1:1/x" } } } }).catch((x) => x);
    expect(classOf(err)).toBe("ConnectionError");
    const d = new PgDriver();
    expect(d.broken(new Error("Connection terminated unexpectedly"))).toBe(true);
    expect(d.broken(Object.assign(new Error("x"), { code: "ECONNRESET" }))).toBe(true);
    expect(d.broken(new Error("other"))).toBe(false);
    expect(d.broken("text")).toBe(false);
    expect(d.native(new Error("x"))).toBe(undefined);
  });

  test("setup failures discard the connection", async () => {
    const err = await Executor.open(new PgDriver(), { ...dialect, setup: ["SET nonsense = 1"] }, { sources: { main: { primary: { dsn: url } } } }).catch((x) => x);
    expect(classOf(err)).toBe("ConnectionError");
  });

  test("a terminated connection is discarded", async () => {
    const e = await open("prepare", 1);
    const tx = await e.beginWrite();
    const [pid] = (await queryOne(tx, q("SELECT pg_backend_pid()"))).values as [number];
    await admin.query("SELECT pg_terminate_backend($1)", [pid]);
    const err = await execute(tx, q("SELECT 1")).catch((x) => x);
    expect(classOf(err)).toBe("ConnectionError");
    // The broken connection fails what follows, and is discarded.
    expect(classOf(await execute(tx, q("SELECT 1")).catch((x) => x))).toBe("ConnectionError");
    await tx.rollback().catch(() => {});
    expect((await queryOne(e, q("SELECT 1"))).values).toEqual([1]);
    // An idle connection that breaks leaves its pool.
    await admin.query("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1", [name]);
    await new Promise((r) => setTimeout(r, 50));
    expect((await queryOne(e, q("SELECT 2"))).values).toEqual([2]);
  });
});
