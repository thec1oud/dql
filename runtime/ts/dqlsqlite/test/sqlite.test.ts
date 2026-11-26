import { describe, expect, test } from "bun:test";
import { mkdtempSync } from "node:fs";
import { DatabaseSync } from "node:sqlite";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Decimal, OffsetTime, type Conn, type Pool, Temporal } from "@dql/runtime";
import { SqliteDriver, codeOf, encode, native, node } from "../src/index.js";
import { binding } from "../src/backend.js";

const setup = ["PRAGMA foreign_keys = ON", "PRAGMA case_sensitive_like = ON"];

const schema = [
  `CREATE TABLE users (
    id INTEGER PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL DEFAULT '',
    age INTEGER,
    CONSTRAINT users_age CHECK (age >= 0),
    CHECK (length(name) < 10)
  ) STRICT`,
  "CREATE UNIQUE INDEX users_lower_name ON users (lower(name))",
  "CREATE TABLE pairs (a INTEGER, b INTEGER, UNIQUE (a, b))",
  "CREATE TABLE posts (id INTEGER PRIMARY KEY, author INTEGER NOT NULL REFERENCES users (id)) STRICT",
];

/** database creates a database file with the test schema and returns its path. */
function database(): string {
  const dir = mkdtempSync(join(tmpdir(), "dqlsqlite-"));
  const path = join(dir, "test.db");
  const db = new DatabaseSync(path);
  for (const s of schema) {
    db.exec(s);
  }
  db.close();
  return path;
}

const sent = (sql: string, args: unknown[] = [], extra: object = {}) => ({ query: "q", sql, args, ...extra });

async function all(c: Conn, sql: string, args: unknown[] = []): Promise<unknown[][]> {
  const r = await c.query(sent(sql, args));
  const out: unknown[][] = [];
  for (let row = await r.next(); row !== undefined; row = await r.next()) {
    out.push(row.values as unknown[]);
  }
  await r.close();
  return out;
}

