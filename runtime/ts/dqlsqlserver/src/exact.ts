// tedious reads a decimal or money value as a JavaScript number, which
// rounds it, and a datetimeoffset as a Date, which drops its offset. dql
// reads both exactly (§8.7, §8.8), so this module wraps tedious's value
// reader: it lets tedious read each value, which also checks the bytes and
// finds where the value ends, and then reads those bytes again exactly. It
// relies on the layout of tedious's lib/value-parser.js, so the package pins
// one tedious version and its tests read every wrapped type.

import { createRequire } from "node:module";

/** Exact is a decimal or money value as its exact text, such as -12.345. */
export class Exact {
  constructor(readonly text: string) {}
}

/** Offset is a datetimeoffset value: the instant tedious read, and the offset it dropped, in minutes east of UTC. */
export class Offset {
  constructor(
    readonly date: Date,
    readonly minutes: number,
  ) {}
}

/** Metadata is what the reader needs of a column's metadata. */
interface Metadata {
  readonly type: { readonly name: string };
  readonly scale?: number;
}

/** Result is what tedious's reader returns: the value and where the next one starts. */
interface Result {
  value: unknown;
  offset: number;
}

type Reader = (buf: Buffer, offset: number, metadata: Metadata, options: unknown) => Result;

/** decimalText writes an unsigned magnitude with scale digits after the point, signed. */
export function decimalText(negative: boolean, magnitude: bigint, scale: number): string {
  const digits = magnitude.toString().padStart(scale + 1, "0");
  const point = digits.length - scale;
  const text = scale === 0 ? digits : `${digits.slice(0, point)}.${digits.slice(point)}`;
  return negative && magnitude !== 0n ? `-${text}` : text;
}

/** money reads a money value of 4 or 8 bytes at at: an integer count of ten-thousandths. */
function money(buf: Buffer, at: number, length: number): Exact {
  const n = length === 4 ? BigInt(buf.readInt32LE(at)) : (BigInt(buf.readInt32LE(at)) << 32n) | BigInt(buf.readUInt32LE(at + 4));
  return new Exact(decimalText(n < 0n, n < 0n ? -n : n, 4));
}

/** exact reads again, exactly, the value tedious read at start as r. */
export function exact(buf: Buffer, start: number, metadata: Metadata, r: Result): Result {
  if (r.value === null) {
    return r;
  }
  switch (metadata.type.name) {
    case "DecimalN":
    case "NumericN": {
      // A length, a sign byte that is 1 for positive, then the magnitude, little-endian.
      const length = buf.readUInt8(start);
      let magnitude = 0n;
      for (let i = length; i >= 2; i--) {
        magnitude = (magnitude << 8n) | BigInt(buf.readUInt8(start + i));
      }
      r.value = new Exact(decimalText(buf.readUInt8(start + 1) !== 1, magnitude, metadata.scale ?? 0));
      break;
    }
    case "Money":
      r.value = money(buf, start, 8);
      break;
    case "SmallMoney":
      r.value = money(buf, start, 4);
      break;
    case "MoneyN":
      r.value = money(buf, start + 1, buf.readUInt8(start));
      break;
    case "DateTimeOffset":
      // A length, then the time, the date, and last the offset in minutes.
      r.value = new Offset(r.value as Date, buf.readInt16LE(start + buf.readUInt8(start) - 1));
      break;
  }
  return r;
}

/** install wraps the reader of the tedious that require loads, once. */
export function install(require: NodeJS.Require = createRequire(import.meta.url)): void {
  const parser = require("tedious/lib/value-parser.js") as { readValue: Reader & { dql?: true } };
  if (parser.readValue.dql === true) {
    return;
  }
  const read = parser.readValue;
  const wrapped: Reader & { dql?: true } = (buf, offset, metadata, options) => exact(buf, offset, metadata, read(buf, offset, metadata, options));
  wrapped.dql = true;
  parser.readValue = wrapped;
}
