// The harness's parts on their own: the declarations it reads, the values it
// builds, the outcomes it compares, and the steps that need no database.

import { expect, test } from "bun:test";
import { mkdir, mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Decimal, Temporal } from "@dql/runtime";
import { compare, diff, mask } from "../src/compare.ts";
import { type Schema, type Step, duration, equal, errorFields, generate, load, member, message, run } from "../src/harness.ts";
import { type Type, Types, parseType } from "../src/types.ts";
import { Decoder, encode, normalize } from "../src/values.ts";

const named = (name: string, ...args: Type[]): Type => ({ k: "name", name, args });

test("parseType", () => {
  expect(parseType("readonly string[] | null")).toEqual({ k: "union", of: [{ k: "array", elem: named("string") }, named("null")] });
  expect(parseType("{ readonly a?: string, b: number }")).toEqual({
    k: "object",
    fields: [
      { name: "a", optional: true, type: named("string") },
      { name: "b", optional: false, type: named("number") },
    ],
  });
  expect(parseType("(typeof Mood)[number]")).toEqual({ k: "enum", name: "Mood" });
  expect(parseType("(A | B)[]")).toEqual({ k: "array", elem: { k: "union", of: [named("A"), named("B")] } });
  expect(parseType("Map<K, V>  ")).toEqual(named("Map", named("K"), named("V")));
  for (const [text, err] of [
    ["A $ B", "cannot read type A $ B"],
    ["Map<K", "type Map<K ends early"],
    ["(A]", "type (A]: want ), got ]"],
    ["A[number]", "an index of a type that is not an enum"],
    [";", "type ;: unexpected ;"],
    ["A B", "type A B: unexpected B"],
  ] as const) {
    expect(() => parseType(text)).toThrow(err);
  }
});

test("Types", () => {
  const t = new Types();
  t.add("export interface A {\n  a: string;\n}\nexport interface B extends A {\n  b?: number; // a comment\n  m(x: A, ...as: B[]): void;\n}\n");
  expect(t.fields("B").map((f) => f.name)).toEqual(["a", "b"]);
  expect(t.method("B", "m")?.params.map((p) => [p.name, p.rest])).toEqual([
    ["x", false],
    ["as", true],
  ]);
  expect(t.method("B", "nope")).toBeUndefined();
  expect(() => t.decl("C")).toThrow("no type C");
  expect(() => t.add("export interface C {\n  +;\n}\n")).toThrow("unknown member:   +;");
});

const types = new Types();
types.add(`export interface Row {
  id: bigint;
  at?: Temporal.Instant;
  kind?: Kind;
}
export type Kind = "a" | "b";
export type Change =
  | { kind: "make"; draft: Row }
  | { kind: "edit"; key?: Row; partial: Row }
  | { kind: "drop" };
export type Odd = string | number;
export type Mixed = { kind: string } | { kind: "x" };
`);
const d = new Decoder(types);

