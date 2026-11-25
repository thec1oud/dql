// Hooks (design record §24).

import { ambient, within } from "./context.js";
import type { Isolation } from "./driver.js";
import type { ErrorClass } from "./errors.js";
import type { Propagation } from "./executor.js";
import type { Consistency } from "./routing.js";

/**
 * Hooks observe what queries, transactions, pools and replicas do (§24).
 * Every member is optional.
 *
 * A start hook returns a state, such as a tracing span, which its end hook
 * receives. Where Go's start hooks return the context the work runs in,
 * activate runs the work with a state active instead: a transaction's
 * closure and its end, retry and leak hooks run in its begin state, and a
 * query and its end hooks in its start state, so hooks fired inside, and
 * tracing that reads its own context, see them.
 *
 * Events name queries rather than carry values: a query's SQL is assembled
 * from fragments and holds none, and its parameters are left out unless
 * params is set, and then a sensitive one is Redacted. A hook must not
 * throw: what it throws reaches the caller in place of the result.
 */
export interface Hooks<S = unknown> {
  /** Includes parameter values in query events. */
  readonly params?: boolean;
  queryStart?(ev: QueryEvent): S;
  queryEnd?(ev: QueryEvent, state: S | undefined): void;
  /** txBegin and txEnd see each transaction a closure or pair begins, and each savepoint of a nested closure; a joined closure begins none. */
  txBegin?(ev: TxEvent): S;
  txEnd?(ev: TxEvent, state: S | undefined): void;
  /** txRetry sees a closure's root transaction about to rerun. */
  txRetry?(ev: TxEvent, state: S | undefined): void;
  /** txLeaked sees a pair-form transaction still open when its operation ended, which the executor then rolls back (§22.3). */
  txLeaked?(ev: TxEvent, state: S | undefined): void;
  poolWait?(ev: PoolWaitEvent): void;
  rotation?(ev: RotationEvent): void;
  /** activate runs fn with state active, such as a tracing span's context. */
  activate?<T>(state: S | undefined, fn: () => T): T;
}

/** QueryEvent is a query's start or end (§24). */
export interface QueryEvent {
  readonly query: string;
  /** The dialect the query runs on, such as postgres. */
  readonly dialect: string;
  /** Such as select, insert or get, as generated code names it. */
  readonly kind: string;
  readonly catalog: string;
  readonly source: string;
  /** The query ran on a replica. Set at the end. */
  readonly replica: boolean;
  readonly consistency: Consistency;
  /** The run of its root transaction's closure, from 1. */
  readonly attempt: number;
  readonly sql: string;
  /** The arguments, when the hooks ask for them. */
  readonly params?: readonly unknown[];
  /** Set at the end, in milliseconds. */
  readonly duration: number;
  readonly rows: number;
  readonly error?: unknown;
  readonly class?: ErrorClass;
}

/** RedactedValue is the type of Redacted. */
export class RedactedValue {
  /** @internal */
  constructor() {}
  toString(): string {
    return "[redacted]";
  }
  /** toJSON spells it out, so a JSON logger prints it rather than {}. */
  toJSON(): string {
    return "[redacted]";
  }
}

/** Redacted stands in an event for the value of a sensitive parameter. */
export const Redacted: RedactedValue = Object.freeze(new RedactedValue());

/** TxOutcome is how a transaction ended. */
export type TxOutcome = "commit" | "rollback";

/** TxEvent is a transaction's begin, end, retry or leak (§24). */
export interface TxEvent {
  readonly propagation: Propagation;
  /** The isolation level it asked for, if any. */
  readonly isolation?: Isolation;
  readonly readOnly: boolean;
  /** The transaction is a savepoint of a nested closure. */
  readonly savepoint: boolean;
  readonly attempt: number;
  /** Set at the end, the leak and the retry, in milliseconds. */
  readonly duration?: number;
  /** Set at the end. */
  readonly outcome?: TxOutcome;
  /** The error that ended the transaction or attempt. */
  readonly error?: unknown;
  /** Set at a retry: the backoff before the next attempt, in milliseconds. */
  readonly delay?: number;
}

/** PoolWaitEvent is the time a statement or transaction waited for a connection (§24). */
export interface PoolWaitEvent {
  readonly source: string;
  readonly replica: boolean;
  /** In milliseconds. */
  readonly wait: number;
}

/** RotationEvent is a replica entering or leaving rotation (§24). */
export interface RotationEvent {
  readonly source: string;
  /** The replica's position in the source's configuration, from 0; a DSN may hold a password, so events do not carry it. */
  readonly replica: number;
  /** In milliseconds. */
  readonly lag: number;
  readonly in: boolean;
}

