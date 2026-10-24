package shapes

import (
	"slices"
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Upserts returns the default update side of an upsert for every uniqueness
// fact of every writable entity, which are its conflict targets (§14.7), in
// entity qualified-name order and then by conflict target.
func Upserts(catalogs []*irv1.Catalog) []*irv1.Upsert {
	var out []*irv1.Upsert
	for _, e := range Entities(catalogs) {
		if !e.Writable() {
			continue
		}
		us := slices.Clone(e.Table.GetUniqueness())
		slices.SortFunc(us, func(a, b *irv1.Uniqueness) int { return strings.Compare(a.GetName(), b.GetName()) })
		for _, u := range us {
			out = append(out, Upsert(e, u))
		}
	}
	return out
}

// Upsert returns the upsert of e on conflict target u. It inserts the whole
// Draft and, on conflict, updates the fields present in it except the
// excluded columns: the conflict key's and the immutable, bound, version and
// db-updated ones (§14.7). Every emitter and dialect uses these exclusions.
func Upsert(e Entity, u *irv1.Uniqueness) *irv1.Upsert {
	up := &irv1.Upsert{
		Entity:         e.Name,
		ConflictTarget: u.GetName(),
		InsertShape:    &irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: e.Name}, Preset: irv1.Preset_PRESET_DRAFT, Form: irv1.Form_FORM_FLAT},
	}
	for _, c := range e.Table.GetColumns() {
		if r := exclude(c, slices.Contains(u.GetColumns(), c.GetName().GetStored())); r != irv1.ExcludeReason_EXCLUDE_UNSPECIFIED {
			up.DefaultUpdateExcludes = append(up.DefaultUpdateExcludes, &irv1.UpdateExclude{Column: c.GetName().GetStored(), Reason: r})
		}
	}
	return up
}

// exclude returns why the default update leaves c alone, or
// EXCLUDE_UNSPECIFIED. A column several rules exclude takes the first reason
// in the order of the forbidden rules: bound, version, immutable, db-updated,
// then the conflict key.
func exclude(c *irv1.Column, conflictKey bool) irv1.ExcludeReason {
	switch {
	case has(c, keyBound):
		return irv1.ExcludeReason_EXCLUDE_BOUND
	case has(c, keyVersion):
		return irv1.ExcludeReason_EXCLUDE_VERSION
	case has(c, keyImmutable):
		return irv1.ExcludeReason_EXCLUDE_IMMUTABLE
	case dbUpdated(c):
		return irv1.ExcludeReason_EXCLUDE_DB_UPDATED
	case conflictKey:
		return irv1.ExcludeReason_EXCLUDE_CONFLICT_KEY
	}
	return irv1.ExcludeReason_EXCLUDE_UNSPECIFIED
}
