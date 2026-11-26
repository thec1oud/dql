# runtime/ts

The TypeScript runtime that dql's generated TypeScript uses (design record §15, §26), as an npm workspace.

| Package | Directory | What |
| --- | --- | --- |
| `@dql/runtime` | `dql` | Values, errors, validation and execution |
| `@dql/pg` | `dqlpg` | The executor's driver for Postgres, on node-postgres |

Hooks observe queries, transactions, pools and replicas (§24): pass them as `hooks` when opening the executor. `logHooks(logger)` logs the events to any logger with `debug`, `info`, `warn` and `error(message, attrs)`, such as `console` or a winston logger; wrap a pino logger to swap its arguments.

The targets are Node (current LTS), Bun and Deno. The time types of §8.8 are Temporal's: native where the runtime has it, `temporal-polyfill` otherwise.

The workspace uses Bun: `bun.lock` is committed, `tsc` type checks and builds, and `bun test` runs the unit tests with 100% coverage. Node and Deno run the TypeScript conformance suite.

```sh
bun install --frozen-lockfile
bun run build        # before typecheck: a package checks against the others' declarations
bun run typecheck
# 100% coverage required; @dql/pg's tests need Postgres and a streaming replica,
# such as conformance/replica/compose.yaml starts.
DQL_TEST_POSTGRES_URL=postgres://postgres:postgres@localhost:5432/postgres \
DQL_TEST_POSTGRES_REPLICA_URL=postgres://postgres:postgres@localhost:5433/postgres \
bun run coverage
```
