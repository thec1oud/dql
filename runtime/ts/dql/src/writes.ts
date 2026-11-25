// Write expressions, filtered writes and writes by key (design record §20).

import type { Pred } from "./builder.js";
import type { Row } from "./decode.js";
import { ConcurrentModification, NotFound } from "./errors.js";
import { type Reader, type Writer, bind } from "./executor.js";
import { type Args, Expression, type Fragments, type Node, type Query, type Use, list, statementOf, use } from "./fragments.js";
import { atomic, queryBulk } from "./large.js";
import { type Statement, execute, queryOptional } from "./statement.js";
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
 */
export async function insertMany<T>(h: Writer, t: Fragments, q: Query, rows: readonly Args[], decode: (r: Row) => T): Promise<T[]> {
  const per = q.plan.reduce((n, node) => n + placeholders(node, false), 0);
  const fetched = await queryBulk(h, {
    rows: rows.length,
    paramsPerRow: per,
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
      return statementOf({ ...q, plan: q.plan.map((n) => repeat(n, to - from)), sensitive }, t, args);
    },
  });
  return fetched.map(decode);
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
    const r = await queryOptional(w, write);
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
