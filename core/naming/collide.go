package naming

import (
	"cmp"
	"slices"
	"strings"
)

// Item is one name in a collision scope: its stored name and its final word
// list, after any annotation and prefix (implementation guide §5.1).
type Item struct {
	Stored string
	Words  []string
}

// Collision is a group of names in one scope whose word lists are equal.
type Collision struct {
	Words  []string // the shared word list
	Stored []string // the colliding stored names, sorted by byte order
}

// Collisions returns the groups of items whose word lists are equal (§4.3).
// The caller passes the items of one scope: the columns and relations of an
// entity, or the objects that become host types in one package. Collisions are
// decided on words, before any emitter casing, so columns user_id and userId
// collide on [user id] in every emitter.
//
// The result is sorted by word list, and each group's stored names by byte
// order, so it does not depend on the order of items (§3.2).
func Collisions(items []Item) []Collision {
	sorted := slices.Clone(items)
	slices.SortFunc(sorted, func(a, b Item) int {
		return cmp.Or(slices.Compare(a.Words, b.Words), strings.Compare(a.Stored, b.Stored))
	})
	var out []Collision
	for i := 0; i < len(sorted); {
		j := i + 1
		for j < len(sorted) && slices.Equal(sorted[j].Words, sorted[i].Words) {
			j++
		}
		if j-i > 1 {
			c := Collision{Words: slices.Clone(sorted[i].Words)}
			for _, it := range sorted[i:j] {
				c.Stored = append(c.Stored, it.Stored)
			}
			out = append(out, c)
		}
		i = j
	}
	return out
}
