// The two SQLite bindings the adapter runs on: bun:sqlite on Bun, and
// node:sqlite on Node and Deno. Both are synchronous and read integers as
// bigints, reals as numbers, text as strings and blobs as Uint8Arrays.

/** Prepared is one prepared statement. */
export interface Prepared {
  /** rows runs the statement and returns its rows, each as an array, read as the caller iterates where lazy is set and the binding can. */
  rows(args: readonly unknown[], lazy: boolean): Iterable<unknown[]>;
  /** run runs the statement and returns how many rows it changed. */
  run(args: readonly unknown[]): number;
}

/** Database is one open database connection. */
export interface Database {
  prepare(sql: string): Prepared;
  /** exec runs statements that take no parameters. */
  exec(sql: string): void;
  close(): void;
}

/** Open opens the database file at path, creating it if it does not exist. */
export type Open = (path: string) => Database;

/** BunDatabase is the part of bun:sqlite's Database the adapter uses. */
interface BunDatabase {
  prepare(sql: string): { values(...args: unknown[]): unknown[][]; run(...args: unknown[]): { changes: number } };
  exec(sql: string): void;
  close(): void;
}

/** bun opens databases with bun:sqlite. */
export function bun(Db: new (path: string, options: { safeIntegers: boolean }) => BunDatabase): Open {
  return (path) => {
    const db = new Db(path, { safeIntegers: true });
    return {
      prepare(sql) {
        const s = db.prepare(sql);
        return {
          rows: (args) => s.values(...args),
          run: (args) => s.run(...args).changes,
        };
      },
      exec: (sql) => db.exec(sql),
      close: () => db.close(),
    };
  };
}

/** NodeStatement is the part of node:sqlite's StatementSync the adapter uses. */
interface NodeStatement {
  setReadBigInts(on: boolean): void;
  setReturnArrays(on: boolean): void;
  all(...args: unknown[]): unknown[];
  iterate(...args: unknown[]): Iterator<unknown>;
  run(...args: unknown[]): { changes: number | bigint };
}

/** NodeDatabase is the part of node:sqlite's DatabaseSync the adapter uses. */
interface NodeDatabase {
  prepare(sql: string): NodeStatement;
  exec(sql: string): void;
  close(): void;
}

/** node opens databases with node:sqlite. */
export function node(Db: new (path: string) => NodeDatabase): Open {
  return (path) => {
    const db = new Db(path);
    return {
      prepare(sql) {
        const s = db.prepare(sql);
        s.setReadBigInts(true);
        s.setReturnArrays(true);
        return {
          rows: (args, lazy) => (lazy ? { [Symbol.iterator]: () => s.iterate(...args) } : s.all(...args)) as Iterable<unknown[]>,
          run: (args) => Number(s.run(...args).changes),
        };
      },
      exec: (sql) => db.exec(sql),
      close: () => db.close(),
    };
  };
}

/** binding returns the binding of the runtime the process runs on: bun:sqlite on Bun, and node:sqlite elsewhere. */
export async function binding(onBun = "Bun" in globalThis): Promise<Open> {
  if (onBun) {
    const m = (await import("bun:sqlite" as string)) as { Database: new (path: string, options: { safeIntegers: boolean }) => BunDatabase };
    return bun(m.Database);
  }
  const m = (await import("node:sqlite" as string)) as { DatabaseSync: new (path: string) => NodeDatabase };
  return node(m.DatabaseSync);
}
