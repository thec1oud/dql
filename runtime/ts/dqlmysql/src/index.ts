// @dql/mysql is the dql executor's driver for MySQL and MariaDB, on mysql2
// (design record §22). It turns the values mysql2 reads into the
// driver-neutral form of Row, and dql's host values into values MySQL reads,
// so generated code holds no mysql2 types.
//
//   const exec = await Executor.open(new MySqlDriver(), dialect, config);
//
// A DSN is a URL, mysql://user:password@host:3306/database. Every connection
// reports the rows an UPDATE matched rather than those it changed, which is
// what the generated code counts, and the ID of the last row an insert
// numbered, by which the runtime re-reads a written row (§20.1). Times are
// read and written in UTC, the session time zone the dialect sets.

import { type BeginOptions, type InsertIdConn, type InsertResult, type Driver, type NativeError, type Pool, type PoolConfig, Row, type Rows, type Sent, type StatementMode } from "@dql/runtime";
import mysql from "mysql2";
import { type Value, encodeArgs } from "./args.js";
import { native } from "./errors.js";
import { type Column, decodeRow, decoders } from "./values.js";

export { MonthsError, bitsValue, encode, timeText } from "./args.js";
export { native } from "./errors.js";
export { time } from "./values.js";

/** The modes the driver sends statements in (§26.4): prepare caches a server-side prepared statement per text on each connection; describe prepares each statement and closes it after, for proxies that share connections; simple writes the values into the text. */
const MODES: readonly StatementMode[] = ["prepare", "describe", "simple"];

/** The error codes of node's sockets, and mysql2's own, that mean a connection failed or broke. */
const brokenCodes = new Set([
  "ECONNREFUSED",
  "ECONNRESET",
  "EPIPE",
  "ETIMEDOUT",
  "ENOTFOUND",
  "EHOSTUNREACH",
  "ECONNABORTED",
  "PROTOCOL_CONNECTION_LOST",
  "PROTOCOL_SEQUENCE_TIMEOUT",
  "POOL_CLOSED",
]);

/** ignore drops an error that needs no answer: a connection's that breaks while idle, or one a statement's failure already reports. */
function ignore(): void {}

/** ServerError is the part of mysql2's error that the server sent. */
interface ServerError {
  readonly errno: number;
  readonly sqlState: string;
  readonly sqlMessage?: string;
  readonly message: string;
}

/** isServerError reports whether e is an error the server sent. */
function isServerError(e: unknown): e is ServerError {
  return e instanceof Error && typeof (e as { sqlState?: unknown }).sqlState === "string" && typeof (e as { errno?: unknown }).errno === "number";
}

/** spatial reads geometry and vector values as their bytes, which mysql2 would otherwise parse. */
const typeCast: mysql.TypeCast = (field, next) => (field.type === "GEOMETRY" || field.type === "VECTOR" ? field.buffer() : next());

/** MySqlDriver is a Driver on mysql2. */
export class MySqlDriver implements Driver {
  readonly statementModes = MODES;

  /** open returns a pool for c.dsn. Each new connection runs c.setup before its first statement. */
  async open(c: PoolConfig): Promise<Pool> {
    const options: mysql.PoolOptions = {
      uri: c.dsn,
      ...(c.maxConns > 0 ? { connectionLimit: c.maxConns } : {}),
      dateStrings: true,
      supportBigNumbers: true,
      bigNumberStrings: true,
      jsonStrings: true,
      decimalNumbers: false,
      timezone: "Z",
      typeCast,
    };
    return new MySqlPool(mysql.createPool(options), { uri: c.dsn }, c);
  }

  /** native returns the fields of an error the server sent. */
  native(e: unknown): NativeError | undefined {
    return isServerError(e) ? native(e.errno, e.sqlMessage ?? e.message) : undefined;
  }

  /** broken reports whether e is a failure to connect, a socket error, or a use of a closed connection. */
  broken(e: unknown): boolean {
    if (!(e instanceof Error) || isServerError(e)) {
      return false;
    }
    const x = e as { code?: unknown; fatal?: unknown };
    return x.fatal === true || (typeof x.code === "string" && brokenCodes.has(x.code)) || /closed state|Pool is closed/.test(e.message);
  }
}

/** MySqlPool is a pool of one server. */
class MySqlPool implements Pool {
  /** The connections that ran the setup statements. */
  private readonly ready = new WeakSet<mysql.PoolConnection>();

  constructor(
    private readonly pool: mysql.Pool,
    /** How to connect, for a cancel request. */
    readonly config: mysql.ConnectionOptions,
    readonly c: PoolConfig,
  ) {}

