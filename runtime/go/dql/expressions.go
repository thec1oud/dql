package dql

import "fmt"

// Expr is an expression a write assigns to a column in place of a value
// (§20.2). Shapes stay plain data: expressions travel on the operation.
type Expr interface{ expr() Node }

// TimeExpr is an expression a time column takes: Now or Default.
type TimeExpr interface {
	Expr
	timeExpr()
}

// DefaultExpr is the expression every column takes: Default.
type DefaultExpr interface {
	TimeExpr
	defaultExpr()
}

type nowExpr struct{}

func (nowExpr) expr() Node { return Use{Fragment: "value.now"} }
func (nowExpr) timeExpr()  {}

type defaultExpr struct{}

func (defaultExpr) expr() Node   { return Use{Fragment: "value.default"} }
func (defaultExpr) timeExpr()    {}
func (defaultExpr) defaultExpr() {}

// Now is the time the statement's transaction began, as the database
// tells it.
func Now() TimeExpr { return nowExpr{} }

// Default is the column's default, or null for a column without one.
func Default() DefaultExpr { return defaultExpr{} }

// Update is an assignment an update takes: Set or arithmetic on the
// column, such as Posts.Views.Add(1).
type Update[E any] interface {
	// E keeps one entity's assignments from another's.
	assignment(E) (string, Node)
}

// Assignment is an assignment every write takes, an insert included: one
// that does not read the row it writes.
type Assignment[E any] interface {
	Update[E]
	valueOnly(E)
}

type set[E any] struct {
	field string
	node  Node
}

func (s set[E]) assignment(E) (string, Node) { return s.field, s.node }
func (set[E]) valueOnly(E)                   {}

type arith[E any] struct {
	field string
	node  Node
}

func (a arith[E]) assignment(E) (string, Node) { return a.field, a.node }

// Set assigns e to field, the stored name of a column of E; generated
// column handles call it.
func Set[E any](field string, e Expr) Assignment[E] { return set[E]{field, e.expr()} }

// Arith assigns to field the value operator fragment op makes of column
// fragment col and v, as col + v; generated column handles call it.
func Arith[E any](op, field, col string, v Node) Update[E] {
	return arith[E]{field, Use{Fragment: op, Holes: map[string]Node{"0": Use{Fragment: col}, "1": v}}}
}

// Values returns an insert's assignments as an update's, for Assign.
func Values[E any](as []Assignment[E]) []Update[E] {
	out := make([]Update[E], len(as))
	for i, a := range as {
		out[i] = a
	}
	return out
}

// expression is an assignment's node in Args, which renders in place of
// the parameter.
type expression struct{ node Node }

// Assign adds the assignments to args, which holds the payload of shape;
// generated writes call it before running q. A field the payload also sets
// is a FieldSetTwice issue (§20.3), and a field q does not write is an
// error.
func Assign[E any](q *Query, shape string, args Args, as []Update[E]) error {
	var v ValidationErrors
	var e E
	for _, a := range as {
		field, node := a.assignment(e)
		if !takes(q.Plan, field) {
			return fmt.Errorf("dql: %s does not write %s", q.Name, field)
		}
		if _, ok := args[field]; ok {
			v = append(v, ValidationIssue{Shape: shape, Field: field, Kind: KindFieldSetTwice})
			continue
		}
		args[field] = expression{node}
	}
	return v.Err()
}

// takes reports whether parameter name appears under the plan's nodes.
func takes(plan []Node, name string) bool {
	for _, n := range plan {
		switch v := n.(type) {
		case Use:
			for _, h := range v.Holes {
				if takes([]Node{h}, name) {
					return true
				}
			}
		case List:
			if takes(v.Items, name) {
				return true
			}
		case Param:
			if string(v) == name {
				return true
			}
		}
	}
	return false
}
