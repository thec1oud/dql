// SQLite for the scenarios: fresh database files, as
// conformance/harness/go/sqlite.go makes them. SQLite runs in the process,
// so it needs no server, and it has no injected failures, stalls or
// replicas. A DSN is the path of the database file, which @dql/sqlite takes.

import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { binding } from "@dql/sqlite";

/** run runs sql, which may hold several statements, on a connection of its own to the database file dsn, waiting up to five seconds for another connection's lock. */
export async function run(dsn: string, sql: string): Promise<void> {
  const db = (await binding())(dsn);
  try {
    db.exec("PRAGMA busy_timeout = 5000");
    db.exec(sql);
  } finally {
    db.close();
  }
}

/** noServer is the error of what only a database server does: no scenario that needs it runs on SQLite. */
export async function noServer(): Promise<never> {
  throw new Error("SQLite runs in the process, with no server to inject failures, end connections or replicate");
}

/** database creates a database file from the DDL files, in order, in a directory of its own, and returns its path and how to remove it. base is unused: there is no server. */
export async function database(_base: string, ddl: readonly string[]): Promise<{ dsn: string; drop: () => Promise<void> }> {
  const dir = await mkdtemp(join(tmpdir(), "conformance-sqlite-"));
  const drop = () => rm(dir, { recursive: true, force: true });
  const dsn = join(dir, "shop.db");
  try {
    for (const f of ddl) {
      await run(dsn, await readFile(f, "utf8"));
    }
  } catch (e) {
    await drop();
    throw e;
  }
  return { dsn, drop };
}
