// @dql/sqlserver is the dql executor's driver for SQL Server 2022 and newer,
// on tedious (design record §22). It turns the values tedious reads into the
// driver-neutral form of Row, and dql's host values into parameters SQL
// Server reads, so generated code holds no tedious types.
//
//   const exec = await Executor.open(new SqlServerDriver(), dialect, config);
//
// A DSN is a URL, sqlserver://user:password@host:1433?database=shop, as the
// Go driver reads it; encrypt=true turns on TLS for the whole connection and
// trustservercertificate=true accepts a self-signed certificate. tedious
// sends each parameterized statement through sp_executesql, so a statement is
// always prepared on the server.

import { type BeginOptions, type Conn, type Driver, type NativeError, type Pool, type PoolConfig, Row, type Rows, type Sent, type StatementMode } from "@dql/runtime";
import { Connection, type ConnectionConfiguration, Request } from "tedious";
import { encodeArgs } from "./args.js";
import { native } from "./errors.js";
import { install } from "./exact.js";
import { type Column, decodeRow, decoders } from "./values.js";

export { NoTypeError, encode } from "./args.js";
export { native } from "./errors.js";
export { Exact, Offset, decimalText, exact, install } from "./exact.js";

install();

/** The statement mode the driver sends statements in (§26.4): tedious prepares each through sp_executesql. */
const MODES: readonly StatementMode[] = ["prepare"];

/** The error codes of tedious that mean a connection failed or broke. */
const brokenCodes = new Set(["ESOCKET", "ECONNRESET", "ETIMEOUT", "ECLOSE", "EINVALIDSTATE", "ELOGIN"]);

/** ServerError is the part of tedious's RequestError that the server sent. */
interface ServerError {
  readonly number: number;
  readonly message: string;
}

/** isServerError reports whether e is an error the server sent. */
function isServerError(e: unknown): e is ServerError {
  return e instanceof Error && typeof (e as { number?: unknown }).number === "number";
}

/** ignore drops an error that needs no answer: a connection's that breaks while idle. */
function ignore(): void {}

/** config reads a sqlserver:// URL as tedious's configuration. */
export function config(dsn: string): ConnectionConfiguration {
  const u = new URL(dsn);
  if (u.protocol !== "sqlserver:") {
    throw new Error(`dqlsqlserver: ${u.protocol} is not sqlserver:`);
  }
  const q = new Map([...u.searchParams].map(([k, v]) => [k.toLowerCase(), v]));
  const yes = (k: string) => /^(?:true|yes|1)$/i.test(q.get(k) ?? "");
  const database = q.get("database");
  return {
    server: u.hostname,
    authentication: { type: "default", options: { userName: decodeURIComponent(u.username), password: decodeURIComponent(u.password) } },
    options: {
      port: u.port === "" ? 1433 : Number(u.port),
      ...(database === undefined ? {} : { database }),
      encrypt: yes("encrypt"),
      trustServerCertificate: yes("trustservercertificate"),
      useUTC: true,
      rowCollectionOnRequestCompletion: false,
      ...(q.has("connection timeout") ? { connectTimeout: Number(q.get("connection timeout")) * 1000 } : {}),
    },
  };
}

/** SqlServerDriver is a Driver on tedious. */
export class SqlServerDriver implements Driver {
  readonly statementModes = MODES;

  /** open returns a pool for c.dsn. Each new connection runs c.setup before its first statement. */
  async open(c: PoolConfig): Promise<Pool> {
    return new SqlServerPool(config(c.dsn), c);
  }

  /** native returns the fields of an error the server sent. */
  native(e: unknown): NativeError | undefined {
    return isServerError(e) ? native(e.number, e.message) : undefined;
  }

  /** broken reports whether e is a failure to connect, a socket error, or a use of a closed connection. */
  broken(e: unknown): boolean {
    if (!(e instanceof Error) || isServerError(e)) {
      return false;
    }
    const code = (e as { code?: unknown }).code;
    return (typeof code === "string" && brokenCodes.has(code)) || e.name === "ConnectionError";
  }
}

/** connect opens one connection. */
function connect(cfg: ConnectionConfiguration): Promise<Connection> {
  return new Promise((resolve, reject) => {
    const c = new Connection(cfg);
    c.connect((e) => {
      if (e === undefined || e === null) {
        resolve(c);
      } else {
        c.close();
        reject(e);
      }
    });
  });
}

/** Waiter is a caller waiting for a connection. */
interface Waiter {
  resolve(c: Connection): void;
  reject(e: unknown): void;
}

/** SqlServerPool holds up to maxConns connections, 10 by default, each set up once. */
class SqlServerPool implements Pool {
  private readonly idle: Connection[] = [];
  private readonly waiters: Waiter[] = [];
  /** How many connections are open or opening. */
  private open = 0;
  private closed = false;
  private readonly max: number;

  constructor(
    private readonly cfg: ConnectionConfiguration,
    readonly c: PoolConfig,
  ) {
    this.max = c.maxConns > 0 ? c.maxConns : 10;
  }

