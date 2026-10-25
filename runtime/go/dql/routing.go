package dql

import (
	"context"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

// Consistency is how fresh a read must be, which decides whether it may
// leave the primary (§22.5). The zero value is unset: a statement then
// takes the executor's configured level, and the executor's default is
// Primary.
type Consistency struct {
	level  level
	bound  time.Duration
	wait   time.Duration
	orFail bool
}

type level uint8

const (
	unset level = iota
	primary
	anyReplica
	bounded
	readYourWrites
)

// The consistency levels (guide §5.6).
var (
	// Primary reads from the primary.
	Primary = Consistency{level: primary}
	// Any reads from a replica in rotation, else the primary.
	Any = Consistency{level: anyReplica}
	// ReadYourWrites reads from a replica that has replayed the writes the
	// context's Session recorded on the source, else the primary.
	ReadYourWrites = Consistency{level: readYourWrites}
)

// Bounded reads from a replica at most d behind, else the primary.
func Bounded(d time.Duration) Consistency { return Consistency{level: bounded, bound: d} }

// Wait makes a ReadYourWrites read wait up to d for a replica to replay the
// session's writes before it falls back.
func (c Consistency) Wait(d time.Duration) Consistency {
	c.wait = d
	return c
}

// OrFail makes a ReadYourWrites read fail with ReplicaUnavailable, rather
// than read from the primary, when no replica has replayed the session's
// writes (after waiting, with Wait).
func (c Consistency) OrFail() Consistency {
	c.orFail = true
	return c
}

// Replication is how a dialect reads replica positions (§22.5), from the
// generated description of the dialect. A dialect without it reads only
// from primaries.
type Replication struct {
	// Position runs on a primary connection after a commit and returns, as
	// text, a position a replica that has replayed has seen the commit.
	Position string
	// Lag runs on a replica and returns how far behind it is in seconds, as
	// a float, or NULL when the server is not a replica.
	Lag string
	// Replayed runs on a replica with one argument, a list of positions as
	// []string, and returns whether it has replayed every one.
	Replayed string
}

// replica is one replica of a source, in rotation while the tracker last
// found it reachable and at most the source's max lag behind.
type replica struct {
	pool Pool
	in   atomic.Bool
	lag  atomic.Int64 // nanoseconds
}

// The defaults of §22.5.
const (
	defaultMaxLag        = 2 * time.Second
	defaultTrackInterval = 500 * time.Millisecond
)

// WithTrackInterval sets how often the tracker polls each replica's lag.
// The default is 500ms.
func WithTrackInterval(d time.Duration) Option {
	return func(e *Executor) { e.trackInterval = d }
}

// track polls every replica until ctx ends.
func (e *Executor) track(ctx context.Context) {
	defer close(e.tracked)
	t := time.NewTicker(e.trackInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.poll(ctx)
		}
	}
}

// poll updates every replica's lag and rotation.
func (e *Executor) poll(ctx context.Context) {
	for _, s := range e.sources {
		for i, r := range s.replicas {
			lag, ok := e.lag(ctx, r)
			r.lag.Store(int64(lag))
			e.rotate(s, i, ok && lag <= s.maxLag)
		}
	}
}

// lag asks a replica how far behind it is, reporting false when it cannot
// answer or is not a replica.
func (e *Executor) lag(ctx context.Context, r *replica) (time.Duration, bool) {
	ctx, cancel := context.WithTimeout(ctx, e.trackInterval)
	defer cancel()
	c, err := r.pool.Acquire(ctx)
	if err != nil {
		return 0, false
	}
	defer c.Release()
	v, err := queryOne(ctx, c, e.dialect.Replication.Lag, nil)
	seconds, ok := v.(float64)
	return time.Duration(seconds * float64(time.Second)), err == nil && ok
}

// queryOne returns the first value of the first row of a statement.
func queryOne(ctx context.Context, c Conn, sql string, args []any) (any, error) {
	rows, err := c.Query(ctx, sql, args)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var v any
	if rows.Next() && len(rows.Row()) > 0 {
		v = rows.Row()[0]
	}
	return v, rows.Err()
}

