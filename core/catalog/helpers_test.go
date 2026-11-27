package catalog

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/thec1oud/dql/core/diagnostics"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// The catalog IR as a source plugin produces it: stored names only, no words
// and no host names (§4.2).

func ident(stored string) *irv1.Identifier { return &irv1.Identifier{Stored: stored} }

func qn(catalog, namespace, object string) *irv1.QualifiedName {
	return &irv1.QualifiedName{Catalog: catalog, Namespace: namespace, Object: object}
}

func empty() *irv1.Empty { return &irv1.Empty{} }

func tInt64() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Int64{Int64: empty()}} }
func tInt32() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Int32{Int32: empty()}} }
func tText() *irv1.Type  { return &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}} }
func tUUID() *irv1.Type  { return &irv1.Type{Kind: &irv1.Type_Uuid{Uuid: empty()}} }
func tDate() *irv1.Type  { return &irv1.Type{Kind: &irv1.Type_Date{Date: empty()}} }

func tInstant() *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Instant{Instant: &irv1.TimeType{Precision: proto.Uint32(6)}}}
}

func tArray(elem *irv1.Type) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Array{Array: &irv1.ArrayType{Element: elem, ElementNullable: true, Dimensions: 1}}}
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

func tOpaque(namespace, object string) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Opaque{Opaque: &irv1.Opaque{}},
		Native: &irv1.NativeType{Name: &irv1.QualifiedName{Namespace: namespace, Object: object}, Raw: object}}
}

func native(object, raw string, modifiers ...int32) *irv1.NativeType {
	n := &irv1.NativeType{Name: &irv1.QualifiedName{Namespace: "pg_catalog", Object: object}, Raw: raw}
	for _, m := range modifiers {
		n.Modifiers = append(n.Modifiers, &irv1.TypeModifier{Kind: &irv1.TypeModifier_Int{Int: m}})
	}
	return n
}

// col is a NOT NULL column; a trailing "?" on the stored name makes it
// nullable and unproven.
func col(stored string, typ *irv1.Type) *irv1.Column {
	c := &irv1.Column{Type: typ, NullabilitySource: irv1.NullabilitySource_NULLABILITY_SOURCE_DATABASE}
	if s, ok := strings.CutSuffix(stored, "?"); ok {
		stored = s
		c.Nullable = true
		c.NullabilitySource = irv1.NullabilitySource_NULLABILITY_SOURCE_UNPROVEN
	}
	c.Name = ident(stored)
	return c
}

func numbered(cols ...*irv1.Column) []*irv1.Column {
	for i, c := range cols {
		c.Ordinal = uint32(i + 1)
	}
	return cols
}

func table(stored string, cols ...*irv1.Column) *irv1.Table {
	return &irv1.Table{Name: ident(stored), Kind: irv1.TableKind_RELATION_KIND_TABLE, Columns: numbered(cols...)}
}

func view(stored string, cols ...*irv1.Column) *irv1.Table {
	t := table(stored, cols...)
	t.Kind = irv1.TableKind_RELATION_KIND_VIEW
	return t
}

func pk(name string, cols ...string) *irv1.Uniqueness {
	return &irv1.Uniqueness{Name: name, Kind: irv1.UniquenessKind_UNIQUENESS_PRIMARY_KEY, Columns: cols, NullsDistinct: true}
}

func unique(name string, cols ...string) *irv1.Uniqueness {
	return &irv1.Uniqueness{Name: name, Kind: irv1.UniquenessKind_UNIQUENESS_CONSTRAINT, Columns: cols, NullsDistinct: true}
}

func fk(name string, cols []string, target *irv1.QualifiedName, refs ...string) *irv1.ForeignKey {
	return &irv1.ForeignKey{
		Name: ident(name), Columns: cols,
		References: &irv1.ColumnSet{Table: target, Columns: refs},
		Match:      irv1.ForeignKeyMatch_FOREIGN_KEY_MATCH_SIMPLE,
		OnUpdate:   irv1.ForeignKeyAction_FOREIGN_KEY_ACTION_NO_ACTION,
		OnDelete:   irv1.ForeignKeyAction_FOREIGN_KEY_ACTION_NO_ACTION,
	}
}

func portable(p *irv1.PortableConstraint) *irv1.Constraint_Portable {
	return &irv1.Constraint_Portable{Portable: p}
}

func lengthCheck(name string, columns []string, minLen, maxLen *uint32) *irv1.Constraint {
	return &irv1.Constraint{Name: name, Kind: irv1.ConstraintKind_CONSTRAINT_KIND_CHECK, Columns: columns, ClientValidated: true,
		Form: portable(&irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Length{Length: &irv1.LengthConstraint{
			Min: minLen, Max: maxLen, Unit: irv1.LengthUnit_LENGTH_UNIT_CHARACTERS,
		}}})}
}

func membershipCheck(name string, columns []string, values ...string) *irv1.Constraint {
	m := &irv1.MembershipConstraint{}
	for _, v := range values {
		m.Values = append(m.Values, &irv1.Value{Kind: &irv1.Value_String_{String_: v}})
	}
	return &irv1.Constraint{Name: name, Kind: irv1.ConstraintKind_CONSTRAINT_KIND_CHECK, Columns: columns, ClientValidated: true,
		Form: portable(&irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Membership{Membership: m}})}
}

func opaqueCheck(name string, columns []string, text string) *irv1.Constraint {
	return &irv1.Constraint{Name: name, Kind: irv1.ConstraintKind_CONSTRAINT_KIND_CHECK, Columns: columns,
		Form: &irv1.Constraint_Opaque{Opaque: &irv1.NativeText{Native: text}}}
}

