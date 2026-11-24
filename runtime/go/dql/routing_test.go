package dql

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var replication = &Replication{Position: "POSITION", Lag: "LAG", Replayed: "REPLAYED"}

// replicated is pg with replica positions.
func replicated() Dialect {
	d := pg
	d.Replication = replication
	return d
}

// cluster is a primary pg and replicas r1, r2 and r3: lag answers each
// replica's lag in seconds (nil when it is not a replica), replayed whether
// replicas have replayed, and position the primary's position.
type cluster struct {
	lag      map[string]any
	replayed map[string]any
	position any
	failed   map[string]bool
}

func (c *cluster) serve(dsn, sql string, args []any) ([]Row, int64, error) {
	switch {
	case strings.HasPrefix(sql, "FAIL "):
		return nil, 0, &fakeNative{NativeError{Code: strings.TrimPrefix(sql, "FAIL ")}}
	case c.failed[dsn+" "+sql]:
		return nil, 0, errors.New(dsn + " failed " + sql)
	case sql == "LAG":
		return []Row{{c.lag[dsn]}}, 1, nil
	case sql == "REPLAYED":
		return []Row{{c.replayed[dsn]}}, 1, nil
	case sql == "POSITION":
		return []Row{{c.position}}, 1, nil
	}
	return []Row{{dsn}}, 1, nil
}

