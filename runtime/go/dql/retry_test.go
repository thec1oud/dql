package dql

import (
	"context"
	"errors"
	"testing"
	"time"
)

// §22.4: Retry(policy).Do reruns a function after the error classes its
// policy names, with the policy's backoff and the deadline rule.
func TestRetryDo(t *testing.T) {
	ctx := context.Background()
	fast := RetryPolicy{Attempts: 3, Initial: time.Microsecond, Max: time.Microsecond}
	runs := 0
	failing := func(errs ...error) func(context.Context) error {
		runs = 0
		return func(context.Context) error {
			runs++
			return errs[min(runs, len(errs))-1]
		}
	}
	lock, deadlock, unique := &LockTimeout{}, &Deadlock{}, &UniqueViolation{}

	// By default, serialization failures and deadlocks, as closures do.
	if err := Retry(fast).Do(ctx, failing(deadlock, nil)); err != nil || runs != 2 {
		t.Errorf("deadlock: %d %v", runs, err)
	}
	if err := Retry(fast).Do(ctx, failing(lock)); err != lock || runs != 1 {
		t.Errorf("lock timeout by default: %d %v", runs, err)
	}
	// The classes the policy names, and no others.
	p := fast
	p.On = []Class{ClassLockTimeout, ClassConnectionError}
	if err := Retry(p).Do(ctx, failing(lock, &ConnectionError{}, nil)); err != nil || runs != 3 {
		t.Errorf("named classes: %d %v", runs, err)
	}
	if err := Retry(p).Do(ctx, failing(deadlock)); err != deadlock || runs != 1 {
		t.Errorf("unnamed class: %d %v", runs, err)
	}
	if err := Retry(p).Do(ctx, failing(lock, unique)); err == nil || !errors.Is(err, unique) || runs != 2 {
		t.Errorf("then another class: %d %v", runs, err)
	}
	// Out of attempts: the last error, with the count.
	var r *Retried
	if err := Retry(p).Do(ctx, failing(lock)); !errors.As(err, &r) || r.Attempts != 3 || !errors.Is(err, lock) || runs != 3 {
		t.Errorf("exhausted: %d %v", runs, err)
	}
	// A policy without attempts is the default's.
	p.Attempts, p.Initial, p.Max = 0, 0, 0
	rt := Retry(p)
	if rt.policy.Attempts != 3 || rt.policy.Initial != DefaultRetry.Initial || rt.policy.Max != DefaultRetry.Max {
		t.Errorf("default policy %+v", rt.policy)
	}
	rt.rand = func() float64 { return 0 }
	if err := rt.Do(ctx, failing(lock)); !errors.As(err, &r) || r.Attempts != 3 {
		t.Errorf("default attempts: %v", err)
	}
	// No rerun when the backoff would pass the deadline.
	slow := RetryPolicy{Attempts: 3, Initial: time.Hour, Max: time.Hour, On: []Class{ClassLockTimeout}}
	short, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	rs := Retry(slow)
	rs.rand = func() float64 { return 1 }
	if err := rs.Do(short, failing(lock)); err != lock || runs != 1 {
		t.Errorf("deadline: %d %v", runs, err)
	}
	// Inside a transaction, fn runs once.
	e := newExec(t, &fakeDriver{})
	err := e.Write(ctx, func(ctx context.Context, _ *WriteTx) error {
		if err := Retry(fast).Do(ctx, failing(deadlock, nil)); err != deadlock || runs != 1 {
			t.Errorf("in a transaction: %d %v", runs, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
