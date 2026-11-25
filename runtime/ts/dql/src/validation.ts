// Validation issues and the error that carries them (design record §10.5,
// §14.5, §20.2). Fields, kinds and messages are those of the Go runtime.

import { DqlError, type Origin } from "./errors.js";

/**
 * IssueKind is the rule a validation issue breaks: one of the portable
 * constraint kinds of §10.2, or a rule of the shape algebra (§14.5, §20.2).
 */
export type IssueKind =
  | "length"
  | "range"
  | "digits"
  | "membership"
  | "not_empty"
  | "pattern"
  /** An FK column and its owning relation are both given and disagree (§14.5). */
  | "relation_conflict"
  /** A NOT NULL FK is given neither through its column nor through its owning relation (§14.5). */
  | "relation_missing"
  /** A field is assigned both by the payload and by an expression (§20.2). */
  | "field_set_twice";

/** LengthUnit is the unit a length is counted in (§10.3): never the host's own, but the database's. */
export type LengthUnit = "characters" | "bytes" | "utf16_code_units";

/**
 * ValidationIssue is one broken rule (§10.5). Its fields are the same in every
 * host language: the field path, the kind, the constraint's qualified name
 * and its parameters. It never holds the value that broke the rule. Fields a
 * kind has no use for are absent.
 */
export interface ValidationIssue {
  /** The shape validated, such as post.draft.flat. */
  readonly shape?: string;
  /** The field's path: stored names joined by dots, with [i] after a list, such as posts_to_author[1].title. */
  readonly field?: string;
  readonly kind: IssueKind;
  /** The constraint's qualified name; absent for the rules of the shape algebra. */
  readonly constraint?: string;
  /** The bounds of a length (a number) or a range (in the field's logical type); absent means unbounded. */
  readonly min?: unknown;
  readonly max?: unknown;
  /** Whether a range includes its bounds. */
  readonly minInclusive?: boolean;
  readonly maxInclusive?: boolean;
  /** The unit of a length. */
  readonly unit?: LengthUnit;
  /** The length counted in unit. */
  readonly got?: number;
  /** The parameters of digits. */
  readonly precision?: number;
  readonly scale?: number;
  /** The listed values of membership. */
  readonly values?: readonly unknown[];
  /** The regex of pattern, in the portable subset of §10.2. */
  readonly pattern?: string;
  /** The second field of a relation rule: the owning relation when field is the FK column. */
  readonly other?: string;
}

/**
 * describeIssue describes an issue without the value that broke it. It leads
 * with the field's path, or, for a value checked on its own such as by a
 * domain's parse function, with the shape or domain checked.
 */
export function describeIssue(i: ValidationIssue): string {
  const lead = i.field ? `${i.field}: ` : i.shape ? `${i.shape}: ` : "";
  let what: string;
  switch (i.kind) {
    case "length":
      what = `length ${i.got} ${i.unit}, want ${bounds(i.min, i.max, true, true)}`;
      break;
    case "range":
      what = `out of range ${bounds(i.min, i.max, i.minInclusive === true, i.maxInclusive === true)}`;
      break;
    case "digits":
      what = `does not fit decimal(${i.precision},${i.scale})`;
      break;
    case "membership":
      what = `not one of ${i.values?.length ?? 0} listed values`;
      break;
    case "not_empty":
      what = "empty";
      break;
    case "pattern":
      what = `does not match ${JSON.stringify(i.pattern)}`;
      break;
    case "relation_conflict":
      what = `disagrees with ${i.other}`;
      break;
    case "relation_missing":
      what = `required, through it or ${i.other}`;
      break;
    case "field_set_twice":
      what = "set by both the payload and an expression";
      break;
  }
  return `${lead}${what}${i.constraint ? ` (${i.constraint})` : ""}`;
}

/** bounds formats an interval such as [1, 200] or (0, ∞). */
function bounds(min: unknown, max: unknown, minIn: boolean, maxIn: boolean): string {
  const lo = minIn && min !== undefined ? "[" : "(";
  const hi = maxIn && max !== undefined ? "]" : ")";
  return `${lo}${min === undefined ? "-∞" : String(min)}, ${max === undefined ? "∞" : String(max)}${hi}`;
}

/**
 * ValidationError is the error a write or a domain's parse function throws
 * when validation found issues (§10.5). Its class in the taxonomy is
 * ValidationErrors.
 */
export class ValidationError extends DqlError {
  constructor(
    readonly issues: readonly ValidationIssue[],
    origin: Origin = {},
  ) {
    super("ValidationErrors", "", origin);
    this.message = `dql: ValidationErrors: ${issues.map(describeIssue).join("; ")}`;
  }
}

/** throwIfIssues throws a ValidationError when issues is not empty. */
export function throwIfIssues(issues: readonly ValidationIssue[]): void {
  if (issues.length > 0) {
    throw new ValidationError(issues);
  }
}

/**
 * issuesAt returns copies of issues with every field path, and every other
 * path, placed under prefix, for issues found in a nested shape:
 * issuesAt(issues, "posts_to_author[1]") turns title into
 * posts_to_author[1].title.
 */
export function issuesAt(issues: readonly ValidationIssue[], prefix: string): ValidationIssue[] {
  return issues.map((i) => ({
    ...i,
    field: join(prefix, i.field ?? ""),
    ...(i.other ? { other: join(prefix, i.other) } : {}),
  }));
}

function join(prefix: string, path: string): string {
  if (prefix === "") {
    return path;
  }
  return path === "" || path.startsWith("[") ? prefix + path : `${prefix}.${path}`;
}
