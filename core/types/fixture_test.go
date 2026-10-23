package types

import (
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func id(s string) *irv1.Identifier {
	return &irv1.Identifier{Stored: s, Words: strings.Split(s, "_")}
}

func qn(cat, ns, obj string) *irv1.QualifiedName {
	return &irv1.QualifiedName{Catalog: cat, Namespace: ns, Object: obj}
}

func native(ns, obj string) *irv1.NativeType {
	return &irv1.NativeType{Name: &irv1.QualifiedName{Namespace: ns, Object: obj}, Raw: obj}
}

var (
	tInt64 = &irv1.Type{Kind: &irv1.Type_Int64{Int64: &irv1.Empty{}}}
	tInt32 = &irv1.Type{Kind: &irv1.Type_Int32{Int32: &irv1.Empty{}}}
	tText  = &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}}
)

func tArray(elem *irv1.Type, dims uint32) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Array{Array: &irv1.ArrayType{Element: elem, ElementNullable: true, Dimensions: dims}}}
}

func tRange(elem *irv1.Type) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Range{Range: &irv1.RangeType{Element: elem}}}
}

func tMultirange(elem *irv1.Type) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Multirange{Multirange: &irv1.RangeType{Element: elem}}}
}

func tWeak(aff *irv1.Type) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Weak{Weak: &irv1.Weak{Affinity: aff, StorageClasses: []irv1.StorageClass{irv1.StorageClass_STORAGE_INTEGER}}}}
}

func tOpaque(ns, obj string) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Opaque{Opaque: &irv1.Opaque{}}, Native: native(ns, obj)}
}

func tEnum(q *irv1.QualifiedName) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Named{Named: &irv1.NamedRef{Kind: &irv1.NamedRef_Enum{Enum: q}}}}
}

func tDomain(q *irv1.QualifiedName) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Named{Named: &irv1.NamedRef{Kind: &irv1.NamedRef_Domain{Domain: q}}}}
}

func tComposite(q *irv1.QualifiedName) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Named{Named: &irv1.NamedRef{Kind: &irv1.NamedRef_Composite{Composite: q}}}}
}

func col(name string, t *irv1.Type) *irv1.Column {
	return &irv1.Column{Name: id(name), Type: t}
}

// shop is the typed part of the running example (§1.3): no opaque and no
// weak types, and no reference across catalogs.
func shop() *irv1.Bundle {
	app := func(o string) *irv1.QualifiedName { return qn("shop", "app", o) }
	return &irv1.Bundle{
		Dialect: irv1.Dialect_DIALECT_POSTGRES,
		Catalogs: []*irv1.Catalog{{
			Name: id("shop"),
			Namespaces: []*irv1.Namespace{{
				Name: id("app"),
				Tables: []*irv1.Table{
					{Name: id("posts"), Columns: []*irv1.Column{
						col("id", tInt64),
						col("author_id", tInt64),
						col("status", tEnum(app("post_status"))),
						col("price", tComposite(app("money_amount"))),
						col("labels", tArray(tText, 1)),
					}, ForeignKeys: []*irv1.ForeignKey{{
						Name: id("posts_author_id_fkey"), Columns: []string{"author_id"},
						References: &irv1.ColumnSet{Table: app("users"), Columns: []string{"id"}},
					}}, Relations: []*irv1.Relation{{
						Name: id("author"), Owner: app("posts"), Via: app("posts"),
						From: &irv1.ColumnSet{Columns: []string{"author_id"}},
						To:   &irv1.ColumnSet{Table: app("users"), Columns: []string{"id"}},
					}}},
					{Name: id("tags"), Columns: []*irv1.Column{col("name", tDomain(app("slug_text")))}},
					{Name: id("published_posts"), Kind: irv1.TableKind_RELATION_KIND_VIEW,
						Columns: []*irv1.Column{col("id", tInt64)}, DependsOn: []*irv1.QualifiedName{app("posts")}},
				},
				Enums:      []*irv1.Enum{{Name: id("post_status"), Labels: []string{"draft", "published", "archived"}}},
				Domains:    []*irv1.Domain{{Name: id("slug_text"), Base: tText}},
				Composites: []*irv1.Composite{{Name: id("money_amount"), Attributes: []*irv1.Column{col("currency", tText)}}},
				Sequences:  []*irv1.Sequence{{Name: id("posts_id_seq"), Type: tInt64}},
				Routines: []*irv1.Routine{{Name: id("post_score"), Overloads: []*irv1.Overload{
					{Signature: []string{"int64"}, Arguments: []*irv1.Argument{{Name: id("p_id"), Type: tInt64}},
						Returns: &irv1.Returns{Kind: &irv1.Returns_Scalar{Scalar: &irv1.ReturnValue{Type: &irv1.Type{Kind: &irv1.Type_Decimal{Decimal: &irv1.Decimal{}}}, Nullable: true}}}},
					{Signature: []string{"int64", "int32"}, Arguments: []*irv1.Argument{{Name: id("p_id"), Type: tInt64}, {Name: id("window_days"), Type: tInt32}},
						Returns: &irv1.Returns{Kind: &irv1.Returns_SetOf{SetOf: &irv1.ReturnValue{Type: tInt64}}}},
					{Signature: []string{}, Returns: &irv1.Returns{Kind: &irv1.Returns_Table{Table: &irv1.ReturnTable{Columns: []*irv1.Column{col("score", tInt64)}}}}},
					{Signature: []string{"text"}, Arguments: []*irv1.Argument{{Name: id("note"), Type: tText}},
						Returns: &irv1.Returns{Kind: &irv1.Returns_Nothing{Nothing: &irv1.Empty{}}}},
				}}},
			}},
		}},
	}
}

// ns returns the first namespace of b's first catalog.
func ns(b *irv1.Bundle) *irv1.Namespace { return b.GetCatalogs()[0].GetNamespaces()[0] }

// codes renders diagnostics as "code target" lines.
func codes(ds []*irv1.Diagnostic) []string {
	var out []string
	for _, d := range ds {
		q := d.GetTarget()
		out = append(out, d.GetCode()+" "+strings.Trim(strings.Join([]string{q.GetCatalog(), q.GetNamespace(), q.GetObject(), q.GetMember()}, "."), "."))
	}
	return out
}
