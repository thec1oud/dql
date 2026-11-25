// Turning the text Postgres writes for each value into the driver-neutral
// form of Row (@dql/runtime). Every value is read as text, so the adapter
// needs no binary codecs: the built-in types are parsed here, and a type the
// catalog defines, such as an enum, a composite type, a domain, a range or an
// array, is looked up once per pool by its OID.

import { Decimal, OffsetTime, Row, Temporal } from "@dql/runtime";
import { type ArrayText, type BoundText, parseArray, parseComposite, parseMultirange, parseRange } from "./literal.js";

/** TypeInfo is what decoding a value of one type needs. */
export type TypeInfo =
  | { readonly kind: "scalar"; readonly parse: (s: string) => unknown }
  | { readonly kind: "array"; readonly elem: number }
  | { readonly kind: "composite"; readonly attrs: readonly number[] }
  | { readonly kind: "domain"; readonly base: number }
  | { readonly kind: "range"; readonly sub: number }
  | { readonly kind: "multirange"; readonly sub: number };

const text: TypeInfo = { kind: "scalar", parse: (s) => s };

/** attempt returns what parse returns, or s when it throws: a value with no host type, such as an infinite date or a numeric NaN, stays text, which a generated decoder rejects as off type (§13). */
function attempt(parse: (s: string) => unknown): TypeInfo {
  return {
    kind: "scalar",
    parse: (s) => {
      try {
        return parse(s);
      } catch {
        return s;
      }
    },
  };
}

/** bytes parses bytea's hex form, \x0a1b. */
function bytes(s: string): Uint8Array {
  if (!s.startsWith("\\x")) {
    throw new SyntaxError("dqlpg: bytea not in hex form");
  }
  return Uint8Array.from(Buffer.from(s.slice(2), "hex"));
}

/** decimal parses a numeric; NaN and the infinities throw. */
const decimal = (s: string) => Decimal.parse(s);

/** money parses money as Postgres writes it for a locale with a decimal point, such as C or en_US: -$1,234.50. */
const money = (s: string) => Decimal.parse(s.replace(/[$,]/g, ""));

/** time parses a time; 24:00:00 throws. */
const time = (s: string) => Temporal.PlainTime.from(s);

/** offsetTime parses a timetz, whose offset may be whole hours, as in 12:34:56+02. */
const offsetTime = (s: string) => OffsetTime.parse(/[+-]\d\d$/.test(s) ? `${s}:00` : s);

/** float parses a float4 or float8, NaN and the infinities included. */
const float = (s: string) => (s === "Infinity" ? Number.POSITIVE_INFINITY : s === "-Infinity" ? Number.NEGATIVE_INFINITY : Number(s));

/** The built-in types, by OID. A type that is neither here nor in the catalog's answer is text. */
export const builtins: ReadonlyMap<number, TypeInfo> = new Map<number, TypeInfo>([
  [16, { kind: "scalar", parse: (s) => s === "t" }],
  [17, attempt(bytes)],
  [20, { kind: "scalar", parse: (s) => BigInt(s) }],
  [21, { kind: "scalar", parse: Number }],
  [23, { kind: "scalar", parse: Number }],
  [26, { kind: "scalar", parse: Number }],
  [114, { kind: "scalar", parse: (s) => JSON.parse(s) }],
  [3802, { kind: "scalar", parse: (s) => JSON.parse(s) }],
  [700, { kind: "scalar", parse: float }],
  [701, { kind: "scalar", parse: float }],
  [790, attempt(money)],
  [1700, attempt(decimal)],
  [1082, attempt((s) => Temporal.PlainDate.from(s))],
  [1083, attempt(time)],
  [1114, attempt((s) => Temporal.PlainDateTime.from(s))],
  [1184, attempt((s) => Temporal.Instant.from(s))],
  [1186, attempt((s) => Temporal.Duration.from(s))],
  [1266, attempt(offsetTime)],
  [3904, { kind: "range", sub: 23 }],
  [3926, { kind: "range", sub: 20 }],
  [3906, { kind: "range", sub: 1700 }],
  [3912, { kind: "range", sub: 1082 }],
  [3908, { kind: "range", sub: 1114 }],
  [3910, { kind: "range", sub: 1184 }],
  [4451, { kind: "multirange", sub: 23 }],
  [4536, { kind: "multirange", sub: 20 }],
  [4532, { kind: "multirange", sub: 1700 }],
  [4535, { kind: "multirange", sub: 1082 }],
  [4533, { kind: "multirange", sub: 1114 }],
  [4534, { kind: "multirange", sub: 1184 }],
]);

