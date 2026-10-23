package mapping

import (
	"fmt"
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

// annotations reads the annotations of an object's comment (§7.1, §7.5),
// reporting each token whose value is never closed.
func (m *mapper) annotations(target *irv1.QualifiedName, location, text string) []*irv1.Annotation {
	out, unclosed := comment.Annotations(text, Source, location)
	for _, tok := range unclosed {
		m.diag(irv1.Severity_SEVERITY_ERROR, CodeAnnotationUnclosed, target, fmt.Sprintf(
			"%s: the value of @%s has no closing parenthesis, so it is not read; close it", location, tok.Key))
	}
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
