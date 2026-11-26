// MySQL for the scenarios: fresh databases, injected failures and ended
// connections, as conformance/harness/go/mysql.go does them. DSNs are in the
// Go driver's syntax, user:password@tcp(host:port)/database, which both
// harnesses read from DQL_TEST_MYSQL_URL; the TypeScript driver takes a URL.

import { readFile } from "node:fs/promises";
import mysql from "mysql2";
import { type Inject, duration } from "./harness.ts";

/** DSN is the Go driver's DSN syntax, [user[:password]@][tcp(address)]/[database][?params], whose password may hold @; the parameters are the Go driver's own. */
const DSN = /^(?:([^:@/]*)(?::(.*))?@)?(?:tcp\(([^)]*)\))?\/([^?]*)(?:\?.*)?$/;

/** url turns a DSN in the Go driver's syntax into the URL the TypeScript driver takes. */
export function url(dsn: string): string {
  const m = DSN.exec(dsn);
  if (m === null) {
    throw new Error(`${dsn} is not a MySQL DSN`);
  }
  const [, user, password, address, db] = m;
  const auth = user === undefined ? "" : `${encodeURIComponent(user)}${password === undefined ? "" : `:${encodeURIComponent(password)}`}@`;
  return `mysql://${auth}${address ?? "127.0.0.1:3306"}/${db}`;
}

/** withDatabase returns dsn, a DSN url reads, naming the database db instead. */
function withDatabase(dsn: string, db: string): string {
  return `${dsn.slice(0, dsn.lastIndexOf("/") + 1)}${db}`;
}

/** query runs sql, which may hold several statements, on a connection of its own to dsn, and returns the rows of its last statement. */
export async function query(dsn: string, sql: string): Promise<Record<string, unknown>[]> {
  const c = mysql.createConnection({ uri: url(dsn), multipleStatements: true });
  try {
    const r = await new Promise<unknown>((resolve, reject) => c.query(sql, (e, r) => (e === null ? resolve(r) : reject(e))));
    return Array.isArray(r) ? (r as Record<string, unknown>[]) : [];
  } finally {
    c.destroy();
  }
}

/** run runs sql, which may hold several statements, on a connection of its own to dsn. */
export async function run(dsn: string, sql: string): Promise<void> {
  await query(dsn, sql);
}

/** errnos are the error numbers MySQL raises for the classes a step injects. MySQL reports a serialization conflict as a deadlock, so it has no serialization failure to inject. */
const errnos: Readonly<Record<string, number>> = { Deadlock: 1213, LockTimeout: 1205 };

/** inject sets the plan of conformance/schemas/mysql/inject.sql: the next times writes sleep for delay, or else fail with class's error number. Times 0 clears it. */
export async function inject(dsn: string, i: Inject): Promise<void> {
  let code = errnos[i.class ?? ""];
  let delay = "NULL";
  if (i.delay !== undefined) {
    let ms: number;
    try {
      ms = duration(i.delay);
    } catch (e) {
      throw new Error(`delay: ${(e as Error).message}`);
    }
    delay = String(Math.round(ms * 1000));
    code ??= 0;
  }
  const times = i.times ?? 0;
  if (code === undefined && times > 0) {
    throw new Error(`no injected failure of class ${i.class}`);
  }
  await run(
    dsn,
    `DELETE FROM conformance_injection;
INSERT INTO conformance_injection VALUES (${code ?? 0}, ${times}, ${delay});
TRUNCATE conformance_injected;`,
  );
}

/** replay stops or starts the applier of the replica at dsn, which stops or resumes its replay of what it receives. */
export async function replay(dsn: string, pause: boolean): Promise<void> {
  await run(dsn, pause ? "STOP REPLICA SQL_THREAD" : "START REPLICA SQL_THREAD");
}

/** replicaOf returns the DSN of the database at dsn on the MySQL replica at base, once the replica has applied everything the primary had executed when it was called: the database's creation and its DDL. */
export async function replicaOf(dsn: string, base: string, timeout = 10): Promise<string> {
  const [row] = await query(dsn, "SELECT @@GLOBAL.gtid_executed AS executed");
  const executed = String(row?.["executed"]);
  const replica = withDatabase(base, dsn.slice(dsn.lastIndexOf("/") + 1));
  // Not on the database, which the replica may not have created yet.
  const [r] = await query(base, `SELECT WAIT_FOR_EXECUTED_GTID_SET('${executed}', ${timeout}) AS timed_out`);
  if (Number(r?.["timed_out"]) !== 0) {
    throw new Error(`the replica has not applied ${executed}`);
  }
  return replica;
}

/** terminate ends every other connection to the database at dsn, as a server shutting them down would. */
export async function terminate(dsn: string): Promise<void> {
  const rows = await query(dsn, "SELECT id FROM information_schema.processlist WHERE db = DATABASE() AND id <> CONNECTION_ID()");
  // A connection may end on its own meanwhile, so a kill's failure does not count.
  await Promise.allSettled(rows.map((r) => run(dsn, `KILL ${String(r["id"])}`)));
}

let made = 0;

/** databaseIn returns how to create a database in collation from the DDL files, in order, on the server base names, a DSN without a database, returning its DSN and how to drop it. */
function databaseIn(collation: string) {
  return async (base: string, ddl: readonly string[]): Promise<{ dsn: string; drop: () => Promise<void> }> => {
    let schema = "";
    for (const f of ddl) {
      schema += `${await readFile(f, "utf8")}\n`;
    }
    const name = `conformance_ts_${process.pid}_${Date.now()}_${made++}`;
    await run(base, `CREATE DATABASE ${name} CHARACTER SET utf8mb4 COLLATE ${collation}`);
    const drop = () => run(base, `DROP DATABASE IF EXISTS ${name}`);
    const dsn = withDatabase(base, name);
    try {
      await run(dsn, schema);
    } catch (e) {
      await drop();
      throw e;
    }
    return { dsn, drop };
  };
}

/** database creates a database from the DDL files on the MySQL server base names, as databaseIn does. */
export const database = databaseIn("utf8mb4_0900_ai_ci");

/** mariadbDatabase creates one on a MariaDB server, in the UCA 14.0 collation of the same meaning. */
export const mariadbDatabase = databaseIn("utf8mb4_uca1400_ai_ci");
