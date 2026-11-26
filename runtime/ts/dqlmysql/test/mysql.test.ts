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
  withSignal,
  withTimeout,
  iterate,
  queryBulk,
  executeBulk,
  type Bulk,
  type InsertIdConn,
} from "@dql/runtime";
import mysql from "mysql2";
import { MonthsError, MySqlDriver, bitsValue, encode, native, time, timeText } from "../src/index.js";
import { decoder, types } from "../src/values.js";

// The tests run against the MySQL server DQL_TEST_MYSQL_URL names, in a fresh
// database; they fail rather than skip without one, since they keep the
// package at full coverage. The variable is in the Go driver's DSN syntax,
// user:password@tcp(host:port)/, which the conformance harnesses share.
const base = process.env["DQL_TEST_MYSQL_URL"];
if (base === undefined || base === "") {
  throw new Error("DQL_TEST_MYSQL_URL is not set; these tests need a MySQL server, such as root:root@tcp(localhost:3306)/");
}

/** mysqlURL turns a DSN in the Go driver's syntax into a URL of database db. */
function mysqlURL(dsn: string, db: string): string {
  const m = /^(?:([^:@]*)(?::([^@]*))?@)?(?:tcp\(([^)]*)\))?\/[^?]*/.exec(dsn);
  if (m === null) {
    throw new Error(`DQL_TEST_MYSQL_URL ${dsn} is not a DSN`);
  }
  const auth = m[1] === undefined ? "" : `${encodeURIComponent(m[1])}${m[2] === undefined ? "" : `:${encodeURIComponent(m[2])}`}@`;
  return `mysql://${auth}${m[3] ?? "127.0.0.1:3306"}/${db}`;
}

const schema = [
  `CREATE TABLE item (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    name VARCHAR(50) NOT NULL,
    mood ENUM('sad', 'happy'),
    CONSTRAINT name_set CHECK (name <> ''),
    UNIQUE KEY item_name (name)
  )`,
  "CREATE TABLE tag (item BIGINT, CONSTRAINT tag_item FOREIGN KEY (item) REFERENCES item (id))",
  `CREATE TABLE kinds (
    b BIT(3), ts TIMESTAMP(6) NULL, j JSON, g GEOMETRY, f FLOAT, y YEAR, bl BLOB, u BIGINT UNSIGNED, t TIME(6), d DATE, dt DATETIME(6), n DECIMAL(10, 3)
  )`,
];

const dialect: Dialect = {
  name: "mysql",
  setup: ["SET time_zone = '+00:00'", "SET cte_max_recursion_depth = 4294967295"],
  errors: {
    codes: { "1062": "UniqueViolation", "1452": "ForeignKeyViolation", "3819": "CheckViolation", "1048": "NotNullViolation", "1364": "NotNullViolation", "1317": "Timeout", "3024": "Timeout" },
    violations: [],
  },
};

const name = `dqlmysql_test_${process.pid}_${Date.now()}`;
let url = "";
let admin: mysql.Connection;
const executors: Executor[] = [];

const adminQuery = (sql: string): Promise<unknown> =>
  new Promise((resolve, reject) => admin.query(sql, (e, r) => (e === null ? resolve(r) : reject(e))));

async function open(mode: "prepare" | "describe" | "simple" = "prepare", maxConns = 4): Promise<Executor> {
  const e = await Executor.open(new MySqlDriver(), dialect, {
    sources: { main: { primary: { dsn: url, maxConns }, statementMode: mode } },
    catalogs: { shop: "main" },
  });
  executors.push(e);
  return e;
}

/** series is a query of the integers 1 to n, as column g. */
const series = (n: number | string) => `WITH RECURSIVE s(g) AS (SELECT 1 UNION ALL SELECT g + 1 FROM s WHERE g < ${n}) SELECT g FROM s`;

beforeAll(async () => {
  admin = mysql.createConnection(mysqlURL(base, ""));
  await adminQuery(`CREATE DATABASE ${name}`);
  url = mysqlURL(base, name);
  for (const s of schema) {
    await adminQuery(`USE ${name}`);
    await adminQuery(s);
  }
});

