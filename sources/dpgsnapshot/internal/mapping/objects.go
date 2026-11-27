package mapping

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"

	pgextv1 "github.com/thec1oud/dql/proto/dql/ext/postgres/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/dpgsnapshot/internal/format"
	"github.com/thec1oud/dql/sources/internal/pgsql"
)

// typeFields are the TYPE record fields every mapped type carries.
var typeFields = []string{"schema", "name", "variant", "comment", "owner"}

// enum maps a native enum with its labels in declared order (§8.4).
func (m *mapper) enum(t *format.Type) *irv1.Enum {
	name := t.Schema + "." + t.Name
	out := &irv1.Enum{Name: ident(t.Name), Labels: t.Values, Origin: irv1.EnumOrigin_ENUM_ORIGIN_NATIVE}
	out.Annotations = m.annotations(m.qn(t.Schema, t.Name, ""), "COMMENT ON TYPE "+name, deref(t.Comment))
	e := &exts{m: m}
	e.comment(deref(t.Comment))
	e.owner(t.Owner)
	e.record("type", "", t, append(slices.Clone(typeFields), "values")...)
	out.Extensions = e.list()
	return out
}

// domain maps a domain with its base type and constraints (§8.5). Its checks
// are recognized against the type it is ultimately over, and those written
// without a name are named as Postgres names them, domain_check
// (fidelity.DerivedNames). A domain is non-null when it is declared NOT NULL.
func (m *mapper) domain(t *format.Type) *irv1.Domain {
	name := t.Schema + "." + t.Name
	out := &irv1.Domain{
		Name:      ident(t.Name),
		Base:      m.typeOf("domain "+name, t.Schema, t.DomainBaseType),
		Nullable:  !t.DomainNotNull,
		Collation: m.collation(t.Schema, t.DomainBaseType, t.DomainCollation),
	}
	if t.DomainDefault != nil {
		out.Default = m.defaultOf("domain "+name, t.Schema, *t.DomainDefault)
	}
	base := m.checkType("domain "+name, t.Schema, t.DomainBaseType)
	e := &exts{m: m}
	for _, c := range t.DomainConstraints {
		if c.Type != "CHECK" {
			// NOT NULL is the domain's nullability; the record keeps it.
			out.Nullable = out.Nullable && c.Type != "NOT NULL"
			e.record("constraint", c.Name, c)
			continue
		}
		ch, err := pgsql.RecognizeDomainCheck(c.Expr, base)
		if err != nil {
			m.fail(fmt.Errorf("mapping: check constraint %q of domain %s: %w", c.Expr, name, err))
			continue
		}
		ch.Name = c.Name
		if ch.Name == "" {
			ch.Name = m.namer(t.Schema).check(t.Name, "", "check")
		}
		ch.Annotations = m.annotations(m.qn(t.Schema, t.Name, ch.Name),
			"COMMENT ON CONSTRAINT "+ch.Name+" ON DOMAIN "+name, c.Comment)
		ce := &exts{m: m}
		ce.comment(c.Comment)
		ce.options(&pgextv1.ConstraintOptions{NotValid: c.NotValid})
		ce.record("constraint", "", c, checkFields(ch)...)
		ch.Extensions = ce.list()
		out.Constraints = append(out.Constraints, ch)
	}
	slices.SortStableFunc(out.Constraints, func(a, b *irv1.Constraint) int { return strings.Compare(a.Name, b.Name) })
	out.Annotations = m.annotations(m.qn(t.Schema, t.Name, ""), "COMMENT ON DOMAIN "+name, deref(t.Comment))
	e.comment(deref(t.Comment))
	e.owner(t.Owner)
	e.record("type", "", t, append(slices.Clone(typeFields),
		"domain_base_type", "domain_not_null", "domain_default", "domain_collation", "domain_constraints")...)
	out.Extensions = e.list()
	return out
}

// checkFields are the fields of a check constraint's record the IR carries:
// all but its text when it is recognized as a portable kind, which does not
// keep the text.
func checkFields(c *irv1.Constraint) []string {
	used := []string{"name", "type", "comment", "not_valid", "no_inherit"}
	if c.GetOpaque() != nil {
		used = append(used, "expr")
	}
	return used
}

// composite maps a composite type. Postgres cannot declare an attribute NOT
// NULL, so every attribute is nullable and unproven (§8.5).
func (m *mapper) composite(t *format.Type) *irv1.Composite {
	name := t.Schema + "." + t.Name
	out := &irv1.Composite{Name: ident(t.Name)}
	for i, a := range t.CompositeAttrs {
		attr := &irv1.Column{
			Name:              ident(a.Name),
			Ordinal:           uint32(i + 1),
			Type:              m.typeOf("attribute "+name+"."+a.Name, t.Schema, a.Type),
			Nullable:          true,
			NullabilitySource: irv1.NullabilitySource_NULLABILITY_SOURCE_UNPROVEN,
			Collation:         m.collation(t.Schema, a.Type, a.Collation),
		}
		attr.Annotations = m.annotations(m.qn(t.Schema, t.Name, a.Name), "COMMENT ON COLUMN "+name+"."+a.Name, deref(a.Comment))
		ae := &exts{m: m}
		ae.comment(deref(a.Comment))
		ae.record("attribute", "", a, "name", "type", "collation", "comment")
		attr.Extensions = ae.list()
		out.Attributes = append(out.Attributes, attr)
	}
	out.Annotations = m.annotations(m.qn(t.Schema, t.Name, ""), "COMMENT ON TYPE "+name, deref(t.Comment))
	e := &exts{m: m}
	e.comment(deref(t.Comment))
	e.owner(t.Owner)
	e.record("type", "", t, append(slices.Clone(typeFields), "composite_attrs")...)
	out.Extensions = e.list()
	return out
}

