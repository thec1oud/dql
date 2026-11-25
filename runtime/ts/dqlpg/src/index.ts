// @dql/pg is the dql executor's driver for Postgres, on node-postgres (design
// record §22). It turns the text Postgres writes into the driver-neutral form
// of Row, and dql's host values into text Postgres reads, so generated code
// holds no pg types.
//
//   const exec = await Executor.open(new PgDriver(), dialect, config);
//
// Every value is read and written as text. A type the catalog defines, such
// as an enum, a composite type or an array of one, is looked up once per pool
// by its OID, so the driver needs no list of types.

import { createHash } from "node:crypto";
import { type BeginOptions, type Conn, type Driver, type NativeError, type Pool, type PoolConfig, Row, type Rows, type Sent, type StatementMode } from "@dql/runtime";
import pg from "pg";
import { encodeArgs } from "./args.js";
import { CATALOG_QUERY, type CatalogRow, Types } from "./values.js";

export { encode, literal } from "./args.js";

/** The modes the driver sends statements in (§26.4): prepare caches a named prepared statement per text; describe sends unnamed statements, which poolers in transaction mode allow. node-postgres cannot send parameters without the extended protocol, so simple is not supported. */
const MODES: readonly StatementMode[] = ["prepare", "describe"];

/** CommitRolledBack is commit's error when the database rolled the transaction back instead, because a statement in it failed. */
export class CommitRolledBack extends Error {
  constructor() {
    super("dqlpg: commit rolled back");
    this.name = "CommitRolledBack";
  }
}

/** The text of each value: node-postgres parses nothing, so the adapter decodes every value itself. */
const raw = { getTypeParser: () => (s: string) => s };

/** The error codes of node's sockets that mean a connection failed or broke. */
const brokenCodes = new Set(["ECONNREFUSED", "ECONNRESET", "EPIPE", "ETIMEDOUT", "ENOTFOUND", "EHOSTUNREACH", "ECONNABORTED"]);

/** ignore drops an error that needs no answer: a connection's that breaks while idle, or one a statement's failure already reports. */
function ignore(): void {}

/** PgDriver is a Driver on node-postgres. */
export class PgDriver implements Driver {
  readonly statementModes = MODES;

  /** open returns a pool for c.dsn. Each new connection runs c.setup before its first statement. */
  async open(c: PoolConfig): Promise<Pool> {
    const config: pg.PoolConfig = { connectionString: c.dsn, ...(c.maxConns > 0 ? { max: c.maxConns } : {}) };
    return new PgPool(new pg.Pool(config), config, c);
  }

  /** native returns the fields of an error the server sent. */
  native(e: unknown): NativeError | undefined {
    if (!(e instanceof pg.DatabaseError) || e.code === undefined) {
      return undefined;
    }
    const n: Record<string, string> = { code: e.code };
    for (const [k, v] of [
      ["message", e.message],
      ["detail", e.detail],
      ["schema", e.schema],
      ["table", e.table],
      ["dataType", e.dataType],
      ["constraint", e.constraint],
      ["column", e.column],
    ] as const) {
      if (v !== undefined) {
        n[k] = v;
      }
    }
    return n as unknown as NativeError;
  }

  /** broken reports whether e is a failure to connect, a socket error, or a use of a closed connection. */
  broken(e: unknown): boolean {
    if (!(e instanceof Error) || e instanceof pg.DatabaseError) {
      return false;
    }
    const code = (e as { code?: unknown }).code;
    return (
      (typeof code === "string" && brokenCodes.has(code)) ||
      /Connection terminated|Client has encountered a connection error|not queryable|Client was closed/.test(e.message)
    );
  }
}

/** PgPool is a pool of one server, with the types its connections have seen. */
class PgPool implements Pool {
  readonly types = new Types();
  /** The connections that ran the setup statements. */
  private readonly ready = new WeakSet<pg.PoolClient>();

  constructor(
    private readonly pool: pg.Pool,
    /** How to connect, for a cancel request. */
    readonly config: pg.ClientConfig,
    readonly c: PoolConfig,
  ) {
    // An idle connection's error must not crash the process; the pool drops it.
    pool.on("error", ignore);
  }

