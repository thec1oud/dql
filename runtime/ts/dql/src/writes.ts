// Write expressions, filtered writes and writes by key (design record §20).

import { type Pred, oneOf } from "./builder.js";
import type { Row } from "./decode.js";
import { ConcurrentModification, NotFound } from "./errors.js";
import { type Reader, type Writer, bind } from "./executor.js";
import { type Args, Expression, type Fragments, type Node, type Query, type Use, arg, list, statementOf, use } from "./fragments.js";
import { type Bulk, atomic, chunk, queryBulk, written } from "./large.js";
import { type Statement, execute, executeId, queryAll, queryOptional, reread } from "./statement.js";
import { throwIfIssues, type ValidationIssue } from "./validation.js";

/** entity brands an assignment with the entity it writes. */
declare const entity: unique symbol;

/** exprKind brands an expression with the columns that take it. */
declare const exprKind: unique symbol;

/** Expr is an expression a write assigns to a column in place of a value (§20.2). Shapes stay plain data: expressions travel on the operation. */
export class Expr<K extends "default" | "now" = "default" | "now"> {
  /** Brands it with the columns that take it. */
  declare readonly [exprKind]?: K;

  /** @internal */
  constructor(
    /** @internal */
    readonly plan: Node,
  ) {}
}

/** now is the time the statement's transaction began, as the database tells it; a time column takes it. */
export const now: Expr<"now"> = new Expr(use("value.now"));

/** DEFAULT is the column's default, or null for a column without one; every column takes it. */
export const DEFAULT: Expr<"default"> = new Expr(use("value.default"));

/** Update is an assignment an update takes: set, or arithmetic on the column, such as posts.views.add(1). */
export class Update<E> {
  /** Brands it with its entity, so one entity's cannot stand for another's. */
  declare readonly [entity]?: (e: E) => E;

  /** @internal */
  constructor(
    /** @internal The stored name of the column. */
    readonly field: string,
    /** @internal */
    readonly plan: Node,
  ) {}
}

/** Assignment is an assignment every write takes, an insert included: one that does not read the row it writes. */
export class Assignment<E> extends Update<E> {
  declare private readonly valueOnly: true;
}

/** set assigns e to field, the stored name of a column of E; generated column handles call it. */
export function set<E>(field: string, e: Expr): Assignment<E> {
  return new Assignment<E>(field, e.plan);
}

/** arith assigns to field the value operator fragment op makes of column fragment col and v, as col + v; generated column handles call it. */
export function arith<E>(op: string, field: string, col: string, v: Node): Update<E> {
  return new Update<E>(field, use(op, { "0": use(col), "1": v }));
}

/**
 * assign adds the assignments to args, which holds the payload of shape;
 * generated writes call it before running q. A field the payload also sets
 * is a field_set_twice issue (§20.3), and a field q does not write is an
 * error. prefix names the parameter of a field prefix+field, as an upsert's
 * update side names set.<column> (§14.7).
 */
export function assign<E>(q: Query, shape: string, args: Args, as: readonly Update<E>[], prefix = ""): void {
  const issues: ValidationIssue[] = [];
  for (const a of as) {
    const name = prefix + a.field;
    if (!takes(q.plan, name)) {
      throw new Error(`dql: ${q.name} does not write ${a.field}`);
    }
    if (Object.hasOwn(args, name)) {
      issues.push({ shape, field: a.field, kind: "field_set_twice" });
      continue;
    }
    args[name] = new Expression(a.plan);
  }
  throwIfIssues(issues);
}

/** takes reports whether parameter name appears under the plan's nodes. */
function takes(plan: readonly Node[], name: string): boolean {
  return plan.some((n) => {
    switch (n.node) {
      case "use":
        return takes(Object.values(n.holes ?? {}), name);
      case "list":
        return takes(n.items, name);
      case "param":
        return n.name === name;
      default:
        return false;
    }
  });
}

