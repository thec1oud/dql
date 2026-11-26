// The harness reports each way a step can go wrong, and nothing else, as
// conformance/harness/go's TestRunProblems checks of Go's.

import { afterAll, beforeAll, expect, test } from "bun:test";
import { parse } from "yaml";
import { type Step, type Target, run } from "../src/harness.ts";
import { type Opened, open } from "../src/scenarios.ts";

let shop: Opened;

beforeAll(async () => {
  shop = await open("shop", "prepare", false, 0);
});

afterAll(async () => {
  await shop.close();
});

const setup = `
- op: tenants.insert
  input: { name: acme }
  save: acme
- op: users.insert
  scope: { tenant_id: $acme.id }
  input: { email: a@acme.com, password_hash: x }
  save: ann
`;
const scope = "  scope: { tenant_id: $acme.id }\n";

/** steps runs setup and then step on t, and returns the problems of the step. */
async function problems(step: string, t: Target = shop.target): Promise<string[]> {
  const steps = parse(setup + step) as Step[];
  const got = await run({ scenario: "s", schema: "shop", steps }, t);
  for (const p of got) {
    expect(p.startsWith("s step 3 (")).toBe(true);
  }
  return got;
}

const cases: [string, string[]][] = [
  ["- op: users.count\n" + scope + "  expect: { value: 1 }\n", []],
  ["- op: nope.insert\n", ["no store nope"]],
  ["- op: tenants.nope\n", ["no method nope on tenants"]],
  ["- op: users.count\n  scope: { tenant_id: [1] }\n", ["scope: "]],
  ["- op: users.get\n" + scope + "  key: { id: x }\n", ["key: "]],
  ["- op: tenants.insert\n  input: { name: 1 }\n", ["input: "]],
  ["- op: users.delete_where\n" + scope + "  where: some\n", [`where "some": only all is supported`]],
  ["- op: users.get\n" + scope + "  key: { id: $nope.id }\n", ["key: no saved row nope"]],
  ["- op: users.get\n" + scope + "  key: { id: $ann.nope }\n", ["key: saved row ann has no nope"]],
  ["- op: posts.insert\n" + scope + "  input: { author_id: $ann.id, title: x, labels: [a, $nope.x] }\n", ["input: no saved row nope"]],
  ["- op: posts.insert\n" + scope + "  input: { author_id: $ann.id, title: x, labels: [a] }\n  expect: { row: { labels: [a] } }\n", []],
  ["- op: posts.insert\n" + scope + "  input: { author_id: $ann.id, title: x }\n  assign: [{ column: nope, set: now }]\n", ["no column nope of posts"]],
  ["- op: posts.insert\n" + scope + "  input: { author_id: $ann.id, title: x }\n  assign: [{ column: title }]\n", ["assign title: no expression"]],
  ["- op: posts.insert\n" + scope + "  input: { author_id: $ann.id, title: x }\n  assign: [{ column: nope }]\n", ["no column nope of posts"]],
  ["- op: posts.insert\n" + scope + "  input: { author_id: $ann.id, title: x }\n  assign: [{ column: title, set: now }]\n", ["column title cannot be set to now"]],
  ["- op: posts.patch\n" + scope + "  key: { id: 1 }\n  input: {}\n  assign: [{ column: title, add: 1 }]\n", ["column title has no add"]],
  ["- op: posts.patch\n" + scope + "  key: { id: 1 }\n  input: {}\n  assign: [{ column: author_id, sub: x }]\n", ["assign author_id: "]],
  ["- op: users.get\n" + scope + "  key: { id: 999999 }\n", ["unexpected error: "]],
  ["- op: users.count\n" + scope + "  expect: { error: { class: NotFound } }\n", ['want error {"class":"NotFound"}, got none']],
  ["- op: users.get\n" + scope + "  key: { id: 999999 }\n  expect: { error: NotFound }\n", ["error expectation NotFound is not a map"]],
  ["- op: users.get\n" + scope + "  key: { id: 999999 }\n  expect: { error: { class: TooManyRows } }\n", ["error class: want TooManyRows, got NotFound"]],
  ["- op: users.count\n" + scope + "  expect: { value: 2, none: true, nope: 1 }\n", ["want none true, got 1", "unknown expectation nope", "want value 2, got 1"]],
  ["- op: users.count\n" + scope + "  expect: { row: { id: 1 } }\n", ['want row {"id":1}, got 1']],
  [
    "- op: users.get\n" + scope + "  key: { id: $ann.id }\n  expect: { row: { nope: 1, display_name: present, email: b, id: $nope.id } }\n",
    ["displayname is null", "email: want b, got a@acme.com", "no saved row nope", "no field nope"],
  ],
  ["- op: posts.insert_many\n" + scope + "  input: [{ author_id: $ann.id, title: x }, { author_id: $ann.id, title: y }]\n  expect: { rows: [{ title: x }, { title: z }] }\n", ["row 1 title: want z, got y"]],
  ["- op: posts.insert_many\n" + scope + "  input: [{ author_id: $ann.id, title: x }]\n  expect: { rows: [] }\n", ["want 0 rows, got 1"]],
  ["- op: nope\n", ["no transaction nope"]],
  ["- op: tenants.insert_many\n  input: { generate: { count: x, row: { name: a } } }\n", ["generate: count x is not a number"]],
  ["- op: tenants.insert_many\n  input: { generate: { count: 2, row: { name: \"g-$i\" } } }\n  expect: { count: 3, statements: 2, rows: [{ name: g-1 }, { name: g-2 }] }\n", ["want count 3, got 2", "want 2 statements, got 1"]],
  ["- op: tenants.insert_many\n  input: { generate: { count: 2, value: { name: \"v-$i\" } } }\n  expect: { rows: [{ name: v-1 }, { name: v-2 }] }\n", []],
  ["- op: select\n  from: tenants\n  shape: nope\n", ["no shape nope"]],
  ["- op: select\n  from: nope\n  shape: tenant_full\n", ["no store nope"]],
  ["- op: select\n  from: tags\n  shape: tenant_full\n", ["shape tenant_full is not read through tags"]],
  ["- op: select\n  from: tenants\n  shape: tenant_full\n  filter: [{ column: nope, op: eq }]\n", ["no column nope of tenants"]],
  ["- op: select\n  from: tenants\n  shape: tenant_full\n  filter: [{ column: name, op: nope }]\n", ["column name has no nope"]],
  ["- op: select\n  from: tenants\n  shape: tenant_full\n  filter: [{ column: name, op: eq, value: [1] }]\n", ["filter name: "]],
  ["- op: select\n  from: tenants\n  shape: tenant_full\n  filter: [{ column: name, op: in, value: { generate: { count: x } } }]\n", ["filter name: generate: count x is not a number"]],
  ["- op: select\n  from: tenants\n  shape: tenant_full\n  order: [{ column: nope }]\n", ["no column nope of tenants"]],
  ["- op: select\n  from: tenants\n  shape: tenant_full\n  read: nope\n", ["no read nope"]],
  ["- op: select\n  from: tenants\n  shape: tenant_full\n  filter: [{ column: name, op: regex_match, value: \"(\" }]\n  expect: { error: { class: DatabaseError } }\n", []],
  ["- op: select\n  from: tenants\n  shape: tenant_full\n  order: [{ column: name }]\n  limit: 1\n  expect: { rows: [{ name: nope }] }\n", ["row 0 name: want nope, got acme"]],
  ["- op: select\n  from: users\n" + scope + "  shape: user_full\n  read: iter\n  stop: 1\n  expect: { count: 1 }\n", []],
  ["- op: select\n  from: users\n" + scope + "  shape: user_full\n  read: iter\n  expect: { count: 1 }\n", []],
  ["- op: select\n  from: users\n" + scope + "  shape: user_full\n  read: count\n  expect: { value: 1 }\n", []],
  ["- op: select\n  from: users\n" + scope + "  shape: user_full\n  read: one\n  expect: { row: { email: a@acme.com } }\n", []],
  ["- op: select\n  from: tenants\n  shape: tenant_full\n  deadline: 1h\n  filter: [{ column: name, op: eq, value: acme }]\n  order: [{ column: name, desc: true }]\n", []],
  ["- op: sleep\n  duration: x\n", ['time: invalid duration "x"']],
  ["- op: sleep\n  duration: 1ms\n", []],
  ["- op: select\n  from: tenants\n  shape: tenant_full\n  consistency: nope\n", ["no consistency nope"]],
  ["- op: select\n  from: tenants\n  shape: tenant_full\n  consistency: bounded\n  bound: x\n", ["bound: time: invalid duration"]],
  ["- op: select\n  from: tenants\n  shape: tenant_full\n  consistency: read_your_writes\n  wait: x\n", ["wait: time: invalid duration"]],
  ["- op: select\n  from: tenants\n  shape: tenant_full\n  consistency: read_your_writes\n  wait: 1ms\n  or_fail: true\n", []],
  ["- op: select\n  from: tenants\n  shape: tenant_full\n  consistency: primary\n", []],
  ["- op: tenants.count\n  expect: { events: [] }\n", ["want 0 events, got 1: "]],
  ["- op: tenants.count\n  expect: { events: [{ query: nope }] }\n", ["event 1 query: want nope, got tenant.count"]],
  ["- op: users.get\n" + scope + "  key: { id: 999999 }\n  expect: { error: { class: NotFound, hides: user } }\n", ["error shows user: "]],
  ["- op: write\n  expect: { attempts: 2 }\n", ["attempts: want 2, got 1"]],
  ["- op: write\n  propagation: sideways\n", ["no propagation sideways"]],
  ["- op: write\n  inject: { class: Timeout, times: 1 }\n", ["inject: no injected failure of class Timeout"]],
  ["- op: write\n  steps: [{ op: tenants.count, expect: { value: 0 } }]\n", ["step 1 (tenants.count): want value 0, got "]],
  ["- op: write\n  steps: [{ op: users.get, scope: { tenant_id: $acme.id }, key: { id: 999999 } }]\n", ["unexpected error: "]],
  ["- op: write\n" + scope + "  steps: [{ op: users.count, expect: { value: 1 } }]\n", []],
  ["- op: write\n  scope: { tenant_id: [1] }\n", ["scope: "]],
  ["- op: retry\n  steps: [{ op: tenants.count }]\n", []],
  ["- op: posts.insert_nested\n" + scope + "  input: { title: x, author: x }\n", ["input: author: a UserRef is an object of one of create, link"]],
  ["- op: posts.insert_nested\n" + scope + "  input: { title: x, author: { unlink: {} } }\n", ["input: author: a UserRef cannot unlink"]],
  ["- op: posts.insert_nested\n" + scope + "  input: { title: x, author: { link: { id: x } } }\n", ["input: author: link: "]],
  ["- op: posts.insert_nested\n" + scope + "  input: { title: x, tags: x }\n", ["input: tags: want a list"]],
  ["- op: posts.insert_nested\n" + scope + "  input: { title: x, tags: [x] }\n", ["input: tags: 0: a TagLink is an object"]],
  ["- op: posts.insert_nested\n" + scope + "  input: x\n", ["input: want an object for PostDraftNested"]],
  ["- op: tenants.count\n  inject: { class: Timeout, times: 1 }\n", ["inject: no injected failure of class Timeout"]],
  ["- op: tenants.count\n  inject: { delay: x, times: 1 }\n", ["inject: delay: time: invalid duration"]],
  ["- op: tenants.count\n  deadline: x\n", [`deadline: time: invalid duration "x"`]],
  ["- op: tenants.count\n  cancel: x\n", [`cancel: time: invalid duration "x"`]],
  ["- op: tenants.count\n  terminate: x\n", [`terminate: time: invalid duration "x"`]],
  ["- op: tenants.count\n  cancel: 1h\n", []],
  ["- op: convert\n  from: nope\n", ["no shape type nope"]],
  ["- op: convert\n  from: tenant_full\n  input: { name: 1 }\n", ["input: "]],
  ["- op: convert\n  from: tenant_full\n  call: key\n  expect: { generated: false }\n", ["key is generated"]],
  ["- op: convert\n  from: tenant_full\n  call: nope\n", ["no conversion nope"]],
  ["- op: convert\n  call: nope\n", ["no conversion nope"]],
  ["- op: convert\n  call: nope\n  expect: { generated: false }\n", []],
  ["- op: convert\n  from: tenant_full\n  input: $acme\n  call: key\n  args: [1]\n", ["key takes 0 arguments, got 1"]],
  ["- op: convert\n  from: tenant_full\n  input: $acme\n  call: key\n  save: key\n  expect: { row: { id: $acme.id } }\n", []],
  ["- op: convert\n  call: merge_tenant_by_key\n  args: [x, []]\n", ["argument 1: "]],
  ["- op: convert\n  call: merge_tenant_by_key\n  args: [[], []]\n  expect: { results: [[]] }\n", ["want 1 results, got 2"]],
  ["- op: convert\n  call: merge_tenant_by_key\n  args: [[$acme], []]\n  expect: { results: [[{ name: x }], []] }\n", ["result 1.0.name: want x, got acme"]],
  ["- op: convert\n  from: user_full\n  input: { email: \"\" }\n  call: to_partial\n  expect: { error: { class: ValidationErrors } }\n", ['want error {"class":"ValidationErrors"}, got none']],
  ["- op: select\n  from: tenants\n  shape: tenant_full\n  deleted: nope\n", ["no deleted nope"]],
  ["- op: select\n  from: users\n  scope: { tenant_id: [1] }\n  shape: user_full\n", ["scope: "]],
  ["- op: sql\n  sql: { sqlite: x }\n", ["no sql for postgres"]],
  ["- op: raw\n  source: main\n  sql: { sqlite: x }\n", ["no sql for postgres"]],
  ["- op: raw\n  source: main\n  sql: { postgres: SELECT $1 }\n  args: [$nope.x]\n", ["args: no saved row nope"]],
  ["- op: raw\n  source: main\n  sql: { postgres: NOT SQL }\n", ["unexpected error: "]],
  ["- op: sql\n  sql: { postgres: NOT SQL }\n", ["syntax error"]],
  ["- op: begin_write\n  end: sideways\n", ["no end sideways"]],
  ["- op: read\n  steps: [{ op: begin_write, expect: { error: { misuse: ReadOnly } } }, { op: begin_write }]\n", ["step 2 (begin_write): unexpected error: "]],
  ["- op: begin_read\n  end: commit\n  steps: [{ op: tenants.count, expect: { value: 0 } }]\n", ["step 1 (tenants.count): want value 0, got "]],
  ["- op: begin_write\n  end: rollback\n  steps: [{ op: tenants.insert, input: { name: gone } }]\n", []],
  ["- op: operation\n  steps: [{ op: tenants.count, expect: { value: 0 } }]\n  expect: { events: [] }\n", ["step 1 (tenants.count): want value 0, got ", "want 0 events, got 1"]],
  [
    "- op: users.insert_nested\n" + scope + "  input: { email: b@acme.com, password_hash: x }\n  expect: { row: { tenant: { name: x }, profile: { bio: x }, posts_to_author: [{ title: x }] } }\n",
    ["poststoauthor: want 1 rows, got 0", 'profile.want row {"bio":"x"}, got null', "tenant.name: want x, got acme"],
  ],
];

for (const [step, want] of cases) {
  test(step, async () => {
    const got = await problems(step);
    expect(got.length, JSON.stringify(got)).toBe(want.length);
    for (const [i, w] of want.entries()) {
      expect(got[i]).toContain(w);
    }
  });
}
