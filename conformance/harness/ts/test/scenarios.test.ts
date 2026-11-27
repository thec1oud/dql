// Every scenario passes for TypeScript on every dialect, in each statement
// mode it runs in (guide M5, M6, §26.4), except one whose schema a dialect
// lacks or that requires a feature the dialect's schemas lack.

import { expect, test } from "bun:test";
import { join } from "node:path";
import { load } from "../src/harness.ts";
import { dialects, runScenario, skipped } from "../src/scenarios.ts";
import { root } from "../src/schemas.ts";

const scs = await load(join(root, "conformance", "scenarios"));

test("there are scenarios", () => {
  expect(scs.length).toBeGreaterThan(0);
});

for (const [dialect, d] of Object.entries(dialects)) {
  const prefix = dialect === "postgres" ? "" : `${dialect}/`;
  for (const mode of d.modes) {
    for (const sc of scs) {
      // Such a scenario runs on another dialect.
      test.skipIf((await skipped(sc, dialect)) !== undefined)(
        `${prefix}${mode}/${sc.scenario}`,
        async () => {
          expect((await runScenario(sc, mode, dialect)).problems).toEqual([]);
        },
        // Large lists take most of a minute on MySQL, in Go as here.
        120_000,
      );
    }
  }
}
