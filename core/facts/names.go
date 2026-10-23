package facts

import (
	"slices"
	"strings"

	"github.com/thec1oud/dql/core/diagnostics"
)

// name gives the relations of one entity their final names, stores them on
// the table, and checks the entity's columns and relations for collisions
// (§4.3, §4.5).
func (d *deriver) name(r *tableRef) {
	ps := d.rels[qnKey(r.qn)]

	// Rule 5: relations with equal names get "to" plus their FK columns'
	// words without id. A name given by an annotation is kept as written.
	count := map[string]int{}
	for _, p := range ps {
		count[wordsKey(p.base)]++
	}
	for _, p := range ps {
		words := p.base
		if !p.fixed && count[wordsKey(p.base)] > 1 {
			words = append(append(slices.Clone(p.base), "to"), p.suffix...)
		}
		p.rel.Name = identifier(words)
	}
	slices.SortFunc(ps, func(a, b *pending) int { return compareRelations(a.rel, b.rel) })
	r.t.Relations = nil
	for _, p := range ps {
		r.t.Relations = append(r.t.Relations, p.rel)
	}

	columns := map[string][]string{}
	var colKeys []string
	for _, c := range r.t.GetColumns() {
		k := wordsKey(columnWords(c))
		if columns[k] == nil {
			colKeys = append(colKeys, k)
		}
		columns[k] = append(columns[k], c.GetName().GetStored())
	}
	for _, k := range colKeys {
		if names := columns[k]; len(names) > 1 {
			d.errorf(CodeColumnNameCollision, member(r.qn, names[0]),
				"columns %s of `%s` all have the words [%s]; rename all but one with a name annotation",
				quoteList(names), diagnostics.Name(r.qn), strings.Join(strings.Split(k, "\x00"), " "))
		}
	}

	for i := 0; i < len(ps); {
		j := i + 1
		for j < len(ps) && slices.Equal(ps[j].rel.GetName().GetWords(), ps[i].rel.GetName().GetWords()) {
			j++
		}
		if j-i > 1 {
			var from []string
			for _, p := range ps[i:j] {
				from = append(from, p.source)
			}
			q := member(r.qn, ps[i].rel.GetName().GetStored())
			d.errorf(CodeRelationNameCollision, q,
				"%d relations of `%s` are named `%s`, from %s; %s",
				j-i, diagnostics.Name(r.qn), ps[i].rel.GetName().GetStored(), strings.Join(from, ", "), ps[i].fix)
		}
		i = j
	}

	// Rule 6: a relation named like a column of the same entity fails.
	for _, p := range ps {
		if names := columns[wordsKey(p.rel.GetName().GetWords())]; names != nil {
			q := member(r.qn, p.rel.GetName().GetStored())
			d.errorf(CodeRelationNameEqualsColumn, q,
				"relation `%s` from %s has the same name as column `%s`; %s",
				diagnostics.Name(q), p.source, names[0], p.fix)
		}
	}
}

func quoteList(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = "`" + n + "`"
	}
	return strings.Join(q, ", ")
}
