// Every scenario passes on Postgres for TypeScript, in each statement mode
// the driver has (guide M5, §26.4).

import { expect, test } from "bun:test";
import { join } from "node:path";
import { load } from "../src/harness.ts";
import { modes, runScenario } from "../src/scenarios.ts";
import { root } from "../src/schemas.ts";

const scs = await load(join(root, "conformance", "scenarios"));

test("there are scenarios", () => {
  expect(scs.length).toBeGreaterThan(0);
});

for (const mode of modes) {
  for (const sc of scs) {
    test(`${mode}/${sc.scenario}`, async () => {
      expect((await runScenario(sc, mode)).problems).toEqual([]);
    });
  }
}
