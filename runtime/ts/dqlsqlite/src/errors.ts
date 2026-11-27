// The fields of an error SQLite reported, as the dialect's violation
// reports read them (§23.2).

import type { NativeError } from "@dql/runtime";

/** UNIQUE constraint failed: t.a, t.b; or, for an expression index, UNIQUE constraint failed: index 'name'. */
const unique = /UNIQUE constraint failed: (.+)$/;
const index = /^index '((?:[^']|'')*)'$/;
/** NOT NULL constraint failed: t.c */
const notNull = /NOT NULL constraint failed: ([^.]+)\.(.+)$/;
/** CHECK constraint failed: name, or the expression of an unnamed check. */
const check = /CHECK constraint failed: (.+)$/;

/**
 * native returns an error's fields by its extended result code and message:
 * a duplicate names the table and the key's columns, as "a, b", or an
 * expression index by its name; a not-null violation names the table and
 * column; a check violation names the constraint, or the expression of one
 * CONSTRAINT gives no name, without its table, which the runtime finds the
 * rule by. A foreign-key violation names nothing. No message quotes values,
 * so there is no detail.
 */
export function native(code: number, message: string): NativeError {
  const n: { -readonly [K in keyof NativeError]: NativeError[K] } = { code: String(code), message };
  let m: RegExpExecArray | null;
  switch (code) {
    case 1555: // SQLITE_CONSTRAINT_PRIMARYKEY
    case 2067: // SQLITE_CONSTRAINT_UNIQUE
      if ((m = unique.exec(message)) !== null) {
        const ix = index.exec(m[1] as string);
        if (ix !== null) {
          n.constraint = (ix[1] as string).replaceAll("''", "'");
          break;
        }
        const cols: string[] = [];
        for (const part of (m[1] as string).split(", ")) {
          const i = part.indexOf(".");
          n.table = part.slice(0, i);
          cols.push(part.slice(i + 1));
        }
        n.constraint = cols.join(", ");
      }
      break;
    case 1299: // SQLITE_CONSTRAINT_NOTNULL
      if ((m = notNull.exec(message)) !== null) {
        n.table = m[1] as string;
        n.column = m[2] as string;
      }
      break;
    case 275: // SQLITE_CONSTRAINT_CHECK
      if ((m = check.exec(message)) !== null) {
        n.constraint = m[1] as string;
      }
  }
  return n;
}

/** The extended result codes of the messages that name them, for a binding that reports no code, as Deno's node:sqlite does. */
const byMessage: readonly [RegExp, number][] = [
  [/^UNIQUE constraint failed/, 2067],
  [/^NOT NULL constraint failed/, 1299],
  [/^CHECK constraint failed/, 275],
  [/^FOREIGN KEY constraint failed/, 787],
  [/^database is locked/, 5],
  [/^database table is locked/, 6],
  [/^interrupted/, 9],
  [/^unable to open database file/, 14],
  [/^attempt to write a readonly database/, 8],
];

/** codeOf returns the extended result code of an error a binding threw, or undefined for an error SQLite did not report. */
export function codeOf(e: unknown): number | undefined {
  if (!(e instanceof Error)) {
    return undefined;
  }
  const x = e as { errcode?: unknown; errno?: unknown; code?: unknown };
  if (typeof x.errcode === "number") {
    return x.errcode; // node:sqlite
  }
  if (typeof x.errno === "number" && typeof x.code === "string" && x.code.startsWith("SQLITE_")) {
    return x.errno; // bun:sqlite
  }
  if (x.code === "ERR_SQLITE_ERROR") {
    return byMessage.find(([re]) => re.test(e.message))?.[1] ?? 1;
  }
  return undefined;
}