  /** acquire waits for a connection until signal aborts, and runs the setup statements on a new one. */
  async acquire(signal?: AbortSignal): Promise<MySqlConn> {
    signal?.throwIfAborted();
    const pending = new Promise<mysql.PoolConnection>((resolve, reject) => this.pool.getConnection((e, c) => (e === null ? resolve(c) : reject(e))));
    const conn = await (signal === undefined
      ? pending
      : new Promise<mysql.PoolConnection>((resolve, reject) => {
          const aborted = () => {
            reject(signal.reason);
            pending.then((c) => c.release(), ignore);
          };
          signal.addEventListener("abort", aborted, { once: true });
          pending.then(
            (c) => {
              signal.removeEventListener("abort", aborted);
              resolve(c);
            },
            (e: unknown) => {
              signal.removeEventListener("abort", aborted);
              reject(e);
            },
          );
        }));
    if (!this.ready.has(conn)) {
      // A connection that breaks while checked out fails its statements; its error event must not crash the process.
      conn.on("error", ignore);
      try {
        for (const s of this.c.setup) {
          await call<unknown>((cb) => conn.query(s, cb));
        }
      } catch (e) {
        conn.destroy();
        throw e;
      }
      this.ready.add(conn);
    }
    return new MySqlConn(this, conn);
  }

  close(): Promise<void> {
    return call((cb) => this.pool.end(cb));
  }
}

/** call runs a function that takes a node callback, and returns what it calls back with. */
function call<T>(fn: (cb: (e: unknown, r: T) => void) => void): Promise<T> {
  return new Promise((resolve, reject) => fn((e, r) => (e === null || e === undefined ? resolve(r) : reject(e))));
}

/** Result is what a statement returns: rows and their columns, or how many rows it wrote. */
type Result = { rows: unknown[][]; fields: Column[] } | mysql.ResultSetHeader;

/** MySqlRows holds a query's rows, read all at once. */
class MySqlRows implements Rows {
  private i = 0;

  constructor(private readonly rows: Row[]) {}

  async next(): Promise<Row | undefined> {
    return this.rows[this.i++];
  }

  async close(): Promise<void> {}
}

/** BATCH is how many rows a stream holds before it pauses the connection. */
const BATCH = 256;

/**
 * MySqlStream reads a statement's rows as they arrive, pausing the
 * connection while it holds a batch the caller has not read, so a stream
 * holds about one batch in memory (§25.1). The connection is busy until the
 * rows end, so closing early reads and drops the rest.
 */
class MySqlStream implements Rows {
  private readonly queue: Row[] = [];
  private decode: ((v: unknown) => unknown)[] = [];
  private ended = false;
  private failure: { e: unknown } | undefined;
  private paused = false;
  /** Set once the caller closed the rows, which drop those that arrive. */
  private closing = false;
  /** Called when a row arrives, the rows end or they fail. */
  private wake: (() => void) | undefined;
  private readonly cancel: () => void;

  constructor(
    private readonly conn: MySqlConn,
    cmd: mysql.Query,
    private readonly signal: AbortSignal | undefined,
    private readonly sql: string,
  ) {
    this.cancel = () => void conn.cancel();
    signal?.addEventListener("abort", this.cancel, { once: true });
    cmd.on("fields", (fields: Column[]) => {
      this.decode = decoders(fields);
    });
    cmd.on("result", (row: unknown) => {
      if (this.closing) {
        return;
      }
      this.queue.push(new Row(decodeRow(this.decode, row as unknown[])));
      if (this.queue.length >= BATCH && !this.paused) {
        this.paused = true;
        conn.core.pause();
      }
      this.notify();
    });
    cmd.on("error", (e: unknown) => {
      this.failure = { e };
      conn.failed(e);
      this.finish();
    });
    cmd.on("end", () => this.finish());
  }

  private finish(): void {
    this.ended = true;
    this.conn.done(this.sql);
    this.signal?.removeEventListener("abort", this.cancel);
    if (this.signal?.aborted === true) {
      this.conn.discard();
    }
    this.notify();
  }

  private notify(): void {
    const w = this.wake;
    this.wake = undefined;
    w?.();
  }

  async next(): Promise<Row | undefined> {
    for (;;) {
      const r = this.queue.shift();
      if (r !== undefined) {
        if (this.paused && this.queue.length === 0) {
          this.paused = false;
          this.conn.core.resume();
        }
        return r;
      }
      if (this.failure !== undefined) {
        throw this.failure.e;
      }
      if (this.ended) {
        return undefined;
      }
      await new Promise<void>((resolve) => {
        this.wake = resolve;
      });
    }
  }

  /** close drops the rows not read and waits for the rest to end, so the connection is free. */
  async close(): Promise<void> {
    this.closing = true;
    this.queue.length = 0;
    if (this.paused) {
      this.paused = false;
      // Resuming may deliver the rows the connection held at once.
      this.conn.core.resume();
    }
    while (!this.ended) {
      await new Promise<void>((resolve) => {
        this.wake = resolve;
      });
    }
  }
}

/** integer is how a prepared statement sends an integer: as a BIGINT, which LIMIT and integer columns read, rather than mysql2's DOUBLE. */
function integer(v: Value): unknown {
  if (typeof v === "bigint") {
    return v > 9_223_372_036_854_775_807n ? mysql.TypedParameter.LONGLONG.unsigned(v) : mysql.TypedParameter.LONGLONG(v);
  }
  if (typeof v === "number" && Number.isSafeInteger(v)) {
    return mysql.TypedParameter.LONGLONG(v);
  }
  return v;
}

