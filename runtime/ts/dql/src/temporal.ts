// The time types of design record §8.8 are Temporal's. A runtime with native
// Temporal uses it; one without gets temporal-polyfill, loaded only then.
import type { Temporal as Polyfill } from "temporal-polyfill";

/**
 * Temporal is the runtime's native Temporal when it has one, else the
 * polyfill. Generated code takes its time types from here, so every value it
 * builds and decodes comes from one implementation.
 */
export const Temporal: typeof Polyfill = await chooseTemporal(
  (globalThis as { Temporal?: typeof Polyfill }).Temporal,
  loadPolyfill,
);

/** loadPolyfill imports temporal-polyfill's Temporal. */
export async function loadPolyfill(): Promise<typeof Polyfill> {
  return (await import("temporal-polyfill")).Temporal;
}

/** chooseTemporal returns native when it is present, else what load gives. */
export async function chooseTemporal<T>(native: T | undefined, load: () => Promise<T>): Promise<T> {
  return native ?? load();
}

/** The Temporal types the logical types of §8.8 map to. */
export declare namespace Temporal {
  /** instant */
  type Instant = Polyfill.Instant;
  /** instant_with_offset, with a fixed-offset time zone */
  type ZonedDateTime = Polyfill.ZonedDateTime;
  /** local_datetime */
  type PlainDateTime = Polyfill.PlainDateTime;
  /** date */
  type PlainDate = Polyfill.PlainDate;
  /** local_time */
  type PlainTime = Polyfill.PlainTime;
  /** interval */
  type Duration = Polyfill.Duration;
}
