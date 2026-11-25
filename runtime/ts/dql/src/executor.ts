// The executor, its handles and transactions (design record §22).

import { ambient, within } from "./context.js";
import type { BeginOptions, Conn, Driver, Isolation, Pool, Rows, Sent, StatementMode } from "./driver.js";
import { ConnectionError, PoolExhausted, ReplicaUnavailable } from "./errors.js";
import { HookSet, type Hooks, Leaked, type TxEvent } from "./hooks.js";
import { DEFAULT_RETRY, type RetryPolicy, backoff, fits, retried, retryable, wait } from "./retry.js";
import { Consistency, type Replication, type Session } from "./routing.js";
import { type ErrorMap, Translator } from "./translate.js";

/**
 * Config is the runtime configuration that lives with the application
 * (§22.1): its sources, which source serves each catalog, the default read
 * consistency and the default retry policy.
 */
export interface Config {
  readonly sources: Readonly<Record<string, SourceConfig>>;
  /** The source of each catalog of the generated package. */
  readonly catalogs?: Readonly<Record<string, string>>;
  /** The level of a read that sets none; absent is primary. */
  readonly consistency?: Consistency;
  /** Absent is DEFAULT_RETRY. */
  readonly retry?: RetryPolicy;
}

/** SourceConfig is one source's primary and replicas (§22.1). */
export interface SourceConfig {
  readonly primary: ConnConfig;
  readonly replicas?: readonly ConnConfig[];
  /** How far behind, in milliseconds, a replica may fall before it leaves rotation; absent is 2000 (§22.5). */
  readonly maxLag?: number;
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
 * statements every new connection runs (§22.7), its error map (§23), how it
 * reads replica positions (§22.5), how many parameters one statement may
 * bind, or 0 for no limit (§25.2), and the statement modes its drivers
 * support, or absent to allow every mode (§26.4).
 */
export interface Dialect {
  readonly name: string;
  readonly setup: readonly string[];
  readonly errors: ErrorMap;
  readonly replication?: Replication;
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
  /** How often, in milliseconds, the tracker polls each replica's lag (§22.5). The default is 500. */
  readonly trackInterval?: number;
  /** Observe queries, transactions, pools and replicas (§24); each event reaches every one in order. */
  readonly hooks?: readonly Hooks[];
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
  /** How fresh a read transaction it begins must be, which decides whether it may run on a replica (§22.5). */
  readonly consistency?: Consistency;
}

/** UsageReason names a misuse of the executor. */
export type UsageReason =
  | "no_transaction"
  | "in_transaction"
  | "read_only"
  | "independent_depth"
  | "done"
  | "statement_mode"
  | "unknown_source"
  | "no_replication";

const usageMessages: Record<UsageReason, string> = {
  no_transaction: "dql: a mandatory transaction found no transaction to join",
  in_transaction: "dql: a transaction that must not nest began inside one",
  read_only: "dql: a write transaction cannot join a read transaction",
  independent_depth: "dql: independent transactions nest too deep",
  done: "dql: the transaction has already committed or rolled back",
  statement_mode: "dql: the dialect does not support the statement mode",
  unknown_source: "dql: no source is named",
  no_replication: "dql: the dialect cannot route reads to replicas",
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

/** Replica is one replica of a source, in rotation while the tracker last found it reachable and at most the source's max lag behind. */
export class Replica {
  in = false;
  /** How far behind, in milliseconds. */
  lag = 0;

  constructor(readonly pool: Pool) {}
}

/** SourcePool is one source's primary pool and its replicas. */
export class SourcePool {
  readonly replicas: Replica[] = [];
  /** Turns reads across the replicas. */
  next = 0;

  constructor(
    readonly name: string,
    readonly pool: Pool,
    /** Limits the connections outside independent transactions when some are reserved for them. */
    readonly general: Slots | undefined,
    readonly maxLag: number,
  ) {}

  async close(): Promise<void> {
    await this.pool.close();
    for (const r of this.replicas) {
      await r.pool.close();
    }
  }
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
  /** @internal */
  readonly consistency: Consistency;
  /** @internal */
  readonly hooks: HookSet;
  private readonly trackInterval: number;
  /** The tracker's next poll, and the poll running, if any. */
  private timer: ReturnType<typeof setTimeout> | undefined;
  private polling: Promise<void> | undefined;
  private closed = false;

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
    this.consistency = config.consistency ?? Consistency.primary;
    this.trackInterval = options.trackInterval ?? 500;
    this.hooks = new HookSet(options.hooks ?? []);
  }

  /**
   * open opens every source of config and connects to each primary, failing
   * when one does not connect (§22.1). A replica need not connect: the
   * tracker puts it in rotation once it answers, and polls every replica
   * until close.
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
    await e.poll();
    e.track();
    return e;
  }

  private async open(name: string, c: SourceConfig): Promise<SourcePool> {
    if ((c.replicas ?? []).length > 0 && this.dialect.replication === undefined) {
      throw new UsageError("no_replication", `dql: source ${name}`);
    }
    const mode = c.statementMode ?? "prepare";
    if (![this.dialect.statementModes, this.driver.statementModes].every((modes) => modes === undefined || modes.includes(mode))) {
      throw new UsageError("statement_mode", `dql: source ${name}`, mode);
    }
    const pool = await this.openPool(name, c.primary, mode);
    const reserve = c.independentReserve ?? 0;
    const s = new SourcePool(name, pool, reserve > 0 ? new Slots(Math.max((c.primary.maxConns ?? 0) - reserve, 1)) : undefined, c.maxLag ?? 2000);
    try {
      for (const rc of c.replicas ?? []) {
        s.replicas.push(new Replica(await this.openPool(name, rc, mode)));
      }
      (await pool.acquire()).release();
    } catch (err) {
      await s.close();
      throw err instanceof ConnectionError ? err : new ConnectionError(name, err, { dialect: this.dialect.name });
    }
    return s;
  }

  /** openPool opens a pool of one server of a source. A dsn written env:NAME is the environment variable NAME. */
  private async openPool(source: string, c: ConnConfig, mode: StatementMode): Promise<Pool> {
    let dsn = c.dsn;
    if (dsn.startsWith("env:")) {
      const v = dsn.slice(4);
      dsn = process.env[v] ?? "";
      if (dsn === "") {
        throw new Error(`dql: source ${source}: environment variable ${v} is not set`);
      }
    }
    try {
      return await this.driver.open({ dsn, maxConns: c.maxConns ?? 0, setup: this.dialect.setup, mode });
    } catch (err) {
      throw new ConnectionError(source, err, { dialect: this.dialect.name });
    }
  }

  /** close stops the replica tracker and closes every source's pools. */
  async close(): Promise<void> {
    this.closed = true;
    clearTimeout(this.timer);
    await this.polling;
    for (const s of this.sources) {
      await s.close();
    }
  }

  /** track polls the replicas every track interval until close. The timer does not keep the process alive. */
  private track(): void {
    if (this.closed || !this.sources.some((s) => s.replicas.length > 0)) {
      return;
    }
    this.timer = setTimeout(() => {
      this.polling = this.poll().then(() => this.track());
    }, this.trackInterval);
    this.timer.unref?.();
  }

  /** @internal poll updates every replica's lag and rotation. */
  async poll(): Promise<void> {
    for (const s of this.sources) {
      for (const [i, r] of s.replicas.entries()) {
        const lag = await this.lag(r);
        r.lag = lag ?? 0;
        this.rotate(s, i, lag !== undefined && lag <= s.maxLag);
      }
    }
  }

  /** rotate puts a replica in or out of rotation, reporting a change. */
  private rotate(s: SourcePool, i: number, into: boolean): void {
    const r = s.replicas[i] as Replica;
    if (r.in === into) {
      return;
    }
    r.in = into;
    this.hooks.rotation({ source: s.name, replica: i, lag: r.lag, in: into });
  }

  /** acquire takes a connection from a pool of s, reporting the wait. */
  private async acquire(s: SourcePool, p: Pool, replica: boolean, signal: AbortSignal | undefined): Promise<Conn> {
    const start = performance.now();
    try {
      return await p.acquire(signal);
    } finally {
      this.hooks.poolWait({ source: s.name, replica, wait: performance.now() - start });
    }
  }

  /** lag asks a replica how far behind it is, in milliseconds, or undefined when it cannot answer or is not a replica. */
  private async lag(r: Replica): Promise<number | undefined> {
    const signal = AbortSignal.timeout(this.trackInterval);
    let c: Conn;
    try {
      c = await r.pool.acquire(signal);
    } catch {
      return undefined;
    }
    try {
      const v = await first(c, (this.dialect.replication as Replication).lag, [], signal);
      return typeof v === "number" ? v * 1000 : undefined;
    } catch {
      return undefined;
    } finally {
      c.release();
    }
  }

  /**
   * @internal route returns the connection a read outside a write transaction
   * runs on (guide §5.6), and whether it is a replica's: a replica when its
   * consistency level allows one, else the primary.
   */
  async route(s: SourcePool, c: Consistency | undefined, independent: boolean, q: string): Promise<[Conn, boolean]> {
    const level = c ?? this.consistency;
    const onPrimary = async (): Promise<[Conn, boolean]> => [await this.conn(s, independent, q), false];
    if (level.level === "primary" || s.replicas.length === 0) {
      return onPrimary();
    }
    let positions: string[] = [];
    if (level.level === "read-your-writes") {
      const [pending, primaryOnly] = ambient().session?.pending(s.name) ?? [[], false];
      if (primaryOnly) {
        return onPrimary();
      }
      positions = pending;
    }
    const deadline = Date.now() + level.waitFor;
    let pause = 1;
    for (;;) {
      const conn = await this.replicaConn(s, level, positions);
      if (conn !== undefined) {
        return [conn, true];
      }
      const left = deadline - Date.now();
      if (positions.length === 0 || left <= 0) {
        break;
      }
      pause = Math.min(2 * pause, 50, left);
      if (!(await wait(pause))) {
        break;
      }
    }
    if (positions.length > 0 && level.fails) {
      throw new ReplicaUnavailable(s.name, { dialect: this.dialect.name, query: q });
    }
    return onPrimary();
  }

  /**
   * replicaConn returns a connection to a replica in rotation that meets c and
   * has replayed positions, taking replicas in turn, or undefined. A replica
   * that does not connect or answer leaves rotation.
   */
  private async replicaConn(s: SourcePool, c: Consistency, positions: string[]): Promise<Conn | undefined> {
    const start = ++s.next;
    const signal = ambient().signal;
    for (let k = 0; k < s.replicas.length; k++) {
      const i = (start + k) % s.replicas.length;
      const r = s.replicas[i] as Replica;
      if (!r.in || (c.level === "bounded" && r.lag > c.bound)) {
        continue;
      }
      let conn: Conn;
      try {
        conn = await this.acquire(s, r.pool, true, signal);
      } catch {
        this.rotate(s, i, false);
        continue;
      }
      if (positions.length === 0) {
        return conn;
      }
      try {
        if ((await first(conn, (this.dialect.replication as Replication).replayed, [positions], signal)) === true) {
          return conn;
        }
      } catch {
        this.rotate(s, i, false);
      }
      conn.release();
    }
    return undefined;
  }

  /**
   * @internal recordWrite records, in session, the position a replica must
   * replay to see what conn, a primary connection of s, has committed. A
   * position it cannot read sends the session's reads of s to the primary.
   */
  async recordWrite(session: Session | undefined, s: SourcePool, conn: Conn): Promise<void> {
    if (session === undefined || s.replicas.length === 0) {
      return;
    }
    let pos: unknown;
    try {
      pos = await first(conn, (this.dialect.replication as Replication).position, [], undefined);
    } catch {
      pos = "";
    }
    session.record(s.name, typeof pos === "string" ? pos : "");
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
        return await this.acquire(s, s.pool, false, signal === undefined ? timer : AbortSignal.any([signal, timer]));
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
      c = await this.acquire(s, s.pool, false, signal);
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
        if (!r.done) {
          this.hooks.tx("txLeaked", { ...r.event(), duration: performance.now() - r.started }, r.states);
          await r.end(false, new Leaked()).catch(() => {});
        }
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
        return [this.newRoot(readOnly, c, depth, 1), true];
      }
    }
    if (enclosing === undefined) {
      return [this.newRoot(readOnly, c, 0, 1), true];
    }
    if (!readOnly && enclosing.root.options.readOnly) {
      throw new UsageError("read_only");
    }
    return [enclosing, false];
  }

  /** newRoot begins a root transaction, firing the begin hooks and tracking it in the context's operation. */
  private newRoot(readOnly: boolean, c: TxOptions, independent: number, attempt: number): Frame {
    const options: BeginOptions = c.isolation === undefined ? { readOnly } : { readOnly, isolation: c.isolation };
    const r = new Root(this, options, independent, c.consistency, ambient().session, c.propagation ?? "join", attempt);
    r.states = this.hooks.txBegin(r.event());
    ambient().operation?.add(r);
    return new Frame(r, undefined, "", r.states);
  }

  /** inFrame runs fn in f, with the states of its begin hooks active. */
  private inFrame<T>(f: Frame, fn: (f: Frame) => T | Promise<T>): Promise<T> {
    return within({ frame: f }, () => this.hooks.activate(f.states, async () => fn(f)));
  }

  /** run runs a closure transaction. */
  private async run<T>(readOnly: boolean, fn: (f: Frame) => T | Promise<T>, c: TxOptions): Promise<T> {
    const enclosing = ambient().frame;
    if (c.propagation === "nested" && enclosing !== undefined) {
      if (!readOnly && enclosing.root.options.readOnly) {
        throw new UsageError("read_only");
      }
      const ev: TxEvent = { ...enclosing.root.event(), propagation: "nested", readOnly, savepoint: true };
      const f = new Frame(enclosing.root, enclosing, `dql_${++savepoints.count}`, this.hooks.txBegin(ev));
      const start = performance.now();
      const end = (error: unknown) => {
        const failed = error !== undefined;
        this.hooks.tx("txEnd", { ...ev, duration: performance.now() - start, outcome: failed ? "rollback" : "commit", ...(failed ? { error } : {}) }, f.states);
      };
      let result: T;
      try {
        result = await this.inFrame(f, fn);
      } catch (err) {
        await f.endSavepoint(false).catch(() => {});
        end(err);
        throw err;
      }
      try {
        await f.endSavepoint(true);
      } catch (err) {
        end(err);
        throw err;
      }
      end(undefined);
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
        const result = await this.inFrame(f, fn);
        await f.root.end(true);
        return result;
      } catch (err) {
        failure = err;
        await f.root.end(false, err).catch(() => {});
      }
      if (!retryable(failure) || attempt >= policy.attempts) {
        throw retried(failure, attempt);
      }
      const delay = backoff(policy, attempt, this.random);
      if (!fits(delay)) {
        throw retried(failure, attempt);
      }
      this.hooks.tx("txRetry", { ...f.root.event(), duration: performance.now() - f.root.started, error: failure, delay }, f.root.states);
      if (!(await wait(delay))) {
        throw retried(failure, attempt);
      }
      f = this.newRoot(readOnly, c, f.root.independent, attempt + 1);
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
  /** The sources whose connection is a replica's. */
  private readonly replicas = new Set<SourcePool>();
  done = false;
  /** When it began, for hooks. */
  readonly started = performance.now();
  /** The states its begin hooks returned. */
  states: readonly unknown[] = [];

  constructor(
    readonly executor: Executor,
    readonly options: BeginOptions,
    /** How deep in independent transactions. */
    readonly independent: number,
    /** How fresh a read transaction must be. */
    readonly consistency: Consistency | undefined,
    /** Records the positions of the root's commits. */
    readonly session: Session | undefined,
    /** For hooks: how it began. */
    readonly propagation: Propagation,
    /** For hooks: its closure's run, from 1. */
    readonly attempt: number,
  ) {}

  /** event returns the root's transaction event. */
  event(): TxEvent {
    const o = this.options;
    return { propagation: this.propagation, ...(o.isolation === undefined ? {} : { isolation: o.isolation }), readOnly: o.readOnly, savepoint: false, attempt: this.attempt };
  }

  /** onReplica reports whether the root's connection to s is a replica's. */
  onReplica(s: SourcePool): boolean {
    return this.replicas.has(s);
  }

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
    const [c, replica] = this.options.readOnly ? await e.route(s, this.consistency, this.independent > 0, q) : [await e.conn(s, this.independent > 0, q), false];
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
    if (replica) {
      this.replicas.add(s);
    }
    return c;
  }

  /**
   * end commits or rolls back every connection of the root and releases
   * them. Commit stops at the first failure and rolls back the rest. cause
   * is why it rolls back, for the end hooks.
   */
  async end(commit: boolean, cause?: unknown): Promise<void> {
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
          if (!this.options.readOnly) {
            await this.executor.recordWrite(this.session, s, c);
          }
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
    const error = failed ? first : cause;
    const ok = commit && !failed;
    this.executor.hooks.tx(
      "txEnd",
      { ...this.event(), duration: performance.now() - this.started, outcome: ok ? "commit" : "rollback", ...(error === undefined ? {} : { error }) },
      this.states,
    );
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
    /** The states the frame's begin hooks returned, which its closure runs in. */
    readonly states: readonly unknown[],
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
    const e = this.frame.root.executor;
    return within({ frame: this.frame }, () => e.hooks.activate(this.frame.states, fn));
  }
}

/** WriteTx is a write transaction: a handle that reads and writes (§22.2). */
export class WriteTx extends TxHandle implements Writer {
  readonly [writes] = true as const;
}

/** ReadTx is a read transaction: a handle that only reads (§22.2). */
export class ReadTx extends TxHandle {}

/** first returns the first value of the first row of a statement. */
async function first(c: Conn, sql: string, args: unknown[], signal: AbortSignal | undefined): Promise<unknown> {
  const rows = await c.query({ query: "", sql, args, ...(signal === undefined ? {} : { signal }) });
  try {
    return (await rows.next())?.values[0];
  } finally {
    await rows.close();
  }
}
