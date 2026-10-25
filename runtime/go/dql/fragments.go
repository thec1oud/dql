package dql

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Placeholder is how a dialect writes the n-th placeholder (§16.2).
type Placeholder int

// The placeholder styles.
const (
	// DollarNumbered writes $1, as Postgres does.
	DollarNumbered Placeholder = iota + 1
	// Question writes ?, as MySQL, MariaDB and SQLite do.
	Question
	// AtNumbered writes @p1, as SQL Server does.
	AtNumbered
)

// Fragments is a dialect's fragment table, generated from the IR (§16.2,
// §18.4): SQL templates by id, with holes written {0} or {name}. The
// runtime fills holes, joins fragments and numbers placeholders; it never
// writes SQL of its own, and never quotes anything.
type Fragments struct {
	Placeholder Placeholder
	Templates   map[string]string
}

// Node is what fills a hole or a list: a Use of a fragment, a named Param,
// an Arg holding a value, or a List.
type Node interface{ node() }

// Use is one fragment of the table, with what fills its holes by name. A
// hole nothing fills renders as nothing.
type Use struct {
	Fragment string
	Holes    map[string]Node
}

// Param is a parameter by name, whose value Args gives. Left out of Args,
// it is an optional field the call does not set (§14.2): as an item of a
// list, such as a VALUES row, it renders the fragment value.default, and a
// list item holding it elsewhere is dropped, such as a SET assignment.
type Param string

// Arg is a parameter whose value the call gives directly, as composed
// queries do.
type Arg struct {
	Value any
	// The value is a sensitive column's, which hooks redact (§24).
	Sensitive bool
}

// List is nodes joined by a separator, such as a column list.
type List struct {
	Items     []Node
	Separator string
}

func (Use) node()   {}
func (Param) node() {}
func (Arg) node()   {}
func (List) node()  {}

// Args are the values of a statement's named parameters. A name left out
// is an optional field the call does not set.
type Args map[string]any

// Assembled is a statement's SQL with its arguments in placeholder order.
type Assembled struct {
	SQL       string
	Args      []any
	Sensitive []bool
}

// errAbsent marks a fragment that uses a parameter the call left out.
var errAbsent = errors.New("absent")

// Assemble joins plan's parts, in order with nothing between them, filling
// holes from args. sensitive names the named parameters that hold
// sensitive values. A fragment the table lacks, or a parameter left out
// where nothing can stand in for it, is an error: generated code only
// builds plans the table anticipates (§16.2).
func (t *Fragments) Assemble(plan []Node, args Args, sensitive map[string]bool) (Assembled, error) {
	a := assembler{t: t, args: args, sensitive: sensitive}
	for _, n := range plan {
		if err := a.node(n); err != nil {
			if errors.Is(err, errAbsent) {
				return Assembled{}, fmt.Errorf("dql: %w", err)
			}
			return Assembled{}, err
		}
	}
	return Assembled{SQL: a.sql.String(), Args: a.out, Sensitive: a.sens}, nil
}

type assembler struct {
	t         *Fragments
	args      Args
	sensitive map[string]bool
	sql       strings.Builder
	out       []any
	sens      []bool
}

// mark and reset let a list drop an item that renders partly before
// failing.
type mark struct{ sql, args int }

func (a *assembler) mark() mark { return mark{a.sql.Len(), len(a.out)} }

func (a *assembler) reset(m mark) {
	s := a.sql.String()[:m.sql]
	a.sql.Reset()
	a.sql.WriteString(s)
	a.out, a.sens = a.out[:m.args], a.sens[:m.args]
}

func (a *assembler) node(n Node) error {
	switch n := n.(type) {
	case Use:
		return a.use(n)
	case Param:
		v, ok := a.args[string(n)]
		if !ok {
			return fmt.Errorf("parameter %s is left out: %w", n, errAbsent)
		}
		if e, ok := v.(expression); ok {
			return a.node(e.node)
		}
		a.arg(v, a.sensitive[string(n)])
	case Arg:
		a.arg(n.Value, n.Sensitive)
	case List:
		return a.list(n)
	}
	return nil
}

func (a *assembler) arg(v any, sensitive bool) {
	a.out = append(a.out, v)
	a.sens = append(a.sens, sensitive)
	switch a.t.Placeholder {
	case Question:
		a.sql.WriteByte('?')
	case AtNumbered:
		a.sql.WriteString("@p" + strconv.Itoa(len(a.out)))
	default:
		a.sql.WriteString("$" + strconv.Itoa(len(a.out)))
	}
}

func (a *assembler) list(l List) error {
	first := true
	for _, item := range l.Items {
		m := a.mark()
		if !first {
			a.sql.WriteString(l.Separator)
		}
		if p, ok := item.(Param); ok {
			if _, set := a.args[string(p)]; !set {
				if err := a.use(Use{Fragment: "value.default"}); err != nil {
					return err
				}
				first = false
				continue
			}
		}
		if err := a.node(item); err != nil {
			if !errors.Is(err, errAbsent) {
				return err
			}
			a.reset(m)
			continue
		}
		first = false
	}
	return nil
}

func (a *assembler) use(u Use) error {
	tmpl, ok := a.t.Templates[u.Fragment]
	if !ok {
		return fmt.Errorf("dql: the fragment table has no %s", u.Fragment)
	}
	for {
		i := strings.IndexByte(tmpl, '{')
		if i < 0 {
			break
		}
		j := strings.IndexByte(tmpl[i:], '}')
		if j < 0 || !isHole(tmpl[i+1:i+j]) {
			a.sql.WriteString(tmpl[:i+1])
			tmpl = tmpl[i+1:]
			continue
		}
		a.sql.WriteString(tmpl[:i])
		if n, ok := u.Holes[tmpl[i+1:i+j]]; ok {
			if err := a.node(n); err != nil {
				return err
			}
		}
		tmpl = tmpl[i+j+1:]
	}
	a.sql.WriteString(tmpl)
	return nil
}

// isHole reports whether s names a hole: lower-case letters, digits and
// underscores.
func isHole(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

// Query is a query of the IR's query layer that generated code runs
// (§16.3): a builder query's plan, or a query file's text as one fragment.
type Query struct {
	// The query's name, such as user.get.by_primary.
	Name    string
	Catalog string
	// For hooks, such as select or insert (§24).
	Kind        string
	Plan        []Node
	Writes      bool
	AtMostOne   bool
	Consistency Consistency
	// The named parameters that hold sensitive values (§24).
	Sensitive map[string]bool
}

// Statement assembles q with args into the statement the executor runs.
func (q *Query) Statement(t *Fragments, args Args) (Statement, error) {
	a, err := t.Assemble(q.Plan, args, q.Sensitive)
	if err != nil {
		return Statement{}, fmt.Errorf("%w in query %s", err, q.Name)
	}
	return Statement{
		Query: q.Name, Catalog: q.Catalog, SQL: a.SQL, Args: a.Args, Writes: q.Writes, AtMostOne: q.AtMostOne,
		Consistency: q.Consistency, Kind: q.Kind, Sensitive: a.Sensitive,
	}, nil
}
