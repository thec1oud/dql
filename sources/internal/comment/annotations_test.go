package comment

import (
	"testing"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func TestAnnotations(t *testing.T) {
	const loc = "COMMENT ON COLUMN app.users.email"
	anns, unclosed := Annotations("@x.go.type(Email) @write-only @logical-key(id) @sensitive @logical-key( a, b ) @name(",
		"pg-live", loc)
	origin := &irv1.Origin{Kind: irv1.OriginKind_ORIGIN_DB_COMMENT, Plugin: "pg-live", Location: loc}
	str := func(s string) *irv1.AnnotationValue {
		return &irv1.AnnotationValue{Kind: &irv1.AnnotationValue_String_{String_: s}}
	}
	want := []*irv1.Annotation{
		{Key: "core.logical-key", Value: str("id"), Origin: origin},
		{Key: "core.logical-key", Value: str(" a, b "), Origin: origin},
		{Key: "core.sensitive", Origin: origin},
		{Key: "core.write-only", Origin: origin},
		{Key: "x.go.type", Value: str("Email"), Origin: origin},
	}
	if len(anns) != len(want) {
		t.Fatalf("got %d annotations, want %d: %v", len(anns), len(want), anns)
	}
	for i := range want {
		if !proto.Equal(anns[i], want[i]) {
			t.Errorf("annotation %d:\n got %v\nwant %v", i, anns[i], want[i])
		}
	}
	if len(unclosed) != 1 || unclosed[0].Key != "name" {
		t.Errorf("unclosed = %+v, want the @name token", unclosed)
	}
}

func TestMarked(t *testing.T) {
	for _, c := range []struct {
		text string
		want bool
	}{
		{"@public", true},
		{"Scores a post. @public.", true},
		{"@public(", false},
		{"@publicly", false},
		{"", false},
	} {
		if got := Marked(c.text, "public"); got != c.want {
			t.Errorf("Marked(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}
