package sqlite

import (
	"fmt"
	"slices"
	"strings"

	"github.com/thec1oud/dql/querysources/queryfile"
)

// A paramKind is how a query file writes a parameter (§17.1).
type paramKind int

const (
	// @name or sqlc.arg(name).
	argParam paramKind = iota
	// sqlc.narg(name): nullable.
	nargParam
	// sqlc.slice(name): a list, written IN (sqlc.slice(name)).
	sliceParam
	// @scope.<column>.
	scopeParam
)

var kindNames = [...]string{"sqlc.arg", "sqlc.narg", "sqlc.slice", "@scope"}

// rawParam is a parameter as the text writes it.
type rawParam struct {
	name string
	kind paramKind
	// Byte offsets of the written form in the statement: for a list, of
	// the parenthesized (sqlc.slice(name)) the IN takes.
	start, end int
}

func (p rawParam) key() string {
	if p.kind == scopeParam {
		return "@scope." + p.name
	}
	return p.name
}

// rewritten is a statement with its parameters replaced by ? placeholders,
// which SQLite numbers by position.
type rewritten struct {
	// The statement to parse: a list is (?) there.
	text string
	// The parameters: first the query's parameters in the order they
	// first appear, then the scope columns, sorted.
	params []rawParam
	// The parameter each placeholder takes, in order, by its index in
	// params; and where in text each one is.
	uses, offsets []int
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

// use returns the placeholder at offset off of r.text by its position
// among the placeholders.
func (r *rewritten) use(off int) int { return slices.Index(r.offsets, off) }

// rewrite finds the parameters of stmt and replaces each with a ?
// placeholder (§17.1). It skips string literals, quoted identifiers and
// comments. A parameter written two ways, such as @id and sqlc.narg(id), is
// an error, and so is one of SQLite's own: ?, ?NNN, :name or $name.
func rewrite(stmt string) (*rewritten, error) {
	found, err := scan(stmt)
	if err != nil {
		return nil, err
	}
	var params, scopes []rawParam
	seen := map[string]rawParam{}
	for _, p := range found {
		if q, ok := seen[p.key()]; ok {
			if q.kind != p.kind {
				return nil, fmt.Errorf("parameter %s is written both as %s and as %s; write it one way", p.name, kindNames[q.kind], kindNames[p.kind])
			}
			continue
		}
		seen[p.key()] = p
		if p.kind == scopeParam {
			scopes = append(scopes, p)
		} else {
			params = append(params, p)
		}
	}
	slices.SortFunc(scopes, func(a, b rawParam) int { return strings.Compare(a.name, b.name) })
	all := append(params, scopes...)
	index := map[string]int{}
	for i, p := range all {
		index[p.key()] = i
	}
	r := &rewritten{params: all}
	var b strings.Builder
	last := 0
	for _, p := range found {
		b.WriteString(stmt[last:p.start])
		if p.kind == sliceParam {
			b.WriteByte('(')
		}
		r.uses = append(r.uses, index[p.key()])
		r.offsets = append(r.offsets, b.Len())
		b.WriteByte('?')
		if p.kind == sliceParam {
			b.WriteByte(')')
		}
		last = p.end
		r.shifts = append(r.shifts, shift{at: b.Len(), by: p.end - b.Len()})
	}
	b.WriteString(stmt[last:])
	r.text = b.String()
	return r, nil
}

// scan returns the parameters of stmt in the order they appear.
func scan(s string) ([]rawParam, error) {
	var out []rawParam
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			i = skipQuoted(s, i, c)
		case c == '[':
			if j := strings.IndexByte(s[i:], ']'); j >= 0 {
				i += j + 1
			} else {
				i = len(s)
			}
		case strings.HasPrefix(s[i:], "--"):
			if j := strings.IndexByte(s[i:], '\n'); j >= 0 {
				i += j
			} else {
				i = len(s)
			}
		case strings.HasPrefix(s[i:], "/*"):
			if j := strings.Index(s[i+2:], "*/"); j >= 0 {
				i += j + 4
			} else {
				i = len(s)
			}
		case c == '?':
			return nil, fmt.Errorf("positional parameter ?; name it with @name or sqlc.arg(name)")
		case (c == ':' || c == '$') && i+1 < len(s) && identStart(s[i+1]) && (i == 0 || !identChar(s[i-1])):
			j := identEnd(s, i+1)
			return nil, fmt.Errorf("parameter %s; write it @%s or sqlc.arg(%s)", s[i:j], s[i+1:j], s[i+1:j])
		case c == '@' && i+1 < len(s) && identStart(s[i+1]):
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
			if !strings.EqualFold(s[i:j], "sqlc") || j >= len(s) || s[j] != '.' {
				i = j
				continue
			}
			p, err := sqlcCall(s, i, j+1)
			if err != nil {
				return nil, err
			}
			if p.kind == sliceParam {
				if p, err = list(s, p); err != nil {
					return nil, err
				}
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
	fn := s[at:j]
	kind, ok := sqlcKinds[strings.ToLower(fn)]
	if !ok {
		return rawParam{}, fmt.Errorf("sqlc.%s is not a parameter; use sqlc.arg, sqlc.narg or sqlc.slice", fn)
	}
	trimmed := strings.TrimLeft(s[j:], " \t\r\n")
	if !strings.HasPrefix(trimmed, "(") {
		return rawParam{}, fmt.Errorf("sqlc.%s takes a parameter name in parentheses", fn)
	}
	inner := trimmed[1:]
	close := strings.IndexByte(inner, ')')
	if close < 0 {
		return rawParam{}, fmt.Errorf("sqlc.%s takes a parameter name in parentheses", fn)
	}
	name := strings.TrimSpace(inner[:close])
	if len(name) >= 2 && name[0] == '\'' && name[len(name)-1] == '\'' {
		name = name[1 : len(name)-1]
	}
	if !queryfile.Identifier.MatchString(name) {
		return rawParam{}, fmt.Errorf("sqlc.%s(%s): %q is not a parameter name", fn, inner[:close], name)
	}
	end := len(s) - len(inner) + close + 1
	return rawParam{name: name, kind: kind, start: start, end: end}, nil
}

// list widens a list parameter to the parentheses around it, which must
// be an IN's: SQLite has no array type, so the list is a JSON array the IN
// expands (§25.3).
func list(s string, p rawParam) (rawParam, error) {
	before := strings.TrimRight(s[:p.start], " \t\r\n")
	after := strings.TrimLeft(s[p.end:], " \t\r\n")
	open := len(before) - 1
	kw := strings.TrimRight(before[:max(open, 0)], " \t\r\n")
	if open < 0 || before[open] != '(' || !strings.HasPrefix(after, ")") ||
		len(kw) < 2 || !strings.EqualFold(kw[len(kw)-2:], "in") || len(kw) > 2 && identChar(kw[len(kw)-3]) {
		return rawParam{}, fmt.Errorf("on SQLite a list parameter is written IN (sqlc.slice(%s))", p.name)
	}
	p.start, p.end = open, len(s)-len(after)+1
	return p, nil
}

// skipQuoted returns the offset after the quoted text starting at i, in
// which a doubled quote stays inside; SQLite has no backslash escapes.
// Unterminated text runs to the end, and the parser reports it.
func skipQuoted(s string, i int, q byte) int {
	for j := i + 1; j < len(s); j++ {
		switch {
		case s[j] == q && j+1 < len(s) && s[j+1] == q:
			j++
		case s[j] == q:
			return j + 1
		}
	}
	return len(s)
}

func identStart(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func identChar(c byte) bool { return identStart(c) || c >= '0' && c <= '9' || c == '$' }

func identEnd(s string, i int) int {
	for i < len(s) && identChar(s[i]) {
		i++
	}
	return i
}
