// Turning dql's host values into what MySQL reads. A decimal, a date or a
// time is sent as its text, which MySQL reads as the parameter's type; an
// instant as its UTC date and time, the session's time zone; an interval as a
// TIME's text; a bit string as the unsigned integer a BIT holds; and a list,
// which has no parameter type, as one JSON array the dialect expands with
// JSON_TABLE (§25.3).

import { Temporal } from "@dql/runtime";

/** Value is a parameter as mysql2 sends it. */
export type Value = string | number | bigint | boolean | Buffer | null;

/** MonthsError is the error for an interval with years, months or weeks, which a MySQL TIME cannot hold. */
export class MonthsError extends Error {
  constructor() {
    super("dqlmysql: an interval with months has no TIME value");
    this.name = "MonthsError";
  }
}

/** utc writes an instant as MySQL reads a DATETIME or TIMESTAMP in UTC: 2024-01-02 03:04:05.123456. */
function utc(i: Temporal.Instant): string {
  return i.toZonedDateTimeISO("UTC").toPlainDateTime().toString({ smallestUnit: "microsecond" }).replace("T", " ");
}

/** timeText writes an interval as a MySQL TIME, [-]H:MM:SS.ffffff, its days counted as 24 hours. */
export function timeText(d: Temporal.Duration): string {
  if (d.years !== 0 || d.months !== 0 || d.weeks !== 0) {
    throw new MonthsError();
  }
  let us =
    BigInt(d.days) * 86_400_000_000n +
    BigInt(d.hours) * 3_600_000_000n +
    BigInt(d.minutes) * 60_000_000n +
    BigInt(d.seconds) * 1_000_000n +
    BigInt(d.milliseconds) * 1000n +
    BigInt(d.microseconds) +
    BigInt(d.nanoseconds) / 1000n;
  const sign = us < 0n ? "-" : "";
  us = us < 0n ? -us : us;
  const two = (n: bigint) => String(n).padStart(2, "0");
  return `${sign}${us / 3_600_000_000n}:${two((us / 60_000_000n) % 60n)}:${two((us / 1_000_000n) % 60n)}.${String(us % 1_000_000n).padStart(6, "0")}`;
}

/** bitsValue returns a bit string, such as 0101, as the unsigned integer a BIT holds, its first bit most significant. */
export function bitsValue(s: string): bigint {
  if (!/^[01]*$/.test(s) || s.length > 64) {
    throw new RangeError(`dqlmysql: ${JSON.stringify(s)} is not a bit string of at most 64 bits`);
  }
  return s === "" ? 0n : BigInt(`0b${s}`);
}

/** isJsonType reports whether a database type, such as json or shop.json, holds JSON documents. */
const isJsonType = (type: string) => /(?:^|\.)json$/i.test(type);

/** isBitType reports whether a database type, such as bit or bit(3), is BIT. */
const isBitType = (type: string) => /(?:^|\.)bit(?:\(\d+\))?$/i.test(type);

/**
 * encode writes one parameter. type is its declared database type, such as
 * json or bit(3), or "" where it is not known, as for raw SQL: a value of a
 * JSON type is written as JSON whatever its JavaScript type, and a bit
 * string of a BIT type as its integer.
 */
export function encode(v: unknown, type: string): Value {
  if (v === null || v === undefined) {
    return null;
  }
  if (isJsonType(type)) {
    return JSON.stringify(v);
  }
  if (typeof v === "string" && isBitType(type)) {
    return bitsValue(v);
  }
  if (Array.isArray(v)) {
    return `[${v.map(item).join(",")}]`;
  }
  return scalar(v);
}

/** scalar writes a value other than a list. */
function scalar(v: unknown): Value {
  switch (typeof v) {
    case "string":
    case "number":
    case "bigint":
    case "boolean":
      return v;
  }
  if (v instanceof Uint8Array) {
    return Buffer.from(v.buffer, v.byteOffset, v.byteLength);
  }
  if (v instanceof Date) {
    return utc(Temporal.Instant.fromEpochMilliseconds(v.getTime()));
  }
  if (v instanceof Temporal.Instant) {
    return utc(v);
  }
  if (v instanceof Temporal.ZonedDateTime) {
    return utc(v.toInstant());
  }
  if (v instanceof Temporal.PlainDateTime) {
    return v.toString({ smallestUnit: "microsecond" }).replace("T", " ");
  }
  if (v instanceof Temporal.PlainTime) {
    return v.toString({ smallestUnit: "microsecond" });
  }
  if (v instanceof Temporal.Duration) {
    return timeText(v);
  }
  if (typeof v === "object" && v !== null && v.constructor !== Object && typeof v.toString === "function" && v.toString !== Object.prototype.toString) {
    // Decimal, OffsetTime and Temporal.PlainDate write what MySQL reads.
    return v.toString();
  }
  // A JSON value where the type is not known.
  return JSON.stringify(v);
}

/** item writes one item of a list as JSON: each is encoded as a parameter would be, and a value sent as text, bytes included, is a JSON string. A plain object or array is a JSON value, written as it is. */
function item(v: unknown): string {
  if (v === null || v === undefined) {
    return "null";
  }
  if (Array.isArray(v) || (typeof v === "object" && v.constructor === Object)) {
    return JSON.stringify(v);
  }
  const x = scalar(v);
  if (typeof x === "bigint") {
    return String(x);
  }
  return JSON.stringify(Buffer.isBuffer(x) ? x.toString("utf8") : x);
}

/** encodeArgs writes a statement's parameters. */
export function encodeArgs(args: readonly unknown[], types: readonly string[] | undefined): Value[] {
  return args.map((v, i) => encode(v, types?.[i] ?? ""));
}
