import { describe, expect, test } from "bun:test";
import {
  Decimal,
  DigitsCheck,
  LengthCheck,
  MembershipCheck,
  NotEmptyCheck,
  PatternCheck,
  RangeCheck,
  UnmatchedKeyError,
  ValidationError,
  type ValidationIssue,
  classOf,
  describeIssue,
  foldAscii,
  issuesAt,
  length,
  throwIfIssues,
} from "../src/index.js";

describe("ValidationError", () => {
  test("§10.5", () => {
    const issues: ValidationIssue[] = [
      { field: "title", kind: "length", constraint: "shop.app.posts.posts_title_check", min: 1, max: 200, unit: "characters", got: 0 },
    ];
    let caught: unknown;
    try {
      throwIfIssues(issues);
    } catch (e) {
      caught = e;
    }
    expect(caught).toBeInstanceOf(ValidationError);
    expect(classOf(caught)).toBe("ValidationErrors");
    expect((caught as ValidationError).issues).toBe(issues);
    expect((caught as ValidationError).message).toBe(
      "dql: ValidationErrors: title: length 0 characters, want [1, 200] (shop.app.posts.posts_title_check)",
    );
    expect(() => throwIfIssues([])).not.toThrow();
    expect(new ValidationError([{ kind: "not_empty" }, { field: "a", kind: "not_empty" }]).message).toBe(
      "dql: ValidationErrors: empty; a: empty",
    );
  });

  test.each<[ValidationIssue, string]>([
    [{ field: "title", kind: "length", max: 200, unit: "bytes", got: 201 }, "title: length 201 bytes, want (-∞, 200]"],
    [{ field: "n", kind: "length", min: 2, unit: "utf16_code_units", got: 1 }, "n: length 1 utf16_code_units, want [2, ∞)"],
    [{ field: "price", kind: "range", min: 0, max: 100, maxInclusive: true }, "price: out of range (0, 100]"],
    [{ field: "price", kind: "range", min: 0, minInclusive: true, constraint: "c" }, "price: out of range [0, ∞) (c)"],
    [{ field: "total", kind: "digits", precision: 12, scale: 2 }, "total: does not fit decimal(12,2)"],
    [{ field: "status", kind: "membership", values: ["a", "b"] }, "status: not one of 2 listed values"],
    [{ field: "status", kind: "membership" }, "status: not one of 0 listed values"],
    [{ field: "name", kind: "not_empty" }, "name: empty"],
    [{ field: "slug", kind: "pattern", pattern: "^[a-z]+$" }, 'slug: does not match "^[a-z]+$"'],
    [{ field: "author_id", kind: "relation_conflict", other: "author" }, "author_id: disagrees with author"],
    [{ field: "author_id", kind: "relation_missing", other: "author" }, "author_id: required, through it or author"],
    [{ field: "published_at", kind: "field_set_twice" }, "published_at: set by both the payload and an expression"],
    [{ shape: "shop.app.slug_text", kind: "pattern", pattern: "^[a-z]+$", constraint: "c" }, 'shop.app.slug_text: does not match "^[a-z]+$" (c)'],
    [{ kind: "not_empty" }, "empty"],
  ])("describe %j", (issue, want) => {
    expect(describeIssue(issue)).toBe(want);
  });

  test("issuesAt", () => {
    const issues: ValidationIssue[] = [
      { field: "title", kind: "not_empty" },
      { field: "author_id", other: "author", kind: "relation_conflict" },
      { field: "[0]", kind: "not_empty" },
      { field: "", kind: "not_empty" },
      { kind: "not_empty" },
    ];
    const got = issuesAt(issues, "posts_to_author[1]");
    expect(got.map((i) => [i.field, i.other])).toEqual([
      ["posts_to_author[1].title", undefined],
      ["posts_to_author[1].author_id", "posts_to_author[1].author"],
      ["posts_to_author[1][0]", undefined],
      ["posts_to_author[1]", undefined],
      ["posts_to_author[1]", undefined],
    ]);
    expect(issues[0]?.field).toBe("title");
    expect(issuesAt(issues, "")[0]?.field).toBe("title");
  });
});