  /** acquire returns an idle connection, opens one when the pool has room, and otherwise waits for one until signal aborts. */
  async acquire(signal?: AbortSignal): Promise<SqlServerConn> {
    signal?.throwIfAborted();
    if (this.closed) {
      throw Object.assign(new Error("dqlsqlserver: the pool is closed"), { code: "ECLOSE" });
    }
    // An idle connection whose session the server ended is closed, and makes room.
    let c = this.idle.pop();
    while (c?.closed === true) {
      this.open--;
      c = this.idle.pop();
    }
    if (c !== undefined) {
      return new SqlServerConn(this, c);
    }
    if (this.open < this.max) {
      return new SqlServerConn(this, await this.fresh());
    }
    return new SqlServerConn(this, await this.wait(signal));
  }

  /** fresh opens a connection and runs the setup on it. */
  private async fresh(): Promise<Connection> {
    this.open++;
    let c: Connection | undefined;
    try {
      c = await connect(this.cfg);
      // A connection that breaks while idle must not crash the process.
      c.on("error", ignore);
      for (const s of this.c.setup) {
        await batch(c, s);
      }
      return c;
    } catch (e) {
      c?.close();
      this.open--;
      this.handOff();
      throw e;
    }
  }

  /** wait waits for a released connection, or for room to open one. */
  private wait(signal?: AbortSignal): Promise<Connection> {
    return new Promise((resolve, reject) => {
      const w: Waiter = {
        resolve: (c) => {
          signal?.removeEventListener("abort", aborted);
          resolve(c);
        },
        reject: (e) => {
          signal?.removeEventListener("abort", aborted);
          reject(e);
        },
      };
      const aborted = () => {
        this.waiters.splice(this.waiters.indexOf(w), 1);
        reject(signal?.reason);
      };
      signal?.addEventListener("abort", aborted, { once: true });
      this.waiters.push(w);
    });
  }

  /** handOff gives a waiter a fresh connection when one closed and made room. */
  private handOff(): void {
    const w = this.waiters.shift();
    if (w !== undefined) {
      this.fresh().then(w.resolve, w.reject);
    }
  }

  /** put takes a connection back: a waiter gets it, or it waits idle; a discarded one closes. */
  put(c: Connection, discard: boolean): void {
    if (discard || this.closed) {
      c.close();
      this.open--;
      this.handOff();
      return;
    }
    const w = this.waiters.shift();
    if (w !== undefined) {
      w.resolve(c);
    } else {
      this.idle.push(c);
    }
  }

  async close(): Promise<void> {
    this.closed = true;
    for (const c of this.idle.splice(0)) {
      c.close();
      this.open--;
    }
  }
}

/** batch runs a statement with no parameters. */
function batch(c: Connection, sql: string): Promise<number> {
  return new Promise((resolve, reject) => {
    c.execSqlBatch(new Request(sql, (e, n) => (e === undefined || e === null ? resolve(n ?? 0) : reject(e))));
  });
}

/** SqlServerRows holds a query's rows, read all at once. */
class SqlServerRows implements Rows {
  private i = 0;

  constructor(private readonly rows: Row[]) {}

  async next(): Promise<Row | undefined> {
    return this.rows[this.i++];
  }

  async close(): Promise<void> {}
}

/** BATCH is how many rows a stream holds before it pauses the request. */
const BATCH = 256;

/** Cell is one value of a row as tedious emits it. */
interface Cell {
  readonly value: unknown;
}

/**
 * SqlServerStream reads a statement's rows as they arrive, pausing the
 * request while it holds a batch the caller has not read, so a stream holds
 * about one batch in memory (§25.1). Closing early cancels the request.
 */
class SqlServerStream implements Rows {
  private readonly queue: Row[] = [];
  private ended = false;
  private failure: { e: unknown } | undefined;
  private paused = false;
  /** Called when a row arrives, the rows end or they fail. */
  private wake: (() => void) | undefined;

  constructor(
    private readonly conn: SqlServerConn,
    private readonly req: Request,
    done: Promise<unknown>,
  ) {
    let ds: ((v: unknown) => unknown)[] = [];
    req.on("columnMetadata", (cols) => {
      ds = decoders(cols as unknown as Column[]);
    });
    req.on("row", (cells) => {
      this.queue.push(new Row(decodeRow(ds, (cells as unknown as Cell[]).map((c) => c.value))));
      if (this.queue.length >= BATCH && !this.paused) {
        this.paused = true;
        req.pause();
      }
      this.notify();
    });
    done.then(
      () => this.finish(),
      (e: unknown) => {
        this.failure = { e };
        this.finish();
      },
    );
  }

  private finish(): void {
    this.ended = true;
    this.notify();
  }

  private notify(): void {
    const w = this.wake;
    this.wake = undefined;
    w?.();
  }

  private until(): Promise<void> {
    return new Promise<void>((resolve) => {
      this.wake = resolve;
    });
  }

