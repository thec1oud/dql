package dql

import (
	"context"
	"iter"
	"slices"
	"strconv"

	"github.com/thec1oud/dql/runtime/go/opt"
)

// Pred is a predicate on the rows of the entity E stands for (§18.2):
// generated column and relation handles make them, so a predicate on one
// entity cannot filter another.
type Pred[E any] struct {
	node Node
	// all marks AllRows, which keeps every row.
	all bool
}

// NewPred wraps a plan node as a predicate on E; generated code calls it.
func NewPred[E any](n Node) Pred[E] { return Pred[E]{node: n} }

// Node returns the predicate's plan node.
func (p Pred[E]) Node() Node { return p.node }

// And holds when every predicate holds.
func And[E any](p Pred[E], more ...Pred[E]) Pred[E] {
	for _, q := range more {
		p = Pred[E]{node: Use{Fragment: "bool.and", Holes: map[string]Node{"0": p.node, "1": q.node}}}
	}
	return p
}

// Or holds when any predicate holds.
func Or[E any](p Pred[E], more ...Pred[E]) Pred[E] {
	for _, q := range more {
		p = Pred[E]{node: Use{Fragment: "bool.or", Holes: map[string]Node{"0": p.node, "1": q.node}}}
	}
	return p
}

// Not holds when p does not.
func Not[E any](p Pred[E]) Pred[E] {
	return Pred[E]{node: Use{Fragment: "bool.not", Holes: map[string]Node{"0": p.node}}}
}

// Compare is the predicate operator fragment op makes of column fragment
// col and args (§9.4); generated column handles call it.
func Compare[E any](op, col string, args ...Node) Pred[E] {
	holes := map[string]Node{"0": Use{Fragment: col}}
	for i, a := range args {
		holes[strconv.Itoa(i+1)] = a
	}
	return Pred[E]{node: Use{Fragment: op, Holes: holes}}
}

// Related holds for a row of E with a row of R through relation fragment
// rel that every predicate holds for (§18.2); generated relation handles
// call it.
func Related[E, R any](rel string, ps ...Pred[R]) Pred[E] {
	u := Use{Fragment: rel}
	if len(ps) > 0 {
		u.Holes = map[string]Node{"and": Use{Fragment: "clause.and", Holes: map[string]Node{"0": And(ps[0], ps[1:]...).node}}}
	}
	return Pred[E]{node: u}
}

// Order is one key of an ordering of E's rows.
type Order[E any] struct{ node Node }

// NewOrder wraps a plan node as an ordering key of E; generated code calls
// it.
func NewOrder[E any](n Node) Order[E] { return Order[E]{node: n} }

// Ordered is the order key fragment dir, order.asc or order.desc, makes of
// column fragment col; generated column handles call it.
func Ordered[E any](dir, col string) Order[E] {
	return Order[E]{node: Use{Fragment: dir, Holes: map[string]Node{"0": Use{Fragment: col}}}}
}

// Shape is a read shape of the entity E stands for, as a builder read
// returns it (§18.2): the fragments that select it, and its decoder.
type Shape[T, E any] struct {
	// The shape's dotted name, which names the builder's reads of it.
	Name    string
	Catalog string
	// The fragments selecting the shape, its table, and its columns in
	// order, which Distinct selects from the table.
	Select  string
	From    string
	Columns []string
	// The entity's count and exists fragments, and their queries' names.
	Count, Exists         string
	CountName, ExistsName string
	Decode                func(Row) (T, error)
}

// From is where a builder read runs: a handle that reads, and the scope
// filter every read through it gets (§21.1).
type From struct {
	H     Reader
	Scope []Node
}

// Builder composes a read of a shape from the fragment table (§16.2, §18).
// Each method returns a new builder, so a builder can be reused.
type Builder[T, E any] struct {
	t           *Fragments
	from        From
	shape       Shape[T, E]
	where       []Node
	order       []Node
	limit       Node
	offset      Node
	distinct    bool
	locks       []string
	noLog       bool
	consistency Consistency
}

// NewBuilder starts a read of shape on from; generated code calls it.
func NewBuilder[T, E any](t *Fragments, from From, shape Shape[T, E]) Builder[T, E] {
	return Builder[T, E]{t: t, from: from, shape: shape}
}

// Where keeps the rows every predicate holds for, and those of earlier
// calls.
func (b Builder[T, E]) Where(ps ...Pred[E]) Builder[T, E] {
	b.where = slices.Clone(b.where)
	for _, p := range ps {
		b.where = append(b.where, p.node)
	}
	return b
}

// OrderBy orders the rows by keys, after those of earlier calls.
func (b Builder[T, E]) OrderBy(keys ...Order[E]) Builder[T, E] {
	b.order = slices.Clone(b.order)
	for _, k := range keys {
		b.order = append(b.order, k.node)
	}
	return b
}

// Limit returns at most n rows.
func (b Builder[T, E]) Limit(n int64) Builder[T, E] {
	b.limit = Arg{Value: n}
	return b
}

// Offset skips the first n rows.
func (b Builder[T, E]) Offset(n int64) Builder[T, E] {
	b.offset = Arg{Value: n}
	return b
}

// Distinct returns each distinct row once.
func (b Builder[T, E]) Distinct() Builder[T, E] {
	b.distinct = true
	return b
}

