// The conformance harness for TypeScript (guide §6.3): it runs the scenarios
// of conformance/scenarios against the ts emitter's generated code, as
// conformance/harness/go does against the go emitter's, with the same steps,
// expectations and problems. Where Go's harness finds methods and types by
// reflection, this finds methods on the handles' objects, under the same
// spelling, and their types in the package's declarations (types.ts).
//
// Go carries a call's transaction, session, deadline and cancellation in a
// context.Context; the TypeScript runtime carries them in its ambient
// context. The runner keeps that context as a snapshot, a function that
// runs code in it, and runs each call through it.

import { AsyncLocalStorage } from "node:async_hooks";
import { readFile, readdir } from "node:fs/promises";
import { join } from "node:path";
import {
  Consistency,
  DEFAULT,
  DecodeError,
  type Executor,
  type Isolation,
  type Propagation,
  type RetryPolicy,
  UsageError,
  ValidationError,
  Violation,
  classOf,
  now,
  retry,
  withSession,
  withSignal,
  withTimeout,
} from "@dql/runtime";
import { parse } from "yaml";
import type { Recorder } from "./events.ts";
import { type Method, type Param, type Type, type Types, norm } from "./types.ts";
import { Decoder, type Json, encode, isObject, normalize, show } from "./values.ts";

/** Scenario is one scenario file: steps run in order against a fresh database of the named schema, on a source with a replica when replica is set. */
export interface Scenario {
  readonly scenario: string;
  readonly schema: string;
  /** The features the scenario needs its schema to hold, as conformance/harness/go's Scenario documents. */
  readonly requires?: readonly string[];
  readonly replica?: boolean;
  /** Bounds the primary's pool; absent is the schema's default. */
  readonly max_conns?: number;
  readonly steps: readonly Step[];
  /** Where the scenario was read from. */
  file?: string;
}

/** Step is one operation and what it must produce, as conformance/harness/go's Step documents. */
export interface Step {
  readonly op: string;
  readonly scope?: Json;
  readonly key?: Json;
  readonly input?: Json;
  readonly bind?: readonly Json[];
  readonly set?: Json;
  readonly where?: string;
  readonly assign?: readonly Assign[];
  readonly save?: string;
  readonly expect?: { readonly [key: string]: Json };
  readonly steps?: readonly Step[];
  readonly propagation?: string;
  readonly isolation?: string;
  readonly retry?: { readonly attempts?: number; readonly on?: readonly string[] };
  readonly inject?: Inject;
  readonly rollback?: boolean;
  readonly from?: string;
  readonly shape?: string;
  readonly filter?: readonly { readonly column: string; readonly op: string; readonly value?: Json }[];
  readonly order?: readonly { readonly column: string; readonly desc?: boolean }[];
  readonly limit?: number;
  readonly read?: string;
  readonly stop?: number;
  readonly deleted?: string;
  readonly consistency?: string;
  readonly bound?: string;
  readonly wait?: string;
  readonly or_fail?: boolean;
  readonly duration?: string;
  readonly deadline?: string;
  readonly cancel?: string;
  readonly terminate?: string;
  readonly end?: string;
  readonly sql?: { readonly [dialect: string]: string };
  readonly source?: string;
  readonly exec?: boolean;
  readonly call?: string;
  readonly args?: readonly Json[];
}

/** Assign is one expression on a column: set to now or default, or add or subtract a value. */
export interface Assign {
  readonly column: string;
  readonly set?: string;
  readonly add?: Json;
  readonly sub?: Json;
}

/** Inject is what the server does on the step's next times writes: sleep for delay, a Go duration, or else raise a failure of the retryable class. */
export interface Inject {
  readonly class?: string;
  readonly times?: number;
  readonly delay?: string;
}

/**
 * Schema is a schema the scenarios run against: its DDL files, relative to
 * the repository root, its generated package, as the module that exports
 * it and its declarations, and how to run statements on the database at a
 * DSN, make it fail or stall its next writes, end every other connection to
 * it, and pause or resume a replica's replay.
 */
export interface Schema {
  readonly ddl: readonly string[];
  readonly dialect: string;
  readonly module: Readonly<Record<string, unknown>>;
  readonly types: Types;
  exec(dsn: string, sql: string): Promise<void>;
  inject(dsn: string, inject: Inject): Promise<void>;
  terminate(dsn: string): Promise<void>;
  replay(dsn: string, pause: boolean): Promise<void>;
}

/** Target is what a scenario runs on: a schema's handle and its executor, open on the database at dsn and its replica at replicaDsn, if any, and the recorder of the executor's hook events. */
export interface Target {
  readonly schema: Schema;
  readonly db: object;
  readonly exec?: Executor;
  readonly dsn?: string;
  readonly replicaDsn?: string;
  readonly events?: Recorder;
}

/** Outcome is what one step produced, which the harnesses write so that CI can compare Go's with TypeScript's: its result or error, and the events it fired. */
export interface Outcome {
  readonly step: string;
  readonly op: string;
  readonly result?: Json;
  readonly error?: Json;
  readonly events?: Json;
}

/** Ctx runs code in a context, as a snapshot of the ambient context does. */
type Ctx = <R>(fn: () => R) => R;

/** load reads every scenario file in dir, sorted by file name. */
export async function load(dir: string): Promise<Scenario[]> {
  const files = (await readdir(dir)).filter((f) => f.endsWith(".yaml")).sort();
  const out: Scenario[] = [];
  for (const f of files) {
    const file = join(dir, f);
    let s: Scenario;
    try {
      s = parse(await readFile(file, "utf8")) as Scenario;
    } catch (e) {
      throw new Error(`${file}: ${(e as Error).message}`);
    }
    s.file = file;
    out.push(s);
  }
  return out;
}

