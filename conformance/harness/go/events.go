package harness

import (
	"context"
	"sync"

	"github.com/thec1oud/dql/runtime/go/dql"
)

// Recorder keeps the hook events an executor fires (§24), each as the
// fields scenarios match: a query's end as query, kind, rows, attempt,
// replica, redacted (how many of its parameters are redacted) and class, and
// a transaction's begin, end and retry as tx with propagation, read_only,
// savepoint, attempt, isolation, outcome and class. A nil Recorder keeps
// nothing.
type Recorder struct {
	mu     sync.Mutex
	events []any
}

// Hooks returns the hooks that record into r.
func (r *Recorder) Hooks() dql.Hooks {
	tx := func(kind string) func(context.Context, dql.TxEvent) {
		return func(_ context.Context, ev dql.TxEvent) {
			r.add(map[string]any{
				"tx": kind, "propagation": ev.Propagation.String(), "readonly": ev.ReadOnly,
				"savepoint": ev.Savepoint, "attempt": float64(ev.Attempt), "isolation": string(ev.Isolation),
				"outcome": string(ev.Outcome), "class": string(dql.ClassOf(ev.Err)),
			})
		}
	}
	begin := tx("begin")
	return dql.Hooks{
		Params: true,
		QueryEnd: func(_ context.Context, ev dql.QueryEvent) {
			redacted := 0
			for _, p := range ev.Params {
				if p == dql.Redacted {
					redacted++
				}
			}
			r.add(map[string]any{
				"query": ev.Query, "kind": ev.Kind, "rows": float64(ev.Rows), "attempt": float64(ev.Attempt),
				"replica": ev.Replica, "redacted": float64(redacted), "class": string(ev.Class),
			})
		},
		TxBegin: func(ctx context.Context, ev dql.TxEvent) context.Context {
			begin(ctx, ev)
			return ctx
		},
		TxEnd:   tx("end"),
		TxRetry: tx("retry"),
	}
}

func (r *Recorder) add(ev map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

// mark returns how many events r holds, and since the events after a mark.
func (r *Recorder) mark() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

func (r *Recorder) since(mark int) []any {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]any{}, r.events[mark:]...)
}
