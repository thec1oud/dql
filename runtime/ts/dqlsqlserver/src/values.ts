// Turning the values tedious reads into the driver-neutral form of Row
// (@dql/runtime), by each column's type. tedious reads integers of up to 32
// bits as numbers and bigint as text, the date and time types as Dates in
// UTC with the digits past a millisecond aside, and uniqueidentifier as
// upper-case text; exact.ts reads decimals, money and datetimeoffset.

import { Decimal, Temporal } from "@dql/runtime";
import { Exact, Offset } from "./exact.js";

/** Column is what decoding a column's values needs of tedious's metadata. */
export interface Column {
  readonly type: { readonly name: string };
}

/** TimeValue is a Date tedious read, with the fraction of a second past its milliseconds. */
type TimeValue = Date & { readonly nanosecondsDelta?: number };

/** nanos returns the nanoseconds past the second of a Date tedious read. */
function nanos(d: TimeValue): number {
  return d.getUTCMilliseconds() * 1_000_000 + Math.round((d.nanosecondsDelta ?? 0) * 1e9);
}

/** plainTime is the time of day of a Date tedious read, in UTC. */
function plainTime(d: TimeValue): Temporal.PlainTime {
  const n = nanos(d);
  return new Temporal.PlainTime(d.getUTCHours(), d.getUTCMinutes(), d.getUTCSeconds(), Math.trunc(n / 1_000_000), Math.trunc(n / 1000) % 1000, n % 1000);
}

/** plainDate is the date of a Date tedious read, in UTC. */
const plainDate = (d: Date) => new Temporal.PlainDate(d.getUTCFullYear(), d.getUTCMonth() + 1, d.getUTCDate());

/** offsetText writes an offset in minutes as ±HH:MM. */
function offsetText(minutes: number): string {
  const m = Math.abs(minutes);
  return `${minutes < 0 ? "-" : "+"}${String(Math.trunc(m / 60)).padStart(2, "0")}:${String(m % 60).padStart(2, "0")}`;
}

/** zoned is a datetimeoffset as an instant in its offset. */
function zoned(o: Offset): Temporal.ZonedDateTime {
  const d = o.date as TimeValue;
  const ns = BigInt(d.getTime() - d.getUTCMilliseconds()) * 1_000_000n + BigInt(nanos(d));
  return new Temporal.ZonedDateTime(ns, offsetText(o.minutes));
}

const same = (v: unknown) => v;

/** decoder returns the function that turns a non-null value of column c into its Row form. */
export function decoder(c: Column): (v: unknown) => unknown {
  switch (c.type.name) {
    case "BigInt":
    case "IntN":
      // An 8-byte integer comes as text, a smaller one as a number.
      return (v) => (typeof v === "string" ? BigInt(v) : v);
    case "DecimalN":
    case "NumericN":
    case "Money":
    case "MoneyN":
    case "SmallMoney":
      return (v) => Decimal.parse((v as Exact).text);
    case "DateTimeOffset":
      return (v) => zoned(v as Offset);
    case "Date":
    case "DateN":
      return (v) => plainDate(v as Date);
    case "Time":
    case "TimeN":
      return (v) => plainTime(v as TimeValue);
    case "DateTime2":
    case "DateTime2N":
    case "DateTime":
    case "DateTimeN":
    case "SmallDateTime":
      return (v) => plainDate(v as Date).toPlainDateTime(plainTime(v as TimeValue));
    case "UniqueIdentifier":
      return (v) => (v as string).toLowerCase();
    case "VarBinary":
    case "BigVarBin":
    case "Binary":
    case "BigBinary":
    case "Image":
      return (v) => Uint8Array.from(v as Buffer);
  }
  return same;
}

/** decoders returns the decoders of a result's columns. */
export function decoders(cs: readonly Column[]): ((v: unknown) => unknown)[] {
  return cs.map(decoder);
}

/** decodeRow decodes one row's values. */
export function decodeRow(ds: readonly ((v: unknown) => unknown)[], row: readonly unknown[]): unknown[] {
  return row.map((v, i) => (v === null ? null : (ds[i] as (v: unknown) => unknown)(v)));
}
