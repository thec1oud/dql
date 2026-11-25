package dql

import (
	"context"
	"math/rand/v2"
	"slices"
	"strconv"
	"time"
)

// RetryPolicy is how a closure that owns its root transaction reruns after
// a serialization failure or a deadlock (§22.4): up to Attempts runs, with
// exponential backoff from Initial, capped at Max, with full jitter.
//
// On lists the error classes Retry(policy).Do reruns after; nil is
// serialization failures and deadlocks. Transaction closures ignore it:
// they rerun only after those two, because a rerun is expected to succeed
// (§22.4).
type RetryPolicy struct {
	Attempts int
	Initial  time.Duration
	Max      time.Duration
	On       []Class
}

// DefaultRetry is 3 attempts from 10ms, capped at 200ms (§22.4).
var DefaultRetry = RetryPolicy{Attempts: 3, Initial: 10 * time.Millisecond, Max: 200 * time.Millisecond}

// retryable reports whether a rerun is expected to succeed after err: only
// serialization failures and deadlocks (§22.4).
func retryable(err error) bool {
	c := ClassOf(err)
	return c == ClassSerializationFailure || c == ClassDeadlock
}

// backoff returns the delay before attempt n+1, after n runs: a uniform
// draw from [0, min(Initial·2^(n-1), Max)) (guide §5.7).
func (p RetryPolicy) backoff(n int, r func() float64) time.Duration {
	d := p.Initial << (n - 1)
	if d > p.Max || d <= 0 {
		d = p.Max
	}
	return time.Duration(r() * float64(d))
}

// wait sleeps d unless ctx would end first, reporting whether it slept.
// The rerun stops when the next backoff would pass the context's deadline
// (§22.4).
func wait(ctx context.Context, d time.Duration) bool {
	if !fits(ctx, d) {
		return false
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// fits reports whether d ends before ctx's deadline, if any.
func fits(ctx context.Context, d time.Duration) bool {
	dl, ok := ctx.Deadline()
	return !ok || !time.Now().Add(d).After(dl)
}

// Retried is the error of a closure that ran more than once: the last
// run's error, and how many runs there were (§22.4). Its class is the last
// error's.
type Retried struct {
	Attempts int
	Err      error
}

func (e *Retried) Error() string {
	return e.Err.Error() + " (after " + strconv.Itoa(e.Attempts) + " attempts)"
}

// Class returns the last error's class.
func (e *Retried) Class() Class { return ClassOf(e.Err) }

// Unwrap returns the last error.
func (e *Retried) Unwrap() error { return e.Err }

// Retrier reruns a function under a policy; Retry makes one.
type Retrier struct {
	policy RetryPolicy
	rand   func() float64
}

// Retry returns a Retrier for code that wants failures retried that
// transaction closures do not rerun, such as lock timeouts or connection
// errors (§22.4). A policy without Attempts takes DefaultRetry's attempts
// and backoff.
func Retry(p RetryPolicy) Retrier {
	if p.Attempts == 0 {
		p.Attempts, p.Initial, p.Max = DefaultRetry.Attempts, DefaultRetry.Initial, DefaultRetry.Max
	}
	return Retrier{policy: p, rand: rand.Float64}
}

// Do runs fn, and reruns it while it fails with an error of a class the
// policy's On lists, up to its attempts, with its backoff. It stops when
// the next backoff would pass ctx's deadline, or ctx ends while it waits,
// and returns the last error, wrapped in Retried after more than one run.
//
// Inside a transaction Do runs fn once: a rerun inside a transaction that
// failed cannot succeed, so only the root's closure reruns (§22.4).
func (r Retrier) Do(ctx context.Context, fn func(context.Context) error) error {
	on := r.policy.On
	if on == nil {
		on = []Class{ClassSerializationFailure, ClassDeadlock}
	}
	for attempt := 1; ; attempt++ {
		err := fn(ctx)
		if err == nil || !slices.Contains(on, ClassOf(err)) || attempt >= r.policy.Attempts || frameOf(ctx) != nil {
			return retried(err, attempt)
		}
		if !wait(ctx, r.policy.backoff(attempt, r.rand)) {
			return retried(err, attempt)
		}
	}
}
