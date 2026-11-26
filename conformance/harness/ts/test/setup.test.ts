// Setting up what scenarios run on, and running them from the command line.

import { afterEach, expect, test } from "bun:test";
import { mkdtemp, readFile, readdir, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import * as mysql from "../src/mysql.ts";
import { database, replicaOf } from "../src/postgres.ts";
import { missing, open, scenarios } from "../src/scenarios.ts";
import { root, rotationWait, schemas } from "../src/schemas.ts";

const base = process.env["DQL_TEST_POSTGRES_URL"] ?? "";
const mysqlBase = process.env["DQL_TEST_MYSQL_URL"] ?? "";
const saved = { ...process.env };

afterEach(() => {
  process.env["DQL_TEST_POSTGRES_URL"] = saved["DQL_TEST_POSTGRES_URL"] as string;
  process.env["DQL_TEST_POSTGRES_REPLICA_URL"] = saved["DQL_TEST_POSTGRES_REPLICA_URL"] as string;
  process.env["DQL_TEST_MYSQL_URL"] = saved["DQL_TEST_MYSQL_URL"] as string;
  delete process.env["DQL_CONFORMANCE_OUTCOMES"];
});

test("a database whose DDL fails is dropped", async () => {
  const ddl = join(await mkdtemp(join(tmpdir(), "ddl-")), "bad.sql");
  await writeFile(ddl, "NOT SQL");
  await expect(database(base, [ddl])).rejects.toThrow("syntax error");
});

test("a server that is no replica", async () => {
  const shop = (await schemas())["shop"];
  const { dsn, drop } = await database(
    base,
    (shop?.ddl ?? []).map((f) => join(root, f)),
  );
  try {
    await expect(replicaOf(dsn, base, 100)).rejects.toThrow("the replica has not replayed");
    // A replica that cannot be reached fails with the error it gave.
    const down = new URL(base);
    down.port = "1";
    down.hostname = "localhost";
    await expect(replicaOf(dsn, down.toString(), 100)).rejects.toThrow(/has not replayed \S+: \w*Error/);
    rotationWait.ms = 100;
    await expect(shop?.open(dsn, [dsn], 0, "prepare", {}) as Promise<unknown>).rejects.toThrow("the replica did not enter rotation");
  } finally {
    rotationWait.ms = 10_000;
    await drop();
  }
});

test("open", async () => {
  await expect(open("nope", "prepare", false, 0)).rejects.toThrow("no schema nope");
  // The driver has no simple mode, so the executor does not open.
  await expect(open("shop", "simple", false, 0)).rejects.toThrow("statement mode");
  process.env["DQL_TEST_POSTGRES_REPLICA_URL"] = "";
  await expect(open("shop", "prepare", true, 0)).rejects.toThrow("DQL_TEST_POSTGRES_REPLICA_URL is not set");
  process.env["DQL_TEST_POSTGRES_URL"] = "";
  await expect(open("shop", "prepare", false, 0)).rejects.toThrow("DQL_TEST_POSTGRES_URL is not set");
  process.env["DQL_TEST_MYSQL_URL"] = "";
  await expect(open("shop", "prepare", false, 0, "mysql")).rejects.toThrow("DQL_TEST_MYSQL_URL is not set");
  await expect(open("shop", "prepare", false, 0, "nope")).rejects.toThrow("no dialect nope");
  expect(() => missing({ scenario: "s", schema: "shop", steps: [] } as never, "nope")).toThrow("no dialect nope");
});

test("MySQL", async () => {
  expect(mysql.url("u:p@w@tcp(db:3307)/x")).toBe("mysql://u:p%40w@db:3307/x");
  expect(mysql.url("u@/x?parseTime=true")).toBe("mysql://u@127.0.0.1:3306/x");
  expect(mysql.url("/")).toBe("mysql://127.0.0.1:3306/");
  expect(() => mysql.url("nonsense")).toThrow("nonsense is not a MySQL DSN");
  await expect(mysql.inject("/", { delay: "soon" })).rejects.toThrow("delay: ");
  await expect(mysql.inject("/", { class: "SerializationFailure", times: 1 })).rejects.toThrow("no injected failure of class SerializationFailure");
  // A database whose DDL fails is dropped.
  const ddl = join(await mkdtemp(join(tmpdir(), "ddl-")), "bad.sql");
  await writeFile(ddl, "NOT SQL");
  await expect(mysql.database(mysqlBase, [ddl])).rejects.toThrow("SQL syntax");
  const shop = (await schemas("mysql"))["shop"];
  await expect(shop?.replay("", true) as Promise<void>).rejects.toThrow("the MySQL schemas have no replica");
});

test("scenarios", async () => {
  const out = await mkdtemp(join(tmpdir(), "outcomes-"));
  process.env["DQL_CONFORMANCE_OUTCOMES"] = out;
  const lines: string[] = [];
  expect(await scenarios(["raw"], (l) => lines.push(l))).toBe(0);
  expect(lines).toEqual(["ok prepare/raw", "ok describe/raw", "ok mysql/prepare/raw", "ok mysql/simple/raw"]);
  expect((await readdir(out)).sort()).toEqual(["describe", "mysql", "prepare"]);
  expect((await readdir(join(out, "mysql"))).sort()).toEqual(["prepare", "simple"]);
  expect(JSON.parse(await readFile(join(out, "prepare", "raw.json"), "utf8"))[0].op).toBe("tenants.insert");
  expect(JSON.parse(await readFile(join(out, "mysql", "simple", "raw.json"), "utf8"))[0].op).toBe("tenants.insert");
});

test("scenarios with problems", async () => {
  const dir = await mkdtemp(join(tmpdir(), "scenarios-"));
  await writeFile(join(dir, "s.yaml"), "scenario: s\nschema: shop\nsteps:\n  - op: tenants.count\n    expect: { value: 1 }\n");
  const lines: string[] = [];
  expect(await scenarios([], (l) => lines.push(l), dir)).toBe(4);
  expect(lines).toEqual(
    ["prepare", "describe", "mysql/prepare", "mysql/simple"].flatMap((m) => [`${m}/s step 1 (tenants.count): want value 1, got 0`, `FAIL ${m}/s`]),
  );
});

test("a scenario needing a feature a dialect lacks is skipped there", async () => {
  const lines: string[] = [];
  expect(await scenarios(["tinyint-bool"], (l) => lines.push(l))).toBe(0);
  expect(lines).toEqual([
    "skip prepare/tinyint-bool: Postgres lacks tinyint-bool",
    "skip describe/tinyint-bool: Postgres lacks tinyint-bool",
    "ok mysql/prepare/tinyint-bool",
    "ok mysql/simple/tinyint-bool",
  ]);
});

test("a scenario on a schema a dialect lacks is skipped there", async () => {
  const dir = await mkdtemp(join(tmpdir(), "scenarios-"));
  await writeFile(join(dir, "v.yaml"), "scenario: v\nschema: shop_variants\nsteps:\n  - op: tenants.count\n    expect: { value: 0 }\n");
  const lines: string[] = [];
  expect(await scenarios([], (l) => lines.push(l), dir)).toBe(0);
  expect(lines).toEqual(["ok prepare/v", "ok describe/v", "skip mysql/prepare/v: MySQL has no schema shop_variants", "skip mysql/simple/v: MySQL has no schema shop_variants"]);
});