  async next(): Promise<Row | undefined> {
    for (;;) {
      const r = this.queue.shift();
      if (r !== undefined) {
        if (this.paused && this.queue.length === 0) {
          this.paused = false;
          this.req.resume();
        }
        return r;
      }
      if (this.failure !== undefined) {
        throw this.failure.e;
      }
      if (this.ended) {
        return undefined;
      }
      await this.until();
    }
  }

  /** close drops the rows not read and cancels the rest, so the connection is free. */
  async close(): Promise<void> {
    this.queue.length = 0;
    if (!this.ended) {
      this.conn.core.cancel();
      if (this.paused) {
        this.paused = false;
        this.req.resume();
      }
    }
    while (!this.ended) {
      await this.until();
    }
  }
}

/** SqlServerConn is one connection. */
class SqlServerConn implements Conn {
  /** Set while a transaction is open, and when the connection broke, so release discards it. */
  private dirty = false;
  /** Set while a transaction runs at an isolation level of its own. */
  private isolated = false;

  constructor(
    private readonly pool: SqlServerPool,
    readonly core: Connection,
  ) {}

  /** start starts one statement, cancelling it on the server when signal aborts; done settles with its row count when it ends. */
  private start(s: Sent): { req: Request; done: Promise<number> } {
    const params = encodeArgs(s.args, s.types);
    let req!: Request;
    const done = new Promise<number>((resolve, reject) => {
      req = new Request(s.sql, (e, n) => (e === undefined || e === null ? resolve(n ?? 0) : reject(e)));
    });
    params.forEach((p, i) => req.addParameter(`p${i + 1}`, p.type, p.value));
    const cancel = () => this.core.cancel();
    s.signal?.addEventListener("abort", cancel, { once: true });
    const settled = done.then(
      (n) => n,
      (e: unknown) => {
        this.failed(e);
        throw e;
      },
    );
    void settled.finally(() => s.signal?.removeEventListener("abort", cancel)).catch(ignore);
    this.core.execSql(req);
    return { req, done: settled };
  }

  /** failed marks the connection to be discarded when e is not an error the server sent or a cancellation. */
  private failed(e: unknown): void {
    if (!isServerError(e) && (e as { code?: unknown }).code !== "ECANCEL") {
      this.dirty = true;
    }
  }

  async query(s: Sent): Promise<Rows> {
    const { req, done } = this.start(s);
    if (s.stream === true) {
      return new SqlServerStream(this, req, done);
    }
    const rows: Row[] = [];
    let ds: ((v: unknown) => unknown)[] = [];
    req.on("columnMetadata", (cols) => {
      ds = decoders(cols as unknown as Column[]);
    });
    req.on("row", (cells) => rows.push(new Row(decodeRow(ds, (cells as unknown as Cell[]).map((c) => c.value)))));
    await done;
    return new SqlServerRows(rows);
  }

  exec(s: Sent): Promise<number> {
    return this.start(s).done;
  }

  /** begin starts a transaction. SQL Server has no read-only transaction, so readOnly changes nothing. An isolation level holds for the session, so the transaction's end sets it back to READ COMMITTED. */
  async begin(o: BeginOptions): Promise<void> {
    this.dirty = true;
    if (o.isolation !== undefined) {
      await this.simple(`SET TRANSACTION ISOLATION LEVEL ${o.isolation.toUpperCase()}`);
      this.isolated = true;
    }
    await this.simple("BEGIN TRANSACTION");
  }

  commit(): Promise<void> {
    return this.end("COMMIT TRANSACTION");
  }

  /** rollback rolls the transaction back; one the server already ended, as an error can, needs nothing. */
  rollback(): Promise<void> {
    return this.end("IF @@TRANCOUNT > 0 ROLLBACK TRANSACTION");
  }

  /** end ends the transaction, then sets the isolation level back. */
  private async end(sql: string): Promise<void> {
    await this.simple(sql);
    if (this.isolated) {
      this.isolated = false;
      await this.simple("SET TRANSACTION ISOLATION LEVEL READ COMMITTED");
    }
    this.dirty = false;
  }

  async savepoint(name: string): Promise<void> {
    await this.simple(`SAVE TRANSACTION ${ident(name)}`);
  }

  async rollbackTo(name: string): Promise<void> {
    await this.simple(`ROLLBACK TRANSACTION ${ident(name)}`);
  }

  /** releaseSavepoint does nothing: a SQL Server savepoint lasts until its transaction ends. */
  async releaseSavepoint(): Promise<void> {}

  /** simple runs a statement with no parameters. */
  private async simple(sql: string): Promise<void> {
    try {
      await batch(this.core, sql);
    } catch (e) {
      this.failed(e);
      throw e;
    }
  }

  /** release returns the connection to its pool, or discards it when it broke or was left in a transaction. */
  release(): void {
    this.pool.put(this.core, this.dirty);
  }
}

/** ident quotes an identifier. */
function ident(name: string): string {
  return `[${name.replaceAll("]", "]]")}]`;
}
