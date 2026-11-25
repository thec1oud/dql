// Package dqlotel reports the executor's hook events to OpenTelemetry,
// following its semantic conventions for database clients (design record
// §24):
//
//	exec, err := shopdb.NewExecutor(ctx, driver, cfg,
//		dql.WithHooks(dqlotel.Hooks()))
//
// Each query is a client span named by the query, with the query name as
// db.query.summary and its SQL text as db.query.text; each transaction and
// savepoint is a span the queries in it nest under. Durations and pool
// waits are histograms keyed by query name, never SQL text (§24).
// Parameters are left out unless Params(true), and then a sensitive one is
// recorded as [redacted].
package dqlotel

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/thec1oud/dql/runtime/go/dql"
)

// ScopeName names the instrumentation scope of the tracer and meter.
const ScopeName = "github.com/thec1oud/dql/runtime/go/dqlotel"

// The attributes the conventions do not define.
const (
	keyKind        = attribute.Key("dql.query.kind")
	keyCatalog     = attribute.Key("dql.catalog")
	keySource      = attribute.Key("dql.source")
	keyReplica     = attribute.Key("dql.replica")
	keyConsistency = attribute.Key("dql.consistency")
	keyAttempt     = attribute.Key("dql.attempt")
	keyRows        = attribute.Key("dql.rows")
	keyPropagation = attribute.Key("dql.transaction.propagation")
	keyIsolation   = attribute.Key("dql.transaction.isolation")
	keyReadOnly    = attribute.Key("dql.transaction.read_only")
	keySavepoint   = attribute.Key("dql.transaction.savepoint")
	keyOutcome     = attribute.Key("dql.transaction.outcome")
	keyDelay       = attribute.Key("dql.transaction.retry_delay")
	keyReplicaNo   = attribute.Key("dql.replica.index")
	keyInRotation  = attribute.Key("dql.replica.in_rotation")
)

// Option configures Hooks.
type Option func(*config)

type config struct {
	tp     trace.TracerProvider
	mp     metric.MeterProvider
	params bool
}

// TracerProvider sets the provider of spans, the global one by default.
func TracerProvider(tp trace.TracerProvider) Option { return func(c *config) { c.tp = tp } }

// MeterProvider sets the provider of metrics, the global one by default.
func MeterProvider(mp metric.MeterProvider) Option { return func(c *config) { c.mp = mp } }

// Params records query parameters on spans, with sensitive ones redacted,
// when on is true. They are left out by default (§24).
func Params(on bool) Option { return func(c *config) { c.params = on } }

// instruments are the metrics the hooks record.
type instruments struct {
	query   metric.Float64Histogram
	wait    metric.Float64Histogram
	tx      metric.Float64Histogram
	retries metric.Int64Counter
	leaked  metric.Int64Counter
	lag     metric.Float64Gauge
}

