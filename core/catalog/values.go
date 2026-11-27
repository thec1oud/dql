package catalog

import (
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/thec1oud/dql/core/annotate"
	"github.com/thec1oud/dql/core/diagnostics"
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

// typeValues reads the values that sources write as text by their key's form
// (§7.1, §7.4): a source keeps a comment's value as written, a string, and
// only the core knows each key's form. A words value is split into its words:
// @inverse-name(user_profile) is the word list [user, profile] (§7.6),
// because only the core splits words (§4.2). A list value is read with
// annotate.ParseList, @logical-key(tenant_id, slug), and a struct value with
// annotate.ParseStruct, @relation(to: shop.users.id, name: user), whose name
// and inverse-name fields are then split into words as well (§5.3); so are
// those of a struct that config wrote. Superseded annotations are read the
// same way.
//
// typeValues returns the annotations whose text could not be read, with a
// diagnostic for each, and leaves them out: their values are unknown, so
// they would only produce a second, less precise diagnostic. Every other
// value stays as written; a value of the wrong form is annotate.Check's to
// report.
func typeValues(target *irv1.QualifiedName, anns []*irv1.Annotation) ([]*irv1.Annotation, []*irv1.Diagnostic) {
	var ds []*irv1.Diagnostic
	kept := anns[:0:0]
	for _, a := range anns {
		var sub []*irv1.Diagnostic
		a.Superseded, sub = typeValues(target, a.GetSuperseded())
		ds = append(ds, sub...)
		if err := typeValue(a); err != nil {
			ds = append(ds, diagnostics.Errorf(annotate.CodeValueMalformed, target,
				"%s on %s, read at %s, is written %q, which is not a %s value: %v (§7.4)",
				a.GetKey(), diagnostics.Name(target), a.GetOrigin().GetLocation(), a.GetValue().GetString_(), formName(a.GetKey()), err))
			continue
		}
		kept = append(kept, a)
	}
	return kept, ds
}

func typeValue(a *irv1.Annotation) error {
	k, ok := annotate.Lookup(a.GetKey())
	s, isString := a.GetValue().GetKind().(*irv1.AnnotationValue_String_)
	switch {
	case !ok:
	case k.Form == annotate.FormWords && isString:
		a.Value = &irv1.AnnotationValue{Kind: &irv1.AnnotationValue_Words{Words: wordList(s.String_)}}
	case k.Form == annotate.FormList && isString:
		items, err := annotate.ParseList(s.String_)
		if err != nil {
			return err
		}
		a.Value = &irv1.AnnotationValue{Kind: &irv1.AnnotationValue_List{List: itemList(items)}}
	case k.Form == annotate.FormStruct:
		if isString {
			fields, err := annotate.ParseStruct(s.String_)
			if err != nil {
				return err
			}
			st := &structpb.Struct{Fields: map[string]*structpb.Value{}}
			for f, v := range fields {
				st.Fields[f] = structpb.NewStringValue(v)
			}
			a.Value = &irv1.AnnotationValue{Kind: &irv1.AnnotationValue_Struct{Struct: st}}
		}
		fields := a.GetValue().GetStruct().GetFields()
		for _, f := range []string{"name", "inverse-name"} {
			if s, isString := fields[f].GetKind().(*structpb.Value_StringValue); isString {
				fields[f] = structpb.NewListValue(wordList(s.StringValue))
			}
		}
	}
	return nil
}

// formName names the form of a key whose text typeValue could not read: a
// list or a struct.
func formName(key string) string {
	if k, _ := annotate.Lookup(key); k.Form == annotate.FormList {
		return "list"
	}
	return "struct"
}

func wordList(s string) *structpb.ListValue { return itemList(naming.Words(s)) }

func itemList(items []string) *structpb.ListValue {
	l := &structpb.ListValue{}
	for _, it := range items {
		l.Values = append(l.Values, structpb.NewStringValue(it))
	}
	return l
}
