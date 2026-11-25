// The executor, its handles and transactions (design record §22).

import { ambient, within } from "./context.js";
import type { BeginOptions, Conn, Driver, Isolation, Pool, Rows, Sent, StatementMode } from "./driver.js";
import { type Origin, PoolExhausted, ConnectionError } from "./errors.js";
import { DEFAULT_RETRY, type RetryPolicy, backoff, fits, retried, retryable, wait } from "./retry.js";
import { type ErrorMap, Translator } from "./translate.js";

/**
 * Config is the runtime configuration that lives with the application
 * (§22.1): its sources, which source serves each catalog, and the default
 * retry policy.
 */
export interface Config {
  readonly sources: Readonly<Record<string, SourceConfig>>;
  /** The source of each catalog of the generated package. */
  readonly catalogs?: Readonly<Record<string, string>>;
  /** Absent is DEFAULT_RETRY. */
  readonly retry?: RetryPolicy;
}

/** SourceConfig is one source (§22.1). */
export interface SourceConfig {
  readonly primary: ConnConfig;
  /** Connections kept for independent transactions only (§22.6). */
  readonly independentReserve?: number;
  /** How the source's servers are sent statements; absent is prepare (§26.4). */
  readonly statementMode?: StatementMode;
}

/** ConnConfig is a server to connect to. A dsn written env:NAME is the environment variable NAME. */
export interface ConnConfig {
  readonly dsn: string;
  /** Absent or 0 is the driver's default. */
  readonly maxConns?: number;
}

/**
 * Dialect is the generated description of the target dialect: its name, the
 * statements every new connection runs (§22.7), its error map (§23), how many
 * parameters one statement may bind, or 0 for no limit (§25.2), and the
 * statement modes its drivers support, or absent to allow every mode
 * (§26.4).
 */
export interface Dialect {
  readonly name: string;
  readonly setup: readonly string[];
  readonly errors: ErrorMap;
  readonly maxParams?: number;
  readonly statementModes?: readonly StatementMode[];
}

/** ExecutorOptions tune an Executor. */
export interface ExecutorOptions {
  /** How long an independent transaction waits for a connection before PoolExhausted, in milliseconds (§22.6). The default is 250. */
  readonly independentTimeout?: number;
  /** How deep independent transactions may nest (§22.6). The default is 2. */
  readonly independentDepth?: number;
  /** The source of randomness of retry jitter, uniform in [0, 1). The default is Math.random. */
  readonly random?: () => number;
}

/** Propagation is how a new transaction relates to the one the context carries (§22.3). */
export type Propagation =
  /** Reuses the enclosing transaction, or begins one. The default. */
  | "join"
  /** Begins a separate transaction on its own connection (§22.6). */
  | "independent"
  /** Begins a savepoint in the enclosing transaction, or a new transaction. */
  | "nested"
  /** Joins the enclosing transaction, and fails without one. */
  | "mandatory"
  /** Begins a new transaction, and fails inside one. */
  | "never";

/** TxOptions configure one transaction. */
export interface TxOptions {
  readonly propagation?: Propagation;
  /** The isolation level of a transaction it begins. */
  readonly isolation?: Isolation;
  /** Overrides the executor's retry policy for one closure (§22.4). */
  readonly retry?: RetryPolicy;
}

/** UsageReason names a misuse of the executor. */
export type UsageReason =
  | "no_transaction"
  | "in_transaction"
  | "read_only"
  | "independent_depth"
  | "done"
  | "statement_mode"
  | "unknown_source";

const usageMessages: Record<UsageReason, string> = {
  no_transaction: "dql: a mandatory transaction found no transaction to join",
  in_transaction: "dql: a transaction that must not nest began inside one",
  read_only: "dql: a write transaction cannot join a read transaction",
  independent_depth: "dql: independent transactions nest too deep",
  done: "dql: the transaction has already committed or rolled back",
  statement_mode: "dql: the dialect does not support the statement mode",
  unknown_source: "dql: no source is named",
};

/** UsageError reports a misuse of the executor, such as a write in a read transaction. Its messages are the Go runtime's. */
export class UsageError extends Error {
  constructor(
    readonly reason: UsageReason,
    context = "",
    detail = "",
  ) {
    super(`${context === "" ? "" : `${context}: `}${usageMessages[reason]}${detail === "" ? "" : `: ${detail}`}`);
    this.name = "UsageError";
  }
}

/** bind is the key of a handle's binding. */
export const bind: unique symbol = Symbol("dql.bind");

/** writes marks a handle that writes. */
export const writes: unique symbol = Symbol("dql.writes");

