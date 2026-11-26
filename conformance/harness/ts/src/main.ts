// main runs every scenario of conformance/scenarios on Postgres, MySQL,
// MariaDB and SQLite, in each statement mode it runs in there (§26.4), and
// prints each problem; it exits 1 if there is any. With DQL_CONFORMANCE_OUTCOMES set, it writes
// each scenario's outcomes there, for comparison with Go's.
//
//	node src/main.ts | bun src/main.ts | deno run -A src/main.ts [scenario...]

import { scenarios } from "./scenarios.ts";

const problems = await scenarios(process.argv.slice(2), (line) => console.log(line));
process.exit(problems > 0 ? 1 : 0);