// ForUpdate locks the rows read for update, ForShare for share; SkipLocked
// skips rows another transaction locked, and NoWait fails on them. A
// locking read runs on the primary.
func (b Builder[T, E]) ForUpdate() Builder[T, E]  { return b.lock("lock.update") }
func (b Builder[T, E]) ForShare() Builder[T, E]   { return b.lock("lock.share") }
func (b Builder[T, E]) SkipLocked() Builder[T, E] { return b.lock("lock.skip_locked") }
func (b Builder[T, E]) NoWait() Builder[T, E]     { return b.lock("lock.nowait") }

func (b Builder[T, E]) lock(f string) Builder[T, E] {
	b.locks = append(slices.Clone(b.locks), f)
	return b
}

// NoLog turns query events off for this read (§24).
func (b Builder[T, E]) NoLog() Builder[T, E] {
	b.noLog = true
	return b
}

// Consistency sets how fresh the read must be, over the executor's default
// (§22.5).
func (b Builder[T, E]) Consistency(c Consistency) Builder[T, E] {
	b.consistency = c
	return b
}

// filter is the WHERE clause: the scope's predicates, then the call's.
func (b Builder[T, E]) filter() Node {
	ps := slices.Concat(b.from.Scope, b.where)
	if len(ps) == 0 {
		return nil
	}
	p := ps[0]
	for _, q := range ps[1:] {
		p = Use{Fragment: "bool.and", Holes: map[string]Node{"0": p, "1": q}}
	}
	return Use{Fragment: "clause.where", Holes: map[string]Node{"0": p}}
}

// rows assembles the read of the shape's rows.
func (b Builder[T, E]) rows() (Statement, error) {
	var plan []Node
	if b.distinct {
		var cols []Node
		for _, c := range b.shape.Columns {
			cols = append(cols, Use{Fragment: c})
		}
		plan = append(plan, Use{Fragment: "select.distinct", Holes: map[string]Node{
			"cols": List{Items: cols, Separator: ", "}, "from": Use{Fragment: b.shape.From},
		}})
	} else {
		plan = append(plan, Use{Fragment: b.shape.Select})
	}
	if w := b.filter(); w != nil {
		plan = append(plan, w)
	}
	if len(b.order) > 0 {
		plan = append(plan, Use{Fragment: "clause.order", Holes: map[string]Node{"0": List{Items: b.order, Separator: ", "}}})
	}
	if b.limit != nil {
		plan = append(plan, Use{Fragment: "clause.limit", Holes: map[string]Node{"0": b.limit}})
	}
	if b.offset != nil {
		plan = append(plan, Use{Fragment: "clause.offset", Holes: map[string]Node{"0": b.offset}})
	}
	for _, l := range b.locks {
		plan = append(plan, Use{Fragment: l})
	}
	return b.statement(b.shape.Name, "select", plan)
}

// aggregate assembles a count or exists read of the rows the filter keeps.
func (b Builder[T, E]) aggregate(name, kind, fragment string) (Statement, error) {
	u := Use{Fragment: fragment}
	if w := b.filter(); w != nil {
		u.Holes = map[string]Node{"where": w}
	}
	return b.statement(name, kind, []Node{u})
}

func (b Builder[T, E]) statement(name, kind string, plan []Node) (Statement, error) {
	q := Query{Name: name, Catalog: b.shape.Catalog, Kind: kind, Plan: plan, Writes: len(b.locks) > 0, Consistency: b.consistency}
	s, err := q.Statement(b.t, nil)
	s.NoLog = b.noLog
	return s, err
}

// One returns the one row: NotFound for none and TooManyRows for more (§19).
func (b Builder[T, E]) One(ctx context.Context) (T, error) {
	s, err := b.rows()
	if err != nil {
		var zero T
		return zero, err
	}
	return OneOf(ctx, b.from.H, s, b.shape.Decode)
}

// OptionalOne returns the row, if any: TooManyRows for more than one (§19).
func (b Builder[T, E]) OptionalOne(ctx context.Context) (opt.Opt[T], error) {
	s, err := b.rows()
	if err != nil {
		return opt.None[T](), err
	}
	return OptionalOf(ctx, b.from.H, s, b.shape.Decode)
}

// All returns every row, an empty slice for none (§19).
func (b Builder[T, E]) All(ctx context.Context) ([]T, error) {
	s, err := b.rows()
	if err != nil {
		return nil, err
	}
	return AllOf(ctx, b.from.H, s, b.shape.Decode)
}

// Iter streams the rows, holding a connection until iteration ends (§25.1).
func (b Builder[T, E]) Iter(ctx context.Context) iter.Seq2[T, error] {
	s, err := b.rows()
	if err != nil {
		return func(yield func(T, error) bool) {
			var zero T
			yield(zero, err)
		}
	}
	return IterOf(ctx, b.from.H, s, b.shape.Decode)
}

// Count returns how many rows the filter keeps; order, limit and offset do
// not apply.
func (b Builder[T, E]) Count(ctx context.Context) (int64, error) {
	s, err := b.aggregate(b.shape.CountName, "count", b.shape.Count)
	if err != nil {
		return 0, err
	}
	return Count(ctx, b.from.H, s)
}

// Exists reports whether the filter keeps any row.
func (b Builder[T, E]) Exists(ctx context.Context) (bool, error) {
	s, err := b.aggregate(b.shape.ExistsName, "exists", b.shape.Exists)
	if err != nil {
		return false, err
	}
	return Exists(ctx, b.from.H, s)
}
