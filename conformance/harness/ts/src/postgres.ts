// Postgres for the scenarios: fresh databases, injected failures, ended
// connections and paused replay, as conformance/harness/go/schemas.go does
// them.

import { readFile } from "node:fs/promises";
import pg from "pg";
import { type Inject, duration } from "./harness.ts";

/** run runs sql, which may hold several statements, on a connection of its own to dsn. */
export async function run(dsn: string, sql: string): Promise<void> {
  await query(dsn, sql);
}

/** query runs sql on a connection of its own to dsn and returns its rows. */
export async function query(dsn: string, sql: string): Promise<Record<string, unknown>[]> {
  const c = new pg.Client({ connectionString: dsn });
  await c.connect();
  try {
    return (await c.query(sql)).rows;
  } finally {
    await c.end();
  }
}

/** sqlstates are the codes Postgres raises for the classes a step injects: the retryable ones, and a lock timeout for retry(policy).do. */
const sqlstates: Readonly<Record<string, string>> = { SerializationFailure: "40001", Deadlock: "40P01", LockTimeout: "55P03" };

/** inject sets the plan of conformance/schemas/postgres/inject.sql: the next times writes sleep for delay, or else fail with class's SQLSTATE. Times 0 clears it. */
export async function inject(dsn: string, i: Inject): Promise<void> {
  let code = sqlstates[i.class ?? ""];
  let delay = "NULL";
  if (i.delay !== undefined) {
    let ms: number;
    try {
      ms = duration(i.delay);
    } catch (e) {
      throw new Error(`delay: ${(e as Error).message}`);
    }
    delay = `'${Math.round(ms * 1000)} microseconds'`;
    code ??= "";
  }
  const times = i.times ?? 0;
  if (code === undefined && times > 0) {
    throw new Error(`no injected failure of class ${i.class}`);
  }
  await run(
    dsn,
    `TRUNCATE conformance.injection;
INSERT INTO conformance.injection VALUES ('${code ?? ""}', ${times}, ${delay});
SELECT setval('conformance.injected', 1, false);`,
  );
}

/** terminate ends every other connection to the database at dsn, as a server shutting them down would. */
export async function terminate(dsn: string): Promise<void> {
  await run(dsn, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = current_database() AND pid <> pg_backend_pid()");
}

/** replay pauses or resumes WAL replay on the replica at dsn. */
export async function replay(dsn: string, pause: boolean): Promise<void> {
  await run(dsn, pause ? "SELECT pg_wal_replay_pause()" : "SELECT pg_wal_replay_resume()");
}

let made = 0;

/** database creates a database from the DDL files, in order, on the server base names, and returns its URL and how to drop it. */
export async function database(base: string, ddl: readonly string[]): Promise<{ dsn: string; drop: () => Promise<void> }> {
  let schema = "";
  for (const f of ddl) {
    schema += `${await readFile(f, "utf8")}\n`;
  }
  const name = `conformance_ts_${process.pid}_${Date.now()}_${made++}`;
  await run(base, `CREATE DATABASE ${name} TEMPLATE template0 ENCODING 'UTF8' LOCALE 'C'`);
  const drop = () => run(base, `DROP DATABASE IF EXISTS ${name} WITH (FORCE)`);
  const u = new URL(base);
  u.pathname = `/${name}`;
  try {
    await run(u.toString(), schema);
  } catch (e) {
    await drop();
    throw e;
  }
  return { dsn: u.toString(), drop };
}

/** replicaOf returns the URL of the database at dsn on the replica server at base, once the replica has replayed everything the primary had written when it was called: the database's creation and its DDL. */
export async function replicaOf(dsn: string, base: string, timeout = 10_000): Promise<string> {
  const u = new URL(base);
  u.pathname = new URL(dsn).pathname;
  const [row] = await query(dsn, "SELECT pg_current_wal_lsn()::text AS lsn");
  const lsn = String(row?.["lsn"]);
  const deadline = Date.now() + timeout;
  for (;;) {
    let err: unknown;
    try {
      const [r] = await query(u.toString(), `SELECT pg_last_wal_replay_lsn() >= '${lsn}'::pg_lsn AS replayed`);
      if (r?.["replayed"] === true) {
        return u.toString();
      }
    } catch (e) {
      err = e;
    }
    if (Date.now() > deadline) {
      throw new Error(`the replica has not replayed ${lsn}: ${String(err)}`);
    }
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
}