/** Leaked is the cause of the rollback of a pair-form transaction still open when its operation ended. */
export class Leaked extends Error {
  constructor() {
    super("dql: a transaction was still open when its operation ended");
    this.name = "Leaked";
  }
}

/** @internal HookSet is an executor's hooks; each event reaches every hook in the order they were given. */
export class HookSet {
  constructor(readonly list: readonly Hooks[]) {}

  /** @internal off reports whether events are off: there are no hooks, or the call opted out. */
  get off(): boolean {
    return this.list.length === 0 || ambient().noLog === true;
  }

  /** @internal queryStart fires the query start hooks and returns their states; the parameters go to the hooks that ask for them. */
  queryStart(ev: QueryEvent, params: readonly unknown[] | undefined): unknown[] {
    return this.list.map((h) => h.queryStart?.(withParams(h, ev, params)));
  }

  /** @internal queryEnd fires the query end hooks. */
  queryEnd(ev: QueryEvent, params: readonly unknown[] | undefined, states: readonly unknown[]): void {
    this.activate(states, () => this.list.forEach((h, i) => h.queryEnd?.(withParams(h, ev, params), states[i])));
  }

  /** @internal txBegin fires the transaction begin hooks and returns their states. */
  txBegin(ev: TxEvent): unknown[] {
    return this.list.map((h) => h.txBegin?.(ev));
  }

  /** @internal tx fires one kind of transaction hook. */
  tx(kind: "txEnd" | "txRetry" | "txLeaked", ev: TxEvent, states: readonly unknown[]): void {
    this.activate(states, () => this.list.forEach((h, i) => h[kind]?.(ev, states[i])));
  }

  /** @internal activate runs fn with each hook's state active, the first hook's outermost. */
  activate<T>(states: readonly unknown[], fn: () => T): T {
    let run = fn;
    for (let i = this.list.length - 1; i >= 0; i--) {
      const h = this.list[i] as Hooks;
      const inner = run;
      if (h.activate !== undefined) {
        run = () => (h as Required<Hooks>).activate(states[i], inner);
      }
    }
    return run();
  }

  /** @internal poolWait reports a wait for a connection, unless events are off. */
  poolWait(ev: PoolWaitEvent): void {
    if (!this.off) {
      for (const h of this.list) {
        h.poolWait?.(ev);
      }
    }
  }

  /** @internal rotation reports a replica entering or leaving rotation. */
  rotation(ev: RotationEvent): void {
    for (const h of this.list) {
      h.rotation?.(ev);
    }
  }

  /** @internal wantsParams reports whether a hook asks for parameters. */
  get wantsParams(): boolean {
    return this.list.some((h) => h.params === true);
  }
}

/** @internal noLog runs fn with events off, for a call that opts out of them. */
export function noLog<T>(off: boolean | undefined, fn: () => T): T {
  return off === true ? within({ noLog: true }, fn) : fn();
}

/** @internal params returns args with the sensitive ones redacted. */
export function params(args: readonly unknown[], sensitive: readonly boolean[] | undefined): unknown[] {
  return args.map((v, i) => (sensitive?.[i] === true ? Redacted : v));
}

/** withParams returns ev, with the parameters when h asks for them. */
function withParams(h: Hooks, ev: QueryEvent, params: readonly unknown[] | undefined): QueryEvent {
  return h.params === true && params !== undefined ? { ...ev, params } : ev;
}

/**
 * formatDuration writes ms milliseconds as Go's time.Duration does, such as
 * 1.5s, 250ms or 1h2m0s, so the runtimes' events read alike.
 */
export function formatDuration(ms: number): string {
  if (ms === 0) {
    return "0s";
  }
  const sign = ms < 0 ? "-" : "";
  let n = Math.abs(ms);
  if (n < 1000) {
    if (n >= 1) {
      return `${sign}${trim(n)}ms`;
    }
    if (n >= 0.001) {
      return `${sign}${trim(n * 1000)}µs`;
    }
    return `${sign}${trim(n * 1e6)}ns`;
  }
  let out = "";
  const h = Math.floor(n / 3_600_000);
  n -= h * 3_600_000;
  const m = Math.floor(n / 60_000);
  n -= m * 60_000;
  if (h > 0) {
    out += `${h}h`;
  }
  if (h > 0 || m > 0) {
    out += `${m}m`;
  }
  return `${sign}${out}${trim(n / 1000)}s`;
}

/** trim writes n with at most nine decimals and no trailing zeros. */
function trim(n: number): string {
  return String(Number(n.toFixed(9)));
}
