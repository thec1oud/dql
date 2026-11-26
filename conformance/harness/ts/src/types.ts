// The harness's view of a generated package's types. Go's harness reads them
// by reflection; TypeScript erases them, so this reads the declarations the
// emitter writes: its interfaces, type aliases, and handle classes' fields.
// The emitter formats them one member to a line, which this relies on, and
// it fails on anything else it meets, so that a change to the emitter's
// output shows here rather than as a wrong conversion.

/** Type is a type expression of a declaration. */
export type Type =
  | { readonly k: "name"; readonly name: string; readonly args: readonly Type[] }
  | { readonly k: "literal"; readonly value: string }
  | { readonly k: "array"; readonly elem: Type }
  | { readonly k: "union"; readonly of: readonly Type[] }
  | { readonly k: "intersection"; readonly of: readonly Type[] }
  | { readonly k: "object"; readonly fields: readonly Field[] }
  | { readonly k: "enum"; readonly name: string };

/** Field is a member of an interface, an object type or a class. */
export interface Field {
  readonly name: string;
  readonly optional: boolean;
  readonly type: Type;
}

/** Param is a parameter of a method. */
export interface Param {
  readonly name: string;
  readonly type: Type;
  readonly rest: boolean;
}

/** Method is a method of an interface. */
export interface Method {
  readonly name: string;
  readonly params: readonly Param[];
  readonly result: Type;
}

/** Decl is a declaration: an interface or class, with its members, or a type alias. */
export interface Decl {
  readonly name: string;
  readonly extends: readonly string[];
  readonly fields: readonly Field[];
  readonly methods: readonly Method[];
  /** A type alias's type. */
  readonly type?: Type;
}

/** Const is an exported object of methods, such as a shape's conversions, or of objects of methods, such as a table's column handles. */
export interface Const {
  readonly methods: readonly Method[];
  readonly members: ReadonlyMap<string, readonly Method[]>;
}

/** Types holds a package's declarations by name. */
export class Types {
  readonly decls = new Map<string, Decl>();
  readonly consts = new Map<string, Const>();
  readonly funcs = new Map<string, Method>();

