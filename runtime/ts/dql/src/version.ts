// A runtime version mismatch fails at import time in TypeScript (§26.5).

/** The generated-code versions this runtime implements. */
export const MIN_VERSION = 1;
export const MAX_VERSION = 1;

/** VersionError is thrown when generated code needs a version this runtime does not implement. */
export class VersionError extends Error {
  override readonly name = "VersionError";
  constructor(readonly version: number) {
    super(`dql: generated code needs runtime version ${version}; @dql/runtime implements ${MIN_VERSION} to ${MAX_VERSION}`);
  }
}

/**
 * enforceVersion throws when this runtime does not implement version, the
 * generated-code version a generated module was generated for. Each generated
 * module calls it at its top, so a mismatch fails when the module is imported.
 */
export function enforceVersion(version: number): void {
  if (!Number.isInteger(version) || version < MIN_VERSION || version > MAX_VERSION) {
    throw new VersionError(version);
  }
}
