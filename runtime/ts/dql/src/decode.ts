// Decoding rows into shapes under the soundness rule (design record §13).
// Generated decoders read a Row with a Decoder and the converters below.
// NULL in a non-null field, an unknown enum label, an array of other
// dimensions and an off-type value each throw a DecodeError, never a silent
// default.

import { DecodeError, type DecodeReason } from "./errors.js";
import type { JsonValue } from "./json.js";
import type { Bound, Range } from "./range.js";
import { Temporal } from "./temporal.js";
import { join } from "./validation.js";
import { type StorageValue, type Weak, weakOf, weakRaw } from "./weak.js";

/**
 * Row is one row of a shape in the driver-neutral form generated decoders
 * read: one value for each of the shape's fields in their order, then one for
 * each of its nests. The driver adapter turns what its driver returns into
 * this form, so decoders hold no dialect knowledge. A value is null for NULL,
 * and otherwise:
 *
 * - an integer as a bigint or a number, a float as a number, a bool as a
 *   boolean or as an integer 0 or 1;
 * - text, an enum label and a string-fallback opaque value as a string, and
 *   bytes and a bytes-fallback opaque value as a Uint8Array;
 * - an array as an array, one level per dimension;
 * - a composite value as a Row of its attributes in their order;
 * - a weak column's value as a StorageValue;
 * - a to-one nest as a Row, and a to-many nest as a Row[];
 * - any other value as its TypeScript type itself, such as a Decimal, a
 *   Temporal.Instant or a Range.
 */
export class Row {
  constructor(readonly values: readonly unknown[]) {}
}

/** A Converter turns one non-null value into a field's type, or throws a DecodeError. */
export type Converter<T> = (v: unknown) => T;

/** fail throws a DecodeError of the reason, for a converter; the decoder adds the shape and the field. */
function fail(reason: DecodeReason): never {
  throw new DecodeError({ shape: "", reason });
}

/**
 * Decoder decodes one Row into a shape. Each method throws the first value
 * that breaks the shape, as a DecodeError with the shape and the field's
 * path.
 */
export class Decoder {
  private readonly values: readonly unknown[];

  /**
   * Decoder starts decoding v, which must be a Row of the given number of
   * values, into the named shape. A composite value is decoded with an
   * empty shape, which the field that holds it fills in.
   */
  constructor(
    private readonly shape: string,
    v: unknown,
    values: number,
  ) {
    if (!(v instanceof Row) || v.values.length !== values) {
      throw new DecodeError({ shape, reason: "off_type" });
    }
    this.values = v.values;
  }

  /** rethrow throws e, a DecodeError of a converter or a nested decoder, under the field's path. */
  private rethrow(field: string, e: unknown): never {
    if (!(e instanceof DecodeError)) {
      throw e;
    }
    throw new DecodeError({ shape: e.shape === "" ? this.shape : e.shape, field: join(field, e.field), reason: e.reason, assertion: e.assertion });
  }

  /** field decodes value i of a field declared or asserted non-null. NULL is a null_into_non_null error that names the assertion, if any. */
  field<T>(i: number, field: string, assertion: string, conv: Converter<T>): T {
    const v = this.values[i];
    if (v === null || v === undefined) {
      throw new DecodeError({ shape: this.shape, field, reason: "null_into_non_null", assertion });
    }
    try {
      return conv(v);
    } catch (e) {
      return this.rethrow(field, e);
    }
  }

  /** nullable decodes value i of a nullable field: null for NULL. */
  nullable<T>(i: number, field: string, conv: Converter<T>): T | null {
    const v = this.values[i];
    if (v === null || v === undefined) {
      return null;
    }
    return this.field(i, field, "", conv);
  }

  /** many decodes value i of a to-many nest, a Row[], with decode. NULL is no rows. */
  many<T>(i: number, field: string, decode: (r: Row) => T): T[] {
    const v = this.values[i];
    if (v === null || v === undefined) {
      return [];
    }
    if (!Array.isArray(v)) {
      throw new DecodeError({ shape: this.shape, field, reason: "off_type" });
    }
    return v.map((r: unknown, j) => {
      try {
        return nest(decode)(r);
      } catch (e) {
        return this.rethrow(`${field}[${j}]`, e);
      }
    });
  }
}

/** nest adapts a shape's decoder to decode a to-one nest with field or nullable. */
export function nest<T>(decode: (r: Row) => T): Converter<T> {
  return (v) => {
    if (!(v instanceof Row)) {
      return fail("off_type");
    }
    return decode(v);
  };
}

/** integer converts an integer between min and max to a number. */
function integer(min: number, max: number): Converter<number> {
  return (v) => {
    const n = typeof v === "bigint" ? Number(v) : v;
    if (typeof n !== "number" || !Number.isInteger(n) || n < min || n > max) {
      return fail("off_type");
    }
    return n;
  };
}

