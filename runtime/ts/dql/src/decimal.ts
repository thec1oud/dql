// Exact decimal numbers (design record §8.1, §8.7).
//
// A Decimal is a bigint coefficient times a power of ten. It never passes
// through binary floating point (§8.7), and it keeps its scale: 1.50 and 1.5
// are equal numbers with different text, as in a database column of scale 2.
//
// A Decimal is always finite. Postgres numeric NaN and ±Infinity have no
// Decimal: decoding one is a DecodeError with reason off_type (§13), as in
// every emitter.

/**
 * MAX_PARSE_EXPONENT bounds the exponent parsing accepts, in both directions.
 * It is beyond the range of every database's decimal type (Postgres numeric
 * holds 131072 digits before the point and 16383 after), and it keeps
 * untrusted text from forcing huge allocations.
 */
export const MAX_PARSE_EXPONENT = 1 << 18;

const MAX_EXPONENT = 2 ** 31 - 1;
const MIN_EXPONENT = -(2 ** 31);

const PATTERN = /^([+-]?)(\d*)(?:\.(\d*))?(?:[eE]([+-]?\d+))?$/;

/**
 * Decimal is coefficient × 10^exponent. Values are immutable; every operation
 * returns a new Decimal. Compare values with cmp or equals: === compares
 * objects, not numbers.
 */
export class Decimal {
  /** The coefficient. */
  readonly coefficient: bigint;
  /** The exponent, a 32-bit signed integer. */
  readonly exponent: number;

  /** Returns coefficient × 10^exponent. Throws a RangeError when the exponent is not a 32-bit integer. */
  constructor(coefficient: bigint, exponent = 0) {
    this.coefficient = coefficient;
    this.exponent = checkExponent(exponent);
  }

  /** The decimal 0. */
  static readonly ZERO = new Decimal(0n);

  /**
   * parse parses a decimal in plain or exponent notation: an optional sign,
   * digits with an optional point (at least one digit on either side), and an
   * optional exponent, such as -12.50, .5, 5. or 1.2e-3. The scale is kept:
   * 1.50 has exponent -2. It throws a SyntaxError on anything else, NaN and
   * Infinity included.
   */
  static parse(s: string): Decimal {
    const d = Decimal.tryParse(s);
    if (d === undefined) {
      throw new SyntaxError(`dql: invalid decimal ${JSON.stringify(s)}`);
    }
    return d;
  }

  /** tryParse is parse that returns undefined instead of throwing. */
  static tryParse(s: string): Decimal | undefined {
    const m = PATTERN.exec(s);
    if (m === null) {
      return undefined;
    }
    const [, sign, int = "", frac = "", exp = "0"] = m;
    const digits = int + frac;
    if (digits === "") {
      return undefined;
    }
    const e = Number(exp) - frac.length;
    if (!(Math.abs(e) <= MAX_PARSE_EXPONENT)) {
      return undefined;
    }
    const c = BigInt(digits);
    return new Decimal(sign === "-" ? -c : c, e);
  }

  /**
   * from returns a Decimal for an integer: a bigint, or a number that is a
   * safe integer. It throws a RangeError for any other number, because a
   * fractional number has already passed through binary floating point (§8.7).
   */
  static from(v: bigint | number): Decimal {
    if (typeof v === "number" && !Number.isSafeInteger(v)) {
      throw new RangeError(`dql: ${v} is not a safe integer; parse decimal text instead`);
    }
    return new Decimal(BigInt(v));
  }

  /** sign returns -1, 0 or +1 as the value is negative, zero or positive. */
  get sign(): -1 | 0 | 1 {
    return this.coefficient < 0n ? -1 : this.coefficient > 0n ? 1 : 0;
  }

  /** isZero reports whether the value is 0, at any scale. */
  isZero(): boolean {
    return this.coefficient === 0n;
  }

  /** neg returns -d. */
  neg(): Decimal {
    return new Decimal(-this.coefficient, this.exponent);
  }

  /** abs returns |d|. */
  abs(): Decimal {
    return this.coefficient < 0n ? this.neg() : this;
  }

