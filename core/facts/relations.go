package facts

import (
	"cmp"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// pending is a relation whose name is not final yet.
type pending struct {
	rel   *irv1.Relation
	owner *tableRef
	// base is the name from rules 1 to 4 of §4.5, or from an annotation.
	base []string
	// fixed marks a name given by an annotation, which rule 5 leaves alone.
	fixed bool
	// suffix is what rule 5 appends after "to": the FK columns' words
	// without a trailing id.
	suffix []string
	// partner is the forward relation of an inverse.
	partner *pending
	// source describes where the relation comes from, and fix how to rename
	// it, for diagnostics.
	source, fix string
}

// link describes one direction-pair of relations: owner's cols reference
// target's refs.
type link struct {
	owner, target *tableRef
	cols, refs    []string
	via           *irv1.QualifiedName
	origin        irv1.RelationOrigin
	// uniqueNullable lets nullable columns of a uniqueness fact prove the
	// link's columns unique: true except under MATCH PARTIAL.
	uniqueNullable bool
	// Names from annotations, nil when absent.
	name, inverseName []string
	// Annotations carried by each direction.
	anns, inverseAnns []*irv1.Annotation
	policy            *irv1.RelationPolicy
	source            string
	fix, inverseFix   string
}

// relations derives every relation of §12, names them by §4.5 and stores
// them on their owners, sorted by name.
func (d *deriver) relations() {
	d.rels = map[string][]*pending{}
	for _, r := range d.order {
		slices.SortFunc(r.t.ForeignKeys, func(a, b *irv1.ForeignKey) int {
			return strings.Compare(a.GetName().GetStored(), b.GetName().GetStored())
		})
		for _, fk := range r.t.GetForeignKeys() {
			d.foreignKey(r, fk)
		}
		for _, c := range r.t.GetColumns() {
			if a := find(c.GetAnnotations(), keyRelation); a != nil {
				d.annotated(r, c, a)
			}
		}
		d.junction(r)
	}
	for _, r := range d.order {
		d.name(r)
	}
	for _, r := range d.order {
		for _, p := range d.rels[qnKey(r.qn)] {
			if p.partner != nil {
				p.rel.InverseOf = p.partner.rel.GetName().GetStored()
			} else if p.rel.GetCrossCatalog() && !d.opts.CrossCatalogReferences {
				// §5.2, §5.3: reported once per declaration, on the forward
				// relation (and on each direction of a junction).
				q := member(r.qn, p.rel.GetName().GetStored())
				d.errorf(CodeRelationCrossCatalog, q,
					"relation `%s` crosses catalogs, and dialect `%s` cannot reference across catalogs; remove %s or keep both tables in one catalog",
					dotted(q), dialectName(d.opts.Dialect), p.source)
			}
		}
	}
}

// foreignKey derives the forward relation of a foreign key and its inverse
// (§12.1, §12.2).
func (d *deriver) foreignKey(r *tableRef, fk *irv1.ForeignKey) {
	via := member(r.qn, fk.GetName().GetStored())
	target := d.lookup(fk.GetReferences().GetTable())
	if target == nil {
		d.errorf(CodeRelationTargetMissing, via,
			"foreign key `%s` references `%s`, which is not in the bundle; add its catalog or namespace to the sources",
			dotted(via), dotted(fk.GetReferences().GetTable()))
		return
	}
	l := &link{
		owner: r, target: target, cols: fk.GetColumns(), refs: fk.GetReferences().GetColumns(),
		via: via, origin: irv1.RelationOrigin_RELATION_ORIGIN_FOREIGN_KEY,
		uniqueNullable: fk.GetMatch() != irv1.ForeignKeyMatch_FOREIGN_KEY_MATCH_PARTIAL,
		source:         "foreign key `" + dotted(via) + "`",
		fix:            "rename it with a relation-name annotation on foreign key `" + dotted(via) + "`",
		inverseFix:     "rename it with an inverse-name annotation on foreign key `" + dotted(via) + "`",
	}
	anns := fk.GetAnnotations()
	if a := find(anns, keyRelationName); a != nil {
		l.name = d.annotationWords(via, a, "relation-name")
		l.anns = append(l.anns, a)
	}
	for _, a := range anns {
		switch a.GetKey() {
		case keyInverseName:
			l.inverseName = d.annotationWords(via, a, "inverse-name")
			l.inverseAnns = append(l.inverseAnns, a)
		case keyCollectionPolicy, keyRemoval:
			l.inverseAnns = append(l.inverseAnns, a)
		}
	}
	l.policy = d.policy(via, anns, nullable(r.t, l.cols))
	d.link(l)
}

func (d *deriver) annotationWords(target *irv1.QualifiedName, a *irv1.Annotation, key string) []string {
	w, ok := valueWords(a.GetValue())
	if !ok {
		d.errorf(CodeAnnotationValueInvalid, target,
			"%s on `%s` needs a word list, such as %s: [user, profile]", key, dotted(target), key)
	}
	return w
}

// nullable reports whether any of cols is nullable. Under MATCH SIMPLE a
// composite key with any NULL column is unenforced, so the relation is then
// absent (§12.3).
func nullable(t *irv1.Table, cols []string) bool {
	return slices.ContainsFunc(cols, func(name string) bool { return column(t, name).GetNullable() })
}

// policy is the collection policy and removal of a to-many or inverse
// relation: merge, and unlink when the foreign key is nullable or delete
// otherwise, unless annotations say otherwise (§12.4, §14.6).
func (d *deriver) policy(via *irv1.QualifiedName, anns []*irv1.Annotation, nullable bool) *irv1.RelationPolicy {
	p := &irv1.RelationPolicy{Collection: irv1.CollectionPolicy_COLLECTION_POLICY_MERGE, Removal: irv1.Removal_REMOVAL_DELETE}
	if nullable {
		p.Removal = irv1.Removal_REMOVAL_UNLINK
	}
	if a := find(anns, keyCollectionPolicy); a != nil {
		switch a.GetValue().GetString_() {
		case "merge":
		case "replace":
			p.Collection = irv1.CollectionPolicy_COLLECTION_POLICY_REPLACE
		case "append":
			p.Collection = irv1.CollectionPolicy_COLLECTION_POLICY_APPEND
		default:
			d.errorf(CodeAnnotationValueInvalid, via,
				"collection-policy on `%s` must be merge, replace or append", dotted(via))
		}
	}
	if a := find(anns, keyRemoval); a != nil {
		switch a.GetValue().GetString_() {
		case "delete":
			p.Removal = irv1.Removal_REMOVAL_DELETE
		case "unlink":
			if !nullable {
				d.errorf(CodeRemovalUnlinkNotNullable, via,
					"removal unlink on `%s` sets the foreign key to NULL, but its columns are NOT NULL; use removal delete or make the columns nullable",
					dotted(via))
			}
			p.Removal = irv1.Removal_REMOVAL_UNLINK
		default:
			d.errorf(CodeAnnotationValueInvalid, via, "removal on `%s` must be delete or unlink", dotted(via))
		}
	}
	return p
}

// link adds the forward relation of l to its owner and the inverse to its
// target.
func (d *deriver) link(l *link) {
	isUnique := unique(l.owner.t, l.cols, l.uniqueNullable)
	cross := l.owner.qn.GetCatalog() != l.target.qn.GetCatalog()
	fwd := &pending{
		owner: l.owner, source: l.source, fix: l.fix, suffix: stripID(l.owner.t, l.cols),
		rel: &irv1.Relation{
			Owner:        cloneQN(l.owner.qn),
			Kind:         irv1.RelationKind_RELATION_KIND_MANY_TO_ONE,
			Origin:       l.origin,
			Via:          l.via,
			From:         &irv1.ColumnSet{Columns: l.cols},
			To:           &irv1.ColumnSet{Table: cloneQN(l.target.qn), Columns: l.refs},
			Optional:     nullable(l.owner.t, l.cols), // §12.3
			CrossCatalog: cross,
			Annotations:  l.anns,
		},
	}
	inv := &pending{
		owner: l.target, partner: fwd, source: l.source, fix: l.inverseFix, suffix: fwd.suffix,
		// Rules 2 and 3: the referencing table's words.
		base: l.owner.t.GetName().GetWords(),
		rel: &irv1.Relation{
			Owner:        cloneQN(l.target.qn),
			Kind:         irv1.RelationKind_RELATION_KIND_ONE_TO_MANY,
			Origin:       l.origin,
			Via:          cloneQN(l.via),
			From:         &irv1.ColumnSet{Columns: l.refs},
			To:           &irv1.ColumnSet{Table: cloneQN(l.owner.qn), Columns: l.cols},
			Policy:       l.policy,
			CrossCatalog: cross,
			Annotations:  l.inverseAnns,
		},
	}
	if isUnique {
		// §12.2: a foreign key whose columns are unique is one-to-one, and its
		// inverse is always optional (§12.3).
		fwd.rel.Kind = irv1.RelationKind_RELATION_KIND_ONE_TO_ONE
		inv.rel.Kind = irv1.RelationKind_RELATION_KIND_INVERSE_TO_ONE
		inv.rel.Optional = true
	}
	fwd.base = toOneWords(l.owner.t, l.cols, l.target.t)
	if l.name != nil {
		fwd.base, fwd.fixed = l.name, true
	}
	if l.inverseName != nil {
		inv.base, inv.fixed = l.inverseName, true
	}
	d.own(fwd, inv)
}

func cloneQN(qn *irv1.QualifiedName) *irv1.QualifiedName {
	if qn == nil {
		return nil
	}
	return proto.Clone(qn).(*irv1.QualifiedName)
}

func (d *deriver) own(ps ...*pending) {
	for _, p := range ps {
		k := qnKey(p.owner.qn)
		d.rels[k] = append(d.rels[k], p)
	}
}

// toOneWords is rule 1 of §4.5: the FK column's words without a trailing id;
// for a composite key, or a last word that is not id, the referenced table's
// words.
func toOneWords(t *irv1.Table, cols []string, target *irv1.Table) []string {
	if len(cols) == 1 {
		w := column(t, cols[0]).GetName().GetWords()
		if n := len(w); n > 1 && w[n-1] == "id" {
			return w[:n-1]
		}
	}
	return target.GetName().GetWords()
}

// stripID is the part rule 5 of §4.5 appends: each column's words without a
// trailing id, in key order. A column named just id keeps its word.
func stripID(t *irv1.Table, cols []string) []string {
	var out []string
	for _, name := range cols {
		w := column(t, name).GetName().GetWords()
		if n := len(w); n > 1 && w[n-1] == "id" {
			w = w[:n-1]
		}
		out = append(out, w...)
	}
	return out
}

// annotated derives a relation declared by a relation annotation on column c
// (§5.3, §12.1). Its value is a struct: to names the referenced column as
// catalog.namespace.table.column, or catalog.table.column for a dialect with
// an implicit namespace; name and inverse-name are optional word lists.
func (d *deriver) annotated(r *tableRef, c *irv1.Column, a *irv1.Annotation) {
	at := member(r.qn, c.GetName().GetStored())
	invalid := func(why string) {
		d.errorf(CodeRelationAnnotationInvalid, at,
			"relation annotation on `%s` %s; write it as relation: { to: catalog.namespace.table.column, name: [words], inverse-name: [words] }",
			dotted(at), why)
	}
	st := a.GetValue().GetStruct()
	if st == nil {
		invalid("is not a struct")
		return
	}
	l := &link{
		owner: r, cols: []string{c.GetName().GetStored()},
		origin: irv1.RelationOrigin_RELATION_ORIGIN_ANNOTATED, uniqueNullable: true,
		anns:       []*irv1.Annotation{a},
		source:     "the relation annotation on `" + dotted(at) + "`",
		fix:        "rename it with name in the relation annotation on `" + dotted(at) + "`",
		inverseFix: "rename it with inverse-name in the relation annotation on `" + dotted(at) + "`",
	}
	var to string
	for _, k := range sortedKeys(st.GetFields()) {
		v := st.GetFields()[k]
		var ok bool
		switch k {
		case "to":
			to = v.GetStringValue()
			ok = to != ""
		case "name":
			l.name, ok = structWords(v)
		case "inverse-name":
			l.inverseName, ok = structWords(v)
		case "through":
			// §12.2 allows a many-to-many relation through a junction with
			// extra columns when a relation annotation names it with through,
			// but the record does not define the form of that annotation.
			d.errorf(CodeRelationThroughUnsupported, at,
				"relation annotation on `%s` names through, which is not supported yet; remove through and declare the junction's relations instead",
				dotted(at))
			return
		default:
			invalid("has unknown field " + k)
			return
		}
		if !ok {
			invalid("has an invalid " + k)
			return
		}
	}
	parts := strings.Split(to, ".")
	var ref *irv1.QualifiedName
	switch len(parts) {
	case 3:
		ref = &irv1.QualifiedName{Catalog: parts[0], Object: parts[1], Member: parts[2]}
	case 4:
		ref = &irv1.QualifiedName{Catalog: parts[0], Namespace: parts[1], Object: parts[2], Member: parts[3]}
	default:
		invalid("needs to: catalog.namespace.table.column")
		return
	}
	target := d.lookup(ref)
	if target == nil {
		d.errorf(CodeRelationTargetMissing, at,
			"relation annotation on `%s` references `%s`, which is not in the bundle; add its catalog or namespace to the sources",
			dotted(at), dotted(member(ref, "")))
		return
	}
	if column(target.t, ref.GetMember()) == nil {
		invalid("references column `" + dotted(ref) + "`, which does not exist")
		return
	}
	l.target, l.refs = target, []string{ref.GetMember()}
	l.policy = d.policy(at, nil, c.GetNullable())
	d.link(l)
}

// junction derives the many-to-many relations through a pure junction
// table: its primary key is exactly the columns of two foreign keys, and it
// has no other columns (§12.2). Both directions are generated.
func (d *deriver) junction(r *tableRef) {
	t := r.t
	pk := primaryKey(t)
	fks := t.GetForeignKeys()
	if t.GetKind() != irv1.TableKind_RELATION_KIND_TABLE || pk == nil || len(fks) != 2 {
		return
	}
	union := append(slices.Clone(fks[0].GetColumns()), fks[1].GetColumns()...)
	var all []string
	for _, c := range t.GetColumns() {
		all = append(all, c.GetName().GetStored())
	}
	if !sameSet(union, pk.GetColumns()) || !sameSet(union, all) {
		return
	}
	a, b := d.lookup(fks[0].GetReferences().GetTable()), d.lookup(fks[1].GetReferences().GetTable())
	if a == nil || b == nil {
		return // reported with the foreign key
	}
	cross := a.qn.GetCatalog() != b.qn.GetCatalog() || a.qn.GetCatalog() != r.qn.GetCatalog()
	side := func(owner *tableRef, near *irv1.ForeignKey, far *tableRef, farFK *irv1.ForeignKey) *pending {
		return &pending{
			owner: owner,
			base:  far.t.GetName().GetWords(), // rule 4: the far table's words
			// Rule 5 on a junction uses the far foreign key's columns, which
			// tell two junctions to the same table apart.
			suffix: stripID(t, farFK.GetColumns()),
			source: "junction table `" + dotted(r.qn) + "`",
			fix:    "an inferred many-to-many relation takes the far table's words, so rename the colliding column or relation with a name, relation-name or inverse-name annotation",
			rel: &irv1.Relation{
				Owner:        cloneQN(owner.qn),
				Kind:         irv1.RelationKind_RELATION_KIND_MANY_TO_MANY,
				Origin:       irv1.RelationOrigin_RELATION_ORIGIN_INFERRED_JUNCTION,
				From:         &irv1.ColumnSet{Columns: near.GetReferences().GetColumns()},
				To:           &irv1.ColumnSet{Table: cloneQN(far.qn), Columns: farFK.GetReferences().GetColumns()},
				Through:      cloneQN(r.qn),
				CrossCatalog: cross,
				// §14.6: a many-to-many relation takes Link, Unlink or Create
				// under its collection policy; it has no Delete, so no removal.
				Policy: &irv1.RelationPolicy{Collection: irv1.CollectionPolicy_COLLECTION_POLICY_MERGE},
			},
		}
	}
	d.own(side(a, fks[0], b, fks[1]), side(b, fks[1], a, fks[0]))
}

// sameSet reports whether a and b hold the same distinct elements, with no
// repeats in a.
func sameSet(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return len(slices.Compact(slices.Clone(x))) == len(x) && slices.Equal(x, slices.Compact(y))
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// compareRelations orders an entity's relations by name, then by what they
// come from, so the order is total even when names collide.
func compareRelations(a, b *irv1.Relation) int {
	return cmp.Or(
		strings.Compare(a.GetName().GetStored(), b.GetName().GetStored()),
		compareQN(a.GetVia(), b.GetVia()),
		compareQN(a.GetThrough(), b.GetThrough()),
		compareQN(a.GetTo().GetTable(), b.GetTo().GetTable()),
		slices.Compare(a.GetFrom().GetColumns(), b.GetFrom().GetColumns()),
		cmp.Compare(a.GetKind(), b.GetKind()),
	)
}
