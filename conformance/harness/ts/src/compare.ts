// compare checks that the TypeScript harness's outcomes are identical to Go's
// (guide M5): for each statement mode both have, each scenario's steps must
// have produced the same results, errors and hook events. Values that differ
// from run to run, generated UUIDs and times, are masked first, each UUID by
// the order it first appears in, and a null field is taken as absent, since
// Go writes a field its shape leaves unset as null and TypeScript leaves it
// out.
// src/check.ts runs it.

import { readFile, readdir } from "node:fs/promises";
import { join } from "node:path";
import type { Outcome } from "./harness.ts";
import { type Json, isObject } from "./values.ts";

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const time = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/;

/** mask returns v with its UUIDs and times masked and its null fields dropped; uuids numbers the UUIDs seen so far. */
export function mask(v: Json, uuids: Map<string, number>): Json {
  if (typeof v === "string") {
    if (uuid.test(v)) {
      if (!uuids.has(v)) {
        uuids.set(v, uuids.size + 1);
      }
      return `<uuid ${uuids.get(v)}>`;
    }
    return time.test(v) ? "<time>" : v;
  }
  if (Array.isArray(v)) {
    return v.map((e) => mask(e, uuids));
  }
  if (isObject(v)) {
    const out: Record<string, Json> = {};
    for (const k of Object.keys(v).sort()) {
      if (v[k] !== null) {
        out[k] = mask(v[k] as Json, uuids);
      }
    }
    return out;
  }
  return v;
}

/** normal returns a scenario's outcomes, each masked, one to a line. */
export function normal(outcomes: readonly Outcome[]): string[] {
  const uuids = new Map<string, number>();
  return outcomes.map((o) =>
    JSON.stringify(
      mask(
        { step: o.step, op: o.op, result: o.error === undefined ? (o.result ?? null) : null, error: o.error ?? null, events: o.events ?? [] },
        uuids,
      ),
    ),
  );
}

/** diff returns how the outcomes of one scenario differ: the first step where they part. */
export function diff(name: string, want: readonly Outcome[], got: readonly Outcome[]): string[] {
  const [w, g] = [normal(want), normal(got)];
  for (let i = 0; i < Math.max(w.length, g.length); i++) {
    if (w[i] !== g[i]) {
      return [`${name}: outcome ${i + 1} differs:\n  go: ${w[i] ?? "(none)"}\n  ts: ${g[i] ?? "(none)"}`];
    }
  }
  return [];
}

/** files returns the outcome files under dir, by their paths relative to it, sorted. */
async function files(dir: string): Promise<string[]> {
  const out: string[] = [];
  for (const e of await readdir(dir, { withFileTypes: true })) {
    if (e.isDirectory()) {
      out.push(...(await files(join(dir, e.name))).map((f) => join(e.name, f)));
    } else {
      out.push(e.name);
    }
  }
  return out.sort();
}

/** compare compares the outcomes in two directories, each holding <mode>/<scenario>.json for Postgres and <dialect>/<mode>/<scenario>.json for the others, over the files the second has. */
export async function compare(goDir: string, tsDir: string): Promise<string[]> {
  const problems: string[] = [];
  for (const f of await files(tsDir)) {
    const read = async (dir: string) => JSON.parse(await readFile(join(dir, f), "utf8")) as Outcome[];
    let want: Outcome[];
    try {
      want = await read(goDir);
    } catch (e) {
      problems.push(`${f}: no Go outcomes: ${(e as Error).message}`);
      continue;
    }
    problems.push(...diff(f.replace(/\.json$/, ""), want, await read(tsDir)));
  }
  return problems;
}
