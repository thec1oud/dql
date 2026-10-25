package dql

import (
	"context"
	"iter"

	"github.com/thec1oud/dql/runtime/go/opt"
)

// OneOf runs s and decodes its one row: NotFound for none and TooManyRows
// for more (§19).
func OneOf[T any](ctx context.Context, h Reader, s Statement, decode func(Row) (T, error)) (T, error) {
	r, err := One(ctx, h, s)
	if err != nil {
		var zero T
		return zero, err
	}
	return decode(r)
}

// OptionalOf runs s and decodes its row, if any: TooManyRows for more than
// one (§19).
func OptionalOf[T any](ctx context.Context, h Reader, s Statement, decode func(Row) (T, error)) (opt.Opt[T], error) {
	r, ok, err := Optional(ctx, h, s)
	if err != nil || !ok {
		return opt.None[T](), err
	}
	v, err := decode(r)
	if err != nil {
		return opt.None[T](), err
	}
	return opt.Some(v), nil
}

// AllOf runs s and decodes every row; no rows is an empty slice, never nil
// (§19).
func AllOf[T any](ctx context.Context, h Reader, s Statement, decode func(Row) (T, error)) ([]T, error) {
	rows, err := Fetch(ctx, h, s)
	if err != nil {
		return nil, err
	}
	out := make([]T, 0, len(rows))
	for _, r := range rows {
		v, err := decode(r)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// IterOf streams s's rows, decoded (§25.1). A row that fails to decode ends
// the iteration with its error.
func IterOf[T any](ctx context.Context, h Reader, s Statement, decode func(Row) (T, error)) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		var zero T
		for r, err := range Iter(ctx, h, s) {
			if err != nil {
				yield(zero, err)
				return
			}
			v, err := decode(r)
			if err != nil {
				yield(zero, err)
				return
			}
			if !yield(v, nil) {
				return
			}
		}
	}
}

// Count runs s, which returns one row holding a count, and returns it.
func Count(ctx context.Context, h Reader, s Statement) (int64, error) {
	return OneOf(ctx, h, s, func(r Row) (int64, error) {
		d := NewDecoder(s.Query, r, 1)
		n := Field(d, 0, "count", "", Int[int64])
		return n, d.Err()
	})
}

// Exists runs s, which returns one row holding whether a row matches, and
// returns it.
func Exists(ctx context.Context, h Reader, s Statement) (bool, error) {
	return OneOf(ctx, h, s, func(r Row) (bool, error) {
		d := NewDecoder(s.Query, r, 1)
		ok := Field(d, 0, "exists", "", Bool)
		return ok, d.Err()
	})
}

// Atomic runs fn in a write transaction: h's, the one ctx carries, or a new
// one, which commits when fn returns nil and reruns after a serialization
// failure or deadlock (§22.3, §22.4).
func Atomic(ctx context.Context, h Writer, fn func(context.Context, Writer) error) error {
	e, _ := h.bind()
	if tx, ok := h.(*WriteTx); ok {
		ctx = tx.Context(ctx)
	}
	return e.Write(ctx, func(ctx context.Context, tx *WriteTx) error { return fn(ctx, tx) })
}

// Missing tells why a write of query by key matched no row: exists looks the
// key up without the expected version. A key that exists was changed since
// it was read, which is ConcurrentModification; one that does not is
// NotFound (§20.4). Run it in the write's transaction.
func Missing(ctx context.Context, h Reader, query string, exists Statement) error {
	_, found, err := Optional(ctx, h, exists)
	if err != nil {
		return err
	}
	e, _ := h.bind()
	o := Origin{Dialect: e.dialect.Name, Query: query}
	if found {
		return &ConcurrentModification{Origin: o}
	}
	return &NotFound{Origin: o}
}

// VersionedOne runs write, a versioned write by key returning its row, and
// decodes the row. When no row matches, exists, the key's lookup without
// the version, tells NotFound from ConcurrentModification in the same
// transaction (§20.4).
func VersionedOne[T any](ctx context.Context, h Writer, write, exists Statement, decode func(Row) (T, error)) (T, error) {
	var out T
	err := Atomic(ctx, h, func(ctx context.Context, w Writer) error {
		r, ok, err := Optional(ctx, w, write)
		switch {
		case err != nil:
			return err
		case !ok:
			return Missing(ctx, w, write.Query, exists)
		}
		out, err = decode(r)
		return err
	})
	return out, err
}

// VersionedExec runs write, a versioned write by key returning no rows, as
// VersionedOne does.
func VersionedExec(ctx context.Context, h Writer, write, exists Statement) error {
	return Atomic(ctx, h, func(ctx context.Context, w Writer) error {
		n, err := Exec(ctx, w, write)
		if err != nil || n > 0 {
			return err
		}
		return Missing(ctx, w, write.Query, exists)
	})
}
