/** JsonValue is a value of a json or jsonb column (design record §8.1). */
export type JsonValue = null | boolean | number | string | JsonValue[] | { [key: string]: JsonValue };