afterAll(async () => {
  for (const e of executors) {
    await e.close();
  }
  await adminQuery(`DROP DATABASE ${name}`);
  admin.end();
});

/** show writes a Temporal value or a Decimal as its class and text, which toEqual compares; any other value is itself. */
const show = (v: unknown): unknown =>
  typeof v === "object" && v !== null && !(v instanceof Uint8Array) && v.constructor !== Object && !Array.isArray(v) ? `${v.constructor.name} ${String(v)}` : v;

const q = (sql: string, args: unknown[] = [], types?: string[]) => ({ query: "q", catalog: "shop", sql, args, ...(types === undefined ? {} : { types }) });

describe("values", () => {
  test("built-in types decode to their host types", async () => {
    for (const mode of ["prepare", "describe", "simple"] as const) {
      const e = await open(mode);
      await rawWrite(e, "main").exec("DELETE FROM kinds");
      await rawWrite(e, "main").exec(
        `INSERT INTO kinds VALUES (b'101', '2024-01-02 03:04:05.5', '{"a": [1, null]}', ST_GeomFromText('POINT(1 2)'), 0.1, 2024, X'00ff',
          18446744073709551615, '-30:04:05.25', '2024-02-29', '2024-01-02 03:04:05.123456', -12.5)`,
      );
      const [r] = await queryAll(e, q("SELECT b, ts, j, g, f, y, bl, u, t, d, dt, n, 'text', CAST(7 AS SIGNED), 1.5e0, NULL, '0000-00-00' FROM kinds"));
      const v = r?.values ?? [];
      expect(v[3]).toBeInstanceOf(Uint8Array);
      expect([...v.slice(0, 3), ...v.slice(4)].map(show)).toEqual(
        [
        "00000101",
        Temporal.Instant.from("2024-01-02T03:04:05.5Z"),
        { a: [1, null] },
        Math.fround(0.1),
        "2024",
        new Uint8Array([0, 255]),
        18446744073709551615n,
        Temporal.Duration.from({ hours: -30, minutes: -4, seconds: -5, milliseconds: -250 }),
        Temporal.PlainDate.from("2024-02-29"),
        Temporal.PlainDateTime.from("2024-01-02T03:04:05.123456"),
        Decimal.parse("-12.500"),
        "text",
        7n,
        1.5,
        null,
        "0000-00-00",
        ].map(show),
      );
    }
  });

  test("host values round-trip as parameters", async () => {
    for (const mode of ["prepare", "simple"] as const) {
      const e = await open(mode);
      const cases: [string, unknown, unknown, string?][] = [
        ["SIGNED", 9007199254740993n, 9007199254740993n],
        ["SIGNED", 7, 7n],
        ["UNSIGNED", 18446744073709551615n, 18446744073709551615n],
        ["DOUBLE", 0.1, 0.1],
        ["DECIMAL(10,3)", Decimal.parse("-0.001"), Decimal.parse("-0.001")],
        ["BINARY(2)", new Uint8Array([0, 255]), new Uint8Array([0, 255])],
        ["CHAR", 'a "quoted" \\ \' value', 'a "quoted" \\ \' value'],
        ["CHAR", true, "1"],
        ["DATE", Temporal.PlainDate.from("2024-02-29"), Temporal.PlainDate.from("2024-02-29")],
        ["TIME(6)", Temporal.PlainTime.from("01:02:03.5"), Temporal.Duration.from({ hours: 1, minutes: 2, seconds: 3, milliseconds: 500 })],
        ["TIME(6)", Temporal.Duration.from({ days: 1, hours: 2, microseconds: 7 }), Temporal.Duration.from({ hours: 26, microseconds: 7 })],
        ["TIME(6)", Temporal.Duration.from({ minutes: -5, nanoseconds: -1500 }), Temporal.Duration.from({ minutes: -5, microseconds: -1 })],
        ["DATETIME(6)", Temporal.PlainDateTime.from("2024-01-02T03:04:05.5"), Temporal.PlainDateTime.from("2024-01-02T03:04:05.5")],
        ["DATETIME(6)", Temporal.Instant.from("2024-01-02T03:04:05Z"), Temporal.PlainDateTime.from("2024-01-02T03:04:05")],
        ["DATETIME(6)", Temporal.ZonedDateTime.from("2024-01-02T03:04:05+05:30[+05:30]"), Temporal.PlainDateTime.from("2024-01-01T21:34:05")],
        ["DATETIME(6)", new Date("2024-01-02T03:04:05Z"), Temporal.PlainDateTime.from("2024-01-02T03:04:05")],
        ["CHAR", OffsetTime.parse("12:34:56+02:00"), "12:34:56+02:00"],
        ["JSON", { a: [1, "x"] }, { a: [1, "x"] }],
        ["JSON", "just a string", "just a string", "json"],
        ["JSON", [1, 2], [1, 2], "shop.json"],
        ["UNSIGNED", "101", 5n, "bit(3)"],
        ["CHAR", null, null],
      ];
      for (const [cast, v, want, type] of cases) {
        // MySQL reads a parameter cast to TIME as a date and time, so a TIME is cast from text.
        const param = cast.startsWith("TIME") ? "CAST(? AS CHAR)" : "?";
        const r = await queryOne(e, q(`SELECT CAST(${param} AS ${cast})`, [v], type === undefined ? undefined : [type]));
        expect([cast, show(r.values[0])]).toEqual([cast, show(want)]);
      }
      // LIMIT reads an integer parameter.
      const limited = await queryAll(e, q(`${series(10)} LIMIT ?`, [3]));
      expect(limited.length).toBe(3);
    }
  });

  test("a list is one JSON array", async () => {
    const e = await open();
    const list = [
      1n,
      2,
      "x",
      true,
      null,
      Decimal.parse("1.5"),
      new Uint8Array([104, 105]),
      Temporal.Instant.from("2024-01-02T03:04:05Z"),
      { k: 1 },
      [1],
    ];
    const r = await queryOne(e, q("SELECT CAST(? AS JSON)", [list]));
    expect(r.values[0]).toEqual([1, 2, "x", true, null, "1.5", "hi", "2024-01-02 03:04:05.000000", { k: 1 }, [1]]);
    const ids = Array.from({ length: 100_000 }, (_, i) => BigInt(i));
    const counted = await queryOne(e, q("SELECT count(*), CAST(sum(id) AS SIGNED) FROM JSON_TABLE(?, '$[*]' COLUMNS (id BIGINT PATH '$')) j", [ids]));
    expect(counted.values).toEqual([100_000n, 4_999_950_000n]);
  });

  test("encoding and decoding helpers", () => {
    expect(encode(undefined, "")).toBe(null);
    expect(encode({ a: 1 }, "")).toBe('{"a":1}');
    expect(encode(Object.create(null), "")).toBe("{}");
    expect(encode([undefined], "")).toBe("[null]");
    expect(encode(Temporal.PlainDate.from("2024-01-02"), "")).toBe("2024-01-02");
    expect(encode("", "BIT")).toBe(0n);
    expect(bitsValue("1".repeat(64))).toBe(18446744073709551615n);
    expect(() => bitsValue("102")).toThrow(RangeError);
    expect(() => bitsValue("1".repeat(65))).toThrow(RangeError);
    expect(timeText(Temporal.Duration.from({ hours: 838, minutes: 59, seconds: 59 }))).toBe("838:59:59.000000");
    expect(() => timeText(Temporal.Duration.from({ months: 1 }))).toThrow(MonthsError);
    expect(() => timeText(Temporal.Duration.from({ weeks: 1 }))).toThrow(MonthsError);
    expect(() => time("12:00")).toThrow(SyntaxError);
    expect(time("00:00:00").toString()).toBe("PT0S");
    // A zero date has no host value, and stays text.
    expect(decoder({ columnType: types.DATE, characterSet: 63 })("0000-00-00")).toBe("0000-00-00");
    // MariaDB sends JSON as text, marked in its extended metadata.
    expect(decoder({ columnType: 252, characterSet: 45, extendedFormat: "json" })('{"a":1}')).toEqual({ a: 1 });
    expect(decoder({ columnType: types.YEAR, characterSet: 63 })(2024)).toBe("2024");
    // The protocol's older DECIMAL, which servers before 5.0 sent.
    expect(String(decoder({ columnType: types.DECIMAL, characterSet: 63 })("1.50"))).toBe("1.50");
  });

  test("native errors name what was violated", () => {
    expect(native(1062, "Duplicate entry 'a' for key 'item.item_name'")).toEqual({
      code: "1062",
      message: "Duplicate entry 'a' for key 'item.item_name'",
      detail: "Duplicate entry 'a' for key 'item.item_name'",
      table: "item",
      constraint: "item_name",
    });
    expect(native(1062, "Duplicate entry 'a' for key 'it''s'")).toMatchObject({ constraint: "it's" });
    expect(native(1062, "Duplicate entry")).toEqual({ code: "1062", message: "Duplicate entry", detail: "Duplicate entry" });
    expect(native(1452, "Cannot add or update a child row: a foreign key constraint fails (`db`.`t``x`, CONSTRAINT `c` FOREIGN KEY (`a`) REFERENCES `p` (`id`))")).toMatchObject({
      table: "t`x",
      constraint: "c",
    });
    expect(native(1451, "no match")).toEqual({ code: "1451", message: "no match" });
    expect(native(3819, "Check constraint 'name_set' is violated.")).toMatchObject({ constraint: "name_set" });
    expect(native(3819, "other")).toEqual({ code: "3819", message: "other" });
    expect(native(4025, "CONSTRAINT `c` failed for `db`.`t`")).toMatchObject({ constraint: "c", table: "t" });
    expect(native(4025, "other")).toEqual({ code: "4025", message: "other" });
    expect(native(1048, "Column 'name' cannot be null")).toMatchObject({ column: "name" });
    expect(native(1263, "Column set to default value; NULL supplied to NOT NULL column 'n' at row 1")).toMatchObject({ column: "n" });
    expect(native(1364, "other")).toEqual({ code: "1364", message: "other" });
    expect(native(1064, "syntax")).toEqual({ code: "1064", message: "syntax" });
  });
});

