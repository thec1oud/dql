package annotate

import (
	"slices"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// q names a member of an object in shop.app.
func q(object, member string) *irv1.QualifiedName {
	return &irv1.QualifiedName{Catalog: "shop", Namespace: "app", Object: object, Member: member}
}

func origin(kind irv1.OriginKind, location string) *irv1.Origin {
	return &irv1.Origin{Kind: kind, Plugin: "pg-live", Location: location}
}

func comment(key string, v *irv1.AnnotationValue) *irv1.Annotation {
	return &irv1.Annotation{Key: key, Value: v, Origin: origin(irv1.OriginKind_ORIGIN_DB_COMMENT, "COMMENT")}
}

func config(key string, v *irv1.AnnotationValue) *irv1.Annotation {
	return &irv1.Annotation{Key: key, Value: v, Origin: origin(irv1.OriginKind_ORIGIN_CONFIG, "dql.yaml")}
}

func inferred(key string, v *irv1.AnnotationValue) *irv1.Annotation {
	return &irv1.Annotation{Key: key, Value: v, Origin: origin(irv1.OriginKind_ORIGIN_INFERRED, "default")}
}

func str(s string) *irv1.AnnotationValue {
	return &irv1.AnnotationValue{Kind: &irv1.AnnotationValue_String_{String_: s}}
}

func listOf(ss ...string) *structpb.ListValue {
	l := &structpb.ListValue{}
	for _, s := range ss {
		l.Values = append(l.Values, structpb.NewStringValue(s))
	}
	return l
}

func words(ss ...string) *irv1.AnnotationValue {
	return &irv1.AnnotationValue{Kind: &irv1.AnnotationValue_Words{Words: listOf(ss...)}}
}

func list(ss ...string) *irv1.AnnotationValue {
	return &irv1.AnnotationValue{Kind: &irv1.AnnotationValue_List{List: listOf(ss...)}}
}

func strct(t *testing.T, fields map[string]any) *irv1.AnnotationValue {
	t.Helper()
	s, err := structpb.NewStruct(fields)
	if err != nil {
		t.Fatal(err)
	}
	return &irv1.AnnotationValue{Kind: &irv1.AnnotationValue_Struct{Struct: s}}
}

func codes(ds []*irv1.Diagnostic) []string {
	var out []string
	for _, d := range ds {
		out = append(out, d.GetCode())
	}
	return out
}

func wantCodes(t *testing.T, ds []*irv1.Diagnostic, want ...string) {
	t.Helper()
	if got := codes(ds); !slices.Equal(got, want) {
		for _, d := range ds {
			t.Log(d.GetMessage())
		}
		t.Fatalf("codes = %q, want %q", got, want)
	}
}

// Logical types used by targets.
var (
	textType  = &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}}
	int32Type = &irv1.Type{Kind: &irv1.Type_Int32{Int32: &irv1.Empty{}}}
	int8Type  = &irv1.Type{Kind: &irv1.Type_Int8{Int8: &irv1.Empty{}}}
	boolType  = &irv1.Type{Kind: &irv1.Type_Bool{Bool: &irv1.Empty{}}}
	instant   = &irv1.Type{Kind: &irv1.Type_Instant{Instant: &irv1.TimeType{}}}
	timeType  = &irv1.Type{Kind: &irv1.Type_LocalTime{LocalTime: &irv1.TimeType{}}}
	intervalT = &irv1.Type{Kind: &irv1.Type_Interval{Interval: &irv1.TimeType{}}}
)

func arrayType(elementNullable bool) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Array{Array: &irv1.ArrayType{Element: textType, ElementNullable: elementNullable, Dimensions: 1}}}
}
