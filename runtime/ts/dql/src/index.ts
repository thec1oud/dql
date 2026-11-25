// @dql/runtime: the TypeScript runtime that dql's generated code uses
// (design record §15, §26).
export { MAX_VERSION, MIN_VERSION, VersionError, enforceVersion } from "./version.js";
export { Temporal } from "./temporal.js";
export { Decimal, MAX_PARSE_EXPONENT } from "./decimal.js";
export { type Cidr, type Inet, type MacAddr, type Uuid, parseCidr, parseInet, parseMacAddr, parseUuid } from "./brands.js";
export type { JsonValue } from "./json.js";
export { type StorageClass, type StorageValue, type Weak, weakOf, weakRaw } from "./weak.js";
export {
  type Bound,
  type Range,
  emptyRange,
  exclusive,
  formatRange,
  inclusive,
  range,
  rangeContains,
  unbounded,
} from "./range.js";
export { OffsetTime } from "./offset-time.js";
export {
  Canceled,
  CheckViolation,
  ConcurrentModification,
  ConnectionError,
  type Constraint,
  type ConstraintKind,
  DatabaseError,
  Deadlock,
  DecodeError,
  type DecodeFailure,
  type DecodeReason,
  DqlError,
  type ErrorClass,
  ExclusionViolation,
  ForeignKeyViolation,
  LockTimeout,
  NotFound,
  NotNullViolation,
  type Origin,
  PoolExhausted,
  ReplicaUnavailable,
  SerializationFailure,
  Timeout,
  TooManyRows,
  UniqueViolation,
  Violation,
  classOf,
  isViolation,
} from "./errors.js";
export {
  type IssueKind,
  type LengthUnit,
  ValidationError,
  type ValidationIssue,
  describeIssue,
  issuesAt,
  throwIfIssues,
} from "./validation.js";
export { DigitsCheck, LengthCheck, MembershipCheck, NotEmptyCheck, PatternCheck, type RangeBounds, RangeCheck, length } from "./checks.js";
export { type Keyed, UnmatchedKeyError, bytesEqual, foldAscii } from "./collections.js";
export {
  type Converter,
  Decoder,
  Row,
  array,
  asBigint,
  asBoolean,
  asBytes,
  asDate,
  asEnum,
  asFloat32,
  asFloat64,
  asInstanceOf,
  asInt16,
  asInt32,
  asInt64,
  asInt8,
  asJson,
  asMultirange,
  asRange,
  asString,
  asUint16,
  asUint32,
  asUint64,
  asUint8,
  elem,
  nest,
  nullableElem,
  unchecked,
  weak,
} from "./decode.js";