test("decode", () => {
  expect(d.decode("a", { k: "literal", value: "a" })).toBe("a");
  expect(() => d.decode("b", { k: "literal", value: "a" })).toThrow('want "a", got b');
  expect(d.decode("x", { k: "enum", name: "Mood" })).toBe("x");
  expect(() => d.decode(1, { k: "array", elem: named("string") })).toThrow("want a list for array, got 1");
  expect(() => d.decode([1], { k: "array", elem: named("string") })).toThrow("0: want a string, got 1");
  expect(d.decode({ a: "x" }, parseType("{ a: string } & { b?: number }"))).toEqual({ a: "x" });
  expect(d.decode({ a: "x" }, parseType("{ a: string }"))).toEqual({ a: "x" });
  expect(d.decode("s", parseType("string & { readonly __domain: \"d\" }"))).toBe("s");
  expect(() => d.fieldsOf({ k: "literal", value: "a" })).toThrow("literal has no fields");
  expect(d.fieldsOf(parseType("{ a: string } & Row")).map((f) => f.name)).toEqual(["a", "id", "at", "kind"]);
  expect(d.decode(null, named("null"))).toBeNull();
  expect(() => d.decode(1, named("null"))).toThrow("want null, got 1");
  expect(d.decode("12", named("bigint"))).toBe(12n);
  expect(() => d.decode(1.5, named("bigint"))).toThrow("want an integer, got 1.5");
  expect(d.decode(1.5, named("Decimal"))).toEqual(Decimal.parse("1.5"));
  expect(() => d.decode(true, named("Decimal"))).toThrow("want a decimal, got true");
  expect(d.decode("0B8D2A3E-1C5F-4D6E-9A7B-8C9D0E1F2A3B", named("Uuid"))).toBe("0b8d2a3e-1c5f-4d6e-9a7b-8c9d0e1f2a3b");
  expect(d.decode({ a: [1] }, named("JsonValue"))).toEqual({ a: [1] });
  expect(d.decode({ key: 1, value: 2 }, named("Keyed", named("bigint"), named("number")))).toEqual({ key: 1n, value: 2 });
  expect(String(d.decode("2026-01-01", named("Temporal.PlainDate")))).toBe("2026-01-01");
  expect(() => d.decode(1, named("Temporal.PlainDate"))).toThrow("want a Temporal.PlainDate, got 1");
  expect(() => d.decode("x", named("Temporal.Nope"))).toThrow("want a Temporal.Nope, got x");
  expect(() => d.decode(1, named("Row"))).toThrow("want an object for Row, got 1");
  expect(d.decode({ id: 1, at: "2026-01-01T00:00:00Z", kind: "a" }, named("Row"))).toEqual({ id: 1n, at: Temporal.Instant.from("2026-01-01T00:00:00Z"), kind: "a" });
  expect(() => d.decode({ kind: "c" }, named("Row"))).toThrow("kind: want one of a, b, got c");
  expect(d.decode({ make: { id: 1 } }, named("Change"))).toEqual({ kind: "make", draft: { id: 1n } });
  expect(d.decode({ edit: { key: { id: 1 }, input: { id: 2 } } }, named("Change"))).toEqual({ kind: "edit", key: { id: 1n }, partial: { id: 2n } });
  expect(d.decode({ edit: {} }, named("Change"))).toEqual({ kind: "edit", partial: {} });
  expect(d.decode({ edit: 1 }, named("Change"))).toEqual({ kind: "edit", partial: {} });
  expect(d.decode({ drop: {} }, named("Change"))).toEqual({ kind: "drop" });
  expect(() => d.decode({ a: 1, b: 2 }, named("Change"))).toThrow("a Change is an object of one of drop, edit, make");
  expect(() => d.decode({ nope: 1 }, named("Change"))).toThrow("a Change cannot nope");
  expect(() => d.decode({ make: 1 }, named("Change"))).toThrow("make: want an object for Row, got 1");
  expect(() => d.decode(1, named("Odd"))).toThrow("Odd is a union this harness cannot build");
  expect(() => d.decode(1, named("Mixed"))).toThrow("Mixed is a union this harness cannot build");
});

test("encode", () => {
  expect(encode(undefined)).toBeNull();
  expect(encode({ aB: [1n, Decimal.parse("1.50"), Temporal.PlainDate.from("2026-01-02"), null] })).toEqual({ ab: [1, "1.50", "2026-01-02", null] });
  expect(normalize({ A_b: [{ C: 1 }] })).toEqual({ ab: [{ c: 1 }] });
});

test("duration", () => {
  expect(duration("1m30.5s")).toBe(90_500);
  expect(duration("250us")).toBe(0.25);
  expect(duration("2h")).toBe(7_200_000);
  expect(() => duration("1d")).toThrow('time: invalid duration "1d"');
});

test("generate", () => {
  expect(generate({ generate: { count: 2, row: { name: "n-$i", size: 3 } } })).toEqual([
    { name: "n-1", size: 3 },
    { name: "n-2", size: 3 },
  ]);
  expect(generate({ generate: { count: 2, value: "v-$i" } })).toEqual(["v-1", "v-2"]);
  expect(generate({ generate: 1 })).toEqual({ generate: 1 });
  expect(() => generate({ generate: {} })).toThrow("generate: count null is not a number");
});

test("equal", () => {
  expect(equal({ a: 1, b: [{ c: 2, d: 3 }] }, { b: [{ d: 3, c: 2 }], a: 1 })).toBe(true);
  expect(equal({ a: 1 }, { a: 2 })).toBe(false);
});

test("member", () => {
  expect(member(1, "x")).toBeUndefined();
  expect(member(null, "x")).toBeUndefined();
  class A {
    fooBar() {
      return 1;
    }
  }
  expect(member(new A(), "foo_bar")?.name).toBe("fooBar");
  expect(member(new A(), "constructor")).toBeUndefined();
});

test("message and errorFields", () => {
  expect(message("x")).toBe("x");
  expect(errorFields(new Error("x"))).toEqual({ class: "" });
});

