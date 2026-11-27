// Turning the values mysql2 reads into the driver-neutral form of Row
// (@dql/runtime). The pool asks mysql2 for dates, times, big integers,
// decimals and JSON as text, in both protocols, so every value is decoded
// here by its column's type: integers come as numbers, binary strings, BIT
// and spatial values as bytes, and the rest as text.

import { Decimal, Temporal } from "@dql/runtime";

/** Column is what decoding a column's values needs of mysql2's field. */
export interface Column {
  readonly columnType: number;
  readonly characterSet: number;
  /** MariaDB's extended metadata: json for a JSON column, which it sends as LONGTEXT. */
  readonly extendedFormat?: string;
}

/** The column types the protocol names. */
export const types = {
  DECIMAL: 0,
  FLOAT: 4,
  TIMESTAMP: 7,
  LONGLONG: 8,
  DATE: 10,
  TIME: 11,
  DATETIME: 12,
  YEAR: 13,
  NEWDATE: 14,
  BIT: 16,
  JSON: 245,
  NEWDECIMAL: 246,
} as const;

/** BINARY is the character set of a binary string. */
const BINARY = 63;

/** attempt returns what parse returns, or s when it throws: a value with no host type, such as a zero date, stays text, which a generated decoder rejects as off type (§13). */
function attempt(parse: (s: string) => unknown): (v: unknown) => unknown {
  return (v) => {
    try {
      return parse(v as string);
    } catch {
      return v;
    }
  };
}

/** bytes copies a Buffer into a plain Uint8Array. */
const bytes = (v: unknown) => Uint8Array.from(v as Buffer);

/** bits writes a BIT's bytes as a bit string, every bit of every byte: a BIT(3) holding b'101' is 00000101, since the protocol does not send the length; the generated decoder cuts it to the column's length (fixedBits). */
const bits = (v: unknown) => [...(v as Buffer)].map((b) => b.toString(2).padStart(8, "0")).join("");

/** dateTime parses a DATETIME as MySQL writes it, 2024-01-02 03:04:05.123456. */
const dateTime = (s: string) => Temporal.PlainDateTime.from(s.replace(" ", "T"));

/** instant parses a TIMESTAMP, which the session's time zone, UTC, writes. */
const instant = (s: string) => Temporal.Instant.from(`${s.replace(" ", "T")}Z`);

/** TIME_TEXT is a TIME as MySQL writes it: [-]H:MM:SS[.ffffff], its hours past 24 for a span over a day. */
const TIME_TEXT = /^(-?)(\d+):(\d\d):(\d\d)(?:\.(\d{1,6}))?$/;

/** time parses a TIME as an interval of hours, minutes, seconds and microseconds. */
export function time(s: string): Temporal.Duration {
  const m = TIME_TEXT.exec(s);
  if (m === null) {
    throw new SyntaxError(`dqlmysql: TIME value ${JSON.stringify(s)}`);
  }
  const sign = m[1] === "-" ? -1 : 1;
  const us = Number(((m[5] ?? "") + "000000").slice(0, 6));
  return Temporal.Duration.from({
    hours: sign * Number(m[2]),
    minutes: sign * Number(m[3]),
    seconds: sign * Number(m[4]),
    milliseconds: sign * Math.trunc(us / 1000),
    microseconds: sign * (us % 1000),
  });
}

const same = (v: unknown) => v;

/** decoder returns the function that turns a non-null value of column c into its Row form. */
export function decoder(c: Column): (v: unknown) => unknown {
  if (c.extendedFormat === "json") {
    return (v) => JSON.parse(v as string);
  }
  switch (c.columnType) {
    case types.FLOAT:
      // The text protocol writes a FLOAT's shortest decimal, which parses to a different double.
      return (v) => Math.fround(v as number);
    case types.LONGLONG:
      return (v) => BigInt(v as string);
    case types.YEAR:
      // YEAR is opaque, which falls back to text (§8.3).
      return (v) => String(v);
    case types.DECIMAL:
    case types.NEWDECIMAL:
      return attempt((s) => Decimal.parse(s));
    case types.DATE:
    case types.NEWDATE:
      return attempt((s) => Temporal.PlainDate.from(s));
    case types.DATETIME:
      return attempt(dateTime);
    case types.TIMESTAMP:
      return attempt(instant);
    case types.TIME:
      return attempt(time);
    case types.JSON:
      return (v) => JSON.parse(v as string);
    case types.BIT:
      return bits;
  }
  return c.characterSet === BINARY ? (v) => (Buffer.isBuffer(v) ? bytes(v) : v) : same;
}

/** decoders returns the decoders of a result's columns. */
export function decoders(cs: readonly Column[]): ((v: unknown) => unknown)[] {
  return cs.map(decoder);
}

/** decodeRow decodes one row's values. */
export function decodeRow(ds: readonly ((v: unknown) => unknown)[], row: readonly unknown[]): unknown[] {
  return row.map((v, i) => (v === null ? null : (ds[i] as (v: unknown) => unknown)(v)));
}
