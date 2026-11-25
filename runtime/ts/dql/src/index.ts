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
