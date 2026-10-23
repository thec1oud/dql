package mapping

import (
	"fmt"
	"slices"
	"strings"

	pg "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	pgextv1 "github.com/thec1oud/dql/proto/dql/ext/postgres/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/sources/dpgsnapshot/internal/format"
)

// viewFields are the VIEW record fields the IR or a Postgres extension
// carries.
var viewFields = []string{"schema", "name", "query", "owner", "tablespace", "storage_params", "indexes"}

// view maps a view or materialized view (§6.4), once: a view another view
// reads is mapped first, since its columns are read from it. No record says
// a view is materialized; only a materialized view has indexes, WITH NO DATA
// or a tablespace (fidelity.MaterializedViews).
func (m *mapper) view(v *format.View) *irv1.Table {
	q := objectKey{v.Schema, v.Name}
	if t, ok := m.mapped[q]; ok {
		return t
	}
	if m.viewing[q] {
		m.fail(fmt.Errorf("mapping: view %s reads itself", q))
		return &irv1.Table{}
	}
	m.viewing[q] = true
	defer delete(m.viewing, q)

	t := &irv1.Table{Name: ident(v.Name), Kind: irv1.TableKind_RELATION_KIND_VIEW}
	what := "VIEW"
	if len(v.Indexes) > 0 || v.WithNoData || v.Tablespace != nil {
		t.Kind, what = irv1.TableKind_RELATION_KIND_MATERIALIZED_VIEW, "MATERIALIZED VIEW"
	}
	tree, err := pg.Parse(v.Query)
	if err != nil || len(tree.Stmts) != 1 || tree.Stmts[0].Stmt.GetSelectStmt() == nil {
		m.fail(fmt.Errorf("mapping: the query of view %s is not one SELECT: %q", q, v.Query))
		return &irv1.Table{}
	}
	t.DependsOn = m.dependsOn(v.Schema, tree)
	t.Columns = m.viewColumns(v, tree.Stmts[0].Stmt.GetSelectStmt())
	if t.Columns == nil {
		m.diag(irv1.Severity_SEVERITY_WARNING, CodeViewColumnsUnknown, m.qn(v.Schema, v.Name, ""), fmt.Sprintf(
			"the columns of view %s are not known: a dpg snapshot records a view's query, and only a query whose "+
				"output columns are plain columns of the snapshot's tables and views is read; read the catalog with pg-live",
			q))
	}
	text, field := dbComment(v.Comment, v.Deprecated)
	t.Annotations = m.annotations(m.qn(v.Schema, v.Name, ""), "COMMENT ON "+what+" "+q.String(), text)
	e := &exts{m: m}
	e.comment(text)
	e.owner(v.Owner)
	m.indexes(t, v.Schema, v.Name, v.Indexes, e)
	e.add(&pgextv1.ViewDefinition{Definition: v.Query})
	e.tablespace(deref(v.Tablespace))
	if v.StorageParams != "" {
		e.add(&pgextv1.StorageParameters{Parameters: strings.Split(v.StorageParams, ", ")})
	}
	e.record("view", "", v, append(slices.Clone(viewFields), field)...)
	t.Extensions = e.list()
	ns := m.b.get(v.Schema).ns
	ns.Tables = append(ns.Tables, t)
	m.mapped[q] = t
	return t
}

// dependsOn lists the tables and views of the snapshot a view's query reads
// anywhere in it, sorted (§6.1). Names of the query's own WITH queries are
// not relations.
func (m *mapper) dependsOn(schema string, tree *pg.ParseResult) []*irv1.QualifiedName {
	ctes := map[string]bool{}
	var vars []*pg.RangeVar
	walk(tree.ProtoReflect(), func(msg protoreflect.Message) {
		switch x := msg.Interface().(type) {
		case *pg.CommonTableExpr:
			ctes[x.Ctename] = true
		case *pg.RangeVar:
			vars = append(vars, x)
		}
	})
	var keys []objectKey
	for _, rv := range vars {
		if rv.Schemaname == "" && ctes[rv.Relname] {
			continue
		}
		if q, ok := m.relation(schema, rv); ok && !slices.Contains(keys, q) {
			keys = append(keys, q)
		}
	}
	slices.SortFunc(keys, func(a, b objectKey) int {
		return strings.Compare(a.schema+"\x00"+a.name, b.schema+"\x00"+b.name)
	})
	var out []*irv1.QualifiedName
	for _, k := range keys {
		out = append(out, m.qn(k.schema, k.name, ""))
	}
	return out
}

