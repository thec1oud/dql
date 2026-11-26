// A driver for the executor's tests: it records every call as text, answers
// statements with handle, and fails the calls fail names.

import { Row } from "../src/decode.js";
import type { BeginOptions, Conn, Driver, NativeError, Pool, PoolConfig, Rows, Sent } from "../src/driver.js";

/** FakeNative is an error the database sent. */
export class FakeNative extends Error {
  constructor(readonly native: NativeError) {
    super(`native ${native.code}`);
  }
}

/** rows answers every statement with these rows; SQL "FAIL <code>" fails with that native code. */
export function rows(...rs: unknown[][]): (s: Sent) => Row[] {
  return (s) => {
    if (s.sql.startsWith("FAIL ")) {
      throw new FakeNative({ code: s.sql.slice(5) });
    }
    return rs.map((r) => new Row(r));
  };
}

export class FakeDriver implements Driver {
  private log: string[] = [];
  readonly pools: FakePool[] = [];
  handle: (s: Sent) => Row[] = rows([1]);
  openError: unknown;
  /** Errors of calls by name, such as acquire, begin or commit, each thrown once when set. */
  readonly fail = new Map<string, unknown>();
  /** Delays acquire, begin and query until released, when set. */
  gate: Promise<void> | undefined;

  record(s: string): void {
    this.log.push(s);
  }

  /** calls returns the calls since the last, and forgets them. */
  calls(): string {
    const s = this.log.join("; ");
    this.log = [];
    return s;
  }

  failing(name: string): void {
    if (this.fail.has(name)) {
      const e = this.fail.get(name);
      this.fail.delete(name);
      throw e;
    }
  }

  async open(c: PoolConfig): Promise<Pool> {
    if (this.openError !== undefined) {
      throw this.openError;
    }
    this.record(`open ${c.dsn} max ${c.maxConns} mode ${c.mode} setup ${JSON.stringify(c.setup)}`);
    const p = new FakePool(this, c);
    this.pools.push(p);
    return p;
  }

  native(e: unknown): NativeError | undefined {
    return e instanceof FakeNative ? e.native : undefined;
  }

  broken(e: unknown): boolean {
    return e instanceof Error && e.message === "broken";
  }
}

export class FakePool implements Pool {
  private n = 0;
  private out = 0;
  private readonly waiting: (() => void)[] = [];
  closed = false;

  constructor(
    readonly d: FakeDriver,
    readonly cfg: PoolConfig,
  ) {}

  async acquire(signal?: AbortSignal): Promise<Conn> {
    await this.d.gate;
    this.d.failing("acquire");
    while (this.cfg.maxConns > 0 && this.out >= this.cfg.maxConns) {
      await new Promise<void>((resolve, reject) => {
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
    this.out++;
    this.n++;
    return new FakeConn(this, `${this.cfg.dsn}#${this.n}`);
  }

  put(): void {
    this.out--;
    this.waiting.shift()?.();
  }

  async close(): Promise<void> {
    this.closed = true;
    this.d.record(`close ${this.cfg.dsn}`);
  }
}

class FakeRows implements Rows {
  private i = 0;

  constructor(
    private readonly rs: Row[],
    private readonly d: FakeDriver,
  ) {}

  async next(): Promise<Row | undefined> {
    this.d.failing("next");
    return this.rs[this.i++];
  }

  async close(): Promise<void> {}
}

export class FakeConn implements Conn {
  constructor(
    private readonly p: FakePool,
    readonly id: string,
  ) {}

  private call(what: string): void {
    this.p.d.record(`${this.id} ${what}`);
    this.p.d.failing(what.split(" ")[0] as string);
  }

  async query(s: Sent): Promise<Rows> {
    await this.p.d.gate;
    this.call(`query ${s.sql} ${JSON.stringify(s.args)}`);
    return new FakeRows(this.p.d.handle(s), this.p.d);
  }

  async exec(s: Sent): Promise<number> {
    this.call(`exec ${s.sql} ${JSON.stringify(s.args)}`);
    return this.p.d.handle(s).length;
  }

  async begin(o: BeginOptions): Promise<void> {
    await this.p.d.gate;
    this.call(`begin${o.readOnly ? " read only" : ""}${o.isolation ? ` ${o.isolation}` : ""}`);
  }

  async commit(): Promise<void> {
    this.call("commit");
  }

  async rollback(): Promise<void> {
    this.call("rollback");
  }

  async savepoint(name: string): Promise<void> {
    this.call(`savepoint ${name}`);
  }

  async rollbackTo(name: string): Promise<void> {
    this.call(`rollbackTo ${name}`);
  }

  async releaseSavepoint(name: string): Promise<void> {
    this.call(`releaseSavepoint ${name}`);
  }

  release(): void {
    this.p.d.record(`${this.id} put back`);
    this.p.put();
  }
}
