// check compares the TypeScript harness's outcomes with Go's, prints each
// scenario whose outcomes differ, and exits 1 if there is any.
//
//	bun src/check.ts <go outcomes> <ts outcomes>

import { compare } from "./compare.ts";

const [goDir, tsDir] = process.argv.slice(2);
const problems = await compare(goDir as string, tsDir as string);
for (const p of problems) {
  console.log(p);
}
console.log(problems.length === 0 ? "the outcomes are identical" : `${problems.length} scenarios differ`);
process.exit(problems.length === 0 ? 0 : 1);