  /** add reads the declarations of one module's source. */
  add(source: string): void {
    const lines = source.split("\n");
    for (let i = 0; i < lines.length; i++) {
      const line = lines[i] as string;
      let m = /^export (?:interface|class) (\w+)(?: extends ([\w, ]+))? \{$/.exec(line);
      if (m !== null) {
        const fields: Field[] = [];
        const methods: Method[] = [];
        const isClass = line.startsWith("export class");
        for (i++; lines[i] !== "}"; i++) {
          member(lines[i] as string, isClass, fields, methods);
        }
        this.decls.set(m[1] as string, { name: m[1] as string, extends: m[2]?.split(", ") ?? [], fields, methods });
        continue;
      }
      m = /^export const (\w+): \{$/.exec(line);
      if (m !== null) {
        // A typed object, as the read shapes: its type's fields.
        const fields: Field[] = [];
        for (i++; lines[i] !== "} = {"; i++) {
          member(lines[i] as string, false, fields, []);
        }
        this.decls.set(m[1] as string, { name: m[1] as string, extends: [], fields, methods: [] });
        continue;
      }
      m = /^export const (\w+) = \{$/.exec(line);
      if (m !== null) {
        const methods: Method[] = [];
        const members = new Map<string, Method[]>();
        let in_: Method[] = methods;
        for (i++; !(lines[i] as string).startsWith("}"); i++) {
          const l = lines[i] as string;
          const sub = /^ {2}(\w+): \{$/.exec(l);
          if (sub !== null) {
            in_ = [];
            members.set(sub[1] as string, in_);
            continue;
          }
          if (l === "  },") {
            in_ = methods;
            continue;
          }
          const fm = /^ {2}( {2})?(\w+)\((.*)\): (.*) \{$/.exec(l);
          if (fm !== null && (fm[1] !== undefined) === (in_ !== methods)) {
            in_.push(signature(fm[2] as string, fm[3] as string, fm[4] as string));
          }
        }
        this.consts.set(m[1] as string, { methods, members });
        continue;
      }
      m = /^export function (\w+)\((.*)$/.exec(line);
      if (m !== null) {
        let text = m[2] as string;
        while (!/\): .* \{$/.test(text)) {
          text += ` ${(lines[++i] as string).trim()}`;
        }
        const fm = /^(.*?),? ?\): (.*) \{$/.exec(text) as RegExpExecArray;
        this.funcs.set(m[1] as string, signature(m[1] as string, fm[1] as string, fm[2] as string));
        continue;
      }
      m = /^export type (\w+) =(.*)$/.exec(line);
      if (m !== null) {
        let text = m[2] as string;
        while (!text.endsWith(";")) {
          text += ` ${(lines[++i] as string).trim()}`;
        }
        this.decls.set(m[1] as string, { name: m[1] as string, extends: [], fields: [], methods: [], type: parseType(text.slice(0, -1)) });
      }
    }
  }

  /** decl returns the declaration name, or throws. */
  decl(name: string): Decl {
    const d = this.decls.get(name);
    if (d === undefined) {
      throw new Error(`no type ${name}`);
    }
    return d;
  }

  /** fields returns the fields of an interface or class, its bases' first. */
  fields(name: string): Field[] {
    const d = this.decl(name);
    return [...d.extends.flatMap((b) => this.fields(b)), ...d.fields];
  }

  /** method returns the method of an interface, or of one of its bases, by its name under norm. */
  method(name: string, method: string): Method | undefined {
    const d = this.decl(name);
    return d.methods.find((m) => norm(m.name) === norm(method)) ?? d.extends.map((b) => this.method(b, method)).find((m) => m !== undefined);
  }

  /** resolve follows type aliases until it reaches a type that is not one. */
  resolve(t: Type): Type {
    while (t.k === "name") {
      const alias = this.decls.get(t.name)?.type;
      if (alias === undefined) {
        return t;
      }
      t = alias;
    }
    return t;
  }
}

/** norm spells a name for matching, as the Go harness does: lower case, without underscores. */
export function norm(s: string): string {
  return s.replaceAll("_", "").toLowerCase();
}

/** member reads one line of an interface or class body into fields or methods; a class's lines other than its fields are skipped. */
function member(line: string, isClass: boolean, fields: Field[], methods: Method[]): void {
  if (isClass) {
    const m = /^ {2}readonly (\w+): (.*);$/.exec(line);
    if (m !== null) {
      fields.push({ name: m[1] as string, optional: false, type: parseType(m[2] as string) });
      return;
    }
    // A handle's methods whose parameters are not functions: withScope.
    const fm = /^ {2}(\w+)\(([^()]*)\): ([^()]*) \{$/.exec(line);
    if (fm !== null) {
      methods.push(signature(fm[1] as string, fm[2] as string, fm[3] as string));
    }
    return;
  }
  const text = line.replace(/ \/\/ .*$/, "");
  let m = /^ {2}(?:readonly )?(\w+)(\??): (.*);$/.exec(text);
  if (m !== null) {
    fields.push({ name: m[1] as string, optional: m[2] === "?", type: parseType(m[3] as string) });
    return;
  }
  m = /^ {2}(\w+)(?:<[^>]*>)?\((.*)\): (.*);$/.exec(text);
  if (m === null) {
    throw new Error(`unknown member: ${line}`);
  }
  methods.push(signature(m[1] as string, m[2] as string, m[3] as string));
}

/** signature reads a method's or function's parameter list and result. */
function signature(name: string, params: string, result: string): Method {
  return {
    name,
    params: split(params, ",").map((p): Param => {
      const pm = /^(\.\.\.)?(\w+)\??: (.*)$/.exec(p.trim()) as RegExpExecArray;
      return { name: pm[2] as string, rest: pm[1] !== undefined, type: parseType(pm[3] as string) };
    }),
    result: parseType(result),
  };
}

/** split splits s at each sep outside brackets, braces and parentheses. */
function split(s: string, sep: string): string[] {
  const out: string[] = [];
  let depth = 0;
  let start = 0;
  for (let i = 0; i < s.length; i++) {
    const c = s[i] as string;
    if ("<([{".includes(c)) {
      depth++;
    } else if (">)]}".includes(c)) {
      depth--;
    } else if (c === sep && depth === 0) {
      out.push(s.slice(start, i));
      start = i + 1;
    }
  }
  const last = s.slice(start);
  return last.trim() === "" ? out : [...out, last];
}

const token = /\s*("[^"]*"|[\w.]+|\.\.\.|[|&()[\]{}<>;:,?])/y;

/** parseType parses a type expression of the declarations. */
export function parseType(text: string): Type {
  const tokens: string[] = [];
  token.lastIndex = 0;
  while (token.lastIndex < text.length) {
    const at = token.lastIndex;
    const m = token.exec(text);
    if (m === null) {
      if (text.slice(at).trim() === "") {
        break;
      }
      throw new Error(`cannot read type ${text}`);
    }
    tokens.push(m[1] as string);
  }
  let pos = 0;
  const peek = () => tokens[pos];
  const next = () => {
    const t = tokens[pos++];
    if (t === undefined) {
      throw new Error(`type ${text} ends early`);
    }
    return t;
  };
  const expect = (t: string) => {
    const got = next();
    if (got !== t) {
      throw new Error(`type ${text}: want ${t}, got ${got}`);
    }
  };
  const union = (): Type => {
    if (peek() === "|") {
      next();
    }
    const of = [intersection()];
    while (peek() === "|") {
      next();
      of.push(intersection());
    }
    return of.length === 1 ? (of[0] as Type) : { k: "union", of };
  };
  const intersection = (): Type => {
    const of = [postfix()];
    while (peek() === "&") {
      next();
      of.push(postfix());
    }
    return of.length === 1 ? (of[0] as Type) : { k: "intersection", of };
  };
  const postfix = (): Type => {
    if (peek() === "readonly") {
      next();
    }
    let t = primary();
    while (peek() === "[") {
      next();
      if (peek() === "number") {
        next();
        if (t.k !== "enum") {
          throw new Error(`type ${text}: an index of a type that is not an enum`);
        }
      } else {
        t = { k: "array", elem: t };
      }
      expect("]");
    }
    return t;
  };
  const primary = (): Type => {
    const t = next();
    if (t.startsWith('"')) {
      return { k: "literal", value: t.slice(1, -1) };
    }
    if (t === "(") {
      if (peek() === "typeof") {
        next();
        const name = next();
        expect(")");
        return { k: "enum", name };
      }
      const inner = union();
      expect(")");
      return inner;
    }
    if (t === "{") {
      const fields: Field[] = [];
      while (peek() !== "}") {
        if (peek() === "readonly") {
          next();
        }
        const name = next();
        const optional = peek() === "?";
        if (optional) {
          next();
        }
        expect(":");
        fields.push({ name, optional, type: union() });
        if (peek() === ";" || peek() === ",") {
          next();
        }
      }
      next();
      return { k: "object", fields };
    }
    if (!/^[\w.]+$/.test(t)) {
      throw new Error(`type ${text}: unexpected ${t}`);
    }
    const args: Type[] = [];
    if (peek() === "<") {
      next();
      args.push(union());
      while (peek() === ",") {
        next();
        args.push(union());
      }
      expect(">");
    }
    return { k: "name", name: t, args };
  };
  const t = union();
  if (pos !== tokens.length) {
    throw new Error(`type ${text}: unexpected ${tokens[pos]}`);
  }
  return t;
}
