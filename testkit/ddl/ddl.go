// Package ddl writes the DDL that creates a catalog's objects, so a test can
// introspect a database, apply what was read to an empty one, introspect that
// and compare the two catalogs (implementation guide §6.5). It is test
// tooling: no source, dialect or emitter uses it, and the core never renders
// SQL (rule 1).
//
// A writer writes what the source read in the form the source recognizes it:
// native text where the catalog keeps it, and portable constraints in the
// first form the dialect's recognizer lists. An object or extension a writer
// cannot write is an error that names it, never a silent omission, so a round
// trip cannot pass by losing what it was meant to carry.
package ddl

import (
	"fmt"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Write returns the DDL that creates c's objects on dialect d, as statements
// separated by a line holding only GO for SQL Server, and by semicolons
// otherwise.
func Write(d irv1.Dialect, c *irv1.Catalog) (string, error) {
	var w writer
	switch d {
	case irv1.Dialect_DIALECT_POSTGRES:
		w = &postgres{}
	case irv1.Dialect_DIALECT_SQLITE:
		w = &sqlite{}
	case irv1.Dialect_DIALECT_MYSQL:
		w = &mysql{}
	case irv1.Dialect_DIALECT_SQLSERVER:
		w = &sqlserver{}
	default:
		return "", fmt.Errorf("ddl: no writer for %s", d)
	}
	return w.write(c)
}

type writer interface {
	write(c *irv1.Catalog) (string, error)
}

// script collects statements and the first error met while writing them.
type script struct {
	stmts []string
	err   error
}

// add appends a statement made from format and args.
func (s *script) add(format string, args ...any) {
	s.stmts = append(s.stmts, fmt.Sprintf(format, args...))
}

// fail records err unless an error is already recorded.
func (s *script) fail(format string, args ...any) {
	if s.err == nil {
		s.err = fmt.Errorf("ddl: "+format, args...)
	}
}

// join returns the statements, each ended by end, or the error.
func (s *script) join(end string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return strings.Join(s.stmts, end) + end, nil
}

// unpack returns the messages of exts. An extension whose type is not
// linked into the binary is an error naming what holds it.
func (s *script) unpack(what string, exts []*anypb.Any) []proto.Message {
	var out []proto.Message
	for _, a := range exts {
		m, err := a.UnmarshalNew()
		if err != nil {
			s.fail("%s: extension %s: %v", what, a.GetTypeUrl(), err)
			continue
		}
		out = append(out, m)
	}
	return out
}

// unwritten fails for an extension of what that the writer does not write.
func (s *script) unwritten(what string, m proto.Message) {
	s.fail("%s: no writer for extension %s", what, m.ProtoReflect().Descriptor().FullName())
}

// ordered returns tables sorted so each comes after the tables of its
// namespace it depends on, keeping their order otherwise. A cycle keeps the
// rest in their order.
func ordered(tables []*irv1.Table) []*irv1.Table {
	var out []*irv1.Table
	done := map[string]bool{}
	var visit func(t *irv1.Table, seen map[string]bool)
	byName := map[string]*irv1.Table{}
	for _, t := range tables {
		byName[t.GetName().GetStored()] = t
	}
	visit = func(t *irv1.Table, seen map[string]bool) {
		name := t.GetName().GetStored()
		if done[name] || seen[name] {
			return
		}
		seen[name] = true
		for _, dep := range t.GetDependsOn() {
			if d, ok := byName[dep.GetObject()]; ok {
				visit(d, seen)
			}
		}
		done[name] = true
		out = append(out, t)
	}
	for _, t := range tables {
		visit(t, map[string]bool{})
	}
	return out
}

// isView reports whether t is a view or a materialized view.
func isView(t *irv1.Table) bool {
	return t.GetKind() != irv1.TableKind_RELATION_KIND_TABLE
}

// checkStyle is how a dialect's recognizer reads portable checks.
type checkStyle struct {
	dialect string
	// quote quotes a string literal.
	quote func(string) string
	// length returns the expression of subject's length in unit and how
	// many of what it counts make one unit, or "" for none.
	length func(subject string, unit irv1.LengthUnit) (string, uint32)
	// pattern returns subject matched against re; nil when the dialect
	// has no portable pattern.
	pattern func(subject, re string) string
	// notEmpty is set when subject <> '' is portable.
	notEmpty bool
}

// portable writes the condition of a portable check on subject in the
// first form the dialect's recognizer lists.
func (s *script) portable(st checkStyle, what, subject string, c *irv1.Constraint) string {
	p := c.GetPortable()
	fail := func() string {
		s.fail("%s: constraint %s: no %s check for %v", what, c.GetName(), st.dialect, p)
		return ""
	}
	switch {
	case p.GetLength() != nil:
		l := p.GetLength()
		n, per := st.length(subject, l.GetUnit())
		if n == "" {
			return fail()
		}
		switch {
		case l.Min != nil && l.Max != nil:
			return fmt.Sprintf("%s BETWEEN %d AND %d", n, l.GetMin()*per, l.GetMax()*per)
		case l.Max != nil:
			return fmt.Sprintf("%s <= %d", n, l.GetMax()*per)
		}
		return fmt.Sprintf("%s >= %d", n, l.GetMin()*per)
	case p.GetNotEmpty() != nil && st.notEmpty:
		return subject + " <> ''"
	case p.GetPattern() != nil && st.pattern != nil:
		return st.pattern(subject, p.GetPattern().GetRegex())
	case p.GetRange() != nil:
		r := p.GetRange()
		var bounds []string
		if r.GetMin() != nil {
			op := map[bool]string{true: ">=", false: ">"}[r.GetMinInclusive()]
			bounds = append(bounds, fmt.Sprintf("%s %s %s", subject, op, literal(st, r.GetMin())))
		}
		if r.GetMax() != nil {
			op := map[bool]string{true: "<=", false: "<"}[r.GetMaxInclusive()]
			bounds = append(bounds, fmt.Sprintf("%s %s %s", subject, op, literal(st, r.GetMax())))
		}
		return strings.Join(bounds, " AND ")
	case p.GetMembership() != nil:
		vals := make([]string, len(p.GetMembership().GetValues()))
		for i, v := range p.GetMembership().GetValues() {
			vals[i] = literal(st, v)
		}
		return subject + " IN (" + strings.Join(vals, ", ") + ")"
	}
	return fail()
}

// literal is the SQL literal of v.
func literal(st checkStyle, v *irv1.Value) string {
	switch k := v.GetKind().(type) {
	case *irv1.Value_String_:
		return st.quote(k.String_)
	case *irv1.Value_Int:
		return strconv.FormatInt(k.Int, 10)
	case *irv1.Value_Uint:
		return strconv.FormatUint(k.Uint, 10)
	case *irv1.Value_Decimal:
		return k.Decimal
	case *irv1.Value_Float:
		return strconv.FormatFloat(k.Float, 'g', -1, 64)
	}
	return strconv.FormatBool(v.GetBool())
}

// typeText is the native text of t, or of the type a narrowing stores.
func (s *script) typeText(what string, t *irv1.Type) string {
	if raw := t.GetNative().GetRaw(); raw != "" {
		return raw
	}
	if n := t.GetNarrowed(); n != nil {
		return s.typeText(what, n.GetStored())
	}
	s.fail("%s: a type with no native text", what)
	return ""
}
