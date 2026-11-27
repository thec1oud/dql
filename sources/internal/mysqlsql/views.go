package mysqlsql

import (
	"fmt"
	"slices"

	"github.com/pingcap/tidb/pkg/parser"
	"github.com/pingcap/tidb/pkg/parser/ast"
)

// ViewReads lists the tables and views a view's definition reads, each
// once and sorted, as database.name. MariaDB has no VIEW_TABLE_USAGE, but
// prints a view's definition with every table qualified by its database;
// a name without one, such as a common table expression's, is left out.
func ViewReads(definition string) ([]string, error) {
	stmt, err := parser.New().ParseOneStmt(definition, "", "")
	if err != nil {
		return nil, fmt.Errorf("the view definition does not parse: %w", err)
	}
	v := &tables{}
	stmt.Accept(v)
	slices.Sort(v.out)
	return slices.Compact(v.out), nil
}

type tables struct{ out []string }

func (v *tables) Enter(n ast.Node) (ast.Node, bool) {
	if t, ok := n.(*ast.TableName); ok && t.Schema.O != "" {
		v.out = append(v.out, t.Schema.O+"."+t.Name.O)
	}
	return n, false
}

func (v *tables) Leave(n ast.Node) (ast.Node, bool) { return n, true }