/** run runs a scenario's steps on t and returns each way the outcomes differ from the expectations; record, if given, receives each step's outcome. */
export async function run(sc: Scenario, t: Target, record?: (o: Outcome) => void): Promise<string[]> {
  const r = new Runner(t, record);
  const problems: string[] = [];
  for (const [i, st] of sc.steps.entries()) {
    r.path = [i + 1];
    for (const p of (await r.run(st)).problems) {
      problems.push(`${sc.scenario} step ${i + 1} (${st.op}): ${p}`);
    }
  }
  if (r.paused) {
    try {
      await t.schema.replay(t.replicaDsn ?? "", false);
    } catch (e) {
      problems.push(`${sc.scenario}: resume_replay: ${message(e)}`);
    }
  }
  return problems;
}

/** Ran is a step's problems, and the error of a call that failed where the step expects none. */
interface Ran {
  readonly problems: string[];
  readonly failed?: { readonly error: unknown };
}

/** errRollback ends a run of a transaction step's closure that rolls back. */
const errRollback = new Error("conformance: rolled back");

const propagations: readonly string[] = ["join", "independent", "nested", "mandatory", "never"];

/** misuses are the runtime's errors of misuse, which have no class, by the name an error expectation's misuse gives them. */
const misuses: Readonly<Record<string, string>> = {
  no_transaction: "NoTransaction",
  in_transaction: "InTransaction",
  read_only: "ReadOnly",
  independent_depth: "IndependentDepth",
  unknown_source: "UnknownSource",
};

/** snapshot captures the current ambient context. */
function snapshot(): Ctx {
  return AsyncLocalStorage.snapshot() as Ctx;
}

class Runner {
  ctx: Ctx = snapshot();
  readonly base: Ctx = this.ctx;
  db: object;
  readonly saved = new Map<string, Json>();
  readonly decoder: Decoder;
  readonly types: Types;
  readonly module: Readonly<Record<string, unknown>>;
  fired: Json[] = [];
  paused = false;
  /** The step being run, as its index and those of the steps that hold it, from 1. */
  path: number[] = [];

  readonly t: Target;
  readonly record: ((o: Outcome) => void) | undefined;

  constructor(t: Target, record: ((o: Outcome) => void) | undefined) {
    this.t = t;
    this.record = record;
    this.db = t.db;
    this.types = t.schema.types;
    this.module = t.schema.module;
    this.decoder = new Decoder(this.types);
  }

  /** run runs one step. */
  async run(st: Step): Promise<Ran> {
    const control = await this.control(st);
    if (control !== undefined) {
      return { problems: control };
    }
    switch (st.op) {
      case "operation":
        return { problems: await this.operate(st) };
      case "begin_write":
      case "begin_read":
        return { problems: await this.begin(st) };
      case "convert":
        return { problems: this.convert(st) };
      case "raw":
        return this.raw(st);
    }
    const dot = st.op.indexOf(".");
    if (dot < 0 && st.op !== "select") {
      return this.transact(st);
    }
    const prepared = await this.prepare(st);
    if (typeof prepared !== "function") {
      return { problems: prepared };
    }
    const mark = this.t.events?.mark() ?? 0;
    const out = dot < 0 ? await this.read(st) : await this.call(st, st.op.slice(0, dot), st.op.slice(dot + 1));
    this.fired = this.t.events?.since(mark) ?? [];
    const problems = [...("problems" in out ? out.problems : []), ...(await prepared())];
    if (problems.length > 0) {
      return { problems };
    }
    const o = out as Called;
    if (o.failed && !has(st.expect, "error")) {
      return { problems: [`unexpected error: ${message(o.error)}`], failed: { error: o.error } };
    }
    if (!o.failed && st.save !== undefined) {
      this.saved.set(st.save, o.result);
    }
    return { problems: this.compare(st, o.result, o.failed, o.error) };
  }

  /** control runs a control step, returning undefined for any other op. */
  async control(st: Step): Promise<string[] | undefined> {
    try {
      switch (st.op) {
        case "session":
          this.ctx = this.base(() => withSession(snapshot));
          return [];
        case "pause_replay":
        case "resume_replay": {
          const pause = st.op === "pause_replay";
          await this.t.schema.replay(this.t.replicaDsn ?? "", pause);
          this.paused = pause;
          return [];
        }
        case "sql": {
          const sql = st.sql?.[this.t.schema.dialect];
          if (sql === undefined) {
            return [`no sql for ${this.t.schema.dialect}`];
          }
          await this.t.schema.exec(this.t.dsn ?? "", sql);
          return [];
        }
        case "sleep":
          await sleep(duration(st.duration ?? ""));
          return [];
      }
    } catch (e) {
      return [message(e)];
    }
    return undefined;
  }

