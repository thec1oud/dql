import { describe, expect, test } from "bun:test";
import { parseArray, parseComposite, parseMultirange, parseRange } from "../src/literal.js";

describe("literals", () => {
  test("parse what Postgres writes", () => {
    expect(parseArray('{ 1 , "a\\"b" ,NULL}')).toEqual(["1", 'a"b', null]);
    expect(parseComposite('("a""b"c,\\,x,)')).toEqual(['a"bc', ",x", null]);
    expect(parseComposite('("")')).toEqual([""]);
    expect(parseRange('["a b","c\\"d")')).toEqual({
      empty: false,
      lower: { kind: "inclusive", value: "a b" },
      upper: { kind: "exclusive", value: 'c"d' },
    });
    expect(parseMultirange("{ }")).toEqual([]);
  });

  test("malformed literals throw", () => {
    for (const fn of [
      () => parseArray("1,2"),
      () => parseArray("{1}x"),
      () => parseArray('{"a'),
      () => parseComposite("(1)x"),
      () => parseRange("<1,2)"),
      () => parseRange("[1,2>"),
      () => parseRange("[1,2)x"),
      () => parseMultirange("{[1,2)}x"),
    ]) {
      expect(fn).toThrow(SyntaxError);
    }
  });
});