/** Types maps OIDs to what decoding their values needs: the built-in types, and those a pool has looked up. */
export class Types {
  private readonly known = new Map<number, TypeInfo>(builtins);
  /** The OIDs the catalog has answered for, holding a type or not. */
  private readonly asked = new Set<number>(builtins.keys());

  /** missing returns which of oids, and of the types they name, the catalog has not been asked about. */
  missing(oids: Iterable<number>): number[] {
    const out = new Set<number>();
    const walk = (oid: number) => {
      if (!this.asked.has(oid)) {
        out.add(oid);
        return;
      }
      const t = this.known.get(oid);
      switch (t?.kind) {
        case "array":
          walk(t.elem);
          break;
        case "composite":
          t.attrs.forEach(walk);
          break;
        case "domain":
          walk(t.base);
          break;
        case "range":
        case "multirange":
          walk(t.sub);
          break;
      }
    };
    for (const oid of oids) {
      walk(oid);
    }
    return [...out];
  }

  /** learn records the catalog's answer for the asked OIDs: each row describes one type; an OID with no row is text. */
  learn(asked: readonly number[], rows: readonly CatalogRow[]): void {
    for (const oid of asked) {
      this.asked.add(oid);
    }
    for (const r of rows) {
      const oid = Number(r.oid);
      if (r.typcategory === "A" && Number(r.typelem) !== 0) {
        this.known.set(oid, { kind: "array", elem: Number(r.typelem) });
      } else if (r.typtype === "c") {
        this.known.set(oid, { kind: "composite", attrs: r.attrs.map(Number) });
      } else if (r.typtype === "d") {
        this.known.set(oid, { kind: "domain", base: Number(r.typbasetype) });
      } else if (r.typtype === "r") {
        this.known.set(oid, { kind: "range", sub: Number(r.rngsub) });
      } else if (r.typtype === "m") {
        this.known.set(oid, { kind: "multirange", sub: Number(r.mrsub) });
      }
    }
  }

  /** decode turns the text s of a value of type oid into its Row form, null for NULL. */
  decode(oid: number, s: string | null): unknown {
    if (s === null) {
      return null;
    }
    const t = this.known.get(oid) ?? text;
    switch (t.kind) {
      case "scalar":
        return t.parse(s);
      case "domain":
        return this.decode(t.base, s);
      case "array":
        return this.elements(t.elem, parseArray(s));
      case "composite": {
        const attrs = parseComposite(s);
        return new Row(t.attrs.map((a, i) => this.decode(a, attrs[i] ?? null)));
      }
      case "range":
        return this.range(t.sub, parseRange(s));
      case "multirange":
        return parseMultirange(s).map((r) => this.range(t.sub, r));
    }
  }

  private elements(elem: number, a: ArrayText): unknown[] {
    return a.map((x) => (Array.isArray(x) ? this.elements(elem, x) : this.decode(elem, x)));
  }

  private range(sub: number, r: ReturnType<typeof parseRange>): unknown {
    if (r.empty) {
      return { empty: true };
    }
    return { empty: false, lower: this.bound(sub, r.lower), upper: this.bound(sub, r.upper) };
  }

  private bound(sub: number, b: BoundText): unknown {
    return b.kind === "unbounded" ? b : { kind: b.kind, value: this.decode(sub, b.value) };
  }
}

/** CatalogRow is one row of CATALOG_QUERY. */
export interface CatalogRow {
  readonly oid: string;
  readonly typtype: string;
  readonly typcategory: string;
  readonly typelem: string;
  readonly typbasetype: string;
  readonly rngsub: string;
  readonly mrsub: string;
  readonly attrs: readonly string[];
}

/** CATALOG_QUERY describes the types whose OIDs $1 lists. */
export const CATALOG_QUERY = `SELECT t.oid::text AS oid, t.typtype::text AS typtype, t.typcategory::text AS typcategory,
  t.typelem::text AS typelem, t.typbasetype::text AS typbasetype,
  coalesce(r.rngsubtype, 0)::text AS rngsub, coalesce(m.rngsubtype, 0)::text AS mrsub,
  coalesce((SELECT array_agg(a.atttypid::text ORDER BY a.attnum) FROM pg_catalog.pg_attribute a
    WHERE a.attrelid = t.typrelid AND a.attnum > 0 AND NOT a.attisdropped), '{}') AS attrs
FROM pg_catalog.pg_type t
LEFT JOIN pg_catalog.pg_range r ON r.rngtypid = t.oid
LEFT JOIN pg_catalog.pg_range m ON m.rngmultitypid = t.oid
WHERE t.oid = ANY($1::oid[])`;
