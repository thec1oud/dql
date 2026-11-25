// The driver interface (design record §22). An adapter package wraps one
// driver library, such as @dql/pg for pg, so the runtime holds no driver and
// no dialect knowledge: SQL comes from the fragment table and query files
// (§16.2), and error codes from the generated error map (§23). A fake driver
// implements the same interfaces (§26.3).

import type { Row } from "./decode.js";

/**
 * StatementMode is how a source sends statements (§26.4). Fragments give
 * each query variant a stable SQL text, so prepared statements can be cached
 * by text: prepare caches named statements, for direct connections and the
 * default; describe uses unnamed statements, for poolers in transaction mode
 * such as PgBouncer; simple prepares nothing, for proxies that support
 * nothing else.
 */
export type StatementMode = "prepare" | "describe" | "simple";

/** Isolation is a transaction isolation level. Unset is the database's default. */
export type Isolation = "read committed" | "repeatable read" | "serializable";

/** Driver is what the executor needs from a database driver. */
export interface Driver {
  /** The statement modes the driver supports, or absent for every mode (§26.4). A source set to another fails when the executor opens. */
  readonly statementModes?: readonly StatementMode[];
  /** open returns a pool for one server. It does not connect. */
  open(c: PoolConfig): Promise<Pool>;
  /** native returns the database's own fields of e, or undefined when the database did not send e. */
  native(e: unknown): NativeError | undefined;
  /** broken reports whether e means a connection failed or broke, as opposed to a statement failing. */
  broken(e: unknown): boolean;
}

/** PoolConfig configures one pool. */
export interface PoolConfig {
  /** The data source name, with any env: reference resolved. */
  readonly dsn: string;
  /** The most connections the pool holds; 0 is the driver's default. */
  readonly maxConns: number;
  /** Statements every new connection runs, in order, before its first use (§22.7). */
  readonly setup: readonly string[];
  readonly mode: StatementMode;
}

/** Pool is a pool of connections to one server. */
export interface Pool {
  /** acquire returns a connection, waiting until signal aborts for one to free up. It connects when the pool has room. */
  acquire(signal?: AbortSignal): Promise<Conn>;
  close(): Promise<void>;
}

/** Sent is one statement as the driver runs it. */
export interface Sent {
  /** The query's name, for a driver, such as a fake, that matches statements by query (§26.3). */
  readonly query: string;
  readonly sql: string;
  readonly args: readonly unknown[];
  /** The database type of each argument, or "" where it is not known; absent when none is (Statement.types). */
  readonly types?: readonly string[];
  readonly signal?: AbortSignal;
}

/** Conn is one connection, which runs one statement at a time. */
export interface Conn {
  /** query runs a statement that returns rows. */
  query(s: Sent): Promise<Rows>;
  /** exec runs a statement and returns how many rows it affected. */
  exec(s: Sent): Promise<number>;
  begin(o: BeginOptions, signal?: AbortSignal): Promise<void>;
  commit(): Promise<void>;
  rollback(): Promise<void>;
  savepoint(name: string, signal?: AbortSignal): Promise<void>;
  rollbackTo(name: string): Promise<void>;
  releaseSavepoint(name: string): Promise<void>;
  /** release returns the connection to its pool. A broken connection is discarded. */
  release(): void;
}

/** Rows reads a query's rows, each in the driver-neutral form of Row. */
export interface Rows {
  /** next returns the next row, or undefined after the last. */
  next(): Promise<Row | undefined>;
  close(): Promise<void>;
}

/** BeginOptions are how a transaction begins. */
export interface BeginOptions {
  readonly readOnly: boolean;
  readonly isolation?: Isolation;
}

/**
 * NativeError is the database's own description of an error. message and
 * detail may quote values (§23.3). The names are those the database sends:
 * the schema and table or data type, and the constraint or column.
 */
export interface NativeError {
  /** Such as a SQLSTATE. */
  readonly code: string;
  readonly message?: string;
  readonly detail?: string;
  readonly schema?: string;
  readonly table?: string;
  readonly dataType?: string;
  readonly constraint?: string;
  readonly column?: string;
}