/** allRows is the filter that keeps every row, which a filtered write must be given explicitly (§20.1); generated tables' allRows call it. */
export function allRows<E>(): Pred<E> {
  return ALL as Pred<E>;
}

/** ALL is the allRows predicate, which has no plan. */
const ALL: Pred<unknown> = Object.freeze({ plan: undefined }) as unknown as Pred<unknown>;

/**
 * filtered returns q with p added to the filter of its WHERE, which holds the
 * scope's and the soft delete's (§20.1); generated filtered writes call it.
 * allRows leaves q as it is; a predicate without a plan is an error, so an
 * empty filter never happens by accident.
 */
export function filtered<E>(q: Query, p: Pred<E>): Query {
  if (p === ALL) {
    return q;
  }
  if (p?.plan === undefined) {
    throw new Error("dql: a filtered write needs a filter; pass the table's allRows() to write every row");
  }
  const [head, ...rest] = q.plan as [Use, ...Node[]];
  const holes: Record<string, Node> = { ...head.holes };
  const where = holes["where"];
  let filter = p.plan;
  if (where !== undefined && where.node === "use") {
    filter = use("bool.and", { "0": (where.holes as Record<string, Node>)["0"] as Node, "1": filter });
  }
  holes["where"] = use("clause.where", { "0": filter });
  return { ...q, plan: [use(head.fragment, holes), ...rest] };
}

/**
 * insertMany inserts every row with q, whose plan writes one VALUES row from
 * named parameters, and decodes the rows it returns, in order (§20.1,
 * §25.2). It repeats the row for each, so the rows split into statements
 * within the dialect's parameter limit, all in one transaction.
 *
 * On a dialect without RETURNING the rows are read back in the same
 * transaction (§20.1). Where every row leaves an auto-increment key to the
 * database and the table has reread.from, each statement's rows are read
 * back at once: a multi-row insert that sets no key takes consecutive
 * values, a step apart, from its last insert ID on. Otherwise each row is
 * inserted and read back on its own, so that its key comes back as its own
 * last insert ID.
 */
export async function insertMany<T>(h: Writer, t: Fragments, q: Query, rows: readonly Args[], decode: (r: Row) => T): Promise<T[]> {
  if (q.reread !== undefined && batched(t, q, rows)) {
    return insertBatched(h, t, q, rows, decode);
  }
  if (q.reread !== undefined) {
    return atomic(h, async (w) => {
      const out: T[] = [];
      for (const r of rows) {
        out.push(await oneOf(w, statementOf(q, t, r), decode));
      }
      return out;
    });
  }
  const gs = groups(t, rows);
  if (gs.length === 1) {
    return (await queryBulk(h, manyRows(t, q, gs[0] as readonly Args[]))).map(decode);
  }
  return atomic(h, async (w) => {
    const out: T[] = [];
    for (const g of gs) {
      out.push(...(await queryBulk(w, manyRows(t, q, g))).map(decode));
    }
    return out;
  });
}

/**
 * groups splits rows into runs that one statement can write. Where the
 * dialect has value.default, a row's unset column takes DEFAULT, so every row
 * writes in one; where it has none, as on SQLite, a statement leaves unset
 * columns out, so each run of rows setting the same columns is written apart
 * (§25.2).
 */
function groups(t: Fragments, rows: readonly Args[]): (readonly Args[])[] {
  if (Object.hasOwn(t.templates, "value.default") || rows.length === 0) {
    return [rows];
  }
  const out: Args[][] = [];
  let run: Args[] = [];
  let key = "";
  for (const r of rows) {
    const k = JSON.stringify(Object.keys(r).sort());
    if (run.length > 0 && k !== key) {
      out.push(run);
      run = [];
    }
    run.push(r);
    key = k;
  }
  out.push(run);
  return out;
}