/** MySqlConn is one connection. */
class MySqlConn implements InsertIdConn {
  /** Set while a transaction is open on the connection, when it broke and when a statement on it was killed, so release discards it. */
  private dirty = false;

  constructor(
    private readonly pool: MySqlPool,
    readonly core: mysql.PoolConnection,
  ) {}

  /** command starts one statement, in the pool's statement mode. */
  private command(s: Sent, cb?: (e: unknown, r: unknown, f: unknown) => void): mysql.Query {
    const values = encodeArgs(s.args, s.types);
    if (this.pool.c.mode === "simple") {
      return this.core.query({ sql: s.sql, values, rowsAsArray: true }, cb as never);
    }
    return this.core.execute({ sql: s.sql, values: values.map(integer) as never, rowsAsArray: true }, cb as never) as unknown as mysql.Query;
  }

  /** run runs one statement, killing it on the server when signal aborts. */
  private async run(s: Sent): Promise<Result> {
    const cancel = () => void this.cancel();
    s.signal?.addEventListener("abort", cancel, { once: true });
    try {
      return await new Promise<Result>((resolve, reject) => {
        this.command(s, (e, r, f) => (e === null ? resolve(Array.isArray(r) ? { rows: r as unknown[][], fields: f as Column[] } : (r as mysql.ResultSetHeader)) : reject(e)));
      });
    } catch (e) {
      this.failed(e);
      throw e;
    } finally {
      s.signal?.removeEventListener("abort", cancel);
      if (s.signal?.aborted === true) {
        // The kill may land after the statement ends, on the next one.
        this.dirty = true;
      }
      this.done(s.sql);
    }
  }

  /** done closes the prepared statement of sql in describe mode, which keeps none. */
  done(sql: string): void {
    if (this.pool.c.mode === "describe") {
      this.core.unprepare(sql);
    }
  }

  /** failed marks the connection to be discarded when e is not an error the server sent. */
  failed(e: unknown): void {
    if (!isServerError(e)) {
      this.dirty = true;
    }
  }

  /** discard marks the connection to be discarded on release. */
  discard(): void {
    this.dirty = true;
  }

  /** cancel asks the server, on a connection of its own, to kill the statement this connection runs. */
  async cancel(): Promise<void> {
    const other = mysql.createConnection(this.pool.config);
    other.on("error", ignore);
    try {
      await call((cb) => other.query(`KILL QUERY ${this.core.threadId}`, cb));
    } catch {
      // The statement ends anyway, by itself or with its connection.
    } finally {
      other.destroy();
    }
  }

  async query(s: Sent): Promise<Rows> {
    if (s.stream === true) {
      return new MySqlStream(this, this.command(s), s.signal, s.sql);
    }
    const r = await this.run(s);
    if (!("rows" in r)) {
      return new MySqlRows([]);
    }
    const ds = decoders(r.fields);
    return new MySqlRows(r.rows.map((row) => new Row(decodeRow(ds, row))));
  }

  async exec(s: Sent): Promise<number> {
    return (await this.execId(s)).affected;
  }

  async execId(s: Sent): Promise<InsertResult> {
    const r = await this.run(s);
    return "rows" in r ? { affected: 0, id: 0n } : { affected: r.affectedRows, id: BigInt(r.insertId) };
  }

  /** begin starts a transaction. An isolation level is set for the next transaction only, so it holds for this one. */
  async begin(o: BeginOptions): Promise<void> {
    this.dirty = true;
    if (o.isolation !== undefined) {
      await this.simple(`SET TRANSACTION ISOLATION LEVEL ${o.isolation.toUpperCase()}`);
    }
    await this.simple(`START TRANSACTION${o.readOnly ? " READ ONLY" : ""}`);
  }

  async commit(): Promise<void> {
    await this.simple("COMMIT");
    this.dirty = false;
  }

  async rollback(): Promise<void> {
    await this.simple("ROLLBACK");
    this.dirty = false;
  }

  async savepoint(name: string): Promise<void> {
    await this.simple(`SAVEPOINT ${ident(name)}`);
  }

  async rollbackTo(name: string): Promise<void> {
    await this.simple(`ROLLBACK TO SAVEPOINT ${ident(name)}`);
  }

  async releaseSavepoint(name: string): Promise<void> {
    await this.simple(`RELEASE SAVEPOINT ${ident(name)}`);
  }

  /** simple runs a statement with no parameters. */
  private simple(sql: string): Promise<unknown> {
    return call((cb) => this.core.query(sql, cb));
  }

  /** release returns the connection to its pool, or discards it when it broke or was left in a transaction. */
  release(): void {
    if (this.dirty) {
      this.core.destroy();
    } else {
      this.core.release();
    }
  }
}

/** ident quotes an identifier. */
function ident(name: string): string {
  return `\`${name.replace(/`/g, "``")}\``;
}