test("mask, diff and compare", async () => {
  const uuids = new Map<string, number>();
  const a = "0b8d2a3e-1c5f-4d6e-9a7b-8c9d0e1f2a3b";
  expect(mask({ b: null, a: [a, "2026-01-01T00:00:00.5Z", "x", 1, a, "1b8d2a3e-1c5f-4d6e-9a7b-8c9d0e1f2a3b"] }, uuids)).toEqual({ a: ["<uuid 1>", "<time>", "x", 1, "<uuid 1>", "<uuid 2>"] });
  const o = { step: "1", op: "x", result: 1, events: [] };
  expect(diff("s", [o], [o])).toEqual([]);
  expect(diff("s", [o], [{ ...o, error: { class: "NotFound" } }])[0]).toContain("s: outcome 1 differs:");
  expect(diff("s", [o], [])[0]).toContain("ts: (none)");
  const dir = await mkdtemp(join(tmpdir(), "compare-"));
  for (const [side, outcomes] of [
    ["go", [o]],
    ["ts", [{ ...o, result: 2 }]],
  ] as const) {
    await mkdir(join(dir, side, "prepare"), { recursive: true });
    await writeFile(join(dir, side, "prepare", "s.json"), JSON.stringify(outcomes));
  }
  await writeFile(join(dir, "ts", "prepare", "t.json"), "[]");
  // Another dialect's outcomes are a directory deeper.
  for (const side of ["go", "ts"]) {
    await mkdir(join(dir, side, "mysql", "simple"), { recursive: true });
    await writeFile(join(dir, side, "mysql", "simple", "s.json"), JSON.stringify([o]));
  }
  await writeFile(join(dir, "ts", "mysql", "simple", "u.json"), "[]");
  const got = await compare(join(dir, "go"), join(dir, "ts"));
  expect(got.length).toBe(3);
  expect(got[0]).toContain("mysql/simple/u.json: no Go outcomes");
  expect(got[1]).toContain("prepare/s: outcome 1 differs");
  expect(got[2]).toContain("prepare/t.json: no Go outcomes");
});

test("load", async () => {
  const dir = await mkdtemp(join(tmpdir(), "load-"));
  await writeFile(join(dir, "bad.yaml"), "steps: {");
  await expect(load(dir)).rejects.toThrow("bad.yaml");
});

/** fake is a schema whose statements and replica do nothing, unless fail names them. */
function fake(fail: readonly string[] = []): Schema {
  const act = (name: string) => async () => {
    if (fail.includes(name)) {
      throw new Error("down");
    }
  };
  return { ddl: [], dialect: "postgres", module: {}, types: new Types(), exec: act("exec"), inject: act("inject"), terminate: act("terminate"), replay: act("replay") };
}

/** steps reads steps from YAML. */
function steps(...ss: Step[]): { scenario: string; schema: string; steps: Step[] } {
  return { scenario: "s", schema: "shop", steps: ss };
}

test("steps without a database", async () => {
  // A replay that fails is reported, and a scenario that ends with replay paused resumes it.
  expect(await run(steps({ op: "pause_replay" }), { schema: fake(), db: {} })).toEqual([]);
  expect(await run(steps({ op: "pause_replay" }), { schema: fake(["replay"]), db: {} })).toEqual(["s step 1 (pause_replay): down"]);
  expect(await run(steps({ op: "pause_replay" }, { op: "resume_replay" }), { schema: fake(), db: {} })).toEqual([]);
  let calls = 0;
  const resumeFails: Schema = {
    ...fake(),
    async replay(_dsn, pause) {
      calls++;
      if (!pause) {
        throw new Error("down");
      }
    },
  };
  expect(await run(steps({ op: "pause_replay" }), { schema: resumeFails, db: {} })).toEqual(["s: resume_replay: down"]);
  expect(calls).toBe(2);
  // A termination that fails is reported.
  expect(await run(steps({ op: "nope.count", terminate: "1ms" }), { schema: fake(["terminate"]), db: {} })).toEqual([
    "s step 1 (nope.count): no store nope",
    "s step 1 (nope.count): terminate: down",
  ]);
  // A handle without pair-form transactions has no begin steps, nor one without withScope scoped stores.
  expect(await run(steps({ op: "begin_read" }), { schema: fake(), db: {} })).toEqual(["s step 1 (begin_read): no transaction begin_read"]);
  expect(await run(steps({ op: "users.count", scope: {} }), { schema: fake(), db: {} })).toEqual(["s step 1 (users.count): the handle has no withScope"]);
  // Filtered writes need the store's table.
  const types = new Types();
  types.add("export class Db {\n  readonly tenants: TenantsStore;\n}\nexport interface TenantsStore {\n  deleteWhere(where: Pred<TenantsTable>): Promise<number>;\n}\n");
  class Db {
    tenants = { deleteWhere: async () => 0 };
  }
  expect(await run(steps({ op: "tenants.delete_where", where: "all" }), { schema: { ...fake(), types }, db: new Db() })).toEqual(["s step 1 (tenants.delete_where): no table tenants"]);
});
