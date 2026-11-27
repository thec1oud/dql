import { afterEach, describe, expect, test } from "bun:test";
import { Row } from "../src/decode.js";
import type { Executor } from "../src/executor.js";
import { type Call, Fake } from "../src/fake.js";
import {
  type Args,
  Consistency,
  DEFAULT,
  Expression,
  type Fragments,
  type Node,
  type Pred,
  type Query,
  type Shape,
  type Use,
  allRows,
  and,
  arg,
  arith,
  assemble,
  assign,
  classOf,
  compare,
  execByKey,
  filtered,
  insertMany,
  list,
  newBuilder,
  newOrder,
  newPred,
  not,
  now,
  or,
  ordered,
  param,
  related,
  set,
  statementOf,
  use,
  versionedExec,
  versionedOne,
} from "../src/index.js";
import { pg } from "./executor.test.js";

const table: Fragments = {
  placeholder: "dollar",
  templates: {
    "insert.users": `INSERT INTO "app"."users" AS u ({cols}) VALUES {rows}{returning}`,
    "update.users": `UPDATE "app"."users" AS u SET {sets}{where}{returning}`,
    "name.users.email": `"email"`,
    "name.users.name": `"name"`,
    "set.users.email": `"email" = {0}`,
    "set.users.name": `"name" = {0}`,
    "values.row": `({0})`,
    "value.default": `DEFAULT`,
    "clause.where": ` WHERE {0}`,
    "op.eq": `{0} = {1}`,
    "col.users.id": `u."id"`,
    "returning.user": ` RETURNING u."id"`,
    "op.json": `{0} @> '{"a": 1}' AND '{}' <> {1} AND '{x' = '{X}' AND '{unclosed`,
    "select.user": `SELECT u."id" FROM "app"."users" u`,
    "clause.limit": ` LIMIT {0}`,
    "wrap.default_hole": `x{missing}y`,
    "set.users.computed": `"n" = {0}`,
  },
};

const eq = (col: string, v: Node) => use("op.eq", { "0": use(col), "1": v });

