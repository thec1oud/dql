// Turning dql's host values into what Postgres reads. Every value is sent as
// text, or bytea as bytes, so node-postgres passes it through as it is: the
// server describes each statement, prepared or not, and reads each value as
// its parameter's type (§26.4).

import { Row, Temporal } from "@dql/runtime";
import { quote } from "./literal.js";

/** isRange reports whether v is a Range of the runtime. */
function isRange(v: object): v is { empty: true } | { empty: false; lower: Bound; upper: Bound } {
  return "empty" in v && typeof v.empty === "boolean" && (v.empty || ("lower" in v && "upper" in v));
}

type Bound = { kind: "unbounded" } | { kind: "inclusive" | "exclusive"; value: unknown };

/** literal writes a non-null value as the text Postgres reads, as a whole parameter or inside an array, composite or range literal. */
export function literal(v: unknown): string {
  switch (typeof v) {
    case "string":
      return v;
    case "number":
    case "bigint":
    case "boolean":
      return String(v);
  }
  if (v instanceof Uint8Array) {
    return `\\x${Buffer.from(v.buffer, v.byteOffset, v.byteLength).toString("hex")}`;
  }
  if (v instanceof Date) {
    return v.toISOString();
  }
  if (v instanceof Temporal.ZonedDateTime) {
    return v.toString({ timeZoneName: "never" });
  }
  if (v instanceof Row) {
    return `(${v.values.map((x) => (x === null || x === undefined ? "" : quote(literal(x)))).join(",")})`;
  }
  if (Array.isArray(v)) {
    return `{${v.map((x) => (x === null || x === undefined ? "NULL" : Array.isArray(x) ? literal(x) : quote(literal(x)))).join(",")}}`;
  }
  if (typeof v === "object" && v !== null) {
    if (isRange(v)) {
      return rangeLiteral(v);
    }
    if (typeof v.toString === "function" && v.toString !== Object.prototype.toString) {
      // Decimal, OffsetTime and the other Temporal types write what Postgres reads.
      return v.toString();
    }
  }
  return JSON.stringify(v);
}

function rangeLiteral(r: { empty: true } | { empty: false; lower: Bound; upper: Bound }): string {
  if (r.empty) {
    return "empty";
  }
  const bound = (b: Bound) => (b.kind === "unbounded" ? "" : quote(literal(b.value)));
  return `${r.lower.kind === "inclusive" ? "[" : "("}${bound(r.lower)},${bound(r.upper)}${r.upper.kind === "inclusive" ? "]" : ")"}`;
}

/** jsonTypes are the types whose values are JSON documents, and their array types. */
const jsonTypes = new Set(["pg_catalog.json", "pg_catalog.jsonb", "json", "jsonb"]);
const jsonArrays = new Set(["pg_catalog._json", "pg_catalog._jsonb", "_json", "_jsonb"]);

/**
 * encode writes one parameter. type is its declared database type, such as
 * pg_catalog.jsonb, or "" where it is not known, as for raw SQL: a value of
 * a JSON type is written as JSON, whatever its JavaScript type, and a list of
 * ranges of a multirange type as a multirange.
 */
export function encode(v: unknown, type: string): string | Buffer | null {
  if (v === null || v === undefined) {
    return null;
  }
  if (jsonTypes.has(type)) {
    return JSON.stringify(v);
  }
  if (jsonArrays.has(type) && Array.isArray(v)) {
    return literal(v.map((x) => (x === null ? null : JSON.stringify(x))));
  }
  if (type.endsWith("multirange") && Array.isArray(v)) {
    return `{${v.map((r) => literal(r)).join(",")}}`;
  }
  if (v instanceof Uint8Array) {
    return Buffer.from(v.buffer, v.byteOffset, v.byteLength);
  }
  return literal(v);
}

/** encodeArgs writes a statement's parameters. */
export function encodeArgs(args: readonly unknown[], types: readonly string[] | undefined): (string | Buffer | null)[] {
  return args.map((v, i) => encode(v, types?.[i] ?? ""));
}
