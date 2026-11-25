package facts

import (
	"slices"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// keySoftDelete marks the column whose value says a row is deleted (§21.3).
const keySoftDelete = "core.soft-delete"

// softDeleteUnique warns about each unique key of a soft-delete table that
// does not exclude deleted rows, since a deleted row's values could never be
// reused (§21.3). A key excludes them when its predicate requires every
// soft-delete column to be NULL, or a boolean one to be not true, as the
// source recorded on the fact; the core never reads the predicate's text
// (P5). A primary key cannot have a predicate, so it is left alone.
func (d *deriver) softDeleteUnique() {
	for _, r := range d.order {
		t := r.t
		if t.GetKind() != irv1.TableKind_RELATION_KIND_TABLE {
			continue
		}
		var cols []*irv1.Column
		for _, c := range t.GetColumns() {
			if find(c.GetAnnotations(), keySoftDelete) != nil {
				cols = append(cols, c)
			}
		}
		if len(cols) == 0 {
			continue
		}
		for _, u := range t.GetUniqueness() {
			if u.GetKind() == irv1.UniquenessKind_UNIQUENESS_PRIMARY_KEY || excludesDeleted(u, cols) {
				continue
			}
			what := "unique index"
			if u.GetKind() == irv1.UniquenessKind_UNIQUENESS_CONSTRAINT {
				what = "unique constraint"
			}
			d.warnf(CodeSoftDeleteUnique, member(r.qn, u.GetName()),
				"%s on a soft-delete table does not exclude deleted rows; a deleted row's values cannot be reused", what)
		}
	}
}

// excludesDeleted reports whether u's predicate leaves out the rows each of
// cols marks as deleted: a timestamp is deleted when set, a boolean when
// true (§21.3).
func excludesDeleted(u *irv1.Uniqueness, cols []*irv1.Column) bool {
	for _, c := range cols {
		name := c.GetName().GetStored()
		null := slices.Contains(u.GetPredicateNullColumns(), name)
		notTrue := !timestamp(c.GetType()) && slices.Contains(u.GetPredicateNotTrueColumns(), name)
		if !null && !notTrue {
			return false
		}
	}
	return true
}

// timestamp reports whether t is one of the timestamp types soft-delete
// accepts (§21.3); every other soft-delete column is a boolean.
func timestamp(t *irv1.Type) bool {
	return t.GetInstant() != nil || t.GetInstantWithOffset() != nil || t.GetLocalDatetime() != nil
}
