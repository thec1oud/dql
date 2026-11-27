// Helpers generated conversions and merges use (design record §8.9, §14.9).

/** Keyed pairs a value with the key of the row it applies to, such as a Partial merged into rows by key (§8.9). */
export interface Keyed<K, V> {
  readonly key: K;
  readonly value: V;
}

/**
 * foldAscii folds ASCII letters to lower case and leaves every other
 * character as it is: the client fold CLIENT_FOLD_ASCII_LOWER a dialect
 * plugin certifies for a collation (§8.9). Generated merges fold both keys
 * before comparing.
 */
export function foldAscii<S extends string>(s: S): S {
  return s.replace(/[A-Z]+/g, (m) => m.toLowerCase()) as S;
}

/**
 * UnmatchedKeyError reports that a change merged into rows by key matched no
 * row exactly, where the key's collation may equate values the host cannot
 * compare (§8.9). It names the change's position, never the key.
 */
export class UnmatchedKeyError extends Error {
  override readonly name = "UnmatchedKeyError";
  constructor(
    /** The merged entity, such as shop.app.countries. */
    readonly entity: string,
    /** The change's position in the changes merged. */
    readonly index: number,
  ) {
    super(`dql: change ${index} for ${entity} matches no row exactly, and its key's collation may match one the client cannot compare`);
  }
}

/** bytesEqual reports whether two byte arrays hold the same bytes, as generated merges compare bytea keys (§8.9). */
export function bytesEqual(a: Uint8Array, b: Uint8Array): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (a[i] !== b[i]) return false;
  }
  return true;
}
