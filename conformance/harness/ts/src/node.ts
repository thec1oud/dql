// Node resolves @dql/runtime, @dql/pg and @dql/mysql to the runtime workspace's builds,
// as tsconfig.json's paths do for tsc and Bun, and deno.json's imports for
// Deno, so that the generated packages and the harness share one runtime.
//
//	node --import ./src/node.ts src/main.ts

import { registerHooks } from "node:module";

const builds: Readonly<Record<string, string>> = {
  "@dql/runtime": new URL("../../../../runtime/ts/dql/dist/index.js", import.meta.url).href,
  "@dql/pg": new URL("../../../../runtime/ts/dqlpg/dist/index.js", import.meta.url).href,
  "@dql/mysql": new URL("../../../../runtime/ts/dqlmysql/dist/index.js", import.meta.url).href,
};

registerHooks({
  resolve(specifier, context, next) {
    const url = builds[specifier];
    return url === undefined ? next(specifier, context) : { url, shortCircuit: true };
  },
});
