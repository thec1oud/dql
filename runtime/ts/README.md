# runtime/ts

The TypeScript runtime that dql's generated TypeScript uses (design record §15, §26), as an npm workspace.

| Package | Directory | What |
| --- | --- | --- |
| `@dql/runtime` | `dql` | Values, errors, validation and execution |

The targets are Node (current LTS), Bun and Deno. The time types of §8.8 are Temporal's: native where the runtime has it, `temporal-polyfill` otherwise.

The workspace uses Bun: `bun.lock` is committed, `tsc` type checks and builds, and `bun test` runs the unit tests with 100% coverage. Node and Deno run the TypeScript conformance suite.

```sh
bun install --frozen-lockfile
bun run typecheck
bun run build
bun run coverage     # 100% coverage required
```
