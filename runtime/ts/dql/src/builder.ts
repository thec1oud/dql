// Composing reads of shapes from the fragment table (design record §16.2,
// §18). Generated column and relation handles make predicates and order
// keys; the builder joins them into a plan and runs it.

import { Decoder, type Row, asBoolean, asInt64 } from "./decode.js";
import type { Reader } from "./executor.js";
import { type Fragments, type Node, type Use, arg, list, statementOf, use } from "./fragments.js";
import { iterate } from "./large.js";
import type { Consistency } from "./routing.js";
import { type Statement, queryAll, queryOne, queryOptional } from "./statement.js";

/** entity brands a predicate, order key or shape with the entity it is of. */
declare const entity: unique symbol;

/**
 * Pred is a predicate on the rows of the entity E stands for (§18.2):
 * generated column and relation handles make them, so a predicate on one
 * entity cannot filter another.
 */
export class Pred<E> {
  /** Brands it with its entity, so one entity's cannot stand for another's. */
  declare readonly [entity]?: (e: E) => E;

  /** @internal Use newPred, or AllRows through allRows. */
  constructor(
    /** @internal The predicate's plan node, absent for allRows. */
    readonly plan: Node | undefined,
  ) {}
}

/** newPred wraps a plan node as a predicate on E; generated code calls it. */
export function newPred<E>(n: Node): Pred<E> {
  return new Pred<E>(n);
}

/** fold joins predicates with a boolean fragment. */
function fold<E>(fragment: string, p: Pred<E>, more: readonly Pred<E>[]): Pred<E> {
  let n = p.plan as Node;
  for (const q of more) {
    n = use(fragment, { "0": n, "1": q.plan as Node });
  }
  return new Pred<E>(n);
}

/** and holds when every predicate holds. */
export function and<E>(p: Pred<E>, ...more: Pred<E>[]): Pred<E> {
  return fold("bool.and", p, more);
}

/** or holds when any predicate holds. */
export function or<E>(p: Pred<E>, ...more: Pred<E>[]): Pred<E> {
  return fold("bool.or", p, more);
}

/** not holds when p does not. */
export function not<E>(p: Pred<E>): Pred<E> {
  return new Pred<E>(use("bool.not", { "0": p.plan as Node }));
}

/** compare is the predicate operator fragment op makes of column fragment col and args (§9.4); generated column handles call it. */
export function compare<E>(op: string, col: string, ...args: Node[]): Pred<E> {
  const holes: Record<string, Node> = { "0": use(col) };
  args.forEach((a, i) => {
    holes[String(i + 1)] = a;
  });
  return new Pred<E>(use(op, holes));
}

/** related holds for a row of E with a row of R through relation fragment rel that every predicate holds for (§18.2); generated relation handles call it. */
export function related<E, R>(rel: string, ...ps: Pred<R>[]): Pred<E> {
  const [p, ...more] = ps;
  if (p === undefined) {
    return new Pred<E>(use(rel));
  }
  return new Pred<E>(use(rel, { and: use("clause.and", { "0": and(p, ...more).plan as Node }) }));
}

/** Order is one key of an ordering of E's rows. */
export class Order<E> {
  /** Brands it with its entity, so one entity's cannot stand for another's. */
  declare readonly [entity]?: (e: E) => E;

  /** @internal Use newOrder or ordered. */
  constructor(
    /** @internal */
    readonly plan: Node,
  ) {}
}

/** newOrder wraps a plan node as an ordering key of E; generated code calls it. */
export function newOrder<E>(n: Node): Order<E> {
  return new Order<E>(n);
}

/** ordered is the order key fragment dir, order.asc or order.desc, makes of column fragment col; generated column handles call it. */
export function ordered<E>(dir: string, col: string): Order<E> {
  return new Order<E>(use(dir, { "0": use(col) }));
}

/** Shape is a read shape of the entity E stands for, as a builder read returns it (§18.2): the fragments that select it, and its decoder. */
export interface Shape<T, E> {
  /** The shape's dotted name, which names the builder's reads of it. */
  readonly name: string;
  readonly catalog: string;
  /** The fragments selecting the shape, its table, and its columns in order, which distinct selects from the table. */
  readonly select: string;
  readonly from: string;
  readonly columns: readonly string[];
  /** The entity's count and exists fragments, and their queries' names. */
  readonly count: string;
  readonly exists: string;
  readonly countName: string;
  readonly existsName: string;
  readonly decode: (r: Row) => T;
  /** Brands the shape with its entity. */
  readonly [entity]?: (e: E) => E;
}