// Hooks returns hooks that report queries and transactions as spans and
// record their durations, pool waits, retries, leaks and replica lag as
// metrics.
func Hooks(opts ...Option) dql.Hooks {
	c := config{tp: otel.GetTracerProvider(), mp: otel.GetMeterProvider()}
	for _, o := range opts {
		o(&c)
	}
	tracer := c.tp.Tracer(ScopeName)
	meter := c.mp.Meter(ScopeName)
	// A meter's instruments are created without error by every provider
	// the SDK and the API ship; one that fails returns a no-op instrument,
	// which records nothing, so the errors are not reported here.
	m := instruments{}
	m.query, _ = meter.Float64Histogram("db.client.operation.duration",
		metric.WithUnit("s"), metric.WithDescription("Duration of database client operations."))
	m.wait, _ = meter.Float64Histogram("db.client.connection.wait_time",
		metric.WithUnit("s"), metric.WithDescription("The time it took to obtain an open connection from the pool."))
	m.tx, _ = meter.Float64Histogram("dql.transaction.duration",
		metric.WithUnit("s"), metric.WithDescription("Duration of transactions and savepoints."))
	m.retries, _ = meter.Int64Counter("dql.transaction.retries",
		metric.WithUnit("{retry}"), metric.WithDescription("Reruns of a transaction closure."))
	m.leaked, _ = meter.Int64Counter("dql.transaction.leaked",
		metric.WithUnit("{transaction}"), metric.WithDescription("Pair-form transactions still open when their operation ended."))
	m.lag, _ = meter.Float64Gauge("dql.replica.lag",
		metric.WithUnit("s"), metric.WithDescription("A replica's lag when it entered or left rotation."))

	return dql.Hooks{
		Params: c.params,
		QueryStart: func(ctx context.Context, q dql.QueryEvent) context.Context {
			attrs := []attribute.KeyValue{
				System(q.Dialect), semconv.DBQuerySummary(q.Query), semconv.DBQueryText(q.SQL),
				keyKind.String(q.Kind), keyCatalog.String(q.Catalog), keySource.String(q.Source),
				keyConsistency.String(q.Consistency.String()), keyAttempt.Int(q.Attempt),
			}
			if c.params {
				for i, v := range q.Params {
					attrs = append(attrs, attribute.String("db.query.parameter."+strconv.Itoa(i), param(v)))
				}
			}
			ctx, _ = tracer.Start(ctx, q.Query, trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attrs...))
			return ctx
		},
		QueryEnd: func(ctx context.Context, q dql.QueryEvent) {
			span := trace.SpanFromContext(ctx)
			span.SetAttributes(keyReplica.Bool(q.Replica), keyRows.Int64(q.Rows))
			attrs := []attribute.KeyValue{System(q.Dialect), semconv.DBQuerySummary(q.Query), keySource.String(q.Source), keyReplica.Bool(q.Replica)}
			if q.Err != nil {
				attrs = append(attrs, errorType(q.Err))
				span.SetAttributes(errorType(q.Err))
				span.RecordError(q.Err)
				span.SetStatus(codes.Error, string(q.Class))
			}
			span.End()
			m.query.Record(ctx, seconds(q.Duration), metric.WithAttributes(attrs...))
		},
		TxBegin: func(ctx context.Context, t dql.TxEvent) context.Context {
			name := "dql transaction"
			if t.Savepoint {
				name = "dql savepoint"
			}
			parent := trace.SpanFromContext(ctx)
			ctx, _ = tracer.Start(ctx, name, trace.WithAttributes(tx(t)...))
			return context.WithValue(ctx, parentKey{}, parent)
		},
		TxEnd: func(ctx context.Context, t dql.TxEvent) {
			span := trace.SpanFromContext(ctx)
			span.SetAttributes(keyOutcome.String(string(t.Outcome)))
			if t.Err != nil {
				span.SetAttributes(errorType(t.Err))
				span.RecordError(t.Err)
				span.SetStatus(codes.Error, t.Err.Error())
			}
			span.End()
			m.tx.Record(ctx, seconds(t.Duration), metric.WithAttributes(
				keyPropagation.String(t.Propagation.String()), keyIsolation.String(string(t.Isolation)),
				keySavepoint.Bool(t.Savepoint), keyOutcome.String(string(t.Outcome))))
		},
		// A retry comes after its attempt's span ended, so it is an event
		// on the span the closure was called in.
		TxRetry: func(ctx context.Context, t dql.TxEvent) {
			parent, _ := ctx.Value(parentKey{}).(trace.Span)
			if parent != nil {
				parent.AddEvent("dql transaction retry", trace.WithAttributes(append(tx(t),
					keyDelay.Float64(seconds(t.Delay)), errorType(t.Err))...))
			}
			m.retries.Add(ctx, 1, metric.WithAttributes(errorType(t.Err)))
		},
		TxLeaked: func(ctx context.Context, t dql.TxEvent) {
			trace.SpanFromContext(ctx).AddEvent("dql transaction leaked")
			m.leaked.Add(ctx, 1, metric.WithAttributes(keyPropagation.String(t.Propagation.String())))
		},
		PoolWait: func(ctx context.Context, p dql.PoolWaitEvent) {
			m.wait.Record(ctx, seconds(p.Wait), metric.WithAttributes(
				semconv.DBClientConnectionPoolName(p.Source), keyReplica.Bool(p.Replica)))
		},
		Rotation: func(r dql.RotationEvent) {
			m.lag.Record(context.Background(), seconds(r.Lag), metric.WithAttributes(
				keySource.String(r.Source), keyReplicaNo.Int(r.Replica), keyInRotation.Bool(r.In)))
		},
	}
}

type parentKey struct{}

// tx returns a transaction event's attributes.
func tx(t dql.TxEvent) []attribute.KeyValue {
	return []attribute.KeyValue{
		keyPropagation.String(t.Propagation.String()), keyIsolation.String(string(t.Isolation)),
		keyReadOnly.Bool(t.ReadOnly), keySavepoint.Bool(t.Savepoint), keyAttempt.Int(t.Attempt),
	}
}

// System returns db.system.name for a dialect: the conventions' name for
// the databases they know, and the dialect's own name otherwise.
func System(dialect string) attribute.KeyValue {
	switch dialect {
	case "postgres":
		return semconv.DBSystemNamePostgreSQL
	case "mysql":
		return semconv.DBSystemNameMySQL
	case "mariadb":
		return semconv.DBSystemNameMariaDB
	case "sqlite":
		return semconv.DBSystemNameSQLite
	case "sqlserver":
		return semconv.DBSystemNameMicrosoftSQLServer
	}
	return semconv.DBSystemNameKey.String(dialect)
}

// errorType returns error.type for err: its class, or _OTHER when it has
// none.
func errorType(err error) attribute.KeyValue {
	if c := dql.ClassOf(err); c != "" {
		return semconv.ErrorTypeKey.String(string(c))
	}
	return semconv.ErrorTypeOther
}

// param formats a parameter value for a span.
func param(v any) string {
	if v == dql.Redacted {
		return "[redacted]"
	}
	return fmt.Sprint(v)
}

func seconds(d time.Duration) float64 { return d.Seconds() }
