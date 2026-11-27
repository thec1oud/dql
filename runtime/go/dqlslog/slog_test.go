package dqlslog

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/thec1oud/dql/runtime/go/dql"
)

func logger() (*slog.Logger, *bytes.Buffer) {
	var b bytes.Buffer
	h := slog.NewJSONHandler(&b, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
	return slog.New(h), &b
}

// §24: every event is logged by query name, without parameter values
// unless asked for, and then with sensitive ones redacted.
func TestHooks(t *testing.T) {
	ctx := context.Background()
	l, b := logger()
	h := Hooks(l)
	if h.Params {
		t.Error("params on by default")
	}
	q := dql.QueryEvent{Query: "user.insert", Dialect: "postgres", Kind: "insert", Catalog: "shop", Source: "main",
		Consistency: dql.Primary, Attempt: 1, SQL: "INSERT ...", Params: []any{"x"}, Duration: time.Millisecond, Rows: 1}
	h.QueryEnd(ctx, q)
	q.Err, q.Class = errors.New("dql: UniqueViolation"), dql.ClassUniqueViolation
	h.QueryEnd(ctx, q)
	tx := dql.TxEvent{Propagation: dql.Nested, Isolation: dql.Serializable, Savepoint: true, Attempt: 2,
		Outcome: dql.Committed, Duration: time.Second, Delay: time.Millisecond, Err: &dql.Deadlock{}}
	if got := h.TxBegin(ctx, tx); got != ctx {
		t.Error("begin changed the context")
	}
	h.TxEnd(ctx, tx)
	h.TxRetry(ctx, tx)
	h.TxLeaked(ctx, tx)
	h.PoolWait(ctx, dql.PoolWaitEvent{Source: "main", Replica: true, Wait: time.Second})
	h.Rotation(dql.RotationEvent{Source: "main", Replica: 1, Lag: 3 * time.Second})
	h.Rotation(dql.RotationEvent{Source: "main", Replica: 1, In: true})
	want := []string{
		`{"level":"DEBUG","msg":"dql query","query":"user.insert","kind":"insert","dialect":"postgres","catalog":"shop","source":"main","replica":false,"consistency":"primary","attempt":1,"duration":1000000,"rows":1,"sql":"INSERT ..."}`,
		`{"level":"WARN","msg":"dql query","query":"user.insert","kind":"insert","dialect":"postgres","catalog":"shop","source":"main","replica":false,"consistency":"primary","attempt":1,"duration":1000000,"rows":1,"sql":"INSERT ...","class":"UniqueViolation","error":"dql: UniqueViolation"}`,
		`{"level":"DEBUG","msg":"dql transaction begin","propagation":"nested","isolation":"serializable","read_only":false,"savepoint":true,"attempt":2}`,
		`{"level":"DEBUG","msg":"dql transaction end","propagation":"nested","isolation":"serializable","read_only":false,"savepoint":true,"attempt":2,"outcome":"commit","duration":1000000000}`,
		`{"level":"INFO","msg":"dql transaction retry","propagation":"nested","isolation":"serializable","read_only":false,"savepoint":true,"attempt":2,"delay":1000000,"class":"Deadlock"}`,
		`{"level":"WARN","msg":"dql transaction leaked","propagation":"nested","isolation":"serializable","read_only":false,"savepoint":true,"attempt":2,"duration":1000000000}`,
		`{"level":"DEBUG","msg":"dql pool wait","source":"main","replica":true,"wait":1000000000}`,
		`{"level":"INFO","msg":"dql replica left rotation","source":"main","replica":1,"lag":3000000000}`,
		`{"level":"INFO","msg":"dql replica entered rotation","source":"main","replica":1,"lag":0}`,
	}
	if got := strings.TrimSpace(b.String()); got != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", got, strings.Join(want, "\n"))
	}

	// With Params, the values, a sensitive one redacted.
	l, b = logger()
	h = Hooks(l, Params(true))
	if !h.Params {
		t.Error("params off")
	}
	h.QueryEnd(ctx, dql.QueryEvent{Query: "user.insert", Params: []any{"a@acme.com", dql.Redacted}})
	if got := b.String(); !strings.Contains(got, `"params":["a@acme.com","[redacted]"]`) {
		t.Error(got)
	}
}
