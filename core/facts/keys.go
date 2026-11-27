package facts

import (
	"slices"
	"strings"

	"github.com/thec1oud/dql/core/diagnostics"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Identifies reports whether a uniqueness fact of t identifies rows: it has
// columns, every one of them exists and is non-null, and it has no predicate
// and no expression (§11.1, §11.2). Nullable keys cannot identify rows
// because dialects disagree on how many NULLs a unique column allows.
func Identifies(t *irv1.Table, u *irv1.Uniqueness) bool {
	if !plain(u) {
		return false
	}
	for _, name := range u.GetColumns() {
		if c := column(t, name); c == nil || c.GetNullable() {
			return false
		}
	}
	return true
}

// plain reports whether a uniqueness fact holds over its columns everywhere:
// it has columns, no predicate (a partial index is unique only within it) and
// no expression.
func plain(u *irv1.Uniqueness) bool {
	return len(u.GetColumns()) > 0 && u.GetPredicate() == nil && len(u.GetExpressions()) == 0
}

// Key returns the columns of t's entity key: its primary key, else a valid
// logical key, else nil (§11.1). An entity without a key gets no Key preset
// and no key-based operations.
func Key(t *irv1.Table) []string {
	if pk := primaryKey(t); pk != nil {
		return pk.GetColumns()
	}
	cols := t.GetLogicalKey().GetColumns()
	if len(cols) == 0 {
		return nil
	}
	for _, name := range cols {
		if c := column(t, name); c == nil || c.GetNullable() {
			return nil
		}
	}
	return cols
}

func primaryKey(t *irv1.Table) *irv1.Uniqueness {
	for _, u := range t.GetUniqueness() {
		if u.GetKind() == irv1.UniquenessKind_UNIQUENESS_PRIMARY_KEY {
			return u
		}
	}
	return nil
}

// unique reports whether the values of cols are unique in t: some plain
// uniqueness fact's columns are all among them. When nullable is false the
// fact's columns must also be non-null.
func unique(t *irv1.Table, cols []string, nullable bool) bool {
	for _, u := range t.GetUniqueness() {
		if !plain(u) {
			continue
		}
		ok := true
		for _, name := range u.GetColumns() {
			c := column(t, name)
			if !slices.Contains(cols, name) || c == nil || (!nullable && c.GetNullable()) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// keys computes identifies on every uniqueness fact and checks logical keys.
func (d *deriver) keys() {
	for _, r := range d.order {
		t := r.t
		slices.SortFunc(t.Uniqueness, func(a, b *irv1.Uniqueness) int { return strings.Compare(a.GetName(), b.GetName()) })
		for _, u := range t.GetUniqueness() {
			u.Identifies = Identifies(t, u)
		}
		d.logicalKey(r)
	}
}

// logicalKey fills a table's logical key from its logical-key annotation
// when the source did not, and checks it (§6.4, §7.3, §11.1).
func (d *deriver) logicalKey(r *tableRef) {
	t := r.t
	if a := find(t.GetAnnotations(), keyLogicalKey); a != nil && t.GetLogicalKey() == nil {
		cols, ok := valueColumns(a.GetValue())
		if !ok {
			d.errorf(CodeAnnotationValueInvalid, r.qn,
				"logical-key on `%s` needs a list of column names; write it as logical-key(col, ...)", diagnostics.Name(r.qn))
			return
		}
		t.LogicalKey = &irv1.LogicalKey{Columns: cols, Origin: a.GetOrigin().GetKind()}
	}
	lk := t.GetLogicalKey()
	if lk == nil {
		return
	}
	if primaryKey(t) != nil {
		d.errorf(CodeLogicalKeyWithPrimaryKey, r.qn,
			"`%s` has a primary key, so it cannot also have a logical key; remove the logical-key annotation", diagnostics.Name(r.qn))
		return
	}
	valid := true
	for _, name := range lk.GetColumns() {
		c := column(t, name)
		switch {
		case c == nil:
			d.errorf(CodeLogicalKeyUnknownColumn, r.qn,
				"logical key of `%s` names column `%s`, which does not exist; name an existing column", diagnostics.Name(r.qn), name)
			valid = false
		case c.GetNullable():
			d.errorf(CodeLogicalKeyNullable, member(r.qn, name),
				"logical key of `%s` includes nullable column `%s`; only non-null columns identify rows (§11.1), so mark it nonnull or leave it out of the key",
				diagnostics.Name(r.qn), name)
			valid = false
		}
	}
	// On a view the key is an assertion (§6.4, §11.1); on a table the core
	// warns when no uniqueness fact backs it.
	if valid && t.GetKind() == irv1.TableKind_RELATION_KIND_TABLE && !unique(t, lk.GetColumns(), false) {
		d.warnf(CodeLogicalKeyNotUnique, r.qn,
			"no primary key, unique constraint or unique index covers the logical key (%s) of `%s`; add a unique constraint on those columns",
			strings.Join(lk.GetColumns(), ", "), diagnostics.Name(r.qn))
	}
}
