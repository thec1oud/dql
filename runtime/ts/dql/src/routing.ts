// Read consistency and sessions (design record §22.5).

import { ambient, within } from "./context.js";

/** Level is how fresh a read must be. */
type Level = "primary" | "any" | "bounded" | "read-your-writes";

/**
 * Consistency is how fresh a read must be, which decides whether it may leave
 * the primary (§22.5, guide §5.6). A statement or transaction without one
 * takes the executor's configured level, whose default is primary.
 */
export class Consistency {
  private constructor(
    /** The level. */
    readonly level: Level,
    /** For bounded: how far behind, in milliseconds, a replica may be. */
    readonly bound: number,
    /** For read-your-writes: how long, in milliseconds, to wait for a replica to replay the session's writes. */
    readonly waitFor: number,
    /** For read-your-writes: fail rather than read from the primary. */
    readonly fails: boolean,
  ) {}

  /** primary reads from the primary. */
  static readonly primary = new Consistency("primary", 0, 0, false);
  /** any reads from a replica in rotation, else the primary. */
  static readonly any = new Consistency("any", 0, 0, false);
  /** readYourWrites reads from a replica that has replayed the writes the context's Session recorded on the source, else the primary. */
  static readonly readYourWrites = new Consistency("read-your-writes", 0, 0, false);

  /** bounded reads from a replica at most ms milliseconds behind, else the primary. */
  static bounded(ms: number): Consistency {
    return new Consistency("bounded", ms, 0, false);
  }

  /** wait makes a read-your-writes read wait up to ms milliseconds for a replica to replay the session's writes before it falls back. */
  wait(ms: number): Consistency {
    return new Consistency(this.level, this.bound, ms, this.fails);
  }

  /** orFail makes a read-your-writes read fail with ReplicaUnavailable, rather than read from the primary, when no replica has replayed the session's writes (after waiting, with wait). */
  orFail(): Consistency {
    return new Consistency(this.level, this.bound, this.waitFor, true);
  }
}

/**
 * Replication is how a dialect reads replica positions (§22.5), from the
 * generated description of the dialect. A dialect without it reads only from
 * primaries.
 */
export interface Replication {
  /** Runs on a primary connection after a commit and returns, as text, a position a replica that has seen the commit has replayed. */
  readonly position: string;
  /** Runs on a replica and returns how far behind it is in seconds, or NULL when the server is not a replica. */
  readonly lag: string;
  /** Runs on a replica with one argument, a list of positions, and returns whether it has replayed every one. */
  readonly replayed: string;
}

/**
 * Session is one request's record of its writes, by source, for
 * read-your-writes reads (§22.5). Its token carries them to a later request
 * (causal tokens).
 */
export class Session {
  private readonly positions = new Map<string, string[]>();

  /** @internal record adds a position of source; the empty position means none could be read. */
  record(source: string, pos: string): void {
    const ps = this.positions.get(source) ?? [];
    if (!ps.includes(pos)) {
      ps.push(pos);
      this.positions.set(source, ps);
    }
  }

  /** @internal pending returns the positions a replica of source must have replayed, and whether the session's reads of source must use the primary. */
  pending(source: string): [string[], boolean] {
    const ps = this.positions.get(source) ?? [];
    return ps.includes("") ? [[], true] : [[...ps], false];
  }

  /** token exports the session's positions, for a later request to import so that its reads see this one's writes. It is the Go runtime's token for the same positions. */
  token(): string {
    const parts: string[] = [];
    for (const source of [...this.positions.keys()].sort()) {
      for (const p of this.positions.get(source) as string[]) {
        parts.push(`${queryEscape(source)}=${queryEscape(p)}`);
      }
    }
    return parts.join("&");
  }

  /** import adds the positions of a token from token. */
  import(token: string): void {
    for (const [source, p] of new URLSearchParams(token)) {
      this.record(source, p);
    }
  }
}

/** queryEscape escapes s as Go's url.QueryEscape does, so tokens match across runtimes. */
function queryEscape(s: string): string {
  return encodeURIComponent(s)
    .replace(/[!'()*]/g, (c) => `%${c.charCodeAt(0).toString(16).toUpperCase()}`)
    .replace(/%20/g, "+");
}

/** withSession runs fn with a new Session in its context, or in the one the context carries. */
export function withSession<T>(fn: () => T): T {
  return ambient().session === undefined ? within({ session: new Session() }, fn) : fn();
}

/** currentSession returns the Session of the current context, if any. */
export function currentSession(): Session | undefined {
  return ambient().session;
}