/** Binding is the executor a handle runs on, and the transaction it is bound to, if any. */
export interface Binding {
  readonly executor: Executor;
  readonly frame: Frame | undefined;
}

/** Reader is a handle that reads: the executor, a write transaction or a read transaction (§22.2). Generated reads take a Reader. */
export interface Reader {
  [bind](): Binding;
}

/**
 * Writer is a handle that writes: the executor or a write transaction.
 * Generated writes take a Writer, so passing a read transaction to a write
 * is a compile error (§22.2).
 */
export interface Writer extends Reader {
  readonly [writes]: true;
}

/** Slots limits how many connections are out at once, in arrival order. */
export class Slots {
  private readonly waiting: (() => void)[] = [];

  constructor(private free: number) {}

  /** take waits for a slot until signal aborts. */
  take(signal: AbortSignal | undefined): Promise<void> {
    if (this.free > 0) {
      this.free--;
      return Promise.resolve();
    }
    return new Promise((resolve, reject) => {
      if (signal?.aborted) {
        reject(signal.reason);
        return;
      }
      const granted = () => {
        signal?.removeEventListener("abort", aborted);
        resolve();
      };
      const aborted = () => {
        this.waiting.splice(this.waiting.indexOf(granted), 1);
        reject(signal?.reason);
      };
      this.waiting.push(granted);
      signal?.addEventListener("abort", aborted, { once: true });
    });
  }

  /** give frees a slot, to the first waiter if any. */
  give(): void {
    const next = this.waiting.shift();
    if (next === undefined) {
      this.free++;
    } else {
      next();
    }
  }
}

/** SourcePool is one source's pool. */
export class SourcePool {
  constructor(
    readonly name: string,
    readonly pool: Pool,
    /** Limits the connections outside independent transactions when some are reserved for them. */
    readonly general: Slots | undefined,
  ) {}
}

/** GeneralConn frees its slot when released. */
class GeneralConn implements Conn {
  constructor(
    private readonly conn: Conn,
    private readonly slots: Slots,
  ) {}

  query(s: Sent): Promise<Rows> {
    return this.conn.query(s);
  }
  exec(s: Sent): Promise<number> {
    return this.conn.exec(s);
  }
  begin(o: BeginOptions, signal?: AbortSignal): Promise<void> {
    return this.conn.begin(o, signal);
  }
  commit(): Promise<void> {
    return this.conn.commit();
  }
  rollback(): Promise<void> {
    return this.conn.rollback();
  }
  savepoint(name: string, signal?: AbortSignal): Promise<void> {
    return this.conn.savepoint(name, signal);
  }
  rollbackTo(name: string): Promise<void> {
    return this.conn.rollbackTo(name);
  }
  releaseSavepoint(name: string): Promise<void> {
    return this.conn.releaseSavepoint(name);
  }
  release(): void {
    this.conn.release();
    this.slots.give();
  }
}

/**
 * Executor runs generated code's statements on the sources of the catalogs
 * it was generated for (§22). It is a handle that reads and writes, and
 * starts transactions.
 */
export class Executor implements Writer {
  readonly [writes] = true as const;
  /** @internal */
  readonly translator: Translator;
  /** @internal */
  readonly sources: SourcePool[] = [];
  private readonly catalogs = new Map<string, SourcePool>();
  /** @internal */
  readonly retry: RetryPolicy;
  private readonly independentTimeout: number;
  private readonly independentDepth: number;
  /** @internal */
  readonly random: () => number;

  private constructor(
    private readonly driver: Driver,
    /** @internal */
    readonly dialect: Dialect,
    config: Config,
    options: ExecutorOptions,
  ) {
    this.translator = new Translator(driver, dialect.name, dialect.errors);
    this.retry = config.retry ?? DEFAULT_RETRY;
    this.independentTimeout = options.independentTimeout ?? 250;
    this.independentDepth = options.independentDepth ?? 2;
    this.random = options.random ?? Math.random;
  }

  /**
   * open opens every source of config and connects to each primary, failing
   * when one does not connect (§22.1).
   */
  static async open(driver: Driver, dialect: Dialect, config: Config, options: ExecutorOptions = {}): Promise<Executor> {
    const e = new Executor(driver, dialect, config, options);
    try {
      const byName = new Map<string, SourcePool>();
      for (const name of Object.keys(config.sources).sort()) {
        const s = await e.open(name, config.sources[name] as SourceConfig);
        e.sources.push(s);
        byName.set(name, s);
      }
      for (const [catalog, name] of Object.entries(config.catalogs ?? {})) {
        const s = byName.get(name);
        if (s === undefined) {
          throw new Error(`dql: catalog ${catalog} names source ${name}, which is not configured`);
        }
        e.catalogs.set(catalog, s);
      }
    } catch (err) {
      await e.close();
      throw err;
    }
    return e;
  }

