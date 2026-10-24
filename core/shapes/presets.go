package shapes

import (
	"slices"

	"github.com/thec1oud/dql/core/diagnostics"
	"github.com/thec1oud/dql/core/facts"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Entity is a table, view or materialized view together with its qualified
// name.
type Entity struct {
	Name  *irv1.QualifiedName
	Table *irv1.Table
}

// Entities returns every entity of catalogs in qualified-name order (§3.2).
func Entities(catalogs []*irv1.Catalog) []Entity {
	var out []Entity
	for _, c := range catalogs {
		for _, ns := range c.GetNamespaces() {
			for _, t := range ns.GetTables() {
				out = append(out, Entity{Table: t, Name: &irv1.QualifiedName{
					Catalog: c.GetName().GetStored(), Namespace: ns.GetName().GetStored(), Object: t.GetName().GetStored(),
				}})
			}
		}
	}
	slices.SortFunc(out, func(a, b Entity) int { return diagnostics.CompareNames(a.Name, b.Name) })
	return out
}

// Writable reports whether e takes writes. Views and materialized views are
// read-only entities: they get Full and no Draft or Partial (§6.4, §14.3).
func (e Entity) Writable() bool {
	return e.Table.GetKind() == irv1.TableKind_RELATION_KIND_TABLE
}

// Presets returns the Flat Full, Draft and Partial presets and the Key and
// Scope presets of every entity in catalogs (§14.3), entity by entity in
// qualified-name order and, within an entity, in preset order. A view gets
// only Full and Key (§6.4); an entity without a key gets no Key (§11.1), and
// one without bound columns gets no Scope.
func Presets(catalogs []*irv1.Catalog) []*irv1.Shape {
	ix := newIndex(catalogs)
	var out []*irv1.Shape
	for _, e := range Entities(catalogs) {
		out = append(out, ix.presets(e)...)
	}
	return out
}

func (ix *index) presets(e Entity) []*irv1.Shape {
	out := []*irv1.Shape{ix.Flat(e, irv1.Preset_PRESET_FULL)}
	if e.Writable() {
		out = append(out, ix.Flat(e, irv1.Preset_PRESET_DRAFT), ix.Flat(e, irv1.Preset_PRESET_PARTIAL))
	}
	if k := Key(e); k != nil {
		out = append(out, k)
	}
	if s := Scope(e); s != nil {
		out = append(out, s)
	}
	return out
}

// Flat returns the Flat form of the Full, Draft or Partial preset of e, with
// every field's modality (§14.1 to §14.3) and the forbidden columns with their
// reasons, all in column order.
func Flat(catalogs []*irv1.Catalog, e Entity, preset irv1.Preset) *irv1.Shape {
	return newIndex(catalogs).Flat(e, preset)
}

func (ix *index) Flat(e Entity, preset irv1.Preset) *irv1.Shape {
	s := newShape(e, preset, irv1.Form_FORM_FLAT)
	key := facts.Key(e.Table)
	for _, c := range e.Table.GetColumns() {
		m := modality(c, preset, slices.Contains(key, c.GetName().GetStored()))
		if m.forbidden != irv1.ForbiddenReason_FORBIDDEN_UNSPECIFIED {
			s.Forbidden = append(s.Forbidden, &irv1.ForbiddenField{Column: c.GetName().GetStored(), Reason: m.forbidden})
			continue
		}
		f := field(c, m.presence)
		if preset != irv1.Preset_PRESET_FULL {
			f.ValueKind = irv1.ValueKind_VALUE_KIND_VALUE_OR_EXPRESSION
			f.Validation = ix.validation(e.Table, c)
		}
		s.Fields = append(s.Fields, f)
	}
	return s
}

// Key returns the Key preset of e: its key columns and, for an entity with a
// version column, the expected version, which writes compare and reads ignore
// (§14.3). It returns nil when e has no key (§11.1).
func Key(e Entity) *irv1.Shape {
	key := facts.Key(e.Table)
	if key == nil {
		return nil
	}
	s := newShape(e, irv1.Preset_PRESET_KEY, irv1.Form_FORM_UNSPECIFIED)
	for _, c := range e.Table.GetColumns() {
		switch {
		case slices.Contains(key, c.GetName().GetStored()):
			s.Fields = append(s.Fields, field(c, irv1.Presence_PRESENCE_REQUIRED))
		case has(c, keyVersion):
			f := field(c, irv1.Presence_PRESENCE_REQUIRED)
			f.Role = irv1.FieldRole_FIELD_ROLE_EXPECTED_VERSION
			s.Fields = append(s.Fields, f)
		}
	}
	return s
}

// Scope returns the Scope preset of e: its bound columns, supplied separately
// from payloads (§14.2, §14.3). It returns nil when e has no bound column.
func Scope(e Entity) *irv1.Shape {
	s := newShape(e, irv1.Preset_PRESET_SCOPE, irv1.Form_FORM_UNSPECIFIED)
	if len(s.Scope) == 0 {
		return nil
	}
	for _, c := range e.Table.GetColumns() {
		if has(c, keyBound) {
			s.Fields = append(s.Fields, field(c, irv1.Presence_PRESENCE_REQUIRED))
		}
	}
	return s
}

// newShape returns an empty preset shape of e: its id, host name, entity,
// lineage root and scope. Key and Scope have no form.
func newShape(e Entity, preset irv1.Preset, form irv1.Form) *irv1.Shape {
	id := &irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: e.Name}, Preset: preset, Form: form}
	return &irv1.Shape{
		Id:       id,
		HostName: &irv1.Identifier{Words: append(slices.Clone(entityWords(e.Table)), presetWord[preset])},
		Entity:   e.Name,
		Lineage:  []*irv1.LineageStep{{Op: irv1.ShapeOp_PRESET, Entity: e.Name, Preset: preset, Form: form}},
		Scope:    boundColumns(e.Table),
	}
}

// presetWord is the word a preset adds to its entity's words: user.partial is
// UserPartial (§14.1). The Flat form adds none.
var presetWord = map[irv1.Preset]string{
	irv1.Preset_PRESET_FULL:    "full",
	irv1.Preset_PRESET_DRAFT:   "draft",
	irv1.Preset_PRESET_PARTIAL: "partial",
	irv1.Preset_PRESET_KEY:     "key",
	irv1.Preset_PRESET_SCOPE:   "scope",
}

// entityWords is an entity's words after its name annotation and prefixes
// (§4.4), falling back to its stored name's words.
func entityWords(t *irv1.Table) []string {
	if w := t.GetHostName().GetWords(); len(w) > 0 {
		return w
	}
	return t.GetName().GetWords()
}

func boundColumns(t *irv1.Table) []string {
	var out []string
	for _, c := range t.GetColumns() {
		if has(c, keyBound) {
			out = append(out, c.GetName().GetStored())
		}
	}
	return out
}

// field returns the field holding c with presence p. Its nullability is the
// column's, already narrowed by nonnull (§13).
func field(c *irv1.Column, p irv1.Presence) *irv1.Field {
	return &irv1.Field{
		Column:            c.GetName().GetStored(),
		Presence:          p,
		Nullable:          c.GetNullable(),
		NullabilitySource: c.GetNullabilitySource(),
		Type:              c.GetType(),
		Redact:            has(c, keySensitive),
	}
}