  /**
   * prepare sets up a step's call: the injected delay or failure, and the
   * deadline, cancellation and termination, which act on the context. The
   * function it returns undoes them once the call returns, and reports what
   * failed; or it returns the problems that kept it from setting them up.
   */
  async prepare(st: Step): Promise<(() => Promise<string[]>) | string[]> {
    const outer = this.ctx;
    const undo: (() => Promise<string[]> | string[])[] = [
      () => {
        this.ctx = outer;
        return [];
      },
    ];
    const finish = async () => {
      const problems: string[] = [];
      for (const u of undo.reverse()) {
        problems.push(...(await u()));
      }
      return problems;
    };
    if (st.inject !== undefined) {
      try {
        await this.t.schema.inject(this.t.dsn ?? "", st.inject);
      } catch (e) {
        return [`inject: ${message(e)}`];
      }
      undo.push(async () => {
        await this.t.schema.inject(this.t.dsn ?? "", {});
        return [];
      });
    }
    const timed: [string, string | undefined, (ms: number) => () => Promise<string[]> | string[]][] = [
      [
        "deadline",
        st.deadline,
        (ms) => {
          this.ctx = this.ctx(() => withTimeout(ms, snapshot));
          return () => [];
        },
      ],
      [
        "cancel",
        st.cancel,
        (ms) => {
          const c = new AbortController();
          this.ctx = this.ctx(() => withSignal(c.signal, snapshot));
          const timer = setTimeout(() => c.abort(), ms);
          return () => {
            clearTimeout(timer);
            return [];
          };
        },
      ],
      [
        "terminate",
        st.terminate,
        (ms) => {
          const done = sleep(ms).then(() => this.t.schema.terminate(this.t.dsn ?? ""));
          return async () => {
            try {
              await done;
              return [];
            } catch (e) {
              return [`terminate: ${message(e)}`];
            }
          };
        },
      ],
    ];
    for (const [name, after, start] of timed) {
      if (after === undefined) {
        continue;
      }
      let ms: number;
      try {
        ms = duration(after);
      } catch (e) {
        await finish();
        return [`${name}: ${message(e)}`];
      }
      undo.push(start(ms));
    }
    return finish;
  }

  /** operate runs an operation step: its steps in one operation, which rolls back and reports each pair-form transaction left open when it ends. Its expect takes events, all that the steps and the operation's end fired. */
  async operate(st: Step): Promise<string[]> {
    const mark = this.t.events?.mark() ?? 0;
    const outer = this.ctx;
    let problems: string[] = [];
    await this.ctx(() =>
      (this.t.exec as Executor).operation(async () => {
        this.ctx = snapshot();
        problems = await this.steps(st.steps ?? []);
      }),
    );
    this.ctx = outer;
    this.fired = this.t.events?.since(mark) ?? [];
    return [...problems, ...this.compare({ op: st.op, ...(st.expect === undefined ? {} : { expect: st.expect }) }, null, false, undefined)];
  }

  /** steps runs steps in order and returns their problems, each prefixed with its step. */
  async steps(steps: readonly Step[]): Promise<string[]> {
    const problems: string[] = [];
    const path = this.path;
    for (const [i, s] of steps.entries()) {
      this.path = [...path, i + 1];
      for (const p of (await this.run(s)).problems) {
        problems.push(`step ${i + 1} (${s.op}): ${p}`);
      }
    }
    this.path = path;
    return problems;
  }

  /**
   * begin runs a begin step: it begins a pair-form transaction, runs its
   * steps on the transaction's stores and context, and then commits it,
   * rolls it back or leaves it open. Its expect takes error, of the
   * beginning or the end, and events.
   */
  async begin(st: Step): Promise<string[]> {
    const end = st.end ?? "";
    if (!["commit", "rollback", ""].includes(end)) {
      return [`no end ${end}`];
    }
    const m = member(this.db, st.op === "begin_write" ? "beginWrite" : "beginRead");
    if (m === undefined) {
      return [`no transaction ${st.op}`];
    }
    const mark = this.t.events?.mark() ?? 0;
    let problems: string[] = [];
    let failed = false;
    let error: unknown;
    try {
      const tx = (await this.ctx(() => m.fn.call(this.db))) as { run: Ctx; commit(): Promise<void>; rollback(): Promise<void> };
      const [outerDB, outerCtx] = [this.db, this.ctx];
      this.db = tx;
      this.ctx = this.ctx(() => tx.run(snapshot));
      problems = await this.steps(st.steps ?? []);
      [this.db, this.ctx] = [outerDB, outerCtx];
      if (end !== "") {
        await this.ctx(() => (end === "commit" ? tx.commit() : tx.rollback()));
      }
    } catch (e) {
      [failed, error] = [true, e];
    }
    this.fired = this.t.events?.since(mark) ?? [];
    return [...problems, ...this.compare({ op: st.op, ...(st.expect === undefined ? {} : { expect: st.expect }) }, null, failed, error)];
  }

  /** raw runs a raw step through the handle's escape hatch (§17, §22.1). */
  async raw(st: Step): Promise<Ran> {
    const sql = st.sql?.[this.t.schema.dialect];
    if (sql === undefined) {
      return { problems: [`no sql for ${this.t.schema.dialect}`] };
    }
    const args: unknown[] = [];
    for (const a of st.args ?? []) {
      try {
        args.push(this.resolve(a));
      } catch (e) {
        return { problems: [`args: ${message(e)}`] };
      }
    }
    const mark = this.t.events?.mark() ?? 0;
    let result: Json = null;
    let failed = false;
    let error: unknown;
    try {
      result = encode(
        await this.ctx(() => {
          const h = (this.db as { raw(source: string): Record<string, (sql: string, ...args: unknown[]) => Promise<unknown>> }).raw(st.source ?? "");
          return st.exec ? (h["exec"] as (sql: string, ...args: unknown[]) => Promise<unknown>).call(h, sql, ...args) : (h["query"] as (sql: string, ...args: unknown[]) => Promise<unknown>).call(h, sql, ...args);
        }),
      );
    } catch (e) {
      [failed, error] = [true, e];
    }
    this.fired = this.t.events?.since(mark) ?? [];
    if (failed && !has(st.expect, "error")) {
      return { problems: [`unexpected error: ${message(error)}`], failed: { error } };
    }
    return { problems: this.compare(st, result, failed, error) };
  }

