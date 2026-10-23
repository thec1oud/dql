package dql

// Map applies f to every element of s. With a method expression it converts
// a collection of shapes, such as dql.Map(rows, shopdb.UserFull.ToPartial)
// (§14.9). A nil s gives a nil result.
func Map[S ~[]E, E, R any](s S, f func(E) R) []R {
	if s == nil {
		return nil
	}
	out := make([]R, len(s))
	for i, e := range s {
		out[i] = f(e)
	}
	return out
}

// Keyed pairs a value with the key of the row it applies to, such as a
// Partial merged into rows by key (§8.9).
type Keyed[K, V any] struct {
	Key   K
	Value V
}
