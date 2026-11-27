// Mapping a dialect's errors into the taxonomy (design record §23).

import type { Driver, NativeError } from "./driver.js";
import {
  Canceled,
  CheckViolation,
  type Constraint,
  type ConstraintKind,
  ConnectionError,
  DatabaseError,
  Deadlock,
  type ErrorClass,
  ExclusionViolation,
  ForeignKeyViolation,
  LockTimeout,
  NotNullViolation,
  type Origin,
  SerializationFailure,
  Timeout,
  UniqueViolation,
  classOf,
} from "./errors.js";
import { join } from "./validation.js";

/**
 * ErrorMap is the generated mapping from a dialect's errors into the
 * taxonomy (§23): the class of each native code that maps into it, and each
 * rule's constraint, by where the database reports its violation (§23.2).
 */
export interface ErrorMap {
  readonly codes: Readonly<Record<string, ErrorClass>>;
  readonly violations: readonly (readonly [Reported, Constraint])[];
}

/**
 * Reported is how a database reports a violation: the catalog the statement
 * ran on, the schema and the table or domain, the class, and the name the
 * error carries, such as a constraint or, for a not-null violation, a
 * column.
 */
export interface Reported {
  readonly catalog: string;
  readonly schema: string;
  readonly object: string;
  readonly class: ErrorClass;
  readonly name: string;
}

/** The kind of an unknown constraint of each violation class. */
const kinds: Partial<Record<ErrorClass, ConstraintKind>> = {
  UniqueViolation: "unique",
  ForeignKeyViolation: "foreign_key",
  CheckViolation: "check",
  NotNullViolation: "not_null",
  ExclusionViolation: "exclusion",
};

function reportedKey(r: Reported): string {
  return [r.catalog, r.schema, r.object, r.class, r.name].join("\u0000");
}

/** @internal Translator maps errors of one dialect's driver into the taxonomy. */
export class Translator {
  private readonly violations = new Map<string, Constraint>();

  constructor(
    private readonly driver: Driver,
    private readonly dialect: string,
    private readonly map: ErrorMap,
  ) {
    for (const [r, c] of map.violations) {
      this.violations.set(reportedKey(r), c);
    }
  }

  /**
   * translate maps e, from a statement of query q on catalog of the given
   * source, into the taxonomy. A statement aborted by signal is Timeout when
   * its reason is a TimeoutError and Canceled otherwise. A violation of a
   * rule the map does not know names what the database reported and drops
   * its detail.
   */
  translate(e: unknown, catalog: string, q: string, source: string, signal: AbortSignal | undefined): unknown {
    const o: Origin = { dialect: this.dialect, query: q };
    if (signal?.aborted) {
      return (signal.reason as { name?: unknown } | undefined)?.name === "TimeoutError" ? new Timeout(e, o) : new Canceled(e, o);
    }
    if (classOf(e) !== undefined) {
      return e;
    }
    const n = this.driver.native(e);
    if (n === undefined) {
      if (this.driver.broken(e)) {
        return new ConnectionError(source, e, o);
      }
      return new DatabaseError(e instanceof Error ? e.message : String(e), "", o);
    }
    const origin: Origin = { ...o, code: n.code };
    const cls = Object.hasOwn(this.map.codes, n.code) ? this.map.codes[n.code] : undefined;
    switch (cls) {
      case "UniqueViolation":
      case "ForeignKeyViolation":
      case "CheckViolation":
      case "NotNullViolation":
      case "ExclusionViolation":
        return this.violation(n, cls, origin, catalog);
      case "SerializationFailure":
        return new SerializationFailure(origin);
      case "Deadlock":
        return new Deadlock(origin);
      case "LockTimeout":
        return new LockTimeout(origin);
      case "Timeout":
        return new Timeout(e, origin);
      case "ConnectionError":
        return new ConnectionError(source, e, origin);
    }
    return new DatabaseError(n.message ?? "", n.detail ?? "", origin);
  }

  private violation(n: NativeError, cls: ErrorClass, o: Origin, catalog: string): unknown {
    const schema = n.schema ?? "";
    const object = n.table || n.dataType || "";
    const name = cls === "NotNullViolation" && !n.constraint ? (n.column ?? "") : (n.constraint ?? "");
    const known = this.violations.get(reportedKey({ catalog, schema, object, class: cls, name }));
    const c: Constraint = known ?? { name: join(join(join(catalog, schema), object), name), kind: kinds[cls] as ConstraintKind, sensitive: true };
    const detail = c.sensitive ? "" : (n.detail ?? "");
    switch (cls) {
      case "UniqueViolation":
        return new UniqueViolation(c, detail, o);
      case "ForeignKeyViolation":
        return new ForeignKeyViolation(c, detail, o);
      case "CheckViolation":
        return new CheckViolation(c, detail, o);
      case "ExclusionViolation":
        return new ExclusionViolation(c, detail, o);
    }
    // The database names the column, not a constraint.
    const constraint = n.constraint ? c : { ...c, name: "" };
    return new NotNullViolation(join(join(join(catalog, schema), object), n.column ?? ""), constraint, detail, o);
  }
}