  /** convert runs a convert step: a shape's conversion, or a function, on its decoded arguments. */
  convert(st: Step): string[] {
    let fn: ((...args: unknown[]) => unknown) | undefined;
    let sig: Method | undefined;
    const args: unknown[] = [];
    if (st.from !== undefined) {
      const name = [...this.types.consts.keys()].find((n) => norm(n) === norm(st.from as string) && this.types.decls.has(n));
      if (name === undefined) {
        return [`no shape type ${st.from}`];
      }
      try {
        args.push(this.decode(st.input ?? null, { k: "name", name, args: [] }));
      } catch (e) {
        return [`input: ${message(e)}`];
      }
      sig = this.types.consts.get(name)?.methods.find((m) => norm(m.name) === norm(st.call ?? ""));
      if (sig !== undefined) {
        const obj = this.module[name] as Record<string, (...args: unknown[]) => unknown>;
        fn = (obj[sig.name] as (...args: unknown[]) => unknown).bind(obj);
      }
    } else {
      const name = [...this.types.funcs.keys()].find((n) => norm(n) === norm(st.call ?? ""));
      if (name !== undefined) {
        sig = this.types.funcs.get(name);
        fn = this.module[name] as (...args: unknown[]) => unknown;
      }
    }
    const expect = { ...st.expect };
    if ("generated" in expect && expect["generated"] === false) {
      return fn !== undefined ? [`${st.call} is generated`] : [];
    }
    delete expect["generated"];
    if (fn === undefined || sig === undefined) {
      return [`no conversion ${st.call}`];
    }
    const params = sig.params.slice(args.length);
    if ((st.args ?? []).length !== params.length) {
      return [`${st.call} takes ${params.length} arguments, got ${(st.args ?? []).length}`];
    }
    for (const [i, a] of (st.args ?? []).entries()) {
      try {
        args.push(this.decode(a, (params[i] as Param).type));
      } catch (e) {
        return [`argument ${i + 1}: ${message(e)}`];
      }
    }
    let result: Json = null;
    let failed = false;
    let error: unknown;
    try {
      const out = fn(...args);
      const shape = this.types.resolve(sig.result);
      // A function of several results returns them as an object's fields.
      result = shape.k === "object" ? shape.fields.map((f) => encode((out as Record<string, unknown>)[f.name])) : encode(out);
    } catch (e) {
      [failed, error] = [true, e];
    }
    if (!failed && st.save !== undefined) {
      this.saved.set(st.save, result);
    }
    this.fired = [];
    return this.compare({ op: st.op, expect }, result, failed, error);
  }

  /** call calls the step's method and returns its result, normalized, or its error, or the problems that kept it from being called. */
  async call(st: Step, storeName: string, method: string): Promise<Called | { problems: string[] }> {
    const h = this.handle(st);
    if ("problems" in h) {
      return h;
    }
    const store = member(h.handle, storeName);
    if (store === undefined) {
      return { problems: [`no store ${storeName}`] };
    }
    const m = member(store.value, method);
    const storeType = this.fieldType(h.handle, store.name);
    const sig = storeType === undefined ? undefined : this.types.method(storeType, method);
    if (m === undefined || sig === undefined) {
      return { problems: [`no method ${method} on ${storeName}`] };
    }
    let args: unknown[];
    try {
      args = this.args(st, storeName, sig);
    } catch (e) {
      return { problems: [message(e)] };
    }
    try {
      return { result: encode(await this.ctx(() => m.fn.apply(store.value, args))), failed: false };
    } catch (e) {
      return { result: null, failed: true, error: e };
    }
  }

  /** fieldType returns the declared type of a handle's field. */
  fieldType(h: object, field: string): string | undefined {
    const t = this.types.decls.has(h.constructor.name) ? this.types.fields(h.constructor.name).find((f) => f.name === field)?.type : undefined;
    return t?.k === "name" ? t.name : undefined;
  }

  /** handle is the handle a step runs on: through withScope, given its scope. */
  handle(st: Step): { handle: object } | { problems: string[] } {
    if (st.scope === undefined) {
      return { handle: this.db };
    }
    const sig = this.types.decls.has(this.db.constructor.name) ? this.types.decl(this.db.constructor.name).methods.find((m) => m.name === "withScope") : undefined;
    if (sig === undefined) {
      return { problems: ["the handle has no withScope"] };
    }
    let scope: unknown;
    try {
      scope = this.decode(st.scope, (sig.params[0] as Param).type);
    } catch (e) {
      return { problems: [`scope: ${message(e)}`] };
    }
    return { handle: (this.db as { withScope(s: unknown): object }).withScope(scope) };
  }

  /** table returns the column handles of a store's table. */
  table(storeName: string): { name: string; value: Record<string, unknown> } | undefined {
    const name = [...this.types.consts.keys()].find((n) => norm(n) === norm(storeName) && this.types.consts.get(n)?.methods.some((m) => m.name === "allRows"));
    return name === undefined ? undefined : { name, value: this.module[name] as Record<string, unknown> };
  }

  /** column returns a column handle's operator, as its function and declaration, or the problem. */
  column(storeName: string, column: string, op: string): { fn: (...args: unknown[]) => unknown; sig: Method } | string {
    const table = this.table(storeName);
    const col = table === undefined ? undefined : member(table.value, column);
    if (table === undefined || col === undefined) {
      return `no column ${column} of ${storeName}`;
    }
    const m = member(col.value, op);
    const sig = this.types.consts.get(table.name)?.members.get(col.name)?.find((s) => s.name === m?.name);
    if (m === undefined || sig === undefined) {
      return `column ${column} has no ${op}`;
    }
    return { fn: m.fn.bind(col.value), sig };
  }