  /** acquire waits for a connection until signal aborts, and runs the setup statements on a new one. */
  async acquire(signal?: AbortSignal): Promise<Conn> {
    signal?.throwIfAborted();
    const pending = this.pool.connect();
    const client = await (signal === undefined
      ? pending
      : new Promise<pg.PoolClient>((resolve, reject) => {
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
    if (!this.ready.has(client)) {
      // A connection that breaks while checked out fails its statements; its error event must not crash the process.
      client.on("error", ignore);
      try {
        for (const s of ["SET intervalstyle = 'iso_8601'", ...this.c.setup]) {
          await client.query(s);
        }
      } catch (e) {
        client.release(true);
        throw e;
      }
      this.ready.add(client);
    }
    return new PgConn(this, client);
  }

  async close(): Promise<void> {
    await this.pool.end();
  }
}

/** statementName names the prepared statement of a text: the same text gets the same name on every connection. */
function statementName(sql: string): string {
  return `dql_${createHash("sha256").update(sql).digest("hex").slice(0, 32)}`;
}

/** PgRows holds a query's rows, read all at once. */
class PgRows implements Rows {
  private i = 0;

  constructor(private readonly rows: Row[]) {}

  async next(): Promise<Row | undefined> {
    return this.rows[this.i++];
  }

  async close(): Promise<void> {}
}

/** PgConn is one connection. */
class PgConn implements Conn {
  /** Set while a transaction is open on the connection, and when it broke, so release discards it. */
  private dirty = false;

  constructor(
    private readonly pool: PgPool,
    private readonly client: pg.PoolClient,
  ) {}

  /** run runs one statement, canceling it on the server when signal aborts. */
  private async run(s: Sent): Promise<pg.QueryArrayResult<string[]>> {
    const config: pg.QueryArrayConfig = {
      text: s.sql,
      values: encodeArgs(s.args, s.types),
      rowMode: "array",
      types: raw,
      ...(this.pool.c.mode === "prepare" ? { name: statementName(s.sql) } : {}),
    };
    const signal = s.signal;
    const cancel = () => void this.cancel();
    signal?.addEventListener("abort", cancel, { once: true });
    try {
      return await this.client.query<string[]>(config);
    } catch (e) {
      if (!(e instanceof pg.DatabaseError)) {
        this.dirty = true;
      }
      throw e;
    } finally {
      signal?.removeEventListener("abort", cancel);
    }
  }

  /** cancel asks the server, on a connection of its own, to cancel the statement this connection runs. */
  private async cancel(): Promise<void> {
    const pid = (this.client as unknown as { processID: number }).processID;
    const other = new pg.Client(this.pool.config);
    try {
      await other.connect();
      await other.query("SELECT pg_catalog.pg_cancel_backend($1)", [pid]);
    } catch {
      // The statement ends anyway, by itself or with its connection.
    } finally {
      await other.end().catch(ignore);
    }
  }

  async query(s: Sent): Promise<Rows> {
    const r = await this.run(s);
    const oids = r.fields.map((f) => f.dataTypeID);
    const missing = this.pool.types.missing(oids);
    if (missing.length > 0) {
      await this.learn(missing);
    }
    const types = this.pool.types;
    return new PgRows(r.rows.map((row) => new Row(row.map((v, i) => types.decode(oids[i] as number, v)))));
  }

  /** learn asks the catalog about the types of oids, and of the types they name, until it knows them all. */
  private async learn(oids: number[]): Promise<void> {
    const types = this.pool.types;
    for (let ask = oids; ask.length > 0; ask = types.missing(ask)) {
      const r = await this.client.query<CatalogRow>(CATALOG_QUERY, [ask]);
      types.learn(ask, r.rows);
    }
  }

  async exec(s: Sent): Promise<number> {
    return (await this.run(s)).rowCount ?? 0;
  }

  async begin(o: BeginOptions): Promise<void> {
    this.dirty = true;
    await this.client.query(`BEGIN${o.isolation === undefined ? "" : ` ISOLATION LEVEL ${o.isolation}`}${o.readOnly ? " READ ONLY" : ""}`);
  }

  async commit(): Promise<void> {
    const r = await this.client.query("COMMIT");
    this.dirty = false;
    if (r.command === "ROLLBACK") {
      throw new CommitRolledBack();
    }
  }

  async rollback(): Promise<void> {
    await this.client.query("ROLLBACK");
    this.dirty = false;
  }

  async savepoint(name: string): Promise<void> {
    await this.client.query(`SAVEPOINT ${ident(name)}`);
  }

  async rollbackTo(name: string): Promise<void> {
    await this.client.query(`ROLLBACK TO SAVEPOINT ${ident(name)}`);
  }

  async releaseSavepoint(name: string): Promise<void> {
    await this.client.query(`RELEASE SAVEPOINT ${ident(name)}`);
  }

  /** release returns the connection to its pool, which discards it when it broke or was left in a transaction. */
  release(): void {
    this.client.release(this.dirty);
  }
}

/** ident quotes an identifier. */
function ident(name: string): string {
  return `"${name.replace(/"/g, '""')}"`;
}
