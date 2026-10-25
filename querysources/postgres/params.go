package postgres

import (
	"fmt"
	"slices"
	"strings"
)

// A paramKind is how a query file writes a parameter (§17.1).
type paramKind int

const (
	// @name or sqlc.arg(name).
	argParam paramKind = iota
	// sqlc.narg(name): nullable.
	nargParam
	// sqlc.slice(name): a list in one placeholder.
	sliceParam
	// @scope.<column>.
	scopeParam
)

var kindNames = [...]string{"sqlc.arg", "sqlc.narg", "sqlc.slice", "@scope"}

// rawParam is a parameter as the text writes it.
type rawParam struct {
	name string
	kind paramKind
	// Byte offsets of the written form in the statement.
	start, end int
}

// rewritten is a statement with its parameters replaced by placeholders.
type rewritten struct {
	text string
	// The parameters by placeholder number - 1: first the query's
	// parameters in the order they first appear, then the scope columns,
	// sorted.
	params []rawParam
	// shifts maps offsets in text back to the statement: an offset at or
	// after shifts[i].at moves by shifts[i].by.
	shifts []shift
}

type shift struct{ at, by int }

// original returns the statement offset of an offset in r.text; -1, for
// none, stays -1.
func (r *rewritten) original(off int) int {
	if off < 0 {
		return off
	}
	by := 0
	for _, s := range r.shifts {
		if off >= s.at {
			by = s.by
		}
	}
	return off + by
}

// rewrite finds the parameters of stmt and replaces each with its $n
// placeholder (§17.1). It skips string literals, quoted identifiers,
// dollar-quoted strings and comments. A parameter written two ways, such
// as @id and sqlc.narg(id), is an error.
func rewrite(stmt string) (*rewritten, error) {
	found, err := scan(stmt)
	if err != nil {
		return nil, err
	}
	var params, scopes []rawParam
	seen := map[string]rawParam{}
	for _, p := range found {
		key := p.name
		if p.kind == scopeParam {
			key = "@scope." + p.name
		}
		if q, ok := seen[key]; ok {
			if q.kind != p.kind {
				return nil, fmt.Errorf("parameter %s is written both as %s and as %s; write it one way", p.name, kindNames[q.kind], kindNames[p.kind])
			}
			continue
		}
		seen[key] = p
		if p.kind == scopeParam {
			scopes = append(scopes, p)
		} else {
			params = append(params, p)
		}
	}
	slices.SortFunc(scopes, func(a, b rawParam) int { return strings.Compare(a.name, b.name) })
	all := append(params, scopes...)
	number := map[string]int{}
	for i, p := range all {
		number[key(p)] = i + 1
	}
	r := &rewritten{params: all}
	var b strings.Builder
	last := 0
	for _, p := range found {
		b.WriteString(stmt[last:p.start])
		ph := fmt.Sprintf("$%d", number[key(p)])
		b.WriteString(ph)
		last = p.end
		r.shifts = append(r.shifts, shift{at: b.Len(), by: p.end - b.Len()})
	}
	b.WriteString(stmt[last:])
	r.text = b.String()
	return r, nil
}

func key(p rawParam) string {
	if p.kind == scopeParam {
		return "@scope." + p.name
	}
	return p.name
}

// scan returns the parameters of stmt in the order they appear.
func scan(s string) ([]rawParam, error) {
	var out []rawParam
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\'':
			i = skipQuoted(s, i, '\'', i > 0 && (s[i-1] == 'E' || s[i-1] == 'e'))
		case c == '"':
			i = skipQuoted(s, i, '"', false)
		case c == '-' && strings.HasPrefix(s[i:], "--"):
			if j := strings.IndexByte(s[i:], '\n'); j >= 0 {
				i += j
			} else {
				i = len(s)
			}
		case c == '/' && strings.HasPrefix(s[i:], "/*"):
			i = skipBlockComment(s, i)
		case c == '$' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9':
			return nil, fmt.Errorf("positional parameter %s; name it with @name or sqlc.arg(name)", s[i:identEnd(s, i+1)])
		case c == '$':
			i = skipDollar(s, i)
		case c == '@' && i+1 < len(s) && identStart(s[i+1]) && (i == 0 || (s[i-1] != '<' && s[i-1] != '@')):
			j := identEnd(s, i+1)
			name := s[i+1 : j]
			if name == "scope" && j < len(s) && s[j] == '.' {
				if j+1 < len(s) && identStart(s[j+1]) {
					k := identEnd(s, j+1)
					out = append(out, rawParam{name: s[j+1 : k], kind: scopeParam, start: i, end: k})
					i = k
					continue
				}
				return nil, fmt.Errorf("@scope. needs a column name, as in @scope.tenant_id")
			}
			out = append(out, rawParam{name: name, kind: argParam, start: i, end: j})
			i = j
		case identStart(c) && (i == 0 || !identChar(s[i-1])):
			j := identEnd(s, i)
			if s[i:j] != "sqlc" || j >= len(s) || s[j] != '.' {
				i = j
				continue
			}
			p, err := sqlcCall(s, i, j+1)
			if err != nil {
				return nil, err
			}
			out = append(out, p)
			i = p.end
		default:
			i++
		}
	}
	return out, nil
}