  /** read runs a select step and returns its rows, or their count, or its error, or the problems that kept it from running. */
  async read(st: Step): Promise<Called | { problems: string[] }> {
    const shapes = this.module["shapes"] as Record<string, unknown>;
    const shape = member(shapes, st.shape ?? "");
    if (shape === undefined) {
      return { problems: [`no shape ${st.shape}`] };
    }
    const h = this.handle(st);
    if ("problems" in h) {
      return h;
    }
    const store = member(h.handle, st.from ?? "");
    if (store === undefined) {
      return { problems: [`no store ${st.from}`] };
    }
    const storeType = this.fieldType(h.handle, store.name) as string;
    const entity = (t: Type | undefined) => (t?.k === "name" ? (t.args[1] as Type & { name: string }).name : undefined);
    const shapeOf = this.types.fields("shapes").find((f) => f.name === shape.name)?.type;
    if (entity(shapeOf) !== entity(this.types.method(storeType, "select")?.params[0]?.type)) {
      return { problems: [`shape ${st.shape} is not read through ${st.from}`] };
    }
    let b = (store.value as { select(s: unknown): Builder }).select(shape.value);
    for (const f of st.filter ?? []) {
      const op = this.column(st.from as string, f.column, f.op);
      if (typeof op === "string") {
        return { problems: [op] };
      }
      try {
        const v = generate(f.value ?? null);
        const params = op.sig.params;
        const args = params.length === 0 || (params[0] as Param).rest ? [] : [this.decode(v, (params[0] as Param).type)];
        b = b.where(op.fn(...args));
      } catch (e) {
        return { problems: [`filter ${f.column}: ${message(e)}`] };
      }
    }
    for (const k of st.order ?? []) {
      const op = this.column(st.from as string, k.column, k.desc ? "desc" : "asc");
      if (typeof op === "string") {
        return { problems: [op.startsWith("no column") ? op : `no column ${k.column} of ${st.from}`] };
      }
      b = b.orderBy(op.fn());
    }
    switch (st.deleted ?? "") {
      case "with":
        b = b.withDeleted();
        break;
      case "only":
        b = b.onlyDeleted();
        break;
      case "":
        break;
      default:
        return { problems: [`no deleted ${st.deleted}`] };
    }
    if ((st.limit ?? 0) > 0) {
      b = b.limit(st.limit as number);
    }
    if (st.consistency !== undefined) {
      try {
        b = b.consistency(consistency(st));
      } catch (e) {
        return { problems: [message(e)] };
      }
    }
    const read = st.read ?? "all";
    const runs: Record<string, () => Promise<unknown>> = {
      all: () => b.all(),
      count: () => b.count(),
      one: () => b.one(),
      iter: async () => {
        const rows: unknown[] = [];
        for await (const row of b.iter()) {
          rows.push(row);
          if (rows.length === st.stop) {
            break;
          }
        }
        return rows;
      },
    };
    const go = runs[read === "" ? "all" : read];
    if (go === undefined) {
      return { problems: [`no read ${st.read}`] };
    }
    try {
      return { result: encode(await this.ctx(go)), failed: false };
    } catch (e) {
      return { result: null, failed: true, error: e };
    }
  }

  /** transact runs a transaction step: its steps in the handle's write or read closure, on the closure's context. Like run, it returns its problems and an error the step does not expect. */
  async transact(st: Step): Promise<Ran> {
    const { problems, failed, error } = await this.transaction(st);
    if (failed && !has(st.expect, "error")) {
      return { problems: [`unexpected error: ${message(error)}`], failed: { error } };
    }
    return { problems };
  }

  async transaction(st: Step): Promise<{ problems: string[]; failed: boolean; error?: unknown }> {
    const mark = this.t.events?.mark() ?? 0;
    const h = this.handle(st);
    if ("problems" in h) {
      return { problems: h.problems, failed: false };
    }
    const m = member(h.handle, st.op);
    if (m === undefined && st.op !== "retry") {
      return { problems: [`no transaction ${st.op}`], failed: false };
    }
    const opts: { propagation?: Propagation; isolation?: Isolation; retry?: RetryPolicy } = {};
    if (st.propagation !== undefined) {
      if (!propagations.includes(st.propagation)) {
        return { problems: [`no propagation ${st.propagation}`], failed: false };
      }
      opts.propagation = st.propagation as Propagation;
    }
    if (st.isolation !== undefined) {
      opts.isolation = st.isolation as Isolation;
    }
    if (st.retry !== undefined) {
      opts.retry = { attempts: st.retry.attempts ?? 0, initial: 0.001, max: 0.001 };
    }
    if (st.inject !== undefined) {
      try {
        await this.t.schema.inject(this.t.dsn ?? "", st.inject);
      } catch (e) {
        return { problems: [`inject: ${message(e)}`], failed: false };
      }
    }
    let runs = 0;
    let inner: string[] = [];
    const body = async () => {
      runs++;
      inner = [];
      const outer = this.ctx;
      this.ctx = snapshot();
      const path = this.path;
      try {
        for (const [i, s] of (st.steps ?? []).entries()) {
          this.path = [...path, i + 1];
          const ran = await this.run(s);
          if (ran.failed !== undefined) {
            throw ran.failed.error;
          }
          for (const p of ran.problems) {
            inner.push(`step ${i + 1} (${s.op}): ${p}`);
          }
        }
      } finally {
        this.ctx = outer;
        this.path = path;
      }
      if (st.rollback) {
        throw errRollback;
      }
    };
    let failed = false;
    let error: unknown;
    try {
      if (st.op === "retry") {
        // retry(policy).do reruns the steps after the classes on names (§22.4), outside any transaction.
        const p: { attempts?: number; initial: number; max: number; on?: never[] } = { initial: 0.001, max: 0.001 };
        if (st.retry !== undefined) {
          p.attempts = st.retry.attempts ?? 0;
          if (st.retry.on !== undefined) {
            p.on = st.retry.on as never[];
          }
        }
        await this.ctx(() => retry(p).do(body));
      } else {
        await this.ctx(() =>
          (m as Member).fn.call(
            h.handle,
            async (tx: object) => {
              if (st.scope === undefined) {
                return body();
              }
              // A scoped handle's transaction runs its steps, so they show the scope it inherits (§22.3).
              const db = this.db;
              this.db = tx;
              try {
                return await body();
              } finally {
                this.db = db;
              }
            },
            opts,
          ),
        );
      }
    } catch (e) {
      if (!chain(e).includes(errRollback)) {
        [failed, error] = [true, e];
      }
    }
    if (st.inject !== undefined) {
      await this.t.schema.inject(this.t.dsn ?? "", {});
    }
    const problems = inner;
    const expect = { ...st.expect };
    if ("attempts" in expect) {
      if (expect["attempts"] !== runs) {
        problems.push(`attempts: want ${show(expect["attempts"])}, got ${runs}`);
      }
      delete expect["attempts"];
    }
    this.fired = this.t.events?.since(mark) ?? [];
    return { problems: [...problems, ...this.compare({ op: st.op, expect }, null, failed, error)], failed, error };
  }

