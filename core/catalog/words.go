package catalog

import (
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/thec1oud/dql/core/annotate"
	"github.com/thec1oud/dql/core/naming"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// fillWords sets the words of every Identifier in m from its stored name
// (§4.2). The core computes words once, here; whatever words a source sent
// are replaced. Identifiers the core computes later (host names, relation
// names) are built with their words. Map fields hold no identifiers (only
// the struct values of annotations are maps), so they are not walked, and an
// extension's Any is bytes, which stay untouched (§6.3).
func fillWords(m protoreflect.Message) {
	if id, ok := m.Interface().(*irv1.Identifier); ok {
		id.Words = naming.Words(id.GetStored())
		return
	}
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case fd.Message() == nil || fd.IsMap():
		case fd.IsList():
			for i, l := 0, v.List(); i < l.Len(); i++ {
				fillWords(l.Get(i).Message())
			}
		default:
			fillWords(v.Message())
		}
		return true
	})
}

// splitWordValues turns a words annotation written as a string into its
// words: @inverse-name(user_profile) in a comment is the word list [user,
// profile] (§7.6), because only the core splits words (§4.2). It does the
// same for the name and inverse-name fields of a relation annotation (§5.3),
// and for every superseded annotation. Other values stay as written; a value
// of the wrong form is annotate.Check's to report.
func splitWordValues(a *irv1.Annotation) {
	for _, s := range a.GetSuperseded() {
		splitWordValues(s)
	}
	k, ok := annotate.Lookup(a.GetKey())
	switch {
	case !ok:
	case k.Form == annotate.FormWords:
		if s, isString := a.GetValue().GetKind().(*irv1.AnnotationValue_String_); isString {
			a.Value = &irv1.AnnotationValue{Kind: &irv1.AnnotationValue_Words{Words: wordList(s.String_)}}
		}
	case k.Form == annotate.FormStruct:
		fields := a.GetValue().GetStruct().GetFields()
		for _, f := range []string{"name", "inverse-name"} {
			if s, isString := fields[f].GetKind().(*structpb.Value_StringValue); isString {
				fields[f] = structpb.NewListValue(wordList(s.StringValue))
			}
		}
	}
}

func wordList(s string) *structpb.ListValue {
	l := &structpb.ListValue{}
	for _, w := range naming.Words(s) {
		l.Values = append(l.Values, structpb.NewStringValue(w))
	}
	return l
}
