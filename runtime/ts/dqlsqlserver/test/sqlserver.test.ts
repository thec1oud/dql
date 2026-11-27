import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { createRequire } from "node:module";
import { type Conn, Decimal, type Dialect, Executor, OffsetTime, type Pool, Row, Temporal, classOf, execute, iterate, queryAll, queryOne, raw, rawWrite, withSignal, withTimeout } from "@dql/runtime";
import { Connection, Request } from "tedious";
import { NoTypeError, SqlServerDriver, config, decimalText, encode, exact, install, native } from "../src/index.js";
import { decoder } from "../src/values.js";

// The tests run against the SQL Server DQL_TEST_SQLSERVER_URL names, in a
// fresh database; they fail rather than skip without one, since they keep the
// package at full coverage. The variable is the Go driver's URL, which this
// driver reads too.
const base = process.env["DQL_TEST_SQLSERVER_URL"];
if (base === undefined || base === "") {
  throw new Error("DQL_TEST_SQLSERVER_URL is not set; these tests need a SQL Server 2022 server, such as sqlserver://sa:password@localhost:1433");
}

const name = `dqlsqlserver_test_${process.pid}_${Date.now()}`;
const url = (() => {
  const u = new URL(base);
  u.searchParams.set("database", name);
  return u.toString();
})();

const schema = [
  "CREATE SCHEMA app",
  `CREATE TABLE app.item (
    id bigint IDENTITY PRIMARY KEY,
    name nvarchar(50) NOT NULL CONSTRAINT item_name UNIQUE,
    CONSTRAINT name_set CHECK (name <> '')
  )`,
  "CREATE TABLE app.flt (k int)",
  "CREATE UNIQUE INDEX flt_k ON app.flt (k) WHERE k > 0",
  "CREATE TABLE app.tag (item bigint CONSTRAINT tag_item FOREIGN KEY REFERENCES app.item (id))",
  `CREATE TABLE app.kinds (
    u8 tinyint, i16 smallint, i32 int, i64 bigint, b bit, f32 real, f64 float,
    d decimal(38, 10), m money, sm smallmoney, c nchar(3), v nvarchar(10), t nvarchar(max), x xml, bin varbinary(4), id uniqueidentifier,
    dt date, tm time(7), ts datetime2(7), old datetime, small smalldatetime, tz datetimeoffset(7)
  )`,
];

const dialect: Dialect = {
  name: "sqlserver",
  setup: ["SET ANSI_NULLS ON", "SET QUOTED_IDENTIFIER ON", "SET ARITHABORT ON"],
  errors: {
    codes: { "2627": "UniqueViolation", "2601": "UniqueViolation", "547": "ForeignKeyViolation", "547/CHECK": "CheckViolation", "515": "NotNullViolation" },
    violations: [],
  },
};

/** adminBatch runs a statement on the server's master database. */
function adminBatch(sql: string): Promise<void> {
  return new Promise((resolve, reject) => {
    const c = new Connection(config(base as string));
    c.connect((e) => {
      if (e) {
        reject(e);
        return;
      }
      c.execSqlBatch(
        new Request(sql, (err) => {
          c.close();
          if (err) {
            reject(err);
          } else {
            resolve();
          }
        }),
      );
    });
  });
}

const executors: Executor[] = [];

async function open(maxConns = 4, setup = dialect.setup): Promise<Executor> {
  const e = await Executor.open(new SqlServerDriver(), { ...dialect, setup }, {
    sources: { main: { primary: { dsn: url, maxConns }, statementMode: "prepare" } },
    catalogs: { shop: "main" },
  });
  executors.push(e);
  return e;
}

async function pool(maxConns = 1, setup: readonly string[] = dialect.setup): Promise<Pool> {
  return new SqlServerDriver().open({ dsn: url, maxConns, setup, mode: "prepare" });
}

const s = (sql: string, args: unknown[] = [], extra: object = {}) => ({ query: "q", sql, args, ...extra });

async function rows(c: Conn, sql: string, args: unknown[] = []): Promise<(readonly unknown[])[]> {
  const r = await c.query(s(sql, args));
  const out: (readonly unknown[])[] = [];
  for (let row = await r.next(); row !== undefined; row = await r.next()) {
    out.push(row.values);
  }
  await r.close();
  return out;
}