/** big converts an integer between min and max to a bigint. */
function big(min: bigint, max: bigint): Converter<bigint> {
  return (v) => {
    const n = typeof v === "number" && Number.isSafeInteger(v) ? BigInt(v) : v;
    if (typeof n !== "bigint" || n < min || n > max) {
      return fail("off_type");
    }
    return n;
  };
}

/** The converters of the integer types: 8, 16 and 32 bits to a number, 64 bits to a bigint. */
export const asInt8 = integer(-0x80, 0x7f);
export const asInt16 = integer(-0x8000, 0x7fff);
export const asInt32 = integer(-0x80000000, 0x7fffffff);
export const asInt64 = big(-(2n ** 63n), 2n ** 63n - 1n);
export const asUint8 = integer(0, 0xff);
export const asUint16 = integer(0, 0xffff);
export const asUint32 = integer(0, 0xffffffff);
export const asUint64 = big(0n, 2n ** 64n - 1n);

/** asFloat64 converts a number. */
export const asFloat64: Converter<number> = (v) => (typeof v === "number" ? v : fail("off_type"));

/** asFloat32 converts a number a float32 holds exactly, or NaN. */
export const asFloat32: Converter<number> = (v) =>
  typeof v === "number" && (Math.fround(v) === v || Number.isNaN(v)) ? v : fail("off_type");

/** asBigint converts any bigint, for an opaque type mapped to bigint. */
export const asBigint: Converter<bigint> = (v) => (typeof v === "bigint" ? v : fail("off_type"));

/** asBoolean converts a boolean, or an integer 0 or 1 such as a TINYINT(1) annotated bool holds; any other integer is off type (§13). */
export const asBoolean: Converter<boolean> = (v) => {
  if (typeof v === "boolean") {
    return v;
  }
  if (v === 0 || v === 1 || v === 0n || v === 1n) {
    return v === 1 || v === 1n;
  }
  return fail("off_type");
};

/** asserted wraps the converter of a field whose type an annotation narrows, such as bool on a TINYINT(1), so an off-type value names where the assertion came from (§13). */
export function asserted<T>(conv: Converter<T>, assertion: string): Converter<T> {
  return (v) => {
    try {
      return conv(v);
    } catch (e) {
      if (!(e instanceof DecodeError)) {
        throw e;
      }
      throw new DecodeError({ shape: e.shape, field: e.field, reason: e.reason, assertion });
    }
  };
}

/**
 * asParsed converts a column an annotation narrowed from text, such as a
 * CHAR(36) as(uuid) (§7.2, §13): an instance of the type when the class is
 * given, or text that parse reads; anything else is off type.
 */
export function asParsed<T>(parse: (s: string) => T, instance?: abstract new (...args: never[]) => T): Converter<T> {
  return (v) => {
    if (instance !== undefined && v instanceof instance) {
      return v;
    }
    if (typeof v === "string") {
      try {
        return parse(v);
      } catch {
        // off type below
      }
    }
    return fail("off_type");
  };
}

/** parseInstant parses RFC 3339 text, or YYYY-MM-DD HH:MM:SS with an optional fraction read as UTC, which is how a MySQL driver writes an instant into a text column. */
export function parseInstant(s: string): Temporal.Instant {
  return /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(\.\d+)?$/.test(s) ? Temporal.Instant.from(`${s.replace(" ", "T")}Z`) : Temporal.Instant.from(s);
}

/** asTimeOfDay converts a Temporal.PlainTime, or a duration of hours and smaller units from zero up to 24 hours, such as a MySQL TIME as(local_time) narrows (§13). */
export const asTimeOfDay: Converter<Temporal.PlainTime> = (v) => {
  if (v instanceof Temporal.PlainTime) {
    return v;
  }
  if (v instanceof Temporal.Duration && v.years === 0 && v.months === 0 && v.weeks === 0 && v.days === 0) {
    const ns = v.total({ unit: "nanoseconds" });
    if (ns >= 0 && ns < 86_400e9) {
      return Temporal.PlainTime.from("00:00").add(v);
    }
  }
  return fail("off_type");
};

/** asString converts a string, typed as S: a branded string such as Uuid when the column's type is one. */
export function asString<S extends string = string>(v: unknown): S {
  return typeof v === "string" ? (v as S) : fail("off_type");
}

/** asBytes converts a Uint8Array. */
export const asBytes: Converter<Uint8Array> = (v) => (v instanceof Uint8Array ? v : fail("off_type"));

/** asJson converts a JSON value, which the adapter has parsed. */
export const asJson: Converter<JsonValue> = (v) => (v === undefined ? fail("off_type") : (v as JsonValue));

/** asInstanceOf converts an instance of a class, such as Decimal or Temporal.Instant. */
export function asInstanceOf<T>(c: abstract new (...args: never[]) => T): Converter<T> {
  return (v) => (v instanceof c ? v : fail("off_type"));
}