// relation resolves a relation a query names to a table or view of the
// snapshot (fidelity.SearchPath).
func (m *mapper) relation(schema string, rv *pg.RangeVar) (objectKey, bool) {
	for _, sc := range candidates(schema, rv.Schemaname) {
		q := objectKey{sc, rv.Relname}
		if m.tables[q] != nil || m.views[q] != nil {
			return q, true
		}
	}
	return objectKey{}, false
}

// source is one relation of a query's FROM list, under the name the query
// refers to it by.
type source struct {
	alias   string
	columns []*irv1.Column
}

// viewColumns reads a view's columns from its query (fidelity.ViewColumnTypes):
// a single SELECT without WITH or set operations, from tables and views of
// the snapshot and joins of them without USING or NATURAL, whose every
// output column is a plain column reference or a *. Each column takes the
// referenced column's type and collation, and is nullable, as Postgres
// reports every view column (§6.4). A declared column list renames them.
// The result is nil when the query is not of that form.
func (m *mapper) viewColumns(v *format.View, sel *pg.SelectStmt) []*irv1.Column {
	if sel.Op != pg.SetOperation_SETOP_NONE || sel.WithClause != nil {
		return nil
	}
	var sources []source
	for _, item := range sel.FromClause {
		s, ok := m.sources(v.Schema, item)
		if !ok {
			return nil
		}
		sources = append(sources, s...)
	}
	var cols []*irv1.Column
	for _, target := range sel.TargetList {
		rt := target.GetResTarget()
		out, ok := resolveTarget(sources, rt.GetVal().GetColumnRef())
		if !ok {
			return nil
		}
		for _, c := range out {
			c = proto.CloneOf(c)
			if rt.Name != "" {
				c.Name = ident(rt.Name)
			}
			cols = append(cols, c)
		}
	}
	for i, c := range cols {
		if i < len(v.Columns) {
			c.Name = ident(v.Columns[i])
		}
		c.Ordinal = uint32(i + 1)
		c.Nullable, c.NullabilitySource = true, irv1.NullabilitySource_NULLABILITY_SOURCE_UNPROVEN
		c.Default, c.Generation, c.Annotations, c.Extensions = nil, nil, nil, nil
	}
	return cols
}

// sources returns the relations one FROM item reads, or false when the item
// is not a relation of the catalog or a plain join of them.
func (m *mapper) sources(schema string, item *pg.Node) ([]source, bool) {
	if j := item.GetJoinExpr(); j != nil {
		if j.IsNatural || len(j.UsingClause) > 0 || j.Alias != nil {
			return nil, false
		}
		left, ok := m.sources(schema, j.Larg)
		if !ok {
			return nil, false
		}
		right, ok := m.sources(schema, j.Rarg)
		return append(left, right...), ok
	}
	rv := item.GetRangeVar()
	if rv == nil {
		return nil, false
	}
	q, ok := m.relation(schema, rv)
	if !ok || !m.wanted(q.schema) {
		return nil, false
	}
	t := m.mapped[q]
	if v := m.views[q]; v != nil {
		t = m.view(v)
	}
	alias := rv.Relname
	if rv.Alias != nil {
		alias = rv.Alias.Aliasname
	}
	return []source{{alias: alias, columns: t.GetColumns()}}, t.GetColumns() != nil
}

// resolveTarget returns the columns an output column reference names: one
// column, or every column of one or all sources for a *.
func resolveTarget(sources []source, ref *pg.ColumnRef) ([]*irv1.Column, bool) {
	if ref == nil {
		return nil, false
	}
	fields := ref.Fields
	last := fields[len(fields)-1]
	star := last.GetAStar() != nil
	rel := ""
	if len(fields) > 1 {
		// schema.table.column names its table by name, as one source.
		rel = fields[len(fields)-2].GetString_().GetSval()
	}
	var out []*irv1.Column
	for _, s := range sources {
		if rel != "" && s.alias != rel {
			continue
		}
		for _, c := range s.columns {
			if star || c.Name.Stored == last.GetString_().GetSval() {
				out = append(out, c)
			}
		}
	}
	return out, len(out) > 0 && (star || len(out) == 1)
}

// walk calls fn on m and every message below it.
func walk(m protoreflect.Message, fn func(protoreflect.Message)) {
	fn(m)
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case fd.Message() == nil:
		case fd.IsList():
			for i := range v.List().Len() {
				walk(v.List().Get(i).Message(), fn)
			}
		default:
			walk(v.Message(), fn)
		}
		return true
	})
}