for (const [name, driver] of [
  ["bun:sqlite", new SqliteDriver()],
  ["node:sqlite", new SqliteDriver(node(DatabaseSync))],
] as const) {
  describe(name, () => {
    async function open(maxConns = 1): Promise<[Pool, Conn]> {
      const p = await driver.open({ dsn: database(), maxConns, setup, mode: "prepare" });
      return [p, await p.acquire()];
    }

    test("values", async () => {
      const [p, c] = await open();
      const at = Temporal.Instant.from("2024-05-06T06:08:09.0000005Z");
      const got = await all(c, "SELECT " + Array(19).fill("?").join(", "), [
        true,
        false,
        { a: 1 },
        Decimal.parse("1.50"),
        Temporal.PlainDate.from("2024-05-06"),
        Temporal.PlainDateTime.from("2024-05-06T01:02:03"),
        Temporal.PlainTime.from("04:00"),
        OffsetTime.parse("04:00:00+01:00"),
        at,
        at.toZonedDateTimeISO("+02:00"),
        new Date(Date.UTC(2024, 0, 2)),
        { class: "integer", value: 1n },
        { ok: true, value: 7n },
        { ok: false, raw: { class: "text", value: "seven" } },
        new Uint8Array([1]),
        2.5,
        null,
        undefined,
        "s",
      ]);
      expect(got).toEqual([
        [
          1n,
          0n,
          `{"a":1}`,
          "1.50",
          "2024-05-06",
          "2024-05-06T01:02:03",
          "04:00:00",
          "04:00:00+01:00",
          "2024-05-06T06:08:09.0000005Z",
          "2024-05-06T06:08:09.0000005Z",
          "2024-01-02T00:00:00Z",
          1n,
          7n,
          "seven",
          new Uint8Array([1]),
          2.5,
          null,
          null,
          "s",
        ],
      ]);
      // A JSON column's value is JSON whatever its type.
      expect(await all(c, "SELECT ?", [])).toEqual([[null]]);
      const r = await c.query(sent("SELECT ?", ["x"], { types: ["json"] }));
      expect((await r.next())?.values).toEqual([`"x"`]);
      c.release();
      await p.close();
    });

    test("lists", async () => {
      const [p, c] = await open();
      const got = await all(c, "SELECT value FROM json_each(?) ORDER BY key", [
        [1n, "a", true, { b: 1 }, [1], Decimal.parse("2.5"), new Uint8Array([104, 105]), { ok: true, value: 3n }, null, undefined],
      ]);
      expect(got.map((r) => r[0])).toEqual([1n, "a", 1n, `{"b":1}`, "[1]", "2.5", "hi", 3n, null, null]);
      c.release();
      await p.close();
    });

    test("writes and streams", async () => {
      const [p, c] = await open();
      expect(await c.exec(sent("INSERT INTO users (email, name) VALUES (?, 'a'), (?, 'b')", ["a@x", "b@x"]))).toBe(2);
      const r = await c.query(sent("SELECT email FROM users ORDER BY id", [], { stream: true }));
      expect((await r.next())?.values).toEqual(["a@x"]);
      await r.close();
      expect(await all(c, "INSERT INTO users (email, name) VALUES ('c@x', 'c') RETURNING id")).toEqual([[3n]]);
      // A released connection is idle, and the next acquire takes it.
      c.release();
      const again = await p.acquire();
      expect(await all(again, "SELECT count(*) FROM users")).toEqual([[3n]]);
      again.release();
      await p.close();
    });

    test("transactions", async () => {
      const [p, c] = await open();
      await c.begin({ readOnly: false });
      await c.exec(sent("INSERT INTO users (email) VALUES ('a@x')"));
      await c.savepoint(`s"1`);
      await c.rollbackTo(`s"1`);
      await c.releaseSavepoint(`s"1`);
      await c.commit();
      expect(await all(c, "SELECT count(*) FROM users")).toEqual([[1n]]);

      // A read-only transaction refuses writes, and the connection writes again after it.
      await c.begin({ readOnly: true, isolation: "read committed" });
      await expect(c.exec(sent("INSERT INTO users (email, name) VALUES ('b@x', 'b')"))).rejects.toThrow();
      await c.rollback();
      await c.exec(sent("INSERT INTO users (email, name) VALUES ('b@x', 'b')"));

      // A rollback after SQLite ended the transaction succeeds.
      await c.begin({ readOnly: false });
      await c.exec(sent("ROLLBACK"));
      await c.rollback();
      // A commit with none open fails, as does a rollback that is no rollback's failure.
      await c.begin({ readOnly: true });
      await c.exec(sent("ROLLBACK"));
      await expect(c.commit()).rejects.toThrow();
      await c.rollback();
      // A read-only begin within a transaction fails and leaves the connection writable.
      await c.begin({ readOnly: false });
      await expect(c.begin({ readOnly: true })).rejects.toThrow();
      await c.exec(sent("INSERT INTO users (email, name) VALUES ('c@x', 'c')"));
      // Released in a transaction, it is discarded, and the pool opens another.
      c.release();
      const c2 = await p.acquire();
      expect(await all(c2, "SELECT count(*) FROM users")).toEqual([[2n]]);
      c2.release();
      await p.close();
    });

    test("errors", async () => {
      const [p, c] = await open();
      await c.exec(sent("INSERT INTO users (id, email) VALUES (1, 'a@x')"));
      await c.exec(sent("INSERT INTO pairs VALUES (1, 2)"));
      for (const [q, want] of [
        ["INSERT INTO users (email, name) VALUES ('a@x', 'q')", { code: "2067", table: "users", constraint: "email" }],
        ["INSERT INTO users (email) VALUES ('e@x')", { code: "2067", constraint: "users_lower_name" }],
        ["INSERT INTO users (id, email, name) VALUES (1, 'c@x', 'r')", { code: "1555", table: "users", constraint: "id" }],
        ["INSERT INTO pairs VALUES (1, 2)", { code: "2067", table: "pairs", constraint: "a, b" }],
        ["INSERT INTO users (email, name) VALUES (NULL, 'n')", { code: "1299", table: "users", column: "email" }],
        ["INSERT INTO users (email, name, age) VALUES ('d@x', 'm', -1)", { code: "275", constraint: "users_age" }],
        ["INSERT INTO users (email, name) VALUES ('d@x', 'a long name')", { code: "275", constraint: "length(name) < 10" }],
        ["INSERT INTO posts (author) VALUES (9)", { code: "787" }],
      ] as const) {
        const e = await c.exec(sent(q)).then(
          () => undefined,
          (e: unknown) => e,
        );
        const { message: _, ...n } = driver.native(e) ?? { code: "" };
        expect(n).toEqual(want);
        expect(driver.broken(e)).toBe(false);
      }
      c.release();
      await p.close();
    });

    test("a pool waits, hands over and closes", async () => {
      const [p, c] = await open();
      const waiting = p.acquire();
      const aborted = new AbortController();
      const gives = p.acquire(aborted.signal);
      aborted.abort(new Error("gave up"));
      await expect(gives).rejects.toThrow("gave up");
      c.release();
      const c2 = await waiting;
      // A discarded connection's waiter gets a new one.
      await c2.begin({ readOnly: false });
      const next = p.acquire();
      c2.release();
      const c3 = await next;
      expect(await all(c3, "SELECT 1")).toEqual([[1n]]);
      const late = p.acquire();
      await p.close();
      await expect(late).rejects.toThrow("the pool is closed");
      await expect(p.acquire()).rejects.toThrow("the pool is closed");
      c3.release(); // closed with the pool
      const stop = new AbortController();
      stop.abort();
      await expect(p.acquire(stop.signal)).rejects.toThrow();
      await expect(c3.query(sent("SELECT 1", [], { signal: stop.signal }))).rejects.toThrow();
      await expect(c3.exec(sent("SELECT 1", [], { signal: stop.signal }))).rejects.toThrow();
      await expect(c3.begin({ readOnly: false }, stop.signal)).rejects.toThrow();
      await expect(c3.savepoint("s", stop.signal)).rejects.toThrow();
      // A closed connection is broken.
      await expect(c3.rollback()).rejects.toThrow();
      const e = await c3.exec(sent("SELECT 2")).then(
        () => undefined,
        (e: unknown) => e,
      );
      expect(driver.broken(e)).toBe(true);
      expect(driver.native(e)).toBeUndefined();
    });

    test("a setup that fails fails the acquire, and the waiter of a discarded connection", async () => {
      const p = await driver.open({ dsn: database(), maxConns: 0, setup: ["NOT SQL"], mode: "simple" });
      await expect(p.acquire()).rejects.toThrow();
      await p.close();
      // A waiter whose new connection fails to open gets the error.
      let fail = false;
      const flaky = new SqliteDriver((path) => {
        if (fail) {
          throw new Error("cannot open");
        }
        return node(DatabaseSync)(path);
      });
      const q = await flaky.open({ dsn: database(), maxConns: 1, setup: [], mode: "prepare" });
      const c = await q.acquire();
      await c.begin({ readOnly: false });
      const next = q.acquire();
      fail = true;
      c.release();
      await expect(next).rejects.toThrow("cannot open");
      await q.close();
    });
  });
}

