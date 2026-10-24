package conversions

import (
	"fmt"
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// This file builds the parts of the running example (§1.3) the conversion
// table reads: the catalog facts of users and posts, and the shapes the shape
// layer expands for them (§14.3 to §14.5, §14.8), written out field by field.

var (
	i32Type   = &irv1.Type{Kind: &irv1.Type_Int32{Int32: &irv1.Empty{}}}
	i64Type   = &irv1.Type{Kind: &irv1.Type_Int64{Int64: &irv1.Empty{}}}
	textType  = &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}}
	uuidType  = &irv1.Type{Kind: &irv1.Type_Uuid{Uuid: &irv1.Empty{}}}
	timeType  = &irv1.Type{Kind: &irv1.Type_Instant{Instant: &irv1.TimeType{}}}
	nameType  = &irv1.Type{Kind: &irv1.Type_Varchar{Varchar: &irv1.Varchar{Length: 80, Unit: irv1.LengthUnit_LENGTH_UNIT_CHARACTERS}}}
	emailType = domainType("email_address")
)

func qn(object string) *irv1.QualifiedName {
	return &irv1.QualifiedName{Catalog: "shop", Namespace: "app", Object: object}
}

func ident(stored string, words ...string) *irv1.Identifier {
	return &irv1.Identifier{Stored: stored, Words: words}
}

func domainType(name string) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Named{Named: &irv1.NamedRef{Kind: &irv1.NamedRef_Domain{Domain: qn(name)}}}}
}

func enumType(name string) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Named{Named: &irv1.NamedRef{Kind: &irv1.NamedRef_Enum{Enum: qn(name)}}}}
}

// col is a non-null column carrying the given core annotations.
func col(name string, typ *irv1.Type, annotations ...string) *irv1.Column {
	c := &irv1.Column{Name: ident(name), Type: typ}
	for _, a := range annotations {
		c.Annotations = append(c.Annotations, &irv1.Annotation{Key: "core." + a})
	}
	return c
}

func withDefault(c *irv1.Column, class irv1.DefaultClass) *irv1.Column {
	c.Default = &irv1.Default{Class: class}
	return c
}

func table(name string, words []string, key []string, cols ...*irv1.Column) *irv1.Table {
	t := &irv1.Table{Name: ident(name), HostName: &irv1.Identifier{Words: words}, Kind: irv1.TableKind_RELATION_KIND_TABLE, Columns: cols}
	if key != nil {
		t.Uniqueness = []*irv1.Uniqueness{{Name: name + "_pkey", Kind: irv1.UniquenessKind_UNIQUENESS_PRIMARY_KEY, Columns: key}}
	}
	return t
}

func users() *irv1.Table {
	return table("users", []string{"user"}, []string{"id"},
		withDefault(col("id", i64Type), irv1.DefaultClass_DEFAULT_IDENTITY_ALWAYS),
		col("tenant_id", uuidType, "bound"),
		col("email", emailType),
		&irv1.Column{Name: ident("display_name"), Type: nameType, Nullable: true},
		col("password_hash", textType, "write-only", "sensitive"),
		withDefault(col("created_at", timeType, "immutable"), irv1.DefaultClass_DEFAULT_EXPRESSION),
		withDefault(col("updated_at", timeType, "db-updated"), irv1.DefaultClass_DEFAULT_EXPRESSION),
		withDefault(col("version", i32Type, "version"), irv1.DefaultClass_DEFAULT_CONSTANT),
	)
}

func posts() *irv1.Table {
	return table("posts", []string{"post"}, []string{"id"},
		withDefault(col("id", i64Type), irv1.DefaultClass_DEFAULT_IDENTITY_BY_DEFAULT),
		col("author_id", i64Type),
		col("title", textType),
	)
}

func bundleOf(tables []*irv1.Table, domains []*irv1.Domain, shapes ...*irv1.Shape) *irv1.Bundle {
	return &irv1.Bundle{
		Catalogs: []*irv1.Catalog{{
			Name: ident("shop"),
			Namespaces: []*irv1.Namespace{{
				Name:    ident("app"),
				Tables:  tables,
				Domains: domains,
			}},
		}},
		Shapes: &irv1.ShapeLayer{Shapes: shapes},
	}
}

func emailDomain() *irv1.Domain {
	return &irv1.Domain{Name: ident("email_address"), Base: textType}
}

