// The fields of an error MySQL or MariaDB sent. A MySQL catalog is one
// database, so the database a message names is dropped and the table kept.

import type { NativeError } from "@dql/runtime";

/** Duplicate entry 'x' for key 'users.email'; MariaDB and MySQL before 8.0.19 write the key alone. */
const duplicate = /for key '((?:[^']|'')*)'$/;
/** ... a foreign key constraint fails (`db`.`t`, CONSTRAINT `c` FOREIGN KEY ... */
const foreignKey = /\(`(?:[^`]|``)*`\.`((?:[^`]|``)*)`, CONSTRAINT `((?:[^`]|``)*)`/;
/** Check constraint 'c' is violated. */
const check = /^Check constraint '((?:[^']|'')*)' is violated/;
/** MariaDB: CONSTRAINT `c` failed for `db`.`t` */
const mariaCheck = /^CONSTRAINT `((?:[^`]|``)*)` failed for `(?:[^`]|``)*`\.`((?:[^`]|``)*)`/;
/** Column 'c' cannot be null; Field 'c' doesn't have a default value; ... NULL supplied to NOT NULL column 'c' at row 1 */
const column = /(?:^Column|^Field|NOT NULL column) '((?:[^']|'')*)'/;

const unquote = (s: string, q: string) => s.replaceAll(q + q, q);

/**
 * native returns the fields of an error the server sent, by its error number
 * and message. A duplicate's message quotes the duplicate key's values, so it
 * is also the detail, which the runtime drops when the key is sensitive
 * (§23.3). MySQL names the table only in a foreign-key violation, a duplicate
 * and MariaDB's check violation; the runtime finds the others' rules by name.
 */
export function native(errno: number, message: string): NativeError {
  const n: { -readonly [K in keyof NativeError]: NativeError[K] } = { code: String(errno), message };
  let m: RegExpExecArray | null;
  switch (errno) {
    case 1062:
    case 1586:
      n.detail = message;
      if ((m = duplicate.exec(message)) !== null) {
        let key = unquote(m[1] as string, "'");
        const i = key.indexOf(".");
        if (i >= 0) {
          n.table = key.slice(0, i);
          key = key.slice(i + 1);
        }
        n.constraint = key;
      }
      break;
    case 1451:
    case 1452:
      if ((m = foreignKey.exec(message)) !== null) {
        n.table = unquote(m[1] as string, "`");
        n.constraint = unquote(m[2] as string, "`");
      }
      break;
    case 3819:
      if ((m = check.exec(message)) !== null) {
        n.constraint = unquote(m[1] as string, "'");
      }
      break;
    case 4025:
      if ((m = mariaCheck.exec(message)) !== null) {
        n.constraint = unquote(m[1] as string, "`");
        n.table = unquote(m[2] as string, "`");
      }
      break;
    case 1048:
    case 1263:
    case 1364:
      if ((m = column.exec(message)) !== null) {
        n.column = unquote(m[1] as string, "'");
      }
      break;
  }
  return n;
}
