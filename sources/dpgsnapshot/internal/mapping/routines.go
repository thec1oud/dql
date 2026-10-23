package mapping

import (
	"cmp"
	"fmt"
	"slices"

	pg "github.com/pganalyze/pg_query_go/v6"

	pgextv1 "github.com/thec1oud/dql/proto/dql/ext/postgres/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/dpgsnapshot/internal/format"
	"github.com/thec1oud/dql/sources/internal/comment"
)

// functionFields are the FUNCTION record fields an overload, or the Routine
// extension of a function that is not exposed, carries.
var functionFields = []string{
	"schema", "name", "args", "return_type", "returns_set", "return_table", "language", "volatility", "parallel",
	"strict", "security_def", "leakproof", "owner",
}

var volatilities = map[string]irv1.Volatility{
	"IMMUTABLE": irv1.Volatility_VOLATILITY_IMMUTABLE,
	"STABLE":    irv1.Volatility_VOLATILITY_STABLE,
	"VOLATILE":  irv1.Volatility_VOLATILITY_VOLATILE,
}

// function maps a function. As in pg-live, only a function whose comment
// holds @public is exposed (§6.5); any other travels as a Routine extension
// of its namespace.
func (m *mapper) function(key string, f *format.Function) {
	text, field := dbComment(f.Comment, f.Deprecated)
	location := "COMMENT ON FUNCTION " + f.Schema + "." + f.Name + "(" + f.Args + ")"
	n := m.b.get(f.Schema)
	parallel := cmp.Or(f.Parallel, "UNSAFE")
	volatility := cmp.Or(f.Volatility, "VOLATILE")
	if !comment.Marked(text, "public") {
		m.ignored(m.qn(f.Schema, f.Name, ""), location, text, "the routine is not exposed, so dpg-snapshot reads no annotations on it")
		n.exts.add(&pgextv1.Routine{Name: f.Name, Kind: "FUNCTION", IdentityArguments: f.Args, Result: result(f),
			Language: f.Language, Strict: f.Strict, Volatility: volatility, Parallel: parallel,
			SecurityDefiner: f.SecurityDef, Leakproof: f.Leakproof, Owner: deref(f.Owner), Comment: text})
		n.exts.record("function", key, f, append(slices.Clone(functionFields), field)...)
		return
	}
	o := m.overload(f.Schema, f.Name, f.Args, location, text)
	o.Volatility = volatilities[volatility]
	if f.Strict {
		o.NullBehavior = irv1.NullBehavior_NULL_BEHAVIOR_STRICT
	}
	o.Returns = m.returns(f, location)
	e := &exts{m: m}
	e.comment(text)
	e.owner(f.Owner)
	e.add(&pgextv1.RoutineSource{Language: f.Language})
	e.add(&pgextv1.RoutineProperties{Parallel: parallel, SecurityDefiner: f.SecurityDef, Leakproof: f.Leakproof})
	e.record("function", "", f, append(slices.Clone(functionFields), field)...)
	o.Extensions = e.list()
	n.add(f.Name, o)
}

// procedure maps a procedure, which dpg keeps by body hash: it records no
// language (fidelity.RoutineBodies). Like a function, it is exposed only when
// its comment holds @public. A procedure is never parallel safe.
func (m *mapper) procedure(key string, p *format.Opaque) {
	text, field := dbComment(p.Comment, p.Deprecated)
	location := "COMMENT ON PROCEDURE " + p.Schema + "." + p.Name + "(" + p.Args + ")"
	n := m.b.get(p.Schema)
	used := []string{"kind", "schema", "name", "args", "procedure_owner", "procedure_security_def", field}
	if !comment.Marked(text, "public") {
		m.ignored(m.qn(p.Schema, p.Name, ""), location, text, "the routine is not exposed, so dpg-snapshot reads no annotations on it")
		n.exts.add(&pgextv1.Routine{Name: p.Name, Kind: "PROCEDURE", IdentityArguments: p.Args, Volatility: "VOLATILE",
			Parallel: "UNSAFE", SecurityDefiner: p.ProcedureSecurityDef, Owner: deref(p.ProcedureOwner), Comment: text})
		n.exts.record("procedure", key, p, used...)
		return
	}
	o := m.overload(p.Schema, p.Name, p.Args, location, text)
	o.Kind = irv1.RoutineKind_ROUTINE_KIND_PROCEDURE
	o.Volatility = irv1.Volatility_VOLATILITY_VOLATILE
	o.Returns = &irv1.Returns{Kind: &irv1.Returns_Nothing{Nothing: &irv1.Empty{}}}
	e := &exts{m: m}
	e.comment(text)
	e.owner(p.ProcedureOwner)
	e.add(&pgextv1.RoutineSource{})
	e.add(&pgextv1.RoutineProperties{Parallel: "UNSAFE", SecurityDefiner: p.ProcedureSecurityDef})
	e.record("procedure", "", p, used...)
	o.Extensions = e.list()
	n.add(p.Name, o)
}

