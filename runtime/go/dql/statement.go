package dql

import "context"

// Reader is a handle that reads: the executor, a write transaction or a
// read transaction (§22.2). Generated reads take a Reader.
type Reader interface {
	bind() (*Executor, *frame)
}

// Writer is a handle that writes: the executor or a write transaction.
// Generated writes take a Writer, so passing a read transaction to a write
// is a compile error (§22.2).
type Writer interface {
	Reader
	writer()
}

func (e *Executor) bind() (*Executor, *frame) { return e, nil }
func (e *Executor) writer()                   {}
func (t *WriteTx) bind() (*Executor, *frame)  { return t.f.root.exec, t.f }
func (t *WriteTx) writer()                    {}
func (t *ReadTx) bind() (*Executor, *frame)   { return t.f.root.exec, t.f }

// Statement is one statement generated code runs: SQL assembled from the
// fragment table or a query file's text, its arguments in placeholder
// order, and what the executor needs to route it and name it in errors.
type Statement struct {
	// The query's name, for errors and hooks (§23.1, §24).
	Query   string
	Catalog string
	SQL     string
	Args    []any
	// The statement writes, or calls a volatile function (§22.2).
	Writes bool
	// The core proved the statement returns at most one row, so One and
	// Optional skip the extra-row check (§19).
	AtMostOne bool
	// How fresh a read outside a write transaction must be: the call's
	// option, else the query's default; unset takes the executor's (§22.5).
	Consistency Consistency
}

// connFor returns the connection a statement runs on: its transaction's,
// the one ctx carries when h is the executor, or a pooled one, which a
// write takes from the primary and a read as its consistency routes it
// (§22.5). done ends the statement with its error: it releases a pooled
// connection, after recording a write's position in the context's session.
func connFor(ctx context.Context, h Reader, s *Statement, writes bool) (e *Executor, c Conn, done func(error), err error) {
	e, f := h.bind()
	if f == nil {
		f = frameOf(ctx)
	}
	src, err := e.source(s.Catalog)
	if err != nil {
		return e, nil, nil, err
	}
	if f != nil {
		if writes && f.root.opts.ReadOnly {
			return e, nil, nil, ErrReadOnly
		}
		c, err = f.conn(ctx, src, s.Query)
		return e, c, func(error) {}, err
	}
	if !writes {
		c, err = e.route(ctx, src, s.Consistency, false, s.Query)
		if err != nil {
			return e, nil, nil, err
		}
		return e, c, func(error) { c.Release() }, nil
	}
	c, err = e.conn(ctx, src, false, s.Query)
	if err != nil {
		return e, nil, nil, err
	}
	return e, c, func(err error) {
		if err == nil {
			e.recordWrite(ctx, SessionFrom(ctx), src, c)
		}
		c.Release()
	}, nil
}

// Fetch runs s and returns its rows (§19's many).
func Fetch(ctx context.Context, h Reader, s Statement) ([]Row, error) {
	return fetch(ctx, h, &s, -1)
}

// fetch reads at most limit rows, or every row for a negative limit.
func fetch(ctx context.Context, h Reader, s *Statement, limit int) (_ []Row, err error) {
	e, c, done, err := connFor(ctx, h, s, s.Writes)
	if err != nil {
		return nil, err
	}
	defer func() { done(err) }()
	src := e.catalogs[s.Catalog]
	rows, err := c.Query(ctx, s.SQL, s.Args)
	if err != nil {
		return nil, e.translate(err, s.Catalog, s.Query, src)
	}
	defer rows.Close()
	out := []Row{}
	for (limit < 0 || len(out) < limit) && rows.Next() {
		out = append(out, rows.Row())
	}
	if err := rows.Err(); err != nil {
		return nil, e.translate(err, s.Catalog, s.Query, src)
	}
	return out, nil
}

// One runs s and returns its one row: NotFound for none and TooManyRows
// for more (§19).
func One(ctx context.Context, h Reader, s Statement) (Row, error) {
	r, ok, err := optional(ctx, h, &s)
	if err == nil && !ok {
		e, _ := h.bind()
		err = &NotFound{Origin: Origin{Dialect: e.dialect.Name, Query: s.Query}}
	}
	return r, err
}

// Optional runs s and returns its row, if any: TooManyRows for more than
// one (§19).
func Optional(ctx context.Context, h Reader, s Statement) (Row, bool, error) {
	return optional(ctx, h, &s)
}

func optional(ctx context.Context, h Reader, s *Statement) (Row, bool, error) {
	limit := 2
	if s.AtMostOne {
		limit = 1
	}
	rows, err := fetch(ctx, h, s, limit)
	switch {
	case err != nil:
		return nil, false, err
	case len(rows) > 1:
		e, _ := h.bind()
		return nil, false, &TooManyRows{Origin: Origin{Dialect: e.dialect.Name, Query: s.Query}}
	case len(rows) == 0:
		return nil, false, nil
	}
	return rows[0], true, nil
}

// Exec runs s, which writes, and returns how many rows it affected.
func Exec(ctx context.Context, h Writer, s Statement) (n int64, err error) {
	e, c, done, err := connFor(ctx, h, &s, true)
	if err != nil {
		return 0, err
	}
	defer func() { done(err) }()
	n, err = c.Exec(ctx, s.SQL, s.Args)
	return n, e.translate(err, s.Catalog, s.Query, e.catalogs[s.Catalog])
}
