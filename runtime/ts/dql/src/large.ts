// Large results and large inputs (design record §25).

import { within } from "./context.js";
import type { Row } from "./decode.js";
import { type Reader, UsageError, type Writer, bind, writes } from "./executor.js";
import { type Statement, execute, executeId, observed, queryAll, queryOptional, reread, sent } from "./statement.js";

/**
 * iterate runs s and yields its rows as they arrive, holding its connection
 * until iteration ends (§25.1): when the rows run out, the loop breaks, or
 * a step fails. It routes and reports to hooks as queryAll does, in the
 * context of the iteration's first step. Each iteration runs s anew.
 */
export function iterate(h: Reader, s: Statement): AsyncIterable<Row> {
  return { [Symbol.asyncIterator]: () => new Stream(h, s) };
}

/** Waiter is the step of an iteration waiting for its row. */
interface Waiter {
  resolve(r: IteratorResult<Row>): void;
  reject(e: unknown): void;
}

/**
 * Stream hands rows from a statement that runs as queryAll does to the
 * steps of an iteration, one row per step, so the statement keeps its
 * context, connection and hooks while the caller iterates.
 */
class Stream implements AsyncIterator<Row> {
  /** The statement, once the first step started it. */
  private running: Promise<unknown> | undefined;
  /** The step waiting for a row. */
  private waiter: Waiter | undefined;
  /** Resumes the statement after a row: true for the next row, false to stop. */
  private resume: ((more: boolean) => void) | undefined;
  private done = false;

  constructor(
    private readonly h: Reader,
    private readonly s: Statement,
  ) {}

  next(): Promise<IteratorResult<Row>> {
    if (this.done) {
      return Promise.resolve({ done: true, value: undefined });
    }
    return new Promise((resolve, reject) => {
      this.waiter = { resolve, reject };
      if (this.running === undefined) {
        this.running = this.run().then(
          () => this.finish({ done: true, value: undefined }),
          (err: unknown) => this.finish(undefined, err),
        );
      } else {
        this.take()?.(true);
      }
    });
  }

  /** return stops the iteration early, releasing the connection before it resolves. */
  async return(): Promise<IteratorResult<Row>> {
    this.done = true;
    this.take()?.(false);
    await this.running;
    return { done: true, value: undefined };
  }

  /** take returns the resumer of the statement, if it waits, and forgets it. */
  private take(): ((more: boolean) => void) | undefined {
    const r = this.resume;
    this.resume = undefined;
    return r;
  }

  /** finish ends the iteration, answering the waiting step, if any, with r or err. */
  private finish(r: IteratorResult<Row> | undefined, err?: unknown): void {
    this.done = true;
    const w = this.waiter;
    this.waiter = undefined;
    if (r === undefined) {
      w?.reject(err);
    } else {
      w?.resolve(r);
    }
  }

  /** run runs the statement, giving each row to the waiting step and waiting for the next step. */
  private run(): Promise<number> {
    const s = this.s;
    return observed(this.h, s, s.writes ?? false, (n) => n, async (b, c) => {
      let n = 0;
      try {
        const rows = await c.query({ ...sent(s), stream: true });
        try {
          for (;;) {
            const r = await rows.next();
            if (r === undefined) {
              return n;
            }
            n++;
            const more = new Promise<boolean>((resolve) => {
              this.resume = resolve;
            });
            const w = this.waiter as Waiter;
            this.waiter = undefined;
            w.resolve({ done: false, value: r });
            if (!(await more)) {
              return n;
            }
          }
        } finally {
          await rows.close();
        }
      } catch (err) {
        throw b.e.translate(err, s.catalog, s.query, b.src);
      }
    });
  }
}

/**
 * atomic runs fn in a write transaction: h's, the one the context carries,
 * or a new one, which commits when fn returns and reruns after a
 * serialization failure or deadlock (§22.3, §22.4).
 */
export function atomic<T>(h: Writer, fn: (w: Writer) => Promise<T>): Promise<T> {
  const { executor, frame } = h[bind]();
  const run = () => executor.write((tx) => fn(tx));
  return frame === undefined ? run() : within({ frame }, run);
}

/**
 * written runs s, a write whose row comes back by re-reading it (§20.1), in
 * one transaction: h's, the one the context carries, or a new one. A write
 * that affects no row, such as an insert-or-ignore that skips, returns none;
 * otherwise the read finds the row by its key, filled from the last insert
 * ID where the call left it out. h must be a Writer.
 */
export function written(h: Reader, s: Statement): Promise<Row | undefined> {
  const back = s[reread];
  if (back === undefined || !(writes in h)) {
    throw new UsageError("read_only");
  }
  return atomic(h as Writer, async (w) => {
    const { affected, id } = await executeId(w, s);
    return affected === 0 ? undefined : queryOptional(w, back(id));
  });
}

/** Bulk is a write of many rows, which splits into statements that each stay within the dialect's parameter limit (§25.2). */
export interface Bulk {
  readonly rows: number;
  /** The parameters each row binds. */
  readonly paramsPerRow: number;
  /** statement returns the statement that writes rows [from, to). */
  statement(from: number, to: number): Statement;
}

/** chunk returns how many rows each statement of b writes. */
export function chunk(b: Bulk, maxParams: number): number {
  if (maxParams <= 0 || b.paramsPerRow <= 0) {
    return Math.max(b.rows, 1);
  }
  return Math.max(Math.floor(maxParams / b.paramsPerRow), 1);
}

/**
 * executeBulk runs b and returns how many rows its statements affected.
 * More than one statement runs in one transaction, joining the one h or the
 * context carries, so the write is all or nothing.
 */
export async function executeBulk(h: Writer, b: Bulk): Promise<number> {
  let total = 0;
  await bulk(
    h,
    b,
    () => {
      total = 0;
    },
    async (w, s) => {
      total += await execute(w, s);
    },
  );
  return total;
}

/** queryBulk runs b, whose statements return rows, and returns them in order. */
export async function queryBulk(h: Writer, b: Bulk): Promise<Row[]> {
  let out: Row[] = [];
  await bulk(
    h,
    b,
    () => {
      out = [];
    },
    async (w, s) => {
      out.push(...(await queryAll(w, s)));
    },
  );
  return out;
}

/** bulk runs each chunk of b with run, after start: alone when one statement does, and otherwise in a write closure, which reruns from start. No rows run nothing. */
async function bulk(h: Writer, b: Bulk, start: () => void, run: (w: Writer, s: Statement) => Promise<void>): Promise<void> {
  const size = chunk(b, h[bind]().executor.dialect.maxParams ?? 0);
  const chunks = async (w: Writer) => {
    start();
    for (let from = 0; from < b.rows; from += size) {
      await run(w, b.statement(from, Math.min(from + size, b.rows)));
    }
  };
  if (b.rows <= size) {
    return chunks(h);
  }
  return atomic(h, chunks);
}
