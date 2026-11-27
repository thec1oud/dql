package dqlotel

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/thec1oud/dql/runtime/go/dql"
)

type env struct {
	spans  *tracetest.SpanRecorder
	reader *sdkmetric.ManualReader
	tp     *sdktrace.TracerProvider
}

func setup(opts ...Option) (env, dql.Hooks) {
	e := env{spans: tracetest.NewSpanRecorder(), reader: sdkmetric.NewManualReader()}
	e.tp = sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(e.spans))
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(e.reader))
	return e, Hooks(append([]Option{TracerProvider(e.tp), MeterProvider(mp)}, opts...)...)
}

func attrs(kvs []attribute.KeyValue) map[attribute.Key]string {
	m := map[attribute.Key]string{}
	for _, kv := range kvs {
		m[kv.Key] = kv.Value.Emit()
	}
	return m
}

func check(t *testing.T, what string, got map[attribute.Key]string, want map[attribute.Key]string) {
	t.Helper()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %s = %q, want %q", what, k, got[k], v)
		}
	}
}

// metrics returns each metric's data points' attributes, by name.
func (e env) metrics(t *testing.T) map[string][]map[attribute.Key]string {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := e.reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	out := map[string][]map[attribute.Key]string{}
	for _, sm := range rm.ScopeMetrics {
		if sm.Scope.Name != ScopeName {
			t.Errorf("scope %q", sm.Scope.Name)
		}
		for _, m := range sm.Metrics {
			switch d := m.Data.(type) {
			case metricdata.Histogram[float64]:
				for _, p := range d.DataPoints {
					out[m.Name] = append(out[m.Name], attrs(p.Attributes.ToSlice()))
				}
			case metricdata.Sum[int64]:
				for _, p := range d.DataPoints {
					out[m.Name] = append(out[m.Name], attrs(p.Attributes.ToSlice()))
				}
			case metricdata.Gauge[float64]:
				for _, p := range d.DataPoints {
					out[m.Name] = append(out[m.Name], attrs(p.Attributes.ToSlice()))
				}
			}
		}
	}
	return out
}

// §24: a query is a client span named by its query, with the database
// conventions' attributes, under the transaction it ran in; its duration
// is a metric keyed by query name, never SQL text.
func TestQueryAndTransactionSpans(t *testing.T) {
	e, h := setup()
	if h.Params {
		t.Error("params on by default")
	}
	ctx := context.Background()
	txe := dql.TxEvent{Propagation: dql.Join, Isolation: dql.Serializable, Attempt: 1}
	tctx := h.TxBegin(ctx, txe)
	q := dql.QueryEvent{Query: "user.insert", Dialect: "postgres", Kind: "insert", Catalog: "shop", Source: "main",
		Consistency: dql.Primary, Attempt: 1, SQL: "INSERT ...", Params: []any{"x"}, Duration: time.Millisecond, Rows: 1}
	qctx := h.QueryStart(tctx, q)
	h.QueryEnd(qctx, q)
	q.Err, q.Class = &dql.UniqueViolation{}, dql.ClassUniqueViolation
	h.QueryEnd(h.QueryStart(tctx, q), q)
	txe.Outcome, txe.Duration = dql.Committed, time.Second
	h.TxEnd(tctx, txe)

	spans := e.spans.Ended()
	if len(spans) != 3 {
		t.Fatalf("%d spans", len(spans))
	}
	ok, failed, txs := spans[0], spans[1], spans[2]
	if ok.Name() != "user.insert" || ok.SpanKind() != trace.SpanKindClient {
		t.Errorf("query span %q %v", ok.Name(), ok.SpanKind())
	}
	if ok.Parent().SpanID() != txs.SpanContext().SpanID() {
		t.Error("query span not under its transaction")
	}
	check(t, "query", attrs(ok.Attributes()), map[attribute.Key]string{
		"db.system.name": "postgresql", "db.query.summary": "user.insert", "db.query.text": "INSERT ...",
		"dql.query.kind": "insert", "dql.catalog": "shop", "dql.source": "main", "dql.consistency": "primary",
		"dql.attempt": "1", "dql.replica": "false", "dql.rows": "1",
	})
	if _, in := attrs(ok.Attributes())["db.query.parameter.0"]; in {
		t.Error("parameters recorded by default")
	}
	if ok.Status().Code != codes.Unset {
		t.Errorf("status %v", ok.Status())
	}
	check(t, "failed", attrs(failed.Attributes()), map[attribute.Key]string{"error.type": "UniqueViolation"})
	if failed.Status().Code != codes.Error || failed.Status().Description != "UniqueViolation" || len(failed.Events()) != 1 {
		t.Errorf("failed span %v %v", failed.Status(), failed.Events())
	}
	if txs.Name() != "dql transaction" {
		t.Errorf("transaction span %q", txs.Name())
	}
	check(t, "transaction", attrs(txs.Attributes()), map[attribute.Key]string{
		"dql.transaction.propagation": "join", "dql.transaction.isolation": "serializable",
		"dql.transaction.read_only": "false", "dql.transaction.savepoint": "false",
		"dql.attempt": "1", "dql.transaction.outcome": "commit",
	})

	m := e.metrics(t)
	d := m["db.client.operation.duration"]
	if len(d) != 2 {
		t.Fatalf("duration points %v", d)
	}
	for _, p := range d {
		if _, in := p["db.query.text"]; in {
			t.Error("duration keyed by SQL text")
		}
		check(t, "duration", p, map[attribute.Key]string{"db.system.name": "postgresql", "db.query.summary": "user.insert", "dql.source": "main"})
	}
	check(t, "transaction duration", m["dql.transaction.duration"][0], map[attribute.Key]string{"dql.transaction.outcome": "commit"})
}