describe("assembling (§16.2)", () => {
  const insert = [
    use("insert.users", {
      cols: list([use("name.users.email"), use("name.users.name")], ", "),
      rows: use("values.row", { "0": list([param("email"), param("name")], ", ") }),
      returning: use("returning.user"),
    }),
  ];
  const update = [
    use("update.users", {
      sets: list(
        [
          use("set.users.email", { "0": param("email") }),
          use("set.users.name", { "0": param("name") }),
          use("set.users.computed", { "0": use("op.eq", { "0": param("name"), "1": param("email") }) }),
        ],
        ", ",
      ),
      where: use("clause.where", { "0": eq("col.users.id", param("key.id")) }),
    }),
  ];
  const at = (t: Fragments, plan: Node[], args: Args = {}) => assemble(t, plan, args, { email: true }, { email: "text", "key.id": "int8" });

  test("plans render with numbered placeholders", () => {
    const cases: [string, Fragments, Node[], Args, string, unknown[], boolean[], string[]][] = [
      ["insert", table, insert, { email: "a@b.c", name: "A" }, `INSERT INTO "app"."users" AS u ("email", "name") VALUES ($1, $2) RETURNING u."id"`, ["a@b.c", "A"], [true, false], ["text", ""]],
      ["insert default", table, insert, { name: "A" }, `INSERT INTO "app"."users" AS u ("email", "name") VALUES (DEFAULT, $1) RETURNING u."id"`, ["A"], [false], [""]],
      [
        "update",
        table,
        update,
        { email: "a@b.c", name: null, "key.id": 7 },
        `UPDATE "app"."users" AS u SET "email" = $1, "name" = $2, "n" = $3 = $4 WHERE u."id" = $5`,
        ["a@b.c", null, null, "a@b.c", 7],
        [true, false, false, true, false],
        ["text", "", "", "text", "int8"],
      ],
      ["update drops", table, update, { name: "A", "key.id": 7 }, `UPDATE "app"."users" AS u SET "name" = $1 WHERE u."id" = $2`, ["A", 7], [false, false], ["", "int8"]],
      ["question", { ...table, placeholder: "question" }, update, { email: "e", "key.id": 7 }, `UPDATE "app"."users" AS u SET "email" = ? WHERE u."id" = ?`, ["e", 7], [true, false], ["text", "int8"]],
      [
        "at",
        { ...table, placeholder: "at" },
        [use("select.user"), use("clause.limit", { "0": arg(20, true, "int8") })],
        {},
        `SELECT u."id" FROM "app"."users" u LIMIT @p1`,
        [20],
        [true],
        ["int8"],
      ],
      ["braces", table, [use("op.json", { "0": arg(1), "1": { node: "arg", value: 2 } })], {}, `$1 @> '{"a": 1}' AND '{}' <> $2 AND '{x' = '{X}' AND '{unclosed`, [1, 2], [false, false], ["", ""]],
      ["unfilled hole", table, [use("wrap.default_hole")], {}, "xy", [], [], []],
    ];
    for (const [name, t, plan, args, sql, vals, sens, types] of cases) {
      expect([name, at(t, plan, args)]).toEqual([name, { sql, args: vals, sensitive: sens, types }]);
    }
    // Without sensitive or types, every argument is neither.
    expect(assemble(table, [use("clause.limit", { "0": param("n") })], { n: 1 })).toEqual({ sql: " LIMIT $1", args: [1], sensitive: [false], types: [""] });
  });

  test("a plan the table does not anticipate fails", () => {
    const fails = (t: Fragments, plan: Node[], args: Args = {}) => {
      try {
        at(t, plan, args);
      } catch (e) {
        return (e as Error).message;
      }
      return "";
    };
    expect(fails(table, update, { name: "A" })).toBe("dql: parameter key.id is left out: absent");
    expect(fails(table, [use("nope")])).toBe("dql: the fragment table has no nope");
    expect(fails(table, [list([use("nope")], ", ")])).toBe("dql: the fragment table has no nope");
    expect(fails({ placeholder: "dollar", templates: {} }, [list([param("x")], ", ")])).toBe("dql: the fragment table has no value.default");
    // An inherited name is not a fragment.
    expect(fails(table, [use("toString")])).toBe("dql: the fragment table has no toString");
  });

  test("a query becomes a statement", () => {
    const q: Query = {
      name: "user.get",
      catalog: "shop",
      kind: "select",
      atMostOne: true,
      consistency: Consistency.any,
      sensitive: { "key.id": true },
      types: { "key.id": "int8" },
      plan: [use("select.user"), use("clause.where", { "0": eq("col.users.id", param("key.id")) })],
    };
    expect(statementOf(q, table, { "key.id": 7 })).toEqual({
      query: "user.get",
      catalog: "shop",
      sql: `SELECT u."id" FROM "app"."users" u WHERE u."id" = $1`,
      args: [7],
      writes: false,
      atMostOne: true,
      consistency: Consistency.any,
      kind: "select",
      sensitive: [true],
      types: ["int8"],
    });
    expect(() => statementOf(q, table)).toThrow("dql: parameter key.id is left out: absent in query user.get");
    const plain = statementOf({ name: "q", catalog: "shop", kind: "select", plan: [use("select.user")] }, table);
    expect([plain.writes, plain.atMostOne, "consistency" in plain]).toEqual([false, false, false]);
  });
});

interface UserE {
  readonly user: true;
}
interface PostE {
  readonly post: true;
}