beforeAll(async () => {
  await adminBatch(`CREATE DATABASE ${name}`);
  const p = await pool();
  const c = await p.acquire();
  for (const sql of schema) {
    await c.exec(s(sql));
  }
  c.release();
  await p.close();
});

afterAll(async () => {
  for (const e of executors) {
    await e.close();
  }
  await adminBatch(`ALTER DATABASE ${name} SET SINGLE_USER WITH ROLLBACK IMMEDIATE; DROP DATABASE ${name}`);
});

/** show writes a Temporal value or a Decimal as its class and text, which toEqual compares; any other value is itself. */
const show = (v: unknown): unknown =>
  typeof v === "object" && v !== null && !(v instanceof Uint8Array) && v.constructor !== Object && !Array.isArray(v) ? `${v.constructor.name} ${String(v)}` : v;

describe("values", () => {
  test("every column type reads back as its host type, exactly", async () => {
    const p = await pool();
    const c = await p.acquire();
    await c.exec(s("DELETE FROM app.kinds"));
    const tz = Temporal.ZonedDateTime.from("2024-05-06T07:08:09.1234567+05:30[+05:30]");
    const ts = Temporal.PlainDateTime.from("2024-05-06T07:08:09.1234567");
    await c.exec(
      s("INSERT INTO app.kinds VALUES (@p1, @p2, @p3, @p4, @p5, @p6, @p7, @p8, @p9, @p10, @p11, @p12, @p13, @p14, @p15, @p16, @p17, @p18, @p19, @p20, @p21, @p22)", [
        200, -5, 2 ** 31 - 1, 9_007_199_254_740_993n, true, 1.5, 2.25,
        Decimal.parse("-1234567890123456789012345678.0123456789"), Decimal.parse("-922337203685477.5808"), Decimal.parse("214748.3647"),
        "abc", "v", "text", "<a/>", new Uint8Array([1, 2]), "6f9619ff-8b86-d011-b42d-00c04fc964ff",
        Temporal.PlainDate.from("2024-05-06"), Temporal.PlainTime.from("07:08:09.1234567"), ts, ts, ts, tz,
      ], { types: ["", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "datetime", "smalldatetime", ""] }),
    );
    const [got] = await rows(c, "SELECT * FROM app.kinds");
    expect(got?.map(show)).toEqual([
      200, -5, 2 ** 31 - 1, 9_007_199_254_740_993n, true, 1.5, 2.25,
      "Decimal -1234567890123456789012345678.0123456789", "Decimal -922337203685477.5808", "Decimal 214748.3647",
      "abc", "v", "text", "<a/>", new Uint8Array([1, 2]), "6f9619ff-8b86-d011-b42d-00c04fc964ff",
      "PlainDate 2024-05-06", "PlainTime 07:08:09.1234567", "PlainDateTime 2024-05-06T07:08:09.1234567",
      "PlainDateTime 2024-05-06T07:08:09.123", "PlainDateTime 2024-05-06T07:08:00",
      "ZonedDateTime 2024-05-06T07:08:09.1234567+05:30[+05:30]",
    ]);
    // Nulls of every type read as null, and money and decimal under their nullable framing.
    await c.exec(s("DELETE FROM app.kinds"));
    await c.exec(s("INSERT INTO app.kinds (u8) VALUES (NULL)"));
    expect((await rows(c, "SELECT * FROM app.kinds"))[0]?.every((v) => v === null)).toBe(true);
    expect((await rows(c, "SELECT CAST(1.5 AS money), CAST(-2 AS smallmoney), CAST(0 AS decimal(5, 0)), CAST('2024-01-02T03:04:05-08:00' AS datetimeoffset(0))")).map((r) => r.map(show))).toEqual([
      ["Decimal 1.5000", "Decimal -2.0000", "Decimal 0", "ZonedDateTime 2024-01-02T03:04:05-08:00[-08:00]"],
    ]);
    // A value that cannot be null comes in the fixed-length framing.
    expect((await rows(c, "SELECT ISNULL(CAST(-1.5 AS money), 0), ISNULL(CAST(2 AS smallmoney), 0), ISNULL(CAST(5 AS bigint), 0)")).map((r) => r.map(show))).toEqual([
      ["Decimal -1.5000", "Decimal 2.0000", 5n],
    ]);
    c.release();
    await p.close();
  });

  test("host values go as parameters", async () => {
    const p = await pool();
    const c = await p.acquire();
    const one = async (sql: string, v: unknown, type = "") => (await rows(c, sql, [v]).catch(() => rows(c, sql, [v])))[0]?.[0];
    void one;
    const at = Temporal.Instant.from("2024-01-02T03:04:05.1234567Z");
    const cases: [string, unknown, unknown][] = [
      ["SELECT CAST(@p1 AS datetimeoffset(7))", at, "ZonedDateTime 2024-01-02T03:04:05.1234567+00:00[+00:00]"],
      ["SELECT CAST(@p1 AS datetimeoffset(3))", new Date(Date.UTC(2024, 0, 2)), "ZonedDateTime 2024-01-02T00:00:00+00:00[+00:00]"],
      ["SELECT CAST(@p1 AS decimal(5, 2))", Decimal.parse("1.5"), "Decimal 1.50"],
      ["SELECT @p1", 1.25, 1.25],
      ["SELECT @p1", -(2 ** 40), BigInt(-(2 ** 40))],
      ["SELECT @p1", 7n, 7n],
      ["SELECT @p1", null, null],
      ["SELECT JSON_VALUE(@p1, '$.a')", { a: "x" }, "x"],
      ["SELECT CAST(@p1 AS nvarchar(10))", undefined, null],
    ];
    for (const [sql, v, want] of cases) {
      expect([sql, show((await rows(c, sql, [v]))[0]?.[0])]).toEqual([sql, want]);
    }
    expect(() => encode(Temporal.Duration.from({ hours: 1 }), "")).toThrow(NoTypeError);
    expect(() => encode(new OffsetTime(Temporal.PlainTime.from("01:00"), 3600), "")).toThrow("a time with an offset");
    c.release();
    await p.close();
  });

  test("a list is one JSON array that OPENJSON reads as the column's type", async () => {
    const e = await open();
    await rawWrite(e, "main").exec("DELETE FROM app.tag");
    await rawWrite(e, "main").exec("DELETE FROM app.item");
    await rawWrite(e, "main").exec("INSERT INTO app.item (name) VALUES (N'a'), (N'B'), (N'c')");
    const inList = (col: string, typ: string) => `SELECT ${col} FROM app.item WHERE ${col} IN (SELECT [v] FROM OPENJSON(@p1) WITH ([v] ${typ} '$')) ORDER BY ${col}`;
    expect((await raw(e, "main").query(inList("name", "nvarchar(50)"), ["a", "b"])).map((r) => r.values[0])).toEqual(["a", "B"]);
    expect(await raw(e, "main").query(inList("name", "nvarchar(50)"), [])).toEqual([]);
    const items: [string, unknown, unknown][] = [
      ["uniqueidentifier", "6f9619ff-8b86-d011-b42d-00c04fc964ff", "6f9619ff-8b86-d011-b42d-00c04fc964ff"],
      ["date", Temporal.PlainDate.from("2024-01-02"), "PlainDate 2024-01-02"],
      ["time(7)", Temporal.PlainTime.from("01:02:03.1234567"), "PlainTime 01:02:03.1234567"],
      ["datetime2(7)", Temporal.PlainDateTime.from("2024-01-02T01:02:03.1234567"), "PlainDateTime 2024-01-02T01:02:03.1234567"],
      ["datetimeoffset(7)", Temporal.ZonedDateTime.from("2024-01-02T01:02:03.1234567-01:00[-01:00]"), "ZonedDateTime 2024-01-02T01:02:03.1234567-01:00[-01:00]"],
      ["decimal(10, 3)", Decimal.parse("1.5"), "Decimal 1.500"],
      ["bigint", 9_007_199_254_740_993n, 9_007_199_254_740_993n],
      ["bit", true, true],
      ["varbinary(10)", new Uint8Array([1, 2]), new Uint8Array([1, 2])],
      ["nvarchar(max)", null, null],
    ];
    for (const [typ, v, want] of items) {
      const sql = `SELECT [v] FROM OPENJSON(@p1) WITH ([v] ${typ} '$')`;
      const got = await raw(e, "main").query(sql, [v]);
      expect([typ, show(got[0]?.values[0])]).toEqual([typ, want]);
    }
    expect(encode([{ a: 1 }, [2]], "").value).toBe('[{"a":1},[2]]');
  });

  test("the exact reader", () => {
    expect([decimalText(false, 5n, 3), decimalText(true, 0n, 2), decimalText(true, 12n, 0)]).toEqual(["0.005", "0.00", "-12"]);
    const r = { value: 1, offset: 0 };
    expect(exact(Buffer.alloc(0), 0, { type: { name: "Int" } }, r)).toBe(r);
    const none = { value: null, offset: 1 };
    expect(exact(Buffer.alloc(1), 0, { type: { name: "DecimalN" } }, none)).toEqual({ value: null, offset: 1 });
    // Installing again wraps tedious's reader once.
    install();
    const parser = createRequire(import.meta.url)("tedious/lib/value-parser.js") as { readValue: { dql?: true } };
    expect(parser.readValue.dql).toBe(true);
    expect(decoder({ type: { name: "IntN" } })(5)).toBe(5);
  });

  test("native errors name what was violated", () => {
    expect(native(2627, "Violation of PRIMARY KEY constraint 'it''s'. Cannot insert duplicate key in object 'odd'. The duplicate key value is (1).")).toMatchObject({ table: "odd", constraint: "it's" });
    for (const n of [2627, 2601, 547, 515]) {
      expect(native(n, "nope")).toEqual({ code: String(n), message: "nope", ...(n === 2627 || n === 2601 ? { detail: "nope" } : {}) });
    }
  });
});

