// Running the scenarios: each on a fresh database of its schema.

import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import type { StatementMode } from "@dql/runtime";
import { Recorder } from "./events.ts";
import { type Outcome, type Scenario, type Target, load, run } from "./harness.ts";
import { database, replicaOf } from "./postgres.ts";
import { root, schemas } from "./schemas.ts";

/** modes are the statement modes the TypeScript driver has; simple needs a protocol node-postgres does not offer. */
export const modes: readonly StatementMode[] = ["prepare", "describe"];

/** features are the features the Postgres schemas hold, as conformance/harness/go's postgresFeatures lists them. */
export const features: readonly string[] = ["arrays", "composites", "regex-match", "replica", "returning", "serialization-failure"];

/** missing returns the features sc requires that the Postgres schemas lack: such a scenario is skipped. */
export function missing(sc: Scenario): string[] {
  return (sc.requires ?? []).filter((f) => !features.includes(f));
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
 * open creates a database of the named schema on the server of
 * DQL_TEST_POSTGRES_URL and opens its handle, with the database's copy on
 * the server of DQL_TEST_POSTGRES_REPLICA_URL, a streaming replica of it,
 * as its replica when replica is set, and at most maxConns connections to
 * the primary, 0 meaning the schema's default, sending statements in mode.
 */
export async function open(name: string, mode: StatementMode, replica: boolean, maxConns: number): Promise<Opened> {
  const base = env("DQL_TEST_POSTGRES_URL");
  if (base === "") {
    throw new Error("DQL_TEST_POSTGRES_URL is not set");
  }
  const replicaBase = env("DQL_TEST_POSTGRES_REPLICA_URL");
  if (replica && replicaBase === "") {
    throw new Error("DQL_TEST_POSTGRES_REPLICA_URL is not set");
  }
  const s = (await schemas())[name];
  if (s === undefined) {
    throw new Error(`no schema ${name}`);
  }
  const { dsn, drop } = await database(
    base,
    s.ddl.map((f) => join(root, f)),
  );
  try {
    const replicas = replica ? [await replicaOf(dsn, replicaBase)] : [];
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

/** runScenario runs one scenario in mode on a fresh database of its schema, and returns its problems and outcomes. */
export async function runScenario(sc: Scenario, mode: StatementMode): Promise<{ problems: string[]; outcomes: Outcome[] }> {
  const o = await open(sc.schema, mode, sc.replica ?? false, sc.max_conns ?? 0);
  try {
    const outcomes: Outcome[] = [];
    const problems = await run(sc, o.target, (x) => outcomes.push(x));
    return { problems, outcomes };
  } finally {
    await o.close();
  }
}

/** scenarios runs the named scenarios of dir, or all of them, in each mode, prints each problem with print, writes the outcomes to DQL_CONFORMANCE_OUTCOMES if it is set, and returns how many problems there were. */
export async function scenarios(names: readonly string[], print: (line: string) => void, dir = join(root, "conformance", "scenarios")): Promise<number> {
  const scs = (await load(dir)).filter((s) => names.length === 0 || names.includes(s.scenario));
  const out = env("DQL_CONFORMANCE_OUTCOMES");
  let problems = 0;
  for (const mode of modes) {
    for (const sc of scs) {
      const lacks = missing(sc);
      if (lacks.length > 0) {
        // It runs on another dialect; Go writes no outcomes for it here either.
        print(`skip ${mode}/${sc.scenario}: Postgres lacks ${lacks.join(", ")}`);
        continue;
      }
      const r = await runScenario(sc, mode);
      for (const p of r.problems) {
        print(`${mode}/${p}`);
      }
      problems += r.problems.length;
      if (out !== "") {
        await mkdir(join(out, mode), { recursive: true });
        await writeFile(join(out, mode, `${sc.scenario}.json`), `${JSON.stringify(r.outcomes, null, 1)}\n`);
      }
      print(`${r.problems.length === 0 ? "ok" : "FAIL"} ${mode}/${sc.scenario}`);
    }
  }
  return problems;
}
