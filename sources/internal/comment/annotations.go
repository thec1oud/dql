package comment

import (
	"slices"
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Annotations reads the @key and @key(value) tokens of a comment as
// annotations, one per token (§7.1). Core keys are written without their
// prefix in comments and get core. in the IR; plugin keys, x.<plugin>.<key>,
// keep theirs (§7.2). A value is kept as written, as a string: splitting it
// into words or a list belongs to the core, which knows each key's form.
// Every annotation's origin names plugin, the source plugin that read it, and
// location, the COMMENT statement that set it (§7.5). The result is sorted by
// key, repeated keys in comment order.
//
// A token whose value is never closed is not read, since what follows might
// belong to another one; it is returned in unclosed, for the caller to
// report.
func Annotations(text, plugin, location string) (out []*irv1.Annotation, unclosed []Token) {
	for _, tok := range ParseComment(text) {
		if tok.Unclosed {
			unclosed = append(unclosed, tok)
			continue
		}
		a := &irv1.Annotation{
			Key:    IRKey(tok.Key),
			Origin: &irv1.Origin{Kind: irv1.OriginKind_ORIGIN_DB_COMMENT, Plugin: plugin, Location: location},
		}
		if tok.HasValue {
			a.Value = &irv1.AnnotationValue{Kind: &irv1.AnnotationValue_String_{String_: tok.Value}}
		}
		out = append(out, a)
	}
	slices.SortStableFunc(out, func(a, b *irv1.Annotation) int { return strings.Compare(a.Key, b.Key) })
	return out, unclosed
}

// IRKey spells a key written in a comment as the IR does (§7.2).
func IRKey(written string) string {
	if strings.HasPrefix(written, "x.") {
		return written
	}
	return "core." + written
}

// Marked reports whether a comment holds the token @key, closed, with or
// without a value.
func Marked(text, key string) bool {
	for _, tok := range ParseComment(text) {
		if tok.Key == key && !tok.Unclosed {
			return true
		}
	}
	return false
}
