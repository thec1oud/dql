// Values between a scenario's YAML and the generated code: decode builds a
// value of a declared type from YAML, as Go's harness does through
// encoding/json, and encode turns a result into the JSON Go's harness
// compares, with every key spelled by norm.

import { Decimal, Row, Temporal, parseUuid } from "@dql/runtime";
import { type Field, type Type, Types, norm } from "./types.ts";

/** Json is a YAML or JSON value, as the harness compares them. */
export type Json = null | boolean | number | string | Json[] | { [key: string]: Json };

/** isObject reports whether v is a JSON object. */
export function isObject(v: unknown): v is { [key: string]: Json } {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

/** normalize respells v's keys by norm. */
export function normalize(v: unknown): Json {
  if (Array.isArray(v)) {
    return v.map(normalize);
  }
  if (isObject(v)) {
    return Object.fromEntries(Object.entries(v).map(([k, e]) => [norm(k), normalize(e)]));
  }
  return v as Json;
}

/** encode turns a value of the generated code into its JSON, normalized: integers and decimals as Go writes them, and time values in their ISO forms, a zoned time without its zone's name, as Go writes one. */
export function encode(v: unknown): Json {
  switch (typeof v) {
    case "undefined":
      return null;
    case "bigint":
      return Number(v);
    case "object":
      if (v === null) {
        return null;
      }
      if (Array.isArray(v)) {
        return v.map(encode);
      }
      if (v instanceof Row) {
        return v.values.map(encode);
      }
      if (v instanceof Temporal.ZonedDateTime) {
        return v.toString({ timeZoneName: "never" });
      }
      if (v instanceof Decimal || String(Object.getPrototypeOf(v)?.[Symbol.toStringTag]).startsWith("Temporal.")) {
        return String(v);
      }
      return Object.fromEntries(Object.entries(v).map(([k, e]) => [norm(k), encode(e)]));
  }
  return v as Json;
}

/** Fill gives the fields a scenario may leave out of an object of a type, by type and field, each a function that makes a value. */
export type Fill = Readonly<Record<string, Readonly<Record<string, () => unknown>>>>;

/** Decoder builds values of a package's types from YAML, giving the fields fill names when a value leaves them out. */
export class Decoder {
  readonly types: Types;
  readonly fill: Fill;

  constructor(types: Types, fill: Fill = {}) {
    this.types = types;
    this.fill = fill;
  }

  /** decode builds a value of type t from v, whose keys are spelled by norm. */
  decode(v: Json, t: Type, name = typeName(t)): unknown {
    t = this.types.resolve(t);
    switch (t.k) {
      case "literal":
        if (v !== t.value) {
          throw new Error(`want "${t.value}", got ${show(v)}`);
        }
        return v;
      case "enum":
        return this.decode(v, { k: "name", name: "string", args: [] });
      case "array":
        if (!Array.isArray(v)) {
          throw new Error(`want a list for ${name}, got ${show(v)}`);
        }
        return v.map((e, i) => at(i, () => this.decode(e, t.elem)));
      case "object":
        return this.object(v, t.fields, name);
      case "union":
        return this.union(v, t.of, name);
      case "intersection":
        if (t.of.some((m) => m.k === "name" && m.name === "string")) {
          return this.decode(v, { k: "name", name: "string", args: [] });
        }
        return this.object(
          v,
          t.of.flatMap((m) => this.fieldsOf(m)),
          name,
        );
    }
    return this.named(v, t.name, t.args, name);
  }

  /** fieldsOf returns the fields of an object type, an interface, or an intersection of them. */
  fieldsOf(t: Type): Field[] {
    t = this.types.resolve(t);
    switch (t.k) {
      case "object":
        return [...t.fields];
      case "intersection":
        return t.of.flatMap((m) => this.fieldsOf(m));
      case "name":
        return this.types.fields(t.name);
    }
    throw new Error(`${typeName(t)} has no fields`);
  }

  private named(v: Json, type: string, args: readonly Type[], name: string): unknown {
    switch (type) {
      case "string":
      case "number":
      case "boolean":
        if (typeof v !== type) {
          throw new Error(`want a ${type}, got ${show(v)}`);
        }
        return v;
      case "null":
        if (v !== null) {
          throw new Error(`want null, got ${show(v)}`);
        }
        return null;
      case "bigint":
        if (!(typeof v === "number" && Number.isInteger(v)) && !(typeof v === "string" && /^-?\d+$/.test(v))) {
          throw new Error(`want an integer, got ${show(v)}`);
        }
        return BigInt(v);
      case "Decimal":
        if (typeof v !== "number" && typeof v !== "string") {
          throw new Error(`want a decimal, got ${show(v)}`);
        }
        return Decimal.parse(String(v));
      case "Uuid":
        return parseUuid(this.decode(v, { k: "name", name: "string", args: [] }) as string);
      case "JsonValue":
        return v;
      case "Keyed":
        return this.object(
          v,
          [
            { name: "key", optional: false, type: args[0] as Type },
            { name: "value", optional: false, type: args[1] as Type },
          ],
          name,
        );
    }
    if (type.startsWith("Temporal.")) {
      const cls = (Temporal as unknown as Record<string, { from(s: string): unknown }>)[type.slice("Temporal.".length)];
      if (cls === undefined || typeof v !== "string") {
        throw new Error(`want a ${type}, got ${show(v)}`);
      }
      // An instant with its offset, as SQL Server's datetimeoffset holds one,
      // is written as an RFC 3339 time, which names no time zone: it is at
      // its own offset, Z being +00:00.
      const offset = /(Z|[+-]\d{2}:\d{2})$/.exec(v)?.[1];
      if (type === "Temporal.ZonedDateTime" && offset !== undefined) {
        return Temporal.Instant.from(v).toZonedDateTimeISO(offset === "Z" ? "+00:00" : offset);
      }
      return cls.from(v);
    }
    return this.object(v, this.types.fields(type), name);
  }

  private object(v: Json, fields: readonly Field[], name: string): Record<string, unknown> {
    // null leaves an object empty, as encoding/json leaves a struct.
    v ??= {};
    if (!isObject(v)) {
      throw new Error(`want an object for ${name}, got ${show(v)}`);
    }
    const out: Record<string, unknown> = {};
    const fill = this.fill[name] ?? {};
    for (const f of fields) {
      const x = v[norm(f.name)];
      const make = fill[f.name];
      if (x !== undefined) {
        out[f.name] = at(f.name, () => this.decode(x, f.type));
      } else if (make !== undefined) {
        out[f.name] = make();
      }
    }
    return out;
  }

  /**
   * union decodes a nullable type, or a relation's change union, whose YAML
   * form is an object of one key, its kind, holding the change's key, draft
   * or partial, or for an update by key both, as { key, input }.
   */
  private union(v: Json, of: readonly Type[], name: string): unknown {
    const members = of.filter((m) => !(m.k === "name" && (m.name === "null" || m.name === "undefined")));
    if (v === null && members.length < of.length) {
      return null;
    }
    if (members.length === 1) {
      return this.decode(v, members[0] as Type, name);
    }
    if (members.every((m) => m.k === "literal")) {
      if (!members.some((m) => m.value === v)) {
        throw new Error(`want one of ${members.map((m) => m.value).join(", ")}, got ${show(v)}`);
      }
      return v;
    }
    const kinds = new Map<string, readonly Field[]>();
    for (const m of members) {
      let fields: Field[] = [];
      try {
        fields = this.fieldsOf(m);
      } catch {
        // Not an object, so not a change.
      }
      const kind = fields.find((f) => f.name === "kind")?.type;
      if (kind?.k !== "literal") {
        throw new Error(`${name} is a union this harness cannot build`);
      }
      kinds.set(kind.value, fields.filter((f) => f.name !== "kind"));
    }
    const entries = isObject(v) ? Object.entries(v) : [];
    if (entries.length !== 1) {
      throw new Error(`a ${name} is an object of one of ${[...kinds.keys()].sort().join(", ")}`);
    }
    const [kind, arg] = entries[0] as [string, Json];
    const fields = kinds.get(kind);
    if (fields === undefined) {
      throw new Error(`a ${name} cannot ${kind}`);
    }
    return at(kind, () => {
      const out: Record<string, unknown> = { kind };
      if (fields.length === 1) {
        const f = fields[0] as Field;
        out[f.name] = this.decode(arg, f.type);
      } else if (fields.length === 2) {
        const m = isObject(arg) ? arg : {};
        const [key, partial] = fields as [Field, Field];
        if (m["key"] !== undefined) {
          out[key.name] = this.decode(m["key"], key.type);
        }
        out[partial.name] = this.decode(m["input"] ?? {}, partial.type);
      }
      return out;
    });
  }
}

/** at runs f, prefixing the message of an error it throws with where. */
function at<T>(where: string | number, f: () => T): T {
  try {
    return f();
  } catch (e) {
    throw new Error(`${where}: ${(e as Error).message}`);
  }
}

/** typeName names a type in a message. */
export function typeName(t: Type): string {
  return t.k === "name" || t.k === "enum" ? t.name : t.k;
}

/** show writes v in a message. */
export function show(v: unknown): string {
  return typeof v === "string" ? v : JSON.stringify(v);
}