/** manyRows is the bulk write of rows with q, each statement repeating q's VALUES row for its rows. */
function manyRows(t: Fragments, q: Query, rows: readonly Args[]): Bulk {
  const { reread: _, ...insert } = q;
  return {
    rows: rows.length,
    paramsPerRow: q.plan.reduce((n, node) => n + placeholders(node, false), 0),
    statement: (from, to) => {
      const args: Args = {};
      const sensitive: Record<string, boolean> = {};
      rows.slice(from, to).forEach((r, i) => {
        for (const [k, v] of Object.entries(r)) {
          args[`${k}#${i}`] = v;
        }
        for (const [k, v] of Object.entries(q.sensitive ?? {})) {
          sensitive[`${k}#${i}`] = v;
        }
      });
      return statementOf({ ...insert, plan: q.plan.map((n) => repeat(n, to - from)), sensitive }, t, args);
    },
  };
}

/** batched reports whether insertMany can read rows back by statement: q's key is an auto-increment every row leaves out, its read filters on it, and the table can read from a last insert ID on. */
function batched(t: Fragments, q: Query, rows: readonly Args[]): boolean {
  const g = q.generated;
  return (
    Object.hasOwn(t.templates, "reread.from") &&
    g !== undefined &&
    g !== "" &&
    readFrom(q.reread as readonly Node[], g, 0n, 0, "") !== undefined &&
    rows.every((r) => !(g in r))
  );
}

/** insertBatched runs insertMany's statements, and after each reads back its rows by their keys, in one transaction. */
function insertBatched<T>(h: Writer, t: Fragments, q: Query, rows: readonly Args[], decode: (r: Row) => T): Promise<T[]> {
  const b = manyRows(t, q, rows);
  const g = q.generated as string;
  const { reread: back, ...read } = q;
  return atomic(h, async (w) => {
    const out: T[] = [];
    const size = chunk(b, w[bind]().executor.dialect);
    for (let from = 0; from < rows.length; from += size) {
      const to = Math.min(from + size, rows.length);
      const { affected, id } = await executeId(w, b.statement(from, to));
      const plan = readFrom(back as readonly Node[], g, id, to - from, q.types?.[g] ?? "") as Node[];
      const s: Statement = { ...statementOf({ ...read, plan }, t, rows[from]), writes: true, atMostOne: false, kind: "select" };
      const got = await queryAll(w, s);
      if (got.length !== affected || affected !== to - from) {
        throw new Error(`dql: query ${q.name} inserted ${affected} rows and read back ${got.length} of ${to - from}`);
      }
      out.push(...got.map(decode));
    }
    return out;
  });
}

/**
 * readFrom returns plan, a read by key, reading instead the n rows whose key
 * gen holds the last insert ID id or a later value of its sequence, in key
 * order: its comparison of gen with its parameter becomes reread.from, and an
 * order and a limit follow. It is undefined when plan has no such comparison.
 */
function readFrom(plan: readonly Node[], gen: string, id: bigint, n: number, type: string): Node[] | undefined {
  let col: Node | undefined;
  const out = plan.map((node) => {
    const [replaced, c] = fromId(node, gen, id, type);
    col ??= c;
    return replaced;
  });
  if (col === undefined) {
    return undefined;
  }
  return [...out, use("clause.order", { "0": use("order.asc", { "0": col }) }), use("clause.limit", { "0": arg(BigInt(n)) })];
}

/** fromId replaces the comparison of a column with gen under node by reread.from, and returns the column. */
function fromId(node: Node, gen: string, id: bigint, type: string): [Node, Node | undefined] {
  if (node.node !== "use") {
    return [node, undefined];
  }
  const p = node.holes?.["1"];
  if (node.fragment === "op.eq" && p?.node === "param" && p.name === gen) {
    const col = node.holes?.["0"] as Node;
    return [use("reread.from", { "0": col, "1": arg(id, false, type), "2": arg(id, false, type) }), col];
  }
  let found: Node | undefined;
  const holes = mapHoles(node, (h) => {
    const [replaced, c] = fromId(h, gen, id, type);
    found ??= c;
    return replaced;
  });
  return [use(node.fragment, holes), found];
}

