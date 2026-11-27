// SQL Server for the scenarios: fresh databases, injected stalls, deadlocks
// and lock timeouts, and ended sessions, as conformance/harness/go/sqlserver.go
// does them. A DSN is a sqlserver:// URL naming its database with ?database=,
// which both harnesses read from DQL_TEST_SQLSERVER_URL and @dql/sqlserver
// takes.

import { readFile } from "node:fs/promises";
import { config } from "@dql/sqlserver";
import { Connection, Request } from "tedious";
import { type Inject, duration } from "./harness.ts";

/** batches splits a script at its GO lines, as sqlcmd does. */
const batches = /^[ \t]*GO[ \t]*$/im;

/** connect opens a connection of its own to dsn. */
function connect(dsn: string): Promise<Connection> {
  return new Promise((resolve, reject) => {
    const c = new Connection(config(dsn));
    c.connect((e) => (e === undefined ? resolve(c) : reject(e)));
  });
}

/** batch runs one batch on c and returns its rows, each by column name. */
function batch(c: Connection, sql: string): Promise<Record<string, unknown>[]> {
  return new Promise((resolve, reject) => {
    const rows: Record<string, unknown>[] = [];
    const r = new Request(sql, (e) => (e === undefined || e === null ? resolve(rows) : reject(e)));
    r.on("row", (cols: { metadata: { colName: string }; value: unknown }[]) => rows.push(Object.fromEntries(cols.map((col) => [col.metadata.colName, col.value]))));
    c.execSqlBatch(r);
  });
}

/** query runs script, its batches separated by GO lines, on a connection of its own to dsn, with the session settings the sqlserver dialect's connections have, which indexed computed columns need, and returns the rows of its last batch. */
export async function query(dsn: string, script: string): Promise<Record<string, unknown>[]> {
  const c = await connect(dsn);
  try {
    let rows: Record<string, unknown>[] = [];
    for (const b of ["SET ANSI_NULLS ON; SET QUOTED_IDENTIFIER ON; SET ARITHABORT ON", ...script.split(batches)]) {
      if (b.trim() !== "") {
        rows = await batch(c, b);
      }
    }
    return rows;
  } finally {
    c.close();
  }
}

/** run runs script on a connection of its own to dsn. */
export async function run(dsn: string, script: string): Promise<void> {
  await query(dsn, script);
}

/** inject sets the plan of conformance/schemas/sqlserver/inject.sql: the next times writes wait for delay, or else meet a real deadlock or lock timeout against a session of the harness's own, which it starts. It first ends the sessions an earlier plan started. Times 0 clears it. */
export async function inject(dsn: string, i: Inject): Promise<void> {
  const times = i.times ?? 0;
  let kind = injectKinds[i.class ?? ""];
  let ms = 0;
  if (i.delay !== undefined) {
    try {
      ms = duration(i.delay);
    } catch (e) {
      throw new Error(`delay: ${(e as Error).message}`);
    }
    kind = "stall";
  }
  if (kind === undefined && times > 0) {
    throw new Error(`no injected failure of class ${i.class} on SQL Server`);
  }
  await run(
    dsn,
    `DECLARE @s INT, @kill NVARCHAR(32);
DECLARE helpers CURSOR LOCAL FAST_FORWARD FOR
  SELECT session_id FROM sys.dm_exec_sessions WHERE database_id = DB_ID() AND context_info = 0x64716C;
OPEN helpers;
FETCH NEXT FROM helpers INTO @s;
WHILE @@FETCH_STATUS = 0
BEGIN
  SET @kill = N'KILL ' + CAST(@s AS NVARCHAR(10));
  EXEC sp_executesql @kill;
  FETCH NEXT FROM helpers INTO @s;
END;
CLOSE helpers;
DEALLOCATE helpers;
DELETE FROM conformance.injection;
INSERT INTO conformance.injection VALUES ('${kind ?? ""}', ${times}, ${Math.round(ms)});
ALTER SEQUENCE conformance.injected RESTART WITH 1;`,
  );
  const script = helper(kind ?? "", times);
  if (script !== "") {
    // The helper's session ends with its script, the next plan, or the
    // database. A write the plan fails waits for it to take its locks.
    run(dsn, script).catch(() => {});
  }
}

/** injectKinds are the plan's kinds of the classes a step injects. */
const injectKinds: Readonly<Record<string, string>> = { Deadlock: "deadlock", LockTimeout: "lock_timeout" };

/** helper is the script of the harness's session for a plan of kind and times: for a deadlock, each n-th time, it holds blocker row (n, 'b') until the write holds (n, 'a'), then waits for that row at high deadlock priority; for a lock timeout, it holds row (0, 'b') for a minute. */
export function helper(kind: string, times: number): string {
  const head = "SET CONTEXT_INFO 0x64716C; SET DEADLOCK_PRIORITY HIGH;\n";
  if (times > 0 && kind === "deadlock") {
    let script = head;
    for (let n = 1; n <= times; n++) {
      script += `GO
BEGIN TRAN;
UPDATE conformance.blocker SET hits += 1 WHERE n = ${n} AND side = 'b';
EXEC conformance.held ${n}, 'a';
UPDATE conformance.blocker SET hits += 1 WHERE n = ${n} AND side = 'a';
COMMIT;
`;
    }
    return script;
  }
  if (times > 0 && kind === "lock_timeout") {
    return `${head}GO
BEGIN TRAN;
UPDATE conformance.blocker SET hits += 1 WHERE n = 0 AND side = 'b';
WAITFOR DELAY '00:01:00';
COMMIT;
`;
  }
  return "";
}

/** terminate ends every other session on the database at dsn, as a server shutting them down would. */
export async function terminate(dsn: string): Promise<void> {
  const rows = await query(
    dsn,
    "SELECT session_id FROM sys.dm_exec_sessions WHERE database_id = DB_ID() AND session_id <> @@SPID AND is_user_process = 1",
  );
  // A session may end on its own meanwhile, so a kill's failure does not count.
  await Promise.allSettled(rows.map((r) => run(dsn, `KILL ${String(r["session_id"])}`)));
}

/** replay reports that SQL Server's schemas have no replica. */
export async function replay(): Promise<never> {
  throw new Error("the SQL Server schemas have no replica");
}

let made = 0;

/** database creates a database from the DDL files, in order, on the SQL Server base names, a URL without a database, and returns its URL and how to drop it. */
export async function database(base: string, ddl: readonly string[]): Promise<{ dsn: string; drop: () => Promise<void> }> {
  const script: string[] = [];
  for (const f of ddl) {
    script.push(await readFile(f, "utf8"));
  }
  const name = `conformance_ts_${process.pid}_${Date.now()}_${made++}`;
  await run(base, `CREATE DATABASE ${name}`);
  const drop = () =>
    run(base, `IF DB_ID('${name}') IS NOT NULL BEGIN ALTER DATABASE ${name} SET SINGLE_USER WITH ROLLBACK IMMEDIATE; DROP DATABASE ${name} END`);
  const u = new URL(base);
  u.searchParams.set("database", name);
  const dsn = u.toString();
  try {
    await run(dsn, script.join("\nGO\n"));
  } catch (e) {
    await drop();
    throw e;
  }
  return { dsn, drop };
}
