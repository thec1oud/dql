import { describe, expect, test } from "bun:test";
import {
  DecodeError,
  Decimal,
  Decoder,
  Row,
  Temporal,
  array,
  asBigint,
  asBoolean,
  asBytes,
  asDate,
  asEnum,
  asFloat32,
  asFloat64,
  asInstanceOf,
  asInt16,
  asInt32,
  asInt64,
  asInt8,
  asJson,
  asMultirange,
  asRange,
  asString,
  asUint16,
  asUint32,
  asUint64,
  asUint8,
  asserted,
  asParsed,
  asTimeOfDay,
  parseInstant,
  parseUuid,
  elem,
  nest,
  nullableElem,
  unchecked,
  weak,
} from "../src/index.js";

/** failure returns the DecodeError fn throws, as its fields. */
function failure(fn: () => unknown): { shape: string; field: string; reason: string; assertion: string } {
  try {
    fn();
  } catch (e) {
    if (e instanceof DecodeError) {
      return { shape: e.shape, field: e.field, reason: e.reason, assertion: e.assertion };
    }
    throw e;
  }
  throw new Error("no DecodeError");
}

const offType = { shape: "", field: "", reason: "off_type", assertion: "" };

describe("Decoder", () => {
  const post = (r: Row) => {
    const d = new Decoder("post.full.flat", r, 2);
    return { id: d.field(0, "id", "", asInt64), title: d.nullable(1, "title", asString) };
  };

  test("decodes fields and nests (§13)", () => {
    const d = new Decoder("user.full.nested", new Row([1n, null, new Row([2, "t"]), [new Row([3, null])], null, null]), 6);
    expect(d.field(0, "id", "", asInt64)).toBe(1n);
    expect(d.nullable(1, "name", asString)).toBe(null);
    expect(d.field(2, "post", "", nest(post))).toEqual({ id: 2n, title: "t" });
    expect(d.many(3, "posts", post)).toEqual([{ id: 3n, title: null }]);
    expect(d.many(4, "tags", post)).toEqual([]);
    expect(d.nullable(5, "profile", nest(post))).toBe(null);
  });

  test("a row of the wrong form", () => {
    expect(failure(() => new Decoder("s", [1], 1))).toEqual({ ...offType, shape: "s" });
    expect(failure(() => new Decoder("s", new Row([1]), 2))).toEqual({ ...offType, shape: "s" });
  });

  test("NULL in a non-null field names the assertion", () => {
    const d = new Decoder("published_post.full.flat", new Row([undefined]), 1);
    expect(failure(() => d.field(0, "title", "core.nonnull from COMMENT ON COLUMN app.published_posts.title", asString))).toEqual({
      shape: "published_post.full.flat",
      field: "title",
      reason: "null_into_non_null",
      assertion: "core.nonnull from COMMENT ON COLUMN app.published_posts.title",
    });
  });

  test("errors carry the field's path", () => {
    const d = new Decoder("s", new Row([[1, null], new Row([1, 2]), [new Row([1, "t"]), new Row(["x", "t"])], "x", [new Row([1])], [5]]), 6);
    expect(failure(() => d.field(0, "tags", "", array(elem(asInt32))))).toEqual({
      shape: "s",
      field: "tags[1]",
      reason: "null_element_into_non_null",
      assertion: "",
    });
    expect(failure(() => d.field(1, "author", "", nest(post)))).toEqual({ shape: "post.full.flat", field: "author.title", reason: "off_type", assertion: "" });
    expect(failure(() => d.many(2, "posts", post))).toEqual({ shape: "post.full.flat", field: "posts[1].id", reason: "off_type", assertion: "" });
    expect(failure(() => d.many(3, "posts", post))).toEqual({ ...offType, shape: "s", field: "posts" });
    expect(failure(() => d.many(4, "posts", post))).toEqual({ shape: "post.full.flat", field: "posts[0]", reason: "off_type", assertion: "" });
    expect(failure(() => d.many(5, "posts", post))).toEqual({ ...offType, shape: "s", field: "posts[0]" });
    expect(failure(() => d.field(3, "post", "", nest(post)))).toEqual({ ...offType, shape: "s", field: "post" });
  });

  test("other errors pass through", () => {
    const d = new Decoder("s", new Row([1, [1], [new Row([])]]), 3);
    const boom = () => {
      throw new RangeError("boom");
    };
    expect(() => d.field(0, "f", "", boom)).toThrow(RangeError);
    expect(() => d.field(1, "f", "", array(boom))).toThrow(RangeError);
    expect(() => d.many(2, "f", boom)).toThrow(RangeError);
  });
});