describe("checks", () => {
  test.each([
    ["", 0, 0, 0],
    ["abc", 3, 3, 3],
    ["héllo", 5, 6, 5],
    ["😀", 1, 4, 2],
    ["日本", 2, 6, 2],
    ["\u00e9\u07ff\u0800", 3, 7, 3],
    ["\ud800", 1, 3, 1],
  ])("length(%j)", (s, chars, bytes, units) => {
    expect(length(s, "characters")).toBe(chars);
    expect(length(s, "bytes")).toBe(bytes);
    expect(length(s, "utf16_code_units")).toBe(units);
  });

  test("length check", () => {
    const issues: ValidationIssue[] = [];
    const c = new LengthCheck("c", "characters", 1, 3);
    c.check(issues, "s", "f", "a");
    c.check(issues, "s", "f", "😀😀😀");
    expect(issues).toEqual([]);
    c.check(issues, "s", "f", "");
    c.check(issues, "s", "f", "abcd");
    new LengthCheck("d", "bytes", undefined, 3).check(issues, "s", "g", "😀");
    new LengthCheck("e", "utf16_code_units", 2).check(issues, "s", "h", "a");
    expect(issues).toEqual([
      { shape: "s", field: "f", kind: "length", constraint: "c", min: 1, max: 3, unit: "characters", got: 0 },
      { shape: "s", field: "f", kind: "length", constraint: "c", min: 1, max: 3, unit: "characters", got: 4 },
      { shape: "s", field: "g", kind: "length", constraint: "d", max: 3, unit: "bytes", got: 4 },
      { shape: "s", field: "h", kind: "length", constraint: "e", min: 2, unit: "utf16_code_units", got: 1 },
    ]);
  });

  test("not empty", () => {
    const issues: ValidationIssue[] = [];
    const c = new NotEmptyCheck("c");
    c.check(issues, "s", "f", " ");
    c.check(issues, "s", "f", "");
    expect(issues).toEqual([{ shape: "s", field: "f", kind: "not_empty", constraint: "c" }]);
  });

  test("pattern", () => {
    const issues: ValidationIssue[] = [];
    const c = new PatternCheck("c", "^[a-z]+$", /^[a-z]+$/gu);
    c.check(issues, "s", "f", "abc");
    c.check(issues, "s", "f", "abc");
    c.check(issues, "s", "f", "ABC");
    expect(issues).toEqual([{ shape: "s", field: "f", kind: "pattern", constraint: "c", pattern: "^[a-z]+$" }]);
  });

  test("range", () => {
    const cmp = (a: number, b: number): number => a - b;
    const issues: ValidationIssue[] = [];
    const closed = new RangeCheck("c", { min: 0, max: 10, minInclusive: true, maxInclusive: true }, cmp);
    const open = new RangeCheck("o", { min: 0, max: 10, minInclusive: false, maxInclusive: false }, cmp);
    const half = new RangeCheck("h", { min: 0, minInclusive: false, maxInclusive: false }, cmp);
    const upper = new RangeCheck("u", { max: 0, minInclusive: false, maxInclusive: true }, cmp);
    for (const x of [0, 5, 10]) {
      closed.check(issues, "s", "f", x);
    }
    open.check(issues, "s", "f", 5);
    half.check(issues, "s", "f", 1e9);
    upper.check(issues, "s", "f", -1e9);
    upper.check(issues, "s", "f", 0);
    expect(issues).toEqual([]);
    closed.check(issues, "s", "f", -1);
    open.check(issues, "s", "f", 0);
    open.check(issues, "s", "f", 10);
    half.check(issues, "s", "f", 0);
    upper.check(issues, "s", "f", 1);
    expect(issues).toEqual([
      { shape: "s", field: "f", kind: "range", constraint: "c", min: 0, max: 10, minInclusive: true, maxInclusive: true },
      { shape: "s", field: "f", kind: "range", constraint: "o", min: 0, max: 10, minInclusive: false, maxInclusive: false },
      { shape: "s", field: "f", kind: "range", constraint: "o", min: 0, max: 10, minInclusive: false, maxInclusive: false },
      { shape: "s", field: "f", kind: "range", constraint: "h", min: 0, minInclusive: false, maxInclusive: false },
      { shape: "s", field: "f", kind: "range", constraint: "u", max: 0, minInclusive: false, maxInclusive: true },
    ]);
  });

  test("digits", () => {
    const issues: ValidationIssue[] = [];
    const c = new DigitsCheck("c", 5, 2);
    c.check(issues, "s", "f", Decimal.parse("999.994"));
    c.check(issues, "s", "f", Decimal.parse("999.995"));
    expect(issues).toEqual([{ shape: "s", field: "f", kind: "digits", constraint: "c", precision: 5, scale: 2 }]);
  });

  test("membership", () => {
    const issues: ValidationIssue[] = [];
    const c = new MembershipCheck("c", ["a", "b"]);
    c.check(issues, "s", "f", "b");
    c.check(issues, "s", "f", "z");
    const d = new MembershipCheck("d", [Decimal.parse("1.5")], (a, b) => a.equals(b));
    d.check(issues, "s", "g", Decimal.parse("1.50"));
    d.check(issues, "s", "g", Decimal.parse("2"));
    expect(issues).toEqual([
      { shape: "s", field: "f", kind: "membership", constraint: "c", values: ["a", "b"] },
      { shape: "s", field: "g", kind: "membership", constraint: "d", values: [Decimal.parse("1.5")] },
    ]);
  });
});

describe("collections", () => {
  test("foldAscii", () => {
    expect(foldAscii("ÀBC-Ünï-XyZ") as string).toBe("Àbc-Ünï-xyz");
  });

  test("UnmatchedKeyError", () => {
    const e = new UnmatchedKeyError("shop.app.countries", 2);
    expect(e.name).toBe("UnmatchedKeyError");
    expect([e.entity, e.index]).toEqual(["shop.app.countries", 2]);
    expect(e.message).toBe(
      "dql: change 2 for shop.app.countries matches no row exactly, and its key's collation may match one the client cannot compare",
    );
  });
});