describe("statements and transactions", () => {
  test("writes, errors and transactions", async () => {
    const e = await open();
    await rawWrite(e, "main").exec("DELETE FROM app.tag");
    await rawWrite(e, "main").exec("DELETE FROM app.item");
    expect(await rawWrite(e, "main").exec("INSERT INTO app.item (name) VALUES (@p1), (N'b')", "a")).toBe(2);
    // An UPDATE counts the rows it matched, changed or not.
    expect(await rawWrite(e, "main").exec("UPDATE app.item SET name = name")).toBe(2);
    const dup = await rawWrite(e, "main").exec("INSERT INTO app.item (name) VALUES ('a')").catch((x) => x);
    expect([classOf(dup), dup.origin.code, dup.constraint.name]).toEqual(["UniqueViolation", "2627", "app.item.item_name"]);
    const check = await rawWrite(e, "main").exec("INSERT INTO app.item (name) VALUES ('')").catch((x) => x);
    expect([classOf(check), check.origin.code]).toEqual(["CheckViolation", "547/CHECK"]);
    const nn = await rawWrite(e, "main").exec("INSERT INTO app.item (name) VALUES (NULL)").catch((x) => x);
    expect(classOf(nn)).toBe("NotNullViolation");
    const fk = await rawWrite(e, "main").exec("INSERT INTO app.tag VALUES (99)").catch((x) => x);
    expect([classOf(fk), fk.origin.code]).toEqual(["ForeignKeyViolation", "547"]);
    const index = await rawWrite(e, "main").exec("INSERT INTO app.flt VALUES (1), (1)").catch((x) => x);
    expect([classOf(index), index.origin.code, index.constraint.name]).toEqual(["UniqueViolation", "2601", "app.flt.flt_k"]);
    const syntax = await raw(e, "main").query("SELECT 1 FROM").catch((x) => x);
    expect([classOf(syntax), syntax.origin.code]).toEqual(["DatabaseError", "102"]);

    // A savepoint undoes its writes; the transaction commits the rest, at its own isolation level.
    await e.write(async (tx) => {
      await execute(tx, { query: "q", catalog: "shop", sql: "UPDATE app.item SET name = 'c' WHERE name = 'a'", args: [] });
      await e
        .write(async (inner) => {
          await execute(inner, { query: "q", catalog: "shop", sql: "UPDATE app.item SET name = 'd' WHERE name = 'c'", args: [] });
          throw new Error("undo");
        }, { propagation: "nested" })
        .catch(() => {});
      await e.write((inner) => execute(inner, { query: "q", catalog: "shop", sql: "UPDATE app.item SET name = 'e' WHERE name = 'b'", args: [] }), { propagation: "nested" });
      const level = await queryOne(tx, { query: "q", catalog: "shop", sql: "SELECT transaction_isolation_level FROM sys.dm_exec_sessions WHERE session_id = @@SPID", args: [] });
      expect(level.values).toEqual([4]);
    }, { isolation: "serializable" });
    expect((await raw(e, "main").query("SELECT name FROM app.item ORDER BY name")).map((r) => r.values[0])).toEqual(["c", "e"]);
    // The level held for that transaction only.
    expect((await raw(e, "main").query("SELECT transaction_isolation_level FROM sys.dm_exec_sessions WHERE session_id = @@SPID"))[0]?.values).toEqual([2]);
    await e.read((tx) => queryAll(tx, { query: "q", catalog: "shop", sql: "SELECT 1", args: [] }), { isolation: "repeatable read" });
  });

  test("a rollback after the server ended the transaction succeeds", async () => {
    const p = await pool();
    const c = await p.acquire();
    await c.begin({ readOnly: false });
    // The server ends the transaction; the statement itself reports the changed count.
    await c.exec(s("ROLLBACK")).catch(() => {});
    await c.rollback();
    await c.savepoint("s]1").catch(() => {});
    await c.releaseSavepoint("s]1");
    const err = await c.rollbackTo("s]1").catch((x) => x);
    expect(err.number).toBe(3903);
    c.release();
    await p.close();
  });

  test("cancellation and timeouts reach the server", async () => {
    const e = await open();
    const slow = "WAITFOR DELAY '00:00:10'; SELECT 1";
    const timedOut = await withTimeout(100, () => queryAll(e, { query: "q", catalog: "shop", sql: slow, args: [] })).catch((x) => x);
    expect(classOf(timedOut)).toBe("Timeout");
    const ac = new AbortController();
    const pending = withSignal(ac.signal, () => e.write((tx) => queryAll(tx, { query: "q", catalog: "shop", sql: slow, args: [] }))).catch((x) => x);
    setTimeout(() => ac.abort(), 100);
    expect(classOf(await pending)).toBe("Canceled");
    // The pool still serves statements.
    expect((await raw(e, "main").query("SELECT 1"))[0]?.values).toEqual([1]);
  });

  test("waiting for a connection gives up when aborted", async () => {
    const e = await open(1);
    const tx = await e.beginWrite();
    await execute(tx, { query: "q", catalog: "shop", sql: "SELECT 1", args: [] });
    const ac = new AbortController();
    const waiting = withSignal(ac.signal, () => raw(e, "main").query("SELECT 1")).catch((x) => x);
    setTimeout(() => ac.abort(), 20);
    expect(classOf(await waiting)).toBe("Canceled");
    const fails = raw(e, "main").query("SELECT 1");
    await tx.commit();
    expect((await fails)[0]?.values).toEqual([1]);
    const gone = await withSignal(AbortSignal.abort(), () => raw(e, "main").query("SELECT 1")).catch((x) => x);
    expect(classOf(gone)).toBe("Canceled");
  });
});

