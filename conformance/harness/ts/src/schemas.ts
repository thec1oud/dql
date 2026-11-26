// The schemas scenarios name, each with the ts emitter's package for it:
// shop is the running example, generated into the emitter's golden package,
// and shop_variants the running example with the design record's variants of
// it (conformance/schemas/postgres/variants.yaml), generated into variantdb.

import { readFile, readdir } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { PgDriver } from "@dql/pg";
import type { Executor, Hooks, StatementMode } from "@dql/runtime";
import * as shop from "../../../../emitters/ts/testdata/shopdb/index.dql.ts";
import * as variants from "../variantdb/index.dql.ts";
import type { Schema } from "./harness.ts";
import { inject, replay, run, terminate } from "./postgres.ts";
import { Types } from "./types.ts";

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
  newExecutor(driver: PgDriver, config: unknown, options: unknown): Promise<Executor>;
}

async function schema(ddl: string[], module: Record<string, unknown>, dir: string): Promise<OpenSchema> {
  const pkg = module as unknown as Package;
  return {
    ddl,
    dialect: "postgres",
    module,
    types: await types(fileURLToPath(new URL(dir, import.meta.url))),
    exec: run,
    inject,
    terminate,
    replay,
    async open(dsn, replicas, maxConns, mode, hooks) {
      // A scenario starts with its replica in rotation. The tracker's first
      // poll opens the replica's first connection, which can take longer
      // than the poll may, so the replica can join only on a later poll.
      const { promise: into, resolve: joined } = Promise.withResolvers<void>();
      const rotation: Hooks = { rotation: (ev) => ev.in && joined() };
      const exec = await pkg.newExecutor(
        new PgDriver(),
        {
          sources: {
            main: {
              primary: { dsn, maxConns: maxConns === 0 ? 4 : maxConns },
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

/** schemas returns the schemas by name. */
export async function schemas(): Promise<Record<string, OpenSchema>> {
  return {
    shop: await schema(["testdata/shop/schema.sql", "conformance/schemas/postgres/inject.sql"], shop, "../../../../emitters/ts/testdata/shopdb/"),
    shop_variants: await schema(
      ["testdata/shop/schema.sql", "conformance/schemas/postgres/variants.sql", "conformance/schemas/postgres/inject.sql"],
      variants,
      "../variantdb/",
    ),
  };
}