/** asDate converts a Temporal.Instant to a Date, for a column the project maps to Date (§7.7, §8.8). */
export const asDate: Converter<Date> = (v) => {
  if (typeof v !== "object" || v === null || !("epochMilliseconds" in v) || typeof v.epochMilliseconds !== "number") {
    return fail("off_type");
  }
  return new Date(v.epochMilliseconds);
};

/** unchecked passes a value of an imported opaque type through: the emitter cannot check a type it only names (§8.3). */
export function unchecked<T>(v: unknown): T {
  return v as T;
}

/** asEnum converts a label of labels; a label added after generation is an unknown_enum_label error (§13). */
export function asEnum<const L extends readonly string[]>(labels: L): Converter<L[number]> {
  return (v) => {
    const s = asString(v);
    if (!(labels as readonly string[]).includes(s)) {
      return fail("unknown_enum_label");
    }
    return s as L[number];
  };
}

/**
 * array converts one dimension of an array with elem converting each
 * element. A value that is not an array where the declaration has a
 * dimension is a dimension_mismatch error (§13).
 */
export function array<E>(elem: Converter<E>): Converter<E[]> {
  return (v) => {
    if (!Array.isArray(v)) {
      return fail("dimension_mismatch");
    }
    return v.map((x: unknown, i) => {
      try {
        return elem(x);
      } catch (e) {
        if (!(e instanceof DecodeError)) {
          throw e;
        }
        throw new DecodeError({ shape: e.shape, field: join(`[${i}]`, e.field), reason: e.reason, assertion: e.assertion });
      }
    });
  };
}

/**
 * elem converts an element of an array whose elements are non-null: a NULL
 * element is a null_element_into_non_null error, and a nested array, a
 * dimension the declaration lacks, is a dimension_mismatch error (§13).
 */
export function elem<T>(conv: Converter<T>): Converter<T> {
  return (v) => {
    if (v === null || v === undefined) {
      return fail("null_element_into_non_null");
    }
    if (Array.isArray(v)) {
      return fail("dimension_mismatch");
    }
    return conv(v);
  };
}

/** nullableElem converts an element of an array whose elements may be NULL. */
export function nullableElem<T>(conv: Converter<T>): Converter<T | null> {
  return (v) => {
    if (v === null || v === undefined) {
      return null;
    }
    return elem(conv)(v);
  };
}

/** bound converts one bound of a range. */
function bound<T>(b: unknown, conv: Converter<T>): Bound<T> {
  if (typeof b !== "object" || b === null || !("kind" in b)) {
    return fail("off_type");
  }
  if (b.kind === "unbounded") {
    return { kind: "unbounded" };
  }
  if ((b.kind !== "inclusive" && b.kind !== "exclusive") || !("value" in b)) {
    return fail("off_type");
  }
  return { kind: b.kind, value: conv(b.value) };
}

/** asRange converts a Range, converting its bounds' values with conv. */
export function asRange<T>(conv: Converter<T>): Converter<Range<T>> {
  return (v) => {
    if (typeof v !== "object" || v === null || !("empty" in v)) {
      return fail("off_type");
    }
    if (v.empty === true) {
      return { empty: true };
    }
    if (v.empty !== false || !("lower" in v) || !("upper" in v)) {
      return fail("off_type");
    }
    return { empty: false, lower: bound(v.lower, conv), upper: bound(v.upper, conv) };
  };
}

/** asMultirange converts a multirange: an array of ranges. */
export function asMultirange<T>(conv: Converter<T>): Converter<Range<T>[]> {
  const r = asRange(conv);
  return (v) => (Array.isArray(v) ? v.map((x: unknown) => r(x)) : fail("off_type"));
}

const storageClasses: Record<StorageValue["class"], string> = { integer: "bigint", real: "number", text: "string", blob: "object" };

/**
 * weak converts a weak column's StorageValue: a value conv accepts as the
 * affinity type, and otherwise the raw value, which is not an error (§8.10,
 * §13).
 */
export function weak<T>(conv: Converter<T>): Converter<Weak<T>> {
  return (v) => {
    if (
      typeof v !== "object" ||
      v === null ||
      !("class" in v) ||
      !("value" in v) ||
      typeof v.class !== "string" ||
      !Object.hasOwn(storageClasses, v.class) ||
      typeof v.value !== storageClasses[v.class as StorageValue["class"]] ||
      (v.class === "blob" && !(v.value instanceof Uint8Array))
    ) {
      return fail("off_type");
    }
    const raw = v as StorageValue;
    try {
      return weakOf(conv(raw.value));
    } catch (e) {
      if (!(e instanceof DecodeError)) {
        throw e;
      }
      return weakRaw(raw);
    }
  };
}