// route returns the connection a read outside a write transaction runs on
// (guide §5.6): a replica when its consistency level allows one, else the
// primary.
func (e *Executor) route(ctx context.Context, s *sourcePool, c Consistency, independent bool, q string) (Conn, bool, error) {
	if c.level == unset {
		c = e.consistency
	}
	onPrimary := func() (Conn, bool, error) {
		conn, err := e.conn(ctx, s, independent, q)
		return conn, false, err
	}
	if c.level <= primary || len(s.replicas) == 0 {
		return onPrimary()
	}
	var positions []string
	if c.level == readYourWrites {
		var primaryOnly bool
		positions, primaryOnly = SessionFrom(ctx).pending(s.name)
		if primaryOnly {
			return onPrimary()
		}
	}
	deadline := time.Now().Add(c.wait)
	pause := time.Millisecond
	for {
		if conn := e.replicaConn(ctx, s, c, positions); conn != nil {
			return conn, true, nil
		}
		if len(positions) == 0 || !time.Now().Before(deadline) || ctx.Err() != nil {
			break
		}
		pause = min(2*pause, 50*time.Millisecond, time.Until(deadline))
		if !wait(ctx, pause) {
			break
		}
	}
	if len(positions) > 0 && c.orFail {
		return nil, false, &ReplicaUnavailable{Origin: Origin{Dialect: e.dialect.Name, Query: q}, Source: s.name}
	}
	return onPrimary()
}

// replicaConn returns a connection to a replica in rotation that meets c
// and has replayed positions, taking replicas in turn, or nil. A replica
// that does not connect or answer leaves rotation.
func (e *Executor) replicaConn(ctx context.Context, s *sourcePool, c Consistency, positions []string) Conn {
	start := int(s.next.Add(1))
	for k := range s.replicas {
		i := (start + k) % len(s.replicas)
		r := s.replicas[i]
		if !r.in.Load() || (c.level == bounded && time.Duration(r.lag.Load()) > c.bound) {
			continue
		}
		conn, err := e.acquire(ctx, s, r.pool, true)
		if err != nil {
			e.rotate(s, i, false)
			continue
		}
		if len(positions) == 0 {
			return conn
		}
		v, err := queryOne(ctx, conn, e.dialect.Replication.Replayed, []any{positions})
		if err == nil && v == true {
			return conn
		}
		conn.Release()
		if err != nil {
			e.rotate(s, i, false)
		}
	}
	return nil
}

// recordWrite records, in the context's session, the position a replica
// must replay to see what conn, a primary connection of s, has committed.
// A position it cannot read sends the session's reads of s to the primary.
func (e *Executor) recordWrite(ctx context.Context, sess *Session, s *sourcePool, conn Conn) {
	if sess == nil || len(s.replicas) == 0 {
		return
	}
	v, err := queryOne(ctx, conn, e.dialect.Replication.Position, nil)
	pos, ok := v.(string)
	if err != nil || !ok {
		pos = ""
	}
	sess.record(s.name, pos)
}

// Session is one request's record of its writes, by source, for
// ReadYourWrites reads (§22.5). It is safe for concurrent use.
type Session struct {
	mu        sync.Mutex
	positions map[string][]string
}

type sessionKey struct{}

// WithSession returns ctx carrying a new Session, or ctx itself when it
// carries one.
func WithSession(ctx context.Context) context.Context {
	if SessionFrom(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, sessionKey{}, &Session{positions: map[string][]string{}})
}

// SessionFrom returns the Session ctx carries, or nil.
func SessionFrom(ctx context.Context) *Session {
	s, _ := ctx.Value(sessionKey{}).(*Session)
	return s
}

// record adds a position of source; the empty position means none could
// be read.
func (s *Session) record(source, pos string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.positions[source] {
		if p == pos {
			return
		}
	}
	s.positions[source] = append(s.positions[source], pos)
}

// pending returns the positions a replica of source must have replayed,
// and whether the session's reads of source must use the primary. A nil
// session has none.
func (s *Session) pending(source string) (positions []string, primaryOnly bool) {
	if s == nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.positions[source] {
		if p == "" {
			return nil, true
		}
	}
	return append([]string(nil), s.positions[source]...), false
}

// Token exports the session's positions, for a later request to Import so
// that its reads see this one's writes (causal tokens, §22.5).
func (s *Session) Token() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return url.Values(s.positions).Encode()
}

// Import adds the positions of a token from Token.
func (s *Session) Import(token string) error {
	v, err := url.ParseQuery(token)
	if err != nil {
		return err
	}
	for source, ps := range v {
		for _, p := range ps {
			s.record(source, p)
		}
	}
	return nil
}
