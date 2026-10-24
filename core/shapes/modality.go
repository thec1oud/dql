package shapes

import irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"

// Core annotation keys that change modalities (§7.2, §14.2, §21.3).
const (
	keyImmutable  = "core.immutable"
	keyWriteOnly  = "core.write-only"
	keyReadOnly   = "core.read-only"
	keyDBFilled   = "core.db-filled"
	keyDBUpdated  = "core.db-updated"
	keyBound      = "core.bound"
	keyVersion    = "core.version"
	keySensitive  = "core.sensitive"
	keySoftDelete = "core.soft-delete"
)

// has reports whether c carries the effective annotation key. Precedence has
// already been applied (§7.1), so there is at most one per key.
func has(c *irv1.Column, key string) bool {
	for _, a := range c.GetAnnotations() {
		if a.GetKey() == key {
			return true
		}
	}
	return false
}

// dbUpdated reports whether the database sets c on every update: annotated
// db-updated, or detected as such by the source (§11.3).
func dbUpdated(c *irv1.Column) bool {
	return has(c, keyDBUpdated) || c.GetDefault().GetOnUpdate()
}

// mod is a field's presence in one preset, or the reason it is forbidden.
type mod struct {
	presence  irv1.Presence
	forbidden irv1.ForbiddenReason
}

// rule is one reason a column may be forbidden in a preset. When several
// rules forbid a column, the first in rules gives its one reason: the
// column's role in the scope and in versioning, then its annotations, then
// the key, then its class.
type rule struct {
	reason irv1.ForbiddenReason
	// applies reports whether the rule forbids c in preset; key says
	// whether c is one of the entity's key columns.
	applies func(c *irv1.Column, preset irv1.Preset, key bool) bool
}

var rules = []rule{
	{irv1.ForbiddenReason_FORBIDDEN_BOUND, func(c *irv1.Column, p irv1.Preset, _ bool) bool {
		return write(p) && has(c, keyBound)
	}},
	{irv1.ForbiddenReason_FORBIDDEN_VERSION, func(c *irv1.Column, p irv1.Preset, _ bool) bool {
		return p == irv1.Preset_PRESET_PARTIAL && has(c, keyVersion)
	}},
	{irv1.ForbiddenReason_FORBIDDEN_WRITE_ONLY, func(c *irv1.Column, p irv1.Preset, _ bool) bool {
		return p == irv1.Preset_PRESET_FULL && has(c, keyWriteOnly)
	}},
	{irv1.ForbiddenReason_FORBIDDEN_READ_ONLY, func(c *irv1.Column, p irv1.Preset, _ bool) bool {
		return write(p) && has(c, keyReadOnly)
	}},
	{irv1.ForbiddenReason_FORBIDDEN_SOFT_DELETE, func(c *irv1.Column, p irv1.Preset, _ bool) bool {
		return write(p) && has(c, keySoftDelete)
	}},
	{irv1.ForbiddenReason_FORBIDDEN_IMMUTABLE, func(c *irv1.Column, p irv1.Preset, _ bool) bool {
		return p == irv1.Preset_PRESET_PARTIAL && has(c, keyImmutable)
	}},
	{irv1.ForbiddenReason_FORBIDDEN_DB_UPDATED, func(c *irv1.Column, p irv1.Preset, _ bool) bool {
		return p == irv1.Preset_PRESET_PARTIAL && dbUpdated(c)
	}},
	{irv1.ForbiddenReason_FORBIDDEN_KEY, func(_ *irv1.Column, p irv1.Preset, key bool) bool {
		return p == irv1.Preset_PRESET_PARTIAL && key
	}},
	{irv1.ForbiddenReason_FORBIDDEN_GENERATED, func(c *irv1.Column, p irv1.Preset, _ bool) bool {
		return write(p) && c.GetGeneration() != nil
	}},
	{irv1.ForbiddenReason_FORBIDDEN_IDENTITY_ALWAYS, func(c *irv1.Column, p irv1.Preset, _ bool) bool {
		return write(p) && c.GetDefault().GetClass() == irv1.DefaultClass_DEFAULT_IDENTITY_ALWAYS
	}},
}

// write reports whether preset is a write shape (§14.1).
func write(p irv1.Preset) bool {
	return p == irv1.Preset_PRESET_DRAFT || p == irv1.Preset_PRESET_PARTIAL
}

// modality computes c's presence in the Full, Draft or Partial preset from its
// class (§14.3) and annotations (§14.2). A forbidden result from any rule
// wins; otherwise db-filled and version turn a required Draft field optional.
func modality(c *irv1.Column, preset irv1.Preset, key bool) mod {
	for _, r := range rules {
		if r.applies(c, preset, key) {
			return mod{forbidden: r.reason}
		}
	}
	if preset == irv1.Preset_PRESET_DRAFT && required(c) && !has(c, keyDBFilled) && !has(c, keyVersion) {
		return mod{presence: irv1.Presence_PRESENCE_REQUIRED}
	}
	if preset == irv1.Preset_PRESET_FULL {
		return mod{presence: irv1.Presence_PRESENCE_REQUIRED}
	}
	return mod{presence: irv1.Presence_PRESENCE_OPTIONAL}
}

// required reports whether c's class makes it required in a Draft: a plain
// non-null column with no default (§11.3). Identity, constant and expression
// defaults let the database complete it, and absent nullable columns become
// NULL.
func required(c *irv1.Column) bool {
	return !c.GetNullable() && c.GetDefault().GetClass() == irv1.DefaultClass_DEFAULT_UNSPECIFIED
}
