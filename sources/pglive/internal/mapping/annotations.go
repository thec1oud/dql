package mapping

import (
	"fmt"
	"slices"
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/internal/comment"
)

// Diagnostic codes. They are stable: tools and tests match on them.
const (
	// An annotation value whose parenthesis is never closed. The annotation
	// is not read, since what follows might belong to another one.
	CodeAnnotationUnclosed = "pglive-annotation-unclosed"
	// Annotations in a comment pg-live does not read, such as an index's or a
	// trigger's, which carry none in the IR. The comment is still kept whole.
	CodeAnnotationIgnored = "pglive-annotation-ignored"
)

// annotations reads the @key and @key(value) tokens of an object's comment
// (§7.1). Core keys are written without their prefix in comments and get
// core. in the IR; plugin keys, x.<plugin>.<key>, keep theirs (§7.2). A value
// is kept as written, as a string: splitting it into words or a list belongs
// to the core, which knows each key's form. Every annotation's origin names
// pg-live and the COMMENT statement that set it (§7.5). The result is sorted
// by key, repeated keys in comment order.
func (m *mapper) annotations(target *irv1.QualifiedName, location, text string) []*irv1.Annotation {
	var out []*irv1.Annotation
	for _, tok := range comment.ParseComment(text) {
		if tok.Unclosed {
			m.diag(irv1.Severity_SEVERITY_ERROR, CodeAnnotationUnclosed, target, fmt.Sprintf(
				"%s: the value of @%s has no closing parenthesis, so it is not read; close it", location, tok.Key))
			continue
		}
		a := &irv1.Annotation{
			Key:    irKey(tok.Key),
			Origin: &irv1.Origin{Kind: irv1.OriginKind_ORIGIN_DB_COMMENT, Plugin: Source, Location: location},
		}
		if tok.HasValue {
			a.Value = &irv1.AnnotationValue{Kind: &irv1.AnnotationValue_String_{String_: tok.Value}}
		}
		out = append(out, a)
	}
	slices.SortStableFunc(out, func(a, b *irv1.Annotation) int { return strings.Compare(a.Key, b.Key) })
	return out
}

// ignored reports the annotation tokens of a comment that pg-live does not
// read, saying why. The comment travels whole as an extension.
func (m *mapper) ignored(target *irv1.QualifiedName, location, text, why string) {
	var keys []string
	for _, tok := range comment.ParseComment(text) {
		keys = append(keys, "@"+tok.Key)
	}
	if len(keys) > 0 {
		m.diag(irv1.Severity_SEVERITY_WARNING, CodeAnnotationIgnored, target, fmt.Sprintf(
			"%s: %s, so %s is ignored; remove the annotation tokens from the comment",
			location, why, strings.Join(keys, " ")))
	}
}

func (m *mapper) diag(sev irv1.Severity, code string, target *irv1.QualifiedName, msg string) {
	m.diags = append(m.diags, &irv1.Diagnostic{Severity: sev, Code: code, Target: target, Message: msg})
}

// irKey spells a key as the IR does (§7.2).
func irKey(written string) string {
	if strings.HasPrefix(written, "x.") {
		return written
	}
	return "core." + written
}