// overload starts an exposed routine's overload (§6.5). A snapshot names an
// overload by its input argument types only, so arguments have a type and
// nothing else: no name, mode or default (fidelity.RoutineArguments).
func (m *mapper) overload(schema, name, args, location, text string) *irv1.Overload {
	o := &irv1.Overload{
		Kind:         irv1.RoutineKind_ROUTINE_KIND_FUNCTION,
		NullBehavior: irv1.NullBehavior_NULL_BEHAVIOR_CALLED_ON_NULL_INPUT,
		Annotations:  m.annotations(m.qn(schema, name, ""), location, text),
	}
	for _, a := range splitList(args) {
		o.Arguments = append(o.Arguments, &irv1.Argument{Type: m.typeOf("an argument of "+location[len("COMMENT ON "):], schema, a)})
	}
	return o
}

// add adds an overload to the routine of its name. Overloads come in key
// order, which sorts them by their argument types.
func (n *namespace) add(name string, o *irv1.Overload) {
	r, ok := n.routines[name]
	if !ok {
		r = &irv1.Routine{Name: ident(name)}
		n.routines[name] = r
	}
	r.Overloads = append(r.Overloads, o)
}

// result spells a function's result as pg_get_function_result prints it.
func result(f *format.Function) string {
	switch {
	case f.ReturnTable != "":
		return "TABLE(" + f.ReturnTable + ")"
	case f.ReturnsSet:
		return "SETOF " + f.ReturnType
	}
	return f.ReturnType
}

// returns reads what a function returns (§6.5): a table of columns, nothing
// for void, or a value or a set of values, which is nullable whatever the
// function's strictness.
func (m *mapper) returns(f *format.Function, location string) *irv1.Returns {
	what := "the result of " + location[len("COMMENT ON "):]
	switch {
	case f.ReturnTable != "":
		return &irv1.Returns{Kind: &irv1.Returns_Table{Table: &irv1.ReturnTable{Columns: m.returnTable(what, f.Schema, f.ReturnTable)}}}
	case f.ReturnType == "void":
		return &irv1.Returns{Kind: &irv1.Returns_Nothing{Nothing: &irv1.Empty{}}}
	}
	v := &irv1.ReturnValue{Type: m.typeOf(what, f.Schema, f.ReturnType), Nullable: true}
	if f.ReturnsSet {
		return &irv1.Returns{Kind: &irv1.Returns_SetOf{SetOf: v}}
	}
	return &irv1.Returns{Kind: &irv1.Returns_Scalar{Scalar: v}}
}

// returnTable reads RETURNS TABLE's column list, "a integer, b text".
func (m *mapper) returnTable(what, schema, list string) []*irv1.Column {
	const prefix = "CREATE TABLE t ("
	var cols []*irv1.Column
	for i, part := range splitList(list) {
		tree, err := pg.Parse(prefix + part + ")")
		var def *pg.ColumnDef
		if err == nil && len(tree.Stmts) == 1 && len(tree.Stmts[0].Stmt.GetCreateStmt().GetTableElts()) == 1 {
			def = tree.Stmts[0].Stmt.GetCreateStmt().GetTableElts()[0].GetColumnDef()
		}
		if def == nil || def.TypeName == nil {
			m.fail(fmt.Errorf("mapping: %s: %q is not a column", what, part))
			continue
		}
		// The type runs from where the parser found it to the end.
		typ := m.typeOf(what, schema, part[int(def.TypeName.Location)-len(prefix):])
		cols = append(cols, &irv1.Column{Name: ident(def.Colname), Ordinal: uint32(i + 1), Type: typ,
			Nullable: true, NullabilitySource: irv1.NullabilitySource_NULLABILITY_SOURCE_UNPROVEN})
	}
	return cols
}