// A savepoint is its own span; a failed transaction records its error; a
// retry is an event on the span the closure was called in; a leak is an
// event on the leaked transaction's span.
func TestRetriesLeaksAndSavepoints(t *testing.T) {
	e, h := setup()
	ctx, caller := e.tp.Tracer("test").Start(context.Background(), "caller")
	txe := dql.TxEvent{Propagation: dql.Nested, Isolation: dql.ReadCommitted, Savepoint: true, Attempt: 1,
		Outcome: dql.RolledBack, Err: &dql.Deadlock{}, Delay: 10 * time.Millisecond}
	tctx := h.TxBegin(ctx, txe)
	h.TxEnd(tctx, txe)
	h.TxRetry(tctx, txe)

	// Without a span to hold it, a retry is only counted.
	bare := h.TxBegin(context.Background(), txe)
	h.TxRetry(bare, txe)
	h.TxRetry(context.Background(), dql.TxEvent{Err: errors.New("x")})

	lctx := h.TxBegin(ctx, dql.TxEvent{Propagation: dql.Independent})
	h.TxLeaked(lctx, dql.TxEvent{Propagation: dql.Independent})
	h.TxEnd(lctx, dql.TxEvent{Propagation: dql.Independent, Outcome: dql.RolledBack, Err: dql.ErrLeaked})
	caller.End()

	spans := e.spans.Ended()
	sp := spans[0]
	if sp.Name() != "dql savepoint" || sp.Status().Code != codes.Error {
		t.Errorf("savepoint span %q %v", sp.Name(), sp.Status())
	}
	check(t, "savepoint", attrs(sp.Attributes()), map[attribute.Key]string{"error.type": "Deadlock", "dql.transaction.outcome": "rollback"})
	leaked := spans[1]
	if len(leaked.Events()) != 2 || leaked.Events()[0].Name != "dql transaction leaked" {
		t.Errorf("leaked span events %v", leaked.Events())
	}
	check(t, "leaked", attrs(leaked.Attributes()), map[attribute.Key]string{"error.type": "_OTHER"})
	c := spans[len(spans)-1]
	if c.Name() != "caller" || len(c.Events()) != 1 || c.Events()[0].Name != "dql transaction retry" {
		t.Fatalf("caller span %q %v", c.Name(), c.Events())
	}
	check(t, "retry", attrs(c.Events()[0].Attributes), map[attribute.Key]string{
		"error.type": "Deadlock", "dql.transaction.retry_delay": "0.01", "dql.attempt": "1",
	})

	m := e.metrics(t)
	retries := m["dql.transaction.retries"]
	if len(retries) != 2 {
		t.Errorf("retry points %v", retries)
	}
	check(t, "leaks", m["dql.transaction.leaked"][0], map[attribute.Key]string{"dql.transaction.propagation": "independent"})
}

// Pool waits and replica lag are metrics.
func TestPoolWaitAndRotation(t *testing.T) {
	e, h := setup()
	ctx := context.Background()
	h.PoolWait(ctx, dql.PoolWaitEvent{Source: "main", Replica: true, Wait: time.Second})
	h.Rotation(dql.RotationEvent{Source: "main", Replica: 1, Lag: 3 * time.Second})
	m := e.metrics(t)
	check(t, "wait", m["db.client.connection.wait_time"][0], map[attribute.Key]string{"db.client.connection.pool.name": "main", "dql.replica": "true"})
	check(t, "lag", m["dql.replica.lag"][0], map[attribute.Key]string{"dql.source": "main", "dql.replica.index": "1", "dql.replica.in_rotation": "false"})
}

// With Params, the values are span attributes, a sensitive one redacted.
func TestParams(t *testing.T) {
	e, h := setup(Params(true))
	if !h.Params {
		t.Fatal("params off")
	}
	q := dql.QueryEvent{Query: "user.insert", Dialect: "postgres", Params: []any{"x", dql.Redacted, 3}}
	h.QueryEnd(h.QueryStart(context.Background(), q), q)
	check(t, "params", attrs(e.spans.Ended()[0].Attributes()), map[attribute.Key]string{
		"db.query.parameter.0": "x", "db.query.parameter.1": "[redacted]", "db.query.parameter.2": "3",
	})
}

// The conventions' names for the databases they know, and the dialect's
// own name otherwise.
func TestSystem(t *testing.T) {
	for dialect, want := range map[string]string{
		"postgres": "postgresql", "mysql": "mysql", "mariadb": "mariadb", "sqlite": "sqlite",
		"sqlserver": "microsoft.sql_server", "other": "other",
	} {
		if got := System(dialect).Value.AsString(); got != want {
			t.Errorf("%s: %s", dialect, got)
		}
	}
}

// Without options, the global providers, which are no-ops until set.
func TestGlobalProviders(t *testing.T) {
	h := Hooks()
	q := dql.QueryEvent{Query: "q"}
	h.QueryEnd(h.QueryStart(context.Background(), q), q)
}