test("the runtime's binding", async () => {
  for (const onBun of [true, false]) {
    const db = (await binding(onBun))(":memory:");
    expect([...db.prepare("SELECT 1").rows([], false)]).toEqual([[1n]]);
    db.close();
  }
});

test("codes of errors", () => {
  const deno = (message: string) => Object.assign(new Error(message), { code: "ERR_SQLITE_ERROR" });
  expect(codeOf(deno("UNIQUE constraint failed: t.a"))).toBe(2067);
  expect(codeOf(deno("database is locked"))).toBe(5);
  expect(codeOf(deno("no such column: x in SELECT x at offset 7"))).toBe(1);
  expect(codeOf(Object.assign(new Error("x"), { errno: 5, code: "SQLITE_BUSY" }))).toBe(5);
  expect(codeOf(Object.assign(new Error("x"), { errno: 5, code: "ENOENT" }))).toBeUndefined();
  expect(codeOf("x")).toBeUndefined();
  const d = new SqliteDriver();
  expect(d.native(new Error("x"))).toBeUndefined();
  expect(d.broken("x")).toBe(false);
  // Messages that do not have the form keep only their code.
  for (const code of [1555, 1299, 275]) {
    expect(native(code, "other")).toEqual({ code: String(code), message: "other" });
  }
  expect(encode("x", "")).toBe("x");
});
