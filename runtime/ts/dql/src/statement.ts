// Running statements on handles (design record §19, §22).

import { ambient } from "./context.js";
import type { Row } from "./decode.js";
import type { Conn, Sent } from "./driver.js";
import { NotFound, TooManyRows, classOf } from "./errors.js";
import { type Executor, type Frame, type Reader, type SourcePool, type Writer, UsageError, bind } from "./executor.js";
import { type QueryEvent, noLog, params } from "./hooks.js";
import { Consistency } from "./routing.js";

/** rawSource is the key of the source a raw statement names in place of its catalog's (§22.1). */
export const rawSource: unique symbol = Symbol("dql.rawSource");

/**
 * Statement is one statement generated code runs: SQL assembled from the
 * fragment table or a query file's text, its arguments in placeholder order,
 * and what the executor needs to route it and name it in errors.
 */
export interface Statement {
  /** The query's name, for errors and hooks (§23.1, §24). */
  readonly query: string;
  readonly catalog: string;
  readonly sql: string;
  readonly args?: readonly unknown[];
  /**
   * The database type of each argument, as the dialect names it, such as
   * pg_catalog.int4 or app._mood, or "" where it is not known, as for raw
   * SQL. A driver that sends values without the server describing the
   * statement first encodes each by its type (§26.4).
   */
  readonly types?: readonly string[];
  /** The statement writes, or calls a volatile function (§22.2). */
  readonly writes?: boolean;
  /** The core proved the statement returns at most one row, so queryOne and queryOptional skip the extra-row check (§19). */
  readonly atMostOne?: boolean;
  /** How fresh a read outside a write transaction must be: the call's option, else the query's default; absent takes the executor's (§22.5). */
  readonly consistency?: Consistency;
  /** For hooks (§24): the kind of query, such as select or insert. */
  readonly kind?: string;
  /** For hooks (§24): which arguments hold sensitive values, by position. */
  readonly sensitive?: readonly boolean[];
  /** For hooks (§24): the call opts out of events, its pool wait included. */
  readonly noLog?: boolean;
  /** The source a raw statement runs on in place of the catalog's. */
  readonly [rawSource]?: string;
}

/** @internal Bound is a statement's executor, frame and source. */
export interface Bound {
  readonly e: Executor;
  readonly f: Frame | undefined;
  readonly src: SourcePool;
}

/**
 * @internal observed runs a statement on h, firing the query hooks around it unless the
 * statement opts out (§24): fn runs on the statement's connection, and count
 * says how many rows its result holds.
 */
export function observed<T>(h: Reader, s: Statement, writes: boolean, count: (r: T) => number, fn: (b: Bound, c: Conn) => Promise<T>): Promise<T> {
  return noLog(s.noLog, async () => {
    const { executor: e, frame } = h[bind]();
    const f = frame ?? ambient().frame;
    const run = async (onConn: (replica: boolean) => void): Promise<T> => {
      const b: Bound = { e, f, src: e.sourceOf(s.catalog, s[rawSource]) };
      return withConn(b, s, writes, (c, replica) => {
        onConn(replica);
        return fn(b, c);
      });
    };
    const hooks = e.hooks;
    if (hooks.off) {
      return run(() => {});
    }
    let consistency = s.consistency ?? e.consistency;
    if (f !== undefined && f.root.options.readOnly && f.root.consistency !== undefined) {
      consistency = f.root.consistency;
    }
    if (writes || (f !== undefined && !f.root.options.readOnly)) {
      consistency = Consistency.primary;
    }
    let source = "";
    try {
      source = e.sourceOf(s.catalog, s[rawSource]).name;
    } catch {
      // run fails with the same error.
    }
    const ev: QueryEvent = {
      query: s.query,
      dialect: e.dialect.name,
      kind: s.kind ?? "",
      catalog: s.catalog,
      source,
      replica: false,
      consistency,
      attempt: f?.root.attempt ?? 1,
      sql: s.sql,
      duration: 0,
      rows: 0,
    };
    const ps = hooks.wantsParams ? params(s.args ?? [], s.sensitive) : undefined;
    const start = performance.now();
    const states = hooks.queryStart(ev, ps);
    let replica = false;
    let rows = 0;
    let error: unknown;
    try {
      const r = await hooks.activate(states, () =>
        run((onReplica) => {
          replica = onReplica;
        }),
      );
      rows = count(r);
      return r;
    } catch (err) {
      error = err;
      throw err;
    } finally {
      const cls = error === undefined ? undefined : classOf(error);
      const end: QueryEvent = {
        ...ev,
        replica,
        rows,
        duration: performance.now() - start,
        ...(error === undefined ? {} : { error }),
        ...(cls === undefined ? {} : { class: cls }),
      };
      hooks.queryEnd(end, ps, states);
    }
  });
}

/**
 * withConn runs fn on the connection a statement runs on: its transaction's,
 * or a pooled one, which a write takes from the primary and a read as its
 * consistency routes it (§22.5), and which withConn releases after fn, once a
 * write's position is recorded in the context's session. A statement whose
 * context has already aborted fails without running.
 */
