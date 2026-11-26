// Assembling statements from the fragment table (design record §16.2,
// §18.4). Generated code builds plans of nodes; the runtime fills holes,
// joins fragments and numbers placeholders. It never writes SQL of its own,
// and never quotes anything.

import type { Consistency } from "./routing.js";
import { type Statement, reread } from "./statement.js";

/** Placeholder is how a dialect writes the n-th placeholder (§16.2): $1 as Postgres does, ? as MySQL, MariaDB and SQLite do, or @p1 as SQL Server does. */
export type Placeholder = "dollar" | "question" | "at";

/** Fragments is a dialect's fragment table, generated from the IR: SQL templates by id, with holes written {0} or {name}. */
export interface Fragments {
  readonly placeholder: Placeholder;
  readonly templates: Readonly<Record<string, string>>;
}

/** Node is what fills a hole or a list: a Use of a fragment, a named Param, an Arg holding a value, or a List. */
export type Node = Use | Param | Arg | List;

/** Use is one fragment of the table, with what fills its holes by name. A hole nothing fills renders as nothing. */
export interface Use {
  readonly node: "use";
  readonly fragment: string;
  readonly holes?: Readonly<Record<string, Node>>;
}

/**
 * Param is a parameter by name, whose value the args give. Left out of the
 * args, it is an optional field the call does not set (§14.2): as an item of
 * a list, such as a VALUES row, it renders the fragment value.default, and a
 * list item holding it elsewhere is dropped, such as a SET assignment.
 */
export interface Param {
  readonly node: "param";
  readonly name: string;
}

/** Arg is a parameter whose value the call gives directly, as composed queries do. */
export interface Arg {
  readonly node: "arg";
  readonly value: unknown;
  /** The value is a sensitive column's, which hooks redact (§24). */
  readonly sensitive?: boolean;
  /** The value's database type, as the dialect names it; see Statement.types. */
  readonly type?: string;
}

/** List is nodes joined by a separator, such as a column list. */
export interface List {
  readonly node: "list";
  readonly items: readonly Node[];
  readonly separator: string;
}

/** use makes a Use of fragment. */
export function use(fragment: string, holes?: Readonly<Record<string, Node>>): Use {
  return holes === undefined ? { node: "use", fragment } : { node: "use", fragment, holes };
}

/** param makes a Param. */
export function param(name: string): Param {
  return { node: "param", name };
}

/** arg makes an Arg. */
export function arg(value: unknown, sensitive = false, type = ""): Arg {
  return { node: "arg", value, sensitive, type };
}

/** list makes a List. */
export function list(items: readonly Node[], separator: string): List {
  return { node: "list", items, separator };
}

/** Args are the values of a statement's named parameters. A name left out is an optional field the call does not set. */
export type Args = Record<string, unknown>;

/** Expression is an assignment's node in the args, which renders in place of the parameter (§20.2). */
export class Expression {
  constructor(readonly plan: Node) {}
}

/** Assembled is a statement's SQL with its arguments, and their database types, in placeholder order. */
export interface Assembled {
  readonly sql: string;
  readonly args: unknown[];
  readonly sensitive: boolean[];
  readonly types: string[];
}

/** Absent marks a fragment that uses a parameter the call left out. */
class Absent extends Error {}

/**
 * assemble joins plan's parts, in order with nothing between them, filling
 * holes from args. sensitive names the named parameters that hold sensitive
 * values, and types gives their database types. A fragment the table lacks,
 * or a parameter left out where nothing can stand in for it, is an error:
 * generated code only builds plans the table anticipates (§16.2).
 */
export function assemble(
  t: Fragments,
  plan: readonly Node[],
  args: Args,
  sensitive: Readonly<Record<string, boolean>> = {},
  types: Readonly<Record<string, string>> = {},
): Assembled {
  const a = new Assembler(t, args, sensitive, types);
  for (const n of plan) {
    try {
      a.node(n);
    } catch (e) {
      throw e instanceof Absent ? new Error(`dql: ${e.message}`) : e;
    }
  }
  return { sql: a.sql, args: a.out, sensitive: a.sens, types: a.typ };
}

class Assembler {
  sql = "";
  readonly out: unknown[] = [];
  readonly sens: boolean[] = [];
  readonly typ: string[] = [];

  constructor(
    private readonly t: Fragments,
    private readonly args: Args,
    private readonly sensitive: Readonly<Record<string, boolean>>,
    private readonly types: Readonly<Record<string, string>>,
  ) {}

  node(n: Node): void {
    switch (n.node) {
      case "use":
        this.use(n);
        return;
      case "param": {
        if (!Object.hasOwn(this.args, n.name)) {
          throw new Absent(`parameter ${n.name} is left out: absent`);
        }
        const v = this.args[n.name];
        if (v instanceof Expression) {
          this.node(v.plan);
          return;
        }
        this.arg(v, this.sensitive[n.name] === true, this.types[n.name] ?? "");
        return;
      }
      case "arg":
        this.arg(n.value, n.sensitive === true, n.type ?? "");
        return;
      case "list":
        this.list(n);
    }
  }

  private arg(v: unknown, sensitive: boolean, type: string): void {
    this.out.push(v);
    this.sens.push(sensitive);
    this.typ.push(type);
    switch (this.t.placeholder) {
      case "question":
        this.sql += "?";
        return;
      case "at":
        this.sql += `@p${this.out.length}`;
        return;
      default:
        this.sql += `$${this.out.length}`;
    }
  }

