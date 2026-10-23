// Package pgsql is the Postgres knowledge the source plugins share: logical
// types for native types (design record §8), recognition of check constraints
// as portable kinds (§10.1, §10.2) and classification of defaults (§11.3).
//
// pg-live (the live catalog) and dpg-snapshot (dpg's snapshots) both use it, so
// both produce the same IR for the same schema. It parses SQL with pg_query,
// which the source plugins may do and the core never does (§10.1).
//
// Every function is deterministic and independent of the process locale.
package pgsql

import (
	"errors"
	"fmt"

	pg "github.com/pganalyze/pg_query_go/v6"
)

// errSQL marks text that does not parse as the fragment it should be.
var errSQL = errors.New("pgsql: not a single SQL fragment of the expected kind")

// parseCmd parses stmt, which embeds one caller-supplied fragment in an
// ALTER TABLE statement, and returns its only command. Text that closes the
// fragment early and adds statements or commands is rejected here.
func parseCmd(stmt string) (*pg.AlterTableCmd, error) {
	tree, err := pg.Parse(stmt)
	if err != nil {
		return nil, fmt.Errorf("pgsql: %w", err)
	}
	if len(tree.Stmts) != 1 {
		return nil, errSQL
	}
	cmds := tree.Stmts[0].Stmt.GetAlterTableStmt().GetCmds()
	if len(cmds) != 1 {
		return nil, errSQL
	}
	return cmds[0].GetAlterTableCmd(), nil
}

// parseTypeName parses text as the type of a column.
func parseTypeName(text string) (*pg.TypeName, error) {
	cmd, err := parseCmd("ALTER TABLE t ALTER COLUMN c TYPE " + text)
	if err != nil {
		return nil, err
	}
	def := cmd.GetDef().GetColumnDef()
	if def.GetRawDefault() != nil || def.GetCollClause() != nil {
		return nil, errSQL
	}
	return def.GetTypeName(), nil
}

// parseCheck parses text as a check constraint, either as the database prints
// it ("CHECK (...)", possibly followed by NOT VALID or NO INHERIT) or as the
// bare expression.
func parseCheck(text string) (*pg.Node, error) {
	cmd, err := parseCmd("ALTER TABLE t ADD " + text)
	if err != nil {
		cmd, err = parseCmd("ALTER TABLE t ADD CHECK (" + text + ")")
		if err != nil {
			return nil, err
		}
	}
	c := cmd.GetDef().GetConstraint()
	if c.GetContype() != pg.ConstrType_CONSTR_CHECK {
		return nil, errSQL
	}
	return c.GetRawExpr(), nil
}

// names returns the strings of a qualified name list, or false when an element
// is not a plain string (such as the * of a column reference).
func names(list []*pg.Node) ([]string, bool) {
	out := make([]string, len(list))
	for i, n := range list {
		s := n.GetString_()
		if s == nil {
			return nil, false
		}
		out[i] = s.Sval
	}
	return out, true
}

// builtinName returns the pg_catalog type a type name refers to, for names
// that are unqualified or qualified with pg_catalog; ok is false for names of
// other namespaces. An unqualified name that is not built in is returned with
// ok false as well, because the search path decides it.
func builtinName(tn *pg.TypeName) (string, bool) {
	ns, _ := names(tn.Names)
	switch {
	case len(ns) == 2 && ns[0] == "pg_catalog":
		return ns[1], true
	case len(ns) == 1 && catalogTypes[ns[0]]:
		return ns[0], true
	}
	return "", false
}