describe("large results (§25)", () => {
  const series = (n: number) => `SELECT TOP (${n}) ROW_NUMBER() OVER (ORDER BY (SELECT NULL)) AS g FROM sys.all_columns a CROSS JOIN sys.all_columns b`;

  test("streams pause the request between batches, and close early", async () => {
    const e = await open();
    let n = 0;
    for await (const r of iterate(e, { query: "q", catalog: "shop", sql: series(2000), args: [] })) {
      n += Number((r as Row).values[0] === BigInt(n + 1));
    }
    expect(n).toBe(2000);
    // Closing early cancels the rest; the connection serves the next statement.
    for await (const r of iterate(e, { query: "q", catalog: "shop", sql: series(100_000), args: [] })) {
      expect((r as Row).values[0]).toBe(1n);
      break;
    }
    for await (const _ of iterate(e, { query: "q", catalog: "shop", sql: series(10), args: [] })) {
      break;
    }
    expect((await raw(e, "main").query("SELECT 2"))[0]?.values).toEqual([2]);
  });

  test("a stream that fails reports its error", async () => {
    const e = await open();
    const failing = (async () => {
      for await (const _ of iterate(e, { query: "q", catalog: "shop", sql: "SELECT 1; SELECT 1/0", args: [] })) {
        // reads the first set
      }
    })().catch((x) => x);
    expect((await failing).origin.code).toBe("8134");
  });
});