const builderTable: Fragments = {
  placeholder: "dollar",
  templates: {
    "select.user": `SELECT u."id" FROM "app"."users" u`,
    "select.distinct": `SELECT DISTINCT {cols} FROM {from}`,
    "from.users": `"app"."users" u`,
    "col.users.id": `u."id"`,
    "col.users.email": `u."email"`,
    "op.eq": `{0} = {1}`,
    "op.gt": `{0} > {1}`,
    "op.between": `{0} BETWEEN {1} AND {2}`,
    "bool.and": `({0} AND {1})`,
    "bool.or": `({0} OR {1})`,
    "bool.not": `NOT ({0})`,
    "op.is_null": `{0} IS NULL`,
    "value.true": `TRUE`,
    "col.users.gone": `u."gone"`,
    "clause.where": ` WHERE {0}`,
    "clause.and": ` AND {0}`,
    "clause.order": ` ORDER BY {0}`,
    "clause.limit": ` LIMIT {0}`,
    "clause.offset": ` OFFSET {0}`,
    "order.asc": `{0} ASC`,
    "order.desc": `{0} DESC`,
    "lock.update": ` FOR UPDATE`,
    "lock.share": ` FOR SHARE`,
    "lock.skip_locked": ` SKIP LOCKED`,
    "lock.nowait": ` NOWAIT`,
    "count.users": `SELECT count(*) FROM "app"."users" u{where}`,
    "exists.users": `SELECT EXISTS (SELECT FROM "app"."users" u{where})`,
    "rel.users.posts": `EXISTS (SELECT 1 FROM "app"."posts" p WHERE p."author_id" = u."id"{and})`,
    "col.posts.title": `p."title"`,
  },
};

const userShape: Shape<bigint, UserE> = {
  name: "user.select",
  catalog: "shop",
  select: "select.user",
  from: "from.users",
  columns: ["col.users.id"],
  count: "count.users",
  exists: "exists.users",
  countName: "user.count",
  existsName: "user.exists",
  decode: (r) => r.values[0] as bigint,
};

const colEq = (col: string, v: unknown): Pred<UserE> => newPred<UserE>(use("op.eq", { "0": use(col), "1": arg(v) }));

let open: Executor[] = [];

afterEach(async () => {
  for (const e of open) {
    await e.close();
  }
  open = [];
});

async function fakeExec(): Promise<[Fake, Executor]> {
  const f = new Fake();
  const e = await f.executor(pg, ["shop"]);
  open.push(e);
  return [f, e];
}

const last = (f: Fake): Call => f.calls().at(-1) as Call;

