package postgres

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"google.golang.org/protobuf/types/known/durationpb"
)

// block is one query of a query file, before parsing: its headers and its
// statement (§17.1).
type block struct {
	name        string
	cardinality irv1.Cardinality
	returns     string
	nonnull     []string
	consistency *irv1.Consistency
	unscoped    bool
	// Where the -- name: line is, as path:line.
	location string
	// The statement, without headers, surrounding space or a final ;.
	body string
	// The 1-based line the statement starts on.
	line int
}

var (
	nameLine   = regexp.MustCompile(`^--\s*name:\s*(\S*)\s*(\S*)\s*$`)
	headerLine = regexp.MustCompile(`^--\s*(returns|nonnull|consistency):(.*)$`)
	unscoped   = regexp.MustCompile(`^--\s*unscoped\s*$`)
	identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
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

// split cuts a query file into its queries. Each starts at a -- name: line;
// the comment lines right after it hold its headers, and other comments
// there are ignored, as sqlc does. Text before the first query other than
// comments is an error. Problems are returned as diagnostics, and a block
// with a bad header is left out.
func split(f *irv1.QueryFile) ([]*block, []*irv1.Diagnostic) {
	var (
		blocks []*block
		ds     []*irv1.Diagnostic
		cur    *block
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
		cur.line += strings.Count(joined[:len(joined)-len(trimmed)], "\n")
		cur.body = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(trimmed), ";"))
		if cur.body == "" {
			ds = append(ds, fileError("query-header", cur.name, "%s: query %s has no statement", cur.location, cur.name))
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
			cur, bad, body, inHead = &block{name: m[1], location: at}, false, nil, true
			c, ok := cardinalities[m[2]]
			switch {
			case !identifier.MatchString(m[1]):
				ds = append(ds, fileError("query-header", m[1], "%s: %q is not a query name; write -- name: <Name> :<cardinality>", at, m[1]))
				bad = true
			case !ok:
				ds = append(ds, fileError("query-header", m[1], "%s: query %s has cardinality %q; use :one, :optional, :many, :count, :exec or :execrows", at, m[1], m[2]))
				bad = true
			}
			cur.cardinality = c
			continue
		}
		if cur == nil {
			if line != "" && !strings.HasPrefix(line, "--") && !stray {
				ds = append(ds, fileError("query-unnamed", "", "%s: a statement before the first -- name: line; start each query with -- name: <Name> :<cardinality>", at))
				stray = true
			}
			continue
		}
		if inHead && strings.HasPrefix(line, "--") {
			if err := cur.header(line); err != "" {
				ds = append(ds, fileError("query-header", cur.name, "%s: query %s: %s", at, cur.name, err))
				bad = true
			}
			continue
		}
		if inHead {
			inHead = false
			cur.line = n
		}
		body = append(body, raw)
	}
	finish()
	return blocks, ds
}

// header reads one header line into b, and returns what is wrong with it.
// Comment lines that are not headers are ignored.
func (b *block) header(line string) string {
	if unscoped.MatchString(line) {
		if b.unscoped {
			return "-- unscoped is given twice"
		}
		b.unscoped = true
		return ""
	}
	m := headerLine.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	key, value := m[1], strings.TrimSpace(m[2])
	switch key {
	case "returns":
		if b.returns != "" {
			return "-- returns: is given twice"
		}
		if !shapeName.MatchString(value) {
			return fmt.Sprintf("-- returns: %q is not a shape name, such as user.full", value)
		}
		b.returns = value
	case "nonnull":
		if b.nonnull != nil {
			return "-- nonnull: is given twice"
		}
		b.nonnull = []string{}
		for _, c := range strings.Split(value, ",") {
			c = strings.TrimSpace(c)
			if !identifier.MatchString(c) {
				return fmt.Sprintf("-- nonnull: %q is not a column name; list result columns separated by commas", c)
			}
			b.nonnull = append(b.nonnull, c)
		}
	default:
		if b.consistency != nil {
			return "-- consistency: is given twice"
		}
		c, err := consistency(value)
		if err != "" {
			return err
		}
		b.consistency = c
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

// fileError is an error diagnostic about the query named q, if any.
func fileError(code, q, format string, args ...any) *irv1.Diagnostic {
	return &irv1.Diagnostic{Severity: irv1.Severity_SEVERITY_ERROR, Code: code, Query: q, Message: fmt.Sprintf(format, args...)}
}
