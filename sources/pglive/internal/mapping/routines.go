package mapping

import (
	"strings"

	pgextv1 "github.com/thec1oud/dql/proto/dql/ext/postgres/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/internal/comment"
	"github.com/thec1oud/dql/sources/pglive/internal/introspect"
)

// placedRoutine is a routine with the schema it belongs to.
type placedRoutine struct {
	schema  string
	routine *irv1.Routine
}

// routineKinds spells prokind.
var routineKinds = map[string]string{"f": "FUNCTION", "p": "PROCEDURE", "a": "AGGREGATE", "w": "WINDOW"}

// routines splits the overloads into the public functions and procedures,
// grouped into routines by name (§6.5), and the rest, which travel as
// extensions. pg-live's marker of a public routine is @public in its
// comment. Aggregates and window functions are not called on their own, so
// they are never public. Overloads arrive sorted by schema, name and identity
// arguments, so routines and their overloads come out sorted.
func (m *mapper) routines(all []introspect.Routine) (public []placedRoutine, hidden []introspect.Routine) {
	for _, r := range all {
		location := "COMMENT ON " + routineKinds[r.Kind] + " " + signature(r)
		target := m.qn(r.Schema, r.Name, "")
		if (r.Kind != "f" && r.Kind != "p") || !comment.Marked(r.Comment, "public") {
			m.ignored(target, location, r.Comment, "the routine is not exposed, so pg-live reads no annotations on it")
			hidden = append(hidden, r)
			continue
		}
		o := m.overload(r)
		o.Annotations = m.annotations(target, location, r.Comment)
		if n := len(public); n > 0 && public[n-1].schema == r.Schema && public[n-1].routine.Name.Stored == r.Name {
			public[n-1].routine.Overloads = append(public[n-1].routine.Overloads, o)
			continue
		}
		public = append(public, placedRoutine{r.Schema, &irv1.Routine{Name: ident(r.Name), Overloads: []*irv1.Overload{o}}})
	}
	return public, hidden
}

// signature names an overload as COMMENT ON names it: its input arguments'
// types (§4.6).
func signature(r introspect.Routine) string {
	var types []string
	for _, a := range r.Arguments {
		if a.Mode == "i" || a.Mode == "b" || a.Mode == "v" {
			types = append(types, a.Type.Formatted)
		}
	}
	return r.Schema + "." + r.Name + "(" + strings.Join(types, ", ") + ")"
}

var argModes = map[string]irv1.ArgMode{
	"i": irv1.ArgMode_ARG_MODE_IN,
	"o": irv1.ArgMode_ARG_MODE_OUT,
	"b": irv1.ArgMode_ARG_MODE_INOUT,
	"v": irv1.ArgMode_ARG_MODE_VARIADIC,
}

var volatilities = map[string]irv1.Volatility{
	"i": irv1.Volatility_VOLATILITY_IMMUTABLE,
	"s": irv1.Volatility_VOLATILITY_STABLE,
	"v": irv1.Volatility_VOLATILITY_VOLATILE,
}

var volatilityNames = map[string]string{"i": "IMMUTABLE", "s": "STABLE", "v": "VOLATILE"}

var parallelNames = map[string]string{"s": "SAFE", "r": "RESTRICTED", "u": "UNSAFE"}

// overload maps a public function or procedure (§6.5). Its result is
// nullable whatever its null behavior: STRICT only says that a NULL argument
// gives a NULL result. A procedure returns nothing; its OUT and INOUT
// arguments are its outputs. A RETURNS TABLE function returns a table of its
// TABLE arguments. The signature is the core's to compute (§4.6).
func (m *mapper) overload(r introspect.Routine) *irv1.Overload {
	what := routineKinds[r.Kind] + " " + signature(r)
	o := &irv1.Overload{
		Kind:         irv1.RoutineKind_ROUTINE_KIND_FUNCTION,
		NullBehavior: irv1.NullBehavior_NULL_BEHAVIOR_CALLED_ON_NULL_INPUT,
		Volatility:   volatilities[r.Volatility],
	}
	if r.Kind == "p" {
		o.Kind = irv1.RoutineKind_ROUTINE_KIND_PROCEDURE
	}
	if r.Strict {
		o.NullBehavior = irv1.NullBehavior_NULL_BEHAVIOR_STRICT
	}
	var table []*irv1.Column
	for _, a := range r.Arguments {
		typ := m.typeOf("argument "+a.Name+" of "+what, a.Type)
		if a.Mode == "t" {
			table = append(table, &irv1.Column{Name: ident(a.Name), Ordinal: uint32(len(table) + 1), Type: typ,
				Nullable: true, NullabilitySource: irv1.NullabilitySource_NULLABILITY_SOURCE_UNPROVEN})
			continue
		}
		arg := &irv1.Argument{Type: typ, Mode: argModes[a.Mode], HasDefault: a.HasDefault}
		if a.Name != "" {
			arg.Name = ident(a.Name)
		}
		o.Arguments = append(o.Arguments, arg)
	}
	switch {
	case r.Kind == "p" || r.Returns == nil || r.Returns.Schema == "pg_catalog" && r.Returns.Name == "void":
		o.Returns = &irv1.Returns{Kind: &irv1.Returns_Nothing{Nothing: &irv1.Empty{}}}
	case len(table) > 0:
		o.Returns = &irv1.Returns{Kind: &irv1.Returns_Table{Table: &irv1.ReturnTable{Columns: table}}}
	case r.ReturnsSet:
		o.Returns = &irv1.Returns{Kind: &irv1.Returns_SetOf{SetOf: &irv1.ReturnValue{
			Type: m.typeOf("result of "+what, *r.Returns), Nullable: true}}}
	default:
		o.Returns = &irv1.Returns{Kind: &irv1.Returns_Scalar{Scalar: &irv1.ReturnValue{
			Type: m.typeOf("result of "+what, *r.Returns), Nullable: true}}}
	}
	e := &exts{m: m}
	e.comment(r.Comment)
	e.owner(r.Owner)
	e.member(r.Extension)
	e.add(&pgextv1.RoutineSource{Language: r.Language, Body: body(r)})
	e.add(&pgextv1.RoutineProperties{Parallel: parallelNames[r.Parallel], SecurityDefiner: r.SecurityDefiner,
		Leakproof: r.Leakproof, ArgumentDefaults: argumentDefaults(r)})
	e.grants(r.Grants)
	o.Extensions = e.list()
	return o
}

// argumentDefaults lists the default expressions of the arguments that have
// one, in argument order.
func argumentDefaults(r introspect.Routine) []string {
	var out []string
	for _, a := range r.Arguments {
		if a.HasDefault {
			out = append(out, a.Default)
		}
	}
	return out
}

// body is a routine's BEGIN ATOMIC body, or else its prosrc.
func body(r introspect.Routine) string {
	if r.SQLBody != "" {
		return r.SQLBody
	}
	return r.Source
}

// hiddenRoutine carries a routine that is not exposed (§6.5).
func (m *mapper) hiddenRoutine(r introspect.Routine) *pgextv1.Routine {
	return &pgextv1.Routine{
		Name: r.Name, Kind: routineKinds[r.Kind], IdentityArguments: r.IdentityArguments, Result: r.Result,
		Language: r.Language, Body: body(r), Strict: r.Strict, Volatility: volatilityNames[r.Volatility],
		Parallel: parallelNames[r.Parallel], SecurityDefiner: r.SecurityDefiner, Leakproof: r.Leakproof,
		Owner: r.Owner, Comment: r.Comment, Extension: r.Extension, Grants: m.grants(r.Grants),
		ArgumentDefaults: argumentDefaults(r),
	}
}