  private async open(name: string, c: SourceConfig): Promise<SourcePool> {
    const mode = c.statementMode ?? "prepare";
    if (this.dialect.statementModes !== undefined && !this.dialect.statementModes.includes(mode)) {
      throw new UsageError("statement_mode", `dql: source ${name}`, mode);
    }
    let dsn = c.primary.dsn;
    if (dsn.startsWith("env:")) {
      const v = dsn.slice(4);
      dsn = process.env[v] ?? "";
      if (dsn === "") {
        throw new Error(`dql: source ${name}: environment variable ${v} is not set`);
      }
    }
    const origin: Origin = { dialect: this.dialect.name };
    let pool: Pool;
    try {
      pool = await this.driver.open({ dsn, maxConns: c.primary.maxConns ?? 0, setup: this.dialect.setup, mode });
    } catch (err) {
      throw new ConnectionError(name, err, origin);
    }
    const reserve = c.independentReserve ?? 0;
    const s = new SourcePool(name, pool, reserve > 0 ? new Slots(Math.max((c.primary.maxConns ?? 0) - reserve, 1)) : undefined);
    try {
      (await pool.acquire()).release();
    } catch (err) {
      await pool.close();
      throw new ConnectionError(name, err, origin);
    }
    return s;
  }

  /** close closes every source's pools. */
  async close(): Promise<void> {
    for (const s of this.sources) {
      await s.pool.close();
    }
  }

  [bind](): Binding {
    return { executor: this, frame: undefined };
  }

  /** @internal sourceOf returns the source a statement runs on: the one a raw statement names, else its catalog's. */
  sourceOf(catalog: string, raw: string | undefined): SourcePool {
    if (raw !== undefined) {
      const s = this.sources.find((x) => x.name === raw);
      if (s === undefined) {
        throw new UsageError("unknown_source", "", raw);
      }
      return s;
    }
    const s = this.catalogs.get(catalog);
    if (s === undefined) {
      throw new Error(`dql: no source serves catalog ${catalog}`);
    }
    return s;
  }

  /** @internal translate maps e, from a statement of query q on catalog of source s, into the taxonomy. */
  translate(e: unknown, catalog: string, q: string, s: SourcePool): unknown {
    return this.translator.translate(e, catalog, q, s.name, ambient().signal);
  }

  /**
   * @internal conn acquires a connection of s outside any transaction, or for
   * one; an independent transaction waits at most the independent timeout and
   * then fails with PoolExhausted (§22.6).
   */
  async conn(s: SourcePool, independent: boolean, q: string): Promise<Conn> {
    const signal = ambient().signal;
    if (independent) {
      const timer = AbortSignal.timeout(this.independentTimeout);
      try {
        return await s.pool.acquire(signal === undefined ? timer : AbortSignal.any([signal, timer]));
      } catch (err) {
        if (timer.aborted && !signal?.aborted) {
          throw new PoolExhausted(s.name, { dialect: this.dialect.name, query: q });
        }
        throw this.translate(err, "", q, s);
      }
    }
    const general = s.general;
    try {
      await general?.take(signal);
    } catch (err) {
      throw this.translate(err, "", q, s);
    }
    let c: Conn;
    try {
      c = await s.pool.acquire(signal);
    } catch (err) {
      general?.give();
      throw this.translate(err, "", q, s);
    }
    return general === undefined ? c : new GeneralConn(c, general);
  }

  /**
   * write runs fn in a write transaction and returns what it returns (§22.3).
   * A transaction the context carries is joined by default; one write begins
   * commits when fn returns and rolls back when fn throws, and reruns fn
   * after a serialization failure or deadlock, so fn must be safe to rerun.
   */
  write<T>(fn: (tx: WriteTx) => T | Promise<T>, options: TxOptions = {}): Promise<T> {
    return this.run(false, (f) => fn(new WriteTx(f, false)), options);
  }

  /** read runs fn in a read transaction, as write does. A read transaction joins an enclosing write transaction and sees its writes. */
  read<T>(fn: (tx: ReadTx) => T | Promise<T>, options: TxOptions = {}): Promise<T> {
    return this.run(true, (f) => fn(new ReadTx(f, false)), options);
  }