  /** args builds the method's arguments. */
  args(st: Step, storeName: string, sig: Method): unknown[] {
    const args: unknown[] = [];
    const payloads = [generate(st.input ?? null), ...(st.bind ?? []), st.set ?? null];
    for (const p of sig.params) {
      const t = p.type;
      if (p.rest) {
        args.push(...this.assignments(st.assign ?? [], storeName));
      } else if (t.k === "name" && t.name === "Pred") {
        if (st.where !== "all") {
          throw new Error(`where "${st.where ?? ""}": only all is supported`);
        }
        const table = this.table(storeName);
        if (table === undefined) {
          throw new Error(`no table ${storeName}`);
        }
        args.push((table.value["allRows"] as () => unknown)());
      } else if (t.k === "name" && t.name.endsWith("Key")) {
        args.push(at("key", () => this.decode(st.key ?? null, t)));
      } else {
        const v = payloads.shift() as Json;
        args.push(at("input", () => this.decode(v, t)));
      }
    }
    return args;
  }

  /** assignments builds the step's expressions on the store's column handles. */
  assignments(as: readonly Assign[], storeName: string): unknown[] {
    const out: unknown[] = [];
    for (const a of as) {
      let call: string;
      let arg: Json;
      if (a.set === "now" || a.set === "default") {
        [call, arg] = ["set", a.set];
      } else if (a.add !== undefined) {
        [call, arg] = ["add", a.add];
      } else if (a.sub !== undefined) {
        [call, arg] = ["sub", a.sub];
      } else {
        if (this.table(storeName) === undefined || member(this.table(storeName)?.value, a.column) === undefined) {
          throw new Error(`no column ${a.column} of ${storeName}`);
        }
        throw new Error(`assign ${a.column}: no expression`);
      }
      const op = this.column(storeName, a.column, call);
      if (typeof op === "string") {
        throw new Error(op);
      }
      const param = (op.sig.params[0] as Param).type;
      if (call === "set") {
        const expr = a.set === "now" ? now : DEFAULT;
        const takes = param.k === "name" && param.name === "Expr" && (param.args.length === 0 || (param.args[0] as Type & { value?: string }).value === a.set);
        if (!takes) {
          throw new Error(`column ${a.column} cannot be set to ${a.set}`);
        }
        out.push(op.fn(expr));
      } else {
        out.push(op.fn(at(`assign ${a.column}`, () => this.decode(arg, param))));
      }
    }
    return out;
  }

  /** decode resolves v's references and builds a value of type t from it. */
  decode(v: Json, t: Type): unknown {
    return this.decoder.decode(normalize(this.resolve(v)), t);
  }

  /** resolve replaces each "$name.field" string with that saved field, and each "$name" with the whole saved result. */
  resolve(v: Json): Json {
    if (typeof v === "string") {
      if (!v.startsWith("$")) {
        return v;
      }
      const ref = v.slice(1);
      const dot = ref.indexOf(".");
      const name = dot < 0 ? ref : ref.slice(0, dot);
      if (dot < 0 && this.saved.has(name)) {
        return this.saved.get(name) as Json;
      }
      const row = this.saved.get(name);
      if (!isObject(row)) {
        throw new Error(`no saved row ${name}`);
      }
      const field = ref.slice(dot + 1);
      const val = row[norm(field)];
      if (val === undefined) {
        throw new Error(`saved row ${name} has no ${field}`);
      }
      return val;
    }
    if (Array.isArray(v)) {
      return v.map((e) => this.resolve(e));
    }
    if (isObject(v)) {
      return Object.fromEntries(Object.entries(v).map(([k, e]) => [k, this.resolve(e)]));
    }
    return v;
  }

