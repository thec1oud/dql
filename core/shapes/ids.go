package shapes

import (
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// formWord spells a form in a shape's dotted id.
var formWord = map[irv1.Form]string{
	irv1.Form_FORM_FLAT:   "flat",
	irv1.Form_FORM_NESTS:  "nests",
	irv1.Form_FORM_NESTED: "nested",
}

// Spelling returns the dotted spelling of id, the display and config form of
// a shape id (§14.1): user.draft.flat, user.key, user_summary, and
// post.draft.flat.for.author for a shape qualified by a relation. words are
// the host-name words of the id's entity; a custom id needs none.
func Spelling(id *irv1.ShapeId, words []string) string {
	if id.GetEntity() == nil {
		return id.GetCustom()
	}
	parts := []string{strings.Join(words, "_"), presetWord[id.GetPreset()]}
	if f, ok := formWord[id.GetForm()]; ok {
		parts = append(parts, f)
	}
	if r := id.GetForRelation(); r != "" {
		parts = append(parts, "for", r)
	}
	return strings.Join(parts, ".")
}
