package dql

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"sort"
	"strings"
	"time"
)

// Config is the runtime configuration that lives with the application
// (§22.1): its sources, which source serves each catalog, and the default
// retry policy.
type Config struct {
	Sources map[string]SourceConfig
	// The source of each catalog of the generated package.
	Catalogs map[string]string
	// The zero policy is DefaultRetry.
	Retry RetryPolicy
}

// SourceConfig is one source's primary (§22.1).
type SourceConfig struct {
	Primary ConnConfig
	// Connections kept for independent transactions only (§22.6).
	IndependentReserve int
}

// ConnConfig is a server to connect to. A DSN written env:NAME is the
// environment variable NAME.
type ConnConfig struct {
	DSN      string
	MaxConns int
}

// Dialect is the generated description of the target dialect: its name,
// the statements every new connection runs (§22.7) and its error map
// (§23).
type Dialect struct {
	Name   string
	Setup  []string
	Errors ErrorMap
}

// Option configures an Executor.
type Option func(*Executor)

// WithIndependentTimeout sets how long an independent transaction waits for
// a connection before PoolExhausted (§22.6). The default is 250ms.
func WithIndependentTimeout(d time.Duration) Option {
	return func(e *Executor) { e.independentTimeout = d }
}

// WithIndependentDepth sets how deep independent transactions may nest
// (§22.6). The default is 2.
func WithIndependentDepth(n int) Option {
	return func(e *Executor) { e.independentDepth = n }
}

// withRand replaces the source of randomness of retry jitter, for tests.
func withRand(f func() float64) Option {
	return func(e *Executor) { e.rand = f }
}

// Executor runs generated code's statements on the sources of the catalogs
// it was generated for (§22). It is a handle that reads and writes, and
// starts transactions. It is safe for concurrent use.
type Executor struct {
	driver             Driver
	dialect            *Dialect
	sources            []*sourcePool
	catalogs           map[string]*sourcePool
	retry              RetryPolicy
	independentTimeout time.Duration
	independentDepth   int
	rand               func() float64
}

// sourcePool is one source's primary pool.
type sourcePool struct {
	name string
	pool Pool
	// Limits the connections outside independent transactions when some
	// are reserved for them; nil otherwise.
	general chan struct{}
}

// Errors of misuse.
var (
	// ErrNoTransaction: a Mandatory transaction found none to join.
	ErrNoTransaction = errors.New("dql: a mandatory transaction found no transaction to join")
	// ErrInTransaction: a Never transaction began inside one.
	ErrInTransaction = errors.New("dql: a transaction that must not nest began inside one")
	// ErrReadOnly: a write transaction tried to join a read transaction.
	ErrReadOnly = errors.New("dql: a write transaction cannot join a read transaction")
	// ErrIndependentDepth: independent transactions nested too deep (§22.6).
	ErrIndependentDepth = errors.New("dql: independent transactions nest too deep")
	// ErrDone: a transaction was used after it committed or rolled back.
	ErrDone = errors.New("dql: the transaction has already committed or rolled back")
)

// New opens every source of c and connects to each primary, failing when
// one does not connect (§22.1).
func New(ctx context.Context, d Driver, dialect Dialect, c Config, opts ...Option) (*Executor, error) {
	e := &Executor{
		driver: d, dialect: &dialect, catalogs: map[string]*sourcePool{}, retry: c.Retry,
		independentTimeout: 250 * time.Millisecond, independentDepth: 2, rand: rand.Float64,
	}
	if e.retry == (RetryPolicy{}) {
		e.retry = DefaultRetry
	}
	for _, o := range opts {
		o(e)
	}
	names := make([]string, 0, len(c.Sources))
	for n := range c.Sources {
		names = append(names, n)
	}
	sort.Strings(names)
	byName := map[string]*sourcePool{}
	for _, n := range names {
		s, err := e.open(ctx, n, c.Sources[n])
		if err != nil {
			e.Close()
			return nil, err
		}
		e.sources = append(e.sources, s)
		byName[n] = s
	}
	for cat, n := range c.Catalogs {
		s := byName[n]
		if s == nil {
			e.Close()
			return nil, fmt.Errorf("dql: catalog %s names source %s, which is not configured", cat, n)
		}
		e.catalogs[cat] = s
	}
	return e, nil
}

func (e *Executor) open(ctx context.Context, name string, c SourceConfig) (*sourcePool, error) {
	dsn := c.Primary.DSN
	if v, ok := strings.CutPrefix(dsn, "env:"); ok {
		if dsn = os.Getenv(v); dsn == "" {
			return nil, fmt.Errorf("dql: source %s: environment variable %s is not set", name, v)
		}
	}
	pool, err := e.driver.Open(ctx, PoolConfig{DSN: dsn, MaxConns: c.Primary.MaxConns, Setup: e.dialect.Setup})
	if err != nil {
		return nil, &ConnectionError{Origin: Origin{Dialect: e.dialect.Name}, Source: name, Err: err}
	}
	s := &sourcePool{name: name, pool: pool}
	if r := c.IndependentReserve; r > 0 {
		s.general = make(chan struct{}, max(c.Primary.MaxConns-r, 1))
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		pool.Close()
		return nil, &ConnectionError{Origin: Origin{Dialect: e.dialect.Name}, Source: name, Err: err}
	}
	conn.Release()
	return s, nil
}

// Close closes every source's pool.
func (e *Executor) Close() {
	for _, s := range e.sources {
		s.pool.Close()
	}
}

// source returns the source of a catalog.
func (e *Executor) source(catalog string) (*sourcePool, error) {
	s := e.catalogs[catalog]
	if s == nil {
		return nil, fmt.Errorf("dql: no source serves catalog %s", catalog)
	}
	return s, nil
}

// conn acquires a connection of s outside any transaction, or for one; an
// independent transaction waits at most the independent timeout and then
// fails with PoolExhausted (§22.6).
func (e *Executor) conn(ctx context.Context, s *sourcePool, independent bool, q string) (Conn, error) {
	if independent {
		actx, cancel := context.WithTimeout(ctx, e.independentTimeout)
		defer cancel()
		c, err := s.pool.Acquire(actx)
		if err != nil && ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
			return nil, &PoolExhausted{Origin: Origin{Dialect: e.dialect.Name, Query: q}, Source: s.name}
		}
		return c, e.translate(err, "", q, s)
	}
	if s.general != nil {
		select {
		case s.general <- struct{}{}:
		case <-ctx.Done():
			return nil, e.translate(ctx.Err(), "", q, s)
		}
	}
	c, err := s.pool.Acquire(ctx)
	if err != nil {
		s.done()
		return nil, e.translate(err, "", q, s)
	}
	return &generalConn{Conn: c, s: s}, nil
}

// done frees a general connection's slot.
func (s *sourcePool) done() {
	if s.general != nil {
		<-s.general
	}
}

// generalConn frees its slot when released.
type generalConn struct {
	Conn
	s *sourcePool
}

func (c *generalConn) Release() {
	c.Conn.Release()
	c.s.done()
}

func (e *Executor) translate(err error, catalog, q string, s *sourcePool) error {
	return e.dialect.Errors.translate(e.driver, e.dialect.Name, err, catalog, q, s.name)
}
