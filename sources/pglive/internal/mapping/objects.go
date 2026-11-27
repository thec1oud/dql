package mapping

import (
	"fmt"

	pgextv1 "github.com/thec1oud/dql/proto/dql/ext/postgres/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/internal/pgsql"
	"github.com/thec1oud/dql/sources/pglive/internal/introspect"
)

// enum maps a native enum with its labels in declared order (§8.4).
func (m *mapper) enum(en introspect.Enum) *irv1.Enum {
	name := en.Schema + "." + en.Name
	out := &irv1.Enum{Name: ident(en.Name), Labels: en.Labels, Origin: irv1.EnumOrigin_ENUM_ORIGIN_NATIVE}
	out.Annotations = m.annotations(m.qn(en.Schema, en.Name, ""), "COMMENT ON TYPE "+name, en.Comment)
	e := &exts{m: m}
	e.comment(en.Comment)
	e.owner(en.Owner)
	e.member(en.Extension)
	e.grants(en.Grants)
	out.Extensions = e.list()
	return out
}

// domain maps a domain with its base type and constraints (§8.5). Its checks
// are recognized against the type it is ultimately over.
func (m *mapper) domain(d introspect.Domain) *irv1.Domain {
	name := d.Schema + "." + d.Name
	out := &irv1.Domain{
		Name:      ident(d.Name),
		Base:      m.typeOf("domain "+name, d.Base),
		Nullable:  !d.NotNull,
		Collation: m.collation(d.Collation, d.Base),
	}
	if d.Default != "" {
		out.Default = m.defaultOf("domain "+name, d.Default)
	}
	base := m.typeOf("domain "+name, m.baseOf(d.Base))
	for _, ch := range d.Checks {
		c, err := pgsql.RecognizeDomainCheck(ch.Definition, base)
		if err != nil {
			m.fail(fmt.Errorf("mapping: check constraint %s of domain %s: %w", ch.Name, name, err))
			continue
		}
		c.Name = ch.Name
		c.Annotations = m.annotations(m.qn(d.Schema, d.Name, ch.Name),
			"COMMENT ON CONSTRAINT "+ch.Name+" ON DOMAIN "+name, ch.Comment)
		ce := &exts{m: m}
		ce.comment(ch.Comment)
		ce.options(&pgextv1.ConstraintOptions{NotValid: ch.NotValid})
		c.Extensions = ce.list()
		out.Constraints = append(out.Constraints, c)
	}
	out.Annotations = m.annotations(m.qn(d.Schema, d.Name, ""), "COMMENT ON DOMAIN "+name, d.Comment)
	e := &exts{m: m}
	e.comment(d.Comment)
	e.owner(d.Owner)
	e.member(d.Extension)
	e.grants(d.Grants)
	out.Extensions = e.list()
	return out
}

// composite maps a composite type. Postgres cannot declare an attribute NOT
// NULL, so every attribute is nullable and unproven (§8.5).
func (m *mapper) composite(ct introspect.Composite) *irv1.Composite {
	name := ct.Schema + "." + ct.Name
	out := &irv1.Composite{Name: ident(ct.Name)}
	for i, a := range ct.Attributes {
		attr := &irv1.Column{
			Name:              ident(a.Name),
			Ordinal:           uint32(i + 1),
			Type:              m.typeOf("attribute "+name+"."+a.Name, a.Type),
			Nullable:          true,
			NullabilitySource: irv1.NullabilitySource_NULLABILITY_SOURCE_UNPROVEN,
			Collation:         m.collation(a.Collation, a.Type),
		}
		attr.Annotations = m.annotations(m.qn(ct.Schema, ct.Name, a.Name), "COMMENT ON COLUMN "+name+"."+a.Name, a.Comment)
		ae := &exts{m: m}
		ae.comment(a.Comment)
		attr.Extensions = ae.list()
		out.Attributes = append(out.Attributes, attr)
	}
	out.Annotations = m.annotations(m.qn(ct.Schema, ct.Name, ""), "COMMENT ON TYPE "+name, ct.Comment)
	e := &exts{m: m}
	e.comment(ct.Comment)
	e.owner(ct.Owner)
	e.member(ct.Extension)
	e.grants(ct.Grants)
	out.Extensions = e.list()
	return out
}

