import { afterAll, beforeAll, beforeEach, describe, expect, test } from "bun:test";
import { context, metrics, trace } from "@opentelemetry/api";
import { AsyncLocalStorageContextManager } from "@opentelemetry/context-async-hooks";
import { AggregationTemporality, type DataPoint, type Histogram, MeterProvider, MetricReader, type ResourceMetrics } from "@opentelemetry/sdk-metrics";
import { BasicTracerProvider, InMemorySpanExporter, type ReadableSpan, SimpleSpanProcessor } from "@opentelemetry/sdk-trace-base";
import { type Dialect, SerializationFailure, execute, queryAll, withTimeout } from "@dql/runtime";
import { Fake } from "@dql/runtime/fake";
import { SCOPE_NAME, otelHooks, system } from "../src/index.js";

const dialect: Dialect = { name: "postgres", setup: [], errors: { codes: {}, violations: [] } };

/** Reader collects the metrics on demand. */
class Reader extends MetricReader {
  constructor() {
    super({ aggregationTemporalitySelector: () => AggregationTemporality.CUMULATIVE });
  }
  protected async onForceFlush(): Promise<void> {}
  protected async onShutdown(): Promise<void> {}
}

const spans = new InMemorySpanExporter();
const tracerProvider = new BasicTracerProvider({ spanProcessors: [new SimpleSpanProcessor(spans)] });
let reader: Reader;
let meterProvider: MeterProvider;
const contextManager = new AsyncLocalStorageContextManager();

beforeAll(() => {
  context.setGlobalContextManager(contextManager.enable());
});

afterAll(() => {
  context.disable();
});

beforeEach(() => {
  spans.reset();
  reader = new Reader();
  meterProvider = new MeterProvider({ readers: [reader] });
});

/** collected returns each metric's data points by name. */
async function collected(): Promise<Record<string, DataPoint<unknown>[]>> {
  const { resourceMetrics } = (await reader.collect()) as { resourceMetrics: ResourceMetrics };
  const out: Record<string, DataPoint<unknown>[]> = {};
  for (const scope of resourceMetrics.scopeMetrics) {
    expect(scope.scope.name).toBe(SCOPE_NAME);
    for (const m of scope.metrics) {
      out[m.descriptor.name] = m.dataPoints as DataPoint<unknown>[];
    }
  }
  return out;
}

/** tree writes each finished span as name < parent name, with chosen attributes. */
function tree(keys: string[] = []): string[] {
  const all = spans.getFinishedSpans();
  const byId = new Map(all.map((s) => [s.spanContext().spanId, s]));
  return all.map((s: ReadableSpan) => {
    const parent = s.parentSpanContext === undefined ? undefined : byId.get(s.parentSpanContext.spanId);
    const attrs = keys.filter((k) => s.attributes[k] !== undefined).map((k) => `${k}=${String(s.attributes[k])}`);
    return [s.name, parent === undefined ? "" : `< ${parent.name}`, ...attrs, s.status.code === 2 ? `error ${s.status.message}` : ""].filter((x) => x !== "").join(" ");
  });
}

const stmt = (query: string, args: unknown[] = [], more: object = {}) => ({ query, kind: "select", catalog: "shop", sql: `SQL ${query}`, args, ...more });

