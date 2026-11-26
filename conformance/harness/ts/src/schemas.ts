// The schemas scenarios name on each dialect, each with the ts emitter's
// package for it. On Postgres, shop is the running example, generated into
// the emitter's golden package, and shop_variants the running example with
// the design record's variants of it (conformance/schemas/postgres/
// variants.yaml), generated into variantdb. On MySQL, shop is the running
// example (conformance/schemas/mysql/shop.yaml), generated into mysqlshopdb,
// and on MariaDB the same schema through shop-mariadb.yaml, into
// mariadbshopdb.

import { readFile, readdir } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { MySqlDriver } from "@dql/mysql";
import { PgDriver } from "@dql/pg";
import { type Driver, type Executor, type Hooks, type StatementMode, parseUuid } from "@dql/runtime";
import * as shop from "../../../../emitters/ts/testdata/shopdb/index.dql.ts";
import * as mariadbShop from "../mariadbshopdb/index.dql.ts";
import * as mysqlShop from "../mysqlshopdb/index.dql.ts";
import * as variants from "../variantdb/index.dql.ts";
import type { Inject, Schema } from "./harness.ts";
import * as mysql from "./mysql.ts";
import * as postgres from "./postgres.ts";
import { Types } from "./types.ts";
import type { Fill } from "./values.ts";

/** root is the repository root, which schemas' DDL paths are relative to. */
export const root = fileURLToPath(new URL("../../../../", import.meta.url));

/** OpenSchema is a schema that opens its handle: on the database at dsn, with replicas of it at the replicas' DSNs, at most maxConns connections to it, 4 by default, sending statements in mode, with hooks. */
export interface OpenSchema extends Schema {
  open(dsn: string, replicas: readonly string[], maxConns: number, mode: StatementMode, hooks: Hooks): Promise<{ db: object; exec: Executor }>;
}

/**
 * A replica leaves rotation 1s behind, which the tracker sees within 20ms,
 * so that the routing scenario need not wait out the 2s default, as in Go's
 * harness.
 */
const maxLag = 1000;
const trackInterval = 20;

/** rotationWait is how long a scenario's executor waits for its replica to enter rotation. */
export const rotationWait = { ms: 10_000 };

/** types reads the declarations of the package in dir. */
async function types(dir: string): Promise<Types> {
  const t = new Types();
  for (const f of (await readdir(dir)).filter((f) => f.endsWith(".dql.ts")).sort()) {
    t.add(await readFile(join(dir, f), "utf8"));
  }
  return t;
}

interface Package {
  readonly Db: new (e: Executor) => object;
  newExecutor(driver: Driver, config: unknown, options: unknown): Promise<Executor>;
}

/** Server is how the scenarios use a dialect's server: its driver, the DSN the driver takes for a harness DSN, and its statements, injected failures, ended connections and replay. */
interface Server {
  readonly dialect: string;
  driver(): Driver;
  url(dsn: string): string;
  exec(dsn: string, sql: string): Promise<void>;
  inject(dsn: string, inject: Inject): Promise<void>;
  terminate(dsn: string): Promise<void>;
  replay(dsn: string, pause: boolean): Promise<void>;
}

const pgServer: Server = {
  dialect: "postgres",
  driver: () => new PgDriver(),
  url: (dsn) => dsn,
  exec: postgres.run,
  inject: postgres.inject,
  terminate: postgres.terminate,
  replay: postgres.replay,
};

const mysqlServer: Server = {
  dialect: "mysql",
  driver: () => new MySqlDriver(),
  url: mysql.url,
  exec: mysql.run,
  inject: mysql.inject,
  terminate: mysql.terminate,
  async replay() {
    throw new Error("the MySQL schemas have no replica");
  },
};

/** newUuid makes a tenant's key, which MySQL's shop fills with an expression default that a Draft must give. */
const newUuid = () => parseUuid(crypto.randomUUID());

/** mysqlFill gives the keys MySQL's shop requires a Draft to hold, as conformance/harness/go/mysql.go's Fill does. */
const mysqlFill: Fill = { TenantDraft: { id: newUuid }, TenantDraftNested: { id: newUuid } };

async function schema(server: Server, ddl: string[], module: Record<string, unknown>, dir: string, fill?: Fill): Promise<OpenSchema> {
  const pkg = module as unknown as Package;
  return {
    ddl,
    dialect: server.dialect,
    ...(fill === undefined ? {} : { fill }),
    module,
    types: await types(fileURLToPath(new URL(dir, import.meta.url))),
    exec: (dsn, sql) => server.exec(dsn, sql),
    inject: (dsn, i) => server.inject(dsn, i),
    terminate: (dsn) => server.terminate(dsn),
    replay: (dsn, pause) => server.replay(dsn, pause),
    async open(dsn, replicas, maxConns, mode, hooks) {
      // A scenario starts with its replica in rotation. The tracker's first
      // poll opens the replica's first connection, which can take longer
      // than the poll may, so the replica can join only on a later poll.
      const { promise: into, resolve: joined } = Promise.withResolvers<void>();
      const rotation: Hooks = { rotation: (ev) => ev.in && joined() };
      const exec = await pkg.newExecutor(
        server.driver(),
        {
          sources: {
            main: {
              primary: { dsn: server.url(dsn), maxConns: maxConns === 0 ? 4 : maxConns },
              replicas: replicas.map((r) => ({ dsn: r, maxConns: 4 })),
              maxLag,
              statementMode: mode,
            },
          },
          catalogs: { shop: "main" },
        },
        { trackInterval, hooks: [hooks, rotation] },
      );
      if (replicas.length > 0) {
        let timer: ReturnType<typeof setTimeout> | undefined;
        const out = new Promise<boolean>((resolve) => {
          timer = setTimeout(() => resolve(false), rotationWait.ms);
        });
        const joinedInTime = await Promise.race([into.then(() => true), out]);
        clearTimeout(timer);
        if (!joinedInTime) {
          await exec.close();
          throw new Error("the replica did not enter rotation");
        }
      }
      return { db: new pkg.Db(exec), exec };
    },
  };
}

/** schemas returns the schemas of dialect by name. */
export async function schemas(dialect = "postgres"): Promise<Record<string, OpenSchema>> {
  const mysqlDDL = ["conformance/schemas/mysql/shop.sql", "conformance/schemas/mysql/inject.sql"];
  if (dialect === "mysql") {
    return { shop: await schema(mysqlServer, mysqlDDL, mysqlShop, "../mysqlshopdb/", mysqlFill) };
  }
  if (dialect === "mariadb") {
    // MariaDB runs MySQL's schema and sql steps, generated by shop-mariadb.yaml.
    return { shop: await schema(mysqlServer, mysqlDDL, mariadbShop, "../mariadbshopdb/", mysqlFill) };
  }
  return {
    shop: await schema(pgServer, ["testdata/shop/schema.sql", "conformance/schemas/postgres/inject.sql"], shop, "../../../../emitters/ts/testdata/shopdb/"),
    shop_variants: await schema(
      pgServer,
      ["testdata/shop/schema.sql", "conformance/schemas/postgres/variants.sql", "conformance/schemas/postgres/inject.sql"],
      variants,
      "../variantdb/",
    ),
  };
}