var sqlcKinds = map[string]paramKind{"arg": argParam, "narg": nargParam, "slice": sliceParam}

// sqlcCall reads sqlc.<fn>(<name>) starting at start, whose fn starts at
// at. The name may be written bare or in single quotes.
func sqlcCall(s string, start, at int) (rawParam, error) {
	j := identEnd(s, at)
	kind, ok := sqlcKinds[s[at:j]]
	if !ok {
		return rawParam{}, fmt.Errorf("sqlc.%s is not a parameter; use sqlc.arg, sqlc.narg or sqlc.slice", s[at:j])
	}
	rest := s[j:]
	trimmed := strings.TrimLeft(rest, " \t\r\n")
	if !strings.HasPrefix(trimmed, "(") {
		return rawParam{}, fmt.Errorf("sqlc.%s takes a parameter name in parentheses", s[at:j])
	}
	inner := trimmed[1:]
	close := strings.IndexByte(inner, ')')
	if close < 0 {
		return rawParam{}, fmt.Errorf("sqlc.%s takes a parameter name in parentheses", s[at:j])
	}
	name := strings.TrimSpace(inner[:close])
	if len(name) >= 2 && name[0] == '\'' && name[len(name)-1] == '\'' {
		name = name[1 : len(name)-1]
	}
	if !identifier.MatchString(name) {
		return rawParam{}, fmt.Errorf("sqlc.%s(%s): %q is not a parameter name", s[at:j], inner[:close], name)
	}
	end := len(s) - len(inner) + close + 1
	return rawParam{name: name, kind: kind, start: start, end: end}, nil
}

// skipQuoted returns the offset after the quoted text starting at i. A
// doubled quote stays inside; so does a backslash escape when escapes is
// set (E'...'). Unterminated text runs to the end, and the parser reports it.
func skipQuoted(s string, i int, q byte, escapes bool) int {
	for j := i + 1; j < len(s); j++ {
		switch {
		case escapes && s[j] == '\\':
			j++
		case s[j] == q && j+1 < len(s) && s[j+1] == q:
			j++
		case s[j] == q:
			return j + 1
		}
	}
	return len(s)
}

// skipBlockComment returns the offset after the comment starting at i,
// which may nest.
func skipBlockComment(s string, i int) int {
	depth := 0
	for j := i; j < len(s)-1; j++ {
		switch {
		case s[j] == '/' && s[j+1] == '*':
			depth++
			j++
		case s[j] == '*' && s[j+1] == '/':
			depth--
			j++
			if depth == 0 {
				return j + 1
			}
		}
	}
	return len(s)
}

// skipDollar returns the offset after a dollar-quoted string starting at
// i, or after the $ when it starts none (a placeholder such as $1).
func skipDollar(s string, i int) int {
	j := i + 1
	for j < len(s) && identChar(s[j]) && !(j == i+1 && s[j] >= '0' && s[j] <= '9') {
		j++
	}
	if j >= len(s) || s[j] != '$' {
		return i + 1
	}
	tag := s[i : j+1]
	if k := strings.Index(s[j+1:], tag); k >= 0 {
		return j + 1 + k + len(tag)
	}
	return len(s)
}

func identStart(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func identChar(c byte) bool { return identStart(c) || c >= '0' && c <= '9' }

func identEnd(s string, i int) int {
	for i < len(s) && identChar(s[i]) {
		i++
	}
	return i
}
