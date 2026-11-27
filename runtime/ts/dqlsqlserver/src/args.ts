// Turning dql's host values into tedious parameters. Integers, booleans,
// floats, text and bytes keep their types; a decimal, a date or time, an
// instant and a UUID are sent as their text, which SQL Server converts to
// the type it compares them with or stores them as; and a list, which has no
// parameter type, is one JSON array the dialect expands with OPENJSON
// (§25.3). SQL Server has no interval or time-with-offset type.

import { Decimal, OffsetTime, Temporal } from "@dql/runtime";
import { TYPES } from "tedious";

/** Param is one parameter as tedious sends it: its type and value. */
export interface Param {
  readonly type: (typeof TYPES)[keyof typeof TYPES];
  readonly value: unknown;
}

/** NoTypeError is the error for a value SQL Server has no type for: an interval or a time with an offset. */
export class NoTypeError extends Error {
  constructor(what: string) {
    super(`dqlsqlserver: SQL Server has no type for ${what}`);
    this.name = "NoTypeError";
  }
}

/** INT32 bounds the integers sent as int; larger ones are bigint. */
const INT32 = 2 ** 31;

/** isLegacy reports whether a database type is datetime or smalldatetime, which read at most three digits of a second. */
const isLegacy = (type: string) => /^(?:small)?datetime$/i.test(type);

/** text writes a value SQL Server reads from text, to the 100 ns it keeps, or to the millisecond for a datetime. */
function text(v: unknown, type: string): string | undefined {
  const step = isLegacy(type) ? ({ smallestUnit: "millisecond" } as const) : ({ smallestUnit: "nanosecond", roundingIncrement: 100 } as const);
  if (v instanceof Temporal.PlainDateTime || v instanceof Temporal.PlainTime) {
    return v.round(step).toString();
  }
  if (v instanceof Temporal.PlainDate || v instanceof Decimal) {
    return v.toString();
  }
  if (v instanceof Temporal.Instant) {
    return v.round(step).toString({ timeZone: "UTC" });
  }
  if (v instanceof Temporal.ZonedDateTime) {
    return v.round(step).toString({ timeZoneName: "never", calendarName: "never" });
  }
  if (v instanceof Date) {
    return v.toISOString();
  }
  return undefined;
}

/** scalar writes a value other than a list or JSON. */
function scalar(v: unknown, type: string): Param {
  switch (typeof v) {
    case "string":
      return { type: TYPES.NVarChar, value: v };
    case "boolean":
      return { type: TYPES.Bit, value: v };
    case "bigint":
      return { type: TYPES.BigInt, value: v };
    case "number":
      if (Number.isInteger(v) && v >= -INT32 && v < INT32) {
        return { type: TYPES.Int, value: v };
      }
      return Number.isSafeInteger(v) ? { type: TYPES.BigInt, value: v } : { type: TYPES.Float, value: v };
  }
  if (v instanceof Uint8Array) {
    return { type: TYPES.VarBinary, value: Buffer.from(v.buffer, v.byteOffset, v.byteLength) };
  }
  if (v instanceof Temporal.Duration) {
    throw new NoTypeError("an interval");
  }
  if (v instanceof OffsetTime) {
    throw new NoTypeError("a time with an offset");
  }
  const t = text(v, type);
  // Anything else is a JSON value.
  return { type: TYPES.NVarChar, value: t ?? JSON.stringify(v) };
}

/** item writes one item of a list as JSON: text as a JSON string, a number, bigint or boolean as itself, and a JSON value as it is. */
function item(v: unknown): string {
  if (v === null || v === undefined) {
    return "null";
  }
  if (Array.isArray(v) || (typeof v === "object" && v.constructor === Object)) {
    return JSON.stringify(v);
  }
  const p = scalar(v, "");
  if (typeof p.value === "bigint" || typeof p.value === "number" || typeof p.value === "boolean") {
    return String(p.value);
  }
  return JSON.stringify(Buffer.isBuffer(p.value) ? p.value.toString("base64") : p.value);
}

/** encode writes one parameter; type is its database type, or "" where it is not known. */
export function encode(v: unknown, type: string): Param {
  if (v === null || v === undefined) {
    return { type: TYPES.NVarChar, value: null };
  }
  if (Array.isArray(v)) {
    return { type: TYPES.NVarChar, value: `[${v.map(item).join(",")}]` };
  }
  return scalar(v, type);
}

/** encodeArgs writes a statement's parameters. */
export function encodeArgs(args: readonly unknown[], types: readonly string[] | undefined): Param[] {
  return args.map((v, i) => encode(v, types?.[i] ?? ""));
}