/**
 * From is where a builder read runs: a handle that reads, the scope filter
 * every read through it gets (§21.1), and, for a soft-delete entity, the
 * predicate its rows hold while not deleted, which reads get unless they ask
 * for deleted rows (§21.3).
 */
export interface From {
  readonly h: Reader;
  readonly scope?: readonly Node[];
  readonly visible?: Node;
}

/** Deleted is which rows a read of a soft-delete entity keeps. */
type Deleted = "live" | "with" | "only";

/** Options are what a builder has composed. */
interface Options {
  readonly where: readonly Node[];
  readonly order: readonly Node[];
  readonly limit?: Node;
  readonly offset?: Node;
  readonly distinct: boolean;
  readonly locks: readonly string[];
  readonly noLog: boolean;
  readonly consistency?: Consistency;
  readonly deleted: Deleted;
}

/**
 * Builder composes a read of a shape from the fragment table (§16.2, §18).
 * Each method returns a new builder, so a builder can be reused.
 */
export class Builder<T, E> {
  /** @internal Use newBuilder. */
  constructor(
    private readonly t: Fragments,
    private readonly from: From,
    private readonly shape: Shape<T, E>,
    private readonly o: Options,
  ) {}

  private with(changes: Partial<Options>): Builder<T, E> {
    return new Builder(this.t, this.from, this.shape, { ...this.o, ...changes });
  }

  /** where keeps the rows every predicate holds for, and those of earlier calls. */
  where(...ps: Pred<E>[]): Builder<T, E> {
    return this.with({ where: [...this.o.where, ...ps.map((p) => p.plan as Node)] });
  }

  /** orderBy orders the rows by keys, after those of earlier calls. */
  orderBy(...keys: Order<E>[]): Builder<T, E> {
    return this.with({ order: [...this.o.order, ...keys.map((k) => k.plan)] });
  }

  /** limit returns at most n rows. */
  limit(n: number | bigint): Builder<T, E> {
    return this.with({ limit: arg(BigInt(n)) });
  }

  /** offset skips the first n rows. */
  offset(n: number | bigint): Builder<T, E> {
    return this.with({ offset: arg(BigInt(n)) });
  }

  /** distinct returns each distinct row once. */
  distinct(): Builder<T, E> {
    return this.with({ distinct: true });
  }

  /** forUpdate locks the rows read for update. A locking read runs on the primary. */
  forUpdate(): Builder<T, E> {
    return this.lock("lock.update");
  }

  /** forShare locks the rows read for share. */
  forShare(): Builder<T, E> {
    return this.lock("lock.share");
  }

  /** skipLocked skips rows another transaction locked. */
  skipLocked(): Builder<T, E> {
    return this.lock("lock.skip_locked");
  }

  /** noWait fails on rows another transaction locked. */
  noWait(): Builder<T, E> {
    return this.lock("lock.nowait");
  }

  private lock(f: string): Builder<T, E> {
    return this.with({ locks: [...this.o.locks, f] });
  }

  /** withDeleted keeps soft-deleted rows too (§21.3). */
  withDeleted(): Builder<T, E> {
    return this.with({ deleted: "with" });
  }

  /** onlyDeleted keeps only soft-deleted rows; an entity without soft delete has none (§21.3). */
  onlyDeleted(): Builder<T, E> {
    return this.with({ deleted: "only" });
  }

  /** noLog turns this read's events off, its pool wait included (§24). */
  noLog(): Builder<T, E> {
    return this.with({ noLog: true });
  }

  /** consistency sets how fresh the read must be, over the executor's default (§22.5). */
  consistency(c: Consistency): Builder<T, E> {
    return this.with({ consistency: c });
  }

  /** filter is the WHERE clause: the scope's predicates, the soft delete's, then the call's. */
  private filter(): Use | undefined {
    const ps = [...(this.from.scope ?? [])];
    const visible = this.from.visible;
    switch (this.o.deleted) {
      case "with":
        break;
      case "only":
        ps.push(use("bool.not", { "0": visible ?? use("value.true") }));
        break;
      default:
        if (visible !== undefined) {
          ps.push(visible);
        }
    }
    ps.push(...this.o.where);
    const [p, ...more] = ps;
    if (p === undefined) {
      return undefined;
    }
    return use("clause.where", { "0": more.reduce<Node>((acc, q) => use("bool.and", { "0": acc, "1": q }), p) });
  }