describe("the builder (§18)", () => {
  test("reads compose", async () => {
    const [f, e] = await fakeExec();
    f.onAny("user.select").returns([7n]);
    f.onAny("user.count").returns([7n]);
    f.onAny("user.exists").returns([true]);
    const scope = [colEq("col.users.id", 9n).plan as Node];
    const live = use("op.is_null", { "0": use("col.users.gone") });
    const b = newBuilder(builderTable, { h: e }, userShape);
    const asc = newOrder<UserE>(use("order.asc", { "0": use("col.users.id") }));
    const desc = ordered<UserE>("order.desc", "col.users.email");
    const gt = newPred<UserE>(use("op.gt", { "0": use("col.users.id"), "1": arg(1n) }));
    const where = b.where(or(colEq("col.users.email", "a"), colEq("col.users.email", "b"), not(gt)));
    const scoped = newBuilder(builderTable, { h: e, scope }, userShape);
    const soft = newBuilder(builderTable, { h: e, scope, visible: live }, userShape);
    const cases: [string, () => Promise<unknown>, string, unknown[]][] = [
      ["plain", () => b.all(), `SELECT u."id" FROM "app"."users" u`, []],
      ["where", () => where.all(), `SELECT u."id" FROM "app"."users" u WHERE ((u."email" = $1 OR u."email" = $2) OR NOT (u."id" > $3))`, ["a", "b", 1n]],
      ["scope and calls", () => scoped.where(and(gt, gt)).where(gt).all(), `SELECT u."id" FROM "app"."users" u WHERE ((u."id" = $1 AND (u."id" > $2 AND u."id" > $3)) AND u."id" > $4)`, [9n, 1n, 1n, 1n]],
      ["soft delete", () => soft.where(gt).all(), `SELECT u."id" FROM "app"."users" u WHERE ((u."id" = $1 AND u."gone" IS NULL) AND u."id" > $2)`, [9n, 1n]],
      ["with deleted", () => soft.onlyDeleted().withDeleted().all(), `SELECT u."id" FROM "app"."users" u WHERE u."id" = $1`, [9n]],
      ["only deleted", () => soft.onlyDeleted().count(), `SELECT count(*) FROM "app"."users" u WHERE (u."id" = $1 AND NOT (u."gone" IS NULL))`, [9n]],
      ["only deleted without soft delete", () => b.onlyDeleted().all(), `SELECT u."id" FROM "app"."users" u WHERE NOT (TRUE)`, []],
      ["with deleted without soft delete", () => b.withDeleted().all(), `SELECT u."id" FROM "app"."users" u`, []],
      ["page", () => b.orderBy(asc).orderBy(desc).limit(10).offset(20n).all(), `SELECT u."id" FROM "app"."users" u ORDER BY u."id" ASC, u."email" DESC LIMIT $1 OFFSET $2`, [10n, 20n]],
      ["distinct", () => b.distinct().all(), `SELECT DISTINCT u."id" FROM "app"."users" u`, []],
      ["locks", () => b.forUpdate().skipLocked().all(), `SELECT u."id" FROM "app"."users" u FOR UPDATE SKIP LOCKED`, []],
      ["share", () => b.forShare().noWait().all(), `SELECT u."id" FROM "app"."users" u FOR SHARE NOWAIT`, []],
      ["count", () => where.orderBy(asc).limit(1).count(), `SELECT count(*) FROM "app"."users" u WHERE ((u."email" = $1 OR u."email" = $2) OR NOT (u."id" > $3))`, ["a", "b", 1n]],
      ["exists", () => b.exists(), `SELECT EXISTS (SELECT FROM "app"."users" u)`, []],
    ];
    for (const [name, run, sql, args] of cases) {
      await run();
      expect([name, last(f).sql, last(f).args]).toEqual([name, sql, args]);
    }
    // A builder is a value: a call leaves the builder it was made from as it was.
    await b.all();
    expect(last(f).sql).toBe(`SELECT u."id" FROM "app"."users" u`);
  });

  test("a page in one clause, and locks as table hints", async () => {
    const [f, e] = await fakeExec();
    f.onAny("user.select").returns([7n]);
    const hinted: Fragments = {
      placeholder: "at",
      templates: {
        "select.user": `SELECT u.[id] FROM [app].[users] AS u{locks}`,
        "select.distinct": `SELECT {cols} FROM {from}{locks}`,
        "clause.distinct": ` GROUP BY {cols}`,
        "from.users": `[app].[users] AS u`,
        "col.users.id": `u.[id]`,
        "clause.order": ` ORDER BY {0}`,
        "clause.offset": ` OFFSET {0} ROWS`,
        "clause.page": ` OFFSET {offset} ROWS FETCH NEXT {limit} ROWS ONLY`,
        "order.asc": `{0} ASC`,
        "order.none": ` ORDER BY (SELECT NULL)`,
        "value.zero": `0`,
        "lock.hints": ` WITH ({0})`,
        "lock.update": `UPDLOCK, ROWLOCK`,
        "lock.skip_locked": `READPAST`,
      },
    };
    const b = newBuilder(hinted, { h: e }, userShape);
    const asc = newOrder<UserE>(use("order.asc", { "0": use("col.users.id") }));
    const noLimit: Fragments = { ...builderTable, templates: { ...builderTable.templates, "value.no_limit": "18446744073709551615" } };
    const cases: [string, () => Promise<unknown>, string, unknown[]][] = [
      ["page", () => b.limit(10).offset(20n).all(), `SELECT u.[id] FROM [app].[users] AS u ORDER BY (SELECT NULL) OFFSET @p1 ROWS FETCH NEXT @p2 ROWS ONLY`, [20n, 10n]],
      ["limit", () => b.orderBy(asc).limit(10).all(), `SELECT u.[id] FROM [app].[users] AS u ORDER BY u.[id] ASC OFFSET 0 ROWS FETCH NEXT @p1 ROWS ONLY`, [10n]],
      ["offset", () => b.offset(5n).all(), `SELECT u.[id] FROM [app].[users] AS u ORDER BY (SELECT NULL) OFFSET @p1 ROWS`, [5n]],
      ["plain", () => b.all(), `SELECT u.[id] FROM [app].[users] AS u`, []],
      ["locks", () => b.forUpdate().skipLocked().all(), `SELECT u.[id] FROM [app].[users] AS u WITH (UPDLOCK, ROWLOCK, READPAST)`, []],
      ["distinct", () => b.distinct().forUpdate().all(), `SELECT u.[id] FROM [app].[users] AS u WITH (UPDLOCK, ROWLOCK) GROUP BY u.[id]`, []],
      ["ordered distinct", () => b.distinct().orderBy(asc).all(), `SELECT u.[id] FROM [app].[users] AS u GROUP BY u.[id] ORDER BY u.[id] ASC`, []],
      // A dialect without OFFSET alone limits to every row.
      ["offset alone", () => newBuilder(noLimit, { h: e }, userShape).offset(5n).all(), `SELECT u."id" FROM "app"."users" u LIMIT 18446744073709551615 OFFSET $1`, [5n]],
    ];
    for (const [name, run, sql, args] of cases) {
      await run();
      expect([name, last(f).sql, last(f).args]).toEqual([name, sql, args]);
    }
  });

  test("terminals", async () => {
    const [f, e] = await fakeExec();
    const b = newBuilder(builderTable, { h: e }, userShape);
    f.onAny("user.select").returns([7n]);
    f.onAny("user.count").returns([7n]);
    f.onAny("user.exists").returns([true]);
    expect(await b.one()).toBe(7n);
    expect(await b.optionalOne()).toBe(7n);
    expect(await b.all()).toEqual([7n]);
    const streamed: bigint[] = [];
    for await (const v of b.iter()) {
      streamed.push(v);
    }
    expect(streamed).toEqual([7n]);
    expect(await b.count()).toBe(7n);
    expect(await b.exists()).toBe(true);
    f.onAny("user.select").returns();
    expect(await b.optionalOne()).toBe(undefined);
    // A shape whose fragments the table lacks fails every terminal.
    const broken = newBuilder(builderTable, { h: e }, { ...userShape, select: "select.missing", count: "count.missing", exists: "exists.missing" });
    const errors = await Promise.all([broken.one(), broken.optionalOne(), broken.all(), broken.count(), broken.exists()].map((p) => p.catch((x: Error) => x.message)));
    expect(errors).toEqual([
      "dql: the fragment table has no select.missing in query user.select",
      "dql: the fragment table has no select.missing in query user.select",
      "dql: the fragment table has no select.missing in query user.select",
      "dql: the fragment table has no count.missing in query user.count",
      "dql: the fragment table has no exists.missing in query user.exists",
    ]);
    const it = broken.iter()[Symbol.asyncIterator]();
    expect(await it.next().catch((x: Error) => x.message)).toBe("dql: the fragment table has no select.missing in query user.select");
  });

  test("locks write, and noLog and consistency carry to the statement", async () => {
    const f = new Fake();
    const seen: string[] = [];
    const e = await f.executor(pg, ["shop"], {
      hooks: [{ queryStart: (q) => void seen.push(`${q.query} ${q.consistency}`) }],
    });
    open.push(e);
    f.onAny("user.select").returns([1n]);
    f.onAny("user.count").returns([1n]);
    const b = newBuilder(builderTable, { h: e }, userShape);
    await b.forUpdate().noLog().consistency(Consistency.primary).all();
    await b.consistency(Consistency.any).all();
    await b.count();
    expect(seen).toEqual(["user.select any", "user.count primary"]);
    // A locking read writes, so a read transaction cannot run it.
    expect(await e.read((tx) => newBuilder(builderTable, { h: tx }, userShape).forUpdate().all()).catch((x) => x.reason)).toBe("read_only");
  });

  test("predicate helpers", () => {
    const title = compare<PostE>("op.eq", "col.posts.title", arg("t"));
    const cases: [Node, string][] = [
      [compare<UserE>("op.between", "col.users.id", arg(1), arg(2)).plan as Node, `u."id" BETWEEN $1 AND $2`],
      [compare<UserE>("op.is_null", "col.users.id").plan as Node, `u."id" IS NULL`],
      [related<UserE, PostE>("rel.users.posts").plan as Node, `EXISTS (SELECT 1 FROM "app"."posts" p WHERE p."author_id" = u."id")`],
      [related<UserE, PostE>("rel.users.posts", title, title).plan as Node, `EXISTS (SELECT 1 FROM "app"."posts" p WHERE p."author_id" = u."id" AND (p."title" = $1 AND p."title" = $2))`],
      [ordered<UserE>("order.desc", "col.users.id").plan, `u."id" DESC`],
    ];
    for (const [n, sql] of cases) {
      expect(assemble(builderTable, [n], {}).sql).toBe(sql);
    }
  });
});