// sequence maps a sequence with its owning column (§6.1).
func (m *mapper) sequence(sq introspect.Sequence) *irv1.Sequence {
	name := sq.Schema + "." + sq.Name
	out := &irv1.Sequence{
		Name:      ident(sq.Name),
		Type:      m.typeOf("sequence "+name, sq.DataType),
		Start:     sq.Start,
		Increment: sq.Increment,
		Min:       sq.Min,
		Max:       sq.Max,
		Cycle:     sq.Cycle,
	}
	if o := sq.OwnedBy; o != nil {
		out.OwnedBy = m.qn(o.Schema, o.Table, o.Column)
	}
	out.Annotations = m.annotations(m.qn(sq.Schema, sq.Name, ""), "COMMENT ON SEQUENCE "+name, sq.Comment)
	e := &exts{m: m}
	e.comment(sq.Comment)
	e.owner(sq.Owner)
	if sq.Cache != 1 {
		e.add(&pgextv1.SequenceCache{Cache: sq.Cache})
	}
	e.grants(sq.Grants)
	out.Extensions = e.list()
	return out
}

// rangeType carries a range type's definition; its columns are ranges of
// their subtype's logical type (§8.1).
func (m *mapper) rangeType(r introspect.Range) *pgextv1.RangeType {
	name := r.Schema + "." + r.Name
	m.ignored(m.qn(r.Schema, r.Name, ""), "COMMENT ON TYPE "+name, r.Comment, "pg-live reads no annotations on range types")
	out := &pgextv1.RangeType{
		Name:        r.Name,
		Subtype:     m.typeOf("range "+name, r.Subtype),
		Multirange:  r.Multirange.Name,
		Canonical:   r.Canonical,
		SubtypeDiff: r.SubtypeDiff,
		Owner:       r.Owner,
		Comment:     r.Comment,
		Extension:   r.Extension,
		Grants:      m.grants(r.Grants),
	}
	if c := r.Collation; c != nil {
		out.Collation = c.Schema + "." + c.Name
	}
	return out
}

// baseType carries a base or pseudo type defined in a namespace read, such
// as an extension's; columns of it are opaque (§8.3).
func (m *mapper) baseType(b introspect.BaseType) *pgextv1.BaseType {
	m.ignored(m.qn(b.Schema, b.Name, ""), "COMMENT ON TYPE "+b.Schema+"."+b.Name, b.Comment, "pg-live reads no annotations on base types")
	return &pgextv1.BaseType{Name: b.Name, Kind: b.Kind, Category: b.Category, Owner: b.Owner,
		Comment: b.Comment, Extension: b.Extension, Grants: m.grants(b.Grants)}
}

// collationDef carries a collation defined in a namespace read. Columns
// record their collation's comparison facts themselves (§8.9).
func (m *mapper) collationDef(c introspect.Collation) *pgextv1.Collation {
	m.ignored(m.qn(c.Schema, c.Name, ""), "COMMENT ON COLLATION "+c.Schema+"."+c.Name, c.Comment, "pg-live reads no annotations on collations")
	return &pgextv1.Collation{Name: c.Name, Provider: localeProviders[c.Provider], Deterministic: c.Deterministic,
		Collate: c.Collate, Ctype: c.Ctype, IcuLocale: c.ICULocale, IcuRules: c.ICURules,
		Owner: c.Owner, Comment: c.Comment}
}

// statistics carries an extended statistics object.
func (m *mapper) statistics(st introspect.Statistics) *pgextv1.Statistics {
	m.ignored(m.qn(st.Schema, st.Name, ""), "COMMENT ON STATISTICS "+st.Schema+"."+st.Name, st.Comment, "pg-live reads no annotations on statistics objects")
	return &pgextv1.Statistics{Name: st.Name, Definition: st.Definition, Owner: st.Owner, Comment: st.Comment}
}