func newCluster(t *testing.T, c *cluster, cfg Config, opts ...Option) (*fakeDriver, *Executor) {
	t.Helper()
	d := &fakeDriver{serve: c.serve}
	if cfg.Sources == nil {
		cfg.Sources = map[string]SourceConfig{"main": {
			Primary:  ConnConfig{DSN: "pg", MaxConns: 3},
			Replicas: []ConnConfig{{DSN: "r1", MaxConns: 2}, {DSN: "r2", MaxConns: 2}, {DSN: "r3", MaxConns: 2}},
		}}
	}
	cfg.Catalogs = map[string]string{"shop": "main"}
	e, err := New(context.Background(), d, replicated(), cfg, append([]Option{WithTrackInterval(time.Hour)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	d.calls()
	return d, e
}

// served returns the server a read ran on.
func served(t *testing.T, ctx context.Context, e Reader, c Consistency) string {
	t.Helper()
	s := stmt("SELECT")
	s.Consistency = c
	r, err := One(ctx, e, s)
	if err != nil {
		t.Fatal(err)
	}
	return r[0].(string)
}

func inRotation(e *Executor) string {
	var in []string
	for i, r := range e.sources[0].replicas {
		if r.in.Load() {
			in = append(in, "r"+string(rune('1'+i)))
		}
	}
	return strings.Join(in, " ")
}

func TestReplicaConfig(t *testing.T) {
	ctx := context.Background()
	cfg := Config{
		Sources:  map[string]SourceConfig{"main": {Primary: ConnConfig{DSN: "pg", MaxConns: 2}, Replicas: []ConnConfig{{DSN: "env:NO_SUCH_REPLICA", MaxConns: 1}}}},
		Catalogs: map[string]string{"shop": "main"},
	}
	if _, err := New(ctx, &fakeDriver{}, pg, cfg); !errors.Is(err, ErrNoReplication) {
		t.Errorf("no replication: %v", err)
	}
	d := &fakeDriver{}
	if _, err := New(ctx, d, replicated(), cfg); err == nil || !strings.Contains(err.Error(), "NO_SUCH_REPLICA") {
		t.Errorf("replica env: %v", err)
	}
	if !d.pools[0].closed {
		t.Error("primary left open")
	}

	// A replica need not connect; the primary must, and a failure closes
	// the replicas too.
	cfg.Sources["main"] = SourceConfig{Primary: ConnConfig{DSN: "pg", MaxConns: 2}, Replicas: []ConnConfig{{DSN: "r1", MaxConns: 2}}}
	d = &fakeDriver{handle: rows(), fail: map[string]error{"acquire r1": errBroken}}
	e, err := New(ctx, d, replicated(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if e.consistency != Primary || e.sources[0].maxLag != 2*time.Second || inRotation(e) != "" {
		t.Error("defaults", e.consistency, e.sources[0].maxLag, inRotation(e))
	}
	e.Close()
	d.fail = map[string]error{"acquire pg": errBroken}
	if _, err := New(ctx, d, replicated(), cfg); ClassOf(err) != ClassConnectionError {
		t.Errorf("primary: %v", err)
	}
	if p := d.pools[len(d.pools)-1]; p.cfg.DSN != "r1" || !p.closed {
		t.Error("replica left open")
	}
}

func TestTracker(t *testing.T) {
	c := &cluster{lag: map[string]any{"r1": 0.1, "r2": 5.0, "r3": nil}}
	d, e := newCluster(t, c, Config{})
	// r2 is too far behind, r3 is not a replica.
	if got := inRotation(e); got != "r1" {
		t.Errorf("rotation %q", got)
	}
	if lag := time.Duration(e.sources[0].replicas[0].lag.Load()); lag != 100*time.Millisecond {
		t.Error(lag)
	}
	c.lag["r2"] = 1.5
	d.fail = map[string]error{"acquire r1": errBroken}
	c.failed = map[string]bool{"r3 LAG": true}
	e.poll(context.Background())
	if got := inRotation(e); got != "r2" {
		t.Errorf("rotation %q", got)
	}

	// The tracker polls in the background until Close.
	var lag atomic.Value
	lag.Store(5.0)
	d = &fakeDriver{serve: func(dsn, sql string, _ []any) ([]Row, int64, error) { return []Row{{lag.Load()}}, 1, nil }}
	e, err := New(context.Background(), d, replicated(), Config{
		Sources:  map[string]SourceConfig{"main": {Primary: ConnConfig{DSN: "pg", MaxConns: 2}, Replicas: []ConnConfig{{DSN: "r1", MaxConns: 2}}, MaxLag: time.Second}},
		Catalogs: map[string]string{"shop": "main"},
	}, WithTrackInterval(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if inRotation(e) != "" {
		t.Error("r1 in rotation")
	}
	lag.Store(0.5)
	for deadline := time.Now().Add(5 * time.Second); inRotation(e) != "r1"; {
		if time.Now().After(deadline) {
			t.Fatal("r1 never entered rotation")
		}
		time.Sleep(time.Millisecond)
	}
	e.Close()
}

func TestRoute(t *testing.T) {
	ctx := context.Background()
	c := &cluster{lag: map[string]any{"r1": 0.1, "r2": 1.0, "r3": nil}}
	d, e := newCluster(t, c, Config{})
	if got := served(t, ctx, e, Consistency{}); got != "pg" {
		t.Errorf("default %s", got)
	}
	if got := served(t, ctx, e, Primary); got != "pg" {
		t.Errorf("primary %s", got)
	}
	// Any turns across the replicas in rotation.
	seen := map[string]bool{}
	for range 4 {
		seen[served(t, ctx, e, Any)] = true
	}
	if len(seen) != 2 || !seen["r1"] || !seen["r2"] {
		t.Errorf("any %v", seen)
	}
	if got := served(t, ctx, e, Bounded(500*time.Millisecond)); got != "r1" {
		t.Errorf("bounded %s", got)
	}
	if got := served(t, ctx, e, Bounded(time.Millisecond)); got != "pg" {
		t.Errorf("bounded tight %s", got)
	}
	// A replica that does not connect leaves rotation.
	d.fail = map[string]error{"acquire r1": errBroken}
	if got := served(t, ctx, e, Bounded(500*time.Millisecond)); got != "pg" || inRotation(e) != "r2" {
		t.Errorf("unreachable %s, rotation %s", got, inRotation(e))
	}
	d.fail = nil

	// Writes, and reads in a write transaction, use the primary.
	s := stmt("INSERT RETURNING")
	s.Writes, s.Consistency = true, Any
	if r, err := One(ctx, e, s); err != nil || r[0] != "pg" {
		t.Errorf("write %v %v", r, err)
	}
	e.Write(ctx, func(ctx context.Context, tx *WriteTx) error {
		if got := served(t, ctx, e, Any); got != "pg" {
			t.Errorf("in write %s", got)
		}
		return nil
	})

	// A read transaction routes by its own consistency.
	d.calls()
	e.Read(ctx, func(ctx context.Context, tx *ReadTx) error {
		if got := served(t, ctx, tx, Primary); got != "r2" {
			t.Errorf("read tx %s", got)
		}
		return nil
	}, WithConsistency(Any))
	expect(t, d, "r2#4 begin read only; r2#4 query SELECT []; r2#4 commit; r2#4 put back")

	// The configured level applies to a statement that sets none.
	_, e = newCluster(t, c, Config{Consistency: Any})
	if got := served(t, ctx, e, Consistency{}); got == "pg" {
		t.Errorf("configured %s", got)
	}
}

func TestReadYourWrites(t *testing.T) {
	c := &cluster{lag: map[string]any{"r1": 0.0}, replayed: map[string]any{"r1": false}, position: "0/10"}
	d, e := newCluster(t, c, Config{Sources: map[string]SourceConfig{"main": {
		Primary: ConnConfig{DSN: "pg", MaxConns: 2}, Replicas: []ConnConfig{{DSN: "r1", MaxConns: 2}},
	}}})
	bg := context.Background()
	write := func(ctx context.Context) {
		t.Helper()
		s := stmt("UPDATE")
		if _, err := Exec(ctx, e, s); err != nil {
			t.Fatal(err)
		}
	}

	// Without a session, or before it writes, any replica will do.
	write(bg)
	if got := served(t, bg, e, ReadYourWrites); got != "r1" {
		t.Errorf("no session %s", got)
	}
	ctx := WithSession(bg)
	if WithSession(ctx) != ctx {
		t.Error("WithSession replaced the session")
	}
	if got := served(t, ctx, e, ReadYourWrites); got != "r1" {
		t.Errorf("no writes %s", got)
	}

	// A write records the primary's position; a replica that has not
	// replayed it is passed over for the primary.
	d.calls()
	write(ctx)
	expect(t, d, "pg#3 exec UPDATE []; pg#3 query POSITION []; pg#3 put back")
	if got := served(t, ctx, e, ReadYourWrites); got != "pg" {
		t.Errorf("behind %s", got)
	}
	c.replayed["r1"] = true
	d.calls()
	if got := served(t, ctx, e, ReadYourWrites); got != "r1" {
		t.Errorf("caught up %s", got)
	}
	expect(t, d, "r1#5 query REPLAYED [[0/10]]; r1#5 query SELECT []; r1#5 put back")

	// A failed write records nothing.
	if _, err := Exec(ctx, e, stmt("FAIL 23505")); err == nil {
		t.Fatal("no error")
	}
	// A write transaction records its commit's position.
	c.position = "0/20"
	e.Write(ctx, func(ctx context.Context, tx *WriteTx) error {
		_, err := Exec(ctx, tx, stmt("UPDATE"))
		return err
	})
	if got := SessionFrom(ctx).Token(); got != "main=0%2F10&main=0%2F20" {
		t.Errorf("token %s", got)
	}

	// OrFail fails rather than read from the primary; Wait waits for a
	// replica to replay.
	c.replayed["r1"] = false
	s := stmt("SELECT")
	s.Consistency = ReadYourWrites.OrFail()
	var ru *ReplicaUnavailable
	if _, err := One(ctx, e, s); !errors.As(err, &ru) || ru.Source != "main" || ru.Query != "q" {
		t.Errorf("or fail %v", err)
	}
	if got := served(t, ctx, e, ReadYourWrites.Wait(5*time.Millisecond)); got != "pg" {
		t.Errorf("wait in vain %s", got)
	}
	var calls atomic.Int32
	d.serve = func(dsn, sql string, args []any) ([]Row, int64, error) {
		if sql == "REPLAYED" {
			return []Row{{calls.Add(1) > 2}}, 1, nil
		}
		return c.serve(dsn, sql, args)
	}
	if got := served(t, ctx, e, ReadYourWrites.Wait(time.Second).OrFail()); got != "r1" || calls.Load() != 3 {
		t.Errorf("wait %s after %d", got, calls.Load())
	}
	// The wait ends with the context.
	short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	calls.Store(-1000)
	s.Consistency = ReadYourWrites.Wait(time.Second).OrFail()
	if _, err := One(short, e, s); err == nil {
		t.Error("no error")
	}
	d.serve = c.serve

	// A replica that cannot answer leaves rotation.
	c.failed = map[string]bool{"r1 REPLAYED": true}
	if got := served(t, ctx, e, ReadYourWrites); got != "pg" || inRotation(e) != "" {
		t.Errorf("unanswered %s, rotation %q", got, inRotation(e))
	}
	e.poll(bg)

	// A position the primary cannot give sends the session's reads to the
	// primary.
	c.failed, c.replayed["r1"] = nil, true
	c.position = nil
	write(ctx)
	if got := served(t, ctx, e, ReadYourWrites); got != "pg" {
		t.Errorf("unknown position %s", got)
	}
}

func TestSession(t *testing.T) {
	s := SessionFrom(WithSession(context.Background()))
	if err := s.Import("main=0%2F10&wh=1&main=0%2F10"); err != nil {
		t.Fatal(err)
	}
	if got := s.Token(); got != "main=0%2F10&wh=1" {
		t.Error(got)
	}
	if err := s.Import("%zz"); err == nil {
		t.Error("bad token imported")
	}
	if ps, primaryOnly := (*Session)(nil).pending("main"); ps != nil || primaryOnly {
		t.Error("nil session")
	}
	if SessionFrom(context.Background()) != nil {
		t.Error("session from nothing")
	}
}

func TestQueryOne(t *testing.T) {
	ctx := context.Background()
	d := &fakeDriver{handle: rows()}
	p, _ := d.Open(ctx, PoolConfig{DSN: "pg", MaxConns: 1})
	c, _ := p.Acquire(ctx)
	defer c.Release()
	if v, err := queryOne(ctx, c, "SELECT", nil); v != nil || err != nil {
		t.Error(v, err)
	}
	if _, err := queryOne(ctx, c, "FAIL 1", nil); err == nil {
		t.Error("no error")
	}
}
