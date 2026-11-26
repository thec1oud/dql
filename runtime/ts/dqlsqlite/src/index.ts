// @dql/sqlite is the dql executor's driver for SQLite, on the runtime's own
// binding: bun:sqlite on Bun, and node:sqlite on Node and Deno (design record
// §22). It turns the values SQLite reads into the driver-neutral form of Row,
// and dql's host values into values SQLite stores, so generated code holds no
// binding types.
//
//   const exec = await Executor.open(new SqliteDriver(), dialect, config);
//
// A DSN is the path of the database file. Both bindings are synchronous, so a
// statement holds the event loop while it runs, and cannot be canceled once
// it starts. A pool holds one connection unless the config asks for more:
// a second connection that waits for the first's write lock would hold the
// event loop the first needs to finish.
//
// SQLite's transactions are serializable whatever the isolation level asked
// for. A transaction that may write begins IMMEDIATE, taking the write lock
// at once, so that it never fails to upgrade a read snapshot part way; a
// read-only one begins DEFERRED and refuses writes with query_only.

import { type BeginOptions, type Conn, type Driver, type NativeError, type Pool, type PoolConfig, Row, type Rows, type Sent } from "@dql/runtime";
import { encodeArgs } from "./args.js";
import { type Database, type Open, binding } from "./backend.js";
import { codeOf, native } from "./errors.js";

export { encode } from "./args.js";
export { type Database, type Open, type Prepared, binding, bun, node } from "./backend.js";
export { codeOf, native } from "./errors.js";

/** SqliteDriver is a Driver on the runtime's SQLite binding. */
export class SqliteDriver implements Driver {
  /** open, when given, opens databases in place of the runtime's binding. */
  constructor(private readonly open_?: Open) {}

  /** open returns a pool for the database file c.dsn. Each new connection runs c.setup. SQLite prepares every statement, so the statement mode changes nothing (§26.4). */
  async open(c: PoolConfig): Promise<Pool> {
    return new SqlitePool(this.open_ ?? (await binding()), c);
  }

  /** native returns the fields of an error SQLite reported. */
  native(e: unknown): NativeError | undefined {
    const code = codeOf(e);
    return code === undefined ? undefined : native(code, (e as Error).message);
  }

  /** broken reports whether e is the use of a closed connection. SQLite runs in the process, so nothing else breaks one. */
  broken(e: unknown): boolean {
    return e instanceof Error && codeOf(e) === undefined && /database is not open|closed database/i.test(e.message);
  }
}

/** Waiter is an acquire waiting for a connection. */
interface Waiter {
  readonly resolve: (db: Database) => void;
  readonly reject: (e: unknown) => void;
}

/** SqlitePool holds the connections to one database file. */
class SqlitePool implements Pool {
  private readonly idle: Database[] = [];
  private readonly waiters: Waiter[] = [];
  /** How many connections are open, idle or not. */
  private open = 0;
  private closed = false;
  private readonly max: number;

  constructor(
    private readonly connect: Open,
    private readonly c: PoolConfig,
  ) {
    this.max = c.maxConns > 0 ? c.maxConns : 1;
  }

  /** acquire returns an idle connection, opens one when the pool has room, or waits until signal aborts for one to free up. */
  async acquire(signal?: AbortSignal): Promise<Conn> {
    signal?.throwIfAborted();
    if (this.closed) {
      throw new Error("dqlsqlite: the pool is closed");
    }
    const idle = this.idle.pop();
    if (idle !== undefined) {
      return new SqliteConn(this, idle);
    }
    if (this.open < this.max) {
      return new SqliteConn(this, this.fresh());
    }
    const db = await new Promise<Database>((resolve, reject) => {
      // No socket keeps the process alive while it waits, and Node's
      // timeout signals do not, so a waiter holds a timer of its own until
      // it is settled: else Node would exit before the wait ends. The
      // timer is there only to be pending, so any callback does.
      const alive = setInterval(Date.now, 2 ** 30);
      const w: Waiter = {
        resolve: (db) => {
          clearInterval(alive);
          signal?.removeEventListener("abort", aborted);
          resolve(db);
        },
        reject: (e) => {
          clearInterval(alive);
          signal?.removeEventListener("abort", aborted);
          reject(e);
        },
      };
      const aborted = () => {
        this.waiters.splice(this.waiters.indexOf(w), 1);
        w.reject(signal?.reason);
      };
      signal?.addEventListener("abort", aborted, { once: true });
      this.waiters.push(w);
    });
    return new SqliteConn(this, db);
  }

  /** fresh opens a connection and runs the setup statements on it. */
  private fresh(): Database {
    const db = this.connect(this.c.dsn);
    try {
      for (const s of this.c.setup) {
        db.exec(s);
      }
    } catch (e) {
      db.close();
      throw e;
    }
    this.open++;
    return db;
  }

