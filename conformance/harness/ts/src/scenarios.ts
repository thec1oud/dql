// Running the scenarios: each on a fresh database of its schema, on each
// dialect.

import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import type { StatementMode } from "@dql/runtime";
import { Recorder } from "./events.ts";
import { type Outcome, type Scenario, type Target, load, run } from "./harness.ts";
import * as mysql from "./mysql.ts";
import * as postgres from "./postgres.ts";
import { root, schemas } from "./schemas.ts";
import * as sqlite from "./sqlite.ts";

/** Dialect is how the scenarios run on one dialect: its name in messages, the variable naming its server, empty for one that has none, the statement modes they run in, the features its schemas hold, how to create a database on its server, and, where it has one, the variable naming a replica of the server and how to find a database's copy there. */
export interface Dialect {
  readonly label: string;
  readonly env: string;
  readonly modes: readonly StatementMode[];
  readonly features: readonly string[];
  database(base: string, ddl: readonly string[]): Promise<{ dsn: string; drop: () => Promise<void> }>;
  readonly replica?: { readonly env: string; of(dsn: string, base: string): Promise<string> };
}

/**
 * dialects are the dialects the scenarios run on, in order, as
 * conformance/harness/go runs them. On Postgres the modes are those the
 * TypeScript driver has, simple needing a protocol node-postgres does not
 * offer; on MySQL and MariaDB they are Go's, whose outcomes the TypeScript
 * ones are compared with. SQLite runs in the process, with no server, and
 * prepares every statement.
 */
export const dialects: Readonly<Record<string, Dialect>> = {
  postgres: {
    label: "Postgres",
    env: "DQL_TEST_POSTGRES_URL",
    modes: ["prepare", "describe"],
    features: ["arrays", "composites", "named-foreign-keys", "params-65535", "pattern-checks", "regex-match", "replica", "returning", "serialization-failure", "settable-identity", "stall"],
    database: postgres.database,
    replica: { env: "DQL_TEST_POSTGRES_REPLICA_URL", of: (dsn, base) => postgres.replicaOf(dsn, base) },
  },
  mysql: {
    label: "MySQL",
    env: "DQL_TEST_MYSQL_URL",
    modes: ["prepare", "simple"],
    features: ["named-foreign-keys", "params-65535", "pattern-checks", "replica", "settable-identity", "stall", "tinyint-bool"],
    database: mysql.database,
    replica: { env: "DQL_TEST_MYSQL_REPLICA_URL", of: (dsn, base) => mysql.replicaOf(dsn, base) },
  },
  mariadb: {
    label: "MariaDB",
    env: "DQL_TEST_MARIADB_URL",
    modes: ["prepare", "simple"],
    features: ["named-foreign-keys", "params-65535", "pattern-checks", "settable-identity", "stall", "tinyint-bool"],
    database: mysql.mariadbDatabase,
  },
  sqlite: {
    label: "SQLite",
    env: "",
    modes: ["prepare"],
    features: ["params-32766", "returning", "settable-identity"],
    database: sqlite.database,
  },
};

/** dialectOf returns the named dialect. */
function dialectOf(name: string): Dialect {
  const d = dialects[name];
  if (d === undefined) {
    throw new Error(`no dialect ${name}`);
  }
  return d;
}

/** missing returns the features sc requires that dialect's schemas lack: such a scenario is skipped there. */
export function missing(sc: Scenario, dialect = "postgres"): string[] {
  const has = dialectOf(dialect).features;
  return (sc.requires ?? []).filter((f) => !has.includes(f));
}

/** env returns an environment variable, or the empty string. */
function env(name: string): string {
  return process.env[name] ?? "";
}

/** Opened is a target on a fresh database, and how to close its executor and drop the database. */
export interface Opened {
  readonly target: Target;
  close(): Promise<void>;
}

/**
 * open creates a database of the named schema of dialect on the server its
 * variable names, such as DQL_TEST_POSTGRES_URL, or in a file of its own on
 * a dialect with no server, and opens its handle, with
 * the database's copy on the dialect's replica of that server, such as
 * DQL_TEST_POSTGRES_REPLICA_URL's, as its replica when replica is set, and at most
 * maxConns connections to the primary, 0 meaning the schema's default,
 * sending statements in mode.
 */
