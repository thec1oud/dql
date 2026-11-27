// Branded strings for the identifier and network types (design record §8.1).
// A brand marks text that has passed its type's syntax check, so a plain
// string never stands in for one by accident.

/** Uuid is a UUID in its canonical text: 32 lower-case hex digits in groups of 8-4-4-4-12. */
export type Uuid = string & { readonly __brand: "Uuid" };

/** Inet is a Postgres inet: an IPv4 or IPv6 host address, with an optional prefix length. */
export type Inet = string & { readonly __brand: "Inet" };

/** Cidr is a Postgres cidr: an IPv4 or IPv6 network with its prefix length. */
export type Cidr = string & { readonly __brand: "Cidr" };

/** MacAddr is a MAC address as six colon-separated pairs of lower-case hex digits. */
export type MacAddr = string & { readonly __brand: "MacAddr" };

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const MAC = /^[0-9a-f]{2}(?::[0-9a-f]{2}){5}$/i;

/**
 * parseUuid checks s and returns it as a Uuid in lower case. It accepts the
 * hyphenated 8-4-4-4-12 form in either case, and throws a SyntaxError on any
 * other text.
 */
export function parseUuid(s: string): Uuid {
  if (!UUID.test(s)) {
    throw syntaxError("uuid", s);
  }
  return s.toLowerCase() as Uuid;
}

/**
 * parseInet checks s and returns it as an Inet: an IPv4 or IPv6 address with
 * an optional /prefix no longer than the address. It throws a SyntaxError on
 * any other text.
 */
export function parseInet(s: string): Inet {
  if (address(s, false) === undefined) {
    throw syntaxError("inet", s);
  }
  return s as Inet;
}

/**
 * parseCidr checks s and returns it as a Cidr: an IPv4 or IPv6 address with a
 * /prefix and no bits set to the right of it. It throws a SyntaxError on any
 * other text.
 */
export function parseCidr(s: string): Cidr {
  const a = address(s, true);
  if (a === undefined || !networkOnly(a.bits, a.prefix)) {
    throw syntaxError("cidr", s);
  }
  return s as Cidr;
}

/** parseMacAddr checks s and returns it as a MacAddr in lower case, or throws a SyntaxError. */
export function parseMacAddr(s: string): MacAddr {
  if (!MAC.test(s)) {
    throw syntaxError("macaddr", s);
  }
  return s.toLowerCase() as MacAddr;
}

function syntaxError(type: string, s: string): SyntaxError {
  return new SyntaxError(`dql: invalid ${type} ${JSON.stringify(s)}`);
}

interface Address {
  bits: boolean[];
  prefix: number;
}

/** address parses an IPv4 or IPv6 address with a /prefix, required when needPrefix. */
function address(s: string, needPrefix: boolean): Address | undefined {
  const slash = s.indexOf("/");
  const host = slash < 0 ? s : s.slice(0, slash);
  const bits = host.includes(":") ? ipv6(host) : ipv4(host);
  if (bits === undefined) {
    return undefined;
  }
  if (slash < 0) {
    return needPrefix ? undefined : { bits, prefix: bits.length };
  }
  const p = s.slice(slash + 1);
  if (!/^\d{1,3}$/.test(p) || Number(p) > bits.length) {
    return undefined;
  }
  return { bits, prefix: Number(p) };
}

function ipv4(s: string): boolean[] | undefined {
  const parts = s.split(".");
  if (parts.length !== 4) {
    return undefined;
  }
  const bits: boolean[] = [];
  for (const p of parts) {
    if (!/^(?:0|[1-9]\d{0,2})$/.test(p) || Number(p) > 255) {
      return undefined;
    }
    pushBits(bits, Number(p), 8);
  }
  return bits;
}

function ipv6(s: string): boolean[] | undefined {
  const halves = s.split("::");
  if (halves.length > 2) {
    return undefined;
  }
  const groups = halves.map((h) => (h === "" ? [] : h.split(":")));
  const [head = [], tail = []] = groups;
  // An embedded IPv4 address may end the address, as in ::ffff:1.2.3.4.
  let v4: boolean[] | undefined;
  const last = groups[groups.length - 1] as string[];
  if (last.length > 0 && (last[last.length - 1] as string).includes(".")) {
    v4 = ipv4(last.pop() as string);
    if (v4 === undefined) {
      return undefined;
    }
  }
  const count = head.length + tail.length + (v4 === undefined ? 0 : 2);
  if (halves.length === 1 ? count !== 8 : count > 7) {
    return undefined;
  }
  const bits: boolean[] = [];
  const push = (gs: string[]): boolean =>
    gs.every((g) => {
      if (!/^[0-9a-f]{1,4}$/i.test(g)) {
        return false;
      }
      pushBits(bits, parseInt(g, 16), 16);
      return true;
    });
  if (!push(head)) {
    return undefined;
  }
  for (let i = count; i < 8; i++) {
    pushBits(bits, 0, 16);
  }
  if (!push(tail)) {
    return undefined;
  }
  return v4 === undefined ? bits : bits.concat(v4);
}

function pushBits(bits: boolean[], v: number, n: number): void {
  for (let i = n - 1; i >= 0; i--) {
    bits.push(((v >> i) & 1) === 1);
  }
}

function networkOnly(bits: boolean[], prefix: number): boolean {
  return bits.slice(prefix).every((b) => !b);
}
