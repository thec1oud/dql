package queries

import (
	"slices"
	"strings"

	"github.com/thec1oud/dql/core/diagnostics"
	"github.com/thec1oud/dql/core/shapes"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

const (
	keySoftDelete = "core.soft-delete"
	keyVersion    = "core.version"
)

// TableID returns how fragment ids name the table q of cats: its stored
// name, behind its namespace when that is not its catalog's default, and
// behind its catalog when there are several (plans/fragment-table-proposal.md,
// §18.4's col.users.email).
func TableID(cats []*irv1.Catalog, q *irv1.QualifiedName) string {
	var id []string
	if len(cats) > 1 {
		id = append(id, q.GetCatalog())
	}
	i := slices.IndexFunc(cats, func(c *irv1.Catalog) bool { return c.GetName().GetStored() == q.GetCatalog() })
	if i < 0 || q.GetNamespace() != cats[i].GetDefaultNamespace() {
		id = append(id, q.GetNamespace())
	}
	return strings.Join(append(id, q.GetObject()), ".")
}

// Plan returns the builder's queries: one per operation generated code runs
// for every entity, each a plan of the fragment table's fragments
// (§16.2, §18 to §21). The table's dialect plugin has rendered b's
// fragments; an operation whose fragments the dialect lacks has no query.
//
//   - <entity>.get.by_<key> and <entity>.find.by_<key> read the Full by
//     each identifying key: by_primary, by_logical_key, or the key's columns
//     joined by _and_.
//   - <entity>.count and <entity>.exists count the rows a handle sees.
//   - <entity>.insert, <entity>.upsert.by_<key> and <entity>.patch take the
//     Draft, Partial and Key presets, and <entity>.insert.<shape> and
//     <entity>.patch.<shape> each custom write shape; all return the Full.
//   - <entity>.delete removes by Key, setting the soft-delete column on a
//     soft-delete entity, which also has <entity>.hard_delete.
//
// Every query filters on the entity's scope and leaves out soft-deleted
// rows. A versioned write compares the Key's expected version and bumps it
// (§20.4). Queries are sorted by name.
func Plan(b *irv1.Bundle) []*irv1.Query {
	p := &planner{b: b, fragments: map[string]bool{}, tables: map[string]*entity{}}
	for _, f := range b.GetQueries().GetFragments().GetFragments() {
		p.fragments[f.GetId()] = true
	}
	for _, cat := range b.GetCatalogs() {
		for _, ns := range cat.GetNamespaces() {
			for _, t := range ns.GetTables() {
				q := &irv1.QualifiedName{Catalog: cat.GetName().GetStored(), Namespace: ns.GetName().GetStored(), Object: t.GetName().GetStored()}
				words := t.GetHostName().GetWords()
				if len(words) == 0 {
					words = t.GetName().GetWords()
				}
				e := &entity{t: t, name: q, id: TableID(b.GetCatalogs(), q), words: words, spelling: strings.Join(words, "_")}
				for _, c := range t.GetColumns() {
					switch {
					case has(c, keyBound):
						e.scope = append(e.scope, c.GetName().GetStored())
					case has(c, keySoftDelete):
						e.softDelete = c
					case has(c, keyVersion):
						e.version = c.GetName().GetStored()
					}
				}
				p.tables[diagnostics.Name(q)] = e
				p.order = append(p.order, e)
			}
		}
	}
	for _, s := range b.GetShapes().GetShapes() {
		e := p.tables[diagnostics.Name(s.GetEntity())]
		e.shapes = append(e.shapes, s)
	}
	for _, e := range p.order {
		p.entity(e)
	}
	for _, u := range b.GetShapes().GetUpserts() {
		p.upsert(p.tables[diagnostics.Name(u.GetEntity())], u)
	}
	slices.SortFunc(p.out, func(a, b *irv1.Query) int { return strings.Compare(a.GetName().GetStored(), b.GetName().GetStored()) })
	return p.out
}

type planner struct {
	b         *irv1.Bundle
	fragments map[string]bool
	tables    map[string]*entity
	order     []*entity
	out       []*irv1.Query
}

// entity is a table or view with what its queries need.
type entity struct {
	t        *irv1.Table
	name     *irv1.QualifiedName
	id       string
	words    []string
	spelling string
	// Its bound columns, in column order.
	scope      []string
	softDelete *irv1.Column
	version    string
	shapes     []*irv1.Shape
}

func (e *entity) column(name string) *irv1.Column {
	i := slices.IndexFunc(e.t.GetColumns(), func(c *irv1.Column) bool { return c.GetName().GetStored() == name })
	return e.t.GetColumns()[i]
}

// preset returns the entity's preset shape in Flat form, or its Key.
func (e *entity) preset(preset irv1.Preset) *irv1.Shape {
	for _, s := range e.shapes {
		id := s.GetId()
		if id.GetEntity() != nil && id.GetPreset() == preset && id.GetForRelation() == "" &&
			(id.GetForm() == irv1.Form_FORM_FLAT || preset == irv1.Preset_PRESET_KEY) {
			return s
		}
	}
	return nil
}

func (e *entity) shapeID(s *irv1.Shape) string {
	return shapes.Spelling(s.GetId(), e.words)
}

// The plan's building blocks.

// use is fragment id with its holes filled; a nil fill leaves its hole
// empty.
func use(id string, holes ...*irv1.HoleFill) *irv1.FragmentUse {
	holes = slices.DeleteFunc(holes, func(h *irv1.HoleFill) bool { return h == nil })
	slices.SortFunc(holes, func(a, b *irv1.HoleFill) int { return strings.Compare(a.GetHole(), b.GetHole()) })
	return &irv1.FragmentUse{Fragment: id, Holes: holes}
}

func fill(hole string, u *irv1.FragmentUse) *irv1.HoleFill {
	return &irv1.HoleFill{Hole: hole, Value: &irv1.HoleFill_Fragment{Fragment: u}}
}

func param(hole, name string) *irv1.HoleFill {
	return &irv1.HoleFill{Hole: hole, Value: &irv1.HoleFill_Param{Param: name}}
}

func list(hole string, items []*irv1.ListItem) *irv1.HoleFill {
	return &irv1.HoleFill{Hole: hole, Value: &irv1.HoleFill_List{List: &irv1.FragmentList{Items: items, Separator: ", "}}}
}

func item(u *irv1.FragmentUse) *irv1.ListItem {
	return &irv1.ListItem{Value: &irv1.ListItem_Fragment{Fragment: u}}
}

func paramItem(name string) *irv1.ListItem {
	return &irv1.ListItem{Value: &irv1.ListItem_Param{Param: name}}
}

func (e *entity) col(c string) *irv1.FragmentUse { return use("col." + e.id + "." + c) }

func eq(e *entity, c, p string) *irv1.FragmentUse {
	return use("op.eq", fill("0", e.col(c)), param("1", p))
}

// and joins predicates, left to right.
func and(preds []*irv1.FragmentUse) *irv1.FragmentUse {
	out := preds[0]
	for _, p := range preds[1:] {
		out = use("bool.and", fill("0", out), fill("1", p))
	}
	return out
}

// where is the WHERE clause of preds, or nil without any.
func where(preds []*irv1.FragmentUse) *irv1.HoleFill {
	if len(preds) == 0 {
		return nil
	}
	return fill("where", use("clause.where", fill("0", and(preds))))
}

// visible filters on the scope and leaves out soft-deleted rows (§21).
func (e *entity) visible() []*irv1.FragmentUse {
	var preds []*irv1.FragmentUse
	for _, c := range e.scope {
		preds = append(preds, eq(e, c, "scope."+c))
	}
	if c := e.softDelete; c != nil {
		col := e.col(c.GetName().GetStored())
		if c.GetType().GetBool() != nil {
			preds = append(preds, use("op.is_distinct_from", fill("0", col), fill("1", use("value.true"))))
		} else {
			preds = append(preds, use("op.is_null", fill("0", col)))
		}
	}
	return preds
}

// keyFilter filters on every column of a key that the scope does not
// supply, as key.<column> parameters, and returns them.
func (e *entity) keyFilter(cols []string) ([]*irv1.FragmentUse, []*irv1.Param) {
	var preds []*irv1.FragmentUse
	var params []*irv1.Param
	for _, c := range cols {
		if slices.Contains(e.scope, c) {
			continue
		}
		preds = append(preds, eq(e, c, "key."+c))
		params = append(params, &irv1.Param{Name: "key." + c, Type: e.column(c).GetType()})
	}
	return preds, params
}

// add adds a query on e unless the dialect lacks one of its fragments.
func (p *planner) add(e *entity, name string, card irv1.Cardinality, writes bool, params []*irv1.Param, result *irv1.Shape, parts ...*irv1.FragmentUse) *irv1.Query {
	for _, u := range parts {
		if !p.renders(u) {
			return nil
		}
	}
	q := &irv1.Query{
		Name:        &irv1.Identifier{Stored: name},
		Source:      irv1.QuerySource_QUERY_SOURCE_BUILDER,
		Cardinality: card,
		Params:      params,
		Writes:      writes,
		Catalog:     e.name.GetCatalog(),
		Sql:         &irv1.Query_Plan{Plan: &irv1.FragmentPlan{Parts: parts}},
		Tables:      []*irv1.QualifiedName{e.name},
	}
	if result != nil {
		q.Result = &irv1.ResultShape{Kind: &irv1.ResultShape_Shape{Shape: result.GetId()}}
	}
	if len(e.scope) > 0 {
		q.Scope = &irv1.ScopeUse{Columns: slices.Sorted(slices.Values(e.scope))}
	}
	p.out = append(p.out, q)
	return q
}

// renders reports whether the fragment table has every fragment of u.
func (p *planner) renders(u *irv1.FragmentUse) bool {
	if !p.fragments[u.GetFragment()] {
		return false
	}
	for _, h := range u.GetHoles() {
		if f := h.GetFragment(); f != nil && !p.renders(f) {
			return false
		}
		for _, it := range h.GetList().GetItems() {
			if f := it.GetFragment(); f != nil && !p.renders(f) {
				return false
			}
		}
	}
	return true
}

// entity adds the queries of one entity.
func (p *planner) entity(e *entity) {
	full := e.preset(irv1.Preset_PRESET_FULL)
	if full == nil {
		return
	}
	sel := use("select." + e.shapeID(full))
	for _, k := range e.keys() {
		preds, params := e.keyFilter(k.cols)
		parts := []*irv1.FragmentUse{sel}
		if w := where(append(e.visible(), preds...)); w != nil {
			parts = append(parts, w.GetFragment())
		}
		for _, verb := range []struct {
			name string
			card irv1.Cardinality
		}{{"get", irv1.Cardinality_CARDINALITY_ONE}, {"find", irv1.Cardinality_CARDINALITY_OPTIONAL}} {
			if q := p.add(e, e.spelling+"."+verb.name+".by_"+k.name, verb.card, false, params, full, parts...); q != nil {
				q.AtMostOne = true
			}
		}
	}
	vis := where(e.visible())
	p.add(e, e.spelling+".count", irv1.Cardinality_CARDINALITY_COUNT, false, nil, nil, use("count."+e.id, vis))
	p.add(e, e.spelling+".exists", irv1.Cardinality_CARDINALITY_EXISTS, false, nil, nil, use("exists."+e.id, vis))
	if e.t.GetKind() != irv1.TableKind_RELATION_KIND_TABLE {
		return
	}
	returning := fill("returning", use("returning."+e.shapeID(full)))
	for _, s := range e.shapes {
		if s.GetId().GetForRelation() != "" || len(s.GetLineage()) == 0 {
			continue
		}
		custom := s.GetId().GetCustom()
		suffix := ""
		if custom != "" {
			suffix = "." + custom
		} else if s.GetId().GetForm() != irv1.Form_FORM_FLAT {
			continue
		}
		switch s.GetLineage()[0].GetPreset() {
		case irv1.Preset_PRESET_DRAFT:
			cols, row, params := e.values(s)
			p.add(e, e.spelling+".insert"+suffix, irv1.Cardinality_CARDINALITY_ONE, true, params, full,
				use("insert."+e.id, cols, row, returning))
		case irv1.Preset_PRESET_PARTIAL:
			p.patch(e, s, suffix, full, returning)
		}
	}
	p.delete(e)
}

// key is an identifying key and how query names spell it.
type key struct {
	name string
	cols []string
}

// keys returns the entity's identifying keys: its primary key or logical
// key first, then its other identifying unique keys, by name (§11.1).
func (e *entity) keys() []key {
	var out []key
	if lk := e.t.GetLogicalKey(); lk != nil {
		out = append(out, key{"logical_key", lk.GetColumns()})
	}
	for _, u := range e.t.GetUniqueness() {
		switch {
		case !u.GetIdentifies():
		case u.GetKind() == irv1.UniquenessKind_UNIQUENESS_PRIMARY_KEY:
			out = slices.Insert(out, 0, key{"primary", u.GetColumns()})
		default:
			out = append(out, key{keyName(u), u.GetColumns()})
		}
	}
	return out
}

// keyName spells a uniqueness fact in a query name: primary, its columns
// joined by _and_, or, for a key with expressions or a predicate, its own
// name.
func keyName(u *irv1.Uniqueness) string {
	switch {
	case u.GetKind() == irv1.UniquenessKind_UNIQUENESS_PRIMARY_KEY:
		return "primary"
	case len(u.GetExpressions()) > 0 || u.GetPredicate() != nil:
		return u.GetName()
	}
	return strings.Join(u.GetColumns(), "_and_")
}

// values returns the column list and the VALUES row of an insert of s, with
// its parameters: the shape's fields, then its bound columns from the scope
// and its bind columns as parameters (§14.2, §14.8).
func (e *entity) values(s *irv1.Shape) (cols, row *irv1.HoleFill, params []*irv1.Param) {
	var names, vals []*irv1.ListItem
	addCol := func(c, p string) {
		names = append(names, item(use("name."+e.id+"."+c)))
		vals = append(vals, paramItem(p))
	}
	for _, f := range s.GetFields() {
		addCol(f.GetColumn(), f.GetColumn())
		params = append(params, &irv1.Param{Name: f.GetColumn(), Type: f.GetType(), Nullable: f.GetNullable(), Optional: f.GetPresence() == irv1.Presence_PRESENCE_OPTIONAL})
	}
	for _, c := range s.GetScope() {
		addCol(c, "scope."+c)
	}
	for _, c := range s.GetBind() {
		addCol(c, c)
		col := e.column(c)
		params = append(params, &irv1.Param{Name: c, Type: col.GetType(), Nullable: col.GetNullable()})
	}
	return list("cols", names), fill("rows", use("values.row", list("0", vals))), params
}

// target returns the filter on the Key, with its expected version, and its
// parameters; ok is false when the entity has no Key.
func (e *entity) target() (preds []*irv1.FragmentUse, params []*irv1.Param, ok bool) {
	k := e.preset(irv1.Preset_PRESET_KEY)
	if k == nil {
		return nil, nil, false
	}
	var cols []string
	for _, f := range k.GetFields() {
		cols = append(cols, f.GetColumn())
	}
	preds, params = e.keyFilter(cols)
	return preds, params, true
}

// bump is the set that increments a versioned entity's version.
func (e *entity) bump() []*irv1.ListItem {
	if e.version == "" {
		return nil
	}
	return []*irv1.ListItem{item(use("set."+e.id+"."+e.version,
		fill("0", use("op.add", fill("0", e.col(e.version)), fill("1", use("value.one"))))))}
}

// patch adds the update by Key of the Partial s; each field is optional,
// so a call sets only the fields it holds (§20.1).
func (p *planner) patch(e *entity, s *irv1.Shape, suffix string, full *irv1.Shape, returning *irv1.HoleFill) {
	preds, keyParams, ok := e.target()
	if !ok {
		return
	}
	// SET comes before WHERE, so its parameters come first.
	var sets []*irv1.ListItem
	var params []*irv1.Param
	for _, f := range s.GetFields() {
		sets = append(sets, item(use("set."+e.id+"."+f.GetColumn(), param("0", f.GetColumn()))))
		params = append(params, &irv1.Param{Name: f.GetColumn(), Type: f.GetType(), Nullable: f.GetNullable(), Optional: f.GetPresence() == irv1.Presence_PRESENCE_OPTIONAL})
	}
	sets = append(sets, e.bump()...)
	if len(sets) == 0 {
		return
	}
	if q := p.add(e, e.spelling+".patch"+suffix, irv1.Cardinality_CARDINALITY_ONE, true, append(params, keyParams...), full,
		use("update."+e.id, list("sets", sets), where(append(e.visible(), preds...)), returning)); q != nil {
		q.AtMostOne = true
	}
}

// delete adds the delete by Key: on a soft-delete entity an update setting
// the column, plus a hard delete (§21.3).
func (p *planner) delete(e *entity) {
	preds, params, ok := e.target()
	if !ok {
		return
	}
	if c := e.softDelete; c != nil {
		value := "value.now"
		if c.GetType().GetBool() != nil {
			value = "value.true"
		}
		sets := append([]*irv1.ListItem{item(use("set."+e.id+"."+c.GetName().GetStored(), fill("0", use(value))))}, e.bump()...)
		p.add(e, e.spelling+".delete", irv1.Cardinality_CARDINALITY_EXEC_ROWS, true, params, nil,
			use("update."+e.id, list("sets", sets), where(append(e.visible(), preds...))))
		// A hard delete reaches deleted rows too, but stays in the scope.
		p.add(e, e.spelling+".hard_delete", irv1.Cardinality_CARDINALITY_EXEC_ROWS, true, params, nil,
			use("delete."+e.id, where(append(e.visible()[:len(e.scope)], preds...))))
		return
	}
	p.add(e, e.spelling+".delete", irv1.Cardinality_CARDINALITY_EXEC_ROWS, true, params, nil, use("delete."+e.id, where(append(e.visible(), preds...))))
}

// upsert adds the upsert of one conflict target: on conflict it sets every
// field the default update does not leave alone from the proposed row
// (§14.7).
func (p *planner) upsert(e *entity, u *irv1.Upsert) {
	i := slices.IndexFunc(e.shapes, func(s *irv1.Shape) bool { return shapeEqual(s.GetId(), u.GetInsertShape()) })
	full := e.preset(irv1.Preset_PRESET_FULL)
	if i < 0 || full == nil {
		return
	}
	s := e.shapes[i]
	excluded := map[string]bool{}
	for _, x := range u.GetDefaultUpdateExcludes() {
		excluded[x.GetColumn()] = true
	}
	var sets []*irv1.ListItem
	for _, f := range s.GetFields() {
		if !excluded[f.GetColumn()] {
			sets = append(sets, item(use("set."+e.id+"."+f.GetColumn(), fill("0", use("excluded."+e.id+"."+f.GetColumn())))))
		}
	}
	sets = append(sets, e.bump()...)
	k := slices.IndexFunc(e.t.GetUniqueness(), func(q *irv1.Uniqueness) bool { return q.GetName() == u.GetConflictTarget() })
	if key := e.t.GetUniqueness()[k].GetColumns(); len(sets) == 0 && len(key) > 0 {
		// Every field is the key or left alone: setting a key column to
		// itself still returns the row.
		sets = append(sets, item(use("set."+e.id+"."+key[0], fill("0", use("excluded."+e.id+"."+key[0])))))
	}
	if len(sets) == 0 {
		return
	}
	cols, row, params := e.values(s)
	p.add(e, e.spelling+".upsert.by_"+keyName(e.t.GetUniqueness()[k]), irv1.Cardinality_CARDINALITY_ONE, true, params, full,
		use("upsert."+e.id+"."+u.GetConflictTarget(), cols, row, list("sets", sets), fill("returning", use("returning."+e.shapeID(full)))))
}

func shapeEqual(a, b *irv1.ShapeId) bool {
	return diagnostics.CompareNames(a.GetEntity(), b.GetEntity()) == 0 && a.GetCustom() == b.GetCustom() &&
		a.GetPreset() == b.GetPreset() && a.GetForm() == b.GetForm() && a.GetForRelation() == b.GetForRelation()
}
