import { describe, expect, test } from "bun:test";
import {
  Canceled,
  CheckViolation,
  ConcurrentModification,
  ConnectionError,
  type Constraint,
  DatabaseError,
  Deadlock,
  DecodeError,
  type DqlError,
  type ErrorClass,
  ExclusionViolation,
  ForeignKeyViolation,
  LockTimeout,
  NotFound,
  NotNullViolation,
  PoolExhausted,
  ReplicaUnavailable,
  SerializationFailure,
  Timeout,
  TooManyRows,
  UniqueViolation,
  classOf,
  isViolation,
} from "../src/index.js";

const origin = { dialect: "postgres", code: "23505", query: "users.insert" };
const tenantEmailKey: Constraint = { name: "shop.app.users.users_tenant_email_key", kind: "unique", sensitive: false };
const detail = "Key (tenant_id, email)=(1, a@x.com) already exists.";

describe("taxonomy", () => {
  test.each<[DqlError, ErrorClass, string]>([
    [new NotFound({ query: "users.get" }), "NotFound", "dql: NotFound in users.get"],
    [new TooManyRows({ query: "users.one" }), "TooManyRows", "dql: TooManyRows in users.one"],
    [new UniqueViolation(tenantEmailKey, detail, origin), "UniqueViolation",
      "dql: UniqueViolation of shop.app.users.users_tenant_email_key in users.insert (postgres 23505)"],
    [new ForeignKeyViolation(tenantEmailKey, detail, origin), "ForeignKeyViolation",
      "dql: ForeignKeyViolation of shop.app.users.users_tenant_email_key in users.insert (postgres 23505)"],
    [new CheckViolation(tenantEmailKey, detail, origin), "CheckViolation",
      "dql: CheckViolation of shop.app.users.users_tenant_email_key in users.insert (postgres 23505)"],
    [new ExclusionViolation(tenantEmailKey, detail, origin), "ExclusionViolation",
      "dql: ExclusionViolation of shop.app.users.users_tenant_email_key in users.insert (postgres 23505)"],
    [new NotNullViolation("shop.app.users.email", tenantEmailKey, detail, origin), "NotNullViolation",
      "dql: NotNullViolation of shop.app.users.users_tenant_email_key in users.insert (postgres 23505)"],
    [new NotNullViolation("shop.app.users.email", undefined, undefined, { dialect: "sqlite" }), "NotNullViolation",
      "dql: NotNullViolation in shop.app.users.email (sqlite)"],
    [new SerializationFailure({ code: "40001" }), "SerializationFailure", "dql: SerializationFailure (40001)"],
    [new Deadlock(), "Deadlock", "dql: Deadlock"],
    [new LockTimeout(), "LockTimeout", "dql: LockTimeout"],
    [new ConcurrentModification({ query: "posts.patch" }), "ConcurrentModification", "dql: ConcurrentModification in posts.patch"],
    [new PoolExhausted("primary"), "PoolExhausted", "dql: PoolExhausted on source primary"],
    [new ReplicaUnavailable(""), "ReplicaUnavailable", "dql: ReplicaUnavailable"],
    [new ConnectionError("primary", new Error("EOF")), "ConnectionError", "dql: ConnectionError on source primary: EOF"],
    [new ConnectionError(""), "ConnectionError", "dql: ConnectionError"],
    [new Timeout("deadline exceeded"), "Timeout", "dql: Timeout: deadline exceeded"],
    [new Timeout(), "Timeout", "dql: Timeout"],
    [new Canceled(new Error("canceled")), "Canceled", "dql: Canceled: canceled"],
    [new Canceled(), "Canceled", "dql: Canceled"],
    [new DatabaseError('invalid input syntax for type integer: "abc"', "", { dialect: "postgres", code: "22P02" }),
      "DatabaseError", "dql: DatabaseError (postgres 22P02)"],
    [new DatabaseError(), "DatabaseError", "dql: DatabaseError"],
    [new DecodeError({ shape: "published_post.full.flat", field: "title", reason: "null_into_non_null",
      assertion: "core.nonnull from COMMENT ON COLUMN app.published_posts.title" }), "DecodeError",
      "dql: DecodeError published_post.full.flat field title: null_into_non_null, asserted by core.nonnull from COMMENT ON COLUMN app.published_posts.title"],
    [new DecodeError({ shape: "shop.app.post_status", reason: "unknown_enum_label" }, { query: "posts.get" }), "DecodeError",
      "dql: DecodeError shop.app.post_status: unknown_enum_label in posts.get"],
  ])("%s", (err, cls, msg) => {
    expect(err.class).toBe(cls);
    expect(err.name).toBe(cls);
    expect(err.message).toBe(msg);
    expect(err).toBeInstanceOf(Error);
    expect(classOf(new Error("creating user", { cause: err }))).toBe(cls);
  });

  test("classOf of other values", () => {
    expect(classOf(undefined)).toBeUndefined();
    expect(classOf(null)).toBeUndefined();
    expect(classOf(new Error("EOF"))).toBeUndefined();
    expect(classOf("text")).toBeUndefined();
    const loop = new Error("a") as Error & { cause?: unknown };
    loop.cause = loop;
    expect(classOf(loop)).toBeUndefined();
  });

  test("causes", () => {
    const eof = new Error("EOF");
    expect(new ConnectionError("primary", eof).cause).toBe(eof);
    expect(new Timeout(eof).cause).toBe(eof);
    expect(new Canceled(eof).cause).toBe(eof);
    expect("cause" in new Timeout()).toBe(false);
  });

  test("fields", () => {
    const v = new UniqueViolation(tenantEmailKey, detail, origin);
    expect(v.constraint).toBe(tenantEmailKey);
    expect(v.detail).toBe(detail);
    expect(v.origin).toBe(origin);
    expect(v.message).not.toContain("a@x.com");
    const d = new DatabaseError("msg", "det");
    expect([d.databaseMessage, d.detail]).toEqual(["msg", "det"]);
    expect(new PoolExhausted("p").source).toBe("p");
    expect(new NotNullViolation("c").column).toBe("c");
    const de = new DecodeError({ shape: "s", reason: "off_type" });
    expect([de.shape, de.field, de.reason, de.assertion]).toEqual(["s", "", "off_type", ""]);
  });
});

test("isViolation", () => {
  const err = new Error("insert", { cause: new UniqueViolation(tenantEmailKey) });
  expect(isViolation(err, tenantEmailKey)).toBe(true);
  const other: Constraint = { name: "shop.app.users.users_pkey", kind: "primary_key", sensitive: false };
  expect(isViolation(err, other)).toBe(false);
  expect(isViolation(new NotFound(), tenantEmailKey)).toBe(false);
  expect(isViolation(undefined, tenantEmailKey)).toBe(false);
});
