package conversions

import (
	"slices"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Core annotation keys this package reads (§7.2, §14.2).
const (
	keyBound     = "core.bound"
	keyDBFilled  = "core.db-filled"
	keyWriteOnly = "core.write-only"
)

// classification is the kind of one conversion.
type classification struct {
	kind irv1.ConversionKind
	// CONVERSION_NEEDS_INPUTS: the fields the caller passes.
	inputs []*irv1.ConversionInput
	// CONVERSION_IMPOSSIBLE: the target's columns and relations the source
	// cannot supply, in the target's order, for diagnostics.
	missing []string
}

// classify computes the kind of the conversion from a to b (§14.9,
// implementation guide §5.3):
//
//   - A Partial applied onto a Full or Draft of its entity is With base.
//   - Total when a supplies every field of b: the same column, the same type,
//     present wherever b requires it, non-null wherever b is non-null, and a
//     value wherever b takes only values. An optional field of b that a does
//     not hold is left absent. A required relation of b needs the same
//     relation in a.
//   - Fallible when the only gap is a nullable field that b narrows to
//     non-null: the conversion checks it at run time (§8.9 and §13).
//   - Via database when a is a Draft and the database produces every missing
//     field: a default, an identity, a generated, db-filled or bound column,
//     a foreign key a relation of a can create (§14.5), or, when b is a
//     persisted row, a column a gives in any modality, which the database
//     stores.
//   - Needs inputs when b is a write shape and every missing field is one a
//     leaves out because it is write-only, picked or omitted away, or bound:
//     the caller passes them.
//   - Impossible otherwise.
func classify(d *deriver, a, b *irv1.Shape) classification {
	ra, rb := rootPreset(a), rootPreset(b)
	if ra == irv1.Preset_PRESET_PARTIAL && (rb == irv1.Preset_PRESET_FULL || rb == irv1.Preset_PRESET_DRAFT) &&
		proto.Equal(a.GetEntity(), b.GetEntity()) {
		return classification{kind: irv1.ConversionKind_CONVERSION_WITH_BASE}
	}
	held := map[string]*irv1.Field{}
	for _, f := range a.GetFields() {
		held[f.GetColumn()] = f
	}
	var gaps, narrowed []*irv1.Field
	for _, f := range b.GetFields() {
		s := held[f.GetColumn()]
		switch {
		case s == nil && f.GetPresence() == irv1.Presence_PRESENCE_OPTIONAL:
			// Left absent.
		case !fits(s, f):
			gaps = append(gaps, f)
		case s.GetNullable() && !f.GetNullable():
			narrowed = append(narrowed, f)
		}
	}
	var nests []string
	for _, n := range b.GetNests() {
		if n.GetPresence() == irv1.Presence_PRESENCE_REQUIRED && !suppliesNest(a, n) {
			nests = append(nests, n.GetRelation())
		}
	}
	switch {
	case len(gaps) == 0 && len(nests) == 0 && len(narrowed) == 0:
		return classification{kind: irv1.ConversionKind_CONVERSION_TOTAL}
	case len(gaps) == 0 && len(nests) == 0:
		return classification{kind: irv1.ConversionKind_CONVERSION_FALLIBLE}
	}
	// Beside any other gap, a narrowed field is one more field a cannot
	// supply. Walking b's fields keeps the target's column order.
	viaDatabase := ra == irv1.Preset_PRESET_DRAFT && len(nests) == 0
	needsInputs := (rb == irv1.Preset_PRESET_DRAFT || rb == irv1.Preset_PRESET_PARTIAL) && len(nests) == 0
	var inputs []*irv1.ConversionInput
	var blocked []string
	for _, f := range b.GetFields() {
		if !slices.Contains(gaps, f) && !slices.Contains(narrowed, f) {
			continue
		}
		produced := ra == irv1.Preset_PRESET_DRAFT && d.produced(a, b, held[f.GetColumn()], f)
		reason := inputReason(a, f)
		viaDatabase = viaDatabase && produced
		needsInputs = needsInputs && reason != irv1.InputReason_INPUT_UNSPECIFIED
		inputs = append(inputs, &irv1.ConversionInput{Column: f.GetColumn(), Reason: reason})
		if !produced && reason == irv1.InputReason_INPUT_UNSPECIFIED {
			blocked = append(blocked, f.GetColumn())
		}
	}
	switch {
	case viaDatabase:
		return classification{kind: irv1.ConversionKind_CONVERSION_VIA_DATABASE}
	case needsInputs:
		return classification{kind: irv1.ConversionKind_CONVERSION_NEEDS_INPUTS, inputs: inputs}
	}
	if len(blocked) == 0 && len(nests) == 0 {
		// Each field can be filled, but some only by the database and others
		// only by the caller, and no conversion does both.
		for _, in := range inputs {
			blocked = append(blocked, in.GetColumn())
		}
	}
	return classification{kind: irv1.ConversionKind_CONVERSION_IMPOSSIBLE, missing: append(blocked, nests...)}
}

// fits reports whether source field s can fill target field f, apart from
// nullability.
func fits(s, f *irv1.Field) bool {
	expression := irv1.ValueKind_VALUE_KIND_VALUE_OR_EXPRESSION
	return s != nil && proto.Equal(s.GetType(), f.GetType()) &&
		(s.GetPresence() == irv1.Presence_PRESENCE_REQUIRED || f.GetPresence() == irv1.Presence_PRESENCE_OPTIONAL) &&
		(s.GetValueKind() != expression || f.GetValueKind() == expression)
}

// suppliesNest reports whether a has the relation n holds, holding the same
// shape or union, present and non-null wherever n is.
func suppliesNest(a *irv1.Shape, n *irv1.Nest) bool {
	for _, s := range a.GetNests() {
		if s.GetRelation() == n.GetRelation() &&
			proto.Equal(s.GetShape(), n.GetShape()) && proto.Equal(s.GetUnion(), n.GetUnion()) &&
			(s.GetPresence() == irv1.Presence_PRESENCE_REQUIRED || n.GetPresence() == irv1.Presence_PRESENCE_OPTIONAL) &&
			(!s.GetNullable() || n.GetNullable()) {
			return true
		}
	}
	return false
}

// produced reports whether the database produces target field f when Draft
// a is written (§11.3, §14.2, §14.5).
func (d *deriver) produced(a, b *irv1.Shape, s, f *irv1.Field) bool {
	c := d.column(b.GetEntity(), f.GetColumn())
	if c.GetDefault().GetClass() != irv1.DefaultClass_DEFAULT_UNSPECIFIED || c.GetGeneration() != nil ||
		annotated(c, keyDBFilled) || annotated(c, keyBound) {
		return true
	}
	for _, n := range a.GetNests() {
		if slices.Contains(n.GetSupplies(), f.GetColumn()) {
			return true
		}
	}
	// A persisted row holds what the Draft gave, and NULL where it gave
	// nothing for a nullable column.
	rb := rootPreset(b)
	return (rb == irv1.Preset_PRESET_FULL || rb == irv1.Preset_PRESET_KEY) &&
		s != nil && proto.Equal(s.GetType(), f.GetType()) && (!s.GetNullable() || f.GetNullable())
}

// inputReason says why a leaves field f to the caller, or is unspecified
// when a cannot: it holds f in a modality that cannot fill it, or leaves it
// out for a reason the caller cannot make up for, such as a generated column.
// The caller passes a write-only column, which no read shape holds, and a
// column a custom shape picked, omitted or bound away (§14.8).
func inputReason(a *irv1.Shape, f *irv1.Field) irv1.InputReason {
	if slices.Contains(a.GetBind(), f.GetColumn()) {
		return irv1.InputReason_INPUT_OMITTED
	}
	for _, x := range a.GetForbidden() {
		if x.GetColumn() != f.GetColumn() {
			continue
		}
		switch x.GetReason() {
		case irv1.ForbiddenReason_FORBIDDEN_WRITE_ONLY:
			return irv1.InputReason_INPUT_WRITE_ONLY
		case irv1.ForbiddenReason_FORBIDDEN_OMITTED:
			return irv1.InputReason_INPUT_OMITTED
		}
	}
	return irv1.InputReason_INPUT_UNSPECIFIED
}

// rootPreset is the preset a shape is or derives from (§14.8).
func rootPreset(s *irv1.Shape) irv1.Preset {
	if p := s.GetId().GetPreset(); p != irv1.Preset_PRESET_UNSPECIFIED {
		return p
	}
	if l := s.GetLineage(); len(l) > 0 {
		return l[0].GetPreset()
	}
	return irv1.Preset_PRESET_UNSPECIFIED
}

func (d *deriver) column(entity *irv1.QualifiedName, name string) *irv1.Column {
	for _, c := range d.tables[qnKey(entity)].GetColumns() {
		if c.GetName().GetStored() == name {
			return c
		}
	}
	return nil
}

func annotated(c *irv1.Column, key string) bool {
	for _, a := range c.GetAnnotations() {
		if a.GetKey() == key {
			return true
		}
	}
	return false
}
