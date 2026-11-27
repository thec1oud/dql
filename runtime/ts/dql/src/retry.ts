// Retries (design record §22.4).

import { ambient } from "./context.js";
import { type ErrorClass, classOf } from "./errors.js";

/**
 * RetryPolicy is how a closure that owns its root transaction reruns after a
 * serialization failure or a deadlock (§22.4): up to attempts runs, with
 * exponential backoff from initial milliseconds, capped at max, with full
 * jitter.
 *
 * on lists the error classes retry(policy).do reruns after; absent is
 * serialization failures and deadlocks. Transaction closures ignore it: they
 * rerun only after those two, because a rerun is expected to succeed.
 */
export interface RetryPolicy {
  readonly attempts: number;
  readonly initial: number;
  readonly max: number;
  readonly on?: readonly ErrorClass[];
}

/** DEFAULT_RETRY is 3 attempts from 10ms, capped at 200ms (§22.4). */
export const DEFAULT_RETRY: RetryPolicy = Object.freeze({ attempts: 3, initial: 10, max: 200 });

const rerunnable: readonly ErrorClass[] = ["SerializationFailure", "Deadlock"];

/** @internal retryable reports whether a rerun is expected to succeed after e: only serialization failures and deadlocks (§22.4). */
export function retryable(e: unknown): boolean {
  return rerunnable.includes(classOf(e) as ErrorClass);
}

/** @internal backoff returns the delay before attempt n+1, after n runs: a uniform draw from [0, min(initial·2^(n-1), max)) (guide §5.7). */
export function backoff(p: RetryPolicy, n: number, random: () => number): number {
  let d = p.initial * 2 ** (n - 1);
  if (d > p.max || d <= 0) {
    d = p.max;
  }
  return random() * d;
}

/** @internal fits reports whether ms from now ends by the context's deadline, if any. */
export function fits(ms: number): boolean {
  const deadline = ambient().deadline;
  return deadline === undefined || Date.now() + ms <= deadline;
}

/** @internal wait sleeps ms unless the context would end first, reporting whether it slept. The rerun stops when the next backoff would pass the context's deadline (§22.4). */
export function wait(ms: number): Promise<boolean> {
  const signal = ambient().signal;
  if (!fits(ms) || signal?.aborted) {
    return Promise.resolve(false);
  }
  return new Promise((resolve) => {
    const done = (slept: boolean) => {
      clearTimeout(timer);
      signal?.removeEventListener("abort", aborted);
      resolve(slept);
    };
    const aborted = () => done(false);
    const timer = setTimeout(() => done(true), ms);
    signal?.addEventListener("abort", aborted, { once: true });
  });
}

/**
 * Retried is the error of a closure that ran more than once: its cause is
 * the last run's error, and attempts how many runs there were (§22.4). Its
 * class is the cause's.
 */
export class Retried extends Error {
  constructor(
    readonly attempts: number,
    cause: unknown,
  ) {
    super(`${cause instanceof Error ? cause.message : String(cause)} (after ${attempts} attempts)`, { cause });
    this.name = "Retried";
  }
}

/** @internal retried returns the error of a closure after attempt runs. */
export function retried(e: unknown, attempt: number): unknown {
  return attempt > 1 ? new Retried(attempt, e) : e;
}

/** Retrier reruns a function under a policy; retry makes one. */
export class Retrier {
  constructor(
    private readonly policy: RetryPolicy,
    private readonly random: () => number,
  ) {}

  /**
   * do runs fn, and reruns it while it fails with an error of a class the
   * policy's on lists, up to its attempts, with its backoff. It stops when
   * the next backoff would pass the context's deadline, or the context's
   * signal aborts while it waits, and throws the last error, wrapped in
   * Retried after more than one run.
   *
   * Inside a transaction do runs fn once: a rerun inside a transaction that
   * failed cannot succeed, so only the root's closure reruns (§22.4).
   */
  async do<T>(fn: () => Promise<T>): Promise<T> {
    const on = this.policy.on ?? rerunnable;
    for (let attempt = 1; ; attempt++) {
      try {
        return await fn();
      } catch (e) {
        if (!on.includes(classOf(e) as ErrorClass) || attempt >= this.policy.attempts || ambient().frame !== undefined) {
          throw retried(e, attempt);
        }
        if (!(await wait(backoff(this.policy, attempt, this.random)))) {
          throw retried(e, attempt);
        }
      }
    }
  }
}

/**
 * retry returns a Retrier for code that wants failures retried that
 * transaction closures do not rerun, such as lock timeouts or connection
 * errors (§22.4). A policy without attempts takes DEFAULT_RETRY's attempts
 * and backoff.
 */
export function retry(policy: Partial<RetryPolicy> = {}, random: () => number = Math.random): Retrier {
  const p: RetryPolicy = policy.attempts ? { initial: 0, max: 0, ...policy, attempts: policy.attempts } : { ...policy, ...DEFAULT_RETRY };
  return new Retrier(p, random);
}
