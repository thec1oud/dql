// The error taxonomy of design record §23, with typed constraint errors
// (§23.2) and redaction (§23.3). Messages are the Go runtime's, word for word,
// so hooks and conformance see the same text from every emitter.

/** ErrorClass names an error's place in the taxonomy of §23.1, as hooks report it and conformance scenarios match it. */
export type ErrorClass =
  // Cardinality
  | "NotFound"
  | "TooManyRows"
  // Constraints
  | "UniqueViolation"
  | "ForeignKeyViolation"
  | "CheckViolation"
  | "NotNullViolation"
  | "ExclusionViolation"
  // Concurrency
  | "SerializationFailure"
  | "Deadlock"
  | "LockTimeout"
  | "ConcurrentModification"
  // Client-side
  | "ValidationErrors"
  | "DecodeError"
  // Infrastructure
  | "ConnectionError"
  | "PoolExhausted"
  | "Timeout"
  | "Canceled"
  | "ReplicaUnavailable"
  // Fallback
  | "DatabaseError";

/**
 * Origin says where an error arose (§23.1): the dialect, the database's
 * native code where there is one, such as a SQLSTATE, and the name of the
 * query or operation.
 */
export interface Origin {
  readonly dialect?: string;
  readonly code?: string;
  readonly query?: string;
}

function suffix(o: Origin): string {
  let s = o.query ? ` in ${o.query}` : "";
  const where = [o.dialect, o.code].filter(Boolean).join(" ");
  if (where !== "") {
    s += ` (${where})`;
  }
  return s;
}

/** DqlError is the base of every error of the taxonomy. */
export abstract class DqlError extends Error {
  /** The error's class in the taxonomy. */
  readonly class: ErrorClass;
  /** Where the error arose. */
  readonly origin: Origin;

  protected constructor(cls: ErrorClass, what: string, origin: Origin, cause?: unknown) {
    super(`dql: ${cls}${what === "" ? "" : ` ${what}`}${suffix(origin)}${cause === undefined ? "" : `: ${causeText(cause)}`}`, cause === undefined ? undefined : { cause });
    this.class = cls;
    this.origin = origin;
    this.name = cls;
  }
}

function causeText(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause);
}

/** classOf returns the class of the first DqlError in e's cause chain, or undefined when there is none. */
export function classOf(e: unknown): ErrorClass | undefined {
  return find(e, (x): x is DqlError => x instanceof DqlError)?.class;
}

/** find walks e and its causes, returning the first that matches. */
function find<T>(e: unknown, is: (x: unknown) => x is T): T | undefined {
  const seen = new Set<unknown>();
  for (let x = e; x !== undefined && x !== null && !seen.has(x); x = (x as { cause?: unknown }).cause) {
    if (is(x)) {
      return x;
    }
    seen.add(x);
  }
  return undefined;
}

/** NotFound reports that an operation that needs a row found none (§19). */
export class NotFound extends DqlError {
  constructor(origin: Origin = {}) {
    super("NotFound", "", origin);
  }
}

/** TooManyRows reports that an operation that allows at most one row found more (§19). */
export class TooManyRows extends DqlError {
  constructor(origin: Origin = {}) {
    super("TooManyRows", "", origin);
  }
}

/** SerializationFailure reports that the database aborted a transaction to keep it serializable. */
export class SerializationFailure extends DqlError {
  constructor(origin: Origin = {}) {
    super("SerializationFailure", "", origin);
  }
}

/** Deadlock reports that the database aborted a transaction to break a deadlock. */
export class Deadlock extends DqlError {
  constructor(origin: Origin = {}) {
    super("Deadlock", "", origin);
  }
}

/** LockTimeout reports that a lock was not granted in time. */
export class LockTimeout extends DqlError {
  constructor(origin: Origin = {}) {
    super("LockTimeout", "", origin);
  }
}

/** ConcurrentModification reports that a versioned write found its row at another version (§20.4). */
export class ConcurrentModification extends DqlError {
  constructor(origin: Origin = {}) {
    super("ConcurrentModification", "", origin);
  }
}

function onSource(source: string): string {
  return source === "" ? "" : `on source ${source}`;
}

/** PoolExhausted reports that no connection of a source's pool became free in time (§22.6). */
export class PoolExhausted extends DqlError {
  constructor(
    readonly source: string,
    origin: Origin = {},
  ) {
    super("PoolExhausted", onSource(source), origin);
  }
}

/** ReplicaUnavailable reports that no replica could serve a read at the consistency level it asked for (§22.5). */
export class ReplicaUnavailable extends DqlError {
  constructor(
    readonly source: string,
    origin: Origin = {},
  ) {
    super("ReplicaUnavailable", onSource(source), origin);
  }
}

/** ConnectionError reports that a connection to a source failed. Its cause is the driver's error. */
export class ConnectionError extends DqlError {
  constructor(
    readonly source: string,
    cause?: unknown,
    origin: Origin = {},
  ) {
    super("ConnectionError", onSource(source), origin, cause);
  }
}

/** Timeout reports that an operation ran out of time. Its cause is the abort reason, where there is one. */
export class Timeout extends DqlError {
  constructor(cause?: unknown, origin: Origin = {}) {
    super("Timeout", "", origin, cause);
  }
}

