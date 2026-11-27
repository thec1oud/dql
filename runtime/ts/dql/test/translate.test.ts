import { describe, expect, test } from "bun:test";
import { DatabaseError, NotNullViolation, Violation, classOf } from "../src/errors.js";
import { Retried, retry } from "../src/retry.js";
import { Translator } from "../src/translate.js";
import { pg } from "./executor.test.js";
import { FakeDriver, FakeNative } from "./fake-driver.js";

describe("translate (§23)", () => {
  const t = new Translator(new FakeDriver(), "postgres", pg.errors);
  const tr = (e: unknown, catalog = "shop") => t.translate(e, catalog, "q", "main", undefined);

  test("classes", () => {
    for (const [code, cls] of [
      ["40001", "SerializationFailure"],
      ["40P01", "Deadlock"],
      ["55P03", "LockTimeout"],
      ["57014", "Timeout"],
      ["08006", "ConnectionError"],
      ["XX000", "DatabaseError"],
      ["constructor", "DatabaseError"],
    ] as const) {
      expect(classOf(tr(new FakeNative({ code, message: "m" })))).toBe(cls);
    }
    const db = tr(new FakeNative({ code: "XX000", message: "boom", detail: "d" })) as DatabaseError;
    expect([db.databaseMessage, db.detail, db.message]).toEqual(["boom", "d", "dql: DatabaseError in q (postgres XX000)"]);
    expect((tr(new FakeNative({ code: "XX000" })) as DatabaseError).databaseMessage).toBe("");
    // An error the database did not send.
    expect(classOf(tr(new Error("broken")))).toBe("ConnectionError");
    expect((tr(new Error("odd")) as DatabaseError).databaseMessage).toBe("odd");
    expect((tr("odd") as DatabaseError).databaseMessage).toBe("odd");
    // A taxonomy error passes through.
    const done = new DatabaseError();
    expect(tr(done)).toBe(done);
  });

  test("violations (§23.2, §23.3)", () => {
    const v = (n: object) => tr(new FakeNative({ code: "", ...n })) as Violation;
    const known = v({ code: "23505", schema: "app", table: "users", constraint: "users_tenant_email_key", detail: "Key (email)=(a@x.com)" });
    expect([known.class, known.constraint.name, known.detail]).toEqual(["UniqueViolation", "shop.app.users.users_tenant_email_key", "Key (email)=(a@x.com)"]);
    // A sensitive constraint drops the detail; an unknown one is sensitive.
    expect(v({ code: "23514", schema: "app", table: "users", constraint: "users_check", detail: "x" }).detail).toBe("");
    const unknown = v({ code: "23503", schema: "app", table: "posts", constraint: "posts_fk", detail: "x" });
    expect([unknown.class, unknown.constraint, unknown.detail]).toEqual([
      "ForeignKeyViolation",
      { name: "shop.app.posts.posts_fk", kind: "foreign_key", sensitive: true },
      "",
    ]);
    expect(v({ code: "23P01", schema: "app", table: "rooms", constraint: "no_overlap" }).class).toBe("ExclusionViolation");
    // A domain's check names its data type.
    expect(v({ code: "23514", schema: "app", dataType: "slug", constraint: "slug_check" }).constraint.name).toBe("shop.app.slug.slug_check");
    expect(v({ code: "23514" }).constraint.name).toBe("shop");
    const nn = v({ code: "23502", schema: "app", table: "users", column: "email" }) as NotNullViolation;
    expect([nn.column, nn.constraint.name, nn.message]).toEqual(["shop.app.users.email", "", "dql: NotNullViolation in shop.app.users.email in q (postgres 23502)"]);
    const named = v({ code: "23502", schema: "app", table: "users", column: "email", constraint: "users_email_not_null" }) as NotNullViolation;
    expect(named.constraint.name).toBe("shop.app.users.users_email_not_null");
    expect((v({ code: "23502" }) as NotNullViolation).column).toBe("shop");
  });

  test("aborts", () => {
    const ac = new AbortController();
    ac.abort();
    expect(classOf(t.translate(new Error("x"), "", "q", "main", ac.signal))).toBe("Canceled");
    expect(classOf(t.translate(new Error("x"), "", "q", "main", AbortSignal.abort()))).toBe("Canceled");
    const timeout = new AbortController();
    timeout.abort(new DOMException("late", "TimeoutError"));
    expect(classOf(t.translate(new Error("x"), "", "q", "main", timeout.signal))).toBe("Timeout");
  });
});

describe("retry (§22.4)", () => {
  const serialization = new FakeNative({ code: "40001" });
  const tr = new Translator(new FakeDriver(), "postgres", pg.errors);
  const fail = (e: unknown) => () => Promise.reject(tr.translate(e, "", "q", "main", undefined));

  test("do reruns the classes its policy lists", async () => {
    let runs = 0;
    const err = await retry({ attempts: 3 }).do(() => {
      runs++;
      return fail(serialization)();
    }).catch((e) => e);
    expect([runs, err instanceof Retried, (err as Retried).attempts, classOf(err)]).toEqual([3, true, 3, "SerializationFailure"]);
    runs = 0;
    expect(
      await retry({ on: ["LockTimeout"] }, () => 0).do(async () => {
        runs++;
        if (runs < 2) {
          await fail(new FakeNative({ code: "55P03" }))();
        }
        return runs;
      }),
    ).toBe(2);
    // An error it does not list runs once, unwrapped.
    const once = await retry().do(fail(new FakeNative({ code: "55P03" }))).catch((e) => e);
    expect(classOf(once)).toBe("LockTimeout");
    expect(once instanceof Retried).toBe(false);
  });

  test("do stops when the wait is cut short", async () => {
    const ac = new AbortController();
    ac.abort();
    const { withSignal, withTimeout } = await import("../src/context.js");
    let runs = 0;
    await withSignal(ac.signal, () =>
      retry().do(() => {
        runs++;
        return fail(serialization)();
      }),
    ).catch(() => {});
    expect(runs).toBe(1);
    runs = 0;
    const late = new AbortController();
    const pending = withSignal(late.signal, () =>
      retry({ attempts: 3, initial: 10_000, max: 10_000 }, () => 1).do(() => {
        runs++;
        return fail(serialization)();
      }),
    ).catch(() => {});
    setTimeout(() => late.abort(), 5);
    await pending;
    expect(runs).toBe(1);
    // Inside a transaction it runs once.
    const { within } = await import("../src/context.js");
    runs = 0;
    await within({ frame: {} as never }, () =>
      retry().do(() => {
        runs++;
        return fail(serialization)();
      }),
    ).catch(() => {});
    expect(runs).toBe(1);
    await withTimeout(1000, () => Promise.resolve());
  });

  test("Retried", () => {
    expect(new Retried(2, "plain").message).toBe("plain (after 2 attempts)");
  });
});
