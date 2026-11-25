// The ambient context (design record §22.3). TypeScript has no
// context.Context, so an AsyncLocalStorage carries what Go's ctx does: the
// active transaction, the operation that tracks pair-form transactions, the
// read-your-writes session, and the abort signal and deadline every
// statement honors. Calls keep the shapes of §26.1, such as
// await h.users.insert(draft).

import { AsyncLocalStorage } from "node:async_hooks";
import type { Frame, Root } from "./executor.js";
import type { Session } from "./routing.js";

/** @internal Ambient is what the context carries. */
export interface Ambient {
  /** The transaction statements join: its root or a savepoint in it. */
  readonly frame?: Frame;
  /** The pair-form roots begun under the current operation. */
  readonly operation?: Set<Root>;
  /** Records the request's writes for read-your-writes reads (§22.5). */
  readonly session?: Session;
  /** Aborts every statement and wait. */
  readonly signal?: AbortSignal;
  /** When the signal aborts on its own, in epoch milliseconds, so a retry stops before it would pass it (§22.4). */
  readonly deadline?: number;
}

const storage = new AsyncLocalStorage<Ambient>();

/** @internal ambient returns the current context. */
export function ambient(): Ambient {
  return storage.getStore() ?? {};
}

/** @internal within runs fn in the current context with changes. */
export function within<T>(changes: Ambient, fn: () => T): T {
  return storage.run({ ...ambient(), ...changes }, fn);
}

/** abortWith joins an enclosing signal, if any, and signal. */
function abortWith(signal: AbortSignal): AbortSignal {
  const outer = ambient().signal;
  return outer === undefined ? signal : AbortSignal.any([outer, signal]);
}

/**
 * withSignal runs fn with signal in its context: a statement fn runs,
 * directly or through generated code, fails with Canceled when signal
 * aborts, or with Timeout when its reason is a TimeoutError. An enclosing
 * signal still applies.
 */
export function withSignal<T>(signal: AbortSignal, fn: () => T): T {
  return within({ signal: abortWith(signal) }, fn);
}

/**
 * withTimeout runs fn with a deadline ms milliseconds away: statements fail
 * with Timeout once it passes, and a transaction stops retrying when the
 * next backoff would pass it (§22.4). An enclosing deadline that is earlier
 * still applies.
 */
export function withTimeout<T>(ms: number, fn: () => T): T {
  const outer = ambient().deadline;
  const deadline = Date.now() + ms;
  return within({ signal: abortWith(AbortSignal.timeout(ms)), deadline: outer === undefined ? deadline : Math.min(outer, deadline) }, fn);
}

/** currentSignal returns the abort signal of the current context, if any, for code that passes it on, such as to fetch. */
export function currentSignal(): AbortSignal | undefined {
  return ambient().signal;
}
