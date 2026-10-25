package dql

import (
	"context"
	"strconv"
	"time"
)

// RetryPolicy is how a closure that owns its root transaction reruns after
// a serialization failure or a deadlock (§22.4): up to Attempts runs, with
// exponential backoff from Initial, capped at Max, with full jitter.
type RetryPolicy struct {
	Attempts int
	Initial  time.Duration
	Max      time.Duration
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
