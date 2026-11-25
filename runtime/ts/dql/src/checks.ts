// The checks of the portable constraint kinds (design record §10.2, §10.3).
// Generated validators declare one per client-validated constraint and run it
// on a field's value, pushing an issue when the value breaks it. Every host
// language checks the same semantics, so a value passes here exactly when it
// passes the constraint in the database.

import type { Decimal } from "./decimal.js";
import type { LengthUnit, ValidationIssue } from "./validation.js";

const encoder = new TextEncoder();

/** length counts s in unit: code points, UTF-8 bytes or UTF-16 code units. */
export function length(s: string, unit: LengthUnit): number {
  switch (unit) {
    case "bytes":
      return encoder.encode(s).length;
    case "utf16_code_units":
      return s.length;
    case "characters": {
      let n = 0;
      for (const _ of s) {
        n++;
      }
      return n;
    }
  }
}

/** LengthCheck is a length constraint, counted in the database's unit (§10.3). An absent min or max is unbounded. */
export class LengthCheck {
  constructor(
    readonly constraint: string,
    readonly unit: LengthUnit,
    readonly min?: number,
    readonly max?: number,
  ) {}

  /** check pushes an issue to issues when s's length is out of bounds. */
  check(issues: ValidationIssue[], shape: string, field: string, s: string): void {
    const n = length(s, this.unit);
    if ((this.min === undefined || n >= this.min) && (this.max === undefined || n <= this.max)) {
      return;
    }
    issues.push({
      shape,
      field,
      kind: "length",
      constraint: this.constraint,
      ...(this.min === undefined ? {} : { min: this.min }),
      ...(this.max === undefined ? {} : { max: this.max }),
      unit: this.unit,
      got: n,
    });
  }
}

/** NotEmptyCheck is a not_empty constraint: a length of at least 1, without trimming. */
export class NotEmptyCheck {
  constructor(readonly constraint: string) {}

  /** check pushes an issue to issues when s is empty. */
  check(issues: ValidationIssue[], shape: string, field: string, s: string): void {
    if (s === "") {
      issues.push({ shape, field, kind: "not_empty", constraint: this.constraint });
    }
  }
}

/**
 * PatternCheck is a pattern constraint: an unanchored search with a regex in
 * the portable subset, matched over code points (§10.2). pattern is the
 * constraint's own text, which issues report; regex is its translation to a
 * JavaScript RegExp with the u flag, which the emitter writes so that ^ and $
 * match the whole string and \d, \w and \s are ASCII only.
 */
export class PatternCheck {
  constructor(
    readonly constraint: string,
    readonly pattern: string,
    readonly regex: RegExp,
  ) {}

  /** check pushes an issue to issues when s does not match. */
  check(issues: ValidationIssue[], shape: string, field: string, s: string): void {
    this.regex.lastIndex = 0;
    if (!this.regex.test(s)) {
      issues.push({ shape, field, kind: "pattern", constraint: this.constraint, pattern: this.pattern });
    }
  }
}

/** The bounds of a RangeCheck. An absent min or max is unbounded. */
export interface RangeBounds<T> {
  readonly min?: T;
  readonly max?: T;
  readonly minInclusive: boolean;
  readonly maxInclusive: boolean;
}

/** RangeCheck is a range constraint, compared in the field's logical type with compare. */
export class RangeCheck<T> {
  constructor(
    readonly constraint: string,
    readonly bounds: RangeBounds<T>,
    readonly compare: (a: T, b: T) => number,
  ) {}

  /** check pushes an issue to issues when x is out of bounds. */
  check(issues: ValidationIssue[], shape: string, field: string, x: T): void {
    const { min, max, minInclusive, maxInclusive } = this.bounds;
    const above = min === undefined || this.compare(x, min) > 0 || (minInclusive && this.compare(x, min) === 0);
    const below = max === undefined || this.compare(x, max) < 0 || (maxInclusive && this.compare(x, max) === 0);
    if (above && below) {
      return;
    }
    issues.push({
      shape,
      field,
      kind: "range",
      constraint: this.constraint,
      ...(min === undefined ? {} : { min }),
      ...(max === undefined ? {} : { max }),
      minInclusive,
      maxInclusive,
    });
  }
}

/** DigitsCheck is a digits constraint: the value fits decimal(precision, scale) after rounding to scale, as the database stores it. */
export class DigitsCheck {
  constructor(
    readonly constraint: string,
    readonly precision: number,
    readonly scale: number,
  ) {}

  /** check pushes an issue to issues when x does not fit. */
  check(issues: ValidationIssue[], shape: string, field: string, x: Decimal): void {
    if (!x.fits(this.precision, this.scale)) {
      issues.push({ shape, field, kind: "digits", constraint: this.constraint, precision: this.precision, scale: this.scale });
    }
  }
}

/** MembershipCheck is a membership constraint: equal to one listed value, by equals, which defaults to ===. */
export class MembershipCheck<T> {
  constructor(
    readonly constraint: string,
    readonly values: readonly T[],
    readonly equals: (a: T, b: T) => boolean = (a, b) => a === b,
  ) {}

  /** check pushes an issue to issues when x is none of the values. */
  check(issues: ValidationIssue[], shape: string, field: string, x: T): void {
    if (!this.values.some((v) => this.equals(x, v))) {
      issues.push({ shape, field, kind: "membership", constraint: this.constraint, values: this.values });
    }
  }
}
