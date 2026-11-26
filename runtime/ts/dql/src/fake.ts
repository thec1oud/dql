// @dql/runtime/fake: a recording fake executor for tests (design record
// §26.3), as Go's dqlfake is.
//
//   const fake = new Fake();
//   fake.on("getUserByEmail", "a@b.c").returns([1, "a@b.c"]);
//   const exec = await fake.executor(dialect, ["shop"]);
//   ... code under test, given exec ...
//   fake.verify();
//
// It is a driver: statements go through the real executor, so routing,
// transactions, hooks and decoding run as they do against a database, and
// only the database is faked. Builder chains are never mocked method by
// method; a statement is matched by its query name and arguments.

import { isDeepStrictEqual } from "node:util";
import { Row } from "./decode.js";
import type { Conn, Driver, NativeError, Pool, Rows, Sent } from "./driver.js";
import { DatabaseError } from "./errors.js";
import { type Dialect, Executor, type ExecutorOptions } from "./executor.js";

/** Call is a statement the fake was asked to run. */
export interface Call {
  /** The query's name, as generated code names it. */
  readonly query: string;
  readonly sql: string;
  readonly args: readonly unknown[];
  /** The statement ran in a transaction. */
  readonly inTx: boolean;
}

/** Expectation is the answer to the calls of one query, with given arguments or any. */
export class Expectation {
  /** @internal */
  rows: Row[] = [];
  /** @internal How many rows a statement affected. */
  n = 0;
  /** @internal */
  error: unknown;
  /** @internal */
  count = 0;

  /** @internal */
  constructor(
    /** @internal */
    readonly query: string,
    /** @internal */
    readonly args: readonly unknown[] | undefined,
  ) {}

  /** returns answers with rows, each a Row or its values, and reports as many affected. */
  returns(...rows: (Row | readonly unknown[])[]): this {
    this.rows = rows.map((r) => (r instanceof Row ? r : new Row([...r])));
    this.n = rows.length;
    return this;
  }

  /** affected answers a statement that returns no rows with n affected. */
  affected(n: number): this {
    this.n = n;
    return this;
  }

  /** fails answers with err, which the executor translates as it would a driver's: a DqlError, such as new UniqueViolation(...), passes through. */
  fails(err: unknown): this {
    this.error = err;
    return this;
  }

  /** calls returns how many calls it answered. */
  get calls(): number {
    return this.count;
  }

  /** @internal matches reports whether the expectation answers c. */
  matches(c: Call): boolean {
    return c.query === this.query && (this.args === undefined || isDeepStrictEqual([...this.args], [...c.args]));
  }
}

/** Unexpected is the error of a call no expectation answers. Its class is DatabaseError. */
export class Unexpected extends DatabaseError {
  constructor(readonly call: Call) {
    super();
    this.message = `dqlfake: unexpected call of ${call.query} with ${format(call.args)}`;
    this.name = "Unexpected";
  }
}

/** format writes args for a message. */
function format(args: readonly unknown[]): string {
  return `[${args.map((a) => (typeof a === "string" ? JSON.stringify(a) : String(a))).join(", ")}]`;
}

/**
 * Fake is a recording fake driver: it answers each statement with the latest
 * expectation that matches its query and arguments, and records every call.
 */
export class Fake implements Driver {
  private readonly expectations: Expectation[] = [];
  private readonly recorded: Call[] = [];
  private readonly missed: Call[] = [];

  /** on answers calls of query with exactly args; with no args it answers only calls with none. */
  on(query: string, ...args: unknown[]): Expectation {
    return this.add(new Expectation(query, args));
  }

  /** onAny answers calls of query with any arguments. */
  onAny(query: string): Expectation {
    return this.add(new Expectation(query, undefined));
  }

  private add(x: Expectation): Expectation {
    this.expectations.push(x);
    return x;
  }

  /** calls returns every call, in order. */
  calls(): Call[] {
    return [...this.recorded];
  }

  /** unexpected returns the calls no expectation answered. */
  unexpected(): Call[] {
    return [...this.missed];
  }

  /** verify throws when a call was unexpected, naming each, so a test fails even when the code under test swallowed the error. */
  verify(): void {
    if (this.missed.length > 0) {
      throw new Error(this.missed.map((c) => `dqlfake: unexpected call of ${c.query} with ${format(c.args)}`).join("\n"));
    }
  }

  /** executor opens an executor on the fake, with one source serving catalogs. Close it when the test ends. */
  executor(dialect: Dialect, catalogs: readonly string[], options: ExecutorOptions = {}): Promise<Executor> {
    const config = { sources: { fake: { primary: { dsn: "fake" } } }, catalogs: Object.fromEntries(catalogs.map((c) => [c, "fake"])) };
    return Executor.open(this, dialect, config, options);
  }

  /** @internal answer records c and returns the expectation that answers it. */
  answer(c: Call): Expectation {
    this.recorded.push(c);
    for (let i = this.expectations.length - 1; i >= 0; i--) {
      const x = this.expectations[i] as Expectation;
      if (x.matches(c)) {
        x.count++;
        if (x.error !== undefined) {
          throw x.error;
        }
        return x;
      }
    }
    this.missed.push(c);
    throw new Unexpected(c);
  }

  async open(): Promise<Pool> {
    return { acquire: async () => new FakeConn(this), close: async () => {} };
  }

  native(): NativeError | undefined {
    return undefined;
  }

  broken(): boolean {
    return false;
  }
}

/** FakeConn answers statements from its fake, noting whether a transaction is open. */
class FakeConn implements Conn {
  private inTx = false;

  constructor(private readonly f: Fake) {}

  private call(s: Sent): Expectation {
    return this.f.answer({ query: s.query, sql: s.sql, args: [...s.args], inTx: this.inTx });
  }

  async query(s: Sent): Promise<Rows> {
    const rows = this.call(s).rows;
    let i = 0;
    return { next: async () => rows[i++], close: async () => {} };
  }

  async exec(s: Sent): Promise<number> {
    return this.call(s).n;
  }

  async begin(): Promise<void> {
    this.inTx = true;
  }

  async commit(): Promise<void> {
    this.inTx = false;
  }

  async rollback(): Promise<void> {
    this.inTx = false;
  }

  async savepoint(): Promise<void> {}
  async rollbackTo(): Promise<void> {}
  async releaseSavepoint(): Promise<void> {}
  release(): void {}
}