export async function open(name: string, mode: StatementMode, replica: boolean, maxConns: number, dialect = "postgres"): Promise<Opened> {
  const d = dialectOf(dialect);
  const base = env(d.env);
  if (d.env !== "" && base === "") {
    throw new Error(`${d.env} is not set`);
  }
  const rep = d.replica;
  if (replica && rep === undefined) {
    throw new Error(`${d.label} has no replica`);
  }
  const replicaBase = rep === undefined ? "" : env(rep.env);
  if (replica && replicaBase === "") {
    throw new Error(`${rep?.env} is not set`);
  }
  const s = (await schemas(dialect))[name];
  if (s === undefined) {
    throw new Error(`no schema ${name}`);
  }
  const { dsn, drop } = await d.database(
    base,
    s.ddl.map((f) => join(root, f)),
  );
  try {
    const replicas = replica && rep !== undefined ? [await rep.of(dsn, replicaBase)] : [];
    const events = new Recorder();
    const { db, exec } = await s.open(dsn, replicas, maxConns, mode, events.hooks());
    return {
      target: { schema: s, db, exec, dsn, ...(replica ? { replicaDsn: replicas[0] as string } : {}), events },
      async close() {
        await exec.close();
        await drop();
      },
    };
  } catch (e) {
    await drop();
    throw e;
  }
}

/** runScenario runs one scenario in mode on a fresh database of its schema on dialect, and returns its problems and outcomes. */
export async function runScenario(sc: Scenario, mode: StatementMode, dialect = "postgres"): Promise<{ problems: string[]; outcomes: Outcome[] }> {
  const o = await open(sc.schema, mode, sc.replica ?? false, sc.max_conns ?? 0, dialect);
  try {
    const outcomes: Outcome[] = [];
    const problems = await run(sc, o.target, (x) => outcomes.push(x));
    return { problems, outcomes };
  } finally {
    await o.close();
  }
}

/** skipped returns why sc does not run on dialect, or undefined when it does: dialect has no schema of its name, or lacks a feature it requires. */
export async function skipped(sc: Scenario, dialect: string): Promise<string | undefined> {
  const d = dialectOf(dialect);
  if ((await schemas(dialect))[sc.schema] === undefined) {
    return `${d.label} has no schema ${sc.schema}`;
  }
  const lacks = missing(sc, dialect);
  return lacks.length > 0 ? `${d.label} lacks ${lacks.join(", ")}` : undefined;
}

/**
 * scenarios runs the named scenarios of dir, or all of them, on each
 * dialect in each of its modes, prints each problem with print, writes the
 * outcomes to DQL_CONFORMANCE_OUTCOMES if it is set, and returns how many
 * problems there were. As in Go, a Postgres scenario's name and outcomes
 * are <mode>/<scenario>, and another dialect's <dialect>/<mode>/<scenario>.
 */
export async function scenarios(names: readonly string[], print: (line: string) => void, dir = join(root, "conformance", "scenarios")): Promise<number> {
  const scs = (await load(dir)).filter((s) => names.length === 0 || names.includes(s.scenario));
  const out = env("DQL_CONFORMANCE_OUTCOMES");
  let problems = 0;
  for (const [dialect, d] of Object.entries(dialects)) {
    const prefix = dialect === "postgres" ? "" : `${dialect}/`;
    for (const mode of d.modes) {
      for (const sc of scs) {
        const why = await skipped(sc, dialect);
        if (why !== undefined) {
          // It runs on another dialect; Go writes no outcomes for it here either.
          print(`skip ${prefix}${mode}/${sc.scenario}: ${why}`);
          continue;
        }
        const r = await runScenario(sc, mode, dialect);
        for (const p of r.problems) {
          print(`${prefix}${mode}/${p}`);
        }
        problems += r.problems.length;
        if (out !== "") {
          await mkdir(join(out, prefix, mode), { recursive: true });
          await writeFile(join(out, prefix, mode, `${sc.scenario}.json`), `${JSON.stringify(r.outcomes, null, 1)}\n`);
        }
        print(`${r.problems.length === 0 ? "ok" : "FAIL"} ${prefix}${mode}/${sc.scenario}`);
      }
    }
  }
  return problems;
}
