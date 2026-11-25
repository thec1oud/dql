package pgsql

import (
	"slices"

	pg "github.com/pganalyze/pg_query_go/v6"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// RecognizePredicate fills the predicate columns of u, a uniqueness fact of a
// partial index, from text, its WHERE clause as the database prints it or as
// written: the columns a top-level conjunct requires to be NULL (`c IS
// NULL`), and those it requires to be not true (`NOT c`, `c = false`,
// `false = c`, `c IS FALSE`, `c IS NOT TRUE`). The core reads these to tell
// whether a unique key excludes soft-deleted rows (§21.3). Text that does
// not parse, and conjuncts of other forms, add nothing.
func RecognizePredicate(u *irv1.Uniqueness, text string) {
	// The text is always a bare expression, so it is parsed as one: as a
	// check it could read as a column definition (`deleted_at is null`).
	cmd, err := parseCmd("ALTER TABLE t ADD CHECK (" + text + ")")
	if err != nil {
		return
	}
	expr := cmd.GetDef().GetConstraint().GetRawExpr()
	var null, notTrue []string
	for _, c := range conjuncts(expr) {
		if name, ok := isNull(c); ok {
			null = append(null, name)
		} else if name, ok := isNotTrue(c); ok {
			notTrue = append(notTrue, name)
		}
	}
	u.PredicateNullColumns = sortedSet(null)
	u.PredicateNotTrueColumns = sortedSet(notTrue)
}

// conjuncts flattens nested ANDs at the top of expr.
func conjuncts(expr *pg.Node) []*pg.Node {
	b := expr.GetBoolExpr()
	if b == nil || b.Boolop != pg.BoolExprType_AND_EXPR {
		return []*pg.Node{expr}
	}
	var out []*pg.Node
	for _, a := range b.Args {
		out = append(out, conjuncts(a)...)
	}
	return out
}

// isNull reads `c IS NULL`.
func isNull(n *pg.Node) (string, bool) {
	t := n.GetNullTest()
	if t == nil || t.Nulltesttype != pg.NullTestType_IS_NULL {
		return "", false
	}
	return columnName(t.Arg)
}

// isNotTrue reads `NOT c`, `c = false`, `false = c`, `c IS FALSE` and `c IS
// NOT TRUE`.
func isNotTrue(n *pg.Node) (string, bool) {
	if b := n.GetBoolExpr(); b != nil && b.Boolop == pg.BoolExprType_NOT_EXPR {
		return columnName(b.Args[0])
	}
	if t := n.GetBooleanTest(); t != nil &&
		(t.Booltesttype == pg.BoolTestType_IS_FALSE || t.Booltesttype == pg.BoolTestType_IS_NOT_TRUE) {
		return columnName(t.Arg)
	}
	e := n.GetAExpr()
	if e == nil || e.Kind != pg.A_Expr_Kind_AEXPR_OP || opName(e) != "=" {
		return "", false
	}
	if isFalse(e.Rexpr) {
		return columnName(e.Lexpr)
	}
	if isFalse(e.Lexpr) {
		return columnName(e.Rexpr)
	}
	return "", false
}

// isFalse reads the constant false, as written or as Postgres prints it.
func isFalse(n *pg.Node) bool {
	if c := n.GetTypeCast(); c != nil {
		n = c.Arg
	}
	a := n.GetAConst()
	if a == nil {
		return false
	}
	if b := a.GetBoolval(); b != nil {
		return !b.Boolval
	}
	return a.GetSval().GetSval() == "false"
}

// columnName reads an unqualified or qualified column reference.
func columnName(n *pg.Node) (string, bool) {
	parts, ok := names(n.GetColumnRef().GetFields())
	if !ok || len(parts) == 0 {
		return "", false
	}
	return parts[len(parts)-1], true
}

func sortedSet(ss []string) []string {
	slices.Sort(ss)
	return slices.Compact(ss)
}
