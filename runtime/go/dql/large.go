package dql

import (
	"context"
	"iter"
)

// Iter runs s and yields its rows as they arrive, holding its connection
// until iteration ends (§25.1). It routes and reports to hooks as Fetch
// does. An error ends the iteration with a nil row.
func Iter(ctx context.Context, h Reader, s Statement) iter.Seq2[Row, error] {
	return func(yield func(Row, error) bool) {
		var n int64
		e, f := handle(ctx, h)
		ctx, q := e.startQuery(ctx, f, &s)
		var replica bool
		var err error
		defer func() { q.end(replica, n, err) }()
		c, replica, done, err := connFor(ctx, e, f, &s, s.Writes)
		if err != nil {
			yield(nil, err)
			return
		}
		defer func() { done(err) }()
		src := e.catalogs[s.Catalog]
		rows, err := c.Query(withQuery(ctx, s.Query), s.SQL, s.Args)
		if err != nil {
			err = e.translate(err, s.Catalog, s.Query, src)
			yield(nil, err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			n++
			if !yield(rows.Row(), nil) {
				return
			}
		}
		if err = rows.Err(); err != nil {
			err = e.translate(err, s.Catalog, s.Query, src)
			yield(nil, err)
		}
	}
}

// Bulk is a write of many rows, which splits into statements that each
// stay within the dialect's parameter limit (§25.2).
type Bulk struct {
	Rows int
	// The parameters each row binds.
	ParamsPerRow int
	// Statement returns the statement that writes rows [from, to).
	Statement func(from, to int) (Statement, error)
}

// chunk returns how many rows each statement writes.
func (b Bulk) chunk(maxParams int) int {
	if maxParams <= 0 || b.ParamsPerRow <= 0 {
		return max(b.Rows, 1)
	}
	return max(maxParams/b.ParamsPerRow, 1)
}

// ExecBulk runs b and returns how many rows its statements affected. More
// than one statement runs in one transaction, joining the one h or ctx
// carries, so the write is all or nothing.
func ExecBulk(ctx context.Context, h Writer, b Bulk) (int64, error) {
	var total int64
	err := bulk(ctx, h, b, func() { total = 0 }, func(ctx context.Context, w Writer, s Statement) error {
		n, err := Exec(ctx, w, s)
		total += n
		return err
	})
	return total, err
}

// FetchBulk runs b, whose statements return rows, and returns them in
// order.
func FetchBulk(ctx context.Context, h Writer, b Bulk) ([]Row, error) {
	var out []Row
	err := bulk(ctx, h, b, func() { out = []Row{} }, func(ctx context.Context, w Writer, s Statement) error {
		rows, err := Fetch(ctx, w, s)
		out = append(out, rows...)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// bulk runs each chunk of b with run, after start: alone when one
// statement does, and otherwise in a write closure, which reruns from
// start. No rows run nothing.
func bulk(ctx context.Context, h Writer, b Bulk, start func(), run func(context.Context, Writer, Statement) error) error {
	e, _ := h.bind()
	size := b.chunk(e.dialect.MaxParams)
	chunks := func(ctx context.Context, w Writer) error {
		start()
		for from := 0; from < b.Rows; from += size {
			s, err := b.Statement(from, min(from+size, b.Rows))
			if err != nil {
				return err
			}
			if err := run(ctx, w, s); err != nil {
				return err
			}
		}
		return nil
	}
	if b.Rows <= size {
		return chunks(ctx, h)
	}
	return Atomic(ctx, h, chunks)
}