/** placeholders counts the placeholders of the values.row uses under node: its parameters and values. */
function placeholders(node: Node, inRow: boolean): number {
  switch (node.node) {
    case "use":
      return Object.values(node.holes ?? {}).reduce((n, h) => n + placeholders(h, inRow || node.fragment === "values.row"), 0);
    case "list":
      return node.items.reduce((n, it) => n + placeholders(it, inRow), 0);
    default:
      return inRow ? 1 : 0;
  }
}

/** repeat returns node with each values.row use repeated n times, its parameters numbered #0 to #n-1. */
function repeat(node: Node, n: number): Node {
  switch (node.node) {
    case "use":
      if (node.fragment === "values.row") {
        return list(
          Array.from({ length: n }, (_, i) => rename(node, `#${i}`)),
          ", ",
        );
      }
      return use(node.fragment, mapHoles(node, (h) => repeat(h, n)));
    case "list":
      return list(
        node.items.map((it) => repeat(it, n)),
        node.separator,
      );
    default:
      return node;
  }
}

/** rename appends suffix to every parameter's name under node. */
function rename(node: Node, suffix: string): Node {
  switch (node.node) {
    case "use":
      return use(node.fragment, mapHoles(node, (h) => rename(h, suffix)));
    case "list":
      return list(
        node.items.map((it) => rename(it, suffix)),
        node.separator,
      );
    case "param":
      return { node: "param", name: node.name + suffix };
    default:
      return node;
  }
}

/** mapHoles maps the holes of u. */
function mapHoles(u: Use, f: (n: Node) => Node): Record<string, Node> {
  return Object.fromEntries(Object.entries(u.holes ?? {}).map(([k, h]) => [k, f(h)]));
}

/**
 * missing tells why a write of query by key matched no row: exists looks the
 * key up without the expected version. A key that exists was changed since
 * it was read, which is ConcurrentModification; one that does not is
 * NotFound (§20.4). Run it in the write's transaction.
 */
export async function missing(h: Reader, query: string, exists: Statement): Promise<Error> {
  const found = (await queryOptional(h, exists)) !== undefined;
  const o = { dialect: h[bind]().executor.dialect.name, query };
  return found ? new ConcurrentModification(o) : new NotFound(o);
}

/**
 * versionedOne runs write, a versioned write by key returning its row, and
 * decodes the row. When no row matches, exists, the key's lookup without the
 * version, tells NotFound from ConcurrentModification in the same
 * transaction (§20.4).
 */
export function versionedOne<T>(h: Writer, write: Statement, exists: Statement, decode: (r: Row) => T): Promise<T> {
  return atomic(h, async (w) => {
    const r = write[reread] === undefined ? await queryOptional(w, write) : await written(w, write);
    if (r === undefined) {
      throw await missing(w, write.query, exists);
    }
    return decode(r);
  });
}

/** versionedExec runs write, a versioned write by key returning no rows, as versionedOne does. */
export function versionedExec(h: Writer, write: Statement, exists: Statement): Promise<void> {
  return atomic(h, async (w) => {
    if ((await execute(w, write)) === 0) {
      throw await missing(w, write.query, exists);
    }
  });
}

/**
 * execByKey runs write, an unversioned write by key returning no rows, and
 * throws NotFound when it matched no row, as get and patch do for the same
 * key (§19, §20.1). On a soft-delete entity a row already deleted matches
 * nothing, so deleting it again is NotFound too (§21.3).
 */
export async function execByKey(h: Writer, write: Statement): Promise<void> {
  if ((await execute(h, write)) === 0) {
    throw new NotFound({ dialect: h[bind]().executor.dialect.name, query: write.query });
  }
}

/** Change is what one element of a nested write does to a related row (§14.5, §14.6): the variant a generated reference or change type holds. Its values are the same in every host language. */
export type Change =
  /** Points at an existing row by key. */
  | "link"
  /** Inserts a new row. */
  | "create"
  /** Patches an existing row. */
  | "update"
  /** Removes a child according to the relation's removal. */
  | "delete"
  /** Sets the foreign key to NULL, or removes a junction row. */
  | "unlink";