  /** add returns d + o, at the larger of the two scales. */
  add(o: Decimal): Decimal {
    const [a, b, e] = align(this, o);
    return new Decimal(a + b, e);
  }

  /** sub returns d - o, at the larger of the two scales. */
  sub(o: Decimal): Decimal {
    const [a, b, e] = align(this, o);
    return new Decimal(a - b, e);
  }

  /** mul returns d × o, at the sum of the two scales. */
  mul(o: Decimal): Decimal {
    return new Decimal(this.coefficient * o.coefficient, this.exponent + o.exponent);
  }

  /** cmp returns -1, 0 or +1 as d is less than, equal to or greater than o. */
  cmp(o: Decimal): -1 | 0 | 1 {
    const [a, b] = align(this, o);
    return a < b ? -1 : a > b ? 1 : 0;
  }

  /** equals reports whether d and o are the same number, at any scale. */
  equals(o: Decimal): boolean {
    return this.cmp(o) === 0;
  }

  /**
   * round returns d rounded to scale digits after the point, half away from
   * zero, as Postgres, MySQL and SQL Server round decimals. The result has
   * exactly that scale: round(1.5, 3) is 1.500. A negative scale rounds to a
   * power of ten: 1250 rounded to -2 is 1300.
   */
  round(scale: number): Decimal {
    return this.rescale(scale, true);
  }

  /** truncate returns d cut to scale digits after the point, toward zero, with exactly that scale. */
  truncate(scale: number): Decimal {
    return this.rescale(scale, false);
  }

  private rescale(scale: number, round: boolean): Decimal {
    const e = checkExponent(-scale);
    if (e <= this.exponent) {
      return new Decimal(this.coefficient * pow10(this.exponent - e), e);
    }
    const div = pow10(e - this.exponent);
    let q = this.coefficient / div;
    const r = this.coefficient % div;
    if (round && 2n * (r < 0n ? -r : r) >= div) {
      q += BigInt(this.sign);
    }
    return new Decimal(q, e);
  }

  /**
   * fits reports whether d fits decimal(precision, scale): whether, rounded to
   * scale as the database rounds it, it has at most precision significant
   * digits. A database accepts exactly such values, so this is the digits
   * constraint of §10.2.
   */
  fits(precision: number, scale: number): boolean {
    const c = this.round(scale).coefficient;
    return (c < 0n ? -c : c) < pow10(precision);
  }

  /**
   * toString returns d in plain notation, never with an exponent: its scale
   * digits after the point when the exponent is negative, and zeros appended
   * when it is positive.
   */
  toString(): string {
    const neg = this.coefficient < 0n;
    let digits = (neg ? -this.coefficient : this.coefficient).toString();
    let s: string;
    if (this.exponent >= 0) {
      s = this.coefficient === 0n ? digits : digits + "0".repeat(this.exponent);
    } else {
      const scale = -this.exponent;
      if (digits.length <= scale) {
        digits = "0".repeat(scale - digits.length + 1) + digits;
      }
      const point = digits.length - scale;
      s = `${digits.slice(0, point)}.${digits.slice(point)}`;
    }
    return neg ? `-${s}` : s;
  }

  /** toJSON writes d as a JSON string, which every reader takes without losing digits. */
  toJSON(): string {
    return this.toString();
  }
}

function checkExponent(e: number): number {
  if (!Number.isInteger(e) || e < MIN_EXPONENT || e > MAX_EXPONENT) {
    throw new RangeError(`dql: decimal exponent ${e} out of range`);
  }
  return e;
}

function pow10(n: number): bigint {
  return 10n ** BigInt(n);
}

/** align returns the coefficients of d and o at their smaller exponent. */
function align(d: Decimal, o: Decimal): [bigint, bigint, number] {
  if (d.exponent > o.exponent) {
    return [d.coefficient * pow10(d.exponent - o.exponent), o.coefficient, o.exponent];
  }
  return [d.coefficient, o.coefficient * pow10(o.exponent - d.exponent), d.exponent];
}
