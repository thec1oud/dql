// Values of weak columns: columns that can hold any storage class, as in a
// non-STRICT SQLite table (design record §8.10).

/**
 * StorageValue is a raw value of one of a dialect's storage classes: integer,
 * real, text and blob, which are SQLite's and the IR's StorageClass. A raw
 * value is never NULL; nullability is a separate axis.
 */
export type StorageValue =
  | { readonly class: "integer"; readonly value: bigint }
  | { readonly class: "real"; readonly value: number }
  | { readonly class: "text"; readonly value: string }
  | { readonly class: "blob"; readonly value: Uint8Array };

/** StorageClass is the name of a storage class. */
export type StorageClass = StorageValue["class"];

/**
 * Weak is a value of affinity T, or a raw value of another storage class that
 * some writer stored. Reading a raw value is not an error (§13); writing one
 * takes explicit construction with weakRaw, so it is always visible in code.
 * Raw values skip client-side validation (§10.5). A nullable weak column is
 * Weak<T> | null.
 */
export type Weak<T> = { readonly ok: true; readonly value: T } | { readonly ok: false; readonly raw: StorageValue };

/** weakOf returns a value of the affinity type: the normal, typed value. */
export function weakOf<T>(value: T): Weak<T> {
  return { ok: true, value };
}

/** weakRaw returns an explicit raw value, written as it is. */
export function weakRaw<T>(raw: StorageValue): Weak<T> {
  return { ok: false, raw };
}
