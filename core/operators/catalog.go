// Package operators holds the core's operator catalog and resolves, for
// every column, the operators the target dialect supports on it (design
// record §9, plans/operator-catalog-proposal.md).
//
// The core defines each operator's signature once. A dialect plugin
// publishes its fidelity per operator in the capability profile, and the
// core resolves fidelity per column, so an emitter generates exactly the
// operators a column lists (§9.3).
package operators

import (
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// The requirements an operator places on its type variable (§9.1).
const (
	Comparable = "comparable"
	Orderable  = "orderable"
	Numeric    = "numeric"
	JSON       = "json"
	// Nullable is met by the operand itself: a nullable column or field.
	Nullable = "nullable"
)

// Catalog returns the core's operators, sorted by name (§9.1). Every call
// returns new messages, so a caller may change them.
func Catalog() []*irv1.Operator {
	var (
		t       = typeVar("T")
		text    = &irv1.OperandType{Kind: &irv1.OperandType_Type{Type: &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}}}}
		boolean = &irv1.OperandType{Kind: &irv1.OperandType_Type{Type: &irv1.Type{Kind: &irv1.Type_Bool{Bool: &irv1.Empty{}}}}}
		arrayT  = &irv1.OperandType{Kind: &irv1.OperandType_Array{Array: t}}
		rangeT  = &irv1.OperandType{Kind: &irv1.OperandType_Range{Range: t}}
		strict  = irv1.NullBehavior_NULL_BEHAVIOR_STRICT
		never   = irv1.NullBehavior_NULL_BEHAVIOR_NEVER_NULL
	)
	op := func(name string, args []*irv1.OperandType, returns *irv1.OperandType, null irv1.NullBehavior, requires ...string) *irv1.Operator {
		return &irv1.Operator{Name: name, Signature: &irv1.Signature{Args: args, Returns: returns}, Requires: requires, NullBehavior: null}
	}
	tt := []*irv1.OperandType{t, t}
	texts := []*irv1.OperandType{text, text}
	return []*irv1.Operator{
		op("add", tt, t, strict, Numeric),
		op("any", []*irv1.OperandType{t, arrayT}, boolean, strict, Comparable),
		op("array_contains", []*irv1.OperandType{arrayT, t}, boolean, strict, Comparable),
		op("array_overlaps", []*irv1.OperandType{arrayT, arrayT}, boolean, strict, Comparable),
		op("between", []*irv1.OperandType{t, t, t}, boolean, strict, Orderable),
		op("contains", texts, boolean, strict),
		op("div", tt, t, strict, Numeric),
		op("eq", tt, boolean, strict, Comparable),
		op("ge", tt, boolean, strict, Orderable),
		op("gt", tt, boolean, strict, Orderable),
		op("ilike", texts, boolean, strict),
		op("in", []*irv1.OperandType{t, arrayT}, boolean, strict, Comparable),
		op("is_distinct_from", tt, boolean, never, Comparable),
		op("is_not_null", []*irv1.OperandType{t}, boolean, never, Nullable),
		op("is_null", []*irv1.OperandType{t}, boolean, never, Nullable),
		op("json_contains", tt, boolean, strict, JSON),
		op("json_get", []*irv1.OperandType{t, text}, t, strict, JSON),
		op("json_path", []*irv1.OperandType{t, text}, t, strict, JSON),
		op("le", tt, boolean, strict, Orderable),
		op("like", texts, boolean, strict),
		op("lt", tt, boolean, strict, Orderable),
		op("mod", tt, t, strict, Numeric),
		op("mul", tt, t, strict, Numeric),
		op("ne", tt, boolean, strict, Comparable),
		op("neg", []*irv1.OperandType{t}, t, strict, Numeric),
		op("range_contains", []*irv1.OperandType{rangeT, t}, boolean, strict, Orderable),
		op("range_overlaps", []*irv1.OperandType{rangeT, rangeT}, boolean, strict, Orderable),
		op("regex_match", texts, boolean, strict),
		op("starts_with", texts, boolean, strict),
		op("sub", tt, t, strict, Numeric),
	}
}

func typeVar(name string) *irv1.OperandType {
	return &irv1.OperandType{Kind: &irv1.OperandType_Var{Var: name}}
}