/** Canceled reports that an operation was canceled. Its cause is the abort reason, where there is one. */
export class Canceled extends DqlError {
  constructor(cause?: unknown, origin: Origin = {}) {
    super("Canceled", "", origin, cause);
  }
}

/**
 * DatabaseError is any database error no other class covers. databaseMessage
 * is the database's own message and detail its detail: either may quote
 * values, so neither is part of the error's message, and the executor clears
 * both when a sensitive column is involved (§23.3).
 */
export class DatabaseError extends DqlError {
  constructor(
    readonly databaseMessage = "",
    readonly detail = "",
    origin: Origin = {},
  ) {
    super("DatabaseError", "", origin);
  }
}

/** ConstraintKind is the kind of a catalog constraint. */
export type ConstraintKind = "primary_key" | "unique" | "foreign_key" | "check" | "not_null" | "exclusion";

/**
 * Constraint is a catalog constraint as a generated value (§23.2), so code
 * branches on which rule failed without matching strings. name is its
 * qualified name, such as shop.app.users.users_tenant_email_key. sensitive is
 * true when any column it involves is sensitive, so the database's detail is
 * dropped (§23.3).
 */
export interface Constraint {
  readonly name: string;
  readonly kind: ConstraintKind;
  readonly sensitive: boolean;
}

/**
 * Violation is what every constraint violation carries: the constraint, and
 * the database's own detail, such as Postgres's "Key (email)=(a@x.com) already
 * exists". The detail may quote values, so it is never part of the message,
 * and it is empty when the constraint is sensitive (§23.3).
 */
export abstract class Violation extends DqlError {
  protected constructor(
    cls: ErrorClass,
    readonly constraint: Constraint,
    readonly detail: string,
    origin: Origin,
    what = `of ${constraint.name}`,
  ) {
    super(cls, what, origin);
  }
}

/** UniqueViolation reports a violated primary key or unique constraint. */
export class UniqueViolation extends Violation {
  constructor(constraint: Constraint, detail = "", origin: Origin = {}) {
    super("UniqueViolation", constraint, detail, origin);
  }
}

/** ForeignKeyViolation reports a violated foreign key. */
export class ForeignKeyViolation extends Violation {
  constructor(constraint: Constraint, detail = "", origin: Origin = {}) {
    super("ForeignKeyViolation", constraint, detail, origin);
  }
}

/** CheckViolation reports a violated check constraint. */
export class CheckViolation extends Violation {
  constructor(constraint: Constraint, detail = "", origin: Origin = {}) {
    super("CheckViolation", constraint, detail, origin);
  }
}

/** ExclusionViolation reports a violated exclusion constraint (Postgres). */
export class ExclusionViolation extends Violation {
  constructor(constraint: Constraint, detail = "", origin: Origin = {}) {
    super("ExclusionViolation", constraint, detail, origin);
  }
}

/**
 * NotNullViolation reports a NULL in a NOT NULL column. column is the
 * column's qualified name; the constraint is named where the dialect names
 * not-null constraints, and has an empty name otherwise.
 */
export class NotNullViolation extends Violation {
  constructor(
    readonly column: string,
    constraint: Constraint = { name: "", kind: "not_null", sensitive: false },
    detail = "",
    origin: Origin = {},
  ) {
    super("NotNullViolation", constraint, detail, origin, constraint.name === "" ? `in ${column}` : `of ${constraint.name}`);
  }
}

/** isViolation reports whether e's cause chain holds a violation of c (§23.2). */
export function isViolation(e: unknown, c: Constraint): boolean {
  return find(e, (x): x is Violation => x instanceof Violation && x.constraint.name === c.name) !== undefined;
}

/** DecodeReason is why a value could not be decoded (§13). */
export type DecodeReason =
  /** NULL in a field declared or asserted non-null. */
  | "null_into_non_null"
  /** A NULL element in an array asserted array-elements-nonnull. */
  | "null_element_into_non_null"
  /** An enum label added after generation. */
  | "unknown_enum_label"
  /** An array whose dimensions differ from the declaration. */
  | "dimension_mismatch"
  /** An off-type value in a strict field. */
  | "off_type";

/** The fields of a DecodeError (§13). */
export interface DecodeFailure {
  /** The shape decoded, such as published_post.full.flat. */
  readonly shape: string;
  /** The field's path, as in ValidationIssue; empty for a value decoded on its own. */
  readonly field: string;
  readonly reason: DecodeReason;
  /** Where an asserted fact came from; empty for facts the database proves. */
  readonly assertion: string;
}

/**
 * DecodeError reports a row value that breaks what its shape declares (§13).
 * Decoding never produces a silent zero value: the worst case is this error.
 * It never holds the value itself.
 */
export class DecodeError extends DqlError implements DecodeFailure {
  readonly shape: string;
  readonly field: string;
  readonly reason: DecodeReason;
  readonly assertion: string;

  constructor(failure: { shape: string; field?: string; reason: DecodeReason; assertion?: string }, origin: Origin = {}) {
    const { shape, field = "", reason, assertion = "" } = failure;
    super(
      "DecodeError",
      `${shape}${field === "" ? "" : ` field ${field}`}: ${reason}${assertion === "" ? "" : `, asserted by ${assertion}`}`,
      origin,
    );
    this.shape = shape;
    this.field = field;
    this.reason = reason;
    this.assertion = assertion;
  }
}
