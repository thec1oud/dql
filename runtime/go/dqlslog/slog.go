// Package dqlslog logs the executor's hook events with log/slog (design
// record §24):
//
//	exec, err := shopdb.NewExecutor(ctx, driver, cfg,
//		dql.WithHooks(dqlslog.Hooks(logger, dqlslog.Params(false))))
//
// Events name queries rather than carry values. A query's SQL text is
// logged, since it is assembled from fragments and holds none; its
// parameters are logged only with Params(true), and then a sensitive one
// is logged as dql.Redacted.
package dqlslog

import (
	"context"
	"log/slog"

	"github.com/thec1oud/dql/runtime/go/dql"
)

// Option configures Hooks.
type Option func(*config)

type config struct{ params bool }

// Params logs query parameters, with sensitive ones redacted, when on is
// true. They are left out by default (§24).
func Params(on bool) Option { return func(c *config) { c.params = on } }

// Hooks returns hooks that log to l: a query's end at debug, or at warn
// when it failed; a transaction's begin and end at debug, a retry at info
// and a leaked transaction at warn; a pool wait at debug; and a replica
// entering or leaving rotation at info.
func Hooks(l *slog.Logger, opts ...Option) dql.Hooks {
	var c config
	for _, o := range opts {
		o(&c)
	}
	return dql.Hooks{
		Params: c.params,
		QueryEnd: func(ctx context.Context, q dql.QueryEvent) {
			attrs := []slog.Attr{
				slog.String("query", q.Query), slog.String("kind", q.Kind), slog.String("dialect", q.Dialect),
				slog.String("catalog", q.Catalog), slog.String("source", q.Source), slog.Bool("replica", q.Replica),
				slog.String("consistency", q.Consistency.String()), slog.Int("attempt", q.Attempt),
				slog.Duration("duration", q.Duration), slog.Int64("rows", q.Rows), slog.String("sql", q.SQL),
			}
			if c.params {
				params := make([]any, len(q.Params))
				for i, v := range q.Params {
					params[i] = v
					if v == dql.Redacted {
						// Spelled out, since a JSON handler would print {}.
						params[i] = "[redacted]"
					}
				}
				attrs = append(attrs, slog.Any("params", params))
			}
			level := slog.LevelDebug
			if q.Err != nil {
				level = slog.LevelWarn
				attrs = append(attrs, slog.String("class", string(q.Class)), slog.String("error", q.Err.Error()))
			}
			l.LogAttrs(ctx, level, "dql query", attrs...)
		},
		TxBegin: func(ctx context.Context, t dql.TxEvent) context.Context {
			l.LogAttrs(ctx, slog.LevelDebug, "dql transaction begin", tx(t)...)
			return ctx
		},
		TxEnd: func(ctx context.Context, t dql.TxEvent) {
			l.LogAttrs(ctx, slog.LevelDebug, "dql transaction end", append(tx(t), slog.String("outcome", string(t.Outcome)), slog.Duration("duration", t.Duration))...)
		},
		TxRetry: func(ctx context.Context, t dql.TxEvent) {
			l.LogAttrs(ctx, slog.LevelInfo, "dql transaction retry", append(tx(t), slog.Duration("delay", t.Delay), slog.String("class", string(dql.ClassOf(t.Err))))...)
		},
		TxLeaked: func(ctx context.Context, t dql.TxEvent) {
			l.LogAttrs(ctx, slog.LevelWarn, "dql transaction leaked", append(tx(t), slog.Duration("duration", t.Duration))...)
		},
		PoolWait: func(ctx context.Context, p dql.PoolWaitEvent) {
			l.LogAttrs(ctx, slog.LevelDebug, "dql pool wait", slog.String("source", p.Source), slog.Bool("replica", p.Replica), slog.Duration("wait", p.Wait))
		},
		Rotation: func(r dql.RotationEvent) {
			msg := "dql replica left rotation"
			if r.In {
				msg = "dql replica entered rotation"
			}
			l.LogAttrs(context.Background(), slog.LevelInfo, msg, slog.String("source", r.Source), slog.Int("replica", r.Replica), slog.Duration("lag", r.Lag))
		},
	}
}

// tx is the attributes every transaction event has.
func tx(t dql.TxEvent) []slog.Attr {
	return []slog.Attr{
		slog.String("propagation", t.Propagation.String()), slog.String("isolation", string(t.Isolation)),
		slog.Bool("read_only", t.ReadOnly), slog.Bool("savepoint", t.Savepoint), slog.Int("attempt", t.Attempt),
	}
}
