// Turning dql's host values into what SQLite stores. SQLite has no time,
// decimal or JSON types, so these are written as text: a decimal, a date, a
// time and JSON as their text, and an instant as ISO 8601 in UTC, which the
// generated decoders read back (§8.10). A bool is 1 or 0. A list, which has
// no parameter type, is one JSON array the dialect expands with json_each
// (§25.3). A weak value is its typed or raw value.

import { Temporal } from "@dql/runtime";

/** Value is a parameter as the bindings take it. */
export type Value = string | number | bigint | Uint8Array | null;

/** isJsonType reports whether a database type, such as json, holds JSON documents. */
const isJsonType = (type: string) => /(?:^|\.)json$/i.test(type);

/** isObject reports whether v is a plain object or an array: a JSON value. */
const isJson = (v: unknown) => Array.isArray(v) || (typeof v === "object" && v !== null && v.constructor === Object);

/** weakValue returns the value a weak value or a raw storage value holds, or v itself. */
function weakValue(v: unknown): unknown {
  if (typeof v === "object" && v !== null && v.constructor === Object) {
    const w = v as { ok?: unknown; value?: unknown; raw?: unknown; class?: unknown };
    if (w.ok === true && "value" in w) {
      return w.value;
    }
    if (w.ok === false && typeof w.raw === "object" && w.raw !== null) {
      return (w.raw as { value: unknown }).value;
    }
    if (typeof w.class === "string" && ["integer", "real", "text", "blob"].includes(w.class) && "value" in w) {
      return w.value;
    }
  }
  return v;
}

/**
 * encode writes one parameter. type is its declared database type, or ""
 * where it is not known, as for raw SQL: a value of a JSON type is written
 * as JSON whatever its JavaScript type.
 */
export function encode(v: unknown, type: string): Value {
  if (v === null || v === undefined) {
    return null;
  }
  if (isJsonType(type)) {
    return JSON.stringify(v);
  }
  if (Array.isArray(v)) {
    return `[${v.map(item).join(",")}]`;
  }
  return scalar(weakValue(v));
}

/** scalar writes a value other than a list. */
function scalar(v: unknown): Value {
  switch (typeof v) {
    case "string":
    case "number":
    case "bigint":
      return v;
    case "boolean":
      return v ? 1n : 0n;
  }
  if (v === null || v instanceof Uint8Array) {
    return v as Value;
  }
  if (v instanceof Date) {
    return Temporal.Instant.fromEpochMilliseconds(v.getTime()).toString();
  }
  if (v instanceof Temporal.ZonedDateTime) {
    return v.toInstant().toString();
  }
  if (isJson(v)) {
    return JSON.stringify(v);
  }
  // An instant writes its UTC text, and a decimal, a plain date or time and
  // an OffsetTime their own.
  return String(v);
}

/** item writes one item of a list as JSON: each is encoded as a parameter would be, a blob as its text, and a plain object or array as the JSON value it is. */
function item(v: unknown): string {
  if (v === null || v === undefined) {
    return "null";
  }
  if (isJson(v) && weakValue(v) === v) {
    return JSON.stringify(v);
  }
  const x = scalar(weakValue(v));
  if (typeof x === "bigint") {
    return String(x);
  }
  return JSON.stringify(x instanceof Uint8Array ? new TextDecoder().decode(x) : x);
}

/** encodeArgs writes a statement's parameters. */
export function encodeArgs(args: readonly unknown[], types: readonly string[] | undefined): Value[] {
  return args.map((v, i) => encode(v, types?.[i] ?? ""));
}