  /**
   * beginWrite begins a write transaction of the pair form, which the caller
   * commits or rolls back and which never retries (§22.3). Its handle is
   * bound to it; code it should reach through the context runs in tx.run.
   */
  async beginWrite(options: TxOptions = {}): Promise<WriteTx> {
    const [f, owns] = this.begin(false, options);
    return new WriteTx(f, owns);
  }

  /** beginRead begins a read transaction of the pair form. */
  async beginRead(options: TxOptions = {}): Promise<ReadTx> {
    const [f, owns] = this.begin(true, options);
    return new ReadTx(f, owns);
  }

  /**
   * operation runs fn as one operation, such as a request: each pair-form
   * transaction begun in it and still open when it ends is rolled back
   * (§22.3).
   */
  async operation<T>(fn: () => T | Promise<T>): Promise<T> {
    const roots = new Set<Root>();
    try {
      return await within({ operation: roots }, fn);
    } finally {
      for (const r of roots) {
        await r.end(false).catch(() => {});
      }
    }
  }

  /**
   * begin starts the frame a transaction runs in, and reports whether the
   * frame owns a new root. A pair-form nested savepoint is not supported: it
   * joins the enclosing transaction.
   */
  private begin(readOnly: boolean, c: TxOptions): [Frame, boolean] {
    const enclosing = ambient().frame;
    switch (c.propagation) {
      case "mandatory":
        if (enclosing === undefined) {
          throw new UsageError("no_transaction");
        }
        break;
      case "never":
        if (enclosing !== undefined) {
          throw new UsageError("in_transaction");
        }
        break;
      case "independent": {
        const depth = (enclosing?.root.independent ?? 0) + 1;
        if (depth > this.independentDepth) {
          throw new UsageError("independent_depth");
        }
        return [this.newRoot(readOnly, c, depth), true];
      }
    }
    if (enclosing === undefined) {
      return [this.newRoot(readOnly, c, 0), true];
    }
    if (!readOnly && enclosing.root.options.readOnly) {
      throw new UsageError("read_only");
    }
    return [enclosing, false];
  }

  /** newRoot begins a root transaction, tracking it in the context's operation. */
  private newRoot(readOnly: boolean, c: TxOptions, independent: number): Frame {
    const options: BeginOptions = c.isolation === undefined ? { readOnly } : { readOnly, isolation: c.isolation };
    const r = new Root(this, options, independent);
    ambient().operation?.add(r);
    return new Frame(r, undefined, "");
  }

  /** run runs a closure transaction. */
  private async run<T>(readOnly: boolean, fn: (f: Frame) => T | Promise<T>, c: TxOptions): Promise<T> {
    const enclosing = ambient().frame;
    if (c.propagation === "nested" && enclosing !== undefined) {
      if (!readOnly && enclosing.root.options.readOnly) {
        throw new UsageError("read_only");
      }
      const f = new Frame(enclosing.root, enclosing, `dql_${++savepoints.count}`);
      let result: T;
      try {
        result = await within({ frame: f }, () => fn(f));
      } catch (err) {
        await f.endSavepoint(false).catch(() => {});
        throw err;
      }
      await f.endSavepoint(true);
      return result;
    }
    let [f, owns] = this.begin(readOnly, c);
    if (!owns) {
      return within({ frame: f }, () => fn(f));
    }
    const policy = c.retry ?? this.retry;
    for (let attempt = 1; ; attempt++) {
      let failure: unknown;
      try {
        const result = await within({ frame: f }, () => fn(f));
        await f.root.end(true);
        return result;
      } catch (err) {
        failure = err;
        await f.root.end(false);
      }
      if (!retryable(failure) || attempt >= policy.attempts || !(await wait(backoff(policy, attempt, this.random)))) {
        throw retried(failure, attempt);
      }
      f = this.newRoot(readOnly, c, f.root.independent);
    }
  }
}

/** @internal savepoints numbers savepoint names; a name need only be unique within its root, and the counter makes it unique everywhere. */
export const savepoints = { count: 0 };

/**
 * Root is a transaction that owns its connections: one per source
 * it has used, each begun on first use, so a transaction spans the sources
 * its statements reach.
 */
export class Root {
  /** The connections being opened, by source. */
  private readonly opening = new Map<SourcePool, Promise<Conn>>();
  /** The connections that began, in the order they did. */
  private readonly began: [SourcePool, Conn][] = [];
  done = false;

  constructor(
    readonly executor: Executor,
    readonly options: BeginOptions,
    /** How deep in independent transactions. */
    readonly independent: number,
  ) {}