// preset is the id of a preset shape; Key and Scope take FORM_UNSPECIFIED.
func preset(entity string, p irv1.Preset, f irv1.Form) *irv1.ShapeId {
	return &irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: qn(entity)}, Preset: p, Form: f}
}

func custom(name string) *irv1.ShapeId {
	return &irv1.ShapeId{Base: &irv1.ShapeId_Custom{Custom: name}}
}

// field spells a field's modality as a short string: "r" or "o" for
// required or optional, "?" for nullable, "x" for value-or-expression.
func field(column string, typ *irv1.Type, modality string) *irv1.Field {
	f := &irv1.Field{Column: column, Type: typ, Presence: irv1.Presence_PRESENCE_REQUIRED}
	if strings.Contains(modality, "o") {
		f.Presence = irv1.Presence_PRESENCE_OPTIONAL
	}
	f.Nullable = strings.Contains(modality, "?")
	if strings.Contains(modality, "x") {
		f.ValueKind = irv1.ValueKind_VALUE_KIND_VALUE_OR_EXPRESSION
	}
	return f
}

func forbid(column string, reason irv1.ForbiddenReason) *irv1.ForbiddenField {
	return &irv1.ForbiddenField{Column: column, Reason: reason}
}

func shape(id *irv1.ShapeId, entity string, lineage []*irv1.LineageStep, fields ...*irv1.Field) *irv1.Shape {
	return &irv1.Shape{Id: id, Entity: qn(entity), Lineage: lineage, Fields: fields}
}

func presetStep(entity string, p irv1.Preset, f irv1.Form) *irv1.LineageStep {
	return &irv1.LineageStep{Op: irv1.ShapeOp_PRESET, Entity: qn(entity), Preset: p, Form: f}
}

const (
	full    = irv1.Preset_PRESET_FULL
	draft   = irv1.Preset_PRESET_DRAFT
	partial = irv1.Preset_PRESET_PARTIAL
	keyP    = irv1.Preset_PRESET_KEY
	scopeP  = irv1.Preset_PRESET_SCOPE
	flat    = irv1.Form_FORM_FLAT
	nests   = irv1.Form_FORM_NESTS
	nestedF = irv1.Form_FORM_NESTED
	noForm  = irv1.Form_FORM_UNSPECIFIED
)

func userFullFields() []*irv1.Field {
	return []*irv1.Field{
		field("id", i64Type, "r"),
		field("tenant_id", uuidType, "r"),
		field("email", emailType, "r"),
		field("display_name", nameType, "r?"),
		field("created_at", timeType, "r"),
		field("updated_at", timeType, "r"),
		field("version", i32Type, "r"),
	}
}

func userDraftFields() []*irv1.Field {
	return []*irv1.Field{
		field("email", emailType, "rx"),
		field("display_name", nameType, "o?x"),
		field("password_hash", textType, "rx"),
		field("created_at", timeType, "ox"),
		field("updated_at", timeType, "ox"),
		field("version", i32Type, "ox"),
	}
}

func userPartialFields() []*irv1.Field {
	return []*irv1.Field{
		field("email", emailType, "ox"),
		field("display_name", nameType, "o?x"),
		field("password_hash", textType, "ox"),
	}
}

// userNests are the relations of user's read shapes (§12.5), loaded and so
// required.
func userNests(p irv1.Preset) []*irv1.Nest {
	return []*irv1.Nest{
		{Relation: "posts_to_author", Presence: irv1.Presence_PRESENCE_REQUIRED,
			Target: &irv1.Nest_Shape{Shape: preset("posts", p, flat)}},
	}
}

