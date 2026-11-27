package dql

import "strconv"

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

// Ptr returns a pointer to a copy of v. Generated conversions use it where a
// non-null field becomes a nullable one (§14.9).
func Ptr[T any](v T) *T {
	return &v
}

// FoldASCII folds ASCII letters to lower case and leaves every other byte as
// it is: the client fold CLIENT_FOLD_ASCII_LOWER a dialect plugin certifies
// for a collation (§8.9). Generated merges fold both keys before comparing.
func FoldASCII[S ~string](s S) S {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return S(b)
}

// UnmatchedKeyError reports that a change merged into rows by key matched no
// row byte-for-byte, where the key's collation may equate values the host
// cannot compare (§8.9). It names the change's position, never the key.
type UnmatchedKeyError struct {
	// Entity is the merged entity, such as shop.app.countries.
	Entity string
	// Index is the change's position in the changes merged.
	Index int
}

func (e *UnmatchedKeyError) Error() string {
	return "dql: change " + strconv.Itoa(e.Index) + " for " + e.Entity +
		" matches no row exactly, and its key's collation may match one the client cannot compare"
}