func stringValue(s string) *irv1.AnnotationValue {
	return &irv1.AnnotationValue{Kind: &irv1.AnnotationValue_String_{String_: s}}
}

func listOf(items ...string) *structpb.ListValue {
	l := &structpb.ListValue{}
	for _, s := range items {
		l.Values = append(l.Values, structpb.NewStringValue(s))
	}
	return l
}

func listValue(items ...string) *irv1.AnnotationValue {
	return &irv1.AnnotationValue{Kind: &irv1.AnnotationValue_List{List: listOf(items...)}}
}

func wordsValue(words ...string) *irv1.AnnotationValue {
	return &irv1.AnnotationValue{Kind: &irv1.AnnotationValue_Words{Words: listOf(words...)}}
}

func structValue(fields map[string]any) *irv1.AnnotationValue {
	s, err := structpb.NewStruct(fields)
	if err != nil {
		panic(err)
	}
	return &irv1.AnnotationValue{Kind: &irv1.AnnotationValue_Struct{Struct: s}}
}

// comment is an annotation pg-live read from a comment; key is written as
// in the comment, without core.
func comment(key, location string, v *irv1.AnnotationValue) *irv1.Annotation {
	return &irv1.Annotation{Key: irKey(key), Value: v, Origin: &irv1.Origin{
		Kind: irv1.OriginKind_ORIGIN_DB_COMMENT, Plugin: "pg-live", Location: location,
	}}
}

// configured is an annotation pg-live read from dql.yaml under key.
func configured(key, annotation string, v *irv1.AnnotationValue) *irv1.Annotation {
	return &irv1.Annotation{Key: irKey(annotation), Value: v, Origin: &irv1.Origin{
		Kind: irv1.OriginKind_ORIGIN_CONFIG, Plugin: "pg-live", Location: "dql.yaml#annotations." + key,
	}}
}

func irKey(k string) string {
	if strings.HasPrefix(k, "x.") {
		return k
	}
	return "core." + k
}

type anyMessage = anypb.Any

// mustAny wraps an extension with the type URL prefix of dql.ext (§6.2).
func mustAny(m proto.Message) *anypb.Any {
	a := &anypb.Any{}
	if err := anypb.MarshalFrom(a, m, proto.MarshalOptions{Deterministic: true}); err != nil {
		panic(err)
	}
	a.TypeUrl = "type.dql.dev/" + string(m.ProtoReflect().Descriptor().FullName())
	return a
}

// assertContains fails unless got holds every field of want, a design record
// example in protobuf JSON. Fields the example elides are not compared
// (implementation guide §6.1).
func assertContains(t *testing.T, got proto.Message, want string) {
	t.Helper()
	w := got.ProtoReflect().New().Interface()
	if err := protojson.Unmarshal([]byte(want), w); err != nil {
		t.Fatalf("example does not decode: %v", err)
	}
	var gm, wm any
	gb, _ := protojson.Marshal(got)
	wb, _ := protojson.Marshal(w)
	_ = json.Unmarshal(gb, &gm)
	_ = json.Unmarshal(wb, &wm)
	if path, ok := subset(wm, gm, "$"); !ok {
		t.Errorf("output differs from the example at %s\n got: %s\nwant: %s", path, gb, wb)
	}
}

func subset(want, got any, path string) (string, bool) {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return path, false
		}
		for k, v := range w {
			if p, ok := subset(v, g[k], path+"."+k); !ok {
				return p, false
			}
		}
		return "", true
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return path, false
		}
		for i := range w {
			if p, ok := subset(w[i], g[i], fmt.Sprintf("%s[%d]", path, i)); !ok {
				return p, false
			}
		}
		return "", true
	}
	return path, reflect.DeepEqual(want, got)
}

// lines renders diagnostics as "SEVERITY code target" lines.
func lines(ds []*irv1.Diagnostic) []string {
	var out []string
	for _, d := range ds {
		out = append(out, strings.TrimPrefix(d.GetSeverity().String(), "SEVERITY_")+" "+d.GetCode()+" "+diagnostics.Name(d.GetTarget()))
	}
	return out
}

// find returns the table, view, enum, domain, composite or sequence with
// stored name object in the bundle, or fails.
func namespace(t *testing.T, b *irv1.Bundle, catalog, ns string) *irv1.Namespace {
	t.Helper()
	for _, c := range b.GetCatalogs() {
		for _, n := range c.GetNamespaces() {
			if c.GetName().GetStored() == catalog && n.GetName().GetStored() == ns {
				return n
			}
		}
	}
	t.Fatalf("no namespace %s.%s", catalog, ns)
	return nil
}

func tableOf(t *testing.T, ns *irv1.Namespace, name string) *irv1.Table {
	t.Helper()
	for _, tb := range ns.GetTables() {
		if tb.GetName().GetStored() == name {
			return tb
		}
	}
	t.Fatalf("no table %s", name)
	return nil
}

func columnOf(t *testing.T, tb *irv1.Table, name string) *irv1.Column {
	t.Helper()
	for _, c := range tb.GetColumns() {
		if c.GetName().GetStored() == name {
			return c
		}
	}
	t.Fatalf("no column %s.%s", tb.GetName().GetStored(), name)
	return nil
}

func relationOf(t *testing.T, tb *irv1.Table, name string) *irv1.Relation {
	t.Helper()
	for _, r := range tb.GetRelations() {
		if r.GetName().GetStored() == name {
			return r
		}
	}
	t.Fatalf("no relation %s.%s", tb.GetName().GetStored(), name)
	return nil
}
