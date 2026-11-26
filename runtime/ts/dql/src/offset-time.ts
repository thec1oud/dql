import { Temporal } from "./temporal.js";

// A time of day and an offset, such as a Postgres timetz (design record
// §8.8). Temporal has no such type, so the runtime supplies it.

/** MAX_OFFSET bounds an offset's magnitude in seconds, as RFC 9557 and Temporal do. */
const MAX_OFFSET = 24 * 60 * 60 - 1;

const TIME = /^(\d{2}):(\d{2})(?::(\d{2})(?:\.(\d{1,9}))?)?/;
const OFFSET = /^(?:[Zz]|([+-])(\d{2}):(\d{2})(?::(\d{2}))?)$/;

/** OffsetTime is a time of day and a UTC offset in seconds east of UTC. */
export class OffsetTime {
  /**
   * Returns time at offset seconds east of UTC. It throws a RangeError when
   * the offset is not an integer of magnitude below 24 hours.
   */
  constructor(
    readonly time: Temporal.PlainTime,
    readonly offset: number,
  ) {
    if (!Number.isInteger(offset) || Math.abs(offset) > MAX_OFFSET) {
      throw new RangeError(`dql: offset ${offset} out of range`);
    }
  }

  /**
   * parse parses a time of HH:MM, HH:MM:SS or HH:MM:SS with a fraction of one
   * to nine digits, followed by an offset: Z, ±HH:MM or ±HH:MM:SS. It throws a
   * SyntaxError on anything else.
   */
  static parse(s: string): OffsetTime {
    const t = TIME.exec(s);
    const o = t === null ? null : OFFSET.exec(s.slice(t[0].length));
    if (t === null || o === null) {
      throw new SyntaxError(`dql: invalid offset time ${JSON.stringify(s)}`);
    }
    const [, h, m, sec = "0", frac = ""] = t;
    const ns = frac.padEnd(9, "0");
    const [, sign, oh = "0", om = "0", os = "0"] = o;
    if (Number(h) > 23 || Number(m) > 59 || Number(sec) > 59 || Number(om) > 59 || Number(os) > 59) {
      throw new SyntaxError(`dql: invalid offset time ${JSON.stringify(s)}`);
    }
    const time = new Temporal.PlainTime(
      Number(h),
      Number(m),
      Number(sec),
      Number(ns.slice(0, 3)),
      Number(ns.slice(3, 6)),
      Number(ns.slice(6)),
    );
    const offset = (Number(oh) * 3600 + Number(om) * 60 + Number(os)) * (sign === "-" ? -1 : 1);
    try {
      return new OffsetTime(time, offset);
    } catch {
      throw new SyntaxError(`dql: invalid offset time ${JSON.stringify(s)}`);
    }
  }

  /** equals reports whether o has the same time and the same offset. */
  equals(o: OffsetTime): boolean {
    return this.offset === o.offset && this.time.equals(o.time);
  }

  /** toString returns HH:MM:SS±HH:MM, with a fraction when the time has one and :SS when the offset has seconds. */
  toString(): string {
    let off = this.offset;
    const sign = off < 0 ? "-" : "+";
    off = Math.abs(off);
    const two = (n: number): string => String(n).padStart(2, "0");
    const secs = off % 60 === 0 ? "" : `:${two(off % 60)}`;
    return `${this.time.toString()}${sign}${two(Math.floor(off / 3600))}:${two(Math.floor(off / 60) % 60)}${secs}`;
  }

  /** toJSON writes the value as toString does. */
  toJSON(): string {
    return this.toString();
  }
}