describe("converters", () => {
  test("integers", () => {
    for (const [conv, ok, bad] of [
      [asInt8, [-128, 127, 5n], [128, -129, 1.5, "1", 2n ** 70n]],
      [asInt16, [-32768, 32767], [32768]],
      [asInt32, [-(2 ** 31), 2 ** 31 - 1], [2 ** 31]],
      [asUint8, [0, 255], [-1, 256]],
      [asUint16, [65535], [65536]],
      [asUint32, [2 ** 32 - 1], [2 ** 32]],
    ] as const) {
      for (const v of ok) {
        expect(conv(v)).toBe(Number(v));
      }
      for (const v of bad) {
        expect(failure(() => conv(v))).toEqual(offType);
      }
    }
    expect(asInt64(-(2n ** 63n))).toBe(-(2n ** 63n));
    expect(asInt64(7)).toBe(7n);
    expect(asUint64(2n ** 64n - 1n)).toBe(2n ** 64n - 1n);
    for (const v of [2n ** 63n, 1.5, 2 ** 60, "1"]) {
      expect(failure(() => asInt64(v))).toEqual(offType);
    }
    expect(failure(() => asUint64(-1n))).toEqual(offType);
    expect(asBigint(1n)).toBe(1n);
    expect(failure(() => asBigint(1))).toEqual(offType);
  });

  test("floats", () => {
    expect(asFloat64(0.1)).toBe(0.1);
    expect(failure(() => asFloat64(1n))).toEqual(offType);
    expect(asFloat32(0.5)).toBe(0.5);
    expect(Number.isNaN(asFloat32(Number.NaN))).toBe(true);
    expect(failure(() => asFloat32(0.1))).toEqual(offType);
    expect(failure(() => asFloat32("0.5"))).toEqual(offType);
  });

  test("bool (§13)", () => {
    expect([asBoolean(true), asBoolean(false), asBoolean(0), asBoolean(1), asBoolean(0n), asBoolean(1n)]).toEqual([true, false, false, true, false, true]);
    expect(failure(() => asBoolean(2))).toEqual(offType);
    expect(failure(() => asBoolean("true"))).toEqual(offType);
  });

  test("narrowed columns decode from their stored form (§7.2, §13)", () => {
    const uuid = asParsed(parseUuid);
    expect(uuid("A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A11")).toBe(parseUuid("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"));
    expect(failure(() => uuid("nope"))).toEqual(offType);
    expect(failure(() => uuid(1))).toEqual(offType);
    const date = asParsed((s) => Temporal.PlainDate.from(s), Temporal.PlainDate);
    const d = Temporal.PlainDate.from("2026-11-26");
    expect(date(d)).toBe(d);
    expect(date("2026-11-26").equals(d)).toBe(true);
    const at = Temporal.Instant.from("2026-11-26T09:30:00.5Z");
    expect(parseInstant("2026-11-26 09:30:00.5").equals(at)).toBe(true);
    expect(parseInstant("2026-11-26T10:30:00.5+01:00").equals(at)).toBe(true);
    const noon = Temporal.PlainTime.from("12:30:15.25");
    expect(asTimeOfDay(noon)).toBe(noon);
    expect(asTimeOfDay(Temporal.Duration.from({ hours: 12, minutes: 30, seconds: 15, milliseconds: 250 })).equals(noon)).toBe(true);
    for (const v of [Temporal.Duration.from({ hours: 24 }), Temporal.Duration.from({ hours: -1 }), Temporal.Duration.from({ days: 1 }), "12:00"]) {
      expect(failure(() => asTimeOfDay(v))).toEqual(offType);
    }
  });

  test("asserted names the annotation that narrows a type (§13)", () => {
    const conv = asserted(asBoolean, "core.bool on shop.app.t.on");
    expect(conv(1)).toBe(true);
    expect(failure(() => conv(2))).toEqual({ ...offType, assertion: "core.bool on shop.app.t.on" });
    const broken = asserted(() => {
      throw new RangeError("broken");
    }, "core.bool on shop.app.t.on");
    expect(() => broken(1)).toThrow(RangeError);
  });

  test("strings, bytes, JSON and instances", () => {
    expect(asString("a") as string).toBe("a");
    expect(failure(() => asString(1))).toEqual(offType);
    expect(asBytes(new Uint8Array([1]))).toEqual(new Uint8Array([1]));
    expect(failure(() => asBytes([1]))).toEqual(offType);
    expect(asJson({ a: [1, null] })).toEqual({ a: [1, null] });
    expect(asJson(null)).toBe(null);
    expect(failure(() => asJson(undefined))).toEqual(offType);
    const d = Decimal.parse("1.5");
    expect(asInstanceOf(Decimal)(d)).toBe(d);
    expect(failure(() => asInstanceOf(Decimal)("1.5"))).toEqual(offType);
    const at = Temporal.Instant.fromEpochMilliseconds(1700000000123);
    expect(asInstanceOf(Temporal.Instant)(at)).toBe(at);
    expect(asDate(at).getTime()).toBe(1700000000123);
    for (const v of [null, "x", {}, { epochMilliseconds: "1" }]) {
      expect(failure(() => asDate(v))).toEqual(offType);
    }
    expect(unchecked<{ x: number }>({ x: 1 })).toEqual({ x: 1 });
  });

  test("enums (§13)", () => {
    const status = asEnum(["draft", "published"] as const);
    expect(status("draft") as string).toBe("draft");
    expect(failure(() => status("archived"))).toEqual({ ...offType, reason: "unknown_enum_label" });
    expect(failure(() => status(1))).toEqual(offType);
  });

  test("arrays (§8.6, §13)", () => {
    const grid = array(array(nullableElem(asInt32)));
    expect(grid([[1, null], []])).toEqual([[1, null], []]);
    expect(failure(() => grid([1]))).toEqual({ ...offType, field: "[0]", reason: "dimension_mismatch" });
    expect(failure(() => grid("x"))).toEqual({ ...offType, reason: "dimension_mismatch" });
    expect(failure(() => grid([[[1]]]))).toEqual({ ...offType, field: "[0][0]", reason: "dimension_mismatch" });
    expect(failure(() => grid([[undefined, "x"]]))).toEqual({ ...offType, field: "[0][1]" });
    expect(failure(() => array(elem(asInt32))([[1]]))).toEqual({ ...offType, field: "[0]", reason: "dimension_mismatch" });
  });

  test("ranges", () => {
    const r = asRange(asInt32);
    expect(r({ empty: true })).toEqual({ empty: true });
    expect(r({ empty: false, lower: { kind: "inclusive", value: 1 }, upper: { kind: "unbounded" } })).toEqual({
      empty: false,
      lower: { kind: "inclusive", value: 1 },
      upper: { kind: "unbounded" },
    });
    expect(r({ empty: false, lower: { kind: "exclusive", value: 1n }, upper: { kind: "exclusive", value: 2 } })).toEqual({
      empty: false,
      lower: { kind: "exclusive", value: 1 },
      upper: { kind: "exclusive", value: 2 },
    });
    for (const v of [
      null,
      "x",
      {},
      { empty: "no" },
      { empty: false, lower: { kind: "unbounded" } },
      { empty: false, lower: null, upper: { kind: "unbounded" } },
      { empty: false, lower: { kind: "open", value: 1 }, upper: { kind: "unbounded" } },
      { empty: false, lower: { kind: "inclusive" }, upper: { kind: "unbounded" } },
      { empty: false, lower: { kind: "inclusive", value: "1" }, upper: { kind: "unbounded" } },
    ]) {
      expect(failure(() => r(v))).toEqual(offType);
    }
    const m = asMultirange(asInt32);
    expect(m([{ empty: true }])).toEqual([{ empty: true }]);
    expect(failure(() => m({ empty: true }))).toEqual(offType);
  });

  test("weak values (§8.10, §13)", () => {
    const w = weak(asInt64);
    expect(w({ class: "integer", value: 7n })).toEqual({ ok: true, value: 7n });
    expect(w({ class: "text", value: "seven" })).toEqual({ ok: false, raw: { class: "text", value: "seven" } });
    expect(weak(asBytes)({ class: "blob", value: new Uint8Array([1]) })).toEqual({ ok: true, value: new Uint8Array([1]) });
    expect(weak(asFloat64)({ class: "real", value: 1.5 })).toEqual({ ok: true, value: 1.5 });
    for (const v of [
      null,
      7n,
      { class: "integer" },
      { value: 1n },
      { class: 1, value: 1n },
      { class: "date", value: "x" },
      { class: "integer", value: 1 },
      { class: "blob", value: [1] },
      { class: "toString", value: "x" },
    ]) {
      expect(failure(() => w(v))).toEqual(offType);
    }
    expect(() =>
      weak(() => {
        throw new RangeError("boom");
      })({ class: "integer", value: 1n }),
    ).toThrow(RangeError);
  });
});
