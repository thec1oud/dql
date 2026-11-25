// Values of the logical type range<T> (design record §8.1), such as a
// Postgres int4range or tstzrange. A multirange<T> is a Range<T>[].

/** Bound is one end of a range: inclusive, exclusive, or absent, which leaves the range unbounded on that side. */
export type Bound<T> =
  | { readonly kind: "unbounded" }
  | { readonly kind: "inclusive"; readonly value: T }
  | { readonly kind: "exclusive"; readonly value: T };

/**
 * Range is the values between two bounds, or the empty range. A Range holds
 * its bounds as given; the database normalizes ranges of discrete types, such
 * as [1,10] into [1,11).
 */
export type Range<T> =
  | { readonly empty: false; readonly lower: Bound<T>; readonly upper: Bound<T> }
  | { readonly empty: true };

/** inclusive returns a bound that includes value. */
export function inclusive<T>(value: T): Bound<T> {
  return { kind: "inclusive", value };
}

/** exclusive returns a bound that excludes value. */
export function exclusive<T>(value: T): Bound<T> {
  return { kind: "exclusive", value };
}

/** unbounded returns an absent bound. */
export function unbounded<T>(): Bound<T> {
  return { kind: "unbounded" };
}

/** range returns the range between lower and upper. */
export function range<T>(lower: Bound<T>, upper: Bound<T>): Range<T> {
  return { empty: false, lower, upper };
}

/** emptyRange returns the empty range. */
export function emptyRange<T>(): Range<T> {
  return { empty: true };
}

/**
 * rangeContains reports whether v lies in r, comparing values with cmp, which
 * returns a negative number, zero or a positive number as its first argument
 * is less than, equal to or greater than its second.
 */
export function rangeContains<T>(r: Range<T>, v: T, cmp: (a: T, b: T) => number): boolean {
  if (r.empty) {
    return false;
  }
  const { lower, upper } = r;
  if (lower.kind === "inclusive" ? cmp(v, lower.value) < 0 : lower.kind === "exclusive" && cmp(v, lower.value) <= 0) {
    return false;
  }
  switch (upper.kind) {
    case "inclusive":
      return cmp(v, upper.value) <= 0;
    case "exclusive":
      return cmp(v, upper.value) < 0;
  }
  return true;
}

/** formatRange writes r as Postgres writes a range, such as [1,10) or (,5], or as empty. */
export function formatRange<T>(r: Range<T>, format: (v: T) => string = String): string {
  if (r.empty) {
    return "empty";
  }
  const { lower, upper } = r;
  const lo = lower.kind === "unbounded" ? "(" : `${lower.kind === "inclusive" ? "[" : "("}${format(lower.value)}`;
  const hi = upper.kind === "unbounded" ? ")" : `${format(upper.value)}${upper.kind === "inclusive" ? "]" : ")"}`;
  return `${lo},${hi}`;
}
