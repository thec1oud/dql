package dql

import "context"

// RawQuery is the name raw statements carry in hooks and errors (§24).
const RawQuery = "raw"

// RawReader runs SQL text a handle that reads may run on one source, by
// name: the escape hatch for what generated code does not cover (§17,
// §22.1). It runs on the source's primary, or in the transaction the handle
// or the context holds there, and returns the driver's values undecoded.
// Its values are parameters, never part of the text.
type RawReader struct {
	h      Reader
	source string
}

// RawWriter is a RawReader on a handle that writes, which also runs
// statements that write.
type RawWriter struct{ RawReader }

// Raw returns the raw reader of source on h; generated read handles call it
// from their Raw method.
func Raw(h Reader, source string) RawReader { return RawReader{h, source} }

// RawWrite returns the raw writer of source on h; generated write handles
// call it from their Raw method.
func RawWrite(h Writer, source string) RawWriter { return RawWriter{RawReader{h, source}} }

// Query runs sql, which reads, and returns its rows.
func (r RawReader) Query(ctx context.Context, sql string, args ...any) ([]Row, error) {
	return Fetch(ctx, r.h, r.statement(sql, args, false))
}

// Query runs sql, which may write, such as an INSERT ... RETURNING, and
// returns its rows.
func (w RawWriter) Query(ctx context.Context, sql string, args ...any) ([]Row, error) {
	return Fetch(ctx, w.h, w.statement(sql, args, true))
}

// Exec runs sql, which writes, and returns how many rows it affected.
func (w RawWriter) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	return Exec(ctx, w.h.(Writer), w.statement(sql, args, true))
}

func (r RawReader) statement(sql string, args []any, writes bool) Statement {
	return Statement{Query: RawQuery, Kind: RawQuery, SQL: sql, Args: args, Writes: writes, Consistency: Primary, source: r.source}
}
