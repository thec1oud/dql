// The fields of an error SQL Server sent. A table is named schema.table, or
// database.schema.table in a not-null violation; the database, the
// catalog, is dropped.

import type { NativeError } from "@dql/runtime";

/** Violation of PRIMARY KEY constraint 'k'. Cannot insert duplicate key in object 'app.users'. ... */
const keyConstraint = /^Violation of (?:PRIMARY|UNIQUE) KEY constraint '((?:[^']|'')*)'\. Cannot insert duplicate key in object '((?:[^']|'')*)'/;
/** Cannot insert duplicate key row in object 'app.users' with unique index 'i'. ... */
const keyIndex = /^Cannot insert duplicate key row in object '((?:[^']|'')*)' with unique index '((?:[^']|'')*)'/;
/** The INSERT statement conflicted with the FOREIGN KEY constraint "c". The conflict occurred in database "d", table "app.t", column 'c'. */
const conflict = /conflicted with the (FOREIGN KEY|REFERENCE|CHECK) constraint "((?:[^"]|"")*)"\. The conflict occurred in database "(?:[^"]|"")*", table "((?:[^"]|"")*)"/;
/** Cannot insert the value NULL into column 'c', table 'd.app.t'; ... */
const nullColumn = /^Cannot insert the value NULL into column '((?:[^']|'')*)', table '((?:[^']|'')*)'/;

const unquote = (s: string, q: string) => s.replaceAll(q + q, q);

/** split splits a [database.]schema.table name into its schema and table. */
function split(name: string): { schema?: string; table: string } {
  const parts = name.split(".");
  return parts.length < 2 ? { table: name } : { schema: parts[parts.length - 2] as string, table: parts[parts.length - 1] as string };
}

/**
 * native returns the fields of an error the server sent, by its number and
 * message. SQL Server reports a foreign-key and a check violation under one
 * number, 547, so a check's code is 547/CHECK, as the dialect's error map has
 * it (§23.1). The table a foreign-key message names is the referenced one on
 * an insert, so only its constraint is kept. A duplicate's message quotes the
 * duplicate key's values, so it is also the detail, which the runtime drops
 * when the key is sensitive (§23.3).
 */
export function native(number: number, message: string): NativeError {
  const n: { -readonly [K in keyof NativeError]: NativeError[K] } = { code: String(number), message };
  let m: RegExpExecArray | null;
  switch (number) {
    case 2627:
      n.detail = message;
      if ((m = keyConstraint.exec(message)) !== null) {
        Object.assign(n, { constraint: unquote(m[1] as string, "'") }, split(unquote(m[2] as string, "'")));
      }
      break;
    case 2601:
      n.detail = message;
      if ((m = keyIndex.exec(message)) !== null) {
        Object.assign(n, split(unquote(m[1] as string, "'")), { constraint: unquote(m[2] as string, "'") });
      }
      break;
    case 547:
      if ((m = conflict.exec(message)) !== null) {
        n.constraint = unquote(m[2] as string, '"');
        if (m[1] === "CHECK") {
          Object.assign(n, { code: "547/CHECK" }, split(unquote(m[3] as string, '"')));
        }
      }
      break;
    case 515:
      if ((m = nullColumn.exec(message)) !== null) {
        Object.assign(n, { column: unquote(m[1] as string, "'") }, split(unquote(m[2] as string, "'")));
      }
      break;
  }
  return n;
}
