package facts

import (
	"regexp"

	"google.golang.org/protobuf/types/known/structpb"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Core annotation keys this package reads (§7.2).
const (
	keyLogicalKey       = "core.logical-key"
	keyRelationName     = "core.relation-name"
	keyInverseName      = "core.inverse-name"
	keyRelation         = "core.relation"
	keyCollectionPolicy = "core.collection-policy"
	keyRemoval          = "core.removal"
)

// find returns the effective annotation with key, or nil. Precedence has
// already been applied (§7.1), so there is at most one per key.
func find(as []*irv1.Annotation, key string) *irv1.Annotation {
	for _, a := range as {
		if a.GetKey() == key {
			return a
		}
	}
	return nil
}

// singleWord matches a string that word splitting (§4.2) leaves as one word:
// lower-case letters, then digits that stay attached to them. Such a string
// is its own word list, so this package needs no word splitter.
var singleWord = regexp.MustCompile(`^[a-z]+[0-9]*$`)

// valueWords reads a words value: a list of non-empty strings, or a string
// that is a single word.
func valueWords(v *irv1.AnnotationValue) ([]string, bool) {
	if l := v.GetWords(); l != nil {
		return stringList(l)
	}
	if s, ok := v.GetKind().(*irv1.AnnotationValue_String_); ok && singleWord.MatchString(s.String_) {
		return []string{s.String_}, true
	}
	return nil, false
}

// structWords reads a words value inside a struct annotation.
func structWords(v *structpb.Value) ([]string, bool) {
	switch k := v.GetKind().(type) {
	case *structpb.Value_ListValue:
		return stringList(k.ListValue)
	case *structpb.Value_StringValue:
		if singleWord.MatchString(k.StringValue) {
			return []string{k.StringValue}, true
		}
	}
	return nil, false
}

// valueColumns reads a list of column names, or a single column name.
func valueColumns(v *irv1.AnnotationValue) ([]string, bool) {
	switch k := v.GetKind().(type) {
	case *irv1.AnnotationValue_List:
		return stringList(k.List)
	case *irv1.AnnotationValue_Words:
		return stringList(k.Words)
	case *irv1.AnnotationValue_String_:
		if k.String_ != "" {
			return []string{k.String_}, true
		}
	}
	return nil, false
}

func stringList(l *structpb.ListValue) ([]string, bool) {
	var out []string
	for _, v := range l.GetValues() {
		s, ok := v.GetKind().(*structpb.Value_StringValue)
		if !ok || s.StringValue == "" {
			return nil, false
		}
		out = append(out, s.StringValue)
	}
	return out, len(out) > 0
}