  private list(l: List): void {
    let first = true;
    for (const item of l.items) {
      const sql = this.sql.length;
      const args = this.out.length;
      if (!first) {
        this.sql += l.separator;
      }
      if (item.node === "param" && !Object.hasOwn(this.args, item.name)) {
        this.use(use("value.default"));
        first = false;
        continue;
      }
      try {
        this.node(item);
      } catch (e) {
        if (!(e instanceof Absent)) {
          throw e;
        }
        // Drop the item, which may have rendered in part.
        this.sql = this.sql.slice(0, sql);
        this.out.length = args;
        this.sens.length = args;
        this.typ.length = args;
        continue;
      }
      first = false;
    }
  }

  private use(u: Use): void {
    if (!Object.hasOwn(this.t.templates, u.fragment)) {
      throw new Error(`dql: the fragment table has no ${u.fragment}`);
    }
    let tmpl = this.t.templates[u.fragment] as string;
    for (;;) {
      const i = tmpl.indexOf("{");
      if (i < 0) {
        break;
      }
      const j = tmpl.indexOf("}", i);
      const name = j < 0 ? "" : tmpl.slice(i + 1, j);
      if (!isHole(name)) {
        this.sql += tmpl.slice(0, i + 1);
        tmpl = tmpl.slice(i + 1);
        continue;
      }
      this.sql += tmpl.slice(0, i);
      const n = u.holes?.[name];
      if (n !== undefined) {
        this.node(n);
      }
      tmpl = tmpl.slice(j + 1);
    }
    this.sql += tmpl;
  }
}

/** isHole reports whether s names a hole: lower-case letters, digits and underscores. */
function isHole(s: string): boolean {
  return /^[a-z0-9_]+$/.test(s);
}

/** Query is a query of the IR's query layer that generated code runs (§16.3): a builder query's plan, or a query file's text as one fragment. */
export interface Query {
  /** The query's name, such as user.get.by_primary. */
  readonly name: string;
  readonly catalog: string;
  /** For hooks, such as select or insert (§24). */
  readonly kind: string;
  readonly plan: readonly Node[];
  readonly writes?: boolean;
  readonly atMostOne?: boolean;
  readonly consistency?: Consistency;
  /** The named parameters that hold sensitive values (§24). */
  readonly sensitive?: Readonly<Record<string, boolean>>;
  /** The database types of the named parameters, as the dialect names them; a list parameter's is its array type. */
  readonly types?: Readonly<Record<string, string>>;
  /** Reads back the row a write returns on a dialect without RETURNING, after the write and in its transaction (§20.1): a plan over the write's own parameters. */
  readonly reread?: readonly Node[];
  /** The parameter the last insert ID fills when the call leaves it out. */
  readonly generated?: string;
}

/** assembled assembles plan, one of q's, with args. */
function assembled(q: Query, t: Fragments, plan: readonly Node[], args: Args): Assembled {
  try {
    return assemble(t, plan, args, q.sensitive, q.types);
  } catch (e) {
    throw new Error(`${(e as Error).message} in query ${q.name}`);
  }
}

/** statementOf assembles q with args into the statement the executor runs. */
export function statementOf(q: Query, t: Fragments, args: Args = {}): Statement {
  const a = assembled(q, t, Object.hasOwn(t.templates, "value.default") ? q.plan : omitUnset(q.plan, args), args);
  const back = q.reread;
  return {
    ...(back === undefined
      ? {}
      : {
          // The read runs as a write, in the write's transaction on the primary.
          [reread]: (id: bigint): Statement => {
            const g = q.generated;
            const r = assembled(q, t, back, g !== undefined && !(g in args) ? { ...args, [g]: id } : args);
            return { query: q.name, catalog: q.catalog, sql: r.sql, args: r.args, writes: true, atMostOne: true, kind: "select", sensitive: r.sensitive, types: r.types };
          },
        }),
    query: q.name,
    catalog: q.catalog,
    sql: a.sql,
    args: a.args,
    writes: q.writes ?? false,
    atMostOne: q.atMostOne ?? false,
    ...(q.consistency === undefined ? {} : { consistency: q.consistency }),
    kind: q.kind,
    sensitive: a.sensitive,
    types: a.types,
  };
}

/**
 * omitUnset leaves the columns args does not set out of an insert's column
 * list and VALUES rows, for a dialect without value.default: the column then
 * takes its default. A multi-row insert's rows set the same columns, as
 * insertMany groups them so, so the first row's say which.
 */
function omitUnset(plan: readonly Node[], args: Args): Node[] {
  return plan.map((node) => {
    const cols = node.node === "use" ? node.holes?.["cols"] : undefined;
    const rows = node.node === "use" ? node.holes?.["rows"] : undefined;
    if (node.node !== "use" || cols?.node !== "list" || rows === undefined) {
      return node;
    }
    const first = (rows.node === "list" ? rows.items[0] : rows) as Use;
    const keep = (first.holes?.["0"] as List).items.map((it) => it.node !== "param" || Object.hasOwn(args, it.name));
    const kept = (l: List): List => list(l.items.filter((_, i) => keep[i]), l.separator);
    const keptRow = (r: Node): Use => use((r as Use).fragment, { "0": kept((r as Use).holes?.["0"] as List) });
    return use(node.fragment, { ...node.holes, cols: kept(cols), rows: rows.node === "list" ? list(rows.items.map(keptRow), rows.separator) : keptRow(rows) });
  });
}
