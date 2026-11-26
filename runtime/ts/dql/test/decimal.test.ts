import { describe, expect, test } from "bun:test";
import { Decimal, MAX_PARSE_EXPONENT } from "../src/index.js";

const p = (s: string): Decimal => Decimal.parse(s);

describe("parse", () => {
  test.each([
    ["0", "0", 0],
    ["-0", "0", 0],
    ["0.00", "0.00", -2],
    ["12", "12", 0],
    ["+12", "12", 0],
    ["-12.50", "-12.50", -2],
    ["1.5", "1.5", -1],
    [".5", "0.5", -1],
    ["-.05", "-0.05", -2],
    ["5.", "5", 0],
    ["1e3", "1000", 3],
    ["1E+3", "1000", 3],
    ["0e3", "0", 3],
    ["1.2e-3", "0.0012", -4],
    ["-123.456e1", "-1234.56", -2],
    ["12345678901234567890123456789.0123456789", "12345678901234567890123456789.0123456789", -10],
    [`1e${MAX_PARSE_EXPONENT}`, "1" + "0".repeat(MAX_PARSE_EXPONENT), MAX_PARSE_EXPONENT],
    [`1e-${MAX_PARSE_EXPONENT}`, "0." + "0".repeat(MAX_PARSE_EXPONENT - 1) + "1", -MAX_PARSE_EXPONENT],
  ])("%s", (input, out, exp) => {
    const d = p(input);
    expect(d.toString()).toBe(out);
    expect(d.exponent).toBe(exp);
    expect(Decimal.tryParse(input)?.toString()).toBe(out);
  });

  test.each([
    "", "+", "-", ".", "+.", "1.2.3", "1,5", " 1", "1 ", "1_000", "0x10", "1e", "1e+",
    "1e1.5", "1e2e3", "e3", "NaN", "Infinity", "-inf", `1e${MAX_PARSE_EXPONENT + 1}`,
    `1e-${MAX_PARSE_EXPONENT + 1}`, `0.1e-${MAX_PARSE_EXPONENT}`, "1e99999999999", "--1", "+-1",
  ])("refuses %j", (input) => {
    expect(Decimal.tryParse(input)).toBeUndefined();
    expect(() => p(input)).toThrow(SyntaxError);
  });
});

describe("construction", () => {
  test("constructor", () => {
    expect(new Decimal(-1250n, -2).toString()).toBe("-12.50");
    expect(new Decimal(7n).exponent).toBe(0);
    expect(() => new Decimal(1n, 2 ** 31)).toThrow(RangeError);
    expect(() => new Decimal(1n, 0.5)).toThrow(RangeError);
  });

  test("from", () => {
    expect(Decimal.from(42).toString()).toBe("42");
    expect(Decimal.from(-(2n ** 70n)).toString()).toBe((-(2n ** 70n)).toString());
    expect(() => Decimal.from(0.1)).toThrow(RangeError);
    expect(() => Decimal.from(2 ** 60)).toThrow(RangeError);
  });

  test("zero", () => {
    expect(Decimal.ZERO.toString()).toBe("0");
    expect(Decimal.ZERO.isZero()).toBe(true);
    expect(Decimal.ZERO.sign).toBe(0);
  });
});

describe("arithmetic", () => {
  test.each([
    [p("1.5").add(p("2.25")), "3.75"],
    [p("2.25").add(p("1.5")), "3.75"],
    [p("1.50").add(p("-1.5")), "0.00"],
    [p("1e2").add(p("1")), "101"],
    [p("1.5").sub(p("2.25")), "-0.75"],
    [p("0.1").sub(p("0.3")), "-0.2"],
    [p("1.5").mul(p("-2.25")), "-3.375"],
    [p("1e2").mul(p("1.5")), "150"],
    [p("-12.50").neg(), "12.50"],
    [p("12.50").neg(), "-12.50"],
    [p("-12.50").abs(), "12.50"],
    [p("12.50").abs(), "12.50"],
    [Decimal.ZERO.add(p("1.5")), "1.5"],
    [Decimal.ZERO.neg(), "0"],
  ])("%s", (got, want) => {
    expect(got.toString()).toBe(want);
  });

  test("0.1 + 0.2 equals 0.3", () => {
    expect(p("0.1").add(p("0.2")).equals(p("0.3"))).toBe(true);
  });

  test("exponent overflow", () => {
    expect(() => new Decimal(1n, 2 ** 31 - 1).mul(new Decimal(1n, 1))).toThrow(RangeError);
  });

  test.each([
    ["1.5", "1.50", 0],
    ["1.5", "1.49", 1],
    ["-1.5", "1.5", -1],
    ["1e3", "999.9", 1],
    ["0", "0.000", 0],
    ["-0.001", "0", -1],
  ] as const)("cmp(%s, %s)", (a, b, want) => {
    expect(p(a).cmp(p(b))).toBe(want);
    expect(p(b).cmp(p(a))).toBe((-want || 0) as -1 | 0 | 1);
    expect(p(a).equals(p(b))).toBe(want === 0);
  });

  test("sign", () => {
    expect(p("-2").sign).toBe(-1);
    expect(p("2").sign).toBe(1);
    expect(p("0.00").isZero()).toBe(true);
  });
});

describe("rounding", () => {
  test.each([
    ["1.5", 3, "1.500", "1.500"],
    ["1.25", 1, "1.3", "1.2"],
    ["1.24", 1, "1.2", "1.2"],
    ["-1.25", 1, "-1.3", "-1.2"],
    ["-1.24", 1, "-1.2", "-1.2"],
    ["0.5", 0, "1", "0"],
    ["-0.5", 0, "-1", "0"],
    ["0.49", 0, "0", "0"],
    ["99.95", 1, "100.0", "99.9"],
    ["1250", -2, "1300", "1200"],
    ["1249", -2, "1200", "1200"],
  ] as const)("%s to %d", (input, scale, round, trunc) => {
    expect(p(input).round(scale).toString()).toBe(round);
    expect(p(input).truncate(scale).toString()).toBe(trunc);
  });

  test.each([
    ["999.99", 5, 2, true],
    ["999.995", 5, 2, false],
    ["999.994", 5, 2, true],
    ["-999.99", 5, 2, true],
    ["1000", 5, 2, false],
    ["0.001", 1, 3, true],
    ["12345", 3, -2, true],
    ["123456", 3, -2, false],
  ] as const)("%s fits (%d, %d): %s", (input, precision, scale, want) => {
    expect(p(input).fits(precision, scale)).toBe(want);
  });
});

test("JSON", () => {
  expect(JSON.stringify({ v: p("1.50") })).toBe('{"v":"1.50"}');
  expect(`${p("1e2")}`).toBe("100");
});
