// Logging hook events (design record §24), as Go's dqlslog does.

import { classOf } from "./errors.js";
import { type Hooks, Redacted, type TxEvent } from "./hooks.js";

/**
 * Logger is what logHooks writes to: console and winston loggers are
 * Loggers, and a pino logger is one through a wrapper that swaps the
 * arguments, such as { debug: (m, a) => p.debug(a, m), ... }.
 */
export interface Logger {
  debug(message: string, attrs: Record<string, unknown>): void;
  info(message: string, attrs: Record<string, unknown>): void;
  warn(message: string, attrs: Record<string, unknown>): void;
  error(message: string, attrs: Record<string, unknown>): void;
}

/** LogOptions configure logHooks. */
export interface LogOptions {
  /** Logs query parameters, with sensitive ones as "[redacted]". They are left out by default (§24). */
  readonly params?: boolean;
}

/**
 * logHooks returns hooks that log to l: a query's end at debug, or at warn
 * when it failed; a transaction's begin and end at debug, a retry at info
 * and a leaked transaction at warn; a pool wait at debug; and a replica
 * entering or leaving rotation at info. Durations are in milliseconds.
 *
 * Events name queries rather than carry values. A query's SQL text is
 * logged, since it is assembled from fragments and holds none; its
 * parameters are logged only with params, and then a sensitive one is
 * logged as "[redacted]".
 */
export function logHooks(l: Logger, options: LogOptions = {}): Hooks {
  const params = options.params === true;
  return {
    params,
    queryEnd(q) {
      const attrs: Record<string, unknown> = {
        query: q.query,
        kind: q.kind,
        dialect: q.dialect,
        catalog: q.catalog,
        source: q.source,
        replica: q.replica,
        consistency: q.consistency.toString(),
        attempt: q.attempt,
        duration: q.duration,
        rows: q.rows,
        sql: q.sql,
      };
      if (params) {
        attrs["params"] = (q.params ?? []).map((v) => (v === Redacted ? "[redacted]" : v));
      }
      if (q.error === undefined) {
        l.debug("dql query", attrs);
        return;
      }
      attrs["class"] = q.class ?? "";
      attrs["error"] = q.error instanceof Error ? q.error.message : String(q.error);
      l.warn("dql query", attrs);
    },
    txBegin(t) {
      l.debug("dql transaction begin", tx(t));
    },
    txEnd(t) {
      l.debug("dql transaction end", { ...tx(t), outcome: t.outcome, duration: t.duration });
    },
    txRetry(t) {
      l.info("dql transaction retry", { ...tx(t), delay: t.delay, class: classOf(t.error) ?? "" });
    },
    txLeaked(t) {
      l.warn("dql transaction leaked", { ...tx(t), duration: t.duration });
    },
    poolWait(p) {
      l.debug("dql pool wait", { source: p.source, replica: p.replica, wait: p.wait });
    },
    rotation(r) {
      l.info(r.in ? "dql replica entered rotation" : "dql replica left rotation", { source: r.source, replica: r.replica, lag: r.lag });
    },
  };
}

/** tx is the attributes every transaction event has. */
function tx(t: TxEvent): Record<string, unknown> {
  return { propagation: t.propagation, isolation: t.isolation ?? "", read_only: t.readOnly, savepoint: t.savepoint, attempt: t.attempt };
}