describe("writes (§20)", () => {
  const update: Query = {
    name: "user.patch",
    catalog: "shop",
    kind: "update",
    writes: true,
    plan: [
      use("update.users", {
        sets: list([use("set.users.email", { "0": param("email") }), use("set.users.name", { "0": param("name") })], ", "),
        where: use("clause.where", { "0": eq("col.users.id", param("key.id")) }),
      }),
    ],
  };
  const t: Fragments = { placeholder: "dollar", templates: { ...table.templates, "value.now": "now()", "op.add": "{0} + {1}", "bool.and": "({0} AND {1})", "col.users.name": `u."name"` } };

  test("expressions assign in place of values (§20.2)", () => {
    const args: Args = { email: "a" };
    assign<UserE>(update, "user.partial", args, [set("name", now)]);
    expect(statementOf(update, t, { ...args, "key.id": 1 }).sql).toBe(`UPDATE "app"."users" AS u SET "email" = $1, "name" = now() WHERE u."id" = $2`);
    const more: Args = {};
    assign<UserE>(update, "user.partial", more, [set("email", DEFAULT), arith("op.add", "name", "col.users.name", arg(1))]);
    expect(statementOf(update, t, { ...more, "key.id": 1 }).sql).toBe(`UPDATE "app"."users" AS u SET "email" = DEFAULT, "name" = u."name" + $1 WHERE u."id" = $2`);
    // A field set twice is a validation issue; a field the query does not write is an error.
    const twice = (() => {
      try {
        assign<UserE>(update, "user.partial", { email: "a" }, [set("email", now)]);
      } catch (e) {
        return e;
      }
    })() as { issues: unknown[] };
    expect([classOf(twice), twice.issues]).toEqual(["ValidationErrors", [{ shape: "user.partial", field: "email", kind: "field_set_twice" }]]);
    expect(() => assign<UserE>(update, "user.partial", {}, [set("id", now)])).toThrow("dql: user.patch does not write id");
    // A prefix names the parameters of an upsert's update side.
    const upsert: Query = { ...update, plan: [arg(0), use("set.users.name", { "0": param("set.name") })] };
    const prefixed: Args = {};
    assign<UserE>(upsert, "user.partial", prefixed, [set("name", now)], "set.");
    expect(statementOf(upsert, t, prefixed).sql).toBe(`$1"name" = now()`);
  });

  test("filtered writes need a filter (§20.1)", () => {
    const where = filtered(update, colEq("col.users.id", 3n));
    expect(statementOf(where, t, { name: "n", "key.id": 1 }).sql).toBe(`UPDATE "app"."users" AS u SET "name" = $1 WHERE (u."id" = $2 AND u."id" = $3)`);
    const bare: Query = { ...update, plan: [use("update.users", { sets: list([use("set.users.name", { "0": param("name") })], ", ") }), use("clause.limit", { "0": arg(1) })] };
    expect(statementOf(filtered(bare, colEq("col.users.id", 3n)), t, { name: "n" }).sql).toBe(`UPDATE "app"."users" AS u SET "name" = $1 WHERE u."id" = $2 LIMIT $3`);
    expect(filtered(update, allRows<UserE>())).toBe(update);
    expect(() => filtered(update, newPred<UserE>(undefined as unknown as Node))).toThrow("dql: a filtered write needs a filter; pass the table's allRows() to write every row");
  });

  test("insertMany repeats the row and splits", async () => {
    const f = new Fake();
    const e = await f.executor({ ...pg, maxParams: 5 }, ["shop"]);
    open.push(e);
    const insert: Query = {
      name: "user.insert",
      catalog: "shop",
      kind: "insert",
      writes: true,
      sensitive: { email: true },
      plan: [
        use("insert.users", {
          cols: list([use("name.users.email"), use("name.users.name")], ", "),
          rows: use("values.row", { "0": list([param("email"), param("name")], ", ") }),
          returning: use("returning.user"),
        }),
      ],
    };
    f.onAny("user.insert").returns([1n], [2n]);
    const rows = [{ email: "a", name: "A" }, { name: "B" }, { email: "c", name: "C" }];
    expect(await insertMany(e, table, insert, rows, (r: Row) => r.values[0])).toEqual([1n, 2n, 1n, 2n]);
    expect(f.calls().map((c) => [c.sql, c.args, c.inTx])).toEqual([
      [`INSERT INTO "app"."users" AS u ("email", "name") VALUES ($1, $2), (DEFAULT, $3) RETURNING u."id"`, ["a", "A", "B"], true],
      [`INSERT INTO "app"."users" AS u ("email", "name") VALUES ($1, $2) RETURNING u."id"`, ["c", "C"], true],
    ]);
    // A plan without a VALUES row binds nothing per row, so one statement writes them all.
    f.onAny("user.touch").affected(0);
    const touch: Query = { name: "user.touch", catalog: "shop", kind: "insert", writes: true, plan: [use("set.users.name", { "0": list([arg(1), use("value.default")], ", ") })] };
    expect(await insertMany(e, table, touch, [{}, {}], (r: Row) => r)).toEqual([]);
    // A value in the row is repeated as it is.
    const fixed: Query = { ...touch, plan: [use("values.row", { "0": list([arg(1), use("value.default")], ", ") })] };
    expect(await insertMany(e, table, fixed, [{}, {}], (r: Row) => r)).toEqual([]);
    expect(last(f).sql).toBe("($1, DEFAULT), ($2, DEFAULT)");
  });

  test("insertMany without value.default leaves unset columns out (§25.2)", async () => {
    const f = new Fake();
    const e = await f.executor(pg, ["shop"]);
    open.push(e);
    const { "value.default": _, ...templates } = table.templates;
    const lite: Fragments = { ...table, templates };
    const insert: Query = {
      name: "user.insert",
      catalog: "shop",
      kind: "insert",
      writes: true,
      plan: [
        use("insert.users", {
          cols: list([use("name.users.email"), use("name.users.name")], ", "),
          rows: use("values.row", { "0": list([param("email"), param("name")], ", ") }),
          returning: use("returning.user"),
        }),
        arg(1),
      ],
    };
    f.onAny("user.insert").returns([1n]);
    const rows = [{ email: "a", name: "A" }, { name: "B" }, { name: "C" }, { name: "D", email: "d" }];
    expect(await insertMany(e, lite, insert, rows, (r: Row) => r.values[0])).toEqual([1n, 1n, 1n]);
    expect(f.calls().map((c) => [c.sql, c.args, c.inTx])).toEqual([
      [`INSERT INTO "app"."users" AS u ("email", "name") VALUES ($1, $2) RETURNING u."id"$3`, ["a", "A", 1], true],
      [`INSERT INTO "app"."users" AS u ("name") VALUES ($1), ($2) RETURNING u."id"$3`, ["B", "C", 1], true],
      [`INSERT INTO "app"."users" AS u ("email", "name") VALUES ($1, $2) RETURNING u."id"$3`, ["d", "D", 1], true],
    ]);
    // Rows setting the same columns are one statement, and one row is assembled the same way.
    expect(await insertMany(e, lite, insert, rows.slice(1, 3), (r: Row) => r.values[0])).toEqual([1n]);
    expect(last(f).sql).toBe(`INSERT INTO "app"."users" AS u ("name") VALUES ($1), ($2) RETURNING u."id"$3`);
    expect(await insertMany(e, lite, insert, [], (r: Row) => r)).toEqual([]);
    expect(statementOf(insert, lite, { email: "e" }).sql).toBe(`INSERT INTO "app"."users" AS u ("email") VALUES ($1) RETURNING u."id"$2`);
    // A MERGE's new-row values leave out the unset columns too.
    const merging: Fragments = {
      ...lite,
      templates: { ...lite.templates, "upsert.users": "MERGE USING (VALUES {rows}) AS n ({cols}) INSERT ({cols}) VALUES ({news})", "excluded.users.email": `n."email"`, "excluded.users.name": `n."name"` },
    };
    const head = insert.plan[0] as Use;
    const merge: Query = {
      name: "user.upsert",
      catalog: "shop",
      kind: "insert",
      writes: true,
      plan: [use("upsert.users", { cols: head.holes?.["cols"] as Node, rows: head.holes?.["rows"] as Node, news: list([use("excluded.users.email"), use("excluded.users.name")], ", ") })],
    };
    expect(statementOf(merge, merging, { name: "n" }).sql).toBe(`MERGE USING (VALUES ($1)) AS n ("name") INSERT ("name") VALUES (n."name")`);
    // DEFAULT in a SET renders the column's default, as SQLite has no DEFAULT there; elsewhere it still needs value.default.
    const withDefault: Fragments = { ...lite, templates: { ...lite.templates, "default.users.name": "('')" } };
    const patch: Query = { name: "user.patch", catalog: "shop", kind: "update", writes: true, plan: [use("set.users.name", { "0": param("name") }), param("email")] };
    expect(statementOf(patch, withDefault, { name: new Expression(use("value.default")), email: 1 }).sql).toBe(`"name" = ('')$1`);
    expect(() => statementOf(patch, withDefault, { name: 1, email: new Expression(use("value.default")) })).toThrow("dql: the fragment table has no value.default");
  });

  test("writes by key tell NotFound from ConcurrentModification (§20.4)", async () => {
    const [f, e] = await fakeExec();
    const write = statementOf({ name: "user.patch", catalog: "shop", kind: "update", writes: true, plan: [use("select.user")] }, table);
    const exists = statementOf({ name: "user.exists_key", catalog: "shop", kind: "select", plan: [use("select.user")] }, table);
    f.on("user.patch").returns([5n]);
    expect(await versionedOne(e, write, exists, (r) => r.values[0])).toBe(5n);
    f.on("user.patch").returns();
    f.on("user.exists_key").returns([1]);
    expect(classOf(await versionedOne(e, write, exists, (r) => r).catch((x) => x))).toBe("ConcurrentModification");
    f.on("user.exists_key").returns();
    expect(classOf(await versionedOne(e, write, exists, (r) => r).catch((x) => x))).toBe("NotFound");
    f.on("user.patch").affected(1);
    await versionedExec(e, write, exists);
    f.on("user.patch").affected(0);
    expect(classOf(await versionedExec(e, write, exists).catch((x) => x))).toBe("NotFound");
    f.on("user.exists_key").returns([1]);
    expect(classOf(await versionedExec(e, write, exists).catch((x) => x))).toBe("ConcurrentModification");
    f.on("user.delete").affected(1);
    await execByKey(e, { ...write, query: "user.delete" });
    f.on("user.delete").affected(0);
    const nf = (await execByKey(e, { ...write, query: "user.delete" }).catch((x) => x)) as Error;
    expect([classOf(nf), nf.message]).toEqual(["NotFound", "dql: NotFound in user.delete (postgres)"]);
    f.verify();
  });
});
