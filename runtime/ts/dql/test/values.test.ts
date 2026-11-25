import { describe, expect, test } from "vitest";
import {
  MAX_VERSION,
  MIN_VERSION,
  OffsetTime,
  Temporal,
  VersionError,
  emptyRange,
  enforceVersion,
  exclusive,
  formatRange,
  inclusive,
  parseCidr,
  parseInet,
  parseMacAddr,
  parseUuid,
  range,
  rangeContains,
  unbounded,
  weakOf,
  weakRaw,
  type Weak,
} from "../src/index.js";
import { chooseTemporal } from "../src/temporal.js";

describe("version", () => {
  test("the implemented versions pass", () => {
    for (let v = MIN_VERSION; v <= MAX_VERSION; v++) {
      expect(() => enforceVersion(v)).not.toThrow();
    }
  });

  test.each([MIN_VERSION - 1, MAX_VERSION + 1, 1.5, Number.NaN])("%d fails", (v) => {
    const e = (() => {
      try {
        enforceVersion(v);
      } catch (err) {
        return err;
      }
    })();
    expect(e).toBeInstanceOf(VersionError);
    expect((e as VersionError).version).toBe(v);
    expect((e as VersionError).name).toBe("VersionError");
    expect((e as VersionError).message).toContain(`implements ${MIN_VERSION} to ${MAX_VERSION}`);
  });
});

describe("Temporal", () => {
  test("is usable", () => {
    expect(Temporal.Instant.from("2020-01-01T00:00:00Z").epochMilliseconds).toBe(1577836800000);
  });

  test("native wins over the polyfill", async () => {
    const native = { name: "native" };
    expect(await chooseTemporal(native, () => Promise.reject(new Error("loaded")))).toBe(native);
    const poly = { name: "polyfill" };
    expect(await chooseTemporal<object>(undefined, () => Promise.resolve(poly))).toBe(poly);
  });
});

describe("brands", () => {
  test("uuid", () => {
    expect(parseUuid("A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A11")).toBe("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11");
    for (const s of ["", "a0eebc999c0b4ef8bb6d6bb9bd380a11", "{a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11}", "g0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"]) {
      expect(() => parseUuid(s)).toThrow(SyntaxError);
    }
  });

  test.each([
    "192.168.0.1",
    "192.168.0.1/24",
    "0.0.0.0/0",
    "::1",
    "::",
    "::/0",
    "2001:db8::1/64",
    "2001:db8:0:0:0:0:0:1",
    "1:2:3:4:5:6:7::",
    "::ffff:1.2.3.4",
    "fe80::1:2.3.4.5/128",
  ])("inet %s", (s) => {
    expect(parseInet(s)).toBe(s);
  });

  test.each([
    "",
    "1.2.3",
    "1.2.3.4.5",
    "256.0.0.1",
    "01.2.3.4",
    "1.2.3.4/33",
    "1.2.3.4/",
    "1.2.3.4/x",
    "1.2.3.4/0024",
    "1:2:3:4:5:6:7",
    "1:2:3:4:5:6:7:8:9",
    "1::2::3",
    "1:2:3:4:5:6:7::8",
    "12345::",
    "::g",
    "::1.2.3",
    "1.2.3.4::",
    "::1/129",
  ])("inet refuses %j", (s) => {
    expect(() => parseInet(s)).toThrow(SyntaxError);
  });

  test("cidr", () => {
    expect(parseCidr("10.0.0.0/8")).toBe("10.0.0.0/8");
    expect(parseCidr("2001:db8::/32")).toBe("2001:db8::/32");
    expect(() => parseCidr("10.0.0.1/8")).toThrow(SyntaxError);
    expect(() => parseCidr("10.0.0.0")).toThrow(SyntaxError);
    expect(() => parseCidr("x/8")).toThrow(SyntaxError);
  });

  test("macaddr", () => {
    expect(parseMacAddr("08:00:2B:01:02:03")).toBe("08:00:2b:01:02:03");
    expect(() => parseMacAddr("08-00-2b-01-02-03")).toThrow(SyntaxError);
  });
});

test("weak", () => {
  const typed: Weak<bigint> = weakOf(3n);
  const raw: Weak<bigint> = weakRaw({ class: "text", value: "three" });
  expect(typed).toEqual({ ok: true, value: 3n });
  expect(raw).toEqual({ ok: false, raw: { class: "text", value: "three" } });
});

describe("range", () => {
  const cmp = (a: number, b: number): number => a - b;

  test.each([
    [range(inclusive(1), exclusive(10)), "[1,10)", [[1, true], [0, false], [9, true], [10, false]]],
    [range(exclusive(1), inclusive(10)), "(1,10]", [[1, false], [2, true], [10, true], [11, false]]],
    [range(unbounded<number>(), unbounded<number>()), "(,)", [[-1e9, true], [1e9, true]]],
    [range(unbounded<number>(), inclusive(5)), "(,5]", [[5, true], [6, false]]],
    [emptyRange<number>(), "empty", [[0, false]]],
  ] as const)("%s", (r, text, cases) => {
    expect(formatRange(r)).toBe(text);
    for (const [v, want] of cases) {
      expect(rangeContains(r, v, cmp)).toBe(want);
    }
  });

  test("format with a formatter", () => {
    expect(formatRange(range(inclusive(1), exclusive(2)), (v) => v.toFixed(1))).toBe("[1.0,2.0)");
  });
});

describe("OffsetTime", () => {
  test.each([
    ["12:30+05:00", "12:30:00+05:00", 18000],
    ["12:30:15Z", "12:30:15+00:00", 0],
    ["12:30:15z", "12:30:15+00:00", 0],
    ["23:59:59.999999999-11:30", "23:59:59.999999999-11:30", -41400],
    ["00:00:00.5+01:02:03", "00:00:00.5+01:02:03", 3723],
    ["01:00-23:59:59", "01:00:00-23:59:59", -86399],
  ])("%s", (s, text, offset) => {
    const t = OffsetTime.parse(s);
    expect(t.toString()).toBe(text);
    expect(t.offset).toBe(offset);
    expect(JSON.stringify(t)).toBe(JSON.stringify(text));
    expect(t.equals(OffsetTime.parse(text))).toBe(true);
  });

  test.each([
    "",
    "12:30",
    "24:00+00:00",
    "12:60+00:00",
    "12:30:60+00:00",
    "12:30:00.+00:00",
    "12:30:00.1234567890+00:00",
    "12:30+0500",
    "12:30+05:60",
    "12:30+05:00:60",
    "12:30+24:00",
    "1:30+05:00",
  ])("refuses %j", (s) => {
    expect(() => OffsetTime.parse(s)).toThrow(SyntaxError);
  });

  test("equals", () => {
    const a = OffsetTime.parse("12:00+01:00");
    expect(a.equals(OffsetTime.parse("12:00+02:00"))).toBe(false);
    expect(a.equals(OffsetTime.parse("12:01+01:00"))).toBe(false);
  });

  test("constructor checks the offset", () => {
    const t = new Temporal.PlainTime(1);
    expect(() => new OffsetTime(t, 86400)).toThrow(RangeError);
    expect(() => new OffsetTime(t, 0.5)).toThrow(RangeError);
    expect(new OffsetTime(t, -86399).toString()).toBe("01:00:00-23:59:59");
  });
});