  /** rows assembles the read of the shape's rows. */
  private rows(): Statement {
    const plan: Node[] = [];
    if (this.o.distinct) {
      plan.push(use("select.distinct", { cols: list(this.shape.columns.map((c) => use(c)), ", "), from: use(this.shape.from) }));
    } else {
      plan.push(use(this.shape.select));
    }
    const w = this.filter();
    if (w !== undefined) {
      plan.push(w);
    }
    if (this.o.order.length > 0) {
      plan.push(use("clause.order", { "0": list(this.o.order, ", ") }));
    }
    if (this.o.limit !== undefined) {
      plan.push(use("clause.limit", { "0": this.o.limit }));
    }
    if (this.o.offset !== undefined) {
      plan.push(use("clause.offset", { "0": this.o.offset }));
    }
    for (const l of this.o.locks) {
      plan.push(use(l));
    }
    return this.statement(this.shape.name, "select", plan);
  }

  /** aggregate assembles a count or exists read of the rows the filter keeps. */
  private aggregate(name: string, kind: string, fragment: string): Statement {
    const w = this.filter();
    return this.statement(name, kind, [w === undefined ? use(fragment) : use(fragment, { where: w })]);
  }

  private statement(name: string, kind: string, plan: Node[]): Statement {
    const q = {
      name,
      catalog: this.shape.catalog,
      kind,
      plan,
      writes: this.o.locks.length > 0,
      ...(this.o.consistency === undefined ? {} : { consistency: this.o.consistency }),
    };
    return { ...statementOf(q, this.t), noLog: this.o.noLog };
  }

  /** one returns the one row: NotFound for none and TooManyRows for more (§19). */
  async one(): Promise<T> {
    return oneOf(this.from.h, this.rows(), this.shape.decode);
  }

  /** optionalOne returns the row, or undefined when there is none: TooManyRows for more than one (§19). */
  async optionalOne(): Promise<T | undefined> {
    return optionalOf(this.from.h, this.rows(), this.shape.decode);
  }

  /** all returns every row, [] for none (§19). */
  async all(): Promise<T[]> {
    return allOf(this.from.h, this.rows(), this.shape.decode);
  }

  /** iter streams the rows, holding a connection until iteration ends (§25.1). */
  iter(): AsyncIterable<T> {
    return iterateOf(this.from.h, () => this.rows(), this.shape.decode);
  }

  /** count returns how many rows the filter keeps; order, limit and offset do not apply. */
  async count(): Promise<bigint> {
    return count(this.from.h, this.aggregate(this.shape.countName, "count", this.shape.count));
  }

  /** exists reports whether the filter keeps any row. */
  async exists(): Promise<boolean> {
    return exists(this.from.h, this.aggregate(this.shape.existsName, "exists", this.shape.exists));
  }
}

/** newBuilder starts a read of shape on from; generated code calls it. */
export function newBuilder<T, E>(t: Fragments, from: From, shape: Shape<T, E>): Builder<T, E> {
  return new Builder(t, from, shape, { where: [], order: [], distinct: false, locks: [], noLog: false, deleted: "live" });
}

/** oneOf runs s and decodes its one row: NotFound for none and TooManyRows for more (§19). */
export async function oneOf<T>(h: Reader, s: Statement, decode: (r: Row) => T): Promise<T> {
  return decode(await queryOne(h, s));
}

/** optionalOf runs s and decodes its row, if any: TooManyRows for more than one (§19). */
export async function optionalOf<T>(h: Reader, s: Statement, decode: (r: Row) => T): Promise<T | undefined> {
  const r = await queryOptional(h, s);
  return r === undefined ? undefined : decode(r);
}

/** allOf runs s and decodes every row; no rows is [] (§19). */
export async function allOf<T>(h: Reader, s: Statement, decode: (r: Row) => T): Promise<T[]> {
  return (await queryAll(h, s)).map(decode);
}

/** iterateOf streams the rows of the statement s returns, decoded (§25.1). A row that fails to decode ends the iteration with its error, as does a statement that fails to assemble. */
export function iterateOf<T>(h: Reader, s: () => Statement, decode: (r: Row) => T): AsyncIterable<T> {
  return {
    async *[Symbol.asyncIterator]() {
      for await (const r of iterate(h, s())) {
        yield decode(r);
      }
    },
  };
}

/** count runs s, which returns one row holding a count, and returns it. */
export function count(h: Reader, s: Statement): Promise<bigint> {
  return oneOf(h, s, (r) => new Decoder(s.query, r, 1).field(0, "count", "", asInt64));
}

/** exists runs s, which returns one row holding whether a row matches, and returns it. */
export function exists(h: Reader, s: Statement): Promise<boolean> {
  return oneOf(h, s, (r) => new Decoder(s.query, r, 1).field(0, "exists", "", asBoolean));
}
