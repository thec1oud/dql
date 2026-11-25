// @dql/otel reports the executor's hook events to OpenTelemetry, following
// its semantic conventions for database clients (design record §24), as Go's
// dqlotel does:
//
//   const exec = await Executor.open(driver, dialect, config, { hooks: [otelHooks()] });
//
// Each query is a client span named by the query, with the query name as
// db.query.summary and its SQL text as db.query.text; each transaction and
// savepoint is a span the queries in it nest under, through the context
// manager the application registered. Durations and pool waits are
// histograms keyed by query name, never SQL text (§24). Parameters are left
// out unless params is set, and then a sensitive one is recorded as
// [redacted].

import { type Attributes, type MeterProvider, type Span, SpanKind, SpanStatusCode, type TracerProvider, context, metrics, trace } from "@opentelemetry/api";
import { type Hooks, Redacted, type TxEvent, classOf } from "@dql/runtime";

/** SCOPE_NAME names the instrumentation scope of the tracer and meter. */
export const SCOPE_NAME = "@dql/otel";

/** OtelOptions configure otelHooks. */
export interface OtelOptions {
  /** The provider of spans; the global one by default. */
  readonly tracerProvider?: TracerProvider;
  /** The provider of metrics; the global one by default. */
  readonly meterProvider?: MeterProvider;
  /** Records query parameters on spans, with sensitive ones redacted. They are left out by default (§24). */
  readonly params?: boolean;
}

/** State is what a start hook hands its end hook: the span, and for a transaction the span its closure was called in. */
export interface State {
  readonly span: Span;
  readonly parent?: Span;
}

/**
 * otelHooks returns hooks that report queries and transactions as spans and
 * record their durations, pool waits, retries, leaks and replica lag as
 * metrics.
 */
export function otelHooks(options: OtelOptions = {}): Hooks<State> {
  const tracer = (options.tracerProvider ?? trace.getTracerProvider()).getTracer(SCOPE_NAME);
  const meter = (options.meterProvider ?? metrics.getMeterProvider()).getMeter(SCOPE_NAME);
  const queryDuration = meter.createHistogram("db.client.operation.duration", { unit: "s", description: "Duration of database client operations." });
  const wait = meter.createHistogram("db.client.connection.wait_time", { unit: "s", description: "The time it took to obtain an open connection from the pool." });
  const txDuration = meter.createHistogram("dql.transaction.duration", { unit: "s", description: "Duration of transactions and savepoints." });
  const retries = meter.createCounter("dql.transaction.retries", { unit: "{retry}", description: "Reruns of a transaction closure." });
  const leaked = meter.createCounter("dql.transaction.leaked", { unit: "{transaction}", description: "Pair-form transactions still open when their operation ended." });
  const lag = meter.createGauge("dql.replica.lag", { unit: "s", description: "A replica's lag when it entered or left rotation." });
  const params = options.params === true;

  return {
    params,
    queryStart(q) {
      const attrs: Attributes = {
        "db.system.name": system(q.dialect),
        "db.query.summary": q.query,
        "db.query.text": q.sql,
        "dql.query.kind": q.kind,
        "dql.catalog": q.catalog,
        "dql.source": q.source,
        "dql.consistency": q.consistency.toString(),
        "dql.attempt": q.attempt,
      };
      if (params) {
        (q.params ?? []).forEach((v, i) => {
          attrs[`db.query.parameter.${i}`] = param(v);
        });
      }
      return { span: tracer.startSpan(q.query, { kind: SpanKind.CLIENT, attributes: attrs }) };
    },
    queryEnd(q, state) {
      const span = (state as State).span;
      span.setAttributes({ "dql.replica": q.replica, "dql.rows": q.rows });
      const attrs: Attributes = { "db.system.name": system(q.dialect), "db.query.summary": q.query, "dql.source": q.source, "dql.replica": q.replica };
      if (q.error !== undefined) {
        attrs["error.type"] = errorType(q.error);
        failed(span, q.error, q.class ?? "");
      }
      span.end();
      queryDuration.record(q.duration / 1000, attrs);
    },
    txBegin(t) {
      const parent = trace.getActiveSpan();
      const span = tracer.startSpan(t.savepoint ? "dql savepoint" : "dql transaction", { attributes: tx(t) });
      return parent === undefined ? { span } : { span, parent };
    },
    txEnd(t, state) {
      const span = (state as State).span;
      span.setAttribute("dql.transaction.outcome", t.outcome ?? "");
      if (t.error !== undefined) {
        failed(span, t.error, message(t.error));
      }
      span.end();
      txDuration.record((t.duration ?? 0) / 1000, {
        "dql.transaction.propagation": t.propagation,
        "dql.transaction.isolation": t.isolation ?? "",
        "dql.transaction.savepoint": t.savepoint,
        "dql.transaction.outcome": t.outcome ?? "",
      });
    },
    // A retry comes after its attempt's span ended, so it is an event on the span the closure was called in.
    txRetry(t, state) {
      state?.parent?.addEvent("dql transaction retry", { ...tx(t), "dql.transaction.retry_delay": (t.delay ?? 0) / 1000, "error.type": errorType(t.error) });
      retries.add(1, { "error.type": errorType(t.error) });
    },
    txLeaked(t, state) {
      state?.span.addEvent("dql transaction leaked");
      leaked.add(1, { "dql.transaction.propagation": t.propagation });
    },
    poolWait(p) {
      wait.record(p.wait / 1000, { "db.client.connection.pool.name": p.source, "dql.replica": p.replica });
    },
    rotation(r) {
      lag.record(r.lag / 1000, { "dql.source": r.source, "dql.replica.index": r.replica, "dql.replica.in_rotation": r.in });
    },
    activate(state, fn) {
      return state === undefined ? fn() : context.with(trace.setSpan(context.active(), state.span), fn);
    },
  };
}

/** tx returns a transaction event's attributes. */
function tx(t: TxEvent): Attributes {
  return {
    "dql.transaction.propagation": t.propagation,
    "dql.transaction.isolation": t.isolation ?? "",
    "dql.transaction.read_only": t.readOnly,
    "dql.transaction.savepoint": t.savepoint,
    "dql.attempt": t.attempt,
  };
}

/** failed records err on span and sets its status. */
function failed(span: Span, err: unknown, status: string): void {
  span.setAttribute("error.type", errorType(err));
  span.recordException(err instanceof Error ? err : String(err));
  span.setStatus({ code: SpanStatusCode.ERROR, message: status });
}

/** message returns an error's message. */
function message(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/** system returns db.system.name for a dialect: the conventions' name for the databases they know, and the dialect's own name otherwise. */
export function system(dialect: string): string {
  switch (dialect) {
    case "postgres":
      return "postgresql";
    case "sqlserver":
      return "microsoft.sql_server";
    default:
      return dialect;
  }
}

/** errorType returns error.type for err: its class, or _OTHER when it has none. */
function errorType(err: unknown): string {
  return classOf(err) ?? "_OTHER";
}

/** param formats a parameter value for a span. */
function param(v: unknown): string {
  return v === Redacted ? "[redacted]" : String(v);
}