// seqOptions are a sequence's declared options; nil means the default.
type seqOptions struct {
	increment, min, max, start, cache *int64
	noMin, noMax, cycle               bool
}

// seqBounds are the ranges of the integer types a sequence may have.
var seqBounds = map[string][2]int64{
	"int2": {math.MinInt16, math.MaxInt16},
	"int4": {math.MinInt32, math.MaxInt32},
	"int8": {math.MinInt64, math.MaxInt64},
}

// formatted names the integer types as format_type prints them, which is
// the type text pg-live reads for a sequence.
var formatted = map[string]string{"int2": "smallint", "int4": "integer", "int8": "bigint"}

// newSequence builds a sequence of the integer type written as typeText,
// filling the options left undeclared as CREATE SEQUENCE does: increment 1;
// an ascending sequence runs from 1 to the type's maximum, a descending one
// from the type's minimum to -1; it starts at its minimum when ascending and
// its maximum when descending.
func (m *mapper) newSequence(schema, name, typeText string, o seqOptions, e *exts) *irv1.Sequence {
	what := "sequence " + schema + "." + name
	n, err := pgsql.ParseTypeText(typeText)
	bounds, ok := seqBounds[n.Name]
	if err != nil || n.Namespace != "pg_catalog" || !ok || n.Array {
		m.fail(fmt.Errorf("mapping: %s has type %q; a sequence is a smallint, integer or bigint", what, typeText))
		return &irv1.Sequence{Name: ident(name)}
	}
	s := &irv1.Sequence{Name: ident(name), Type: m.typeOf(what, schema, formatted[n.Name]), Increment: 1, Cycle: o.cycle}
	if o.increment != nil {
		s.Increment = *o.increment
	}
	s.Min, s.Max = 1, bounds[1]
	if s.Increment < 0 {
		s.Min, s.Max = bounds[0], -1
	}
	if o.min != nil && !o.noMin {
		s.Min = *o.min
	}
	if o.max != nil && !o.noMax {
		s.Max = *o.max
	}
	s.Start = s.Min
	if s.Increment < 0 {
		s.Start = s.Max
	}
	if o.start != nil {
		s.Start = *o.start
	}
	if o.cache != nil && *o.cache != 1 {
		e.add(&pgextv1.SequenceCache{Cache: *o.cache})
	}
	return s
}

// sequence maps a sequence with its owning column (§6.1).
func (m *mapper) sequence(sq *format.Sequence) {
	name := sq.Schema + "." + sq.Name
	e := &exts{m: m}
	s := m.newSequence(sq.Schema, sq.Name, cmp.Or(sq.AsType, "bigint"), seqOptions{
		increment: sq.IncrementBy, min: sq.MinValue, max: sq.MaxValue, start: sq.StartValue, cache: sq.Cache,
		noMin: sq.NoMinValue, noMax: sq.NoMaxValue, cycle: sq.Cycle,
	}, e)
	if sq.OwnedBy != nil {
		s.OwnedBy = m.ownedBy(sq.Schema, *sq.OwnedBy)
	}
	s.Annotations = m.annotations(m.qn(sq.Schema, sq.Name, ""), "COMMENT ON SEQUENCE "+name, deref(sq.Comment))
	e.comment(deref(sq.Comment))
	e.owner(sq.Owner)
	if sq.Unlogged {
		e.add(&pgextv1.Unlogged{})
	}
	e.record("sequence", "", sq, "schema", "name", "owner", "comment", "increment_by", "min_value", "max_value",
		"no_min_value", "no_max_value", "start_value", "cache", "cycle", "as_type", "owned_by", "unlogged")
	s.Extensions = e.list()
	ns := m.b.get(sq.Schema).ns
	ns.Sequences = append(ns.Sequences, s)
}

// ownedBy reads OWNED BY's table.column or schema.table.column, as written;
// dpg writes OWNED BY NONE as NONE, which owns nothing.
func (m *mapper) ownedBy(schema, text string) *irv1.QualifiedName {
	parts := strings.Split(text, ".")
	switch {
	case text == "NONE":
		return nil
	case len(parts) == 2:
		return m.qn(schema, parts[0], parts[1])
	case len(parts) == 3:
		return m.qn(parts[0], parts[1], parts[2])
	}
	m.fail(fmt.Errorf("mapping: sequence %s.%s is owned by %q, which names no column", schema, text, text))
	return nil
}

// defaultOf classifies a default expression (§11.3). The sequence of a
// nextval default is in this catalog, in the schema of the object using it
// when it is written without one (fidelity.SearchPath).
func (m *mapper) defaultOf(what, schema, text string) *irv1.Default {
	d, err := pgsql.ClassifyDefault(text)
	if err != nil {
		m.fail(fmt.Errorf("mapping: default of %s: %w", what, err))
		return nil
	}
	if d.Sequence != nil {
		d.Sequence.Catalog = m.opts.Catalog
		d.Sequence.Namespace = cmp.Or(d.Sequence.Namespace, schema)
	}
	return d
}

// options carries o unless every option has its default.
func (e *exts) options(o *pgextv1.ConstraintOptions) {
	if o.Deferrable || o.InitiallyDeferred || o.NotValid || o.NoInherit || len(o.OnDeleteColumns) > 0 {
		e.add(o)
	}
}