describe("connections", () => {
  test("the URL configures tedious", () => {
    expect(config("sqlserver://u%40x:p%3Ay@h?Database=d&encrypt=true&TrustServerCertificate=yes&connection+timeout=3")).toMatchObject({
      server: "h",
      authentication: { options: { userName: "u@x", password: "p:y" } },
      options: { port: 1433, database: "d", encrypt: true, trustServerCertificate: true, connectTimeout: 3000 },
    });
    expect(config("sqlserver://h:1444").options).toMatchObject({ port: 1444, encrypt: false });
    expect(() => config("postgres://h")).toThrow("not sqlserver:");
  });

  test("a server that does not answer is a connection error", async () => {
    const d = new SqlServerDriver();
    const p = await d.open({ dsn: "sqlserver://sa:x@127.0.0.1:1?connection+timeout=1", maxConns: 0, setup: [], mode: "prepare" });
    const err = await p.acquire().catch((x) => x);
    expect(d.broken(err)).toBe(true);
    expect(d.native(err)).toBe(undefined);
    await p.close();
    expect(d.broken(await p.acquire().catch((x) => x))).toBe(true);
    expect([d.broken(new Error("x")), d.broken("x"), d.broken(Object.assign(new Error("x"), { number: 1 }))]).toEqual([false, false, false]);
  });

  test("setup failures discard the connection, and a waiter gets the room", async () => {
    const p = await pool(1, ["SET nope ON"]);
    const first = p.acquire().catch((x) => x);
    const second = p.acquire().catch((x) => x);
    expect((await first).number).toBe(195);
    expect((await second).number).toBe(195);
    await p.close();
  });

  test("a released connection is reused, a waiter gets it, and one in a transaction is discarded", async () => {
    const p = await pool(1);
    const spid = async (c: Conn) => (await rows(c, "SELECT CONVERT(nvarchar(36), connection_id) FROM sys.dm_exec_connections WHERE session_id = @@SPID"))[0]?.[0];
    const a = await p.acquire();
    const id = await spid(a);
    const waiting = p.acquire();
    a.release();
    const b = await waiting;
    expect(await spid(b)).toBe(id);
    await b.begin({ readOnly: true });
    b.release();
    const c = await p.acquire();
    expect(await spid(c)).not.toBe(id);
    c.release();
    await p.close();
  });

  test("a killed connection is discarded", async () => {
    const p = await pool(1);
    const c = await p.acquire();
    const [[session]] = (await rows(c, "SELECT @@SPID")) as [[number]];
    await adminBatch(`KILL ${session}`);
    const err = await c.query(s("SELECT 1")).catch((x) => x);
    expect(new SqlServerDriver().broken(err)).toBe(true);
    const tx = await c.begin({ readOnly: false }).catch((x) => x);
    expect(tx).toBeInstanceOf(Error);
    c.release();
    const d = await p.acquire();
    expect(await rows(d, "SELECT 1")).toEqual([[1]]);
    d.release();
    await p.close();
  });

  test("an idle connection whose session was killed is skipped", async () => {
    const p = await pool(1);
    const c = await p.acquire();
    const [[session]] = (await rows(c, "SELECT @@SPID")) as [[number]];
    c.release();
    await adminBatch(`KILL ${session}`);
    // The server's end of the session reaches the idle connection.
    await new Promise((resolve) => setTimeout(resolve, 200));
    const d = await p.acquire();
    const [[other]] = (await rows(d, "SELECT @@SPID")) as [[number]];
    expect(typeof other).toBe("number");
    d.release();
    await p.close();
  });
});