describe("statements and transactions", () => {
  test("writes, errors and transactions", async () => {
    for (const mode of ["prepare", "describe", "simple"] as const) {
      const e = await open(mode);
      await rawWrite(e, "main").exec("DELETE FROM tag");
      await rawWrite(e, "main").exec("DELETE FROM item");
      expect(await rawWrite(e, "main").exec("INSERT INTO item (id, name, mood) VALUES (?, ?, ?), (2, 'b', NULL)", 1, "a", "happy")).toBe(2);
      expect((await raw(e, "main").query("SELECT mood FROM item WHERE id = ?", 1))[0]?.values).toEqual(["happy"]);
      // An UPDATE counts the rows it matched, changed or not.
      expect(await rawWrite(e, "main").exec("UPDATE item SET name = name")).toBe(2);
      const dup = await rawWrite(e, "main").exec("INSERT INTO item (id, name) VALUES (3, 'a')").catch((x) => x);
      expect([classOf(dup), dup.constraint.name]).toEqual(["UniqueViolation", "item.item_name"]);
      const check = await rawWrite(e, "main").exec("INSERT INTO item (id, name) VALUES (3, '')").catch((x) => x);
      expect([classOf(check), check.constraint.name]).toEqual(["CheckViolation", "name_set"]);
      const nn = await rawWrite(e, "main").exec("INSERT INTO item (id, name) VALUES (3, NULL)").catch((x) => x);
      expect([classOf(nn), nn.column]).toEqual(["NotNullViolation", "name"]);
      const fk = await rawWrite(e, "main").exec("INSERT INTO tag VALUES (99)").catch((x) => x);
      expect([classOf(fk), fk.constraint.name]).toEqual(["ForeignKeyViolation", "tag.tag_item"]);
      const syntax = await raw(e, "main").query("SELEC 1").catch((x) => x);
      expect([classOf(syntax), syntax.origin.code]).toEqual(["DatabaseError", "1064"]);

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
      const ro = await e.read((tx) => queryAll(tx, q("DELETE FROM item"))).catch((x) => x);
      expect(ro.origin.code).toBe("1792");
      // A failed commit leaves the connection to be discarded.
      const tx = await e.beginWrite();
      await execute(tx, q("SELECT 1"));
      await tx.commit();
    }
  });

  test("an insert reports the ID it numbered", async () => {
    const pool = await new MySqlDriver().open({ dsn: url, maxConns: 1, setup: [], mode: "prepare" });
    const conn = (await pool.acquire()) as InsertIdConn;
    const r = await conn.execId({ query: "q", sql: "INSERT INTO item (name) VALUES (?)", args: ["numbered"] });
    expect(r.affected).toBe(1);
    expect(r.id).toBeGreaterThan(0n);
    expect(await conn.execId({ query: "q", sql: "SELECT 1", args: [] })).toEqual({ affected: 0, id: 0n });
    expect(await (await conn.query({ query: "q", sql: "DELETE FROM item WHERE name = 'numbered'", args: [] })).next()).toBe(undefined);
    conn.release();
    await pool.close();
  });

  test("cancellation and timeouts reach the server", async () => {
    const e = await open();
    const slow = `SELECT count(*) FROM (${series(1_000_000_000)}) n`;
    const timedOut = await withTimeout(50, () => queryAll(e, q(slow))).catch((x) => x);
    expect(classOf(timedOut)).toBe("Timeout");
    const ac = new AbortController();
    const pending = withSignal(ac.signal, () => e.write((tx) => queryAll(tx, q(slow)))).catch((x) => x);
    setTimeout(() => ac.abort(), 50);
    expect(classOf(await pending)).toBe("Canceled");
    // The pool still serves statements.
    expect((await queryOne(e, q("SELECT 1"))).values).toEqual([1n]);
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
    expect((await fails)[0].values).toEqual([1n]);
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

  test("streams pause the connection between batches", async () => {
    for (const mode of ["prepare", "describe", "simple"] as const) {
      const e = await open(mode, 1);
      const all = await values(iterate(e, q(`${series("?")}`, [600])));
      expect(all.length).toBe(600);
      expect(all[599]).toEqual([600n]);
      // Breaking out reads and drops the rest; the one connection serves the next statement.
      for await (const r of iterate(e, q(series(100_000)))) {
        expect(r.values).toEqual([1n]);
        break;
      }
      expect((await queryOne(e, q("SELECT 2"))).values).toEqual([2n]);
      // In a transaction, the stream sees its writes.
      await e
        .write(async (tx) => {
          await rawWrite(tx, "main").exec("INSERT INTO item (id, name) VALUES (500, 'streamed')");
          expect(await values(iterate(tx, q("SELECT name FROM item WHERE id = 500")))).toEqual([["streamed"]]);
          throw new Error("undo");
        })
        .catch(() => {});
    }
  });

  test("a stream that fails discards its connection", async () => {
    const e = await open("describe", 1);
    const failed = await values(iterate(e, q(`SELECT CAST(IF(g = 300, 'x', g) AS JSON) FROM (${series(600)}) n`))).catch((x) => x);
    expect([classOf(failed), failed.origin.code]).toEqual(["DatabaseError", "3141"]);
    expect((await queryOne(e, q("SELECT 3"))).values).toEqual([3n]);
    // A canceled stream reaches the server.
    const ac = new AbortController();
    const canceled = withSignal(ac.signal, () => values(iterate(e, q(`SELECT g, IF(g = 300, (SELECT count(*) FROM (${series(1_000_000_000)}) m), 0) FROM (${series(600)}) n`)))).catch(
      (x) => x,
    );
    setTimeout(() => ac.abort(), 100);
    expect(classOf(await canceled)).toBe("Canceled");
    expect((await queryOne(e, q("SELECT 4"))).values).toEqual([4n]);
  });

  test("bulk writes split", async () => {
    const limited = await Executor.open(new MySqlDriver(), { ...dialect, maxParams: 4 }, { sources: { main: { primary: { dsn: url } } }, catalogs: { shop: "main" } });
    executors.push(limited);
    const bulk: Bulk = {
      rows: 5,
      paramsPerRow: 2,
      statement: (from, to) => {
        const rows = Array.from({ length: to - from }, () => "(?, ?)").join(", ");
        const args = Array.from({ length: to - from }, (_, i) => [BigInt(1000 + from + i), `bulk ${from + i}`]).flat();
        return { ...q(`INSERT INTO item (id, name) VALUES ${rows}`, args), writes: true };
      },
    };
    expect(await executeBulk(limited, bulk)).toBe(5);
    const read: Bulk = { rows: 5, paramsPerRow: 1, statement: (from, to) => q(`SELECT id FROM item WHERE id >= ${1000 + from} AND id < ${1000 + to} ORDER BY id`) };
    expect((await queryBulk(limited, read)).map((r) => r.values[0])).toEqual([1000n, 1001n, 1002n, 1003n, 1004n]);
  });
});

describe("connections", () => {
  test("a failed connect with a signal is the driver's error", async () => {
    const pool = await new MySqlDriver().open({ dsn: "mysql://nobody@127.0.0.1:1/x", maxConns: 0, setup: [], mode: "prepare" });
    expect(await pool.acquire(new AbortController().signal).catch((x) => (x as { code?: string }).code)).toBe("ECONNREFUSED");
    await pool.close();
  });

  test("a cancel request that cannot connect leaves the statement to finish", async () => {
    const pool = await new MySqlDriver().open({ dsn: url, maxConns: 0, setup: [], mode: "describe" });
    const conn = await pool.acquire();
    (pool as unknown as { config: { uri: string } }).config.uri = "mysql://nobody@127.0.0.1:1/x";
    const ac = new AbortController();
    setTimeout(() => ac.abort(), 20);
    const rows = await conn.query({ query: "q", sql: "SELECT SLEEP(0.2)", args: [], signal: ac.signal });
    expect(await rows.next()).toEqual(new Row([0n]));
    conn.release();
    await pool.close();
  });

  test("a server that does not answer is a connection error", async () => {
    const err = await Executor.open(new MySqlDriver(), dialect, { sources: { main: { primary: { dsn: "mysql://nobody@127.0.0.1:1/x" } } } }).catch((x) => x);
    expect(classOf(err)).toBe("ConnectionError");
    const d = new MySqlDriver();
    expect(d.broken(Object.assign(new Error("lost"), { fatal: true }))).toBe(true);
    expect(d.broken(Object.assign(new Error("x"), { code: "ECONNRESET" }))).toBe(true);
    expect(d.broken(new Error("Can't add new command when connection is in closed state"))).toBe(true);
    expect(d.broken(Object.assign(new Error("server"), { errno: 1064, sqlState: "42000" }))).toBe(false);
    expect(d.broken(new Error("other"))).toBe(false);
    expect(d.broken("text")).toBe(false);
    expect(d.native(new Error("x"))).toBe(undefined);
    expect(d.native(Object.assign(new Error("m"), { errno: 1064, sqlState: "42000" }))).toEqual({ code: "1064", message: "m" });
  });

  test("setup failures discard the connection", async () => {
    const err = await Executor.open(new MySqlDriver(), { ...dialect, setup: ["SET nonsense = 1"] }, { sources: { main: { primary: { dsn: url } } } }).catch((x) => x);
    expect(classOf(err)).toBe("ConnectionError");
  });

  test("a killed connection is discarded", async () => {
    const e = await open("prepare", 1);
    const tx = await e.beginWrite();
    const [id] = (await queryOne(tx, q("SELECT CONNECTION_ID()"))).values as [bigint];
    await adminQuery(`KILL ${id}`);
    const err = await execute(tx, q("SELECT 1")).catch((x) => x);
    expect(classOf(err)).toBe("ConnectionError");
    await tx.rollback().catch(() => {});
    expect((await queryOne(e, q("SELECT 1"))).values).toEqual([1n]);
  });
});
