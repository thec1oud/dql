// Postgres's text forms of arrays, composite values, ranges and multiranges:
// parsing what the server writes, and writing what it reads.

/** A parsed array: its elements, each a nested array for another dimension, text, or null for NULL. */
export type ArrayText = (ArrayText | string | null)[];

/** A parsed range: empty, or two bounds, each absent when unbounded. */
export type RangeText = { readonly empty: true } | { readonly empty: false; readonly lower: BoundText; readonly upper: BoundText };

/** BoundText is one bound of a parsed range. */
export type BoundText = { readonly kind: "unbounded" } | { readonly kind: "inclusive" | "exclusive"; readonly value: string };

/** Reader walks a literal. */
class Reader {
  i = 0;

  constructor(readonly s: string) {}

  peek(): string {
    return this.s[this.i] ?? "";
  }

  expect(c: string): void {
    if (this.s[this.i] !== c) {
      throw new SyntaxError(`dqlpg: malformed literal ${JSON.stringify(this.s)}: expected ${c} at ${this.i}`);
    }
    this.i++;
  }

  spaces(): void {
    while (this.peek() === " ") {
      this.i++;
    }
  }

  /** quoted reads a double-quoted string, in which a backslash escapes the next character and, doubled, a quote is a quote. */
  quoted(): string {
    this.expect('"');
    let out = "";
    for (;;) {
      const c = this.s[this.i++];
      if (c === undefined) {
        throw new SyntaxError(`dqlpg: malformed literal ${JSON.stringify(this.s)}: unterminated quote`);
      }
      if (c === "\\") {
        out += this.s[this.i++] ?? "";
      } else if (c === '"') {
        if (this.peek() !== '"') {
          return out;
        }
        out += '"';
        this.i++;
      } else {
        out += c;
      }
    }
  }

  /** unquoted reads text up to one of the stops, in which a backslash escapes the next character. */
  unquoted(stops: string): string {
    let out = "";
    while (this.i < this.s.length && !stops.includes(this.peek())) {
      const c = this.s[this.i++] as string;
      out += c === "\\" ? (this.s[this.i++] ?? "") : c;
    }
    return out;
  }
}

/** parseArray parses an array literal, such as {1,NULL,"a b"} or [0:1]={{1},{2}}. */
export function parseArray(s: string): ArrayText {
  const eq = s.startsWith("[") ? s.indexOf("=") : -1;
  const r = new Reader(eq < 0 ? s : s.slice(eq + 1));
  const out = arrayOf(r);
  if (r.i !== r.s.length) {
    throw new SyntaxError(`dqlpg: malformed literal ${JSON.stringify(s)}: trailing text`);
  }
  return out;
}

function arrayOf(r: Reader): ArrayText {
  r.expect("{");
  const out: ArrayText = [];
  if (r.peek() === "}") {
    r.i++;
    return out;
  }
  for (;;) {
    r.spaces();
    const c = r.peek();
    if (c === "{") {
      out.push(arrayOf(r));
    } else if (c === '"') {
      out.push(r.quoted());
    } else {
      const text = r.unquoted(",}").trim();
      out.push(text.toUpperCase() === "NULL" ? null : text);
    }
    r.spaces();
    if (r.peek() === "}") {
      r.i++;
      return out;
    }
    r.expect(",");
  }
}

/** parseComposite parses a composite value's literal, such as (1,,"a ""b""") into its attributes, an empty one being NULL. */
export function parseComposite(s: string): (string | null)[] {
  const r = new Reader(s);
  r.expect("(");
  const out: (string | null)[] = [];
  for (;;) {
    const c = r.peek();
    if (c === '"') {
      // A quoted part may run on unquoted, as in "a"b.
      let text = r.quoted();
      text += r.unquoted(",)");
      out.push(text);
    } else {
      const text = r.unquoted(",)");
      out.push(text === "" ? null : text);
    }
    if (r.peek() === ")") {
      r.i++;
      break;
    }
    r.expect(",");
  }
  if (r.i !== s.length) {
    throw new SyntaxError(`dqlpg: malformed literal ${JSON.stringify(s)}: trailing text`);
  }
  return out;
}

/** parseRange parses a range literal, such as [1,5), (,"x"] or empty. */
export function parseRange(s: string): RangeText {
  const r = new Reader(s);
  const out = rangeOf(r);
  if (r.i !== s.length) {
    throw new SyntaxError(`dqlpg: malformed literal ${JSON.stringify(s)}: trailing text`);
  }
  return out;
}

function rangeOf(r: Reader): RangeText {
  if (r.s.startsWith("empty", r.i)) {
    r.i += 5;
    return { empty: true };
  }
  const open = r.s[r.i++];
  if (open !== "[" && open !== "(") {
    throw new SyntaxError(`dqlpg: malformed literal ${JSON.stringify(r.s)}: expected [ or (`);
  }
  const lower = boundOf(r, ",");
  r.expect(",");
  const upper = boundOf(r, "])");
  const close = r.s[r.i++];
  if (close !== "]" && close !== ")") {
    throw new SyntaxError(`dqlpg: malformed literal ${JSON.stringify(r.s)}: expected ] or )`);
  }
  return {
    empty: false,
    lower: lower === undefined ? { kind: "unbounded" } : { kind: open === "[" ? "inclusive" : "exclusive", value: lower },
    upper: upper === undefined ? { kind: "unbounded" } : { kind: close === "]" ? "inclusive" : "exclusive", value: upper },
  };
}

/** boundOf reads one bound's text, or undefined when it is absent. */
function boundOf(r: Reader, stops: string): string | undefined {
  if (r.peek() === '"') {
    return r.quoted() + r.unquoted(stops);
  }
  const text = r.unquoted(stops);
  return text === "" ? undefined : text;
}

/** parseMultirange parses a multirange literal, such as {[1,3), [5,7)}. */
export function parseMultirange(s: string): RangeText[] {
  const r = new Reader(s);
  r.expect("{");
  const out: RangeText[] = [];
  r.spaces();
  if (r.peek() === "}") {
    r.i++;
  } else {
    for (;;) {
      r.spaces();
      out.push(rangeOf(r));
      r.spaces();
      if (r.peek() === "}") {
        r.i++;
        break;
      }
      r.expect(",");
    }
  }
  if (r.i !== s.length) {
    throw new SyntaxError(`dqlpg: malformed literal ${JSON.stringify(s)}: trailing text`);
  }
  return out;
}

/** quote writes s as a double-quoted element of an array, composite or range literal. */
export function quote(s: string): string {
  return `"${s.replace(/[\\"]/g, "\\$&")}"`;
}