  /** put takes a connection back, handing it to the first waiter, or closes it when it is discarded and opens a waiter its own. */
  put(db: Database, discard: boolean): void {
    if (discard || this.closed) {
      db.close();
      this.open--;
    }
    const w = this.waiters.shift();
    if (w === undefined) {
      if (!discard && !this.closed) {
        this.idle.push(db);
      }
      return;
    }
    if (!discard) {
      w.resolve(db);
      return;
    }
    try {
      w.resolve(this.fresh());
    } catch (e) {
      w.reject(e);
    }
  }

  async close(): Promise<void> {
    this.closed = true;
    for (const db of this.idle.splice(0)) {
      db.close();
      this.open--;
    }
    for (const w of this.waiters.splice(0)) {
      w.reject(new Error("dqlsqlite: the pool is closed"));
    }
  }
}

/** SqliteRows reads a statement's rows from its iterator. */
class SqliteRows implements Rows {
  constructor(private readonly it: Iterator<unknown[]>) {}

  async next(): Promise<Row | undefined> {
    const r = this.it.next();
    return r.done === true ? undefined : new Row(r.value);
  }

  async close(): Promise<void> {
    this.it.return?.();
  }
}

/** SqliteConn is one connection. It runs transaction control as statements, so it knows when a transaction is open: a connection released in one, or left read-only, is discarded. */
class SqliteConn implements Conn {
  private tx = false;
  private readOnly = false;
  /** The connection's prepared statements, by text. */
  private readonly prepared = new Map<string, ReturnType<Database["prepare"]>>();

  constructor(
    private readonly pool: SqlitePool,
    private readonly db: Database,
  ) {}

  private statement(sql: string): ReturnType<Database["prepare"]> {
    let p = this.prepared.get(sql);
    if (p === undefined) {
      p = this.db.prepare(sql);
      this.prepared.set(sql, p);
    }
    return p;
  }

  async query(s: Sent): Promise<Rows> {
    s.signal?.throwIfAborted();
    return new SqliteRows(this.statement(s.sql).rows(encodeArgs(s.args, s.types), s.stream === true)[Symbol.iterator]());
  }

  async exec(s: Sent): Promise<number> {
    s.signal?.throwIfAborted();
    return this.statement(s.sql).run(encodeArgs(s.args, s.types));
  }

  /** begin starts a transaction: IMMEDIATE when it may write, and DEFERRED under query_only when it only reads. */
  async begin(o: BeginOptions, signal?: AbortSignal): Promise<void> {
    signal?.throwIfAborted();
    if (o.readOnly) {
      this.db.exec("PRAGMA query_only = ON");
      this.readOnly = true;
      try {
        this.db.exec("BEGIN DEFERRED");
      } catch (e) {
        this.writable();
        throw e;
      }
    } else {
      this.db.exec("BEGIN IMMEDIATE");
    }
    this.tx = true;
  }

  async commit(): Promise<void> {
    this.end("COMMIT");
  }

  /** rollback rolls the transaction back. SQLite rolls a transaction back on its own after some errors, such as a full disk; the transaction has then ended, so the rollback succeeds. */
  async rollback(): Promise<void> {
    try {
      this.end("ROLLBACK");
    } catch (e) {
      if (!(e instanceof Error) || !e.message.includes("no transaction is active")) {
        throw e;
      }
      this.tx = false;
      this.writable();
    }
  }

  /** end ends the transaction, and lets a read-only one's connection write again. When the statement fails the transaction may still be open, so the connection is discarded on release. */
  private end(sql: string): void {
    this.db.exec(sql);
    this.tx = false;
    this.writable();
  }

  /** writable turns query_only off after a read-only transaction. */
  private writable(): void {
    if (this.readOnly) {
      this.db.exec("PRAGMA query_only = OFF");
      this.readOnly = false;
    }
  }

  async savepoint(name: string, signal?: AbortSignal): Promise<void> {
    signal?.throwIfAborted();
    this.db.exec(`SAVEPOINT ${ident(name)}`);
  }

  async rollbackTo(name: string): Promise<void> {
    this.db.exec(`ROLLBACK TO SAVEPOINT ${ident(name)}`);
  }

  async releaseSavepoint(name: string): Promise<void> {
    this.db.exec(`RELEASE SAVEPOINT ${ident(name)}`);
  }

  /** release returns the connection to its pool, or discards it when it is left in a transaction or read-only. */
  release(): void {
    this.pool.put(this.db, this.tx || this.readOnly);
  }
}

/** ident quotes an identifier. */
function ident(name: string): string {
  return `"${name.replaceAll('"', '""')}"`;
}