  /**
   * compare checks an outcome against the step's expectations: row (a
   * subset of the result's fields, where "present" means not null), rows,
   * none (no row), value, results (a convert step's several results, each
   * matched as a field is), count (how many rows), statements (how many
   * queries the step ran), events (the hook events it fired, in order), and
   * error (class, constraint, kind, field, misuse, a decode error's shape,
   * reason and assertion, and hides, a value the error's text must not
   * show). It records the step's outcome.
   */
  compare(st: Step, result: Json, failed: boolean, error: unknown): string[] {
    this.record?.({
      step: this.path.join("."),
      op: st.op,
      ...(failed ? { error: errorFields(error) } : { result }),
      events: this.fired,
    });
    const want = st.expect?.["error"];
    if (failed) {
      if (want === undefined) {
        return [`unexpected error: ${message(error)}`];
      }
      return this.compareError(normalize(want), error);
    }
    if (want !== undefined) {
      return [`want error ${show(want)}, got none`];
    }
    const problems: string[] = [];
    for (const k of Object.keys(st.expect ?? {}).sort()) {
      const w = normalize((st.expect as Record<string, Json>)[k]);
      switch (k) {
        case "row":
          problems.push(...this.compareRow("", w, result));
          break;
        case "rows":
        case "results": {
          const ws = Array.isArray(w) ? w : [];
          const gs = Array.isArray(result) ? result : [];
          if (ws.length !== gs.length) {
            problems.push(`want ${ws.length} ${k}, got ${gs.length}`);
            break;
          }
          for (const [i, x] of ws.entries()) {
            problems.push(...(k === "rows" ? this.compareRow(`row ${i} `, x, gs[i] as Json) : this.match(`result ${i + 1}`, x, gs[i] as Json)));
          }
          break;
        }
        case "none":
          if ((result === null) !== (w === true)) {
            problems.push(`want none ${show(w)}, got ${show(result)}`);
          }
          break;
        case "value":
          if (!equal(w, result)) {
            problems.push(`want value ${show(w)}, got ${show(result)}`);
          }
          break;
        case "count": {
          const n = Array.isArray(result) ? result.length : 0;
          if (w !== n) {
            problems.push(`want count ${show(w)}, got ${n}`);
          }
          break;
        }
        case "statements": {
          const n = this.fired.filter((ev) => isObject(ev) && "query" in ev).length;
          if (w !== n) {
            problems.push(`want ${show(w)} statements, got ${n}`);
          }
          break;
        }
        case "events":
          problems.push(...this.compareEvents(w));
          break;
        default:
          problems.push(`unknown expectation ${k}`);
      }
    }
    return problems;
  }

  compareRow(at: string, want: Json, got: Json): string[] {
    if (!isObject(want) || !isObject(got)) {
      return [`${at}want row ${show(want)}, got ${show(got)}`];
    }
    const problems: string[] = [];
    for (const k of Object.keys(want).sort()) {
      let wv: Json;
      try {
        wv = this.resolve(want[k] as Json);
      } catch (e) {
        problems.push(at + message(e));
        continue;
      }
      if (!(k in got)) {
        problems.push(`${at}no field ${k}`);
        continue;
      }
      problems.push(...this.match(at + k, wv, got[k] as Json));
    }
    return problems;
  }

  /** match compares one field: "present" means not null, an object a subset of the fields of a nested row, and a list of objects as many nested rows. */
  match(path: string, want: Json, got: Json): string[] {
    if (want === "present") {
      return got === null ? [`${path} is null`] : [];
    }
    if (isObject(want)) {
      return this.compareRow(`${path}.`, want, got);
    }
    if (Array.isArray(want) && want.length > 0 && isObject(want[0])) {
      const gs = Array.isArray(got) ? got : [];
      if (want.length !== gs.length) {
        return [`${path}: want ${want.length} rows, got ${gs.length}`];
      }
      return want.flatMap((w, i) => this.compareRow(`${path}.${i}.`, w, gs[i] as Json));
    }
    return equal(want, got) ? [] : [`${path}: want ${show(want)}, got ${show(got)}`];
  }

  /** compareEvents compares the events the step fired with want, in order, each on the fields want names. */
  compareEvents(want: Json): string[] {
    const ws = Array.isArray(want) ? want : [];
    if (ws.length !== this.fired.length) {
      return [`want ${ws.length} events, got ${this.fired.length}: ${show(this.fired)}`];
    }
    return ws.flatMap((w, i) => this.compareRow(`event ${i + 1} `, w, this.fired[i] as Json));
  }

  compareError(want: Json, err: unknown): string[] {
    const got = errorFields(err);
    if (!isObject(want)) {
      return [`error expectation ${show(want)} is not a map`];
    }
    const problems: string[] = [];
    for (const k of Object.keys(want).sort()) {
      if (k === "hides") {
        const h = String(want[k]);
        if (message(err).includes(h)) {
          problems.push(`error shows ${h}: ${message(err)}`);
        }
        continue;
      }
      if (!(k in got) || got[k] !== want[k]) {
        problems.push(`error ${k}: want ${show(want[k])}, got ${show(got[k] ?? null)} (${message(err)})`);
      }
    }
    return problems;
  }
}

/** Builder is the part of the runtime's Builder a select step uses. */
interface Builder {
  where(p: unknown): Builder;
  orderBy(o: unknown): Builder;
  withDeleted(): Builder;
  onlyDeleted(): Builder;
  limit(n: number): Builder;
  consistency(c: Consistency): Builder;
  all(): Promise<unknown>;
  count(): Promise<unknown>;
  one(): Promise<unknown>;
  iter(): AsyncIterable<unknown>;
}

/** Called is a call's result, or its error. */
interface Called {
  readonly result: Json;
  readonly failed: boolean;
  readonly error?: unknown;
}

