// Package queryfile reads query files in sqlc's conventions: it cuts a
// file into its queries and reads each one's headers (design record
// §17.1). Every dialect's query-source plugin shares it; what a statement
// means is the dialect's own.
package queryfile

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"google.golang.org/protobuf/types/known/durationpb"
)

// Block is one query of a query file, before parsing: its headers and its
// statement (§17.1).
type Block struct {
	Name        string
	Cardinality irv1.Cardinality
	Returns     string
	Nonnull     []string
	Consistency *irv1.Consistency
	Unscoped    bool
	// Where the -- name: line is, as path:line.
	Location string
	// The statement, without headers, surrounding space or a final ;.
	Body string
	// The 1-based line the statement starts on.
	Line int
}

var (
	nameLine   = regexp.MustCompile(`^--\s*name:\s*(\S*)\s*(\S*)\s*$`)
	headerLine = regexp.MustCompile(`^--\s*(returns|nonnull|consistency):(.*)$`)
	unscoped   = regexp.MustCompile(`^--\s*unscoped\s*$`)
	// Identifier matches a query, parameter or column name.
	Identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	shapeName  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)
)

var cardinalities = map[string]irv1.Cardinality{
	":one":      irv1.Cardinality_CARDINALITY_ONE,
	":optional": irv1.Cardinality_CARDINALITY_OPTIONAL,
	":many":     irv1.Cardinality_CARDINALITY_MANY,
	":count":    irv1.Cardinality_CARDINALITY_COUNT,
	":exec":     irv1.Cardinality_CARDINALITY_EXEC,
	":execrows": irv1.Cardinality_CARDINALITY_EXEC_ROWS,
}

var levels = map[string]irv1.ConsistencyLevel{
	"primary":          irv1.ConsistencyLevel_CONSISTENCY_PRIMARY,
	"any":              irv1.ConsistencyLevel_CONSISTENCY_ANY,
	"read-your-writes": irv1.ConsistencyLevel_CONSISTENCY_READ_YOUR_WRITES,
}

// Split cuts a query file into its queries. Each starts at a -- name: line;
// the comment lines right after it hold its headers, and other comments
// there are ignored, as sqlc does. Text before the first query other than
// comments is an error. Problems are returned as diagnostics, and a block
// with a bad header is left out.
func Split(f *irv1.QueryFile) ([]*Block, []*irv1.Diagnostic) {
	var (
		blocks []*Block
		ds     []*irv1.Diagnostic
		cur    *Block
		bad    bool
		body   []string
		inHead bool
		stray  bool
	)
	finish := func() {
		if cur == nil {
			return
		}
		joined := strings.Join(body, "\n")
		trimmed := strings.TrimLeft(joined, " \t\r\n")
		cur.Line += strings.Count(joined[:len(joined)-len(trimmed)], "\n")
		cur.Body = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(trimmed), ";"))
		if cur.Body == "" {
			ds = append(ds, Error("query-header", cur.Name, "%s: query %s has no statement", cur.Location, cur.Name))
			bad = true
		}
		if !bad {
			blocks = append(blocks, cur)
		}
	}
	for i, raw := range strings.Split(f.GetText(), "\n") {
		n := i + 1
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		at := fmt.Sprintf("%s:%d", f.GetPath(), n)
		if m := nameLine.FindStringSubmatch(line); m != nil {
			finish()
			cur, bad, body, inHead = &Block{Name: m[1], Location: at}, false, nil, true
			c, ok := cardinalities[m[2]]
			switch {
			case !Identifier.MatchString(m[1]):
				ds = append(ds, Error("query-header", m[1], "%s: %q is not a query name; write -- name: <Name> :<cardinality>", at, m[1]))
				bad = true
			case !ok:
				ds = append(ds, Error("query-header", m[1], "%s: query %s has cardinality %q; use :one, :optional, :many, :count, :exec or :execrows", at, m[1], m[2]))
				bad = true
			}
			cur.Cardinality = c
			continue
		}
		if cur == nil {
			if line != "" && !strings.HasPrefix(line, "--") && !stray {
				ds = append(ds, Error("query-unnamed", "", "%s: a statement before the first -- name: line; start each query with -- name: <Name> :<cardinality>", at))
				stray = true
			}
			continue
		}
		if inHead && strings.HasPrefix(line, "--") {
			if err := cur.header(line); err != "" {
				ds = append(ds, Error("query-header", cur.Name, "%s: query %s: %s", at, cur.Name, err))
				bad = true
			}
			continue
		}
		if inHead {
			inHead = false
			cur.Line = n
		}
		body = append(body, raw)
	}
	finish()
	return blocks, ds
}

// header reads one header line into b, and returns what is wrong with it.
// Comment lines that are not headers are ignored.
func (b *Block) header(line string) string {
	if unscoped.MatchString(line) {
		if b.Unscoped {
			return "-- unscoped is given twice"
		}
		b.Unscoped = true
		return ""
	}
	m := headerLine.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	key, value := m[1], strings.TrimSpace(m[2])
	switch key {
	case "returns":
		if b.Returns != "" {
			return "-- returns: is given twice"
		}
		if !shapeName.MatchString(value) {
			return fmt.Sprintf("-- returns: %q is not a shape name, such as user.full", value)
		}
		b.Returns = value
	case "nonnull":
		if b.Nonnull != nil {
			return "-- nonnull: is given twice"
		}
		b.Nonnull = []string{}
		for _, c := range strings.Split(value, ",") {
			c = strings.TrimSpace(c)
			if !Identifier.MatchString(c) {
				return fmt.Sprintf("-- nonnull: %q is not a column name; list result columns separated by commas", c)
			}
			b.Nonnull = append(b.Nonnull, c)
		}
	default:
		if b.Consistency != nil {
			return "-- consistency: is given twice"
		}
		c, err := consistency(value)
		if err != "" {
			return err
		}
		b.Consistency = c
	}
	return ""
}

// consistency reads a -- consistency: value: primary, any,
// read-your-writes or bounded-staleness <duration> (§22.5).
func consistency(v string) (*irv1.Consistency, string) {
	if l, ok := levels[v]; ok {
		return &irv1.Consistency{Level: l}, ""
	}
	if d, ok := strings.CutPrefix(v, "bounded-staleness"); ok && strings.TrimSpace(d) != d {
		max, err := time.ParseDuration(strings.TrimSpace(d))
		if err != nil || max <= 0 {
			return nil, fmt.Sprintf("-- consistency: bounded-staleness takes a positive duration, such as 5s, not %q", strings.TrimSpace(d))
		}
		return &irv1.Consistency{Level: irv1.ConsistencyLevel_CONSISTENCY_BOUNDED_STALENESS, MaxStaleness: durationpb.New(max)}, ""
	}
	return nil, fmt.Sprintf("-- consistency: %q is not a level; use primary, any, read-your-writes or bounded-staleness <duration>", v)
}

// Error is an error diagnostic about the query named q, if any.
func Error(code, q, format string, args ...any) *irv1.Diagnostic {
	return &irv1.Diagnostic{Severity: irv1.Severity_SEVERITY_ERROR, Code: code, Query: q, Message: fmt.Sprintf(format, args...)}
}

// At returns where offset off of the statement is, as path:line:column,
// or the -- name: line for -1.
func (b *Block) At(path string, off int) string {
	if off < 0 {
		return b.Location
	}
	off = min(off, len(b.Body))
	before := b.Body[:off]
	line := b.Line + strings.Count(before, "\n")
	col := off - strings.LastIndexByte(before, '\n')
	return fmt.Sprintf("%s:%d:%d", path, line, col)
}
