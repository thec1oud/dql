# runtime/ts

The TypeScript runtime that dql's generated TypeScript uses (design record §15, §26), as an npm workspace.

| Package | Directory | What |
| --- | --- | --- |
| `@dql/runtime` | `dql` | Values, errors, validation and execution |
| `@dql/pg` | `dqlpg` | The executor's driver for Postgres, on node-postgres |
| `@dql/mysql` | `dqlmysql` | The executor's driver for MySQL and MariaDB, on mysql2; its DSN is a URL, `mysql://user:password@host:3306/database` |
| `@dql/sqlite` | `dqlsqlite` | The executor's driver for SQLite, on bun:sqlite on Bun and node:sqlite on Node and Deno; its DSN is the database file's path |
| `@dql/otel` | `dqlotel` | Hooks that report to OpenTelemetry: spans for queries and transactions, metrics for durations, pool waits, retries, leaks and replica lag |

Hooks observe queries, transactions, pools and replicas (§24): pass them as `hooks` when opening the executor. `logHooks(logger)` logs the events to any logger with `debug`, `info`, `warn` and `error(message, attrs)`, such as `console` or a winston logger; wrap a pino logger to swap its arguments.

Large reads stream with `iterate(h, statement)`, an `AsyncIterable` that holds its connection until the loop ends (§25.1); `@dql/pg` reads them through `pg-cursor`, 256 rows at a time, `@dql/mysql` pauses the connection while it holds 256 rows the loop has not read, and `@dql/sqlite` steps the statement as the loop reads on node:sqlite, while bun:sqlite reads every row at once. Bulk writes split within the dialect's parameter limit in one transaction with `executeBulk` and `queryBulk` (§25.2).

Tests of code that runs generated queries use the recording fake executor from `@dql/runtime/fake` (§26.3): `fake.on(query, ...args).returns(rows)` answers a query by name and arguments, `fake.executor(dialect, catalogs)` opens an executor on it, and `fake.verify()` fails on any call nothing answered.

The targets are Node (current LTS), Bun and Deno. The time types of §8.8 are Temporal's: native where the runtime has it, `temporal-polyfill` otherwise.

The workspace uses Bun: `bun.lock` is committed, `tsc` type checks and builds, and `bun test` runs the unit tests with 100% coverage. Node and Deno run the TypeScript conformance suite.

```sh
bun install --frozen-lockfile
bun run build        # before typecheck: a package checks against the others' declarations
bun run typecheck
# 100% coverage required; @dql/pg's tests need Postgres and a streaming replica,
# such as conformance/replica/compose.yaml starts, and @dql/mysql's need MySQL,
# such as conformance/mysql/compose.yaml starts, named in the Go driver's syntax.
DQL_TEST_POSTGRES_URL=postgres://postgres:postgres@localhost:5432/postgres \
DQL_TEST_POSTGRES_REPLICA_URL=postgres://postgres:postgres@localhost:5433/postgres \
DQL_TEST_MYSQL_URL='root:root@tcp(localhost:3306)/' \
bun run coverage
```