// userShapes are the presets of app.users (§14.3) in every form (§14.4), and
// the custom shapes user_summary and user_summary_named (§14.8).
func userShapes() []*irv1.Shape {
	fullFlat := shape(preset("users", full, flat), "users", []*irv1.LineageStep{presetStep("users", full, flat)}, userFullFields()...)
	fullFlat.Forbidden = []*irv1.ForbiddenField{forbid("password_hash", irv1.ForbiddenReason_FORBIDDEN_WRITE_ONLY)}
	draftFlat := shape(preset("users", draft, flat), "users", nil, userDraftFields()...)
	draftFlat.Forbidden = []*irv1.ForbiddenField{
		forbid("id", irv1.ForbiddenReason_FORBIDDEN_IDENTITY_ALWAYS),
		forbid("tenant_id", irv1.ForbiddenReason_FORBIDDEN_BOUND),
	}
	draftFlat.Scope = []string{"tenant_id"}
	partialFlat := shape(preset("users", partial, flat), "users", nil, userPartialFields()...)
	k := shape(preset("users", keyP, noForm), "users", nil, field("id", i64Type, "r"), field("version", i32Type, "r"))
	k.Fields[1].Role = irv1.FieldRole_FIELD_ROLE_EXPECTED_VERSION
	scope := shape(preset("users", scopeP, noForm), "users", nil, field("tenant_id", uuidType, "r"))

	out := []*irv1.Shape{fullFlat, draftFlat, partialFlat, k, scope}
	for _, p := range []struct {
		preset irv1.Preset
		fields func() []*irv1.Field
	}{{full, userFullFields}, {draft, userDraftFields}, {partial, userPartialFields}} {
		ns := userNests(p.preset)
		if p.preset != full {
			ns[0].Presence = irv1.Presence_PRESENCE_OPTIONAL
		}
		nested := shape(preset("users", p.preset, nestedF), "users", nil, p.fields()...)
		nested.Nests = ns
		only := shape(preset("users", p.preset, nests), "users", nil)
		only.Nests = ns
		out = append(out, nested, only)
	}

	summary := shape(custom("user_summary"), "users",
		[]*irv1.LineageStep{presetStep("users", full, flat), {Op: irv1.ShapeOp_PICK, Columns: []string{"id", "email", "display_name"}}},
		field("id", i64Type, "r"), field("email", emailType, "r"), field("display_name", nameType, "r?"))
	for _, c := range []string{"tenant_id", "created_at", "updated_at", "version"} {
		summary.Forbidden = append(summary.Forbidden, forbid(c, irv1.ForbiddenReason_FORBIDDEN_OMITTED))
	}
	summary.Forbidden = append(summary.Forbidden, forbid("password_hash", irv1.ForbiddenReason_FORBIDDEN_WRITE_ONLY))
	named := shape(custom("user_summary_named"), "users",
		[]*irv1.LineageStep{
			presetStep("users", full, flat),
			{Op: irv1.ShapeOp_PICK, Columns: []string{"id", "email", "display_name"}},
			{Op: irv1.ShapeOp_FROM, Shape: custom("user_summary")},
			{Op: irv1.ShapeOp_NONNULL, Columns: []string{"display_name"}},
		},
		field("id", i64Type, "r"), field("email", emailType, "r"), field("display_name", nameType, "r"))
	named.Forbidden = summary.Forbidden
	return append(out, summary, named)
}

// postDraftShapes are post's Flat and Nested Drafts (§14.5): in the Nested
// form author_id is optional because the author relation can supply it.
func postDraftShapes() []*irv1.Shape {
	flatDraft := shape(preset("posts", draft, flat), "posts", nil,
		field("id", i64Type, "ox"), field("author_id", i64Type, "rx"), field("title", textType, "rx"))
	nestedDraft := shape(preset("posts", draft, nestedF), "posts", nil,
		field("id", i64Type, "ox"), field("author_id", i64Type, "ox"), field("title", textType, "rx"))
	nestedDraft.Nests = []*irv1.Nest{{
		Relation: "author", Presence: irv1.Presence_PRESENCE_OPTIONAL,
		Target:   &irv1.Nest_Union{Union: &irv1.RelationUnionId{Kind: irv1.RelationUnionKind_RELATION_UNION_REF, Entity: qn("users")}},
		Supplies: []string{"author_id"}, NeedsOne: true,
	}}
	return []*irv1.Shape{flatDraft, nestedDraft}
}

// rows renders a conversion table one line per conversion, with the dotted
// spellings of §14.1.
func rows(d *deriver, cs []*irv1.Conversion) []string {
	var out []string
	for _, c := range cs {
		line := fmt.Sprintf("%s -> %s %s %s", d.dotted(c.GetFrom()), d.dotted(c.GetTo()),
			strings.TrimPrefix(c.GetKind().String(), "CONVERSION_"),
			strings.TrimPrefix(c.GetOrigin().String(), "CONVERSION_ORIGIN_"))
		for _, in := range c.GetInputs() {
			line += fmt.Sprintf(" %s(%s)", in.GetColumn(), strings.TrimPrefix(in.GetReason().String(), "INPUT_"))
		}
		out = append(out, line)
	}
	return out
}

// spelled returns a deriver over b that spells shape ids, for rendering.
func spelled(b *irv1.Bundle) *deriver {
	d := &deriver{tables: map[string]*irv1.Table{}, shapes: map[string]*irv1.Shape{}}
	d.index(b)
	return d
}