/** has reports whether an expectation names key. */
function has(expect: Step["expect"], key: string): boolean {
  return expect !== undefined && key in expect;
}

/** message is an error's text. */
export function message(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

/** sleep waits ms milliseconds. */
function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

const units: Readonly<Record<string, number>> = { ns: 1e-6, us: 1e-3, "µs": 1e-3, ms: 1, s: 1000, m: 60_000, h: 3_600_000 };

/** duration parses a Go duration, as 500ms or 1m30s, into milliseconds, failing as Go's time.ParseDuration does. */
export function duration(s: string): number {
  const m = /^([0-9]*\.?[0-9]+(ns|us|µs|ms|s|m|h))+$/.test(s);
  if (!m) {
    throw new Error(`time: invalid duration "${s}"`);
  }
  let ms = 0;
  for (const [, n, unit] of s.matchAll(/([0-9]*\.?[0-9]+)(ns|us|µs|ms|s|m|h)/g)) {
    ms += Number(n) * (units[unit as string] as number);
  }
  return ms;
}

/** Member is a method or field of an object, found by its name under norm. */
interface Member {
  readonly name: string;
  readonly value: unknown;
  readonly fn: (...args: unknown[]) => unknown;
}

/** member returns the method or field of v whose name norm matches name, looking through its prototypes. */
export function member(v: unknown, name: string): Member | undefined {
  if (typeof v !== "object" || v === null) {
    return undefined;
  }
  for (let o: object | null = v; o !== null && o !== Object.prototype; o = Object.getPrototypeOf(o) as object | null) {
    for (const k of Object.getOwnPropertyNames(o)) {
      if (k !== "constructor" && norm(k) === norm(name)) {
        const value = (v as Record<string, unknown>)[k];
        return { name: k, value, fn: value as (...args: unknown[]) => unknown };
      }
    }
  }
  return undefined;
}

/** chain returns e and its causes. */
function chain(e: unknown): unknown[] {
  const out: unknown[] = [];
  for (let x = e; x !== undefined && x !== null && !out.includes(x); x = (x as { cause?: unknown }).cause) {
    out.push(x);
  }
  return out;
}

/** errorFields are the fields of an error that expectations match: its class, the misuse, the violated constraint, the first validation issue's kind and field, and a decode error's shape, field, reason and assertion. */
export function errorFields(err: unknown): Record<string, Json> {
  const got: Record<string, Json> = { class: classOf(err) ?? "" };
  for (const x of chain(err)) {
    if (x instanceof UsageError && x.reason in misuses) {
      got["misuse"] = misuses[x.reason] as string;
    }
    if (x instanceof Violation) {
      got["constraint"] = x.constraint.name.slice(x.constraint.name.lastIndexOf(".") + 1);
    }
    if (x instanceof ValidationError) {
      const first = x.issues[0];
      got["kind"] = first?.kind ?? "";
      got["field"] = first?.field ?? "";
    }
    if (x instanceof DecodeError) {
      Object.assign(got, { shape: x.shape, field: x.field, reason: x.reason, assertion: x.assertion });
    }
  }
  return got;
}

/** consistency builds a select step's consistency level. */
function consistency(st: Step): Consistency {
  const ms = (name: string, text: string | undefined) => (text === undefined ? 0 : at(name, () => duration(text)));
  const bound = ms("bound", st.bound);
  const wait = ms("wait", st.wait);
  switch (st.consistency) {
    case "primary":
      return Consistency.primary;
    case "any":
      return Consistency.any;
    case "bounded":
      return Consistency.bounded(bound);
    case "read_your_writes": {
      const c = Consistency.readYourWrites.wait(wait);
      return st.or_fail ? c.orFail() : c;
    }
  }
  throw new Error(`no consistency ${st.consistency}`);
}

/**
 * generate expands an input of the form { generate: { count: n, row: r } }
 * into n copies of r, with each "$i" in r's strings replaced by the copy's
 * number, from 1; value in place of row names a list's item. Any other input
 * is returned as it is.
 */
export function generate(input: Json): Json {
  const g = isObject(input) && Object.keys(input).length === 1 ? input["generate"] : undefined;
  if (!isObject(g)) {
    return input;
  }
  const n = g["count"];
  if (typeof n !== "number" || !Number.isInteger(n)) {
    throw new Error(`generate: count ${show(n ?? null)} is not a number`);
  }
  const item = "value" in g ? (g["value"] as Json) : (g["row"] as Json);
  return Array.from({ length: n }, (_, i) => numbered(item, String(i + 1)));
}

/** numbered returns v with "$i" replaced by i in its strings. */
function numbered(v: Json, i: string): Json {
  if (typeof v === "string") {
    return v.replaceAll("$i", i);
  }
  if (isObject(v)) {
    return Object.fromEntries(Object.entries(v).map(([k, e]) => [k, numbered(e, i)]));
  }
  return v;
}

/** equal compares two JSON values. */
export function equal(a: Json, b: Json): boolean {
  return JSON.stringify(sorted(a)) === JSON.stringify(sorted(b));
}

function sorted(v: Json): Json {
  if (Array.isArray(v)) {
    return v.map(sorted);
  }
  if (isObject(v)) {
    return Object.fromEntries(
      Object.keys(v)
        .sort()
        .map((k) => [k, sorted(v[k] as Json)]),
    );
  }
  return v;
}

/** at runs f, prefixing the message of an error it throws with where. */
function at<T>(where: string, f: () => T): T {
  try {
    return f();
  } catch (e) {
    throw new Error(`${where}: ${message(e)}`);
  }
}