async function withConn<T>(b: Bound, s: Statement, writes: boolean, fn: (c: Conn, replica: boolean) => Promise<T>): Promise<T> {
  const signal = ambient().signal;
  if (signal?.aborted) {
    throw b.e.translate(signal.reason, s.catalog, s.query, b.src);
  }
  if (b.f !== undefined) {
    if (writes && b.f.root.options.readOnly) {
      throw new UsageError("read_only");
    }
    const c = await b.f.conn(b.src, s.query);
    return fn(c, b.f.root.onReplica(b.src));
  }
  if (!writes) {
    const [c, replica] = await b.e.route(b.src, s.consistency, false, s.query);
    try {
      return await fn(c, replica);
    } finally {
      c.release();
    }
  }
  const c = await b.e.conn(b.src, false, s.query);
  try {
    const result = await fn(c, false);
    await b.e.recordWrite(ambient().session, b.src, c);
    return result;
  } finally {
    c.release();
  }
}

/** @internal sent is a statement as the driver runs it. */
export function sent(s: Statement): Sent {
  const signal = ambient().signal;
  return {
    query: s.query,
    sql: s.sql,
    args: s.args ?? [],
    ...(s.types === undefined ? {} : { types: s.types }),
    ...(signal === undefined ? {} : { signal }),
  };
}

/** fetchRows reads at most limit rows, or every row for a negative limit. */
function fetchRows(h: Reader, s: Statement, limit: number): Promise<Row[]> {
  return observed(h, s, s.writes ?? false, (r) => r.length, async (b, c) => {
    try {
      const rows = await c.query(sent(s));
      try {
        const out: Row[] = [];
        while (limit < 0 || out.length < limit) {
          const r = await rows.next();
          if (r === undefined) {
            break;
          }
          out.push(r);
        }
        return out;
      } finally {
        await rows.close();
      }
    } catch (err) {
      throw b.e.translate(err, s.catalog, s.query, b.src);
    }
  });
}

/** queryAll runs s and returns its rows (§19's many). */
export function queryAll(h: Reader, s: Statement): Promise<Row[]> {
  return fetchRows(h, s, -1);
}

/** queryOne runs s and returns its one row: NotFound for none and TooManyRows for more (§19). */
export async function queryOne(h: Reader, s: Statement): Promise<Row> {
  const r = await queryOptional(h, s);
  if (r === undefined) {
    throw new NotFound({ dialect: h[bind]().executor.dialect.name, query: s.query });
  }
  return r;
}

/** queryOptional runs s and returns its row, or undefined when there is none: TooManyRows for more than one (§19). */
export async function queryOptional(h: Reader, s: Statement): Promise<Row | undefined> {
  const rows = await fetchRows(h, s, s.atMostOne ? 1 : 2);
  if (rows.length > 1) {
    throw new TooManyRows({ dialect: h[bind]().executor.dialect.name, query: s.query });
  }
  return rows[0];
}

/** execute runs s, which writes, and returns how many rows it affected. */
export function execute(h: Writer, s: Statement): Promise<number> {
  return observed(h, s, true, (n) => n, async (b, c) => {
    try {
      return await c.exec(sent(s));
    } catch (err) {
      throw b.e.translate(err, s.catalog, s.query, b.src);
    }
  });
}

/** RAW_QUERY is the name raw statements carry in hooks and errors (§24). */
export const RAW_QUERY = "raw";

/**
 * RawReader runs SQL text a handle that reads may run on one source, by
 * name: the escape hatch for what generated code does not cover (§17,
 * §22.1). It runs on the source's primary, or in the transaction the handle
 * or the context holds there, and returns the driver's values undecoded. Its
 * values are parameters, never part of the text.
 */
export class RawReader {
  constructor(
    protected readonly handle: Reader,
    protected readonly source: string,
  ) {}

  /** query runs sql, which reads, and returns its rows. */
  query(sql: string, ...args: unknown[]): Promise<Row[]> {
    return queryAll(this.handle, this.statement(sql, args, false));
  }

  protected statement(sql: string, args: unknown[], writes: boolean): Statement {
    return { query: RAW_QUERY, kind: RAW_QUERY, catalog: "", sql, args, writes, consistency: Consistency.primary, [rawSource]: this.source };
  }
}

/** RawWriter is a RawReader on a handle that writes, which also runs statements that write. */
export class RawWriter extends RawReader {
  constructor(
    protected override readonly handle: Writer,
    source: string,
  ) {
    super(handle, source);
  }

  /** query runs sql, which may write, such as an INSERT ... RETURNING, and returns its rows. */
  override query(sql: string, ...args: unknown[]): Promise<Row[]> {
    return queryAll(this.handle, this.statement(sql, args, true));
  }

  /** exec runs sql, which writes, and returns how many rows it affected. */
  exec(sql: string, ...args: unknown[]): Promise<number> {
    return execute(this.handle, this.statement(sql, args, true));
  }
}

/** raw returns the raw reader of source on h; generated read handles call it from their raw method. */
export function raw(h: Reader, source: string): RawReader {
  return new RawReader(h, source);
}

/** rawWrite returns the raw writer of source on h; generated write handles call it from their raw method. */
export function rawWrite(h: Writer, source: string): RawWriter {
  return new RawWriter(h, source);
}