  /** conn returns the root's connection to s, beginning the transaction there on first use. */
  conn(s: SourcePool, q: string): Promise<Conn> {
    let c = this.opening.get(s);
    if (c === undefined) {
      c = this.open(s, q);
      this.opening.set(s, c);
      c.catch(() => this.opening.delete(s));
    }
    return c;
  }

  private async open(s: SourcePool, q: string): Promise<Conn> {
    const e = this.executor;
    const c = await e.conn(s, this.independent > 0, q);
    try {
      await c.begin(this.options, ambient().signal);
    } catch (err) {
      c.release();
      throw e.translate(err, "", q, s);
    }
    if (this.done) {
      // The root ended while the connection began.
      await c.rollback().catch(() => {});
      c.release();
      throw new UsageError("done");
    }
    this.began.push([s, c]);
    return c;
  }

  /**
   * end commits or rolls back every connection of the root and releases
   * them. Commit stops at the first failure and rolls back the rest.
   */
  async end(commit: boolean): Promise<void> {
    if (this.done) {
      return;
    }
    this.done = true;
    await Promise.allSettled(this.opening.values());
    let first: unknown;
    let failed = false;
    for (const [s, c] of this.began) {
      try {
        if (commit && !failed) {
          await c.commit();
        } else {
          await c.rollback();
        }
      } catch (err) {
        if (!failed) {
          failed = true;
          first = this.executor.translate(err, "", "", s);
        }
      }
      c.release();
    }
    if (failed) {
      throw first;
    }
  }
}

/** Frame is one level of a transaction: its root, or a savepoint in it. */
export class Frame {
  /** The savepoints made, by source. */
  private readonly saved = new Map<SourcePool, Promise<void>>();

  constructor(
    readonly root: Root,
    readonly parent: Frame | undefined,
    readonly savepoint: string,
  ) {}

  /** conn returns the frame's connection to s, beginning the root's transaction on s and making the frame's savepoints there on first use. */
  async conn(s: SourcePool, q: string): Promise<Conn> {
    if (this.root.done) {
      throw new UsageError("done");
    }
    const c = await this.root.conn(s, q);
    await this.save(s, c, q);
    return c;
  }

  /** save makes the frame's savepoint on s, and its parents' first. */
  private save(s: SourcePool, c: Conn, q: string): Promise<void> {
    if (this.savepoint === "") {
      return Promise.resolve();
    }
    let p = this.saved.get(s);
    if (p === undefined) {
      p = (async () => {
        await this.parent?.save(s, c, q);
        try {
          await c.savepoint(this.savepoint, ambient().signal);
        } catch (err) {
          this.saved.delete(s);
          throw this.root.executor.translate(err, "", q, s);
        }
      })();
      this.saved.set(s, p);
    }
    return p;
  }

  /** endSavepoint releases the savepoint, or rolls back to it, on every source it was made on. */
  async endSavepoint(commit: boolean): Promise<void> {
    for (const [s, made] of this.saved) {
      await made;
      const c = await this.root.conn(s, "");
      try {
        if (!commit) {
          await c.rollbackTo(this.savepoint);
        }
        await c.releaseSavepoint(this.savepoint);
      } catch (err) {
        throw this.root.executor.translate(err, "", "", s);
      }
    }
  }
}

/** TxHandle is what both transaction handles do. */
abstract class TxHandle implements Reader {
  /** @internal */
  constructor(
    /** @internal */
    readonly frame: Frame,
    /** Set on a handle of the pair form, which ends its own root. */
    private readonly owns: boolean,
  ) {}

  [bind](): Binding {
    return { executor: this.frame.root.executor, frame: this.frame };
  }

  /** commit commits a transaction of the pair form. A joined transaction's commit does nothing: its root commits. */
  async commit(): Promise<void> {
    if (!this.owns) {
      return;
    }
    if (this.frame.root.done) {
      throw new UsageError("done");
    }
    await this.frame.root.end(true);
  }

  /** rollback rolls back a transaction of the pair form. After commit it does nothing, so it can sit in a finally block. */
  async rollback(): Promise<void> {
    if (this.owns) {
      await this.frame.root.end(false);
    }
  }

  /** run runs fn with the transaction in its context, so generated code called with the executor inside fn joins it. */
  run<T>(fn: () => T): T {
    return within({ frame: this.frame }, fn);
  }
}

/** WriteTx is a write transaction: a handle that reads and writes (§22.2). */
export class WriteTx extends TxHandle implements Writer {
  readonly [writes] = true as const;
}

/** ReadTx is a read transaction: a handle that only reads (§22.2). */
export class ReadTx extends TxHandle {}
