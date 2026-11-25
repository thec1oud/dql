# runtime/ts

The TypeScript runtime that dql's generated TypeScript uses (design record §15, §26), as an npm workspace.

| Package | Directory | What |
| --- | --- | --- |
| `@dql/runtime` | `dql` | Values, errors, validation and execution |

The targets are Node (current LTS), Bun and Deno. The time types of §8.8 are Temporal's: native where the runtime has it, `temporal-polyfill` otherwise.

```sh
npm ci
npm run typecheck
npm run build
npm run coverage     # Node, 100% coverage required
npm run test:bun
npm run test:deno
```