describe("@dql/otel (§24)", () => {
  test("queries are client spans nested in their transactions", async () => {
    const fake = new Fake();
    fake.onAny("users.select").returns([1], [2]);
    fake.on("users.insert").fails(new SerializationFailure({ dialect: "postgres", query: "users.insert" }));
    fake.on("users.boom").fails(new Error("plain"));
    const e = await fake.executor(dialect, ["shop"], { hooks: [otelHooks({ tracerProvider, meterProvider, params: true })] });
    try {
      await tracerProvider.getTracer("app").startActiveSpan("request", async (request) => {
        await e.write(async (tx) => {
          await queryAll(tx, stmt("users.select", [1, "secret"], { sensitive: [false, true] }));
          await e.write((n) => queryAll(n, stmt("users.select")), { propagation: "nested" });
        });
        request.end();
      });
      let runs = 0;
      await e
        .write(async (tx) => {
          runs++;
          await execute(tx, stmt("users.insert"));
        }, { retry: { attempts: 2, initial: 1, max: 1 } })
        .catch(() => {});
      await queryAll(e, stmt("users.boom")).catch(() => {});
    } finally {
      await e.close();
    }
    expect(tree(["db.system.name", "db.query.summary", "db.query.parameter.0", "db.query.parameter.1", "dql.rows", "dql.attempt", "dql.transaction.outcome", "error.type"])).toEqual([
      "users.select < dql transaction db.system.name=postgresql db.query.summary=users.select db.query.parameter.0=1 db.query.parameter.1=[redacted] dql.rows=2 dql.attempt=1",
      "users.select < dql savepoint db.system.name=postgresql db.query.summary=users.select dql.rows=2 dql.attempt=1",
      "dql savepoint < dql transaction dql.attempt=1 dql.transaction.outcome=commit",
      "dql transaction < request dql.attempt=1 dql.transaction.outcome=commit",
      "request",
      "users.insert < dql transaction db.system.name=postgresql db.query.summary=users.insert dql.rows=0 dql.attempt=1 error.type=SerializationFailure error SerializationFailure",
      "dql transaction dql.attempt=1 dql.transaction.outcome=rollback error.type=SerializationFailure error dql: SerializationFailure in users.insert (postgres)",
      "users.insert < dql transaction db.system.name=postgresql db.query.summary=users.insert dql.rows=0 dql.attempt=2 error.type=SerializationFailure error SerializationFailure",
      "dql transaction dql.attempt=2 dql.transaction.outcome=rollback error.type=SerializationFailure error dql: SerializationFailure in users.insert (postgres)",
      "users.boom db.system.name=postgresql db.query.summary=users.boom dql.rows=0 dql.attempt=1 error.type=DatabaseError error DatabaseError",
    ]);
    const first = spans.getFinishedSpans()[0] as ReadableSpan;
    expect([first.kind, first.attributes["db.query.text"], first.attributes["dql.consistency"], first.attributes["dql.source"]]).toEqual([2, "SQL users.select", "primary", "fake"]);
    const m = await collected();
    expect(Object.keys(m).sort()).toEqual(["db.client.connection.wait_time", "db.client.operation.duration", "dql.transaction.duration", "dql.transaction.retries"]);
    expect((m["db.client.operation.duration"] ?? []).map((p) => [p.attributes["db.query.summary"], (p.value as Histogram).count])).toEqual([
      ["users.select", 2],
      ["users.insert", 2],
      ["users.boom", 1],
    ]);
  });

  test("retries are events on the closure's span, and leaks on the leaked transaction's", async () => {
    const fake = new Fake();
    fake.on("users.insert").fails(new SerializationFailure({ query: "users.insert" }));
    fake.on("users.update").affected(1);
    const hooks = otelHooks({ tracerProvider, meterProvider });
    const e = await fake.executor(dialect, ["shop"], { hooks: [hooks] });
    try {
      await tracerProvider.getTracer("app").startActiveSpan("request", async (request) => {
        await e.write((tx) => execute(tx, stmt("users.insert")), { retry: { attempts: 2, initial: 1, max: 1 } }).catch(() => {});
        request.end();
      });
      // Without an enclosing span there is no span to add the retry to.
      await e.write((tx) => execute(tx, stmt("users.insert")), { retry: { attempts: 2, initial: 1, max: 1 } }).catch(() => {});
      await e.operation(async () => {
        const tx = await e.beginWrite();
        await execute(tx, stmt("users.update"));
      });
      // The activation of an absent state runs the function as it is.
      expect(hooks.activate?.(undefined, () => 7)).toBe(7);
      hooks.txRetry?.({ propagation: "join", readOnly: false, savepoint: false, attempt: 1, error: new Error("x") }, undefined);
      hooks.txLeaked?.({ propagation: "join", readOnly: false, savepoint: false, attempt: 1 }, undefined);
      hooks.rotation?.({ source: "main", replica: 1, lag: 1500, in: true });
      // A transaction that ends without an outcome or duration, as no executor sends, still ends its span.
      const state = hooks.txBegin?.({ propagation: "join", readOnly: true, savepoint: false, attempt: 1 });
      hooks.txEnd?.({ propagation: "join", readOnly: true, savepoint: false, attempt: 1, error: "odd" }, state);
    } finally {
      await e.close();
    }
    const request = spans.getFinishedSpans().find((s) => s.name === "request") as ReadableSpan;
    expect(request.events.map((ev) => [ev.name, ev.attributes?.["dql.transaction.retry_delay"] !== undefined, ev.attributes?.["error.type"]])).toEqual([
      ["dql transaction retry", true, "SerializationFailure"],
    ]);
    const leakedSpan = spans.getFinishedSpans().find((s) => s.events.some((ev) => ev.name === "dql transaction leaked")) as ReadableSpan;
    expect([leakedSpan.name, leakedSpan.attributes["dql.transaction.outcome"]]).toEqual(["dql transaction", "rollback"]);
    const odd = spans.getFinishedSpans().at(-1) as ReadableSpan;
    expect([odd.attributes["dql.transaction.outcome"], odd.attributes["error.type"], odd.status.message]).toEqual(["", "_OTHER", "odd"]);
    const m = await collected();
    expect((m["dql.transaction.retries"] ?? []).map((p) => [p.attributes["error.type"], p.value])).toEqual([
      ["SerializationFailure", 2],
      ["_OTHER", 1],
    ]);
    expect((m["dql.transaction.leaked"] ?? []).map((p) => p.value)).toEqual([2]);
    expect((m["dql.replica.lag"] ?? []).map((p) => [p.attributes, p.value])).toEqual([[{ "dql.source": "main", "dql.replica.index": 1, "dql.replica.in_rotation": true }, 1.5]]);
  });

  test("the global providers, and the dialects' system names", async () => {
    const fake = new Fake();
    fake.on("q").returns([1]);
    const e = await fake.executor(dialect, ["shop"], { hooks: [otelHooks()] });
    try {
      await withTimeout(1000, () => queryAll(e, stmt("q")));
    } finally {
      await e.close();
    }
    expect(metrics.getMeterProvider()).toBeDefined();
    expect(["postgres", "sqlserver", "mysql", "mariadb", "sqlite", "oracle"].map(system)).toEqual(["postgresql", "microsoft.sql_server", "mysql", "mariadb", "sqlite", "oracle"]);
  });
});
